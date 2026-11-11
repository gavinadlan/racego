package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/ledger"
	"github.com/gavinadlan/racego/backend/internal/worker"
)

// Uji beban (docs/11-performa.md): menyiapkan pengguna sintetis + event uji langsung di database,
// dan memeriksa integritas sesudahnya. Tidak ada pintu belakang di server: pengguna sintetis memakai
// sesi biasa (cookie), dan kedua perintah menolak jalan di luar development / di database lain.

// LoadgenAllowed: hanya APP_ENV=development dan database yang namanya berawalan/berakhiran "loadtest".
func LoadgenAllowed(env, dbName string) error {
	if err := DevOnly(env); err != nil {
		return err
	}
	if !strings.HasPrefix(dbName, "loadtest_") && !strings.HasSuffix(dbName, "_loadtest") {
		return fmt.Errorf("database %q bukan database uji beban: nama harus berawalan loadtest_ atau berakhiran _loadtest (lihat make loadtest-up)", dbName)
	}
	return nil
}

func loadgenGuard(ctx context.Context, db *pgxpool.Pool, env string) error {
	var name string
	if err := db.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		return err
	}
	return LoadgenAllowed(env, name)
}

const loadgenOrg = "loadtest-org"

type seedCategory struct {
	ID    uuid.UUID `json:"id"`
	Code  string    `json:"code"`
	Quota int       `json:"quota"`
}

type seedUser struct {
	Token string `json:"t"`
	IP    string `json:"ip"`
}

// seedFile dibaca skrip k6 dan loadgen-check. Berisi token sesi: simpan di loadtest/results/ (di-.gitignore).
type seedFile struct {
	EventID     uuid.UUID      `json:"event_id"`
	WaitingRoom bool           `json:"waiting_room"`
	SaleOpenAt  time.Time      `json:"sale_open_at"`
	Categories  []seedCategory `json:"categories"`
	Users       []seedUser     `json:"users"`
}

// loadgenSeed: N pengguna + sesi, satu event uji baru dengan 3 kategori (kuota dibagi 50/30/20).
func loadgenSeed(ctx context.Context, db *pgxpool.Pool, env string, args []string) error {
	fs := flag.NewFlagSet("loadgen-seed", flag.ExitOnError)
	users := fs.Int("users", 100, "jumlah pengguna sintetis")
	quota := fs.Int("quota", 500, "total kuota tiket (dibagi ke 3 kategori)")
	openIn := fs.Duration("open-in", 30*time.Second, "penjualan dibuka sekian lama dari sekarang")
	queue := fs.Bool("queue", true, "event memakai antrean virtual")
	out := fs.String("out", "", "berkas keluaran JSON (wajib)")
	_ = fs.Parse(args)
	if *out == "" || *users < 1 || *quota < 3 {
		return errors.New("pemakaian: migrate loadgen-seed --users N --quota Q --open-in 30s --queue=true --out berkas.json")
	}
	if err := loadgenGuard(ctx, db, env); err != nil {
		return err
	}

	seed := seedFile{WaitingRoom: *queue, SaleOpenAt: time.Now().Add(*openIn).Truncate(time.Millisecond)}
	tokens := make([]string, *users)
	hashes := make([][]byte, *users)
	for i := range tokens {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		tokens[i] = base64.RawURLEncoding.EncodeToString(raw)
		h := sha256.Sum256([]byte(tokens[i]))
		hashes[i] = h[:]
		// ±32 pengguna per IP (mis. NAT kampus), supaya batas laju per IP tetap ikut teruji.
		seed.Users = append(seed.Users, seedUser{Token: tokens[i], IP: fmt.Sprintf("10.%d.%d.%d", (i>>13)&255, (i>>5)&255, (i&31)+1)})
	}

	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		var orgID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO organizations (slug, name, kyc_status, city) VALUES ($1, 'Uji Beban', 'verified', 'Jakarta')
			ON CONFLICT (slug) DO UPDATE SET kyc_status = 'verified' RETURNING id`, loadgenOrg).Scan(&orgID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO users (email, name)
			SELECT 'lt-' || lpad(i::text, 6, '0') || '@loadtest.invalid', 'Pelari Uji ' || i FROM generate_series(1, $1) i
			ON CONFLICT (email) DO NOTHING`, *users); err != nil {
			return err
		}
		// Sesi lama pengguna sintetis dibuang: token lama tidak berlaku lagi setelah seed ulang.
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'lt-%@loadtest.invalid')`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO sessions (user_id, token_hash, expires_at, user_agent, ip)
			SELECT u.id, h.hash, now() + interval '1 day', 'loadgen', ''
			FROM unnest($1::bytea[]) WITH ORDINALITY h(hash, i)
			JOIN users u ON u.email = 'lt-' || lpad(h.i::text, 6, '0') || '@loadtest.invalid'`, hashes); err != nil {
			return err
		}
		// Event uji sebelumnya ditutup supaya worker tidak terus mengurus antreannya.
		if _, err := tx.Exec(ctx, `UPDATE events SET status = 'closed' WHERE org_id = $1 AND status = 'published'`, orgID); err != nil {
			return err
		}
		slug := "war-" + strconv.FormatInt(time.Now().UnixMilli(), 36)
		if err := tx.QueryRow(ctx, `
			INSERT INTO events (org_id, slug, name, venue, city, race_date, sale_open_at, sale_close_at, status, waiting_room)
			VALUES ($1, $2, 'War Tiket Uji ' || $2, 'Monas', 'Jakarta', current_date + 60, $3, now() + interval '2 days', 'published', $4)
			RETURNING id`, orgID, slug, seed.SaleOpenAt, *queue).Scan(&seed.EventID); err != nil {
			return err
		}
		q5, q10 := *quota/2, *quota*3/10
		for _, c := range []struct {
			code   string
			km     int
			quota  int
			prefix string
		}{{"5K", 5, q5, "5"}, {"10K", 10, q10, "10"}, {"21K", 21, *quota - q5 - q10, "21"}} {
			sc := seedCategory{Code: c.code, Quota: c.quota}
			if err := tx.QueryRow(ctx, `
				INSERT INTO ticket_categories (event_id, org_id, code, name, distance_km, price, quota, max_per_user, bib_prefix)
				VALUES ($1, $2, $3, 'Uji ' || $3, $4, 150000, $5, 1, $6) RETURNING id`,
				seed.EventID, orgID, c.code, c.km, c.quota, c.prefix).Scan(&sc.ID); err != nil {
				return err
			}
			seed.Categories = append(seed.Categories, sc)
		}
		return nil
	})
	if err != nil {
		return err
	}
	b, _ := json.Marshal(seed)
	if err := os.WriteFile(*out, b, 0o600); err != nil {
		return err
	}
	fmt.Printf("✓ %d pengguna sintetis + event uji %s (antrean=%v, kuota %d, buka %s) → %s\n",
		*users, seed.EventID, *queue, *quota, seed.SaleOpenAt.Format("15:04:05"), *out)
	return nil
}

// ===== Pemeriksaan integritas =====

type checkResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// loadgenCheck memeriksa event uji setelah skenario war*. Keluar dengan error bila ada yang gagal.
// --settle: tunggu juga sampai semua reservasi/order yang ditinggal kedaluwarsa dan slotnya kembali.
func loadgenCheck(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, env string, args []string) error {
	fs := flag.NewFlagSet("loadgen-check", flag.ExitOnError)
	seedPath := fs.String("seed", "", "berkas keluaran loadgen-seed (wajib)")
	drain := fs.Duration("drain", 2*time.Minute, "batas menunggu outbox kosong")
	settle := fs.Duration("settle", 0, "batas menunggu hold & order pending habis (0 = tidak menunggu)")
	converge := fs.Duration("converge", 60*time.Second, "batas menunggu stok Redis konvergen (rekonsiliasi worker)")
	jsonOut := fs.String("json", "", "tulis hasil sebagai JSON ke berkas ini")
	_ = fs.Parse(args)
	raw, err := os.ReadFile(*seedPath)
	if err != nil {
		return fmt.Errorf("baca --seed: %w", err)
	}
	var seed seedFile
	if err := json.Unmarshal(raw, &seed); err != nil {
		return err
	}
	if err := loadgenGuard(ctx, db, env); err != nil {
		return err
	}

	// Tunggu worker menghabiskan outbox (tiket, ledger, email) sebelum membandingkan angka.
	backlog := func() (n int, err error) {
		err = db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE processed_at IS NULL AND attempts < $1`, worker.MaxAttempts).Scan(&n)
		return
	}
	if err := waitFor(ctx, *drain, func() (bool, error) { n, err := backlog(); return n == 0, err }); err != nil {
		return err
	}
	pendingAndHolds := func() (pending, holds int, err error) {
		if err = db.QueryRow(ctx, `SELECT count(*) FROM orders WHERE event_id = $1 AND status = 'pending'`, seed.EventID).Scan(&pending); err != nil {
			return
		}
		for _, c := range seed.Categories {
			n, e := rdb.ZCard(ctx, "{cat:"+c.ID.String()+"}:holds").Result()
			if e != nil {
				return 0, 0, e
			}
			holds += int(n)
		}
		return
	}
	settled := *settle == 0
	if *settle > 0 {
		err := waitFor(ctx, *settle, func() (bool, error) {
			p, h, err := pendingAndHolds()
			if err != nil || p+h > 0 {
				return false, err
			}
			n, err := backlog() // order.released dari order kedaluwarsa
			return n == 0, err
		})
		if err != nil {
			return err
		}
		settled = true
	}

	var res []checkResult
	add := func(name string, ok bool, format string, a ...any) {
		res = append(res, checkResult{name, ok, fmt.Sprintf(format, a...)})
	}
	count := func(sql string, a ...any) (n int64) {
		if e := db.QueryRow(ctx, sql, a...).Scan(&n); e != nil && err == nil {
			err = e
		}
		return n
	}
	ev := seed.EventID

	// (a) tiket ≤ kuota per kategori; order lunas konsisten dengan tiket.
	var totalTickets, totalQuota int64
	oversold := 0
	var perCat []string
	for _, c := range seed.Categories {
		t := count(`SELECT count(*) FROM tickets WHERE category_id = $1`, c.ID)
		paid := count(`SELECT coalesce(sum(qty), 0) FROM orders WHERE category_id = $1 AND status = 'paid'`, c.ID)
		if t > int64(c.Quota) || paid > int64(c.Quota) {
			oversold++
		}
		totalTickets, totalQuota = totalTickets+t, totalQuota+int64(c.Quota)
		perCat = append(perCat, fmt.Sprintf("%s %d/%d", c.Code, t, c.Quota))
	}
	add("a1 tiket terbit ≤ kuota (tanpa oversell)", oversold == 0, "%s", strings.Join(perCat, ", "))
	paidQty := count(`SELECT coalesce(sum(qty), 0) FROM orders WHERE event_id = $1 AND status = 'paid'`, ev)
	add("a2 order lunas = tiket terbit", paidQty == totalTickets, "qty lunas %d, tiket %d", paidQty, totalTickets)
	needsRefund := count(`SELECT count(*) FROM orders WHERE event_id = $1 AND status = 'needs_refund'`, ev)
	add("a3 tidak ada order needs_refund", needsRefund == 0, "%d order (bayar telat saat kuota habis)", needsRefund)

	// (b) BIB ganda / tiket ganda per peserta.
	dupBib := count(`SELECT count(*) FROM (SELECT 1 FROM tickets WHERE event_id = $1 GROUP BY category_id, bib_number HAVING count(*) > 1) x`, ev)
	dupPart := count(`SELECT count(*) FROM (SELECT 1 FROM tickets WHERE event_id = $1 GROUP BY participant_id HAVING count(*) > 1) x`, ev)
	add("b tidak ada BIB ganda / tiket ganda per peserta", dupBib+dupPart == 0, "BIB ganda %d, peserta bertiket ganda %d", dupBib, dupPart)

	// (c) batas slot per pembeli per kategori.
	overLimit := count(`
		SELECT count(*) FROM (
		  SELECT 1 FROM orders o JOIN ticket_categories c ON c.id = o.category_id
		  WHERE o.event_id = $1 AND o.status IN ('pending', 'paid')
		  GROUP BY o.buyer_id, o.category_id, c.max_per_user HAVING sum(o.qty) > c.max_per_user) x`, ev)
	add("c batas per pembeli per kategori tidak terlampaui", overLimit == 0, "%d pembeli melampaui batas", overLimit)

	// (d) ledger seimbang; tepat satu jurnal order.paid per order lunas.
	rep, lerr := ledger.Check(ctx, db)
	if lerr != nil {
		return lerr
	}
	var bad []string
	for _, it := range rep.Checks {
		if !it.OK {
			bad = append(bad, it.Name+": "+it.Detail)
		}
	}
	add("d1 ledger/check hijau", rep.OK, "%d pemeriksaan %s", len(rep.Checks), strings.Join(bad, "; "))
	paidOrders := count(`SELECT count(*) FROM orders WHERE event_id = $1 AND status = 'paid'`, ev)
	journals := count(`
		SELECT count(*) FROM ledger_journals j JOIN orders o ON o.id::text = j.ref_id
		WHERE j.ref_type = 'order.paid' AND o.event_id = $1`, ev)
	orphan := count(`
		SELECT count(*) FROM ledger_journals j JOIN orders o ON o.id::text = j.ref_id
		WHERE j.ref_type = 'order.paid' AND o.event_id = $1 AND o.status <> 'paid'`, ev)
	add("d2 satu jurnal order.paid per order lunas", paidOrders == journals && orphan == 0,
		"order lunas %d, jurnal %d, jurnal untuk order tidak lunas %d", paidOrders, journals, orphan)

	// (e) Redis vs DB: stok + slot ditahan + terjual = kuota, per kategori. Rekonsiliasi stok di worker
	// (checkout.StockReconciler) berjalan tiap 15 dtk dan butuh dua putaran, jadi selisih akibat Redis
	// yang kehilangan tulisan boleh bertahan sampai --converge sebelum dinyatakan gagal.
	reconcile := func() (mismatch int, detail string, rerr error) {
		var recon []string
		for _, c := range seed.Categories {
			base := "{cat:" + c.ID.String() + "}"
			stock, e := rdb.Get(ctx, base+":stock").Int()
			if e != nil && !errors.Is(e, redis.Nil) {
				return 0, "", e
			}
			rids, e := rdb.ZRange(ctx, base+":holds", 0, -1).Result()
			if e != nil {
				return 0, "", e
			}
			held := 0
			for _, rid := range rids {
				v, _ := rdb.Get(ctx, base+":hold:"+rid).Result()
				_, q, _ := strings.Cut(v, "|")
				n, _ := strconv.Atoi(q)
				held += n
			}
			sold := int(count(`SELECT coalesce(sum(qty), 0) FROM orders WHERE category_id = $1 AND status = 'paid'`, c.ID))
			if stock+held+sold != c.Quota {
				mismatch++
			}
			recon = append(recon, fmt.Sprintf("%s stok %d + ditahan %d + terjual %d = %d (kuota %d)", c.Code, stock, held, sold, stock+held+sold, c.Quota))
		}
		return mismatch, strings.Join(recon, "; "), nil
	}
	mismatch, recon, rerr := reconcile()
	for end := time.Now().Add(*converge); rerr == nil && mismatch > 0 && time.Now().Before(end); {
		time.Sleep(5 * time.Second)
		mismatch, recon, rerr = reconcile()
	}
	if rerr != nil {
		return rerr
	}
	add("e stok Redis = kuota − terjual − ditahan", mismatch == 0, "%s", recon)

	// (f) tidak ada order pending tanpa hold aktif; outbox bersih.
	var pendingRIDs []struct {
		Cat uuid.UUID
		RID string
	}
	rows, qerr := db.Query(ctx, `SELECT category_id, reservation_id FROM orders WHERE event_id = $1 AND status = 'pending'`, ev)
	if qerr != nil {
		return qerr
	}
	if pendingRIDs, qerr = pgx.CollectRows(rows, pgx.RowToStructByPos[struct {
		Cat uuid.UUID
		RID string
	}]); qerr != nil {
		return qerr
	}
	stale := 0
	for _, p := range pendingRIDs {
		if _, e := rdb.ZScore(ctx, "{cat:"+p.Cat.String()+"}:holds", p.RID).Result(); errors.Is(e, redis.Nil) {
			stale++
		} else if e != nil {
			return e
		}
	}
	add("f1 tidak ada order pending tanpa reservasi aktif", stale == 0, "%d order pending, %d tanpa hold", len(pendingRIDs), stale)
	left, berr := backlog()
	if berr != nil {
		return berr
	}
	dead := count(`SELECT count(*) FROM outbox WHERE processed_at IS NULL AND attempts >= $1`, worker.MaxAttempts)
	add("f2 outbox kosong setelah drain", left == 0 && dead == 0, "tertinggal %d, berhenti dicoba %d", left, dead)
	if settled && *settle > 0 {
		p, h, e := pendingAndHolds()
		if e != nil {
			return e
		}
		add("f3 setelah masa tunggu: tidak ada order pending / hold tersisa", p+h == 0, "pending %d, hold %d", p, h)
	}
	if err != nil {
		return err
	}

	ok := true
	fmt.Printf("Pemeriksaan integritas event %s (kuota %d, tiket terbit %d):\n", ev, totalQuota, totalTickets)
	for _, r := range res {
		mark := "✓"
		if !r.OK {
			mark, ok = "✗", false
		}
		fmt.Printf("  %s %-52s %s\n", mark, r.Name, r.Detail)
	}
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(map[string]any{"ok": ok, "event_id": ev, "quota": totalQuota, "tickets": totalTickets, "checks": res}, "", "  ")
		if err := os.WriteFile(*jsonOut, b, 0o600); err != nil {
			return err
		}
	}
	if !ok {
		return errors.New("PEMERIKSAAN INTEGRITAS GAGAL")
	}
	fmt.Println("✓ semua pemeriksaan integritas lulus")
	return nil
}

func waitFor(ctx context.Context, limit time.Duration, done func() (bool, error)) error {
	deadline := time.Now().Add(limit)
	for {
		ok, err := done()
		if err != nil || ok {
			return err
		}
		if time.Now().After(deadline) {
			return nil // batas habis: pemeriksaan di bawah yang melaporkan apa yang tertinggal
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
