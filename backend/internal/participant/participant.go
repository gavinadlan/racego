// Package participant: daftar peserta per event untuk mitra (tampilan portal + export CSV).
//
// Minimisasi data: nomor identitas hanya 4 digit terakhir, tanpa data pembayaran. Basis daftar adalah
// tiket (peserta yang tiketnya sudah terbit), jadi status yang tampil = status tiket.
package participant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/csvx"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/partner"
	"github.com/gavinadlan/racego/backend/internal/ratelimit"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

const (
	pageSize        = 50
	downloadsPerMin = 10 // unduhan CSV per pengguna per menit
)

var statuses = []string{"issued", "collected", "refunded", "cancelled"}

type Handler struct {
	db  *pgxpool.Pool
	rdb redis.Cmdable
}

func NewHandler(db *pgxpool.Pool, rdb redis.Cmdable) *Handler { return &Handler{db: db, rdb: rdb} }

// filter dari query string; sudah divalidasi.
type filter struct {
	eventID  uuid.UUID
	category *uuid.UUID
	status   string
	q        string
	attended string // "" | yes | no
}

// where: tiket event ini di org ini (tenant), plus filter. $1 org, $2 event, $3 kategori, $4 status, $5 cari, $6 hadir.
const where = `t.org_id = $1 AND t.event_id = $2
	AND ($3::uuid IS NULL OR t.category_id = $3)
	AND ($4 = '' OR t.status = $4)
	AND ($5 = '' OR p.full_name ILIKE '%' || $5 || '%' OR p.email ILIKE '%' || $5 || '%' OR t.bib_number = $5)
	AND ($6 = '' OR ($6 = 'yes' AND t.attended_at IS NOT NULL) OR ($6 = 'no' AND t.attended_at IS NULL))`

func (f filter) args(orgID uuid.UUID) []any {
	return []any{orgID, f.eventID, f.category, f.status, f.q, f.attended}
}

// parse membaca {eventID} + ?category=&status=&q=. false = respons error sudah ditulis.
func (h *Handler) parse(w http.ResponseWriter, r *http.Request) (filter, bool) {
	ctx := r.Context()
	var f filter
	id, err := uuid.Parse(chi.URLParam(r, "eventID"))
	var exists bool
	if err == nil {
		err = h.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE id = $1 AND org_id = $2)`, id, tenant.OrgID(ctx)).Scan(&exists)
		if err != nil {
			httpx.Internal(w, r, err)
			return f, false
		}
	}
	if !exists {
		httpx.Error(w, http.StatusNotFound, "event_not_found", "Event tidak ditemukan")
		return f, false
	}
	f.eventID = id
	q := r.URL.Query()
	fields := httpx.Fields{}
	if c := q.Get("category"); c != "" {
		cid, err := uuid.Parse(c)
		if err != nil {
			fields["category"] = "Kategori tidak valid"
		} else {
			f.category = &cid
		}
	}
	if s := q.Get("status"); s != "" && s != "all" {
		if !contains(statuses, s) {
			fields["status"] = "Status harus issued, collected, refunded, atau cancelled"
		}
		f.status = s
	}
	if a := q.Get("attended"); a != "" && a != "all" {
		if a != "yes" && a != "no" {
			fields["attended"] = "Filter hadir harus yes atau no"
		}
		f.attended = a
	}
	f.q = strings.TrimSpace(q.Get("q"))
	if len(f.q) > 100 {
		fields["q"] = "Pencarian maksimal 100 karakter"
	}
	if len(fields) > 0 {
		httpx.Validation(w, fields)
		return f, false
	}
	return f, true
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ===== Ringkasan (owner, admin, rpc_staff) =====

type count struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
	ID    string `json:"id,omitempty"`
	Count int    `json:"count"`
	// Attended: jumlah yang sudah diabsen hari-H (hanya terisi untuk ringkasan per kategori).
	Attended int `json:"attended,omitempty"`
}

// Summary: GET /v1/org/{org}/events/{eventID}/participants/summary — tanpa data pribadi.
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	f, ok := h.parse(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	orgID := tenant.OrgID(ctx)
	var total, attended int
	if err := h.db.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE attended_at IS NOT NULL AND status IN ('issued', 'collected')) FROM tickets WHERE org_id = $1 AND event_id = $2`, orgID, f.eventID).Scan(&total, &attended); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	// Semua kategori event (juga yang belum punya peserta) agar bisa dipakai sebagai pilihan filter.
	byCat, err := h.counts(ctx, `
		SELECT c.code, c.name, c.id::text, count(t.id)::int, count(t.id) FILTER (WHERE t.attended_at IS NOT NULL AND t.status IN ('issued', 'collected'))::int FROM ticket_categories c
		LEFT JOIN tickets t ON t.category_id = c.id AND t.org_id = $1
		WHERE c.event_id = $2 AND c.org_id = $1 GROUP BY c.id ORDER BY c.distance_km, c.code`, orgID, f.eventID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	// Ukuran jersey & status: hanya tiket yang berlaku (refund/batal tidak perlu disiapkan).
	byJersey, err := h.counts(ctx, `
		SELECT p.jersey_size, '', '', count(*)::int, 0 FROM tickets t JOIN participants p ON p.id = t.participant_id
		WHERE t.org_id = $1 AND t.event_id = $2 AND t.status IN ('issued', 'collected')
		GROUP BY p.jersey_size ORDER BY array_position(ARRAY['XS','S','M','L','XL','XXL','XXXL'], p.jersey_size), p.jersey_size`, orgID, f.eventID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	byStatus, err := h.counts(ctx, `
		SELECT status, '', '', count(*)::int, 0 FROM tickets WHERE org_id = $1 AND event_id = $2
		GROUP BY status ORDER BY array_position(ARRAY['issued','collected','refunded','cancelled'], status)`, orgID, f.eventID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"total": total, "attended": attended, "by_category": byCat, "by_jersey": byJersey, "by_status": byStatus})
}

func (h *Handler) counts(ctx context.Context, sql string, args ...any) ([]count, error) {
	rows, err := h.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[count])
}

// ===== Daftar & CSV (owner, admin) =====

type Participant struct {
	TicketID       uuid.UUID  `json:"ticket_id"`
	Bib            string     `json:"bib"`
	Name           string     `json:"name"`
	Email          string     `json:"email"`
	CategoryCode   string     `json:"category"`
	JerseySize     string     `json:"jersey_size"`
	Gender         *string    `json:"gender"`
	BirthDate      *string    `json:"birth_date"`
	IDNumber       string     `json:"id_number"` // tersamar: ••••1234
	EmergencyName  string     `json:"emergency_name"`
	EmergencyPhone string     `json:"emergency_phone"`
	Status         string     `json:"status"`
	IssuedAt       time.Time  `json:"issued_at"`
	CollectedAt    *time.Time `json:"collected_at"`
	AttendedAt     *time.Time `json:"attended_at"`
}

// Nomor identitas tidak pernah keluar dari DB utuh: hanya 4 digit terakhir yang dipilih.
const selectCols = `t.id, t.bib_number, p.full_name, p.email, c.code, p.jersey_size, p.gender, p.birth_date::text,
	right(p.id_number, 4), p.emergency_name, p.emergency_phone, t.status, t.created_at, t.collected_at, t.attended_at
	FROM tickets t JOIN participants p ON p.id = t.participant_id JOIN ticket_categories c ON c.id = t.category_id`

func scan(row pgx.Row, p *Participant) error {
	var last4 string
	if err := row.Scan(&p.TicketID, &p.Bib, &p.Name, &p.Email, &p.CategoryCode, &p.JerseySize, &p.Gender, &p.BirthDate,
		&last4, &p.EmergencyName, &p.EmergencyPhone, &p.Status, &p.IssuedAt, &p.CollectedAt, &p.AttendedAt); err != nil {
		return err
	}
	p.IDNumber = Mask(last4)
	return nil
}

// Mask: "1234" → "••••1234"; kosong tetap kosong.
func Mask(last4 string) string {
	if last4 == "" {
		return ""
	}
	return "••••" + last4
}

// List: GET /v1/org/{org}/events/{eventID}/participants?q=&category=&status=&page=
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	f, ok := h.parse(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(1, min(page, math.MaxInt32/pageSize))
	args := f.args(tenant.OrgID(ctx))
	items := []Participant{}
	rows, err := h.db.Query(ctx, `SELECT `+selectCols+` WHERE `+where+` ORDER BY c.distance_km, c.code, length(t.bib_number), t.bib_number LIMIT $7 OFFSET $8`,
		append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var p Participant
		if err := scan(rows, &p); err != nil {
			httpx.Internal(w, r, err)
			return
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var total int
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM tickets t JOIN participants p ON p.id = t.participant_id WHERE `+where, args...).Scan(&total); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize})
}

var statusLabel = map[string]string{"issued": "Diterbitkan", "collected": "Diambil", "refunded": "Refund", "cancelled": "Dibatalkan"}

// CSV: GET /v1/org/{org}/events/{eventID}/participants.csv?category=&status=&q=
// Dibatasi per pengguna, tercatat di audit (filter + jumlah baris, tanpa isi), dan di-stream per baris
// dari kursor database: memori tetap kecil walau peserta puluhan ribu.
func (h *Handler) CSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, _ := auth.UserID(ctx)
	f, ok := h.parse(w, r)
	if !ok {
		return
	}
	if err := ratelimit.Allow(ctx, h.rdb, "participants_csv:"+userID.String(), downloadsPerMin, time.Minute); err != nil {
		var ex ratelimit.Exceeded
		if !errors.As(err, &ex) {
			httpx.Internal(w, r, err)
			return
		}
		secs := int(math.Ceil(ex.RetryAfter.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		httpx.JSON(w, http.StatusTooManyRequests, map[string]any{"error": "too_many_requests",
			"message": fmt.Sprintf("Terlalu sering mengunduh. Coba lagi dalam %d detik.", secs), "retry_after": secs})
		return
	}
	m, _ := tenant.FromContext(ctx)
	args := f.args(m.OrgID)

	var eventName string
	var total int
	if err := h.db.QueryRow(ctx, `SELECT name FROM events WHERE id = $1`, f.eventID).Scan(&eventName); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM tickets t JOIN participants p ON p.id = t.participant_id WHERE `+where, args...).Scan(&total); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	// Audit dulu: unduhan yang tidak tercatat tidak boleh terjadi.
	filters := map[string]any{"category": f.category, "status": f.status, "q_len": len(f.q), "attended": f.attended}
	if _, err := h.db.Exec(ctx, `INSERT INTO audit_logs (actor_id, org_id, action, entity, entity_id, after) VALUES ($1, $2, 'participants.download', 'event', $3, $4)`,
		userID, m.OrgID, f.eventID.String(), map[string]any{"filters": filters, "rows": total, "ip": auth.ClientIP(r)}); err != nil {
		httpx.Internal(w, r, err)
		return
	}

	rows, err := h.db.Query(ctx, `SELECT `+selectCols+` WHERE `+where+` ORDER BY c.distance_km, c.code, length(t.bib_number), t.bib_number`, args...)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	defer rows.Close()

	name := partner.Slugify(eventName)
	if name == "" {
		name = "event"
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/csv; charset=utf-8")
	hd.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="peserta-%s-%s.csv"`, name, time.Now().Format("20060102")))
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	cw, err := csvx.New(w)
	if err == nil {
		err = cw.Header("BIB", "Nama", "Email", "Kategori", "Ukuran jersey", "Gender", "Tanggal lahir", "Nomor identitas",
			"Kontak darurat", "Telepon darurat", "Status tiket", "Diterbitkan (WIB)", "Diambil (WIB)", "Hadir hari-H (WIB)")
	}
	wib := time.FixedZone("WIB", 7*3600)
	flusher, _ := w.(http.Flusher)
	n := 0
	for err == nil && rows.Next() {
		var p Participant
		if err = scan(rows, &p); err != nil {
			break
		}
		collected := ""
		if p.CollectedAt != nil {
			collected = p.CollectedAt.In(wib).Format("2006-01-02 15:04")
		}
		attended := ""
		if p.AttendedAt != nil {
			attended = p.AttendedAt.In(wib).Format("2006-01-02 15:04")
		}
		err = cw.Row(p.Bib, p.Name, p.Email, p.CategoryCode, p.JerseySize, deref(p.Gender), deref(p.BirthDate), p.IDNumber,
			p.EmergencyName, p.EmergencyPhone, statusLabel[p.Status], p.IssuedAt.In(wib).Format("2006-01-02 15:04"), collected, attended)
		if n++; err == nil && n%500 == 0 {
			if err = cw.Flush(); err == nil && flusher != nil {
				flusher.Flush()
			}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	if err == nil {
		err = cw.Flush()
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		// Header sudah terkirim: hanya bisa dicatat; klien menerima berkas terpotong.
		slog.ErrorContext(ctx, "stream CSV peserta", "event_id", f.eventID, "err", err)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
