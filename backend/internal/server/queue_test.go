package server

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/gavinadlan/racego/backend/internal/queue"
)

// Antrian virtual lewat router lengkap: pass wajib untuk event ber-antrian, dilepas setelah
// reservasi, event tanpa antrian tidak berubah, dan isolasi mitra.
func TestQueueReserveFlow(t *testing.T) {
	e := newPortalEnv(t, true)
	ctx := context.Background()
	owner, runner, other, rival := e.user("owner"), e.user("pelari"), e.user("pelari2"), e.user("rival")
	org := "/v1/org/" + e.createOrg(owner, "Antri Uji "+e.tag)
	rivalOrg := "/v1/org/" + e.createOrg(rival, "Rival Uji "+e.tag)
	now := time.Now()
	open, close := now.Add(-time.Minute), now.AddDate(0, 0, 30)

	body := eventBody("Antri "+e.tag, "Bogor", open, close, cat("5K", "5", 5))
	body["waiting_room"] = true
	queued := e.must(e.do(owner, "POST", org+"/events", body), 201)
	qid := queued.body["id"].(string)
	if queued.body["waiting_room"] != true {
		t.Fatalf("waiting_room tidak tersimpan: %s", queued.raw)
	}
	queuedCat := catID(e.must(e.do(owner, "POST", org+"/events/"+qid+"/publish", nil), 200), "5K")
	plain := e.must(e.do(owner, "POST", org+"/events", eventBody("Biasa "+e.tag, "Bogor", open, close, cat("5K", "5", 5))), 201)
	plainCat := catID(e.must(e.do(owner, "POST", org+"/events/"+plain.body["id"].(string)+"/publish", nil), 200), "5K")
	draft := e.must(e.do(owner, "POST", org+"/events", body), 201)

	if d := e.must(e.do(uuid.Nil, "GET", "/v1/events/"+qid, nil), 200); d.body["waiting_room"] != true || d.body["lobby_opens_at"] == nil {
		t.Fatalf("detail publik: %s", d.raw)
	}

	reserve := func(user uuid.UUID, catID, pass string) resp {
		h := map[string]string{}
		if pass != "" {
			h["X-Queue-Pass"] = pass
		}
		return e.doH(user, "POST", "/v1/categories/"+catID+"/reservations", map[string]any{"qty": 1}, h)
	}
	if r := reserve(runner, queuedCat, ""); r.code != 403 || r.body["error"] != "queue_required" {
		t.Fatalf("reservasi tanpa pass: %s", r.raw)
	}
	// Event tanpa antrian: tetap tanpa pass.
	e.must(reserve(runner, plainCat, ""), 201)

	qpath := "/v1/events/" + qid + "/queue"
	if r := e.must(e.do(runner, "GET", qpath+"/me", nil), 200); r.body["state"] != "not_in_queue" {
		t.Fatalf("belum bergabung: %s", r.raw)
	}
	if r := e.do(runner, "GET", qpath+"/me", nil); r.code != 429 || r.body["retry_after"] == nil {
		t.Fatalf("status < 1 detik harus 429: %d %s", r.code, r.raw)
	}
	if r := e.do(uuid.Nil, "POST", qpath, nil); r.code != 401 {
		t.Fatalf("tanpa login: %d", r.code)
	}
	j := e.must(e.do(runner, "POST", qpath, nil), 201)
	if j.body["state"] != "waiting" || j.body["position"] != float64(1) || j.body["server_time"] == nil {
		t.Fatalf("join: %s", j.raw)
	}
	e.must(e.do(runner, "POST", qpath, nil), 200)
	e.must(e.do(other, "POST", qpath, nil), 201)
	e.must(e.do(uuid.Nil, "GET", "/v1/events/"+uuid.NewString()+"/queue/me", nil), 401)
	if r := e.do(runner, "POST", "/v1/events/"+plain.body["id"].(string)+"/queue", nil); r.code != 404 {
		t.Fatalf("event tanpa antrian: %d", r.code)
	}
	if r := e.do(runner, "POST", "/v1/events/"+draft.body["id"].(string)+"/queue", nil); r.code != 404 {
		t.Fatalf("event draft: %d", r.code)
	}

	ev := queue.Event{ID: qid, SaleOpenAt: open, SaleCloseAt: close, CategoryIDs: []string{queuedCat}}
	if n, err := e.q.Tick(ctx, ev, time.Now()); err != nil || n != 2 {
		t.Fatalf("tick: %d %v", n, err)
	}
	e.mr.FastForward(2 * time.Second) // jeda batas status
	st := e.must(e.do(runner, "GET", qpath+"/me", nil), 200)
	pass, _ := st.body["pass"].(string)
	if st.body["state"] != "admitted" || pass == "" || st.body["pass_expires_at"] == nil {
		t.Fatalf("dipanggil: %s", st.raw)
	}
	otherSt := e.must(e.do(other, "GET", qpath+"/me", nil), 200)

	if r := reserve(other, queuedCat, pass); r.code != 403 || r.body["error"] != "queue_required" {
		t.Fatalf("pass milik user lain: %s", r.raw)
	}
	e.must(reserve(runner, queuedCat, pass), 201)
	e.mr.FastForward(2 * time.Second)
	if r := e.must(e.do(runner, "GET", qpath+"/me", nil), 200); r.body["state"] != "not_in_queue" {
		t.Fatalf("pass harus dilepas setelah reservasi: %s", r.raw)
	}
	if r := reserve(runner, queuedCat, pass); r.code != 403 || r.body["error"] != "queue_required" {
		t.Fatalf("pass yang sudah dipakai: %s", r.raw)
	}
	// Keluar antrian mencabut pass.
	e.must(e.do(other, "DELETE", qpath+"/me", nil), 204)
	if r := reserve(other, queuedCat, otherSt.body["pass"].(string)); r.code != 403 {
		t.Fatalf("pass setelah keluar: %s", r.raw)
	}

	// Mitra: ringkasan langsung, isolasi org lain, dan kunci setelah penjualan dibuka.
	sum := e.must(e.do(owner, "GET", org+"/events/"+qid+"/queue", nil), 200)
	if sum.body["admitted_total"] != float64(2) || sum.body["active"] != float64(0) || sum.body["phase"] != "selling" {
		t.Fatalf("ringkasan: %s", sum.raw)
	}
	if r := e.do(rival, "GET", org+"/events/"+qid+"/queue", nil); r.code != 404 {
		t.Fatalf("org lain lewat slug asing: %d", r.code)
	}
	if r := e.do(rival, "GET", rivalOrg+"/events/"+qid+"/queue", nil); r.code != 404 {
		t.Fatalf("org lain lewat slug sendiri: %d", r.code)
	}
	patch := eventBody("Antri "+e.tag, "Bogor", open, close)
	delete(patch, "categories")
	patch["waiting_room"] = false
	if r := e.do(rival, "PATCH", rivalOrg+"/events/"+qid, patch); r.code != 404 {
		t.Fatalf("org lain mengubah antrian: %d %s", r.code, r.raw)
	}
	if r := e.do(owner, "PATCH", org+"/events/"+qid, patch); r.code != 422 || r.field("waiting_room") == "" {
		t.Fatalf("ubah antrian setelah buka: %d %s", r.code, r.raw)
	}
	delete(patch, "waiting_room") // tidak dikirim = tidak diubah
	if r := e.must(e.do(owner, "PATCH", org+"/events/"+qid, patch), 200); r.body["waiting_room"] != true {
		t.Fatalf("PATCH tanpa waiting_room: %s", r.raw)
	}

	// Sebelum lobi: tidak bisa bergabung; antrian masih boleh diubah sebelum jam buka.
	later := eventBody("Nanti "+e.tag, "Bogor", now.Add(time.Hour), close, cat("5K", "5", 5))
	later["waiting_room"] = true
	lid := e.must(e.do(owner, "POST", org+"/events", later), 201).body["id"].(string)
	e.must(e.do(owner, "POST", org+"/events/"+lid+"/publish", nil), 200)
	if r := e.do(runner, "POST", "/v1/events/"+lid+"/queue", nil); r.code != 409 || r.body["error"] != "queue_not_open" {
		t.Fatalf("sebelum lobi: %d %s", r.code, r.raw)
	}
	delete(later, "categories")
	later["waiting_room"] = false
	if r := e.must(e.do(owner, "PATCH", org+"/events/"+lid, later), 200); r.body["waiting_room"] != false {
		t.Fatalf("matikan antrian sebelum buka: %s", r.raw)
	}
	var audits int
	if err := e.db.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE entity_id = $1 AND action = 'event.updated' AND after->>'waiting_room' = 'false'`, lid).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit log perubahan antrian: %d %v", audits, err)
	}
}

// GET /v1/orders/{id} membawa qty & tickets_issued untuk polling "tiket sedang diterbitkan".
func TestOrderTicketsIssued(t *testing.T) {
	e := newPortalEnv(t, true)
	ctx := context.Background()
	owner, runner := e.user("owner"), e.user("pelari")
	org := "/v1/org/" + e.createOrg(owner, "Order Uji "+e.tag)
	now := time.Now()
	ev := e.must(e.do(owner, "POST", org+"/events", eventBody("Order "+e.tag, "Bogor", now.Add(-time.Minute), now.AddDate(0, 0, 30), cat("5K", "5", 5))), 201)
	c := catID(e.must(e.do(owner, "POST", org+"/events/"+ev.body["id"].(string)+"/publish", nil), 200), "5K")
	var orderID uuid.UUID
	if err := e.db.QueryRow(ctx, `
		INSERT INTO orders (org_id, event_id, category_id, buyer_id, reservation_id, idempotency_key, qty, subtotal, platform_fee, total, status, expires_at, paid_at)
		SELECT org_id, event_id, id, $2, 'r-'||$3, 'k-'||$3, 2, 300000, 9000, 300000, 'paid', now(), now() FROM ticket_categories WHERE id = $1
		RETURNING id`, c, runner, e.tag).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	r := e.must(e.do(runner, "GET", "/v1/orders/"+orderID.String(), nil), 200)
	if r.body["qty"] != float64(2) || r.body["tickets_issued"] != float64(0) {
		t.Fatalf("order: %s", r.raw)
	}
	e.must(e.do(owner, "GET", "/v1/orders/"+orderID.String(), nil), 404)
}
