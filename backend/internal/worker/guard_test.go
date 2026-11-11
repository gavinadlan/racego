package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/checkout"
	"github.com/gavinadlan/racego/backend/internal/metrics"
	"github.com/gavinadlan/racego/backend/internal/notify"
	"github.com/gavinadlan/racego/backend/internal/ticket"
)

// Pagar kuota di database (Tugas 11): apa pun keadaan Redis, tiket terbit ≤ kuota di database.

type guardEnv struct {
	t     *testing.T
	db    *pgxpool.Pool
	mr    *miniredis.Miniredis
	inv   *checkout.Inventory
	w     *Worker
	org   uuid.UUID
	event uuid.UUID
	cat   uuid.UUID
	user  uuid.UUID
}

func newGuardEnv(t *testing.T, quota int) *guardEnv {
	t.Helper()
	dsn := os.Getenv("RACEGO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RACEGO_TEST_DATABASE_URL kosong")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	inv := checkout.NewInventory(rdb)
	signer, err := ticket.NewSigner("", true)
	if err != nil {
		t.Fatal(err)
	}
	e := &guardEnv{t: t, db: db, mr: mr, inv: inv, w: New(db, inv, signer, notify.NewMailer("127.0.0.1:1", "", "", "RaceGo <x@uji.test>"), "http://x")}
	tag := uuid.NewString()[:8]
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.QueryRow(ctx, `INSERT INTO users (email, name) VALUES ($1, 'Uji Pagar') RETURNING id`, "pagar-"+tag+"@uji.test").Scan(&e.user))
	must(db.QueryRow(ctx, `INSERT INTO organizations (slug, name, created_by) VALUES ($1, 'Uji Pagar', $2) RETURNING id`, "pagar-"+tag, e.user).Scan(&e.org))
	must(db.QueryRow(ctx, `INSERT INTO events (org_id, slug, name, race_date, sale_open_at, sale_close_at, status) VALUES ($1, $2, 'Uji Pagar', current_date + 30, now() - interval '1 day', now() + interval '10 days', 'published') RETURNING id`, e.org, "ev-"+tag).Scan(&e.event))
	must(db.QueryRow(ctx, `INSERT INTO ticket_categories (event_id, org_id, code, name, distance_km, price, quota, bib_prefix) VALUES ($1, $2, '10K', '10K', 10, 100000, $3, '10') RETURNING id`, e.event, e.org, quota).Scan(&e.cat))
	t.Cleanup(func() {
		type stmt struct {
			sql string
			arg uuid.UUID
		}
		err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil { // ledger append-only
				return err
			}
			ord := `(SELECT id::text FROM orders WHERE org_id = $1)`
			for _, st := range []stmt{
				{`DELETE FROM ledger_entries WHERE journal_id IN (SELECT id FROM ledger_journals WHERE ref_id IN ` + ord + `)`, e.org},
				{`DELETE FROM ledger_journals WHERE ref_id IN ` + ord, e.org},
				{`DELETE FROM outbox WHERE payload->>'order_id' IN ` + ord, e.org},
				{`DELETE FROM tickets WHERE org_id = $1`, e.org},
				{`DELETE FROM participants WHERE org_id = $1`, e.org},
				{`DELETE FROM orders WHERE org_id = $1`, e.org},
				{`DELETE FROM ticket_categories WHERE org_id = $1`, e.org},
				{`DELETE FROM events WHERE org_id = $1`, e.org},
				{`DELETE FROM org_members WHERE org_id = $1`, e.org},
				{`DELETE FROM organizations WHERE id = $1`, e.org},
				{`DELETE FROM users WHERE id = $1`, e.user},
			} {
				if _, err := tx.Exec(ctx, st.sql, st.arg); err != nil {
					return fmt.Errorf("%.40s: %w", st.sql, err)
				}
			}
			return nil
		})
		if err != nil {
			t.Errorf("bersih-bersih: %v", err)
		}
		db.Close()
	})
	return e
}

// rejections membaca nilai counter metrik penolakan pagar.
func rejections() float64 {
	var m dto.Metric
	_ = metrics.StockGuardRejections.Write(&m)
	return m.GetCounter().GetValue()
}

// order membuat order lunas dengan n peserta + pesan outbox order.paid.
func (e *guardEnv) order(n int) uuid.UUID {
	e.t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO orders (org_id, event_id, category_id, buyer_id, reservation_id, idempotency_key, qty, subtotal, platform_fee, total, status, expires_at, paid_at)
			VALUES ($1, $2, $3, $4, $5, $5, $6, $7, 0, $7, 'paid', now(), now()) RETURNING id`,
			e.org, e.event, e.cat, e.user, uuid.NewString(), n, 100000*n).Scan(&id); err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			if _, err := tx.Exec(ctx, `INSERT INTO participants (order_id, org_id, event_id, category_id, full_name, email, id_number, jersey_size)
				VALUES ($1, $2, $3, $4, 'Peserta Uji', 'p@uji.test', '3170000000000001', 'M')`, id, e.org, e.event, e.cat); err != nil {
				return err
			}
		}
		return e.enqueue(tx, id)
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *guardEnv) enqueue(tx pgx.Tx, id uuid.UUID) error {
	payload, _ := json.Marshal(map[string]any{"order_id": id, "payment_type": "bank_transfer"})
	_, err := tx.Exec(context.Background(), `INSERT INTO outbox (topic, payload) VALUES ('order.paid', $1)`, payload)
	return err
}

// drain: delapan pekerja memproses outbox bersamaan (SKIP LOCKED), seperti beberapa instance worker.
func (e *guardEnv) drain(workers int) {
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); e.w.drainOutbox(context.Background()) }()
	}
	wg.Wait()
}

type outcome struct {
	tickets, needsRefund, paidNoTicket, journals int
	bibs                                         []string
}

func (e *guardEnv) outcome() outcome {
	e.t.Helper()
	ctx := context.Background()
	var o outcome
	q := func(sql string, dst *int) {
		if err := e.db.QueryRow(ctx, sql, e.cat).Scan(dst); err != nil {
			e.t.Fatal(err)
		}
	}
	q(`SELECT count(*) FROM tickets WHERE category_id = $1`, &o.tickets)
	q(`SELECT count(*) FROM orders WHERE category_id = $1 AND status = 'needs_refund'`, &o.needsRefund)
	q(`SELECT count(*) FROM orders o WHERE category_id = $1 AND status = 'paid' AND NOT EXISTS (SELECT 1 FROM tickets t WHERE t.order_id = o.id)`, &o.paidNoTicket)
	q(`SELECT count(*) FROM ledger_journals WHERE ref_id IN (SELECT id::text FROM orders WHERE category_id = $1)`, &o.journals)
	rows, err := e.db.Query(ctx, `SELECT bib_number FROM tickets WHERE category_id = $1 ORDER BY bib_number`, e.cat)
	if err != nil {
		e.t.Fatal(err)
	}
	o.bibs, _ = pgx.CollectRows(rows, pgx.RowTo[string])
	return o
}

func (e *guardEnv) looseRedis() {
	e.t.Helper()
	if _, err := e.inv.Load(context.Background(), e.cat.String(), 1000); err != nil { // Redis "longgar": mengizinkan 1000 slot
		e.t.Fatal(err)
	}
}

func contiguous(t *testing.T, bibs []string) {
	t.Helper()
	want := make([]string, len(bibs))
	for i := range bibs {
		want[i] = fmt.Sprintf("10%04d", i+1)
	}
	sort.Strings(bibs)
	for i := range bibs {
		if bibs[i] != want[i] {
			t.Fatalf("BIB tidak berurutan/ganda/lompat: %v", bibs)
		}
	}
}

func TestGuardRejectsAfterQuotaFull(t *testing.T) {
	e := newGuardEnv(t, 5)
	e.looseRedis()
	before := rejections()
	for i := 0; i < 5; i++ {
		e.order(1)
	}
	e.drain(1)
	if o := e.outcome(); o.tickets != 5 || o.needsRefund != 0 {
		t.Fatalf("lima order pertama: %+v", o)
	}
	sixth := e.order(1)
	e.drain(1)
	o := e.outcome()
	if o.tickets != 5 || o.needsRefund != 1 || o.journals != 5 {
		t.Fatalf("order keenam harus needs_refund tanpa tiket/jurnal: %+v", o)
	}
	var tickets int
	e.db.QueryRow(context.Background(), `SELECT count(*) FROM tickets WHERE order_id = $1`, sixth).Scan(&tickets) //nolint:errcheck
	if tickets != 0 {
		t.Fatal("order ditolak punya tiket")
	}
	if d := rejections() - before; d != 1 {
		t.Fatalf("metrik penolakan bertambah %v, mau 1", d)
	}
	contiguous(t, o.bibs)
}

func TestGuardNeverIssuesPartialOrder(t *testing.T) {
	e := newGuardEnv(t, 5)
	e.looseRedis()
	for i := 0; i < 3; i++ {
		e.order(1)
	}
	multi := e.order(3) // sisa kuota 2: tiga peserta tidak muat, tidak boleh terbit sebagian
	e.drain(1)
	o := e.outcome()
	if o.tickets != 3 || o.needsRefund != 1 {
		t.Fatalf("order multi-peserta: %+v", o)
	}
	var n int
	e.db.QueryRow(context.Background(), `SELECT count(*) FROM tickets WHERE order_id = $1`, multi).Scan(&n) //nolint:errcheck
	if n != 0 {
		t.Fatalf("order ditolak menerbitkan %d tiket parsial", n)
	}
	e.order(2) // order yang muat tetap berhasil setelah penolakan
	e.drain(1)
	o = e.outcome()
	if o.tickets != 5 {
		t.Fatalf("order yang muat: %+v", o)
	}
	contiguous(t, o.bibs)
}

// Redis rusak: stok longgar (Confirm selalu mengizinkan). Dua belas order lunas paralel, kuota 3 → tepat 3 tiket.
func TestGuardBrokenRedisParallel(t *testing.T) {
	for run := 0; run < 20; run++ {
		t.Run(fmt.Sprintf("run%02d", run), func(t *testing.T) {
			e := newGuardEnv(t, 3)
			loose := run%2 == 0
			if loose {
				e.looseRedis()
			} // selain itu Redis kosong (FLUSHALL): Confirm menjawab refund
			for i := 0; i < 12; i++ {
				e.order(1)
			}
			e.drain(8)
			o := e.outcome()
			if o.tickets > 3 {
				t.Fatalf("tiket %d > kuota 3 (%+v)", o.tickets, o)
			}
			if loose && (o.tickets != 3 || o.needsRefund != 9 || o.paidNoTicket != 0) {
				t.Fatalf("%+v", o)
			}
			if o.tickets+o.needsRefund != 12 {
				t.Fatalf("ada order yang hilang: %+v", o)
			}
			contiguous(t, o.bibs)
		})
	}
}

// Pesan order.paid yang sama diproses dua kali (berurutan dan paralel): satu penerbitan, satu needs_refund.
func TestGuardIdempotentReprocess(t *testing.T) {
	e := newGuardEnv(t, 1)
	e.looseRedis()
	e.order(1)
	rejected := e.order(1)
	e.drain(1)
	for i := 0; i < 3; i++ { // kirim ulang pesan yang sama untuk kedua order
		for _, id := range []uuid.UUID{rejected} {
			if err := pgx.BeginFunc(context.Background(), e.db, func(tx pgx.Tx) error { return e.enqueue(tx, id) }); err != nil {
				t.Fatal(err)
			}
		}
	}
	var first uuid.UUID
	e.db.QueryRow(context.Background(), `SELECT id FROM orders WHERE category_id = $1 AND status = 'paid'`, e.cat).Scan(&first) //nolint:errcheck
	for i := 0; i < 3 && first != uuid.Nil; i++ {
		pgx.BeginFunc(context.Background(), e.db, func(tx pgx.Tx) error { return e.enqueue(tx, first) }) //nolint:errcheck
	}
	before := rejections()
	e.drain(6)
	o := e.outcome()
	if o.tickets != 1 || o.needsRefund != 1 || o.journals != 1 {
		t.Fatalf("setelah diproses ulang: %+v", o)
	}
	if rejections() != before {
		t.Fatal("penolakan dihitung ulang untuk order yang sudah needs_refund")
	}
}

// Kuota diubah mitra: naik → order berikutnya terbit; turun di bawah tiket yang sudah terbit → ditolak.
func TestGuardHonorsCurrentQuota(t *testing.T) {
	e := newGuardEnv(t, 2)
	e.looseRedis()
	e.order(1)
	e.order(1)
	e.order(1)
	e.drain(1)
	if o := e.outcome(); o.tickets != 2 || o.needsRefund != 1 {
		t.Fatalf("kuota 2: %+v", o)
	}
	e.db.Exec(context.Background(), `UPDATE ticket_categories SET quota = 4 WHERE id = $1`, e.cat) //nolint:errcheck
	e.order(1)
	e.order(1)
	e.order(1)
	e.drain(1)
	if o := e.outcome(); o.tickets != 4 || o.needsRefund != 2 {
		t.Fatalf("kuota naik ke 4: %+v", o)
	}
	e.db.Exec(context.Background(), `UPDATE ticket_categories SET quota = 3 WHERE id = $1`, e.cat) //nolint:errcheck
	e.order(1)
	e.drain(1)
	if o := e.outcome(); o.tickets != 4 || o.needsRefund != 3 {
		t.Fatalf("kuota turun di bawah terbit: %+v", o)
	}
}

// IssueForOrder sendiri dilindungi (pemanggil mana pun): tiket berstatus apa pun dihitung terbit.
func TestIssueForOrderGuardCountsAllStatuses(t *testing.T) {
	e := newGuardEnv(t, 2)
	e.looseRedis()
	e.order(2)
	e.drain(1)
	e.db.Exec(context.Background(), `UPDATE tickets SET status = 'refunded' WHERE category_id = $1`, e.cat) //nolint:errcheck
	id := e.order(1)
	err := pgx.BeginFunc(context.Background(), e.db, func(tx pgx.Tx) error {
		_, err := ticket.IssueForOrder(context.Background(), tx, nil, id)
		return err
	})
	if err != ticket.ErrQuotaExceeded {
		t.Fatalf("mau ErrQuotaExceeded, dapat %v", err)
	}
}

// Batas per pembeli dipulihkan dari database setelah Redis kehilangan semua kunci (FLUSHALL).
func TestUserLimitRestoredFromDatabase(t *testing.T) {
	e := newGuardEnv(t, 50)
	ctx := context.Background()
	other := func(name string) uuid.UUID {
		var id uuid.UUID
		if err := e.db.QueryRow(ctx, `INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id`, name+uuid.NewString()[:6]+"@uji.test", name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { e.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) }) //nolint:errcheck
		return id
	}
	mk := func(buyer uuid.UUID, qty int, status, expires string) {
		if _, err := e.db.Exec(ctx, `
			INSERT INTO orders (org_id, event_id, category_id, buyer_id, reservation_id, idempotency_key, qty, subtotal, platform_fee, total, status, expires_at)
			VALUES ($1, $2, $3, $4, $5, $5, $6, 1, 0, 1, $7, `+expires+`)`, e.org, e.event, e.cat, buyer, uuid.NewString(), qty, status); err != nil {
			t.Fatal(err)
		}
	}
	paid, pending, expired, cancelled, fresh := other("lunas"), other("pending"), other("kadaluarsa"), other("batal"), other("baru")
	mk(paid, 2, "paid", "now()")
	mk(pending, 1, "pending", "now() + interval '10 minutes'")
	mk(expired, 2, "expired", "now() - interval '1 hour'")
	mk(cancelled, 2, "cancelled", "now() - interval '1 hour'")
	mk(e.user, 1, "pending", "now() - interval '1 hour'") // pending yang reservasinya sudah lewat: tidak dihitung

	e.mr.FlushAll() // Redis kehilangan segalanya
	if err := checkout.SyncCategory(ctx, e.db, e.inv, e.cat); err != nil {
		t.Fatal(err)
	}
	reserve := func(u uuid.UUID, qty int) error {
		_, err := e.inv.Reserve(ctx, checkout.ReserveRequest{CategoryID: e.cat.String(), UserID: u.String(), Qty: qty, MaxPerUser: 2, TTL: 15 * time.Minute})
		return err
	}
	if err := reserve(paid, 1); err != checkout.ErrLimitExceeded {
		t.Fatalf("pembeli dengan order lunas 2 (batas 2) harus ditolak: %v", err)
	}
	if err := reserve(pending, 2); err != checkout.ErrLimitExceeded {
		t.Fatalf("pembeli dengan order pending 1 lalu 2 lagi harus ditolak: %v", err)
	}
	if err := reserve(pending, 1); err != nil {
		t.Fatalf("pembeli pending 1 masih boleh 1 lagi: %v", err)
	}
	for _, u := range []uuid.UUID{expired, cancelled, fresh, e.user} {
		if _, err := e.mr.Get("{cat:" + e.cat.String() + "}:user:" + u.String()); err == nil {
			t.Fatalf("kunci tidak boleh ada untuk pembeli %s tanpa order aktif", u)
		}
		if err := reserve(u, 2); err != nil {
			t.Fatalf("pembeli tanpa order aktif tidak boleh terdampak: %v", err)
		}
	}
	// Idempoten: dijalankan ulang tidak mengubah apa pun (tidak pernah menurunkan atau menggandakan).
	n, err := checkout.RestoreUserLimits(ctx, e.db, e.inv, e.cat)
	if err != nil || n != 0 {
		t.Fatalf("pemulihan kedua: n=%d err=%v", n, err)
	}
	if ttl := e.mr.TTL("{cat:" + e.cat.String() + "}:user:" + paid.String()); ttl < 24*time.Hour {
		t.Fatalf("TTL kunci terlalu pendek: %v", ttl)
	}
}
