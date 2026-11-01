package ticket

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/ratelimit"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

// Scanner offline: manifest (daftar tiket minimal untuk verifikasi lokal) dan sinkron batch.
// Lihat docs/05-tiket-qr-dan-scan.md.

const (
	manifestPerUser = 30  // unduhan manifest (penuh/inkremental) per pengguna per 10 menit
	batchMax        = 200 // entri per sinkron
	batchBodyMax    = 128 << 10
	// versionOverlap: ?since= mengulang perubahan sejauh ini ke belakang, agar perubahan yang commit
	// belakangan (updated_at diisi saat UPDATE, bukan saat commit) tidak terlewat. Duplikat aman:
	// klien menimpa per ticket_id.
	versionOverlap = time.Minute
)

// SetOffline mengaktifkan manifest & batch (pembatas laju di Redis, masa berlaku manifest).
func (h *Handler) SetOffline(rdb redis.Cmdable, manifestTTL time.Duration) {
	if manifestTTL <= 0 {
		manifestTTL = 24 * time.Hour
	}
	h.rdb, h.manifestTTL = rdb, manifestTTL
}

func (h *Handler) eventFromURL(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	m, _ := tenant.FromContext(r.Context())
	eventID, err := uuid.Parse(chi.URLParam(r, "eventID"))
	if err != nil || !h.eventBelongsToOrg(r.Context(), eventID, m.OrgID) {
		httpx.Error(w, http.StatusNotFound, "event_not_found", "Event tidak ditemukan")
		return uuid.Nil, false
	}
	return eventID, true
}

// ManifestTicket: hanya yang perlu untuk memutuskan di meja race pack. Tanpa email, nomor identitas,
// telepon, kontak darurat, tanggal lahir, atau data pembayaran.
type ManifestTicket struct {
	ID          uuid.UUID  `json:"id"`
	Bib         string     `json:"bib"`
	Name        string     `json:"name"`
	Category    string     `json:"category"`
	Jersey      string     `json:"jersey"`
	Status      string     `json:"status"`
	CollectedAt *time.Time `json:"collected_at,omitempty"`
	CollectedBy *string    `json:"collected_by,omitempty"` // nama staf yang menandai
	PickedUpBy  *string    `json:"picked_up_by,omitempty"` // "diwakilkan oleh"
}

// Manifest: GET /v1/org/{org}/events/{eventID}/scan-manifest[?since=<version>] — owner, admin, rpc_staff.
// Di-stream per baris dari kursor database dan di-gzip; ?since mengembalikan tiket yang berubah/baru.
func (h *Handler) Manifest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	userID, _ := auth.UserID(ctx)
	eventID, ok := h.eventFromURL(w, r)
	if !ok {
		return
	}
	since := time.Time{}
	if s := r.URL.Query().Get("since"); s != "" {
		us, err := strconv.ParseInt(s, 10, 64)
		if err != nil || us < 0 {
			httpx.Validation(w, httpx.Fields{"since": "Versi manifest tidak valid"})
			return
		}
		since = time.UnixMicro(us).Add(-versionOverlap)
	}
	if err := ratelimit.Allow(ctx, h.rdb, "scan_manifest:"+userID.String(), manifestPerUser, 10*time.Minute); err != nil {
		tooMany(w, r, err, "Terlalu sering mengunduh data offline.")
		return
	}

	var ev struct {
		Name, RaceDate string
		Total          int
	}
	now := time.Now()
	if err := h.db.QueryRow(ctx, `SELECT e.name, e.race_date::text, (SELECT count(*) FROM tickets t WHERE t.event_id = e.id AND t.org_id = $2 AND t.updated_at > $3)
		FROM events e WHERE e.id = $1`, eventID, m.OrgID, since).Scan(&ev.Name, &ev.RaceDate, &ev.Total); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	// Audit dulu (jumlah tiket, bukan isinya): unduhan yang tidak tercatat tidak boleh terjadi.
	if _, err := h.db.Exec(ctx, `INSERT INTO audit_logs (actor_id, org_id, action, entity, entity_id, after) VALUES ($1, $2, 'scan.manifest.download', 'event', $3, $4)`,
		userID, m.OrgID, eventID.String(), map[string]any{"tickets": ev.Total, "incremental": !since.IsZero(), "ip": auth.ClientIP(r)}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	rows, err := h.db.Query(ctx, `
		SELECT t.id, t.bib_number, p.full_name, c.code, p.jersey_size, t.status, t.collected_at, u.name, t.collected_by_name
		FROM tickets t JOIN participants p ON p.id = t.participant_id JOIN ticket_categories c ON c.id = t.category_id
		LEFT JOIN users u ON u.id = t.collected_by
		WHERE t.event_id = $1 AND t.org_id = $2 AND t.updated_at > $3
		ORDER BY t.id`, eventID, m.OrgID, since)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	defer rows.Close()

	hd := w.Header()
	hd.Set("Content-Type", "application/json")
	hd.Set("Cache-Control", "no-store")
	var out io.Writer = w
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		hd.Set("Content-Encoding", "gzip")
		hd.Add("Vary", "Accept-Encoding")
		gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
		defer gz.Close()
		out = gz
	}
	head, _ := json.Marshal(map[string]any{
		"event":        map[string]any{"id": eventID, "name": ev.Name, "race_date": ev.RaceDate, "org_slug": m.OrgSlug},
		"algorithm":    "Ed25519",
		"public_key":   h.signer.PublicKeyBase64(),
		"generated_at": now,
		"expires_at":   now.Add(h.manifestTTL),
		// Versi = waktu server saat dibuat (mikrodetik); dikirim balik sebagai ?since= untuk pembaruan.
		"version":     strconv.FormatInt(now.UnixMicro(), 10),
		"incremental": !since.IsZero(),
		"count":       ev.Total,
	})
	// {…kepala…, "tickets":[ … ]}: kepala ditulis tanpa '}' penutup, lalu tiket satu per satu.
	_, err = io.WriteString(out, string(head[:len(head)-1])+`,"tickets":[`)
	enc := json.NewEncoder(out)
	n := 0
	for err == nil && rows.Next() {
		var t ManifestTicket
		if err = rows.Scan(&t.ID, &t.Bib, &t.Name, &t.Category, &t.Jersey, &t.Status, &t.CollectedAt, &t.CollectedBy, &t.PickedUpBy); err != nil {
			break
		}
		if n > 0 {
			_, err = io.WriteString(out, ",")
		}
		if err == nil {
			err = enc.Encode(t)
		}
		n++
	}
	if err == nil {
		err = rows.Err()
	}
	if err == nil {
		_, err = io.WriteString(out, "]}")
	}
	if err != nil && ctx.Err() == nil {
		// Header sudah terkirim: hanya bisa dicatat (tanpa isi); klien menolak JSON terpotong.
		slog.ErrorContext(ctx, "stream manifest scan", "event_id", eventID, "err", err)
	}
}

func tooMany(w http.ResponseWriter, r *http.Request, err error, msg string) {
	var ex ratelimit.Exceeded
	if !errors.As(err, &ex) {
		httpx.Internal(w, r, err)
		return
	}
	secs := int(math.Ceil(ex.RetryAfter.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	httpx.JSON(w, http.StatusTooManyRequests, map[string]any{"error": "too_many_requests", "message": fmt.Sprintf("%s Coba lagi dalam %d detik.", msg, secs), "retry_after": secs})
}

// BatchEntry: satu "tandai diambil" dari antrean perangkat.
type BatchEntry struct {
	ClientOpID string    `json:"client_op_id"`
	Token      string    `json:"token,omitempty"`
	TicketID   string    `json:"ticket_id,omitempty"`
	ScannedAt  time.Time `json:"scanned_at"` // jam perangkat: disimpan, tidak dipakai memutuskan
	PickedUpBy string    `json:"picked_up_by,omitempty"`
}

type BatchResult struct {
	ClientOpID string `json:"client_op_id"`
	ScanResponse
}

// Batch: POST /v1/org/{org}/events/{eventID}/scan/batch {"device_id","entries":[…]} — owner, admin, rpc_staff.
// Idempoten per client_op_id; diproses berurutan, tiap entri dalam transaksinya sendiri dengan UPDATE
// atomik yang sama dengan scan online (yang pertama diproses server menang).
func (h *Handler) Batch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	staffID, _ := auth.UserID(ctx)
	eventID, ok := h.eventFromURL(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, batchBodyMax)
	var in struct {
		DeviceID string       `json:"device_id"`
		Entries  []BatchEntry `json:"entries"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	deviceID, err := uuid.Parse(in.DeviceID)
	f := httpx.Fields{}
	if err != nil {
		f["device_id"] = "ID perangkat tidak valid"
	}
	if len(in.Entries) == 0 || len(in.Entries) > batchMax {
		f["entries"] = fmt.Sprintf("Kirim 1–%d entri per permintaan", batchMax)
	}
	if len(f) > 0 {
		httpx.Validation(w, f)
		return
	}
	results := make([]BatchResult, 0, len(in.Entries))
	for _, e := range in.Entries {
		res, err := h.syncOne(ctx, m.OrgID, eventID, staffID, deviceID, e)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		results = append(results, res)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"server_time": time.Now(), "results": results})
}

func (h *Handler) syncOne(ctx context.Context, orgID, eventID, staffID, deviceID uuid.UUID, e BatchEntry) (BatchResult, error) {
	opID, err := uuid.Parse(e.ClientOpID)
	if err != nil {
		return BatchResult{ClientOpID: e.ClientOpID, ScanResponse: ScanResponse{Result: "invalid", Message: "client_op_id tidak valid"}}, nil
	}
	if res, found, err := h.replay(ctx, opID, orgID, eventID); err != nil || found {
		return res, err
	}
	var scannedAt *time.Time
	if !e.ScannedAt.IsZero() {
		scannedAt = &e.ScannedAt
	}
	var res BatchResult
	err = pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		resp, ticketID := scan(ctx, tx, h.signer, orgID, eventID, staffID, ScanRequest{Token: e.Token, TicketID: e.TicketID, PickedUpBy: e.PickedUpBy})
		if resp.Result == "error" {
			return errors.New(resp.Message)
		}
		res = BatchResult{ClientOpID: opID.String(), ScanResponse: resp}
		_, err := tx.Exec(ctx, `
			INSERT INTO scan_logs (org_id, event_id, ticket_id, staff_id, result, client_op_id, device_id, scanned_at_device, source)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'offline_sync')`, orgID, eventID, ticketID, staffID, resp.Result, opID, deviceID, scannedAt)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// Entri yang sama sedang/baru diproses permintaan lain: transaksi ini batal, pakai hasil yang tersimpan.
		var found bool
		if res, found, err = h.replay(ctx, opID, orgID, eventID); err == nil && !found {
			res = BatchResult{ClientOpID: opID.String(), ScanResponse: ScanResponse{Result: "invalid", Message: "client_op_id sudah dipakai"}}
		}
	}
	return res, err
}

// replay: hasil entri yang sudah pernah diproses (pengiriman ulang tidak berefek ganda).
func (h *Handler) replay(ctx context.Context, opID, orgID, eventID uuid.UUID) (BatchResult, bool, error) {
	var result string
	var ticketID *uuid.UUID
	err := h.db.QueryRow(ctx, `SELECT result, ticket_id FROM scan_logs WHERE client_op_id = $1 AND org_id = $2 AND event_id = $3`, opID, orgID, eventID).Scan(&result, &ticketID)
	if errors.Is(err, pgx.ErrNoRows) {
		return BatchResult{}, false, nil
	}
	if err != nil {
		return BatchResult{}, false, err
	}
	res := BatchResult{ClientOpID: opID.String(), ScanResponse: ScanResponse{Result: result, Message: resultMessage[result]}}
	if ticketID != nil && result != "wrong_event" {
		if t, err := ticketDetail(ctx, h.db, *ticketID, eventID, orgID); err == nil {
			res.Ticket = &t
		}
	}
	return res, true, nil
}

var resultMessage = map[string]string{
	"ok":                "Valid — serahkan race pack",
	"already_collected": "Race pack sudah diambil sebelumnya",
	"invalid":           "QR tidak dikenali atau palsu",
	"wrong_event":       "Tiket ini untuk event lain",
	"not_found":         "Tiket tidak terdaftar di event ini",
	"refunded":          "Tiket berstatus refunded",
	"cancelled":         "Tiket berstatus cancelled",
}
