package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type manifest struct {
	Event       map[string]any   `json:"event"`
	PublicKey   string           `json:"public_key"`
	GeneratedAt time.Time        `json:"generated_at"`
	ExpiresAt   time.Time        `json:"expires_at"`
	Version     string           `json:"version"`
	Incremental bool             `json:"incremental"`
	Count       int              `json:"count"`
	Tickets     []map[string]any `json:"tickets"`
}

// getManifest: GET dengan Accept-Encoding gzip; mengembalikan kode, isi mentah (sudah di-unzip), dan hasil parse.
func (e *finEnv) getManifest(user uuid.UUID, path string) (int, string, manifest) {
	e.t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("X-User-ID", user.String())
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	body := rec.Body.Bytes()
	if rec.Header().Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			e.t.Fatal(err)
		}
		body, _ = io.ReadAll(zr)
	}
	var m manifest
	if rec.Code == 200 {
		if err := json.Unmarshal(body, &m); err != nil {
			e.t.Fatalf("manifest bukan JSON utuh: %v", err)
		}
	}
	return rec.Code, string(body), m
}

func (e *finEnv) ticketTokens(orderID uuid.UUID) (ids, tokens []string) {
	e.t.Helper()
	rows, err := e.db.Query(context.Background(), `SELECT id::text, qr_token FROM tickets WHERE order_id = $1 ORDER BY bib_number`, orderID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, tok string
		rows.Scan(&id, &tok) //nolint:errcheck
		ids, tokens = append(ids, id), append(tokens, tok)
	}
	return ids, tokens
}

func TestScanManifest(t *testing.T) {
	e := newFinEnv(t)
	ctx := context.Background()
	slug, orgID, owner := e.verifiedOrg("Org Manifest", finAccount)
	evID, catID := e.event(slug, owner, "Lari Manifest")
	o1 := e.paidOrder(orgID, evID, catID, e.user("pelari1"), 2)
	o2 := e.paidOrder(orgID, evID, catID, e.user("pelari2"), 1)
	e.db.Exec(ctx, `UPDATE participants SET birth_date = '1990-01-01', emergency_name = 'Darurat CONTOH', emergency_phone = '081299990000', gender = 'F' WHERE order_id = $1`, o1) //nolint:errcheck
	slugB, orgB, ownerB := e.verifiedOrg("Org Manifest B", "5555666677")
	evB, catB := e.event(slugB, ownerB, "Lari B")
	e.paidOrder(orgB, evB, catB, e.user("pelariB"), 1)
	admin, staff, fin, stranger := e.member(orgID, "admin", "admin"), e.member(orgID, "staf", "rpc_staff"), e.member(orgID, "keu", "finance"), e.user("asing")
	path := "/v1/org/" + slug + "/events/" + evID.String() + "/scan-manifest"

	for user, want := range map[uuid.UUID]int{owner: 200, admin: 200, staff: 200, fin: 403, stranger: 404, ownerB: 404} {
		if code, _, _ := e.getManifest(user, path); code != want {
			t.Errorf("manifest oleh %v: %d, mau %d", user, code, want)
		}
	}
	if code, _, _ := e.getManifest(owner, "/v1/org/"+slug+"/events/"+evB.String()+"/scan-manifest"); code != 404 {
		t.Fatalf("event org lain: %d", code)
	}

	code, raw, m := e.getManifest(staff, path)
	if code != 200 || len(m.Tickets) != 3 || m.Count != 3 || m.Incremental || m.PublicKey != e.signer.PublicKeyBase64() {
		t.Fatalf("manifest: %d %s", code, raw)
	}
	if d := m.ExpiresAt.Sub(m.GeneratedAt); d != 24*time.Hour {
		t.Fatalf("kedaluwarsa default 24 jam, dapat %v", d)
	}
	allowed := map[string]bool{"id": true, "bib": true, "name": true, "category": true, "jersey": true, "status": true, "collected_at": true, "collected_by": true, "picked_up_by": true}
	for _, tk := range m.Tickets {
		for k := range tk {
			if !allowed[k] {
				t.Fatalf("kunci tak terduga di manifest: %q", k)
			}
		}
	}
	for _, s := range []string{finNIK, "peserta@uji.test", "081299990000", "Darurat CONTOH", "1990-01-01", "350000", "RAHASIA"} {
		if strings.Contains(raw, s) {
			t.Fatalf("manifest memuat data yang tidak perlu: %q", s)
		}
	}

	// Inkremental: mundurkan updated_at semua tiket, lalu ubah sebagian.
	e.db.Exec(ctx, `UPDATE tickets SET updated_at = now() - interval '10 minutes' WHERE event_id = $1`, evID) //nolint:errcheck
	_, _, base := e.getManifest(staff, path)
	ids1, tokens1 := e.ticketTokens(o1)
	e.must(e.do(staff, "POST", "/v1/org/"+slug+"/events/"+evID.String()+"/scan", map[string]any{"token": tokens1[0]}), 200)
	e.must(e.do(owner, "POST", "/v1/org/"+slug+"/orders/"+o2.String()+"/refund", map[string]any{"reason": "Peserta CONTOH batal ikut"}), 201)
	var refundID string
	e.db.QueryRow(ctx, `SELECT id FROM refunds WHERE order_id = $1`, o2).Scan(&refundID) //nolint:errcheck
	e.must(e.do(e.sa, "POST", "/v1/admin/refunds/"+refundID+"/approve", nil), 200)
	o3 := e.paidOrder(orgID, evID, catID, e.user("pelari3"), 1)
	_, _, inc := e.getManifest(staff, path+"?since="+base.Version)
	got := map[string]string{}
	for _, tk := range inc.Tickets {
		got[tk["id"].(string)] = tk["status"].(string)
	}
	ids2, _ := e.ticketTokens(o2)
	ids3, _ := e.ticketTokens(o3)
	want := map[string]string{ids1[0]: "collected", ids2[0]: "refunded", ids3[0]: "issued"}
	if !inc.Incremental || len(got) != len(want) {
		t.Fatalf("inkremental: %v, mau %v", got, want)
	}
	for id, st := range want {
		if got[id] != st {
			t.Fatalf("inkremental %s: %q, mau %q", id, got[id], st)
		}
	}
	for _, tk := range inc.Tickets {
		if tk["id"] == ids1[0] && (tk["collected_at"] == nil || tk["collected_by"] != "staf") {
			t.Fatalf("info pengambilan: %v", tk)
		}
	}
	if r := e.do(staff, "GET", path+"?since=abc", nil); r.code != 422 {
		t.Fatalf("since salah: %d", r.code)
	}

	// Audit: aktor, event, jumlah tiket; tanpa isi.
	var n int
	var after string
	e.db.QueryRow(ctx, `SELECT count(*), max(after::text) FROM audit_logs WHERE action = 'scan.manifest.download' AND actor_id = $1 AND entity_id = $2`, staff, evID.String()).Scan(&n, &after) //nolint:errcheck
	if n != 4 || !strings.Contains(after, `"tickets":`) || strings.Contains(after, "Peserta") {
		t.Fatalf("audit manifest %d: %s", n, after)
	}
}

func TestScanManifest10k(t *testing.T) {
	e := newFinEnv(t)
	ctx := context.Background()
	slug, orgID, owner := e.verifiedOrg("Org Manifest Besar", finAccount)
	evID, catID := e.event(slug, owner, "Lari Manifest Besar")
	o := e.paidOrder(orgID, evID, catID, e.user("pelari"), 1)
	if _, err := e.db.Exec(ctx, `
		WITH p AS (
		  INSERT INTO participants (order_id, org_id, event_id, category_id, full_name, email, id_number, jersey_size)
		  SELECT $1, $2, $3, $4, 'Peserta CONTOH ' || g, 'contoh' || g || '@uji.test', lpad(g::text, 16, '7'), 'M'
		  FROM generate_series(1, 10000) g RETURNING id)
		INSERT INTO tickets (participant_id, order_id, org_id, event_id, category_id, bib_number, qr_token)
		SELECT p.id, $1, $2, $3, $4, 'X' || row_number() OVER (), 'uji' FROM p`, o, orgID, evID, catID); err != nil {
		t.Fatal(err)
	}
	defer debug.SetGCPercent(debug.SetGCPercent(5))
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak atomic.Uint64
	stop, done := make(chan struct{}), make(chan struct{})
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
	req := httptest.NewRequest("GET", "/v1/org/"+slug+"/events/"+evID.String()+"/scan-manifest", nil)
	req.Header.Set("X-User-ID", owner.String())
	req.Header.Set("Accept-Encoding", "gzip")
	w := &countingWriter{h: http.Header{}}
	start := time.Now()
	e.h.ServeHTTP(w, req)
	elapsed := time.Since(start)
	close(stop)
	<-done
	growth := int64(peak.Load()) - int64(before.HeapInuse)
	t.Logf("manifest 10.001 tiket: %.0f KB gzip dalam %v; heap in-use awal %.1f MB, puncak +%.2f MB",
		float64(w.n.Load())/1024, elapsed.Round(time.Millisecond), float64(before.HeapInuse)/1e6, float64(growth)/1e6)
	if w.h.Get("Content-Encoding") != "gzip" || w.n.Load() == 0 {
		t.Fatal("manifest tidak di-gzip")
	}
	if growth > 4<<20 { // tanpa streaming: 10.000 struct + JSON utuh ≈ 5 MB lebih
		t.Fatalf("puncak heap +%d byte: tidak streaming?", growth)
	}
}

type batchResp struct {
	ServerTime time.Time `json:"server_time"`
	Results    []struct {
		ClientOpID string         `json:"client_op_id"`
		Result     string         `json:"result"`
		Ticket     map[string]any `json:"ticket"`
	} `json:"results"`
}

func (e *finEnv) batch(user uuid.UUID, path, device string, entries ...map[string]any) (int, batchResp, string) {
	e.t.Helper()
	r := e.do(user, "POST", path, map[string]any{"device_id": device, "entries": entries})
	var out batchResp
	json.Unmarshal([]byte(r.raw), &out) //nolint:errcheck
	return r.code, out, r.raw
}

func TestScanBatch(t *testing.T) {
	e := newFinEnv(t)
	ctx := context.Background()
	slug, orgID, owner := e.verifiedOrg("Org Batch", finAccount)
	evID, catID := e.event(slug, owner, "Lari Batch")
	_, ev2Cat := e.event(slug, owner, "Lari Batch Lain")
	var ev2 uuid.UUID
	e.db.QueryRow(ctx, `SELECT event_id FROM ticket_categories WHERE id = $1`, ev2Cat).Scan(&ev2) //nolint:errcheck
	o := e.paidOrder(orgID, evID, catID, e.user("pelari"), 4)
	oOther := e.paidOrder(orgID, ev2, ev2Cat, e.user("pelari-lain"), 1)
	oRefund := e.paidOrder(orgID, evID, catID, e.user("pelari-refund"), 1)
	slugB, orgB, ownerB := e.verifiedOrg("Org Batch B", "5555666677")
	evB, catB := e.event(slugB, ownerB, "Lari B")
	oB := e.paidOrder(orgB, evB, catB, e.user("pelariB"), 1)
	staff, fin, stranger := e.member(orgID, "staf", "rpc_staff"), e.member(orgID, "keu", "finance"), e.user("asing")
	ids, tokens := e.ticketTokens(o)
	_, otherTokens := e.ticketTokens(oOther)
	idsB, tokensB := e.ticketTokens(oB)
	refundIDs, refundTokens := e.ticketTokens(oRefund)
	e.must(e.do(owner, "POST", "/v1/org/"+slug+"/orders/"+oRefund.String()+"/refund", map[string]any{"reason": "Peserta CONTOH batal ikut"}), 201)
	var refundID string
	e.db.QueryRow(ctx, `SELECT id FROM refunds WHERE order_id = $1`, oRefund).Scan(&refundID) //nolint:errcheck
	e.must(e.do(e.sa, "POST", "/v1/admin/refunds/"+refundID+"/approve", nil), 200)

	path := "/v1/org/" + slug + "/events/" + evID.String() + "/scan/batch"
	devA, devB := uuid.NewString(), uuid.NewString()
	op := func(token, ticketID string, at time.Time) map[string]any {
		return map[string]any{"client_op_id": uuid.NewString(), "token": token, "ticket_id": ticketID, "scanned_at": at.Format(time.RFC3339Nano)}
	}

	// Peran.
	for user, want := range map[uuid.UUID]int{fin: 403, stranger: 404, ownerB: 404} {
		if code, _, _ := e.batch(user, path, devA, op(tokens[3], "", time.Now())); code != want {
			t.Errorf("batch oleh %v: %d", user, code)
		}
	}

	// Idempoten: entri sama dikirim dua kali → satu efek, respons sama. Jam perangkat ngawur tidak berpengaruh.
	skewed := time.Date(2001, 1, 1, 7, 0, 0, 0, time.UTC)
	first := op(tokens[0], "", skewed)
	code, r1, raw1 := e.batch(staff, path, devA, first)
	_, r2, _ := e.batch(staff, path, devA, first)
	if code != 200 || r1.Results[0].Result != "ok" || r2.Results[0].Result != "ok" || r1.Results[0].ClientOpID != first["client_op_id"] {
		t.Fatalf("idempoten: %s", raw1)
	}
	var logs int
	var src, dev string
	var atDevice, collectedAt time.Time
	e.db.QueryRow(ctx, `SELECT count(*), max(source), max(device_id::text), max(scanned_at_device) FROM scan_logs WHERE client_op_id = $1`, first["client_op_id"]).Scan(&logs, &src, &dev, &atDevice) //nolint:errcheck
	e.db.QueryRow(ctx, `SELECT collected_at FROM tickets WHERE id = $1`, ids[0]).Scan(&collectedAt)                                                                                  //nolint:errcheck
	if logs != 1 || src != "offline_sync" || dev != devA || !atDevice.Equal(skewed) || time.Since(collectedAt) > time.Minute {
		t.Fatalf("log: %d %s %s %v; collected_at %v", logs, src, dev, atDevice, collectedAt)
	}

	// Dua perangkat offline memindai tiket yang sama, bersamaan → tepat satu ok.
	var wg sync.WaitGroup
	var oks, conflicts atomic.Int32
	for _, d := range []string{devA, devB, uuid.NewString(), uuid.NewString()} {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			_, r, raw := e.batch(staff, path, d, op(tokens[1], "", time.Now()))
			switch r.Results[0].Result {
			case "ok":
				oks.Add(1)
			case "already_collected":
				if r.Results[0].Ticket["collected_by_staff"] != "staf" || r.Results[0].Ticket["collected_at"] == nil {
					t.Errorf("info bentrokan: %s", raw)
				}
				conflicts.Add(1)
			default:
				t.Errorf("paralel: %s", raw)
			}
		}(d)
	}
	wg.Wait()
	if oks.Load() != 1 || conflicts.Load() != 3 {
		t.Fatalf("bentrokan: %d ok, %d already_collected", oks.Load(), conflicts.Load())
	}

	// Satu batch berisi berbagai kasus; urutan hasil = urutan entri.
	forged := []byte(tokens[2])
	forged[10] ^= 1 // ubah satu karakter ID tiket
	_, r, raw := e.batch(staff, path, devB,
		op(string(forged), "", time.Now()),
		op(otherTokens[0], "", time.Now()),
		op(refundTokens[0], "", time.Now()),
		op("", refundIDs[0], time.Now()),
		op(tokensB[0], "", time.Now()),
		op("", idsB[0], time.Now()),
		map[string]any{"client_op_id": "bukan-uuid", "token": tokens[2]},
		op("", ids[2], time.Now()),
	)
	wantRes := []string{"invalid", "wrong_event", "refunded", "refunded", "wrong_event", "not_found", "invalid", "ok"}
	for i, w := range wantRes {
		if r.Results[i].Result != w {
			t.Fatalf("entri %d: %q, mau %q (%s)", i, r.Results[i].Result, w, raw)
		}
	}
	if strings.Contains(raw, "pelariB") || r.Results[5].Ticket != nil || r.Results[4].Ticket != nil {
		t.Fatalf("data org lain bocor: %s", raw)
	}
	var collectedB int
	e.db.QueryRow(ctx, `SELECT count(*) FROM tickets WHERE order_id = $1 AND status = 'collected'`, oB).Scan(&collectedB) //nolint:errcheck
	if collectedB != 0 {
		t.Fatal("tiket org lain berubah")
	}

	// Batas: > 200 entri → 422; body > 128 KB → 413; device_id wajib.
	many := make([]map[string]any, 201)
	for i := range many {
		many[i] = op(tokens[3], "", time.Now())
	}
	if code, _, _ := e.batch(staff, path, devA, many...); code != 422 {
		t.Fatalf("201 entri: %d", code)
	}
	big := op(tokens[3], "", time.Now())
	big["picked_up_by"] = strings.Repeat("x", 130<<10)
	if code, _, _ := e.batch(staff, path, devA, big); code != 413 {
		t.Fatalf("body besar: %d", code)
	}
	if code, _, _ := e.batch(staff, path, "perangkat", op(tokens[3], "", time.Now())); code != 422 {
		t.Fatalf("device_id salah: %d", code)
	}

	// Log konsisten: hasil online & offline tercatat dengan source yang benar; jumlah "diambil" benar.
	e.must(e.do(staff, "POST", "/v1/org/"+slug+"/events/"+evID.String()+"/scan", map[string]any{"token": tokens[0]}), 200)
	var online, offlineOK, collected int
	e.db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE source = 'online'), count(*) FILTER (WHERE source = 'offline_sync' AND result = 'ok') FROM scan_logs WHERE event_id = $1`, evID).Scan(&online, &offlineOK) //nolint:errcheck
	e.db.QueryRow(ctx, `SELECT count(*) FROM tickets WHERE event_id = $1 AND status = 'collected'`, evID).Scan(&collected)                                                                                       //nolint:errcheck
	if online != 1 || offlineOK != 3 || collected != 3 {
		t.Fatalf("log online %d, offline ok %d, diambil %d", online, offlineOK, collected)
	}
}

// Scan online tidak berubah: hasil sama seperti sebelumnya dan log ber-source online.
func TestScanOnlineUnchanged(t *testing.T) {
	e := newFinEnv(t)
	ctx := context.Background()
	slug, orgID, owner := e.verifiedOrg("Org Scan Online", finAccount)
	evID, catID := e.event(slug, owner, "Lari Online")
	_, tokens := e.ticketTokens(e.paidOrder(orgID, evID, catID, e.user("pelari"), 1))
	path := "/v1/org/" + slug + "/events/" + evID.String() + "/scan"
	for i, want := range []string{"ok", "already_collected"} {
		if r := e.must(e.do(owner, "POST", path, map[string]any{"token": tokens[0], "picked_up_by": "Kakak"}), 200); r.body["result"] != want {
			t.Fatalf("scan %d: %s", i, r.raw)
		}
	}
	if r := e.must(e.do(owner, "POST", path, map[string]any{"token": tokens[0][:len(tokens[0])-2] + "AA"}), 200); r.body["result"] != "invalid" {
		t.Fatalf("palsu: %s", r.raw)
	}
	var n int
	e.db.QueryRow(ctx, `SELECT count(*) FROM scan_logs WHERE event_id = $1 AND source = 'online' AND client_op_id IS NULL AND device_id IS NULL`, evID).Scan(&n) //nolint:errcheck
	if n != 3 {
		t.Fatalf("log online: %d", n)
	}
}
