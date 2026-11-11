package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/gavinadlan/racego/backend/internal/metrics"
)

// maxSeries: batas jumlah deret di /metrics setelah lalu lintas sintetis. Router punya ±110 rute;
// tiap rute yang dipanggil menambah ±16 deret (histogram 13 bucket + sum + count + counter status).
// Lonjakan di atas batas ini berarti ada label berkardinalitas tinggi (ID, path mentah, dsb.).
const maxSeries = 2500

var (
	reUUID  = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	reEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	// Token sesi/pass/QR: deret panjang base64url/hex. Nama metrik tidak pernah sepanjang ini tanpa "_".
	reToken = regexp.MustCompile(`[A-Za-z0-9+/=-]{32,}`)
	reLabel = regexp.MustCompile(`([a-z_]+)="([^"]*)"`)
	reIP    = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
)

// Setelah lalu lintas sintetis yang sengaja membawa UUID, email, token, IP, dan path acak, seluruh
// keluaran /metrics harus tetap berkardinalitas rendah dan bebas data sensitif.
func TestMetricsOutputSafe(t *testing.T) {
	e := newPortalEnv(t, true)
	owner := e.user("metrikowner").String()
	secret := "tok_" + strings.Repeat("Zx9", 14)
	send := func(method, path, user string, hdr map[string]string) {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		if user != "" {
			req.Header.Set("X-User-ID", user)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		e.h.ServeHTTP(httptest.NewRecorder(), req)
	}
	for i := 0; i < 300; i++ {
		id := uuid.NewString()
		email := fmt.Sprintf("orang%d@contoh.test", i)
		ip := fmt.Sprintf("203.0.113.%d", i%250)
		hdr := map[string]string{"X-Forwarded-For": ip, "Cookie": "racego_session=" + secret, "X-Queue-Pass": secret, "Idempotency-Key": id}
		send(http.MethodGet, "/v1/events/"+id, "", hdr)
		send(http.MethodGet, "/v1/events?q="+email, "", hdr)
		send(http.MethodGet, "/v1/organizers/slug-"+id, "", hdr)
		send(http.MethodGet, "/v1/invitations/"+secret+id, "", hdr)
		send(http.MethodGet, "/v1/orders/"+id, owner, hdr)
		send(http.MethodPost, "/v1/categories/"+id+"/reservations", owner, hdr)
		send(http.MethodPost, "/v1/events/"+id+"/queue", owner, hdr)
		send(http.MethodGet, "/v1/events/"+id+"/queue/me", owner, hdr)
		send(http.MethodGet, "/v1/org/org-"+id+"/events", owner, hdr)
		send(http.MethodGet, "/v1/admin/payouts/"+id, owner, hdr)
		send(http.MethodGet, "/uploads/"+id+".png", "", hdr)
		send(http.MethodGet, "/acak/"+id+"/"+email, "", hdr)     // rute tidak ada
		send("M"+strings.ToUpper(id[:8]), "/v1/events", "", hdr) // metode liar
		send(http.MethodPost, "/v1/webhooks/midtrans", "", hdr)  // signature tidak valid
		send(http.MethodPost, "/v1/auth/otp/verify", "", hdr)    // body tidak lengkap
		send(http.MethodGet, "/v1/events/"+email, "", hdr)       // email di tempat ID
		send(http.MethodGet, "/healthz", "", hdr)
	}

	rec := httptest.NewRecorder()
	metrics.Handler("", false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics: status %d", rec.Code)
	}
	series := 0
	routes := map[string]bool{}
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		series++
		for _, re := range []*regexp.Regexp{reUUID, reEmail, reIP} {
			if m := re.FindString(line); m != "" {
				t.Fatalf("keluaran metrik memuat data mirip identitas %q: %s", m, line)
			}
		}
		for _, m := range reLabel.FindAllStringSubmatch(line, -1) {
			name, val := m[1], m[2]
			if len(val) > 80 || reToken.MatchString(val) || strings.Contains(val, secret) {
				t.Fatalf("label %s bernilai mencurigakan (token/teks bebas): %s", name, line)
			}
			if name == "route" {
				routes[val] = true
			}
			if name == "method" && !strings.Contains("GET POST PUT PATCH DELETE OPTIONS HEAD OTHER", val) {
				t.Fatalf("label method di luar himpunan tetap: %s", line)
			}
		}
	}
	if series > maxSeries {
		t.Fatalf("jumlah deret %d melebihi batas %d: ada label berkardinalitas tinggi", series, maxSeries)
	}
	// Rute tercatat sebagai pola chi, bukan path mentah; path tak dikenal jadi satu label.
	for _, want := range []string{"/v1/events/{eventID}", "/v1/categories/{categoryID}/reservations", "/v1/events/{eventID}/queue/me", "unmatched", "/healthz"} {
		if !routes[want] {
			t.Errorf("label route %q tidak ada (rute: %d macam)", want, len(routes))
		}
	}
	// Registry dipakai bersama tes lain di paket ini, jadi yang diperiksa: setiap label route harus
	// salah satu pola yang terdaftar di router (bukan path mentah).
	known := map[string]bool{"unmatched": true}
	_ = chi.Walk(e.h.(chi.Routes), func(_, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		known[route] = true
		return nil
	})
	for r := range routes {
		// "/v1/admin/*": middleware sub-router menolak sebelum rute di dalamnya dipilih; tetap pola, bukan path.
		if prefix, ok := strings.CutSuffix(r, "/*"); ok {
			for k := range known {
				known[r] = known[r] || strings.HasPrefix(k, prefix+"/")
			}
		}
		if !known[r] {
			t.Errorf("label route %q bukan pola rute terdaftar: path mentah bocor ke label", r)
		}
	}
	t.Logf("deret: %d (batas %d), macam route: %d", series, maxSeries, len(routes))
}
