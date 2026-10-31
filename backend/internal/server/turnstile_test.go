package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/config"
	"github.com/gavinadlan/racego/backend/internal/notify"
	"github.com/gavinadlan/racego/backend/internal/payment"
	"github.com/gavinadlan/racego/backend/internal/ticket"
	"github.com/gavinadlan/racego/backend/internal/turnstile"
)

const tsSecret = "rahasia-turnstile-router-uji"

// Turnstile hidup (server siteverify palsu lokal): OTP & masuk antrean butuh token, pembatas laju
// berjalan sebelum verifikasi, gagal = 422, tidak terjangkau = 503 (fail closed), secret/token tidak di log.
func TestTurnstileOTPAndQueue(t *testing.T) {
	e := newPortalEnv(t, true)
	logs := captureLogs(t)
	var calls atomic.Int32
	fakeCF := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if r.PostForm.Get("secret") != tsSecret {
			w.Write([]byte(`{"success":false,"error-codes":["invalid-input-secret"]}`)) //nolint:errcheck
			return
		}
		switch r.PostForm.Get("response") {
		case "token-lolos-uji":
			w.Write([]byte(`{"success":true}`)) //nolint:errcheck
		case "token-lambat-uji":
			time.Sleep(300 * time.Millisecond)
		default:
			w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`)) //nolint:errcheck
		}
	}))
	t.Cleanup(fakeCF.Close)
	v := turnstile.New(tsSecret, fakeCF.URL)
	v.Client.Timeout = 100 * time.Millisecond

	smtpSrv := newFakeSMTP(t)
	rdb := redis.NewClient(&redis.Options{Addr: e.mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	signer, _ := ticket.NewSigner("", true)
	cfg := config.Config{Env: "development", FrontendURL: "http://localhost:3000", ReservationTTL: 15 * time.Minute, AutoVerifyOrgs: true, UploadDir: t.TempDir(),
		Turnstile: config.TurnstileConfig{SiteKey: "1x00000000000000000000AA", SecretKey: tsSecret},
		Queue:     config.QueueConfig{LobbyMinutes: 15, AdmitPerSec: 20, MaxActive: 300, PassTTL: 10 * time.Minute, HeartbeatTTL: 45 * time.Second}}
	e.h = NewRouter(Deps{Cfg: cfg, DB: e.db, Redis: rdb, Inv: e.inv, Snap: payment.NewMockSnap(cfg.FrontendURL), Signer: signer,
		Mailer: notify.NewMailer(smtpSrv.addr, "", "", "RaceGo <no-reply@racego.test>"), Queue: e.q, Turnstile: v})

	otp := func(token string) resp {
		body := map[string]any{"email": "contoh.ts." + e.tag + "@uji.test"}
		if token != "" {
			body["turnstile_token"] = token
		}
		return e.do(uuid.Nil, "POST", "/v1/auth/otp/request", body)
	}
	expect := func(r resp, code int, errCode string, wantCalls int32, step string) {
		t.Helper()
		if r.code != code || (errCode != "" && r.body["error"] != errCode) || calls.Load() != wantCalls {
			t.Fatalf("%s: %d %s (panggilan siteverify %d, mau %d)", step, r.code, r.raw, calls.Load(), wantCalls)
		}
	}
	r := otp("")
	expect(r, 422, "captcha_failed", 0, "OTP tanpa token")
	if r.field("turnstile_token") != "Verifikasi keamanan gagal, coba lagi" {
		t.Fatalf("pesan: %s", r.raw)
	}
	expect(otp("token-salah-uji"), 422, "captcha_failed", 1, "OTP token salah")
	expect(otp("token-lambat-uji"), 503, "captcha_unavailable", 2, "OTP timeout")
	expect(otp("token-lolos-uji"), 200, "", 3, "OTP token valid")
	// Batas 20 permintaan kode per IP per jam berlaku di depan verifikasi: 16 lagi sampai batas,
	// sesudahnya 429 tanpa panggilan keluar.
	for i := 0; i < 16; i++ {
		otp("token-salah-uji")
	}
	n := calls.Load()
	expect(otp("token-lolos-uji"), 429, "", n, "OTP setelah batas IP")

	// Masuk antrean: satu tantangan per sesi, polling tidak pernah.
	owner, runner := e.user("owner"), e.user("pelari")
	org := "/v1/org/" + e.createOrg(owner, "Antri TS "+e.tag)
	now := time.Now()
	body := eventBody("Antri TS "+e.tag, "Bogor", now.Add(-time.Minute), now.AddDate(0, 0, 30), cat("5K", "5", 5))
	body["waiting_room"] = true
	qid := e.must(e.do(owner, "POST", org+"/events", body), 201).body["id"].(string)
	e.must(e.do(owner, "POST", org+"/events/"+qid+"/publish", nil), 200)
	qpath := "/v1/events/" + qid + "/queue"
	expect(e.do(runner, "POST", qpath, nil), 422, "captcha_failed", n, "join tanpa token")
	expect(e.do(runner, "POST", qpath, map[string]any{"turnstile_token": "token-salah-uji"}), 422, "captcha_failed", n+1, "join token salah")
	expect(e.do(runner, "POST", qpath, map[string]any{"turnstile_token": "token-lolos-uji"}), 201, "", n+2, "join token valid")
	expect(e.do(runner, "POST", qpath, nil), 200, "", n+2, "join ulang dalam sesi yang sama")
	expect(e.do(runner, "GET", qpath+"/me", nil), 200, "", n+2, "polling")
	e.must(e.do(runner, "DELETE", qpath+"/me", nil), 204)
	expect(e.do(runner, "POST", qpath, nil), 422, "captcha_failed", n+2, "sesi baru setelah keluar")

	out := logs.String()
	if !strings.Contains(out, "fail closed") {
		t.Fatalf("timeout harus dicatat: %s", out)
	}
	for _, s := range []string{tsSecret, "token-lolos-uji", "token-salah-uji", "token-lambat-uji"} {
		if strings.Contains(out, s) {
			t.Fatalf("%q bocor ke log: %s", s, out)
		}
	}
}
