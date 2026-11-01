package server

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/config"
	"github.com/gavinadlan/racego/backend/internal/notify"
	"github.com/gavinadlan/racego/backend/internal/partner"
	"github.com/gavinadlan/racego/backend/internal/payment"
	"github.com/gavinadlan/racego/backend/internal/ticket"
)

var inviteLink = regexp.MustCompile(`/undangan/([A-Za-z0-9_-]{43})`)

type inviteEnv struct {
	*portalEnv
	smtp *fakeSMTP
	logs *syncBuf
}

func newInviteEnv(t *testing.T) *inviteEnv {
	t.Helper()
	pe := newPortalEnv(t, true)
	e := &inviteEnv{portalEnv: pe, smtp: newFakeSMTP(t), logs: captureLogs(t)}
	rdb := redis.NewClient(&redis.Options{Addr: pe.mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	signer, _ := ticket.NewSigner("", true)
	cfg := config.Config{Env: "development", FrontendURL: "http://localhost:3000", ReservationTTL: 15 * time.Minute, AutoVerifyOrgs: true, UploadDir: t.TempDir(),
		Queue: config.QueueConfig{LobbyMinutes: 15, AdmitPerSec: 20, MaxActive: 300, PassTTL: 10 * time.Minute, HeartbeatTTL: 45 * time.Second}}
	e.h = NewRouter(Deps{Cfg: cfg, DB: pe.db, Redis: rdb, Inv: pe.inv, Snap: payment.NewMockSnap(cfg.FrontendURL), Signer: signer,
		Mailer: notify.NewMailer(e.smtp.addr, "", "", "RaceGo <no-reply@racego.test>"), Queue: pe.q})
	return e
}

// invite membuat undangan lalu mengambil token dari email (satu-satunya tempat token mentah ada).
func (e *inviteEnv) invite(by uuid.UUID, org, email, role string) (id, token string) {
	e.t.Helper()
	r := e.must(e.do(by, "POST", org+"/invitations", map[string]any{"email": email, "role": role}), 201)
	m := inviteLink.FindStringSubmatch(e.smtp.lastMail())
	if m == nil {
		e.t.Fatalf("tautan tidak ada di email: %q", e.smtp.lastMail())
	}
	for _, it := range r.body["invitations"].([]any) {
		if inv := it.(map[string]any); inv["email"] == strings.ToLower(email) {
			id = inv["id"].(string)
		}
	}
	return id, m[1]
}

func (e *inviteEnv) count(sql string, args ...any) int {
	var n int
	if err := e.db.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestInvitationRolesAndTenants(t *testing.T) {
	e := newInviteEnv(t)
	owner, admin, fin, staff, stranger, ownerB := e.user("owner"), e.user("admin"), e.user("keuangan"), e.user("staf"), e.user("asing"), e.user("ownerb")
	a := "/v1/org/" + e.createOrg(owner, "Undang A "+e.tag)
	b := "/v1/org/" + e.createOrg(ownerB, "Undang B "+e.tag)
	e.join(a, admin, "admin")
	e.join(a, fin, "finance")
	e.join(a, staff, "rpc_staff")

	// Membuat: [rpc_staff, finance, admin, owner].
	for _, tc := range []struct {
		name string
		user uuid.UUID
		want [4]int
	}{
		{"owner", owner, [4]int{201, 201, 201, 422}},
		{"admin", admin, [4]int{201, 403, 403, 422}},
		{"finance", fin, [4]int{403, 403, 403, 403}},
		{"rpc_staff", staff, [4]int{403, 403, 403, 403}},
		{"non-anggota", stranger, [4]int{404, 404, 404, 404}},
		{"anonim", uuid.Nil, [4]int{401, 401, 401, 401}},
	} {
		for i, role := range []string{"rpc_staff", "finance", "admin", "owner"} {
			r := e.do(tc.user, "POST", a+"/invitations", map[string]any{"email": tc.name + "-" + role + "." + e.tag + "@uji.test", "role": role})
			if r.code != tc.want[i] {
				t.Errorf("%s mengundang %s: %d %s", tc.name, role, r.code, r.raw)
			}
			if r.code == 403 && tc.name == "admin" && r.body["error"] != "role_not_allowed" {
				t.Errorf("admin → %s: %s", role, r.raw)
			}
		}
	}
	// Daftar.
	for user, want := range map[uuid.UUID]int{owner: 200, admin: 200, fin: 403, staff: 403, stranger: 404, uuid.Nil: 401} {
		if r := e.do(user, "GET", a+"/invitations", nil); r.code != want {
			t.Errorf("daftar oleh %v: %d", user, r.code)
		}
	}
	list := e.must(e.do(owner, "GET", a+"/invitations", nil), 200)
	if n := len(list.body["invitations"].([]any)); n != 4 {
		t.Fatalf("4 undangan tertunda, dapat %d: %s", n, list.raw)
	}
	if strings.Contains(list.raw, "token") {
		t.Fatalf("daftar memuat token: %s", list.raw)
	}
	var adminInvite, staffInvite string
	for _, it := range list.body["invitations"].([]any) {
		inv := it.(map[string]any)
		switch inv["role"] {
		case "admin":
			adminInvite = inv["id"].(string)
		case "rpc_staff":
			staffInvite = inv["id"].(string)
		}
	}
	// Admin tidak bisa mengelola undangan admin; finance/rpc_staff/non-anggota/org lain tidak bisa sama sekali.
	if r := e.do(admin, "DELETE", a+"/invitations/"+adminInvite, nil); r.code != 403 {
		t.Fatalf("admin cabut undangan admin: %s", r.raw)
	}
	if r := e.do(admin, "POST", a+"/invitations/"+adminInvite+"/resend", nil); r.code != 403 {
		t.Fatalf("admin kirim ulang undangan admin: %s", r.raw)
	}
	for user, want := range map[uuid.UUID]int{fin: 403, staff: 403, stranger: 404, uuid.Nil: 401} {
		if r := e.do(user, "DELETE", a+"/invitations/"+staffInvite, nil); r.code != want {
			t.Errorf("cabut oleh %v: %d", user, r.code)
		}
		if r := e.do(user, "POST", a+"/invitations/"+staffInvite+"/resend", nil); r.code != want {
			t.Errorf("kirim ulang oleh %v: %d", user, r.code)
		}
	}
	// Tenant: owner B lewat org A → 404; lewat org B dengan id undangan A → 404.
	if r := e.do(ownerB, "GET", a+"/invitations", nil); r.code != 404 {
		t.Fatalf("owner B lihat undangan A: %d", r.code)
	}
	if r := e.do(ownerB, "DELETE", b+"/invitations/"+staffInvite, nil); r.code != 404 {
		t.Fatalf("owner B cabut undangan A lewat org B: %s", r.raw)
	}
	if r := e.must(e.do(ownerB, "GET", b+"/invitations", nil), 200); len(r.body["invitations"].([]any)) != 0 {
		t.Fatalf("undangan A bocor ke B: %s", r.raw)
	}
	e.must(e.do(admin, "DELETE", a+"/invitations/"+staffInvite, nil), 200)
	if e.count(`SELECT count(*) FROM org_invitations WHERE id = $1 AND revoked_at IS NOT NULL`, staffInvite) != 1 {
		t.Fatal("undangan staf tidak tercabut")
	}
}

func TestInvitationAcceptFlow(t *testing.T) {
	e := newInviteEnv(t)
	ctx := context.Background()
	owner := e.user("owner")
	slug := e.createOrg(owner, "Undang Terima "+e.tag)
	a := "/v1/org/" + slug
	email := "Calon.Staf." + e.tag + "@Uji.Test" // huruf besar: disimpan & dicocokkan tanpa membedakan
	id, token := e.invite(owner, a, email, "rpc_staff")

	// Isi email: hanya nama org, peran, pengundang, tautan.
	mail := e.smtp.lastMail()
	if !strings.Contains(mail, "Undang Terima") || !strings.Contains(mail, "Staf race pack") || !strings.Contains(mail, "owner") {
		t.Fatalf("isi email: %s", mail)
	}
	// Token tidak tersimpan mentah di mana pun: kolom, outbox, audit, log.
	for _, q := range []string{
		`SELECT count(*) FROM org_invitations i WHERE to_jsonb(i)::text LIKE '%' || $1 || '%'`,
		`SELECT count(*) FROM outbox WHERE payload::text LIKE '%' || $1 || '%'`,
		`SELECT count(*) FROM audit_logs WHERE coalesce(after::text, '') || coalesce(before::text, '') LIKE '%' || $1 || '%'`,
	} {
		if n := e.count(q, token); n != 0 {
			t.Fatalf("token mentah tersimpan (%s): %d", q, n)
		}
	}
	if e.count(`SELECT count(*) FROM org_invitations WHERE id = $1 AND token_hash = sha256($2::bytea)`, id, token) != 1 {
		t.Fatal("hash SHA-256 token tidak tersimpan")
	}

	// Lihat (publik): ringkasan tanpa email lengkap.
	show := e.must(e.do(uuid.Nil, "GET", "/v1/invitations/"+token, nil), 200)
	if show.body["org_name"] != "Undang Terima "+e.tag || show.body["role"] != "rpc_staff" || show.body["email_masked"] != "c•••@uji.test" ||
		strings.Contains(show.raw, "calon.staf") || show.body["email_matches"] != nil {
		t.Fatalf("ringkasan: %s", show.raw)
	}

	// Email lain → ditolak tanpa mengubah apa pun.
	other := e.user("lain")
	if r := e.do(other, "GET", "/v1/invitations/"+token, nil); r.body["email_matches"] != false {
		t.Fatalf("email_matches: %s", r.raw)
	}
	r := e.do(other, "POST", "/v1/invitations/"+token+"/accept", nil)
	if r.code != 403 || r.body["error"] != "invitation_email_mismatch" || !strings.Contains(r.raw, "c•••@uji.test") {
		t.Fatalf("email beda: %s", r.raw)
	}
	if e.count(`SELECT count(*) FROM org_invitations WHERE id = $1 AND accepted_at IS NULL`, id) != 1 ||
		e.count(`SELECT count(*) FROM org_members m JOIN organizations o ON o.id = m.org_id WHERE o.slug = $1`, slug) != 1 {
		t.Fatal("email beda mengubah data")
	}

	// Penerima sah (akun dengan email sama, huruf kecil). Terima paralel → tepat satu keanggotaan.
	var invitee uuid.UUID
	if err := e.db.QueryRow(ctx, `INSERT INTO users (email, name) VALUES ($1, 'Calon Staf') RETURNING id`, strings.ToLower(email)).Scan(&invitee); err != nil {
		t.Fatal(err)
	}
	if r := e.do(invitee, "GET", "/v1/invitations/"+token, nil); r.body["email_matches"] != true {
		t.Fatalf("email_matches benar: %s", r.raw)
	}
	var ok, gone atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch r := e.do(invitee, "POST", "/v1/invitations/"+token+"/accept", nil); r.code {
			case 200:
				ok.Add(1)
			case 404:
				gone.Add(1)
			default:
				t.Errorf("terima paralel: %d %s", r.code, r.raw)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || gone.Load() != 7 {
		t.Fatalf("terima paralel: %d ok, %d ditolak", ok.Load(), gone.Load())
	}
	if e.count(`SELECT count(*) FROM org_members m JOIN organizations o ON o.id = m.org_id WHERE o.slug = $1 AND m.user_id = $2 AND m.role = 'rpc_staff'`, slug, invitee) != 1 {
		t.Fatal("keanggotaan tidak tepat satu")
	}
	e.must(e.do(invitee, "POST", a+"/events/"+uuid.NewString()+"/scan", map[string]any{"token": "x"}), 404) // anggota: event tidak ada → 404 event, bukan org
	if r := e.do(invitee, "GET", a+"/events", nil); r.code != 200 {
		t.Fatalf("anggota baru buka event: %d", r.code)
	}

	// Dipakai ulang, dicabut, kedaluwarsa, token salah → respons identik.
	_, revoked := e.invite(owner, a, "dicabut."+e.tag+"@uji.test", "finance")
	var revokedID string
	e.db.QueryRow(ctx, `SELECT id FROM org_invitations WHERE email = $1`, "dicabut."+e.tag+"@uji.test").Scan(&revokedID) //nolint:errcheck
	e.must(e.do(owner, "DELETE", a+"/invitations/"+revokedID, nil), 200)
	_, expired := e.invite(owner, a, "basi."+e.tag+"@uji.test", "rpc_staff")
	e.db.Exec(ctx, `UPDATE org_invitations SET expires_at = now() - interval '1 minute' WHERE email = $1`, "basi."+e.tag+"@uji.test") //nolint:errcheck
	bogus := strings.Repeat("A", 43)
	var want string
	for name, tok := range map[string]string{"dipakai": token, "dicabut": revoked, "kedaluwarsa": expired, "salah": bogus, "pendek": "abc"} {
		for _, path := range []string{"/v1/invitations/" + tok, "/v1/invitations/" + tok + "/accept"} {
			method := "GET"
			if strings.HasSuffix(path, "/accept") {
				method = "POST"
			}
			r := e.do(invitee, method, path, nil)
			if r.code != 404 || r.body["error"] != "invitation_invalid" {
				t.Fatalf("%s %s: %d %s", name, method, r.code, r.raw)
			}
			if want == "" {
				want = r.raw
			} else if r.raw != want {
				t.Fatalf("respons %s berbeda: %s vs %s", name, r.raw, want)
			}
		}
	}
	// Membuka (GET) tautan basi juga tercatat: halaman /undangan hanya memanggil GET.
	before := e.count(`SELECT count(*) FROM audit_logs WHERE action = 'invite.expired_attempt' AND entity_id = $1`, revokedID)
	e.do(uuid.Nil, "GET", "/v1/invitations/"+revoked, nil)
	if after := e.count(`SELECT count(*) FROM audit_logs WHERE action = 'invite.expired_attempt' AND entity_id = $1`, revokedID); after != before+1 {
		t.Fatalf("GET tautan dicabut tidak diaudit: %d → %d", before, after)
	}
	// Undangan kedaluwarsa tidak menghalangi undangan baru untuk email yang sama.
	e.invite(owner, a, "basi."+e.tag+"@uji.test", "rpc_staff")

	// Audit: tanpa token.
	for action, min := range map[string]int{"invite.created": 4, "invite.revoked": 1, "invite.accepted": 1, "invite.expired_attempt": 3} {
		if n := e.count(`SELECT count(*) FROM audit_logs a JOIN organizations o ON o.id = a.org_id WHERE o.slug = $1 AND a.action = $2`, slug, action); n < min {
			t.Errorf("audit %s: %d, mau ≥ %d", action, n, min)
		}
	}
	if strings.Contains(e.logs.String(), token) || strings.Contains(e.logs.String(), expired) {
		t.Fatal("token undangan muncul di log")
	}

	// Worker: hash token berumur > 30 hari dibuang; yang baru tetap.
	e.db.Exec(ctx, `UPDATE org_invitations SET created_at = now() - interval '31 days' WHERE id = $1`, id) //nolint:errcheck
	if _, err := partner.CleanupInvitations(ctx, e.db); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM org_invitations WHERE id = $1 AND token_hash IS NULL`, id) != 1 ||
		e.count(`SELECT count(*) FROM org_invitations i JOIN organizations o ON o.id = i.org_id WHERE o.slug = $1 AND token_hash IS NOT NULL`, slug) == 0 {
		t.Fatal("pembersihan token lama")
	}
}

func TestInvitationLimits(t *testing.T) {
	e := newInviteEnv(t)
	ctx := context.Background()
	owner := e.user("owner")
	a := "/v1/org/" + e.createOrg(owner, "Undang Batas "+e.tag)

	// Email yang sudah anggota & undangan aktif ganda → 422 per kolom.
	member := e.user("anggota")
	e.join(a, member, "finance")
	if r := e.do(owner, "POST", a+"/invitations", map[string]any{"email": e.email("anggota"), "role": "rpc_staff"}); r.code != 422 || !strings.Contains(r.field("email"), "sudah menjadi anggota") {
		t.Fatalf("sudah anggota: %s", r.raw)
	}
	if r := e.do(owner, "POST", a+"/invitations", map[string]any{"email": e.email("pemilik-diri"), "role": "rpc_staff"}); r.code != 201 {
		t.Fatalf("undang: %s", r.raw)
	}
	if r := e.do(owner, "POST", a+"/invitations", map[string]any{"email": strings.ToUpper(e.email("pemilik-diri")), "role": "admin"}); r.code != 422 || !strings.Contains(r.field("email"), "masih aktif") {
		t.Fatalf("undangan ganda: %s", r.raw)
	}

	// Kirim ulang: tidak lebih cepat dari 5 menit, maksimal 5 kali total; token lama tidak berlaku.
	id, first := e.invite(owner, a, "ulang."+e.tag+"@uji.test", "rpc_staff")
	if r := e.do(owner, "POST", a+"/invitations/"+id+"/resend", nil); r.code != 429 || r.body["error"] != "resend_too_soon" || r.body["retry_after"] == nil {
		t.Fatalf("kirim ulang terlalu cepat: %s", r.raw)
	}
	var last string
	for i := 2; i <= 5; i++ {
		e.db.Exec(ctx, `UPDATE org_invitations SET last_sent_at = now() - interval '6 minutes' WHERE id = $1`, id) //nolint:errcheck
		e.must(e.do(owner, "POST", a+"/invitations/"+id+"/resend", nil), 200)
		last = inviteLink.FindStringSubmatch(e.smtp.lastMail())[1]
	}
	e.db.Exec(ctx, `UPDATE org_invitations SET last_sent_at = now() - interval '6 minutes' WHERE id = $1`, id) //nolint:errcheck
	if r := e.do(owner, "POST", a+"/invitations/"+id+"/resend", nil); r.code != 429 || r.body["error"] != "resend_limit" {
		t.Fatalf("kirim ke-6: %s", r.raw)
	}
	if r := e.do(owner, "GET", "/v1/invitations/"+first, nil); r.code != 404 {
		t.Fatalf("token lama setelah kirim ulang: %d", r.code)
	}
	e.must(e.do(owner, "GET", "/v1/invitations/"+last, nil), 200)
	if n := e.count(`SELECT count(*) FROM audit_logs WHERE action = 'invite.resent' AND entity_id = $1`, id); n != 4 {
		t.Fatalf("audit kirim ulang: %d", n)
	}

	// Batas per org/pengundang: 20 permintaan kirim per jam (termasuk yang ditolak di atas).
	limited := false
	for i := 0; i < 20 && !limited; i++ {
		r := e.do(owner, "POST", a+"/invitations", map[string]any{"email": uuid.NewString()[:8] + "." + e.tag + "@uji.test", "role": "rpc_staff"})
		limited = r.code == 429 && r.body["retry_after"] != nil
		if !limited && r.code != 201 {
			t.Fatalf("undang massal: %s", r.raw)
		}
	}
	if !limited || e.count(`SELECT count(*) FROM org_invitations i JOIN organizations o ON o.id = i.org_id WHERE o.slug = $1`, strings.TrimPrefix(a, "/v1/org/")) > 20 {
		t.Fatal("batas 20 undangan per jam tidak berlaku")
	}

	// Percobaan token per IP: 30 per 10 menit (sudah 2 di atas).
	e.mr.FastForward(11 * time.Minute)
	for i := 0; i < 30; i++ {
		e.do(uuid.Nil, "GET", "/v1/invitations/"+strings.Repeat("B", 43), nil)
	}
	if r := e.do(uuid.Nil, "GET", "/v1/invitations/"+last, nil); r.code != 429 {
		t.Fatalf("batas percobaan token per IP: %d", r.code)
	}
}
