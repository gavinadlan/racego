package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Export peserta: peran & tenant, isolasi, minimisasi data, injeksi rumus, filter, audit, refund.
func TestParticipantsExport(t *testing.T) {
	e := newFinEnv(t)
	ctx := context.Background()
	slug, orgID, owner := e.verifiedOrg("Org Peserta", finAccount)
	evID, catID := e.event(slug, owner, "Lari Peserta CONTOH")
	o1 := e.paidOrder(orgID, evID, catID, e.user("pelari1"), 2)
	o2 := e.paidOrder(orgID, evID, catID, e.user("pelari2"), 1)
	// Nama berbahaya + data opsional pada satu peserta order 1.
	if _, err := e.db.Exec(ctx, `UPDATE participants SET full_name = '=HYPERLINK("http://jahat.test","klik")', gender = 'F', birth_date = '1990-05-17',
		emergency_name = 'Ibu CONTOH', emergency_phone = '081234567890', jersey_size = 'S'
		WHERE id = (SELECT id FROM participants WHERE order_id = $1 ORDER BY id LIMIT 1)`, o1); err != nil {
		t.Fatal(err)
	}
	// Org lain dengan peserta sendiri: tidak boleh pernah muncul.
	slugB, orgB, ownerB := e.verifiedOrg("Org Lain", "5555666677")
	evB, catB := e.event(slugB, ownerB, "Lari Org Lain")
	oB := e.paidOrder(orgB, evB, catB, e.user("pelariB"), 1)
	e.db.Exec(ctx, `UPDATE participants SET full_name = 'RAHASIA ORG LAIN' WHERE order_id = $1`, oB) //nolint:errcheck

	admin := e.member(orgID, "admin", "admin")
	staff := e.member(orgID, "staf", "rpc_staff")
	fin := e.member(orgID, "keuangan", "finance")
	stranger := e.user("asing")
	base := "/v1/org/" + slug + "/events/" + evID.String() + "/participants"

	get := func(user uuid.UUID, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-User-ID", user.String())
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}

	// Matriks peran: [ringkasan, daftar, csv].
	for _, tc := range []struct {
		name string
		user uuid.UUID
		want [3]int
	}{
		{"owner", owner, [3]int{200, 200, 200}},
		{"admin", admin, [3]int{200, 200, 200}},
		{"rpc_staff", staff, [3]int{200, 403, 403}},
		{"finance", fin, [3]int{403, 403, 403}},
		{"non-anggota", stranger, [3]int{404, 404, 404}},
	} {
		for i, p := range []string{base + "/summary", base, base + ".csv"} {
			if rec := get(tc.user, p); rec.Code != tc.want[i] {
				t.Errorf("%s %s: %d, mau %d", tc.name, p, rec.Code, tc.want[i])
			}
		}
	}
	// Event org lain lewat slug org sendiri → 404 (tenant).
	other := "/v1/org/" + slug + "/events/" + evB.String() + "/participants"
	for _, p := range []string{other + "/summary", other, other + ".csv"} {
		if rec := get(owner, p); rec.Code != 404 {
			t.Errorf("event org lain %s: %d", p, rec.Code)
		}
	}

	// Ringkasan tanpa data pribadi.
	sum := e.must(e.do(staff, "GET", base+"/summary", nil), 200)
	if sum.body["total"] != float64(3) || strings.Contains(sum.raw, "pelari") || strings.Contains(sum.raw, "@") {
		t.Fatalf("ringkasan: %s", sum.raw)
	}
	if !strings.Contains(sum.raw, `{"key":"M","count":2}`) || !strings.Contains(sum.raw, `{"key":"S","count":1}`) {
		t.Fatalf("jersey: %s", sum.raw)
	}

	// Daftar: tersamar, terisolasi, filter & cari.
	list := e.must(e.do(owner, "GET", base, nil), 200)
	if list.body["total"] != float64(3) || strings.Contains(list.raw, finNIK) || !strings.Contains(list.raw, "••••"+finNIK[len(finNIK)-4:]) ||
		strings.Contains(list.raw, "RAHASIA ORG LAIN") {
		t.Fatalf("daftar: %s", list.raw)
	}
	if r := e.must(e.do(owner, "GET", base+"?q=HYPERLINK", nil), 200); r.body["total"] != float64(1) {
		t.Fatalf("cari nama: %s", r.raw)
	}
	bib := list.body["items"].([]any)[0].(map[string]any)["bib"].(string)
	if r := e.must(e.do(owner, "GET", base+"?q="+bib, nil), 200); r.body["total"] != float64(1) {
		t.Fatalf("cari BIB: %s", r.raw)
	}
	if r := e.must(e.do(owner, "GET", base+"?category="+catID.String()+"&status=issued", nil), 200); r.body["total"] != float64(3) {
		t.Fatalf("filter: %s", r.raw)
	}
	if r := e.do(owner, "GET", base+"?status=lunas", nil); r.code != 422 || r.field("status") == "" {
		t.Fatalf("status salah: %s", r.raw)
	}

	// Refund order 2 → status tiket "refunded" tercermin di daftar & CSV.
	e.must(e.do(owner, "POST", "/v1/org/"+slug+"/orders/"+o2.String()+"/refund", map[string]any{"reason": "Peserta CONTOH batal ikut"}), 201)
	var refundID string
	e.db.QueryRow(ctx, `SELECT id FROM refunds WHERE order_id = $1`, o2).Scan(&refundID) //nolint:errcheck
	e.must(e.do(e.sa, "POST", "/v1/admin/refunds/"+refundID+"/approve", nil), 200)
	if r := e.must(e.do(owner, "GET", base+"?status=refunded", nil), 200); r.body["total"] != float64(1) {
		t.Fatalf("filter refund: %s", r.raw)
	}

	// CSV: header aman, injeksi ternetralkan, NIK tersamar, status refund, filter.
	rec := get(owner, base+".csv")
	body := rec.Body.String()
	h := rec.Header()
	if !strings.HasPrefix(body, "\uFEFF") || h.Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(h.Get("Content-Disposition"), `attachment; filename="peserta-lari-peserta-contoh-`) {
		t.Fatalf("header CSV: %v", h)
	}
	cr := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\uFEFF")))
	cr.Comma = ';'
	recs, err := cr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 4 || len(recs[0]) != 14 || recs[0][0] != "BIB" {
		t.Fatalf("baris/kolom CSV: %q", recs)
	}
	var sawInjection, sawRefund bool
	for _, r := range recs[1:] {
		if strings.HasPrefix(r[1], "=") {
			t.Fatalf("injeksi rumus lolos: %q", r[1])
		}
		sawInjection = sawInjection || strings.HasPrefix(r[1], `'=HYPERLINK(`)
		sawRefund = sawRefund || r[10] == "Refund"
		if r[7] != "••••"+finNIK[len(finNIK)-4:] {
			t.Fatalf("nomor identitas: %q", r[7])
		}
	}
	if !sawInjection || !sawRefund || strings.Contains(body, finNIK) || strings.Contains(body, "RAHASIA") || strings.Contains(body, "350000") {
		t.Fatalf("isi CSV: %s", body)
	}
	if rec := get(owner, base+".csv?status=refunded"); strings.Count(rec.Body.String(), "\r\n") != 2 {
		t.Fatalf("CSV filter: %q", rec.Body.String())
	}

	// Audit: aktor, org, event, filter, jumlah baris; tanpa isi data.
	var n int
	var after string
	e.db.QueryRow(ctx, `SELECT count(*), max(after::text) FILTER (WHERE after->'filters'->>'status' = 'refunded') FROM audit_logs
		WHERE action = 'participants.download' AND org_id = $1 AND entity_id = $2 AND actor_id = $3`, orgID, evID.String(), owner).Scan(&n, &after) //nolint:errcheck
	if n != 3 || !strings.Contains(after, `"rows": 1`) || strings.Contains(after, "Peserta") || strings.Contains(after, finNIK) {
		t.Fatalf("audit %d: %s", n, after)
	}
	var adminAudit int
	e.db.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'participants.download' AND actor_id = $1`, admin).Scan(&adminAudit) //nolint:errcheck
	if adminAudit != 1 {                                                                                                                    // dari matriks peran
		t.Fatalf("audit admin: %d", adminAudit)
	}

	// Pembatas laju: 10 unduhan per menit per pengguna (owner sudah 3).
	for i := 0; i < 7; i++ {
		if rec := get(owner, base+".csv"); rec.Code != 200 {
			t.Fatalf("unduhan ke-%d: %d", i+4, rec.Code)
		}
	}
	if rec := get(owner, base+".csv"); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("batas laju: %d", rec.Code)
	}
	if rec := get(admin, base+".csv"); rec.Code != 200 {
		t.Fatalf("batas per pengguna, admin: %d", rec.Code)
	}
}

// countingWriter: ResponseWriter yang hanya menghitung byte (tidak menyimpan isi).
type countingWriter struct {
	h     http.Header
	n     atomic.Int64
	lines atomic.Int64
	code  int
}

func (c *countingWriter) Header() http.Header  { return c.h }
func (c *countingWriter) WriteHeader(code int) { c.code = code }
func (c *countingWriter) Write(p []byte) (int, error) {
	c.n.Add(int64(len(p)))
	c.lines.Add(int64(bytes.Count(p, []byte{'\n'})))
	return len(p), nil
}

// 10.000 peserta di-stream per baris: kenaikan heap puncak jauh di bawah ukuran berkas.
func TestParticipantsExport10k(t *testing.T) {
	e := newFinEnv(t)
	ctx := context.Background()
	slug, orgID, owner := e.verifiedOrg("Org Besar", finAccount)
	evID, catID := e.event(slug, owner, "Lari Besar CONTOH")
	o := e.paidOrder(orgID, evID, catID, e.user("pelari"), 1)
	const n = 10000
	start := time.Now()
	if _, err := e.db.Exec(ctx, `
		WITH p AS (
		  INSERT INTO participants (order_id, org_id, event_id, category_id, full_name, email, id_number, jersey_size, emergency_name, emergency_phone)
		  SELECT $1, $2, $3, $4, 'Peserta CONTOH ' || g, 'contoh' || g || '@uji.test', lpad(g::text, 16, '7'), (ARRAY['S','M','L','XL'])[1 + g % 4],
		         'Kontak CONTOH', '0812000' || lpad(g::text, 5, '0')
		  FROM generate_series(1, $5::int) g RETURNING id)
		INSERT INTO tickets (participant_id, order_id, org_id, event_id, category_id, bib_number, qr_token)
		SELECT p.id, $1, $2, $3, $4, 'X' || row_number() OVER (), 'uji' FROM p`, o, orgID, evID, catID, n); err != nil {
		t.Fatal(err)
	}
	t.Logf("data sintetis %d peserta: %v", n, time.Since(start).Round(time.Millisecond))

	// GC agresif selama pengukuran: puncak heap ≈ data hidup, bukan sampah yang belum dikumpulkan.
	defer debug.SetGCPercent(debug.SetGCPercent(5))
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
				runtime.ReadMemStats(&m)
				if m.HeapInuse > peak.Load() {
					peak.Store(m.HeapInuse)
				}
			}
		}
	}()
	req := httptest.NewRequest("GET", "/v1/org/"+slug+"/events/"+evID.String()+"/participants.csv", nil)
	req.Header.Set("X-User-ID", owner.String())
	w := &countingWriter{h: http.Header{}}
	start = time.Now()
	e.h.ServeHTTP(w, req)
	elapsed := time.Since(start)
	close(stop)
	<-done
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	growth := int64(peak.Load()) - int64(before.HeapInuse)
	t.Logf("CSV %d baris, %.2f MB dalam %v; heap in-use awal %.1f MB, puncak +%.2f MB, total alokasi %.1f MB",
		w.lines.Load(), float64(w.n.Load())/1e6, elapsed.Round(time.Millisecond),
		float64(before.HeapInuse)/1e6, float64(growth)/1e6, float64(after.TotalAlloc-before.TotalAlloc)/1e6)
	if w.code != 0 && w.code != 200 {
		t.Fatalf("status %d", w.code)
	}
	if got := w.lines.Load(); got != n+2 { // header + peserta paidOrder + 10.000
		t.Fatalf("%d baris, mau %d", got, n+2)
	}
	// Berkas utuh ±1,5 MB; streaming berarti puncak heap tidak ikut membesar sebesar berkas.
	if growth > 2*w.n.Load() { // tanpa streaming: buffer berkas + 10.000 struct ≈ 4 MB lebih
		t.Fatalf("puncak heap +%d byte ≥ ukuran berkas %d byte: tidak streaming?", growth, w.n.Load())
	}
}
