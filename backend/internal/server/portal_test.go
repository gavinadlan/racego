package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/checkout"
	"github.com/gavinadlan/racego/backend/internal/config"
	"github.com/gavinadlan/racego/backend/internal/notify"
	"github.com/gavinadlan/racego/backend/internal/payment"
	"github.com/gavinadlan/racego/backend/internal/queue"
	"github.com/gavinadlan/racego/backend/internal/ticket"
)

// Uji portal mitra lewat router lengkap dengan Postgres sungguhan (data dibuat & dihapus sendiri).
// Jalan bila RACEGO_TEST_DATABASE_URL diisi; identitas memakai header X-User-ID (mode development).

type portalEnv struct {
	t   *testing.T
	db  *pgxpool.Pool
	inv *checkout.Inventory
	mr  *miniredis.Miniredis
	q   *queue.Queue
	h   http.Handler
	tag string
}

func newPortalEnv(t *testing.T, autoVerify bool) *portalEnv {
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
	cfg := config.Config{Env: "development", FrontendURL: "http://localhost:3000", ReservationTTL: 15 * time.Minute,
		AutoVerifyOrgs: autoVerify, UploadDir: t.TempDir(),
		Queue: config.QueueConfig{LobbyMinutes: 15, AdmitPerSec: 20, MaxActive: 300, PassTTL: 10 * time.Minute, HeartbeatTTL: 45 * time.Second}}
	passSigner, err := queue.NewPassSigner("", true)
	if err != nil {
		t.Fatal(err)
	}
	q := queue.New(rdb, inv, passSigner, cfg.Queue)
	e := &portalEnv{t: t, db: db, inv: inv, mr: mr, q: q, tag: strings.ReplaceAll(uuid.NewString()[:8], "-", ""),
		h: NewRouter(Deps{Cfg: cfg, DB: db, Redis: rdb, Inv: inv, Snap: payment.NewMockSnap(cfg.FrontendURL), Signer: signer,
			Mailer: notify.NewMailer("localhost:1", "", "", "x@x"), Queue: q})}
	t.Cleanup(func() {
		pattern := "%" + e.tag + "@uji.test"
		orgs := `SELECT id FROM organizations WHERE created_by IN (SELECT id FROM users WHERE email LIKE $1)`
		for _, q := range []string{
			`DELETE FROM audit_logs WHERE org_id IN (` + orgs + `)`,
			`DELETE FROM orders WHERE org_id IN (` + orgs + `)`,
			`DELETE FROM ticket_categories WHERE org_id IN (` + orgs + `)`,
			`DELETE FROM events WHERE org_id IN (` + orgs + `)`,
			`DELETE FROM organizations WHERE id IN (` + orgs + `)`,
			`DELETE FROM users WHERE email LIKE $1`,
		} {
			if _, err := db.Exec(ctx, q, pattern); err != nil {
				t.Errorf("bersih-bersih: %v", err)
			}
		}
		rdb.Close()
		db.Close()
	})
	return e
}

func (e *portalEnv) user(name string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.db.QueryRow(context.Background(), `INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id`,
		name+"."+e.tag+"@uji.test", name).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *portalEnv) email(name string) string { return name + "." + e.tag + "@uji.test" }

type resp struct {
	code int
	body map[string]any
	raw  string
}

func (r resp) field(k string) string {
	f, _ := r.body["fields"].(map[string]any)
	s, _ := f[k].(string)
	return s
}

func (e *portalEnv) do(user uuid.UUID, method, path string, body any) resp {
	e.t.Helper()
	return e.doH(user, method, path, body, nil)
}

func (e *portalEnv) doH(user uuid.UUID, method, path string, body any, headers map[string]string) resp {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	if user != uuid.Nil {
		req.Header.Set("X-User-ID", user.String())
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	out := resp{code: rec.Code, raw: rec.Body.String()}
	json.Unmarshal(rec.Body.Bytes(), &out.body) //nolint:errcheck
	return out
}

func (e *portalEnv) must(r resp, code int) resp {
	e.t.Helper()
	if r.code != code {
		e.t.Fatalf("status %d, mau %d: %s", r.code, code, r.raw)
	}
	return r
}

func (e *portalEnv) createOrg(owner uuid.UUID, name string) string {
	e.t.Helper()
	r := e.must(e.do(owner, "POST", "/v1/orgs", map[string]any{"name": name, "city": "Bogor", "description": "Uji"}), 201)
	return r.body["slug"].(string)
}

var wibZone = time.FixedZone("WIB", 7*3600)

func eventBody(name, city string, open, close time.Time, cats ...map[string]any) map[string]any {
	if cats == nil {
		cats = []map[string]any{}
	}
	return map[string]any{
		"name": name, "city": city, "venue": "Alun-alun", "race_date": time.Now().In(wibZone).AddDate(0, 0, 60).Format("2006-01-02"),
		"start_time": "05:30", "sale_open_at": open, "sale_close_at": close, "categories": cats,
	}
}

func cat(code, prefix string, quota int) map[string]any {
	return map[string]any{"code": code, "name": "Kategori " + code, "distance_km": 10, "price": 150000, "quota": quota,
		"max_per_user": 2, "bib_prefix": prefix}
}

func catID(ev resp, code string) string {
	for _, c := range ev.body["categories"].([]any) {
		m := c.(map[string]any)
		if m["code"] == code {
			return m["id"].(string)
		}
	}
	return ""
}

func (e *portalEnv) stock(catID string) int {
	e.t.Helper()
	n, ok, err := e.inv.Available(context.Background(), catID)
	if err != nil || !ok {
		e.t.Fatalf("stok %s belum ada di Redis (%v)", catID, err)
	}
	return n
}

func TestPortalPublishAndSalesRules(t *testing.T) {
	e := newPortalEnv(t, true)
	ctx := context.Background()
	owner, runner := e.user("owner"), e.user("pelari")
	slug := e.createOrg(owner, "Lari Uji "+e.tag)
	if r := e.do(owner, "GET", "/v1/orgs/slug-check?slug="+slug, nil); r.body["available"] != false {
		t.Fatalf("slug yang sudah dipakai harus tidak tersedia: %s", r.raw)
	}
	org := "/v1/org/" + slug

	// Tanpa kategori → tidak bisa terbit.
	now := time.Now()
	empty := e.must(e.do(owner, "POST", org+"/events", eventBody("Event Kosong", "Bogor", now.Add(time.Hour), now.AddDate(0, 0, 30))), 201)
	if r := e.do(owner, "POST", org+"/events/"+empty.body["id"].(string)+"/publish", nil); r.code != 422 || r.field("categories") == "" {
		t.Fatalf("terbit tanpa kategori: %s", r.raw)
	}

	city := "Kota" + e.tag
	name := "Lari Draft " + e.tag
	ev := e.must(e.do(owner, "POST", org+"/events", eventBody(name, city, now.Add(-time.Minute), now.AddDate(0, 0, 30),
		cat("5K", "5", 100), cat("10K", "10", 50), cat("21K", "21", 20))), 201)
	id := ev.body["id"].(string)
	if ev.body["status"] != "draft" || len(ev.body["categories"].([]any)) != 3 {
		t.Fatalf("event baru: %s", ev.raw)
	}

	// Draft tidak bocor ke endpoint publik mana pun.
	visible := func() (list, detail, organizer, filters bool) {
		r := e.do(uuid.Nil, "GET", "/v1/events?q="+e.tag, nil)
		list = strings.Contains(r.raw, id)
		detail = e.do(uuid.Nil, "GET", "/v1/events/"+id, nil).code == 200
		organizer = strings.Contains(e.do(uuid.Nil, "GET", "/v1/organizers/"+slug, nil).raw, id)
		filters = strings.Contains(e.do(uuid.Nil, "GET", "/v1/events/filters", nil).raw, city)
		return
	}
	if l, d, o, f := visible(); l || d || o || f {
		t.Fatalf("draft bocor: list=%v detail=%v organizer=%v filters=%v", l, d, o, f)
	}

	pub := e.must(e.do(owner, "POST", org+"/events/"+id+"/publish", nil), 200)
	if pub.body["status"] != "published" {
		t.Fatalf("status: %s", pub.raw)
	}
	// Stok langsung ada di Redis tanpa menunggu worker.
	for code, quota := range map[string]int{"5K": 100, "10K": 50, "21K": 20} {
		if got := e.stock(catID(pub, code)); got != quota {
			t.Fatalf("stok %s = %d, mau %d", code, got, quota)
		}
	}
	if l, d, o, f := visible(); !l || !d || !o || !f {
		t.Fatalf("event terbit harus tampil: list=%v detail=%v organizer=%v filters=%v", l, d, o, f)
	}

	// Worker: stok yang hilang dari Redis dimuat ulang oleh sinkron berkala.
	c10 := catID(pub, "10K")
	e.mr.Del("{cat:" + c10 + "}:stock")
	if _, err := checkout.SyncOpenEvents(ctx, e.db, e.inv); err != nil {
		t.Fatal(err)
	}
	if e.stock(c10) != 50 {
		t.Fatal("sinkron worker tidak memuat kategori baru")
	}

	// Ada penjualan: 2 slot 10K lunas.
	res, err := e.inv.Reserve(ctx, checkout.ReserveRequest{CategoryID: c10, UserID: runner.String(), Qty: 2, MaxPerUser: 2, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(ctx, `
		INSERT INTO orders (org_id, event_id, category_id, buyer_id, reservation_id, idempotency_key, qty, subtotal, platform_fee, total, status, expires_at, paid_at)
		SELECT org_id, event_id, id, $2, $3, $3, 2, 300000, 9000, 300000, 'paid', now(), now() FROM ticket_categories WHERE id = $1`,
		c10, runner, res.ID); err != nil {
		t.Fatal(err)
	}
	catPath := org + "/events/" + id + "/categories/" + c10
	upd := func(mod func(map[string]any)) resp {
		b := cat("10K", "10", 50)
		mod(b)
		return e.do(owner, "PATCH", catPath, b)
	}
	if r := upd(func(b map[string]any) { b["price"] = 99000 }); r.code != 422 || !strings.Contains(r.field("price"), "terkunci") && !strings.Contains(r.field("price"), "Terkunci") {
		t.Fatalf("harga harus terkunci: %s", r.raw)
	}
	for k, v := range map[string]any{"code": "10KM", "bib_prefix": "11", "distance_km": 10.5} {
		if r := upd(func(b map[string]any) { b[k] = v }); r.code != 422 || r.field(k) == "" {
			t.Fatalf("%s harus terkunci: %s", k, r.raw)
		}
	}
	if r := upd(func(b map[string]any) { b["quota"] = 1 }); r.code != 422 || r.field("quota") == "" {
		t.Fatalf("kuota di bawah terjual harus ditolak: %s", r.raw)
	}
	before := e.stock(c10)
	e.must(upd(func(b map[string]any) { b["quota"] = 80; b["name"] = "10K Baru" }), 200)
	if got := e.stock(c10); got != before+30 {
		t.Fatalf("stok setelah kuota +30 = %d, mau %d", got, before+30)
	}
	if r := e.do(owner, "DELETE", catPath, nil); r.code != 409 {
		t.Fatalf("hapus kategori berpenjualan: %s", r.raw)
	}
	if r := e.do(owner, "POST", org+"/events/"+id+"/unpublish", nil); r.code != 409 {
		t.Fatalf("unpublish berpenjualan: %s", r.raw)
	}
	if r := e.do(owner, "DELETE", org+"/events/"+id, nil); r.code != 409 {
		t.Fatalf("hapus event berpenjualan: %s", r.raw)
	}
	// Kategori baru setelah penjualan dibuka ditolak.
	if r := e.do(owner, "POST", org+"/events/"+id+"/categories", cat("42K", "42", 10)); r.code != 409 {
		t.Fatalf("tambah kategori setelah penjualan dibuka: %s", r.raw)
	}

	// Tutup penjualan → reservasi & order ditolak, event tetap tampil sebagai "closed".
	e.must(e.do(owner, "POST", org+"/events/"+id+"/close", nil), 200)
	if r := e.do(runner, "POST", "/v1/categories/"+catID(pub, "5K")+"/reservations", map[string]any{"qty": 1}); r.code != 409 {
		t.Fatalf("reservasi di event ditutup: %s", r.raw)
	}
	if r := e.do(uuid.Nil, "GET", "/v1/events/"+id, nil); r.body["status"] != "closed" {
		t.Fatalf("detail publik event ditutup: %s", r.raw)
	}
	var audits int
	e.db.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE entity_id = $1 AND action IN ('event.created', 'event.published', 'event.closed')`, id).Scan(&audits) //nolint:errcheck
	if audits != 3 {
		t.Fatalf("audit event: %d, mau 3", audits)
	}
}

func TestPortalTenantIsolationAndRoles(t *testing.T) {
	e := newPortalEnv(t, true)
	ownerA, ownerB, staff, fin := e.user("ownera"), e.user("ownerb"), e.user("staf"), e.user("keuangan")
	a, b := "/v1/org/"+e.createOrg(ownerA, "Org A "+e.tag), "/v1/org/"+e.createOrg(ownerB, "Org B "+e.tag)
	now := time.Now()
	evA := e.must(e.do(ownerA, "POST", a+"/events", eventBody("Event A", "Bogor", now.Add(time.Hour), now.AddDate(0, 0, 30), cat("5K", "5", 10))), 201)
	idA, catA := evA.body["id"].(string), catID(evA, "5K")
	e.must(e.do(ownerB, "POST", b+"/events", eventBody("Event B", "Bogor", now.Add(time.Hour), now.AddDate(0, 0, 30))), 201)

	// Owner B ke org A, dan owner B ke event A lewat org B: semua 404.
	png := pngFile(t)
	for _, base := range []string{a, b} {
		for _, c := range []struct{ method, path string }{
			{"GET", "/events/" + idA}, {"PATCH", "/events/" + idA}, {"DELETE", "/events/" + idA},
			{"POST", "/events/" + idA + "/publish"}, {"POST", "/events/" + idA + "/close"}, {"POST", "/events/" + idA + "/unpublish"},
			{"POST", "/events/" + idA + "/categories"}, {"PATCH", "/events/" + idA + "/categories/" + catA},
			{"DELETE", "/events/" + idA + "/categories/" + catA},
		} {
			var body any
			switch {
			case c.method == "PATCH" && strings.Contains(c.path, "categories"), c.method == "POST" && strings.HasSuffix(c.path, "categories"):
				body = cat("5K", "5", 20)
			case c.method == "PATCH":
				body = eventBody("Dibajak", "Bogor", now.Add(time.Hour), now.AddDate(0, 0, 30))
				delete(body.(map[string]any), "categories")
			}
			if r := e.do(ownerB, c.method, base+c.path, body); r.code != 404 {
				t.Errorf("owner B %s %s: %d %s", c.method, base+c.path, r.code, r.raw)
			}
		}
	}
	for _, c := range []struct{ method, path string }{{"GET", "/events"}, {"GET", "/profile"}, {"PATCH", "/profile"}, {"GET", "/members"}, {"POST", "/members"}, {"POST", "/events"}} {
		if r := e.do(ownerB, c.method, a+c.path, map[string]any{}); r.code != 404 {
			t.Errorf("owner B %s org A%s: %d", c.method, c.path, r.code)
		}
	}
	if code := e.upload(ownerB, a, png); code != 404 {
		t.Errorf("owner B unggah ke org A: %d", code)
	}
	if code := e.upload(ownerA, a, png); code != 201 {
		t.Errorf("owner A unggah: %d", code)
	}
	if r := e.do(ownerA, "GET", a+"/events/"+idA, nil); r.code != 200 {
		t.Fatalf("owner A lihat event sendiri: %s", r.raw)
	}

	// Anggota tim.
	if r := e.do(ownerA, "POST", a+"/members", map[string]any{"email": "belum.pernah." + e.tag + "@uji.test", "role": "rpc_staff"}); r.code != 404 || r.body["error"] != "user_not_found" {
		t.Fatalf("email belum login: %s", r.raw)
	}
	e.must(e.do(ownerA, "POST", a+"/members", map[string]any{"email": e.email("staf"), "role": "rpc_staff"}), 201)
	e.must(e.do(ownerA, "POST", a+"/members", map[string]any{"email": e.email("keuangan"), "role": "finance"}), 201)
	if r := e.do(ownerA, "POST", a+"/members", map[string]any{"email": e.email("staf"), "role": "owner"}); r.code != 422 {
		t.Fatalf("undang sebagai owner: %s", r.raw)
	}

	// rpc_staff & finance: baca boleh, tulis ditolak.
	for _, u := range []uuid.UUID{staff, fin} {
		e.must(e.do(u, "GET", a+"/events", nil), 200)
		e.must(e.do(u, "GET", a+"/events/"+idA, nil), 200)
		for _, c := range []struct{ method, path string }{
			{"POST", "/events"}, {"PATCH", "/events/" + idA}, {"POST", "/events/" + idA + "/publish"}, {"DELETE", "/events/" + idA},
			{"PATCH", "/events/" + idA + "/categories/" + catA}, {"PATCH", "/profile"}, {"GET", "/members"}, {"POST", "/members"},
		} {
			if r := e.do(u, c.method, a+c.path, map[string]any{}); r.code != 403 {
				t.Errorf("%s %s oleh peran baca: %d", c.method, c.path, r.code)
			}
		}
		if code := e.upload(u, a, png); code != 403 {
			t.Errorf("unggah oleh peran baca: %d", code)
		}
	}

	// Owner tidak bisa mengubah/menghapus dirinya sendiri; owner terakhir aman.
	if r := e.do(ownerA, "DELETE", a+"/members/"+ownerA.String(), nil); r.code != 409 {
		t.Fatalf("owner hapus diri sendiri: %s", r.raw)
	}
	if r := e.do(ownerA, "PATCH", a+"/members/"+ownerA.String(), map[string]any{"role": "admin"}); r.code != 409 {
		t.Fatalf("owner turunkan diri sendiri: %s", r.raw)
	}
	// Dua owner: owner baru boleh menurunkan owner lama, lalu tidak bisa menghapus dirinya (owner terakhir).
	e.must(e.do(ownerA, "PATCH", a+"/members/"+staff.String(), map[string]any{"role": "owner"}), 200)
	e.must(e.do(staff, "PATCH", a+"/members/"+ownerA.String(), map[string]any{"role": "admin"}), 200)
	if r := e.do(ownerA, "GET", a+"/members", nil); r.code != 403 {
		t.Fatalf("mantan owner masih bisa kelola tim: %d", r.code)
	}
	if r := e.do(staff, "DELETE", a+"/members/"+staff.String(), nil); r.code != 409 {
		t.Fatalf("owner terakhir hapus diri: %s", r.raw)
	}
	e.must(e.do(staff, "DELETE", a+"/members/"+fin.String(), nil), 200)
	if r := e.do(fin, "GET", a+"/events", nil); r.code != 404 {
		t.Fatalf("anggota yang dihapus masih punya akses: %d", r.code)
	}
}

func TestPortalOrgLimitAndPending(t *testing.T) {
	e := newPortalEnv(t, false)
	u := e.user("rajin")
	var slugs []string
	for i := 1; i <= 3; i++ {
		slugs = append(slugs, e.createOrg(u, fmt.Sprintf("Org %d %s", i, e.tag)))
	}
	if r := e.do(u, "POST", "/v1/orgs", map[string]any{"name": "Org 4 " + e.tag, "city": "Bogor"}); r.code != 429 {
		t.Fatalf("org ke-4: %s", r.raw)
	}
	// Slug yang sama persis ditolak dengan pesan di kolom slug.
	if r := e.do(e.user("lain"), "POST", "/v1/orgs", map[string]any{"name": "Apa Saja", "city": "Bogor", "slug": slugs[0]}); r.code != 422 || r.field("slug") == "" {
		t.Fatalf("slug kembar: %s", r.raw)
	}

	// AUTO_VERIFY_ORGS=false: org pending boleh draft, tidak boleh terbit, tidak tampil publik.
	org := "/v1/org/" + slugs[0]
	if r := e.must(e.do(u, "GET", org+"/profile", nil), 200); r.body["kyc_status"] != "pending" {
		t.Fatalf("org baru harus pending: %s", r.raw)
	}
	now := time.Now()
	ev := e.must(e.do(u, "POST", org+"/events", eventBody("Event Pending", "Bogor", now.Add(time.Hour), now.AddDate(0, 0, 30), cat("5K", "5", 10))), 201)
	id := ev.body["id"].(string)
	if r := e.do(u, "POST", org+"/events/"+id+"/publish", nil); r.code != 409 || r.body["error"] != "org_not_verified" {
		t.Fatalf("terbit saat pending: %s", r.raw)
	}
	if r := e.do(uuid.Nil, "GET", "/v1/organizers/"+slugs[0], nil); r.code != 404 {
		t.Fatalf("profil org pending harus 404: %d", r.code)
	}
	if strings.Contains(e.do(uuid.Nil, "GET", "/v1/organizers", nil).raw, slugs[0]) {
		t.Fatal("org pending tampil di direktori")
	}
	e.db.Exec(context.Background(), `UPDATE organizations SET kyc_status = 'verified' WHERE slug = $1`, slugs[0]) //nolint:errcheck
	e.must(e.do(u, "POST", org+"/events/"+id+"/publish", nil), 200)

	// Batas 20 draft per org.
	for i := 0; i < 20; i++ {
		e.must(e.do(u, "POST", org+"/events", eventBody(fmt.Sprintf("Draft %d", i), "Bogor", now.Add(time.Hour), now.AddDate(0, 0, 30))), 201)
	}
	if r := e.do(u, "POST", org+"/events", eventBody("Draft 21", "Bogor", now.Add(time.Hour), now.AddDate(0, 0, 30))); r.code != 422 || r.body["error"] != "too_many_drafts" {
		t.Fatalf("draft ke-21: %s", r.raw)
	}
}

// Kuota diturunkan 100 → 60 lewat API saat 300 pembeli berebut: reservasi tidak pernah melebihi
// kuota yang tersimpan di database.
func TestPortalQuotaChangeDuringReservations(t *testing.T) {
	e := newPortalEnv(t, true)
	owner := e.user("owner")
	org := "/v1/org/" + e.createOrg(owner, "Org Kuota "+e.tag)
	now := time.Now()
	ev := e.must(e.do(owner, "POST", org+"/events", eventBody("Event Kuota", "Bogor", now.Add(-time.Minute), now.AddDate(0, 0, 30), cat("10K", "10", 100))), 201)
	id := ev.body["id"].(string)
	c := catID(ev, "10K")
	e.must(e.do(owner, "POST", org+"/events/"+id+"/publish", nil), 200)

	ctx := context.Background()
	var ok atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 300; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := e.inv.Reserve(ctx, checkout.ReserveRequest{CategoryID: c, UserID: uuid.NewString(), Qty: 1, MaxPerUser: 1, TTL: time.Minute}); err == nil {
				ok.Add(1)
			}
		}()
	}
	var patch resp
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		patch = e.do(owner, "PATCH", org+"/events/"+id+"/categories/"+c, cat("10K", "10", 60))
	}()
	close(start)
	wg.Wait()

	var quota int
	e.db.QueryRow(ctx, `SELECT quota FROM ticket_categories WHERE id = $1`, c).Scan(&quota) //nolint:errcheck
	if patch.code != 200 && patch.code != 422 {
		t.Fatalf("PATCH kuota: %s", patch.raw)
	}
	if (patch.code == 200) != (quota == 60) {
		t.Fatalf("PATCH %d tapi kuota DB %d", patch.code, quota)
	}
	if left := e.stock(c); int(ok.Load()) != quota || left != 0 {
		t.Fatalf("kuota %d, reservasi %d, sisa stok %d", quota, ok.Load(), left)
	}
}

func pngFile(t *testing.T) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (e *portalEnv) upload(user uuid.UUID, org string, data []byte) int {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "logo.png")
	fw.Write(data) //nolint:errcheck
	mw.Close()
	req := httptest.NewRequest("POST", org+"/uploads?kind=logo", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-User-ID", user.String())
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec.Code
}
