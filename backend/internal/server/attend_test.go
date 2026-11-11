package server

import (
	"context"
	"encoding/csv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Uji absen hari-H (Tugas 10, Bagian A) lewat router lengkap + Postgres sungguhan.

// attendEnv: finEnv + pembersih tabel undian (append-only; dihapus dengan session_replication_role = replica
// seperti ledger) yang berjalan SEBELUM pembersih finEnv (Cleanup berjalan terbalik).
type attendEnv struct {
	*finEnv
	slug          string
	orgID, owner  uuid.UUID
	admin, staff  uuid.UUID
	fin, stranger uuid.UUID
	evID, catID   uuid.UUID
	base          string
}

func newAttendEnv(t *testing.T, name string) *attendEnv {
	t.Helper()
	e := &attendEnv{finEnv: newFinEnv(t)}
	t.Cleanup(func() {
		ctx := context.Background()
		orgs := `(SELECT id FROM organizations WHERE created_by IN (SELECT id FROM users WHERE email LIKE $1))`
		err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
			for _, q := range []string{
				`SET LOCAL session_replication_role = replica`,
				`DELETE FROM draw_winners WHERE org_id IN ` + orgs,
				`DELETE FROM draw_prizes WHERE org_id IN ` + orgs,
			} {
				var args []any
				if strings.Contains(q, "$1") {
					args = []any{"%" + e.tag + "@uji.test"}
				}
				if _, err := tx.Exec(ctx, q, args...); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Errorf("bersih-bersih undian: %v", err)
		}
	})
	e.slug, e.orgID, e.owner = e.verifiedOrg(name, finAccount)
	e.evID, e.catID = e.event(e.slug, e.owner, "Lari "+name)
	e.admin, e.staff = e.member(e.orgID, "admin", "admin"), e.member(e.orgID, "staf", "rpc_staff")
	e.fin, e.stranger = e.member(e.orgID, "keu", "finance"), e.user("asing")
	e.base = "/v1/org/" + e.slug + "/events/" + e.evID.String()
	return e
}

// open membuka jendela absen (jam sekarang −1 jam sampai +1 hari).
func (e *attendEnv) open() {
	e.t.Helper()
	now := time.Now()
	e.must(e.do(e.owner, "PUT", e.base+"/attendance/settings", map[string]any{
		"opens_at": now.Add(-time.Hour).Format(time.RFC3339), "closes_at": now.Add(24 * time.Hour).Format(time.RFC3339)}), 200)
}

func (e *attendEnv) attend(user uuid.UUID, token, ticketID string) resp {
	e.t.Helper()
	body := map[string]any{}
	if token != "" {
		body["token"] = token
	}
	if ticketID != "" {
		body["ticket_id"] = ticketID
	}
	return e.do(user, "POST", e.base+"/attend", body)
}

func (e *attendEnv) status(ticketID string) (status string, collected, attended *time.Time) {
	e.t.Helper()
	if err := e.db.QueryRow(context.Background(), `SELECT status, collected_at, attended_at FROM tickets WHERE id = $1`, ticketID).Scan(&status, &collected, &attended); err != nil {
		e.t.Fatal(err)
	}
	return
}

func TestAttendWindowAndRoles(t *testing.T) {
	e := newAttendEnv(t, "Org Absen")
	ids, tokens := e.ticketTokens(e.paidOrder(e.orgID, e.evID, e.catID, e.user("pelari"), 3))

	// Bawaan: event 30 hari lagi → absen belum dibuka, pesan jelas.
	r := e.attend(e.staff, tokens[0], "")
	if r.code != 409 || r.body["error"] != "attend_not_open" || !strings.Contains(r.raw, "WIB") {
		t.Fatalf("di luar jendela: %d %s", r.code, r.raw)
	}
	if st, _, att := e.status(ids[0]); st != "issued" || att != nil {
		t.Fatalf("absen di luar jendela mengubah tiket: %s %v", st, att)
	}
	// Jendela bawaan dihitung dari tanggal lomba WIB: hari lomba = buka, lusa 00:00 WIB = tutup.
	var opens, closes time.Time
	e.db.QueryRow(context.Background(), `SELECT (race_date::timestamp AT TIME ZONE 'Asia/Jakarta'), (race_date::timestamp AT TIME ZONE 'Asia/Jakarta') + interval '2 days' FROM events WHERE id = $1`, e.evID).Scan(&opens, &closes) //nolint:errcheck
	s := e.must(e.do(e.staff, "GET", e.base+"/attendance/settings", nil), 200)
	gotO, _ := time.Parse(time.RFC3339, s.body["effective_opens_at"].(string))
	gotC, _ := time.Parse(time.RFC3339, s.body["effective_closes_at"].(string))
	if !gotO.Equal(opens) || !gotC.Equal(closes) || s.body["opens_at"] != nil || s.body["open"] != false {
		t.Fatalf("jendela bawaan: %s (mau %v – %v)", s.raw, opens, closes)
	}
	// Event hari lomba: absen boleh sepanjang hari lomba dan hari berikutnya, tidak sebelum/sesudahnya.
	for days, want := range map[int]int{0: 200, -1: 200, 1: 409, -2: 409} {
		e.db.Exec(context.Background(), `UPDATE events SET race_date = (now() AT TIME ZONE 'Asia/Jakarta')::date + $2::int WHERE id = $1`, e.evID, days) //nolint:errcheck
		if r := e.attend(e.staff, "", ids[2]); r.code != want {
			t.Errorf("race_date hari %+d: %d, mau %d (%s)", days, r.code, want, r.raw)
		}
		e.db.Exec(context.Background(), `UPDATE tickets SET attended_at = NULL, attended_by = NULL WHERE id = $1`, ids[2]) //nolint:errcheck
	}
	e.db.Exec(context.Background(), `UPDATE events SET race_date = current_date + 30 WHERE id = $1`, e.evID) //nolint:errcheck

	e.open()
	// Peran & tenant.
	for user, want := range map[uuid.UUID]int{e.fin: 403, e.stranger: 404} {
		if r := e.attend(user, tokens[0], ""); r.code != want {
			t.Errorf("attend oleh %v: %d, mau %d", user, r.code, want)
		}
	}
	if r := e.attend(uuid.Nil, tokens[0], ""); r.code != 401 {
		t.Errorf("anonim: %d", r.code)
	}
	_, _, ownerB := e.verifiedOrg("Org Absen B", "5555666677")
	if r := e.do(ownerB, "POST", e.base+"/attend", map[string]any{"token": tokens[0]}); r.code != 404 {
		t.Errorf("event org lain: %d", r.code)
	}
	// rpc_staff, admin, owner masing-masing boleh.
	for i, u := range []uuid.UUID{e.staff, e.admin, e.owner} {
		r := e.attend(u, tokens[i], "")
		if r.code != 200 || r.body["result"] != "ok" {
			t.Fatalf("peran %d: %d %s", i, r.code, r.raw)
		}
	}
}

func TestAttendResultsAndRaceDayIndependence(t *testing.T) {
	e := newAttendEnv(t, "Org Hasil")
	e.open()
	ctx := context.Background()
	ids, tokens := e.ticketTokens(e.paidOrder(e.orgID, e.evID, e.catID, e.user("pelari"), 4))
	// Event lain di org yang sama → wrong_event.
	ev2, cat2 := e.event(e.slug, e.owner, "Lari Lain")
	_, tok2 := e.ticketTokens(e.paidOrder(e.orgID, ev2, cat2, e.user("pelari2"), 1))
	// Refund.
	oRef := e.paidOrder(e.orgID, e.evID, e.catID, e.user("pelari-refund"), 1)
	refIDs, refTokens := e.ticketTokens(oRef)
	e.db.Exec(ctx, `UPDATE tickets SET status = 'refunded' WHERE id = $1`, refIDs[0]) //nolint:errcheck
	e.db.Exec(ctx, `UPDATE tickets SET status = 'cancelled' WHERE id = $1`, ids[3])   //nolint:errcheck

	// hijau, lalu kuning dengan siapa & kapan.
	r := e.attend(e.staff, tokens[0], "")
	tk, _ := r.body["ticket"].(map[string]any)
	if r.body["result"] != "ok" || tk["participant_name"] == nil || tk["bib_number"] == nil || tk["category_code"] == nil {
		t.Fatalf("ok: %s", r.raw)
	}
	r = e.attend(e.admin, tokens[0], "")
	tk, _ = r.body["ticket"].(map[string]any)
	if r.body["result"] != "already_attended" || tk["attended_by_staff"] != "staf" || tk["attended_at"] == nil {
		t.Fatalf("already_attended harus menyebut siapa dan kapan: %s", r.raw)
	}
	// Merah.
	// Rusak satu karakter di tengah (bagian bertanda tangan): selalu mengubah byte, tidak seperti ekor base64.
	mid := len(tokens[1]) / 2
	flip := "A"
	if tokens[1][mid:mid+1] == "A" {
		flip = "B"
	}
	tampered := tokens[1][:mid] + flip + tokens[1][mid+1:]
	for name, c := range map[string]struct{ tok, tid, want string }{
		"palsu":      {tampered, "", "invalid"},
		"sampah":     {"bukan-token", "", "invalid"},
		"event lain": {tok2[0], "", "wrong_event"},
		"refund":     {refTokens[0], "", "refunded"},
		"batal":      {"", ids[3], "cancelled"},
		"tak ada":    {"", uuid.NewString(), "not_found"},
		"id rusak":   {"", "bukan-uuid", "invalid"},
		"kosong":     {"", "", "invalid"},
	} {
		if r := e.attend(e.staff, c.tok, c.tid); r.code != 200 || r.body["result"] != c.want {
			t.Errorf("%s: %d %s, mau %s", name, r.code, r.raw, c.want)
		}
	}
	if _, _, att := e.status(refIDs[0]); att != nil {
		t.Error("tiket refund tidak boleh tercatat hadir")
	}
	if _, _, att := e.status(ids[3]); att != nil {
		t.Error("tiket batal tidak boleh tercatat hadir")
	}

	// Manual berdasarkan ticket_id (peserta yang HP-nya mati) berhasil.
	if r := e.attend(e.staff, "", ids[1]); r.body["result"] != "ok" {
		t.Fatalf("manual: %s", r.raw)
	}

	// Absen tidak mengubah status/collected_*; hadir tanpa ambil pack tetap 'issued'.
	if st, col, att := e.status(ids[0]); st != "issued" || col != nil || att == nil {
		t.Fatalf("absen mengubah tiket: %s %v %v", st, col, att)
	}
	// Mode ambil race pack tidak berubah: tiket yang sudah hadir tetap bisa diambil pack-nya (dan sebaliknya).
	if r := e.do(e.staff, "POST", e.base+"/scan", map[string]any{"token": tokens[0]}); r.body["result"] != "ok" {
		t.Fatalf("ambil pack setelah absen: %s", r.raw)
	}
	if r := e.do(e.staff, "POST", e.base+"/scan", map[string]any{"token": tokens[2]}); r.body["result"] != "ok" {
		t.Fatalf("ambil pack: %s", r.raw)
	}
	if st, col, att := e.status(ids[2]); st != "collected" || col == nil || att != nil {
		t.Fatalf("ambil pack tidak boleh mengabsen: %s %v %v", st, col, att)
	}
	if r := e.attend(e.staff, tokens[2], ""); r.body["result"] != "ok" {
		t.Fatalf("absen setelah ambil pack: %s", r.raw)
	}
	if st, _, _ := e.status(ids[2]); st != "collected" {
		t.Fatalf("status berubah jadi %s", st)
	}

	// Progres & log: stats.attended; scan_logs.kind.
	lst := e.must(e.do(e.staff, "GET", e.base+"/tickets", nil), 200)
	stats := lst.body["stats"].(map[string]any)
	if stats["attended"] != float64(3) || stats["issued"] != float64(3) {
		t.Fatalf("progres hadir: %v", stats)
	}
	var attendLogs, collectLogs int
	e.db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE kind = 'attend'), count(*) FILTER (WHERE kind = 'collect') FROM scan_logs WHERE event_id = $1`, e.evID).Scan(&attendLogs, &collectLogs) //nolint:errcheck
	if attendLogs < 10 || collectLogs != 2 {
		t.Fatalf("scan_logs kind: attend %d collect %d", attendLogs, collectLogs)
	}
	// Pencarian manual menampilkan status hadir.
	q := e.must(e.do(e.staff, "GET", e.base+"/tickets?q=Peserta", nil), 200)
	found := false
	for _, it := range q.body["results"].([]any) {
		if m := it.(map[string]any); m["id"] == ids[0] && m["attended_at"] != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("pencarian tidak memuat attended_at: %s", q.raw)
	}
}

func TestAttendConcurrentSingleOK(t *testing.T) {
	e := newAttendEnv(t, "Org Paralel")
	e.open()
	_, tokens := e.ticketTokens(e.paidOrder(e.orgID, e.evID, e.catID, e.user("pelari"), 1))
	var oks, dups atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch r := e.attend(e.staff, tokens[0], ""); r.body["result"] {
			case "ok":
				oks.Add(1)
			case "already_attended":
				dups.Add(1)
			default:
				t.Errorf("hasil tak terduga: %s", r.raw)
			}
		}()
	}
	wg.Wait()
	if oks.Load() != 1 || dups.Load() != 11 {
		t.Fatalf("12 pemindai bersamaan: ok=%d duplikat=%d", oks.Load(), dups.Load())
	}
}

func TestAttendCancel(t *testing.T) {
	e := newAttendEnv(t, "Org Batal")
	e.open()
	ctx := context.Background()
	ids, tokens := e.ticketTokens(e.paidOrder(e.orgID, e.evID, e.catID, e.user("pelari"), 2))
	e.must(e.attend(e.staff, tokens[0], ""), 200)
	e.must(e.attend(e.staff, tokens[1], ""), 200)
	path := e.base + "/attend/cancel"

	// Peran: rpc_staff, finance, orang asing ditolak.
	for user, want := range map[uuid.UUID]int{e.staff: 403, e.fin: 403, e.stranger: 404} {
		if r := e.do(user, "POST", path, map[string]any{"ticket_id": ids[0], "reason": "Salah scan peserta"}); r.code != want {
			t.Errorf("batal oleh %v: %d, mau %d", user, r.code, want)
		}
	}
	// Alasan wajib 5–300.
	for _, reason := range []string{"", "ab", "  abcd  ", strings.Repeat("x", 301)} {
		if r := e.do(e.admin, "POST", path, map[string]any{"ticket_id": ids[0], "reason": reason}); r.code != 422 || r.field("reason") == "" {
			t.Errorf("alasan %q: %d %s", reason, r.code, r.raw)
		}
	}
	if _, _, att := e.status(ids[0]); att == nil {
		t.Fatal("penolakan tidak boleh menghapus absen")
	}
	// Berhasil + audit (alasan, aktor; tanpa nama peserta).
	e.must(e.do(e.admin, "POST", path, map[string]any{"ticket_id": ids[0], "reason": "Salah scan peserta"}), 200)
	if st, _, att := e.status(ids[0]); att != nil || st != "issued" {
		t.Fatalf("setelah batal: %s %v", st, att)
	}
	var n int
	var after, before string
	e.db.QueryRow(ctx, `SELECT count(*), max(after::text), max(before::text) FROM audit_logs WHERE action = 'attendance.cancelled' AND entity_id = $1 AND actor_id = $2`, ids[0], e.admin).Scan(&n, &after, &before) //nolint:errcheck
	if n != 1 || !strings.Contains(after, "Salah scan peserta") || !strings.Contains(before, "attended_at") || strings.Contains(after+before, "Peserta 0") {
		t.Fatalf("audit batal absen: %d %s %s", n, after, before)
	}
	// Bisa diabsen lagi; batal dua kali → not_attended.
	if r := e.do(e.admin, "POST", path, map[string]any{"ticket_id": ids[0], "reason": "Salah scan peserta"}); r.code != 409 || r.body["error"] != "not_attended" {
		t.Fatalf("batal dua kali: %d %s", r.code, r.raw)
	}
	e.must(e.attend(e.staff, tokens[0], ""), 200)
	// Pemenang aktif → attendance_in_use; setelah gugur (forfeited) boleh dibatalkan.
	var prizeID uuid.UUID
	e.db.QueryRow(ctx, `INSERT INTO draw_prizes (event_id, org_id, name, quantity) VALUES ($1, $2, 'Hadiah uji', 1) RETURNING id`, e.evID, e.orgID).Scan(&prizeID) //nolint:errcheck
	if _, err := e.db.Exec(ctx, `INSERT INTO draw_winners (prize_id, event_id, org_id, ticket_id, seq, drawn_by, pool_size, pool_hash) VALUES ($1, $2, $3, $4, 1, $5, 2, 'x')`,
		prizeID, e.evID, e.orgID, ids[1], e.owner); err != nil {
		t.Fatal(err)
	}
	r := e.do(e.owner, "POST", path, map[string]any{"ticket_id": ids[1], "reason": "Salah scan peserta"})
	if r.code != 409 || r.body["error"] != "attendance_in_use" {
		t.Fatalf("pemenang aktif: %d %s", r.code, r.raw)
	}
	if _, _, att := e.status(ids[1]); att == nil {
		t.Fatal("absen pemenang aktif terhapus")
	}
	e.db.Exec(ctx, `UPDATE draw_winners SET status = 'forfeited', note = 'tidak hadir' WHERE ticket_id = $1`, ids[1]) //nolint:errcheck
	e.must(e.do(e.owner, "POST", path, map[string]any{"ticket_id": ids[1], "reason": "Salah scan peserta"}), 200)
	// Tiket org lain / tak ada → 404.
	if r := e.do(e.owner, "POST", path, map[string]any{"ticket_id": uuid.NewString(), "reason": "Salah scan peserta"}); r.code != 404 {
		t.Fatalf("tiket tak ada: %d", r.code)
	}
}

func TestAttendanceSettings(t *testing.T) {
	e := newAttendEnv(t, "Org Setel")
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	body := map[string]any{"opens_at": now.Format(time.RFC3339), "closes_at": now.Add(2 * time.Hour).Format(time.RFC3339)}
	for user, want := range map[uuid.UUID]int{e.staff: 403, e.fin: 403, e.stranger: 404} {
		if r := e.do(user, "PUT", e.base+"/attendance/settings", body); r.code != want {
			t.Errorf("PUT oleh %v: %d, mau %d", user, r.code, want)
		}
	}
	if r := e.do(e.fin, "GET", e.base+"/attendance/settings", nil); r.code != 403 {
		t.Errorf("GET finance: %d", r.code)
	}
	// Tutup ≤ buka ditolak.
	r := e.do(e.owner, "PUT", e.base+"/attendance/settings", map[string]any{"opens_at": now.Format(time.RFC3339), "closes_at": now.Format(time.RFC3339)})
	if r.code != 422 || r.field("closes_at") == "" {
		t.Fatalf("tutup ≤ buka: %d %s", r.code, r.raw)
	}
	e.must(e.do(e.admin, "PUT", e.base+"/attendance/settings", body), 200)
	g := e.must(e.do(e.staff, "GET", e.base+"/attendance/settings", nil), 200)
	if g.body["open"] != true || g.body["opens_at"] == nil {
		t.Fatalf("setelan: %s", g.raw)
	}
	// null = kembali ke bawaan.
	e.must(e.do(e.owner, "PUT", e.base+"/attendance/settings", map[string]any{"opens_at": nil, "closes_at": nil}), 200)
	if g := e.must(e.do(e.staff, "GET", e.base+"/attendance/settings", nil), 200); g.body["opens_at"] != nil || g.body["open"] != false {
		t.Fatalf("kembali ke bawaan: %s", g.raw)
	}
	var n int
	e.db.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'attendance.settings.changed' AND entity_id = $1`, e.evID.String()).Scan(&n) //nolint:errcheck
	if n != 2 {
		t.Fatalf("audit pengaturan absen: %d", n)
	}
	// Event org lain → 404.
	_, _, ownerB := e.verifiedOrg("Org Setel B", "5555666677")
	if r := e.do(ownerB, "GET", e.base+"/attendance/settings", nil); r.code != 404 {
		t.Fatalf("org lain: %d", r.code)
	}
}

func TestAttendBatchOffline(t *testing.T) {
	e := newAttendEnv(t, "Org Offline Absen")
	e.open()
	ctx := context.Background()
	ids, tokens := e.ticketTokens(e.paidOrder(e.orgID, e.evID, e.catID, e.user("pelari"), 4))
	path := e.base + "/scan/batch"
	devA, devB := uuid.NewString(), uuid.NewString()
	op := func(kind, token string, at time.Time) map[string]any {
		m := map[string]any{"client_op_id": uuid.NewString(), "token": token, "scanned_at": at.Format(time.RFC3339Nano)}
		if kind != "" {
			m["kind"] = kind
		}
		return m
	}
	now := time.Now()

	// Manifest awal memuat jendela absen dan tanpa kunci absen pada tiket yang belum hadir.
	_, raw0, base := e.getManifest(e.staff, e.base+"/scan-manifest")
	if !strings.Contains(raw0, `"attendance_window"`) {
		t.Fatalf("manifest tanpa jendela absen: %.200s", raw0)
	}
	for _, tk := range base.Tickets {
		if tk["attended_at"] != nil {
			t.Fatalf("belum ada yang hadir: %v", tk)
		}
	}
	e.db.Exec(ctx, `UPDATE tickets SET updated_at = now() - interval '10 minutes' WHERE event_id = $1`, e.evID) //nolint:errcheck

	// Idempoten per client_op_id.
	first := op("attend", tokens[0], now)
	for i := 0; i < 2; i++ {
		code, r, raw := e.batch(e.staff, path, devA, first)
		if code != 200 || r.Results[0].Result != "ok" || r.Results[0].ClientOpID != first["client_op_id"] {
			t.Fatalf("kirim ke-%d: %s", i+1, raw)
		}
	}
	var n int
	var kind string
	e.db.QueryRow(ctx, `SELECT count(*), max(kind) FROM scan_logs WHERE client_op_id = $1`, first["client_op_id"]).Scan(&n, &kind) //nolint:errcheck
	if n != 1 || kind != "attend" {
		t.Fatalf("log: %d %s", n, kind)
	}
	if _, _, att := e.status(ids[0]); att == nil {
		t.Fatal("absen offline tidak tercatat")
	}

	// Dua perangkat memindai tiket yang sama bersamaan → tepat satu ok, sisanya bentrokan (Perlu ditinjau).
	var oks, conflicts atomic.Int32
	var wg sync.WaitGroup
	for _, d := range []string{devA, devB, uuid.NewString(), uuid.NewString()} {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			_, r, raw := e.batch(e.staff, path, d, op("attend", tokens[1], now))
			switch r.Results[0].Result {
			case "ok":
				oks.Add(1)
			case "already_attended":
				if r.Results[0].Ticket["attended_by_staff"] != "staf" || r.Results[0].Ticket["attended_at"] == nil {
					t.Errorf("bentrokan tanpa siapa/kapan: %s", raw)
				}
				conflicts.Add(1)
			default:
				t.Errorf("hasil tak terduga: %s", raw)
			}
		}(d)
	}
	wg.Wait()
	if oks.Load() != 1 || conflicts.Load() != 3 {
		t.Fatalf("empat perangkat: ok=%d bentrok=%d", oks.Load(), conflicts.Load())
	}

	// Di luar jendela (jam perangkat sebelum buka) ditolak; kind ngawur ditolak; pengambilan pack tetap bekerja.
	_, r, raw := e.batch(e.staff, path, devA, op("attend", tokens[2], now.Add(-48*time.Hour)), op("makan", tokens[2], now), op("", tokens[2], now))
	if r.Results[0].Result != "attend_not_open" || r.Results[1].Result != "invalid" || r.Results[2].Result != "ok" {
		t.Fatalf("jendela/kind/collect: %s", raw)
	}
	if st, col, att := e.status(ids[2]); st != "collected" || col == nil || att != nil {
		t.Fatalf("collect via batch harus tetap mode ambil pack: %s %v %v", st, col, att)
	}
	// Refund/palsu → merah, tanpa efek.
	e.db.Exec(ctx, `UPDATE tickets SET status = 'refunded' WHERE id = $1`, ids[3]) //nolint:errcheck
	_, r, raw = e.batch(e.staff, path, devA, op("attend", tokens[3], now), op("attend", "palsu", now))
	if r.Results[0].Result != "refunded" || r.Results[1].Result != "invalid" {
		t.Fatalf("merah: %s", raw)
	}
	if _, _, att := e.status(ids[3]); att != nil {
		t.Fatal("refund tercatat hadir")
	}

	// Manifest inkremental memuat absen (attended_at + nama staf), tanpa data pribadi tambahan.
	_, rawInc, inc := e.getManifest(e.staff, e.base+"/scan-manifest?since="+base.Version)
	byID := map[string]map[string]any{}
	for _, tk := range inc.Tickets {
		byID[tk["id"].(string)] = tk
	}
	for _, id := range ids[:2] {
		if byID[id]["attended_at"] == nil || byID[id]["attended_by"] != "staf" {
			t.Fatalf("inkremental tanpa absen untuk %s: %v", id, byID[id])
		}
	}
	allowed := map[string]bool{"id": true, "bib": true, "name": true, "category": true, "jersey": true, "status": true, "collected_at": true,
		"collected_by": true, "picked_up_by": true, "attended_at": true, "attended_by": true}
	for _, tk := range inc.Tickets {
		for k := range tk {
			if !allowed[k] {
				t.Fatalf("kunci tak terduga di manifest: %q", k)
			}
		}
	}
	for _, s := range []string{finNIK, "peserta@uji.test"} {
		if strings.Contains(rawInc, s) {
			t.Fatalf("manifest memuat data pribadi %q", s)
		}
	}
	// Peran: finance ditolak, org lain 404.
	_, _, ownerB := e.verifiedOrg("Org Offline B", "5555666677")
	for user, want := range map[uuid.UUID]int{e.fin: 403, ownerB: 404} {
		if code, _, _ := e.batch(user, path, devA, op("attend", tokens[0], now)); code != want {
			t.Errorf("batch absen oleh %v: %d, mau %d", user, code, want)
		}
	}
}

func TestParticipantsAttended(t *testing.T) {
	e := newAttendEnv(t, "Org Peserta Hadir")
	e.open()
	_, tokens := e.ticketTokens(e.paidOrder(e.orgID, e.evID, e.catID, e.user("pelari"), 3))
	e.must(e.attend(e.staff, tokens[0], ""), 200)
	e.must(e.attend(e.staff, tokens[1], ""), 200)
	sum := e.must(e.do(e.staff, "GET", e.base+"/participants/summary", nil), 200)
	if sum.body["attended"] != float64(2) {
		t.Fatalf("ringkasan hadir: %s", sum.raw)
	}
	byCat := sum.body["by_category"].([]any)[0].(map[string]any)
	if byCat["attended"] != float64(2) || byCat["count"] != float64(3) {
		t.Fatalf("hadir per kategori: %v", byCat)
	}
	for filter, want := range map[string]int{"yes": 2, "no": 1, "all": 3, "": 3} {
		l := e.must(e.do(e.owner, "GET", e.base+"/participants?attended="+filter, nil), 200)
		if l.body["total"] != float64(want) {
			t.Errorf("filter attended=%q: total %v, mau %d", filter, l.body["total"], want)
		}
	}
	if r := e.do(e.owner, "GET", e.base+"/participants?attended=mungkin", nil); r.code != 422 {
		t.Errorf("filter tidak valid: %d", r.code)
	}
	l := e.must(e.do(e.owner, "GET", e.base+"/participants?attended=yes", nil), 200)
	for _, it := range l.body["items"].([]any) {
		if it.(map[string]any)["attended_at"] == nil {
			t.Fatalf("item hadir tanpa attended_at: %v", it)
		}
	}
	// CSV: kolom baru + waktu hadir; filter berlaku; sel tetap aman (diuji di participants_test).
	r := e.do(e.owner, "GET", e.base+"/participants.csv", nil)
	cr := csv.NewReader(strings.NewReader(strings.TrimPrefix(r.raw, "\uFEFF")))
	cr.Comma = ';'
	rows, err := cr.ReadAll()
	if err != nil || len(rows) != 4 {
		t.Fatalf("CSV: %v %d baris\n%s", err, len(rows), r.raw)
	}
	last := len(rows[0]) - 1
	if rows[0][last] != "Hadir hari-H (WIB)" {
		t.Fatalf("kolom terakhir: %q", rows[0][last])
	}
	var hadir, belum int
	for _, row := range rows[1:] {
		if row[last] == "" {
			belum++
		} else if len(row[last]) == len("2026-01-02 15:04") {
			hadir++
		}
	}
	if hadir != 2 || belum != 1 {
		t.Fatalf("CSV hadir %d belum %d\n%s", hadir, belum, r.raw)
	}
}
