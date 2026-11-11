package server

import (
	"context"
	"encoding/csv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/gavinadlan/racego/backend/internal/draw"
)

// Uji undian doorprize (Tugas 10, Bagian B) lewat router lengkap + Postgres sungguhan.

type drawEnv struct {
	*attendEnv
	ids []string // semua tiket
}

// newDrawEnv: n tiket kategori 21K; yang diabsen = indeks di `attend`. Jendela absen/undian dibuka.
func newDrawEnv(t *testing.T, name string, n int, attend ...int) *drawEnv {
	t.Helper()
	e := &drawEnv{attendEnv: newAttendEnv(t, name)}
	e.open()
	e.ids, _ = e.ticketTokens(e.paidOrder(e.orgID, e.evID, e.catID, e.user("pelari"), n))
	for _, i := range attend {
		e.must(e.attend(e.staff, "", e.ids[i]), 200)
	}
	return e
}

func (e *drawEnv) prize(name string, qty int, category any) string {
	e.t.Helper()
	r := e.must(e.do(e.owner, "POST", e.base+"/draw/prizes", map[string]any{"name": name, "quantity": qty, "category_id": category}), 201)
	return r.body["id"].(string)
}

func (e *drawEnv) draw(user uuid.UUID, prize string, body map[string]any) resp {
	e.t.Helper()
	return e.do(user, "POST", e.base+"/draw/prizes/"+prize+"/draw", body)
}

func winnerList(r resp) []map[string]any {
	var out []map[string]any
	for _, w := range r.body["winners"].([]any) {
		out = append(out, w.(map[string]any))
	}
	return out
}

func (e *drawEnv) ticketOf(winnerID string) string {
	var id string
	if err := e.db.QueryRow(context.Background(), `SELECT ticket_id::text FROM draw_winners WHERE id = $1`, winnerID).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestDrawEligibility(t *testing.T) {
	// 7 tiket: 0-3 hadir; 4 tidak hadir; 5 hadir lalu direfund; 6 collected tanpa hadir.
	e := newDrawEnv(t, "Org Layak", 7, 0, 1, 2, 3, 5)
	ctx := context.Background()
	e.db.Exec(ctx, `UPDATE tickets SET status = 'refunded' WHERE id = $1`, e.ids[5])                        //nolint:errcheck
	e.db.Exec(ctx, `UPDATE tickets SET status = 'collected', collected_at = now() WHERE id = $1`, e.ids[6]) //nolint:errcheck
	hadir := map[string]bool{e.ids[0]: true, e.ids[1]: true, e.ids[2]: true, e.ids[3]: true}

	pool := e.must(e.do(e.owner, "GET", e.base+"/draw/pool", nil), 200)
	if pool.body["total"] != float64(4) || pool.body["attended"] != float64(4) || pool.body["draw_open"] != true {
		t.Fatalf("kolam attended: %s", pool.raw)
	}
	// Puluhan undian (pengembalian lewat banyak hadiah + allow_multiple_wins): peserta tak hadir/refund tak pernah muncul.
	e.must(e.do(e.owner, "PUT", e.base+"/draw/settings", map[string]any{"draw_eligibility": "attended", "allow_multiple_wins": true, "draw_public": false}), 200)
	for i := 0; i < 40; i++ {
		r := e.must(e.draw(e.owner, e.prize("Hadiah", 1, nil), nil), 200)
		if tid := e.ticketOf(winnerList(r)[0]["id"].(string)); !hadir[tid] {
			t.Fatalf("pemenang bukan peserta hadir: %s", tid)
		}
	}
	// Mode collected: hanya tiket collected (6). Terkunci sudah ada pemenang → ubah lewat DB hanya untuk uji kolam.
	e.db.Exec(ctx, `UPDATE events SET draw_eligibility = 'collected' WHERE id = $1`, e.evID) //nolint:errcheck
	pool = e.must(e.do(e.owner, "GET", e.base+"/draw/pool", nil), 200)
	if pool.body["total"] != float64(1) {
		t.Fatalf("kolam collected: %s", pool.raw)
	}
}

func TestDrawCategoryFilterAndOneWin(t *testing.T) {
	e := newDrawEnv(t, "Org Kategori", 3, 0, 1, 2)
	ctx := context.Background()
	var cat2 uuid.UUID
	if err := e.db.QueryRow(ctx, `INSERT INTO ticket_categories (event_id, org_id, code, name, distance_km, price, quota, max_per_user, bib_prefix)
		SELECT event_id, org_id, '10K', 'Kategori 10K', 10, price, quota, max_per_user, '10' FROM ticket_categories WHERE id = $1 RETURNING id`, e.catID).Scan(&cat2); err != nil {
		t.Fatal(err)
	}
	ids2, _ := e.ticketTokens(e.paidOrder(e.orgID, e.evID, cat2, e.user("pelari10"), 2))
	e.must(e.attend(e.staff, "", ids2[0]), 200)
	e.must(e.attend(e.staff, "", ids2[1]), 200)

	p10 := e.prize("Sepeda 10K", 2, cat2.String())
	for range 2 {
		r := e.must(e.draw(e.owner, p10, nil), 200)
		if tid := e.ticketOf(winnerList(r)[0]["id"].(string)); tid != ids2[0] && tid != ids2[1] {
			t.Fatalf("filter kategori bocor: %s", tid)
		}
	}
	// Kolam kategori 10K habis walau hadiah masih punya sisa kuota → tidak ada, tapi kuota habis lebih dulu: pakai hadiah baru.
	if r := e.draw(e.owner, e.prize("Topi 10K", 1, cat2.String()), nil); r.code != 409 || r.body["error"] != "pool_empty" {
		t.Fatalf("pool_empty setelah pemenang keluar dari kolam: %d %s", r.code, r.raw)
	}
	// Satu-menang: 3 peserta 21K, hadiah qty 3 dengan all → ketiganya, tidak ada ganda.
	all := e.prize("Kaos", 3, e.catID.String())
	r := e.must(e.draw(e.owner, all, map[string]any{"all": true}), 200)
	seen := map[string]bool{}
	for _, w := range winnerList(r) {
		tid := e.ticketOf(w["id"].(string))
		if seen[tid] {
			t.Fatal("tiket menang dua kali")
		}
		seen[tid] = true
	}
	if len(seen) != 3 {
		t.Fatalf("all: %d pemenang", len(seen))
	}
	// Kolam 21K sudah kosong untuk hadiah lain.
	if r := e.draw(e.owner, e.prize("Botol", 1, nil), nil); r.code != 409 || r.body["error"] != "pool_empty" {
		t.Fatalf("sudah menang tidak boleh menang lagi: %d %s", r.code, r.raw)
	}
}

func TestDrawNotOpenAndPoolEmpty(t *testing.T) {
	e := newAttendEnv(t, "Org Belum")
	d := &drawEnv{attendEnv: e}
	p := d.prize("Sepeda", 1, nil)
	// Event 30 hari lagi, jendela bawaan belum dibuka.
	if r := d.draw(e.owner, p, nil); r.code != 409 || r.body["error"] != "draw_not_open" {
		t.Fatalf("draw_not_open: %d %s", r.code, r.raw)
	}
	e.open()
	if r := d.draw(e.owner, p, nil); r.code != 409 || r.body["error"] != "pool_empty" {
		t.Fatalf("pool_empty: %d %s", r.code, r.raw)
	}
}

func TestDrawCountAndIdempotency(t *testing.T) {
	e := newDrawEnv(t, "Org Hitung", 6, 0, 1, 2, 3, 4, 5)
	p := e.prize("Kaos", 3, nil)
	for _, c := range []int{-1, 1001} {
		if r := e.draw(e.owner, p, map[string]any{"count": c}); r.code != 422 || r.field("count") == "" {
			t.Errorf("count %d: %d %s", c, r.code, r.raw)
		}
	}
	if r := e.draw(e.owner, p, map[string]any{"count": 4}); r.code != 409 || r.body["error"] != "prize_exhausted" {
		t.Fatalf("count > sisa: %d %s", r.code, r.raw)
	}
	// Kunci yang sama → hasil sama, tidak mengundi lagi.
	h := map[string]string{"Idempotency-Key": "klik-1"}
	a := e.doH(e.owner, "POST", e.base+"/draw/prizes/"+p+"/draw", nil, h)
	b := e.doH(e.owner, "POST", e.base+"/draw/prizes/"+p+"/draw", nil, h)
	if a.code != 200 || b.code != 200 || b.body["replayed"] != true ||
		winnerList(a)[0]["id"] != winnerList(b)[0]["id"] {
		t.Fatalf("idempoten: %s | %s", a.raw, b.raw)
	}
	var n int
	e.db.QueryRow(context.Background(), `SELECT count(*) FROM draw_winners WHERE prize_id = $1`, p).Scan(&n) //nolint:errcheck
	if n != 1 {
		t.Fatalf("baris pemenang: %d", n)
	}
	// Undi semua sisa (2) → kuota habis.
	r := e.must(e.draw(e.owner, p, map[string]any{"all": true}), 200)
	if len(winnerList(r)) != 2 {
		t.Fatalf("all: %s", r.raw)
	}
	if r := e.draw(e.owner, p, nil); r.code != 409 || r.body["error"] != "prize_exhausted" {
		t.Fatalf("kuota habis: %d %s", r.code, r.raw)
	}
}

func TestDrawConcurrency(t *testing.T) {
	e := newDrawEnv(t, "Org Bersamaan", 4, 0, 1, 2, 3)
	p := e.prize("Satu-satunya", 1, nil)
	var oks, rejected atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := e.draw([]uuid.UUID{e.owner, e.admin}[i%2], p, nil); r.code == 200 {
				oks.Add(1)
			} else if r.body["error"] == "prize_exhausted" {
				rejected.Add(1)
			} else {
				t.Errorf("tak terduga: %d %s", r.code, r.raw)
			}
		}()
	}
	wg.Wait()
	if oks.Load() != 1 || rejected.Load() != 7 {
		t.Fatalf("8 permintaan bersamaan, kuota 1: ok=%d ditolak=%d", oks.Load(), rejected.Load())
	}

	// Satu peserta hadir, dua hadiah paralel → tiket tidak menang dua hadiah.
	e2 := newDrawEnv(t, "Org Satu", 1, 0)
	pa, pb := e2.prize("Hadiah A", 1, nil), e2.prize("Hadiah B", 1, nil)
	var win atomic.Int32
	for _, pz := range []string{pa, pb} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := e2.draw(e2.owner, pz, nil); r.code == 200 {
				win.Add(1)
			} else if r.body["error"] != "pool_empty" {
				t.Errorf("tak terduga: %d %s", r.code, r.raw)
			}
		}()
	}
	wg.Wait()
	if win.Load() != 1 {
		t.Fatalf("tiket menang %d kali", win.Load())
	}
}

func TestDrawClaimForfeitAppendOnly(t *testing.T) {
	e := newDrawEnv(t, "Org Status", 3, 0, 1, 2)
	ctx := context.Background()
	p := e.prize("Sepeda", 1, nil)
	first := winnerList(e.must(e.draw(e.owner, p, nil), 200))[0]
	wid := first["id"].(string)
	base := e.base + "/draw/winners/" + wid

	// Pengaturan terkunci setelah pemenang pertama; draw_public tetap boleh.
	if r := e.do(e.owner, "PUT", e.base+"/draw/settings", map[string]any{"draw_eligibility": "collected", "allow_multiple_wins": false, "draw_public": false}); r.code != 409 || r.body["error"] != "settings_locked" {
		t.Fatalf("settings_locked: %d %s", r.code, r.raw)
	}
	e.must(e.do(e.owner, "PUT", e.base+"/draw/settings", map[string]any{"draw_eligibility": "attended", "allow_multiple_wins": false, "draw_public": true}), 200)
	// Hadiah bersuara: hapus ditolak, kurangi di bawah pemenang aktif ditolak.
	if r := e.do(e.owner, "DELETE", e.base+"/draw/prizes/"+p, nil); r.code != 409 || r.body["error"] != "prize_has_winners" {
		t.Fatalf("hapus hadiah bermenang: %d %s", r.code, r.raw)
	}
	if r := e.do(e.owner, "PUT", e.base+"/draw/prizes/"+p, map[string]any{"name": "Sepeda", "quantity": 0}); r.code != 422 {
		t.Fatalf("quantity 0: %d", r.code)
	}
	// Forfeit wajib alasan; peran.
	if r := e.do(e.owner, "POST", base+"/forfeit", map[string]any{"reason": "ab"}); r.code != 422 || r.field("reason") == "" {
		t.Fatalf("alasan forfeit: %d %s", r.code, r.raw)
	}
	for user, want := range map[uuid.UUID]int{e.staff: 403, e.fin: 403, e.stranger: 404} {
		if r := e.do(user, "POST", base+"/claim", nil); r.code != want {
			t.Errorf("claim oleh %v: %d mau %d", user, r.code, want)
		}
	}
	e.must(e.do(e.admin, "POST", base+"/forfeit", map[string]any{"reason": "Tidak hadir di panggung"}), 200)
	if r := e.do(e.admin, "POST", base+"/claim", nil); r.code != 409 || r.body["error"] != "invalid_status" {
		t.Fatalf("status final: %d %s", r.code, r.raw)
	}
	// Pengganti = baris baru (seq 2), peserta gugur tidak masuk lagi (satu-menang: sudah pernah menang).
	repl := winnerList(e.must(e.draw(e.owner, p, nil), 200))[0]
	if repl["seq"] != float64(2) || e.ticketOf(repl["id"].(string)) == e.ticketOf(wid) {
		t.Fatalf("pengganti: %v", repl)
	}
	e.must(e.do(e.owner, "POST", e.base+"/draw/winners/"+repl["id"].(string)+"/claim", nil), 200)
	// Kuota 1 sudah terpenuhi oleh pengganti yang diterima.
	if r := e.draw(e.owner, p, nil); r.code != 409 || r.body["error"] != "prize_exhausted" {
		t.Fatalf("kuota: %d %s", r.code, r.raw)
	}
	var n int
	e.db.QueryRow(ctx, `SELECT count(*) FROM draw_winners WHERE prize_id = $1`, p).Scan(&n) //nolint:errcheck
	if n != 2 {
		t.Fatalf("baris pemenang: %d", n)
	}
	// Append-only: DELETE dan UPDATE kolom inti ditolak trigger.
	if _, err := e.db.Exec(ctx, `DELETE FROM draw_winners WHERE id = $1`, wid); err == nil {
		t.Error("DELETE pemenang seharusnya ditolak")
	}
	other := e.ids[0]
	if other == e.ticketOf(wid) {
		other = e.ids[1]
	}
	if _, err := e.db.Exec(ctx, `UPDATE draw_winners SET ticket_id = $2 WHERE id = $1`, wid, other); err == nil {
		t.Error("UPDATE ticket_id pemenang seharusnya ditolak")
	}
	// Audit: tanpa nama peserta.
	var acts []string
	rows, _ := e.db.Query(ctx, `SELECT action FROM audit_logs WHERE org_id = $1 AND action LIKE 'draw.%' ORDER BY id`, e.orgID)
	for rows.Next() {
		var a string
		rows.Scan(&a) //nolint:errcheck
		acts = append(acts, a)
	}
	rows.Close()
	if got := strings.Join(acts, ","); !strings.Contains(got, "draw.prize.created,draw.drawn,draw.settings.changed,draw.forfeited,draw.drawn,draw.claimed") {
		t.Fatalf("audit: %s", got)
	}
	var blob string
	e.db.QueryRow(ctx, `SELECT string_agg(coalesce(before::text,'') || after::text, ' ') FROM audit_logs WHERE org_id = $1 AND action LIKE 'draw.%'`, e.orgID).Scan(&blob) //nolint:errcheck
	if strings.Contains(blob, "Peserta ") || strings.Contains(blob, "peserta@uji.test") {
		t.Fatalf("audit undian memuat data pribadi: %s", blob)
	}
}

func TestDrawPrizeCRUDAndRoles(t *testing.T) {
	e := newDrawEnv(t, "Org Peran", 2, 0, 1)
	for _, bad := range []map[string]any{{"name": "x", "quantity": 1}, {"name": "Hadiah", "quantity": 0}, {"name": "Hadiah", "quantity": 1001}, {"name": "Hadiah", "quantity": 1, "category_id": uuid.NewString()}} {
		if r := e.do(e.owner, "POST", e.base+"/draw/prizes", bad); r.code != 422 || len(r.body["fields"].(map[string]any)) == 0 {
			t.Errorf("validasi %v: %d %s", bad, r.code, r.raw)
		}
	}
	p := e.prize("Sepeda", 2, nil)
	e.must(e.do(e.admin, "PUT", e.base+"/draw/prizes/"+p, map[string]any{"name": "Sepeda Lipat", "quantity": 3}), 200)
	extra := e.prize("Hapus saya", 1, nil)
	e.must(e.do(e.owner, "DELETE", e.base+"/draw/prizes/"+extra, nil), 204)

	type call struct{ method, path string }
	reads := []call{{"GET", "/draw/settings"}, {"GET", "/draw/prizes"}, {"GET", "/draw/winners"}}
	writes := []call{{"GET", "/draw/pool"}, {"POST", "/draw/prizes/" + p + "/draw"}, {"GET", "/draw/results.csv"}, {"POST", "/draw/prizes"}, {"PUT", "/draw/settings"}}
	for _, c := range reads {
		for user, want := range map[uuid.UUID]int{e.owner: 200, e.admin: 200, e.staff: 200, e.fin: 403, e.stranger: 404, uuid.Nil: 401} {
			if r := e.do(user, c.method, e.base+c.path, nil); r.code != want {
				t.Errorf("%s %s oleh %v: %d mau %d", c.method, c.path, user, r.code, want)
			}
		}
	}
	for _, c := range writes {
		for user, want := range map[uuid.UUID]int{e.staff: 403, e.fin: 403, e.stranger: 404, uuid.Nil: 401} {
			if r := e.do(user, c.method, e.base+c.path, map[string]any{}); r.code != want {
				t.Errorf("%s %s oleh %v: %d mau %d", c.method, c.path, user, r.code, want)
			}
		}
	}
	_, _, ownerB := e.verifiedOrg("Org Peran B", "5555666677")
	if r := e.do(ownerB, "GET", e.base+"/draw/prizes", nil); r.code != 404 {
		t.Errorf("org lain: %d", r.code)
	}
}

func TestDrawPoolHashAndCSVAndPublic(t *testing.T) {
	e := newDrawEnv(t, "Org Audit", 5, 0, 1, 2, 3)
	ctx := context.Background()
	p := e.prize("Sepeda", 2, nil)
	r := e.must(e.draw(e.owner, p, map[string]any{"all": true}), 200)
	ws := winnerList(r)
	if len(ws) != 2 {
		t.Fatal(r.raw)
	}
	// pool_hash: kolam saat itu = 4 tiket hadir, direproduksi dari data absen.
	var want []uuid.UUID
	for _, s := range e.ids[:4] {
		want = append(want, uuid.MustParse(s))
	}
	for _, w := range ws {
		if w["pool_hash"] != draw.PoolHash(want) || w["pool_size"] != float64(4) {
			t.Fatalf("pool_hash/pool_size: %v", w)
		}
		if tid := e.ticketOf(w["id"].(string)); !strings.Contains(strings.Join(e.ids[:4], ","), tid) {
			t.Fatalf("pemenang di luar kolam: %s", tid)
		}
	}

	// CSV: peran, kolom, dan audit.
	for user, code := range map[uuid.UUID]int{e.staff: 403, e.fin: 403, e.stranger: 404} {
		if r := e.do(user, "GET", e.base+"/draw/results.csv", nil); r.code != code {
			t.Errorf("csv oleh %v: %d", user, r.code)
		}
	}
	c := e.must(e.do(e.admin, "GET", e.base+"/draw/results.csv", nil), 200)
	cr := csv.NewReader(strings.NewReader(strings.TrimPrefix(c.raw, "\ufeff")))
	cr.Comma = ';'
	rows, err := cr.ReadAll()
	if err != nil || len(rows) != 3 || rows[0][0] != "Hadiah" || rows[0][7] != "Ukuran kolam" || rows[1][7] != "4" {
		t.Fatalf("csv: %v %q", err, c.raw)
	}
	var n int
	e.db.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'draw.results.downloaded' AND org_id = $1`, e.orgID).Scan(&n) //nolint:errcheck
	if n != 1 {
		t.Fatalf("audit unduhan: %d", n)
	}

	// Publik: 404 saat mati; hidup → hanya BIB + nama depan + kategori, noindex.
	pub := "/v1/events/" + e.evID.String() + "/draw-results"
	e.db.Exec(ctx, `UPDATE events SET status = 'published' WHERE id = $1`, e.evID) //nolint:errcheck
	if r := e.do(uuid.Nil, "GET", pub, nil); r.code != 404 {
		t.Fatalf("publik mati: %d", r.code)
	}
	e.must(e.do(e.owner, "PUT", e.base+"/draw/settings", map[string]any{"draw_eligibility": "attended", "allow_multiple_wins": false, "draw_public": true}), 200)
	pr := e.must(e.do(uuid.Nil, "GET", pub, nil), 200)
	if !strings.Contains(pr.hdr.Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("X-Robots-Tag hasil publik: %q", pr.hdr.Get("X-Robots-Tag"))
	}
	for _, bad := range []string{"@", "peserta@uji.test", "Peserta 0", "email", "phone", "id_number", "full_name", "pool_hash"} {
		if strings.Contains(pr.raw, bad) {
			t.Fatalf("hasil publik memuat %q: %s", bad, pr.raw)
		}
	}
	if !strings.Contains(pr.raw, `"first_name":"Peserta"`) || !strings.Contains(pr.raw, `"bib"`) {
		t.Fatalf("hasil publik: %s", pr.raw)
	}
	if r := e.do(uuid.Nil, "GET", "/v1/events/"+uuid.NewString()+"/draw-results", nil); r.code != 404 {
		t.Fatalf("event asing: %d", r.code)
	}
}
