package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/config"
	"github.com/gavinadlan/racego/backend/internal/finance"
	"github.com/gavinadlan/racego/backend/internal/ledger"
	"github.com/gavinadlan/racego/backend/internal/notify"
	"github.com/gavinadlan/racego/backend/internal/payment"
	"github.com/gavinadlan/racego/backend/internal/secretbox"
	"github.com/gavinadlan/racego/backend/internal/ticket"
)

// Uji keuangan mitra lewat router lengkap + langkah worker, Postgres sungguhan.
// Nilai uji bukan data asli; nomor rekening dicari sebagai teks di DB, audit, outbox, dan log.

const (
	finAccount  = "1234509876"
	finNIK      = "3201111122223333"
	ticketPrice = 350000
)

// scriptDisb: MockDisburser (jeda 0) yang bisa diberi daftar error untuk Create berikutnya.
type scriptDisb struct {
	finance.MockDisburser
	mu      sync.Mutex
	errs    []error
	creates atomic.Int32
	status  *finance.DisburseStatus // bila diisi, menimpa hasil Status
}

func (d *scriptDisb) Create(ctx context.Context, req finance.DisburseRequest) (string, error) {
	d.creates.Add(1)
	d.mu.Lock()
	if len(d.errs) > 0 {
		err := d.errs[0]
		d.errs = d.errs[1:]
		d.mu.Unlock()
		return "", err
	}
	d.mu.Unlock()
	return d.MockDisburser.Create(ctx, req)
}

func (d *scriptDisb) Status(ctx context.Context, ref string) (finance.DisburseStatus, error) {
	d.mu.Lock()
	st := d.status
	d.mu.Unlock()
	if st != nil {
		return *st, nil
	}
	return d.MockDisburser.Status(ctx, ref)
}

type finEnv struct {
	*portalEnv
	box    *secretbox.Box
	svc    *finance.Service
	disb   *scriptDisb
	signer *ticket.Signer
	sa     uuid.UUID
	logs   *bytes.Buffer
}

func newFinEnv(t *testing.T) *finEnv {
	t.Helper()
	pe := newPortalEnv(t, false)
	key := make([]byte, 32)
	rand.Read(key) //nolint:errcheck
	box, _ := secretbox.New(key)
	signer, _ := ticket.NewSigner("", true)
	e := &finEnv{portalEnv: pe, box: box, signer: signer, disb: &scriptDisb{}}
	fcfg := config.FinanceConfig{HoldDays: 3, PayoutMin: 50000, DisburseMode: "mock"}
	e.svc = finance.NewService(pe.db, box, fcfg, e.disb, finance.MockRefunder{})
	rdb := redis.NewClient(&redis.Options{Addr: pe.mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	cfg := config.Config{Env: "development", FrontendURL: "http://localhost:3000", ReservationTTL: 15 * time.Minute,
		UploadDir: t.TempDir(), KYCDir: t.TempDir(), Finance: fcfg,
		Queue: config.QueueConfig{LobbyMinutes: 15, AdmitPerSec: 20, MaxActive: 300, PassTTL: 10 * time.Minute, HeartbeatTTL: 45 * time.Second}}
	e.h = NewRouter(Deps{Cfg: cfg, DB: pe.db, Redis: rdb, Inv: pe.inv, Snap: payment.NewMockSnap(cfg.FrontendURL), Signer: signer,
		Mailer: notify.NewMailer("localhost:1", "", "", "x@x"), Queue: pe.q, KYCBox: box, Finance: e.svc})
	e.sa = e.user("sa")
	if _, err := pe.db.Exec(context.Background(), `UPDATE users SET is_super_admin = true WHERE id = $1`, e.sa); err != nil {
		t.Fatal(err)
	}
	e.logs = &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(e.logs, nil)))
	// Didaftarkan setelah newPortalEnv → jalan lebih dulu. Ledger append-only (trigger), jadi data uji
	// dihapus dengan session_replication_role = replica (hanya bisa superuser DB uji; aplikasi tidak pernah).
	t.Cleanup(func() {
		slog.SetDefault(prev)
		ctx := context.Background()
		orgs := `(SELECT id FROM organizations WHERE created_by IN (SELECT id FROM users WHERE email LIKE $1))`
		err := pgx.BeginFunc(ctx, pe.db, func(tx pgx.Tx) error {
			for _, q := range []string{
				`SET LOCAL session_replication_role = replica`,
				`CREATE TEMP TABLE fin_j ON COMMIT DROP AS SELECT DISTINCT journal_id AS id FROM ledger_entries WHERE org_id IN ` + orgs,
				`DELETE FROM ledger_entries WHERE journal_id IN (SELECT id FROM fin_j)`,
				`DELETE FROM ledger_journals WHERE id IN (SELECT id FROM fin_j)`,
				`DELETE FROM outbox WHERE payload->>'payout_id' IN (SELECT id::text FROM payouts WHERE org_id IN ` + orgs + `)
				   OR payload->>'refund_id' IN (SELECT id::text FROM refunds WHERE org_id IN ` + orgs + `)
				   OR payload->>'order_id' IN (SELECT id::text FROM orders WHERE org_id IN ` + orgs + `)
				   OR payload->>'user_id' IN (SELECT id::text FROM users WHERE email LIKE $1)`,
				`DELETE FROM refunds WHERE org_id IN ` + orgs,
				`DELETE FROM payouts WHERE org_id IN ` + orgs,
				`DELETE FROM scan_logs WHERE org_id IN ` + orgs,
				`DELETE FROM tickets WHERE org_id IN ` + orgs,
				`DELETE FROM org_members WHERE user_id IN (SELECT id FROM users WHERE email LIKE $1)`,
			} {
				var args []any
				if strings.Contains(q, "$1") {
					args = []any{"%" + pe.tag + "@uji.test"}
				}
				if _, err := tx.Exec(ctx, q, args...); err != nil {
					return fmt.Errorf("%s: %w", q[:40], err)
				}
			}
			return nil
		})
		if err != nil {
			t.Errorf("bersih-bersih keuangan: %v", err)
		}
	})
	return e
}

// verifiedOrg: org baru → pengajuan verifikasi (rekening terenkripsi) → disetujui super admin lewat API.
func (e *finEnv) verifiedOrg(name, account string) (slug string, orgID, owner uuid.UUID) {
	e.t.Helper()
	ctx := context.Background()
	owner = e.user("owner-" + strings.ReplaceAll(strings.ToLower(name), " ", ""))
	slug = e.createOrg(owner, name+" "+e.tag)
	if err := e.db.QueryRow(ctx, `SELECT id FROM organizations WHERE slug = $1`, slug).Scan(&orgID); err != nil {
		e.t.Fatal(err)
	}
	var vid uuid.UUID
	if err := e.db.QueryRow(ctx, `
		INSERT INTO org_verifications (org_id, attempt, status, org_type, legal_name, pic_name, bank_code, bank_name,
		  account_holder, account_enc, account_last4, submitted_at)
		VALUES ($1, 1, 'submitted', 'perorangan', 'Uji', 'Budi Contoh', 'bca', 'BCA', 'Budi Contoh', $2, $3, now()) RETURNING id`,
		orgID, e.box.SealString(account), account[len(account)-4:]).Scan(&vid); err != nil {
		e.t.Fatal(err)
	}
	e.must(e.do(e.sa, "POST", "/v1/admin/verifications/"+vid.String()+"/approve", nil), 200)
	return slug, orgID, owner
}

func (e *finEnv) member(orgID uuid.UUID, name, role string) uuid.UUID {
	e.t.Helper()
	id := e.user(name)
	if _, err := e.db.Exec(context.Background(), `INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)`, orgID, id, role); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// event membuat event + satu kategori lewat API; mengembalikan id event & kategori.
func (e *finEnv) event(slug string, owner uuid.UUID, name string) (uuid.UUID, uuid.UUID) {
	e.t.Helper()
	now := time.Now()
	ev := e.must(e.do(owner, "POST", "/v1/org/"+slug+"/events", eventBody(name, "Bogor", now.Add(-time.Hour), now.AddDate(0, 0, 30), cat("21K", "21", 100))), 201)
	return uuid.MustParse(ev.body["id"].(string)), uuid.MustParse(catID(ev, "21K"))
}

// paidOrder meniru hasil worker untuk order lunas via VA: order paid, tiket terbit, jurnal order.paid.
func (e *finEnv) paidOrder(orgID, eventID, catID, buyer uuid.UUID, qty int) uuid.UUID {
	e.t.Helper()
	ctx := context.Background()
	total := int64(ticketPrice * qty)
	fee := ledger.PlatformFee(total, 300)
	var id uuid.UUID
	err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO orders (org_id, event_id, category_id, buyer_id, reservation_id, idempotency_key, qty, subtotal, platform_fee, total, status, expires_at, paid_at)
			VALUES ($1, $2, $3, $4, $5, $5, $6, $7, $8, $7, 'paid', now(), now()) RETURNING id`,
			orgID, eventID, catID, buyer, uuid.NewString(), qty, total, fee).Scan(&id); err != nil {
			return err
		}
		for i := range qty {
			if _, err := tx.Exec(ctx, `
				INSERT INTO participants (order_id, org_id, event_id, category_id, full_name, email, id_number, jersey_size)
				VALUES ($1, $2, $3, $4, $5, 'peserta@uji.test', $6, 'M')`, id, orgID, eventID, catID, fmt.Sprintf("Peserta %d", i), finNIK); err != nil {
				return err
			}
		}
		if _, err := ticket.IssueForOrder(ctx, tx, e.signer, id); err != nil {
			return err
		}
		j, err := ledger.OrderPaid(id, orgID, total, fee, ledger.EstimatePGFee("bank_transfer", total))
		if err != nil {
			return err
		}
		_, err = ledger.Post(ctx, tx, j)
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *finEnv) balances(orgID uuid.UUID) ledger.Balances {
	e.t.Helper()
	b, err := ledger.OrgBalances(context.Background(), e.db, orgID)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

// checkLedger: GET /v1/admin/ledger/check harus hijau (dipanggil setelah tiap skenario).
func (e *finEnv) checkLedger() {
	e.t.Helper()
	r := e.must(e.do(e.sa, "GET", "/v1/admin/ledger/check", nil), 200)
	if r.body["ok"] != true {
		e.t.Fatalf("ledger/check merah: %s", r.raw)
	}
}

func (e *finEnv) step(f func(context.Context) error) {
	e.t.Helper()
	if err := f(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *finEnv) pastRace(eventID uuid.UUID, daysAgo int) {
	e.t.Helper()
	if _, err := e.db.Exec(context.Background(), `UPDATE events SET race_date = (now() AT TIME ZONE 'Asia/Jakarta')::date - $2::int WHERE id = $1`, eventID, daysAgo); err != nil {
		e.t.Fatal(err)
	}
}

func (e *finEnv) payout(user uuid.UUID, slug string, amount any, key string) resp {
	e.t.Helper()
	h := map[string]string{}
	if key != "" {
		h["Idempotency-Key"] = key
	}
	return e.doH(user, "POST", "/v1/org/"+slug+"/payouts", map[string]any{"amount": amount}, h)
}

func (e *finEnv) noAccountLeak() {
	e.t.Helper()
	var n int
	e.db.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE after::text LIKE $1 OR before::text LIKE $1`, "%"+finAccount+"%").Scan(&n) //nolint:errcheck
	if n > 0 {
		e.t.Fatalf("nomor rekening muncul di %d baris audit", n)
	}
	e.db.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE payload::text LIKE $1`, "%"+finAccount+"%").Scan(&n) //nolint:errcheck
	if n > 0 || strings.Contains(e.logs.String(), finAccount) {
		e.t.Fatalf("nomor rekening bocor ke outbox/log")
	}
}

func TestFinanceAccountFromKYCAndEligibility(t *testing.T) {
	e := newFinEnv(t)
	ctx := context.Background()
	slug, orgID, owner := e.verifiedOrg("Org Rek", finAccount)

	var enc, last4 string
	var vid *uuid.UUID
	if err := e.db.QueryRow(ctx, `SELECT account_enc, account_last4, verification_id FROM payout_accounts WHERE org_id = $1`, orgID).Scan(&enc, &last4, &vid); err != nil {
		t.Fatalf("persetujuan KYC harus membuat payout_accounts: %v", err)
	}
	if !strings.HasPrefix(enc, "v1:") || strings.Contains(enc, finAccount) || last4 != "9876" || vid == nil {
		t.Fatalf("rekening harus terenkripsi: last4=%s", last4)
	}
	if plain, err := e.box.OpenString(enc); err != nil || plain != finAccount {
		t.Fatal("ciphertext harus bisa dibuka dengan kunci yang sama")
	}
	sum := e.must(e.do(owner, "GET", "/v1/org/"+slug+"/finance/summary", nil), 200)
	acc := sum.body["account"].(map[string]any)
	if acc["last4"] != "9876" || acc["bank_name"] != "BCA" || strings.Contains(sum.raw, finAccount) || sum.body["can_payout"] != false {
		t.Fatalf("summary: %s", sum.raw)
	}
	if b := sum.body["blocker"].(map[string]any); b["code"] != "below_minimum" {
		t.Fatalf("saldo 0 → below_minimum: %s", sum.raw)
	}

	// Org verified lewat jalur lama (seed / make verify) tidak punya rekening.
	legacyOwner := e.user("legacy")
	legacy := e.createOrg(legacyOwner, "Org Lama "+e.tag)
	e.db.Exec(ctx, `UPDATE organizations SET kyc_status = 'verified' WHERE slug = $1`, legacy) //nolint:errcheck
	r := e.payout(legacyOwner, legacy, 60000, "k1")
	if r.code != 409 || r.body["error"] != "no_payout_account" || !strings.Contains(r.body["message"].(string), "Lengkapi verifikasi penyelenggara dan rekening") {
		t.Fatalf("tanpa rekening: %s", r.raw)
	}
	// Belum verified.
	pendingOwner := e.user("pending")
	pending := e.createOrg(pendingOwner, "Org Belum "+e.tag)
	if r := e.payout(pendingOwner, pending, 60000, "k1"); r.code != 409 || r.body["error"] != "org_not_verified" {
		t.Fatalf("belum verified: %s", r.raw)
	}
	e.checkLedger()
	e.noAccountLeak()
}

func TestFinanceRelease(t *testing.T) {
	e := newFinEnv(t)
	ctx := context.Background()
	slug, orgID, owner := e.verifiedOrg("Org Rilis", finAccount)
	evID, catID := e.event(slug, owner, "Lari Rilis")
	buyer := e.user("pelari")
	o1 := e.paidOrder(orgID, evID, catID, buyer, 1)
	o2 := e.paidOrder(orgID, evID, catID, buyer, 1)

	if b := e.balances(orgID); b.Held != 2*335060 || b.Available != 0 {
		t.Fatalf("awal: %+v", b)
	}
	// Order 2 di-refund sebelum rilis → tidak pernah dirilis.
	e.must(e.do(e.sa, "POST", "/v1/admin/refunds", map[string]any{"order_id": o2, "reason": "Pembeli sakit, refund penuh"}), 201)

	e.step(func(ctx context.Context) error { _, _, err := e.svc.Release(ctx, nil); return err }) // lomba 60 hari lagi
	e.pastRace(evID, 2)                                                                          // H+2 < masa tahan 3 hari
	e.step(func(ctx context.Context) error { _, _, err := e.svc.Release(ctx, nil); return err })
	if b := e.balances(orgID); b.Available != 0 || b.Held != 335060 {
		t.Fatalf("sebelum masa tahan tidak boleh dirilis: %+v", b)
	}
	e.pastRace(evID, 3)
	var wg sync.WaitGroup
	for range 8 { // worker paralel
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := e.svc.Release(ctx, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	e.step(func(ctx context.Context) error { _, _, err := e.svc.Release(ctx, nil); return err })
	var n int
	e.db.QueryRow(ctx, `SELECT count(*) FROM ledger_journals WHERE ref_type = 'balance.release' AND ref_id = ANY($1)`, []string{o1.String(), o2.String()}).Scan(&n) //nolint:errcheck
	if b := e.balances(orgID); n != 1 || b.Available != 335060 || b.Held != 0 {
		t.Fatalf("rilis tepat sekali sebesar bagian mitra: jurnal=%d %+v", n, b)
	}
	e.checkLedger()

	// make dev-release: tanpa menunggu masa tahan, hanya org itu.
	ev2, cat2 := e.event(slug, owner, "Lari Nanti")
	e.paidOrder(orgID, ev2, cat2, buyer, 2)
	got, amount, err := e.svc.Release(ctx, &orgID)
	if err != nil || got != 1 || amount != 2*ticketPrice-ledger.PlatformFee(2*ticketPrice, 300)-ledger.EstimatePGFee("bank_transfer", 2*ticketPrice) {
		t.Fatalf("dev-release: %d %d %v", got, amount, err)
	}
	e.checkLedger()
}

func TestFinancePayoutFlow(t *testing.T) {
	e := newFinEnv(t)
	ctx := context.Background()
	slug, orgID, owner := e.verifiedOrg("Org Cair", finAccount)
	evID, catID := e.event(slug, owner, "Lari Cair")
	buyer := e.user("pelari")
	for range 3 {
		e.paidOrder(orgID, evID, catID, buyer, 1)
	}
	e.step(func(ctx context.Context) error { _, _, err := e.svc.Release(ctx, &orgID); return err })
	avail := int64(3 * 335060)
	fin := e.member(orgID, "fin", "finance")
	adm := e.member(orgID, "adm", "admin")
	rpc := e.member(orgID, "rpc", "rpc_staff")
	outsider := e.user("luar")
	base := "/v1/org/" + slug

	// Matriks peran untuk semua endpoint keuangan mitra.
	for _, p := range []string{"/finance/summary", "/finance/ledger", "/payouts", "/orders", "/refunds", "/reports/sales", "/reports/sales.csv", "/reports/ledger.csv"} {
		for user, want := range map[uuid.UUID]int{owner: 200, fin: 200, adm: 403, rpc: 403, outsider: 404, uuid.Nil: 401} {
			if r := e.do(user, "GET", base+p, nil); r.code != want {
				t.Fatalf("GET %s user %s: %d, mau %d", p, user, r.code, want)
			}
		}
	}
	for user, want := range map[uuid.UUID]int{adm: 403, rpc: 403, outsider: 404, uuid.Nil: 401} {
		if r := e.payout(user, slug, 60000, "x"); r.code != want {
			t.Fatalf("POST payouts: %d, mau %d", r.code, want)
		}
	}

	// Validasi jumlah.
	if r := e.payout(owner, slug, 60000, ""); r.code != 400 || r.body["error"] != "idempotency_key_required" {
		t.Fatalf("tanpa Idempotency-Key: %s", r.raw)
	}
	for _, bad := range []any{60000.5, "60000", 0, -5, 1e20} {
		if r := e.payout(owner, slug, bad, uuid.NewString()); r.code != 422 && r.code != 400 {
			t.Fatalf("jumlah %v: %s", bad, r.raw)
		}
	}
	if r := e.payout(owner, slug, 49999, uuid.NewString()); r.code != 422 || r.body["error"] != "below_minimum" {
		t.Fatalf("di bawah minimum: %s", r.raw)
	}
	if r := e.payout(owner, slug, avail+1, uuid.NewString()); r.code != 422 || r.body["error"] != "insufficient_balance" {
		t.Fatalf("melebihi saldo: %s", r.raw)
	}

	// Idempotency-Key sama (paralel) → satu payout.
	key := uuid.NewString()
	var wg sync.WaitGroup
	ids := make([]string, 10)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := e.payout(fin, slug, 100000, key); r.code == 201 {
				ids[i] = r.body["id"].(string)
			}
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id == "" || id != ids[0] {
			t.Fatalf("kunci sama harus mengembalikan payout yang sama: %v", ids)
		}
	}
	p1 := ids[0]
	if r := e.payout(owner, slug, 60000, uuid.NewString()); r.code != 409 || r.body["error"] != "payout_in_progress" {
		t.Fatalf("satu payout aktif: %s", r.raw)
	}
	if b := e.balances(orgID); b.Available != avail-100000 || b.InProcess != 100000 {
		t.Fatalf("setelah pengajuan: %+v", b)
	}
	e.checkLedger()

	// Persetujuan.
	adminPath := "/v1/admin/payouts/" + p1
	for _, u := range []uuid.UUID{owner, fin, outsider} {
		if r := e.do(u, "POST", adminPath+"/approve", nil); r.code != 404 {
			t.Fatalf("non super admin: %d", r.code)
		}
	}
	saMember := e.user("sa-anggota")
	e.db.Exec(ctx, `UPDATE users SET is_super_admin = true WHERE id = $1`, saMember)                              //nolint:errcheck
	e.db.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'finance')`, orgID, saMember) //nolint:errcheck
	if r := e.do(saMember, "POST", adminPath+"/approve", nil); r.code != 403 || r.body["error"] != "conflict_of_interest" {
		t.Fatalf("benturan kepentingan: %s", r.raw)
	}
	if r := e.do(e.sa, "POST", adminPath+"/reject", map[string]any{"reason": "pendek"}); r.code != 422 {
		t.Fatalf("tolak tanpa alasan cukup: %s", r.raw)
	}
	detail := e.must(e.do(e.sa, "GET", adminPath, nil), 200)
	if strings.Contains(detail.raw, finAccount) || detail.body["conflict_of_interest"] != false {
		t.Fatalf("detail: %s", detail.raw)
	}
	if r := e.must(e.do(e.sa, "POST", adminPath+"/reveal", map[string]any{}), 200); r.body["value"] != finAccount {
		t.Fatalf("reveal: %s", r.raw)
	}
	var ok200, conflict409 atomic.Int32
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch e.do(e.sa, "POST", adminPath+"/approve", nil).code {
			case 200:
				ok200.Add(1)
			case 409:
				conflict409.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok200.Load() != 1 || conflict409.Load() != 9 {
		t.Fatalf("approve paralel: %d ok, %d 409", ok200.Load(), conflict409.Load())
	}
	e.step(e.svc.ExecutePayouts)
	e.step(e.svc.ExecutePayouts)
	if e.disb.creates.Load() != 1 {
		t.Fatalf("Disburser dipanggil %d kali, mau 1", e.disb.creates.Load())
	}
	if r := e.must(e.do(owner, "GET", base+"/payouts", nil), 200); !strings.Contains(r.raw, `"status":"processing"`) {
		t.Fatalf("harus processing: %s", r.raw)
	}
	e.step(e.svc.CheckPayouts)
	if b := e.balances(orgID); b.PaidOut != 100000 || b.InProcess != 0 || b.Available != avail-100000 {
		t.Fatalf("setelah selesai: %+v", b)
	}
	// Tidak ada transisi mundur: gateway kini bilang gagal, tapi payout completed tidak berubah.
	e.disb.status = &finance.DisburseStatus{State: finance.DisburseFailed, Reason: "x"}
	e.step(e.svc.CheckPayouts)
	e.disb.status = nil
	var st string
	e.db.QueryRow(ctx, `SELECT status FROM payouts WHERE id = $1`, p1).Scan(&st) //nolint:errcheck
	if st != "completed" || e.balances(orgID).PaidOut != 100000 {
		t.Fatalf("completed tidak boleh mundur: %s", st)
	}
	e.checkLedger()
	avail -= 100000

	// Ditolak: saldo kembali persis.
	p2 := e.must(e.payout(owner, slug, 70000, uuid.NewString()), 201).body["id"].(string)
	e.must(e.do(e.sa, "POST", "/v1/admin/payouts/"+p2+"/reject", map[string]any{"reason": "Nama rekening perlu dicek ulang"}), 200)
	if b := e.balances(orgID); b.Available != avail || b.InProcess != 0 {
		t.Fatalf("tolak harus mengembalikan saldo: %+v", b)
	}
	if r := e.do(e.sa, "POST", "/v1/admin/payouts/"+p2+"/approve", nil); r.code != 409 || r.body["error"] != "invalid_state" {
		t.Fatalf("setujui yang ditolak: %s", r.raw)
	}
	e.checkLedger()

	// Gagal permanen dari gateway → failed + saldo kembali.
	e.disb.errs = []error{&finance.PermanentError{Reason: "rekening ditutup"}}
	p3 := e.must(e.payout(owner, slug, 80000, uuid.NewString()), 201).body["id"].(string)
	e.must(e.do(e.sa, "POST", "/v1/admin/payouts/"+p3+"/approve", nil), 200)
	e.step(e.svc.ExecutePayouts)
	e.db.QueryRow(ctx, `SELECT status FROM payouts WHERE id = $1`, p3).Scan(&st) //nolint:errcheck
	if b := e.balances(orgID); st != "failed" || b.Available != avail || b.InProcess != 0 {
		t.Fatalf("gagal permanen: %s %+v", st, b)
	}
	e.checkLedger()

	// Timeout sementara 2x → retry dengan backoff → sukses.
	e.disb.errs = []error{errors.New("timeout"), errors.New("HTTP 503")}
	p4 := e.must(e.payout(owner, slug, 90000, uuid.NewString()), 201).body["id"].(string)
	e.must(e.do(e.sa, "POST", "/v1/admin/payouts/"+p4+"/approve", nil), 200)
	for i := range 3 {
		e.step(e.svc.ExecutePayouts)
		var next *time.Time
		e.db.QueryRow(ctx, `SELECT status, next_attempt_at FROM payouts WHERE id = $1`, p4).Scan(&st, &next) //nolint:errcheck
		if i < 2 {
			if st != "approved" || next == nil || !next.After(time.Now().Add(20*time.Second)) {
				t.Fatalf("percobaan %d: harus menunggu backoff (%s %v)", i+1, st, next)
			}
			e.step(e.svc.ExecutePayouts)                                                   // backoff belum lewat → tidak dipanggil
			e.db.Exec(ctx, `UPDATE payouts SET next_attempt_at = now() WHERE id = $1`, p4) //nolint:errcheck
		}
	}
	if st != "processing" {
		t.Fatalf("setelah retry harus processing: %s", st)
	}
	e.step(e.svc.CheckPayouts)
	avail -= 90000
	e.checkLedger()

	// Gagal sementara terus-menerus → failed setelah batas percobaan.
	e.disb.errs = []error{errors.New("t"), errors.New("t"), errors.New("t"), errors.New("t"), errors.New("t")}
	p5 := e.must(e.payout(owner, slug, 60000, uuid.NewString()), 201).body["id"].(string)
	e.must(e.do(e.sa, "POST", "/v1/admin/payouts/"+p5+"/approve", nil), 200)
	for range 5 {
		e.step(e.svc.ExecutePayouts)
		e.db.Exec(ctx, `UPDATE payouts SET next_attempt_at = now() WHERE id = $1 AND status = 'approved'`, p5) //nolint:errcheck
	}
	var attempts int
	e.db.QueryRow(ctx, `SELECT status, attempts FROM payouts WHERE id = $1`, p5).Scan(&st, &attempts) //nolint:errcheck
	if b := e.balances(orgID); st != "failed" || b.Available != avail {
		t.Fatalf("batas percobaan: %s attempts=%d %+v", st, attempts, b)
	}
	e.checkLedger()

	// Audit & email lengkap, tanpa nomor rekening.
	for _, action := range []string{"payout.requested", "payout.approved", "payout.rejected", "payout.processing", "payout.completed", "payout.failed", "payout.account.reveal"} {
		var n int
		e.db.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE org_id = $1 AND action = $2`, orgID, action).Scan(&n) //nolint:errcheck
		if n == 0 {
			t.Fatalf("audit %s tidak ada", action)
		}
	}
	var events []string
	rows, _ := e.db.Query(ctx, `SELECT DISTINCT payload->>'event' FROM outbox WHERE topic = 'payout.email' AND payload->>'payout_id' = ANY($1) ORDER BY 1`, []string{p1, p2, p3})
	events, _ = pgx.CollectRows(rows, pgx.RowTo[string])
	if strings.Join(events, ",") != "approved,completed,failed,rejected,requested" {
		t.Fatalf("email pencairan: %v", events)
	}
	tx, _ := e.db.Begin(ctx)
	defer tx.Rollback(ctx) //nolint:errcheck
	var payload []byte
	tx.QueryRow(ctx, `SELECT payload FROM outbox WHERE topic = 'payout.email' AND payload->>'payout_id' = $1 AND payload->>'event' = 'rejected' LIMIT 1`, p2).Scan(&payload) //nolint:errcheck
	m, ok, err := finance.PayoutMail(ctx, tx, payload, "http://localhost:3000")
	if err != nil || !ok || !strings.Contains(m.Body, "9876") || !strings.Contains(m.Body, "Rp70.000") || strings.Contains(m.Body, finAccount) || !strings.Contains(m.Body, "Nama rekening perlu dicek ulang") {
		t.Fatalf("email: %v %v %s", ok, err, m.Body)
	}
	e.noAccountLeak()
}

func TestFinancePayoutConcurrency(t *testing.T) {
	e := newFinEnv(t)
	slug, orgID, owner := e.verifiedOrg("Org Paralel", finAccount)
	evID, catID := e.event(slug, owner, "Lari Paralel")
	buyer := e.user("pelari")
	for range 3 {
		e.paidOrder(orgID, evID, catID, buyer, 1)
	}
	e.step(func(ctx context.Context) error { _, _, err := e.svc.Release(ctx, &orgID); return err })
	start := e.balances(orgID).Available // 1.005.180
	var passed int64
	for round := range 3 {
		var wg sync.WaitGroup
		var ok atomic.Int32
		var codes sync.Map
		for range 25 { // 25 × Rp400.000 jauh melebihi saldo
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := e.payout(owner, slug, 400000, uuid.NewString())
				if r.code == 201 {
					ok.Add(1)
				} else {
					codes.Store(r.body["error"], true)
				}
				if b := e.balances(orgID); b.Available < 0 {
					t.Errorf("saldo negatif: %+v", b)
				}
			}()
		}
		wg.Wait()
		want := int32(1)
		if round == 2 {
			want = 0 // sisa 205.180 < 400.000
		}
		if ok.Load() != want {
			t.Fatalf("putaran %d: %d lolos, mau %d", round, ok.Load(), want)
		}
		codes.Range(func(k, _ any) bool {
			if k != "payout_in_progress" && k != "insufficient_balance" {
				t.Fatalf("penolakan tidak rapi: %v", k)
			}
			return true
		})
		passed += int64(ok.Load()) * 400000
		// Selesaikan payout aktif agar putaran berikut bisa mengajukan lagi.
		var id string
		if e.db.QueryRow(context.Background(), `SELECT id FROM payouts WHERE org_id = $1 AND status = 'requested'`, orgID).Scan(&id) == nil {
			e.must(e.do(e.sa, "POST", "/v1/admin/payouts/"+id+"/approve", nil), 200)
			e.step(e.svc.ExecutePayouts)
			e.step(e.svc.CheckPayouts)
		}
		e.checkLedger()
	}
	b := e.balances(orgID)
	if passed > start || b.PaidOut != passed || b.Available != start-passed || b.Available < 0 {
		t.Fatalf("lolos %d dari saldo %d: %+v", passed, start, b)
	}
}

func TestFinanceRefund(t *testing.T) {
	e := newFinEnv(t)
	ctx := context.Background()
	slug, orgID, owner := e.verifiedOrg("Org Refund", finAccount)
	evID, catID := e.event(slug, owner, "Lari Refund")
	buyer := e.user("pelari")
	o1 := e.paidOrder(orgID, evID, catID, buyer, 2)
	base := "/v1/org/" + slug

	// Daftar order untuk refund: tanpa data peserta.
	list := e.must(e.do(owner, "GET", base+"/orders?q="+o1.String()[:8], nil), 200)
	if list.body["total"] != float64(1) || strings.Contains(list.raw, finNIK) {
		t.Fatalf("daftar order: %s", list.raw)
	}
	if r := e.do(owner, "POST", base+"/orders/"+o1.String()+"/refund", map[string]any{"reason": "pendek"}); r.code != 422 {
		t.Fatalf("alasan pendek: %s", r.raw)
	}
	// Org lain tidak bisa menyentuh order org ini.
	slugB, _, ownerB := e.verifiedOrg("Org B", "5555666677")
	if r := e.do(ownerB, "POST", "/v1/org/"+slugB+"/orders/"+o1.String()+"/refund", map[string]any{"reason": "Mencoba refund order org lain"}); r.code != 404 {
		t.Fatalf("org lain: %s", r.raw)
	}
	if r := e.do(ownerB, "GET", "/v1/org/"+slug+"/refunds", nil); r.code != 404 {
		t.Fatalf("org lain lihat refund: %d", r.code)
	}
	// Pengajuan paralel: satu lolos, sisanya already_refunded.
	var wg sync.WaitGroup
	var created atomic.Int32
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := e.do(owner, "POST", base+"/orders/"+o1.String()+"/refund", map[string]any{"reason": "Peserta cedera, minta refund penuh"})
			if r.code == 201 {
				created.Add(1)
			} else if r.body["error"] != "already_refunded" {
				t.Errorf("penolakan: %s", r.raw)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("refund paralel: %d dibuat", created.Load())
	}
	var refundID string
	e.db.QueryRow(ctx, `SELECT id FROM refunds WHERE order_id = $1`, o1).Scan(&refundID) //nolint:errcheck
	if r := e.do(owner, "POST", "/v1/admin/refunds/"+refundID+"/approve", nil); r.code != 404 {
		t.Fatalf("owner menyetujui: %d", r.code)
	}
	saMember := e.user("sa-anggota")
	e.db.Exec(ctx, `UPDATE users SET is_super_admin = true WHERE id = $1`, saMember)                            //nolint:errcheck
	e.db.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'admin')`, orgID, saMember) //nolint:errcheck
	if r := e.do(saMember, "POST", "/v1/admin/refunds/"+refundID+"/approve", nil); r.body["error"] != "conflict_of_interest" {
		t.Fatalf("benturan kepentingan: %s", r.raw)
	}
	var ok200 atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e.do(e.sa, "POST", "/v1/admin/refunds/"+refundID+"/approve", nil).code == 200 {
				ok200.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok200.Load() != 1 {
		t.Fatalf("approve refund paralel: %d", ok200.Load())
	}
	var orderStatus string
	var refundedTickets int
	e.db.QueryRow(ctx, `SELECT o.status, (SELECT count(*) FROM tickets t WHERE t.order_id = o.id AND t.status = 'refunded') FROM orders o WHERE o.id = $1`, o1).
		Scan(&orderStatus, &refundedTickets) //nolint:errcheck
	if orderStatus != "refunded" || refundedTickets != 2 {
		t.Fatalf("order %s, tiket refunded %d", orderStatus, refundedTickets)
	}
	var ticketID string
	e.db.QueryRow(ctx, `SELECT id FROM tickets WHERE order_id = $1 LIMIT 1`, o1).Scan(&ticketID) //nolint:errcheck
	if r := e.must(e.do(owner, "POST", base+"/events/"+evID.String()+"/scan", map[string]any{"ticket_id": ticketID}), 200); r.body["result"] != "refunded" {
		t.Fatalf("scanner harus menolak tiket refunded: %s", r.raw)
	}
	if b := e.balances(orgID); b.Held != 0 || b.Available != 0 {
		t.Fatalf("refund sebelum rilis menarik saldo tertahan: %+v", b)
	}
	if r := e.do(owner, "POST", base+"/orders/"+o1.String()+"/refund", map[string]any{"reason": "Coba refund kedua kalinya"}); r.body["error"] != "already_refunded" {
		t.Fatalf("refund kedua: %s", r.raw)
	}
	e.step(e.svc.ExecuteRefunds)
	var st string
	e.db.QueryRow(ctx, `SELECT status FROM refunds WHERE id = $1`, refundID).Scan(&st) //nolint:errcheck
	if st != "completed" {
		t.Fatalf("mock refund harus completed: %s", st)
	}
	e.checkLedger()

	// Order belum lunas tidak bisa di-refund.
	pending := e.paidOrder(orgID, evID, catID, buyer, 1)
	// (dibuat lunas lalu ditandai pending langsung untuk menguji aturan status)
	e.db.Exec(ctx, `UPDATE orders SET status = 'expired' WHERE id = $1`, pending) //nolint:errcheck
	if r := e.do(owner, "POST", base+"/orders/"+pending.String()+"/refund", map[string]any{"reason": "Order kedaluwarsa dicoba"}); r.body["error"] != "order_not_refundable" {
		t.Fatalf("order bukan paid: %s", r.raw)
	}
	e.db.Exec(ctx, `UPDATE orders SET status = 'paid' WHERE id = $1`, pending) //nolint:errcheck

	// Sesudah rilis + pencairan: refund menarik saldo tersedia, boleh negatif (utang mitra).
	e.step(func(ctx context.Context) error { _, _, err := e.svc.Release(ctx, &orgID); return err })
	if b := e.balances(orgID); b.Available != 335060 {
		t.Fatalf("setelah rilis: %+v", b)
	}
	pid := e.must(e.payout(owner, slug, 300000, uuid.NewString()), 201).body["id"].(string)
	e.must(e.do(e.sa, "POST", "/v1/admin/payouts/"+pid+"/approve", nil), 200)
	e.step(e.svc.ExecutePayouts)
	e.step(e.svc.CheckPayouts)
	// Paket sudah diambil: tetap boleh oleh super admin, respons memberi tahu jumlah tiket diambil.
	e.db.Exec(ctx, `UPDATE tickets SET status = 'collected', collected_at = now() WHERE order_id = $1`, pending) //nolint:errcheck
	r := e.must(e.do(e.sa, "POST", "/v1/admin/refunds", map[string]any{"order_id": pending, "reason": "Event dipindah, refund MANUAL ke rekening"}), 201)
	if r.body["collected_tickets"] != float64(1) || r.body["status"] != "approved" {
		t.Fatalf("refund langsung: %s", r.raw)
	}
	manualID := r.body["id"].(string)
	b := e.balances(orgID)
	if b.Available != 335060-300000-335060 {
		t.Fatalf("saldo harus negatif setelah refund pasca-rilis: %+v", b)
	}
	sum := e.must(e.do(owner, "GET", base+"/finance/summary", nil), 200)
	if blk := sum.body["blocker"].(map[string]any); blk["code"] != "insufficient_balance" {
		t.Fatalf("saldo negatif menghalangi pencairan: %s", sum.raw)
	}
	if r := e.payout(owner, slug, 50000, uuid.NewString()); r.body["error"] != "insufficient_balance" {
		t.Fatalf("pencairan saat negatif: %s", r.raw)
	}
	// Selisih biaya PG ke beban_refund_pg.
	var pg int64
	e.db.QueryRow(ctx, `SELECT coalesce(sum(e.debit), 0) FROM ledger_entries e JOIN ledger_journals j ON j.id = e.journal_id
		WHERE j.ref_type = 'order.refund' AND j.ref_id = $1 AND e.account = 'beban_refund_pg'`, pending.String()).Scan(&pg) //nolint:errcheck
	if pg != 4440 {
		t.Fatalf("beban_refund_pg = %d, mau 4440", pg)
	}
	// manual_required → mark-done → completed.
	e.step(e.svc.ExecuteRefunds)
	e.db.QueryRow(ctx, `SELECT status FROM refunds WHERE id = $1`, manualID).Scan(&st) //nolint:errcheck
	if st != "manual_required" {
		t.Fatalf("harus manual_required: %s", st)
	}
	if r := e.do(e.sa, "POST", "/v1/admin/refunds/"+manualID+"/mark-done", map[string]any{"reference": ""}); r.code != 422 {
		t.Fatalf("mark-done tanpa referensi: %s", r.raw)
	}
	r = e.must(e.do(e.sa, "POST", "/v1/admin/refunds/"+manualID+"/mark-done", map[string]any{"reference": "TRF-BCA-0001"}), 200)
	if r.body["status"] != "completed" || r.body["reference"] != "TRF-BCA-0001" {
		t.Fatalf("mark-done: %s", r.raw)
	}
	if r := e.do(e.sa, "POST", "/v1/admin/refunds/"+manualID+"/mark-done", map[string]any{"reference": "TRF-2"}); r.body["error"] != "invalid_state" {
		t.Fatalf("mark-done ulang: %s", r.raw)
	}
	e.checkLedger()

	// Ditolak: order tetap paid, tiket tetap berlaku.
	o3 := e.paidOrder(orgID, evID, catID, buyer, 1)
	rid := e.must(e.do(owner, "POST", base+"/orders/"+o3.String()+"/refund", map[string]any{"reason": "Salah beli kategori lomba"}), 201).body["id"].(string)
	e.must(e.do(e.sa, "POST", "/v1/admin/refunds/"+rid+"/reject", map[string]any{"reason": "Batas waktu refund sudah lewat"}), 200)
	e.db.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, o3).Scan(&orderStatus) //nolint:errcheck
	if orderStatus != "paid" {
		t.Fatalf("refund ditolak: order %s", orderStatus)
	}

	// needs_refund (bayar telat, kuota habis): belum ada order.paid → dibukukan saat refund.
	var nr uuid.UUID
	e.db.QueryRow(ctx, `INSERT INTO orders (org_id, event_id, category_id, buyer_id, reservation_id, idempotency_key, qty, subtotal, platform_fee, total, status, expires_at, paid_at)
		VALUES ($1, $2, $3, $4, $5, $5, 1, 350000, 10500, 350000, 'needs_refund', now(), now()) RETURNING id`, orgID, evID, catID, buyer, uuid.NewString()).Scan(&nr) //nolint:errcheck
	rid = e.must(e.do(owner, "POST", base+"/orders/"+nr.String()+"/refund", map[string]any{"reason": "Kuota habis saat pembayaran masuk"}), 201).body["id"].(string)
	before := e.balances(orgID)
	e.must(e.do(e.sa, "POST", "/v1/admin/refunds/"+rid+"/approve", nil), 200)
	if after := e.balances(orgID); after != before {
		t.Fatalf("needs_refund tidak mengubah saldo mitra: %+v → %+v", before, after)
	}
	e.checkLedger()

	// Email pembeli tanpa data sensitif; audit lengkap.
	tx, _ := e.db.Begin(ctx)
	defer tx.Rollback(ctx) //nolint:errcheck
	var payload []byte
	tx.QueryRow(ctx, `SELECT payload FROM outbox WHERE topic = 'refund.email' AND payload->>'refund_id' = $1 AND payload->>'audience' = 'buyer' LIMIT 1`, refundID).Scan(&payload) //nolint:errcheck
	m, ok, err := finance.RefundMail(ctx, tx, payload, "http://localhost:3000")
	if err != nil || !ok || m.To != e.email("pelari") || strings.Contains(m.Body, finNIK) || strings.Contains(m.Body, "Peserta 0") || !strings.Contains(m.Body, "Rp700.000") {
		t.Fatalf("email pembeli: %v %v %s %s", ok, err, m.To, m.Body)
	}
	for _, action := range []string{"refund.requested", "refund.created", "refund.approved", "refund.rejected", "refund.completed", "refund.manual_required", "refund.completed_manual"} {
		var n int
		e.db.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE org_id = $1 AND action = $2`, orgID, action).Scan(&n) //nolint:errcheck
		if n == 0 {
			t.Fatalf("audit %s tidak ada", action)
		}
	}
	e.noAccountLeak()
}

func TestFinanceReports(t *testing.T) {
	e := newFinEnv(t)
	ctx := context.Background()
	slug, orgID, owner := e.verifiedOrg("Org Lapor", finAccount)
	evID, catID := e.event(slug, owner, "Lari Lapor")
	e.db.Exec(ctx, `UPDATE events SET name = $2 WHERE id = $1`, evID, "=cmd|' /C calc'!A0") //nolint:errcheck
	buyer := e.user("pelari")
	e.paidOrder(orgID, evID, catID, buyer, 1)
	o2 := e.paidOrder(orgID, evID, catID, buyer, 2)
	e.must(e.do(e.sa, "POST", "/v1/admin/refunds", map[string]any{"order_id": o2, "reason": "Refund untuk uji laporan"}), 201)
	base := "/v1/org/" + slug

	r := e.must(e.do(owner, "GET", base+"/reports/sales", nil), 200)
	items := r.body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("ringkasan: %s", r.raw)
	}
	s := items[0].(map[string]any)
	// 3 tiket Rp350.000 lunas, 2 di-refund. Bersih = bagian mitra order 1 saja.
	want := map[string]float64{"orders_paid": 2, "orders_refunded": 1, "gross": 1050000, "platform_fee": 31500, "pg_fee": 8880, "refunds": 700000, "net": 335060}
	for k, v := range want {
		if s[k] != v {
			t.Fatalf("%s = %v, mau %v (%s)", k, s[k], v, r.raw)
		}
	}
	if b := e.balances(orgID); float64(b.Held+b.Available) != s["net"] {
		t.Fatalf("ringkasan tidak cocok dengan ledger: %+v", b)
	}

	for _, path := range []string{"/reports/sales.csv", "/reports/ledger.csv"} {
		req := httptest.NewRequest("GET", base+path, nil)
		req.Header.Set("X-User-ID", owner.String())
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != 200 || !strings.HasPrefix(body, "\uFEFF") || !strings.Contains(rec.Header().Get("Content-Type"), "text/csv") {
			t.Fatalf("%s: %d %q", path, rec.Code, body[:min(40, len(body))])
		}
		if strings.Contains(body, finNIK) || strings.Contains(body, "Peserta") || strings.Contains(body, finAccount) {
			t.Fatalf("%s memuat data peserta/rekening", path)
		}
		if path == "/reports/sales.csv" && (!strings.Contains(body, `"'=cmd|' /C calc'!A0"`) && !strings.Contains(body, `'=cmd|' /C calc'!A0`)) {
			t.Fatalf("injeksi rumus tidak dinetralkan: %s", body)
		}
		if path == "/reports/ledger.csv" && (!strings.Contains(body, ";'-674560;") || strings.Contains(body, ";-")) {
			t.Fatalf("angka negatif harus berawalan ': %s", body)
		}
	}
	var n int
	e.db.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE org_id = $1 AND action = 'report.download'`, orgID).Scan(&n) //nolint:errcheck
	if n != 2 {
		t.Fatalf("unduhan tercatat %d, mau 2", n)
	}
	led := e.must(e.do(owner, "GET", base+"/finance/ledger", nil), 200)
	if led.body["total"] != float64(3) || !strings.Contains(led.raw, "Refund order") {
		t.Fatalf("mutasi: %s", led.raw)
	}
	ov := e.must(e.do(e.sa, "GET", "/v1/admin/finance/overview", nil), 200)
	if ov.body["mock"] != true || ov.body["disburse_mode"] != "mock" {
		t.Fatalf("overview: %s", ov.raw)
	}
	e.checkLedger()
}

func TestLedgerIntegrityAndImmutable(t *testing.T) {
	e := newFinEnv(t)
	ctx := context.Background()
	e.checkLedger()
	if r := e.do(e.user("biasa"), "GET", "/v1/admin/ledger/check", nil); r.code != 404 {
		t.Fatalf("non super admin: %d", r.code)
	}
	// Rusak data di dalam transaksi yang di-rollback: hasil cek harus merah, DB lain tidak terganggu.
	tx, err := e.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var jid uuid.UUID
	tx.QueryRow(ctx, `INSERT INTO ledger_journals (ref_type, ref_id) VALUES ('uji.rusak', $1) RETURNING id`, uuid.NewString()).Scan(&jid) //nolint:errcheck
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (journal_id, account, debit) VALUES ($1, 'kas_midtrans', 999)`, jid); err != nil {
		t.Fatal(err)
	}
	rep, err := ledger.Check(ctx, tx)
	if err != nil || rep.OK || rep.Checks[0].OK || rep.Checks[1].OK {
		t.Fatalf("cek harus merah: %+v %v", rep, err)
	}
	// Ledger append-only: UPDATE/DELETE/TRUNCATE ditolak trigger.
	for _, q := range []string{`UPDATE ledger_entries SET debit = 1 WHERE journal_id = $1`, `DELETE FROM ledger_entries WHERE journal_id = $1`,
		`UPDATE ledger_journals SET memo = 'x' WHERE id = $1`, `DELETE FROM ledger_journals WHERE id = $1`} {
		if _, err := tx.Exec(ctx, `SAVEPOINT s`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, q, jid); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s harus ditolak: %v", q, err)
		}
		tx.Exec(ctx, `ROLLBACK TO SAVEPOINT s`) //nolint:errcheck
	}
	tx.Exec(ctx, `SAVEPOINT s`) //nolint:errcheck
	if _, err := tx.Exec(ctx, `TRUNCATE ledger_entries`); err == nil {
		t.Fatal("TRUNCATE harus ditolak")
	}
}
