package partner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/checkout"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

// OrgCategory adalah kategori tiket seperti dilihat mitra (dengan angka penjualan).
type OrgCategory struct {
	ID         uuid.UUID `json:"id"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	DistanceKm float64   `json:"distance_km"`
	Price      int64     `json:"price"`
	Quota      int       `json:"quota"`
	MaxPerUser int       `json:"max_per_user"`
	BibPrefix  string    `json:"bib_prefix"`
	Sold       int       `json:"sold"`       // slot lunas
	Pending    int       `json:"pending"`    // slot order yang menunggu pembayaran
	Locked     bool      `json:"locked"`     // sudah ada order aktif: kode, harga, jarak, prefix BIB terkunci
	HasOrders  bool      `json:"has_orders"` // ada order apa pun (termasuk kedaluwarsa): tidak bisa dihapus
	Remaining  *int      `json:"remaining"`  // stok di Redis; null = belum dimuat
}

// EventDetail adalah isi form edit event.
type EventDetail struct {
	ID           uuid.UUID     `json:"id"`
	Slug         string        `json:"slug"`
	Status       string        `json:"status"` // draft | published | closed
	Name         string        `json:"name"`
	Tagline      string        `json:"tagline"`
	Description  string        `json:"description"`
	Venue        string        `json:"venue"`
	City         string        `json:"city"`
	RaceDate     string        `json:"race_date"`
	StartTime    string        `json:"start_time"`
	SaleOpenAt   time.Time     `json:"sale_open_at"`
	SaleCloseAt  time.Time     `json:"sale_close_at"`
	RacepackInfo string        `json:"racepack_info"`
	ThemeColor   *string       `json:"theme_color"`
	CoverURL     *string       `json:"cover_url"`
	WaitingRoom  bool          `json:"waiting_room"`
	HasOrders    bool          `json:"has_orders"`
	Categories   []OrgCategory `json:"categories"`
	Org          struct {
		Name       string `json:"name"`
		KYCStatus  string `json:"kyc_status"`
		ThemeColor string `json:"theme_color"`
	} `json:"org"`
}

func (d EventDetail) input() EventInput {
	in := EventInput{
		Name: d.Name, Tagline: d.Tagline, Description: d.Description, Venue: d.Venue, City: d.City,
		RaceDate: d.RaceDate, StartTime: d.StartTime,
		SaleOpenAt: d.SaleOpenAt.Format(time.RFC3339), SaleCloseAt: d.SaleCloseAt.Format(time.RFC3339),
		RacepackInfo: d.RacepackInfo, WaitingRoom: &d.WaitingRoom,
	}
	if d.ThemeColor != nil {
		in.ThemeColor = *d.ThemeColor
	}
	if d.CoverURL != nil {
		in.CoverURL = *d.CoverURL
	}
	return in
}

var errEventNotFound = errors.New("event tidak ditemukan")

func eventNotFound(w http.ResponseWriter) {
	httpx.Error(w, http.StatusNotFound, "event_not_found", "Event tidak ditemukan")
}

// loadEvent membaca event milik org ini saja. lock=true mengunci baris (SELECT … FOR UPDATE)
// agar dua perubahan bersamaan pada event yang sama berjalan bergantian.
func (h *Handler) loadEvent(ctx context.Context, q querier, id uuid.UUID, lock bool) (EventDetail, error) {
	var d EventDetail
	sql := `
		SELECT e.id, e.slug, e.status, e.name, e.tagline, e.description, e.venue, e.city,
		       to_char(e.race_date, 'YYYY-MM-DD'), e.start_time, e.sale_open_at, e.sale_close_at,
		       e.racepack_info, e.theme_color, e.cover_url, e.waiting_room,
		       EXISTS (SELECT 1 FROM orders o WHERE o.event_id = e.id),
		       o.name, o.kyc_status, o.theme_color
		FROM events e JOIN organizations o ON o.id = e.org_id
		WHERE e.id = $1 AND e.org_id = $2`
	if lock {
		sql += ` FOR UPDATE OF e`
	}
	err := q.QueryRow(ctx, sql, id, tenant.OrgID(ctx)).Scan(&d.ID, &d.Slug, &d.Status, &d.Name, &d.Tagline, &d.Description,
		&d.Venue, &d.City, &d.RaceDate, &d.StartTime, &d.SaleOpenAt, &d.SaleCloseAt, &d.RacepackInfo, &d.ThemeColor,
		&d.CoverURL, &d.WaitingRoom, &d.HasOrders, &d.Org.Name, &d.Org.KYCStatus, &d.Org.ThemeColor)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, errEventNotFound
	}
	return d, err
}

type rowsQuerier interface {
	querier
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

const categorySelect = `
	SELECT c.id, c.code, c.name, c.distance_km::float8, c.price, c.quota, c.max_per_user, c.bib_prefix,
	       COALESCE((SELECT sum(o.qty) FROM orders o WHERE o.category_id = c.id AND o.status = 'paid'), 0),
	       COALESCE((SELECT sum(o.qty) FROM orders o WHERE o.category_id = c.id AND o.status = 'pending'), 0),
	       EXISTS (SELECT 1 FROM orders o WHERE o.category_id = c.id AND o.status NOT IN ('cancelled', 'expired')),
	       EXISTS (SELECT 1 FROM orders o WHERE o.category_id = c.id)
	FROM ticket_categories c`

func scanCategory(row pgx.Row) (OrgCategory, error) {
	var c OrgCategory
	err := row.Scan(&c.ID, &c.Code, &c.Name, &c.DistanceKm, &c.Price, &c.Quota, &c.MaxPerUser, &c.BibPrefix,
		&c.Sold, &c.Pending, &c.Locked, &c.HasOrders)
	return c, err
}

func (h *Handler) loadCategories(ctx context.Context, q rowsQuerier, eventID uuid.UUID) ([]OrgCategory, error) {
	rows, err := q.Query(ctx, categorySelect+` WHERE c.event_id = $1 AND c.org_id = $2 ORDER BY c.distance_km, c.code`,
		eventID, tenant.OrgID(ctx))
	if err != nil {
		return nil, err
	}
	cats, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (OrgCategory, error) { return scanCategory(row) })
	if err != nil {
		return nil, err
	}
	for i := range cats {
		if left, ok, err := h.inv.Available(ctx, cats[i].ID.String()); err == nil && ok {
			cats[i].Remaining = &left
		}
	}
	return cats, nil
}

func (h *Handler) fullEvent(ctx context.Context, id uuid.UUID) (EventDetail, error) {
	d, err := h.loadEvent(ctx, h.db, id, false)
	if err != nil {
		return d, err
	}
	d.Categories, err = h.loadCategories(ctx, h.db, id)
	return d, err
}

func eventID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "eventID"))
	return id, err == nil
}

// writeEvent membalas detail event terbaru, atau error yang sesuai.
func (h *Handler) writeEvent(w http.ResponseWriter, r *http.Request, status int, id uuid.UUID) {
	d, err := h.fullEvent(r.Context(), id)
	if errors.Is(err, errEventNotFound) {
		eventNotFound(w)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, status, d)
}

// GetEvent: GET /v1/org/{org}/events/{eventID} — semua anggota; termasuk draft.
func (h *Handler) GetEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := eventID(r)
	if !ok {
		eventNotFound(w)
		return
	}
	h.writeEvent(w, r, http.StatusOK, id)
}

// statusError: aksi tidak cocok dengan status event sekarang.
type statusError struct{ code, msg string }

func (e statusError) Error() string { return e.msg }

// validationError membawa pesan per kolom keluar dari transaksi.
type validationError struct{ fields httpx.Fields }

func (e validationError) Error() string { return "validasi gagal" }

// finish menerjemahkan error dari transaksi menjadi respons HTTP; true bila sudah membalas.
func finish(w http.ResponseWriter, r *http.Request, err error) bool {
	var se statusError
	var ve validationError
	switch {
	case err == nil:
		return false
	case errors.Is(err, errEventNotFound):
		eventNotFound(w)
	case errors.Is(err, errCategoryNotFound):
		httpx.Error(w, http.StatusNotFound, "category_not_found", "Kategori tidak ditemukan")
	case errors.As(err, &se):
		httpx.Error(w, http.StatusConflict, se.code, se.msg)
	case errors.As(err, &ve):
		httpx.Validation(w, ve.fields)
	default:
		if f := categoryConstraintFields(err, ""); f != nil {
			httpx.Validation(w, f)
		} else {
			httpx.Internal(w, r, err)
		}
	}
	return true
}

// categoryConstraintFields mengubah pelanggaran UNIQUE kategori menjadi pesan kolom.
func categoryConstraintFields(err error, prefix string) httpx.Fields {
	switch uniqueViolation(err) {
	case "ticket_categories_event_id_code_key":
		return httpx.Fields{prefix + "code": "Kode ini sudah dipakai kategori lain di event ini"}
	case "ticket_categories_bib_prefix_uniq":
		return httpx.Fields{prefix + "bib_prefix": "Prefix BIB ini sudah dipakai kategori lain di event ini"}
	}
	return nil
}

func takenEventSlugs(q querier, orgID uuid.UUID) func(ctx context.Context, base string) ([]string, error) {
	return func(ctx context.Context, base string) ([]string, error) {
		var out []string
		err := q.QueryRow(ctx, `SELECT COALESCE(array_agg(slug), '{}') FROM events WHERE org_id = $1 AND (slug = $2 OR slug LIKE $3)`,
			orgID, base, base+"-%").Scan(&out)
		return out, err
	}
}

func insertCategory(ctx context.Context, tx pgx.Tx, eventID, orgID uuid.UUID, c CategoryInput) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO ticket_categories (event_id, org_id, code, name, distance_km, price, quota, max_per_user, bib_prefix)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		eventID, orgID, c.Code, c.Name, c.DistanceKm, c.Price, c.Quota, c.MaxPerUser, c.BibPrefix).Scan(&id)
	return id, err
}

// CreateEvent: POST /v1/org/{org}/events — draft baru + kategori awal (opsional) dalam satu transaksi.
func (h *Handler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		EventInput
		Categories []CategoryInput `json:"categories"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	in.trim()
	f := in.validate(h.now(), true)
	for i := range in.Categories {
		in.Categories[i].trim()
		in.Categories[i].validate(fmt.Sprintf("categories.%d.", i), f)
	}
	checkDuplicates(in.Categories, f)
	if len(f) > 0 {
		httpx.Validation(w, f)
		return
	}
	orgID := tenant.OrgID(ctx)
	var drafts int
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM events WHERE org_id = $1 AND status = 'draft'`, orgID).Scan(&drafts); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if drafts >= maxDraftsPerOrg {
		httpx.Error(w, http.StatusUnprocessableEntity, "too_many_drafts",
			fmt.Sprintf("Maksimal %d event draft per penyelenggara. Terbitkan atau hapus draft lama dulu.", maxDraftsPerOrg))
		return
	}
	base := Slugify(in.Name)
	if base == "" {
		base = "event"
	}
	var id uuid.UUID
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		slug, err := uniqueSlug(ctx, base, nil, takenEventSlugs(tx, orgID))
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO events (org_id, slug, name, tagline, description, venue, city, race_date, start_time,
			                    sale_open_at, sale_close_at, racepack_info, theme_color, cover_url, waiting_room, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 'draft') RETURNING id`,
			orgID, slug, in.Name, in.Tagline, in.Description, in.Venue, in.City, in.RaceDate, in.StartTime,
			in.SaleOpenAt, in.SaleCloseAt, in.RacepackInfo, nullIfEmpty(strings.ToUpper(in.ThemeColor)), nullIfEmpty(in.CoverURL),
			in.WaitingRoom != nil && *in.WaitingRoom,
		).Scan(&id); err != nil {
			return err
		}
		for _, c := range in.Categories {
			if _, err := insertCategory(ctx, tx, id, orgID, c); err != nil {
				return err
			}
		}
		return audit(ctx, tx, orgID, "event.created", "event", id.String(), nil, in)
	})
	if finish(w, r, err) {
		return
	}
	h.writeEvent(w, r, http.StatusCreated, id)
}

// UpdateEvent: PATCH /v1/org/{org}/events/{eventID} — owner & admin. Body berisi semua kolom form.
// Tanggal race hanya wajib di masa depan bila diubah (event lama tetap bisa diedit teksnya).
func (h *Handler) UpdateEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := eventID(r)
	if !ok {
		eventNotFound(w)
		return
	}
	var in EventInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	in.trim()
	orgID := tenant.OrgID(ctx)
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		cur, err := h.loadEvent(ctx, tx, id, true)
		if err != nil {
			return err
		}
		f := in.validate(h.now(), in.RaceDate != cur.RaceDate)
		if in.WaitingRoom == nil {
			in.WaitingRoom = &cur.WaitingRoom
		} else if *in.WaitingRoom != cur.WaitingRoom && cur.Status != "draft" && !h.now().Before(cur.SaleOpenAt) {
			// Mengubah antrian di tengah war tiket mengubah aturan main bagi pembeli yang sudah menunggu.
			f["waiting_room"] = "Antrian virtual tidak bisa diubah setelah penjualan dibuka"
		}
		if len(f) > 0 {
			return validationError{f}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE events SET name = $3, tagline = $4, description = $5, venue = $6, city = $7, race_date = $8,
			       start_time = $9, sale_open_at = $10, sale_close_at = $11, racepack_info = $12, theme_color = $13, cover_url = $14,
			       waiting_room = $15
			WHERE id = $1 AND org_id = $2`,
			id, orgID, in.Name, in.Tagline, in.Description, in.Venue, in.City, in.RaceDate, in.StartTime,
			in.SaleOpenAt, in.SaleCloseAt, in.RacepackInfo, nullIfEmpty(strings.ToUpper(in.ThemeColor)), nullIfEmpty(in.CoverURL),
			*in.WaitingRoom); err != nil {
			return err
		}
		return audit(ctx, tx, orgID, "event.updated", "event", id.String(), cur.input(), in)
	})
	if finish(w, r, err) {
		return
	}
	h.writeEvent(w, r, http.StatusOK, id)
}

// transition menjalankan perubahan status event di dalam transaksi dengan baris event terkunci.
func (h *Handler) transition(w http.ResponseWriter, r *http.Request, action string, fn func(ctx context.Context, tx pgx.Tx, cur EventDetail) error) (uuid.UUID, bool) {
	ctx := r.Context()
	id, ok := eventID(r)
	if !ok {
		eventNotFound(w)
		return id, false
	}
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		cur, err := h.loadEvent(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if err := fn(ctx, tx, cur); err != nil {
			return err
		}
		return audit(ctx, tx, tenant.OrgID(ctx), action, "event", id.String(), map[string]string{"status": cur.Status}, nil)
	})
	return id, !finish(w, r, err)
}

func setStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error {
	_, err := tx.Exec(ctx, `UPDATE events SET status = $3 WHERE id = $1 AND org_id = $2`, id, tenant.OrgID(ctx), status)
	return err
}

// Publish: POST /v1/org/{org}/events/{eventID}/publish — owner & admin.
// Syarat: org terverifikasi, minimal 1 kategori, semua kolom valid, penjualan belum lewat.
// Setelah terbit stok semua kategori langsung dimuat ke Redis (tidak menunggu worker).
func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	var catIDs []uuid.UUID
	id, ok := h.transition(w, r, "event.published", func(ctx context.Context, tx pgx.Tx, cur EventDetail) error {
		if cur.Status != "draft" {
			return statusError{"invalid_status", "Hanya event draft yang bisa diterbitkan"}
		}
		if cur.Org.KYCStatus != "verified" {
			return statusError{"org_not_verified", "Penyelenggara belum terverifikasi, jadi event belum bisa diterbitkan. Draft tetap tersimpan."}
		}
		cats, err := h.loadCategories(ctx, tx, cur.ID)
		if err != nil {
			return err
		}
		now := h.now()
		f := cur.input().validate(now, true)
		if !cur.SaleCloseAt.After(now) {
			f["sale_close_at"] = "Waktu tutup penjualan sudah lewat"
		}
		if len(cats) == 0 {
			f["categories"] = "Tambahkan minimal satu kategori tiket"
		}
		for i, c := range cats {
			CategoryInput{Code: c.Code, Name: c.Name, DistanceKm: c.DistanceKm, Price: c.Price, Quota: c.Quota,
				MaxPerUser: c.MaxPerUser, BibPrefix: c.BibPrefix}.validate(fmt.Sprintf("categories.%d.", i), f)
			catIDs = append(catIDs, c.ID)
		}
		if len(f) > 0 {
			return validationError{f}
		}
		return setStatus(ctx, tx, cur.ID, "published")
	})
	if !ok {
		return
	}
	h.loadStock(r.Context(), catIDs)
	h.writeEvent(w, r, http.StatusOK, id)
}

// loadStock memuat stok kategori ke Redis (SETNX: stok yang sudah ada tidak ditimpa).
// Gagal di sini tidak membatalkan penerbitan: worker menyinkronkan ulang tiap 30 detik dan
// reservasi pertama juga memuat stok bila belum ada.
func (h *Handler) loadStock(ctx context.Context, ids []uuid.UUID) {
	for _, id := range ids {
		if err := checkout.SyncCategory(ctx, h.db, h.inv, id); err != nil {
			slog.ErrorContext(ctx, "muat stok kategori", "category_id", id, "err", err)
		}
	}
}

// Unpublish: POST /v1/org/{org}/events/{eventID}/unpublish — kembali ke draft, hanya bila belum ada order.
func (h *Handler) Unpublish(w http.ResponseWriter, r *http.Request) {
	id, ok := h.transition(w, r, "event.unpublished", func(ctx context.Context, tx pgx.Tx, cur EventDetail) error {
		if cur.Status != "published" {
			return statusError{"invalid_status", "Hanya event yang sudah terbit yang bisa dikembalikan ke draft"}
		}
		if cur.HasOrders {
			return statusError{"has_orders", "Event sudah punya order, jadi tidak bisa dikembalikan ke draft. Gunakan Tutup penjualan."}
		}
		return setStatus(ctx, tx, cur.ID, "draft")
	})
	if ok {
		h.writeEvent(w, r, http.StatusOK, id)
	}
}

// Close: POST /v1/org/{org}/events/{eventID}/close — penjualan berhenti permanen.
// Event tetap tampil publik (arsip & pemegang tiket), tapi reservasi/order ditolak.
func (h *Handler) Close(w http.ResponseWriter, r *http.Request) {
	id, ok := h.transition(w, r, "event.closed", func(ctx context.Context, tx pgx.Tx, cur EventDetail) error {
		if cur.Status != "published" {
			return statusError{"invalid_status", "Hanya event yang sudah terbit yang bisa ditutup"}
		}
		return setStatus(ctx, tx, cur.ID, "closed")
	})
	if ok {
		h.writeEvent(w, r, http.StatusOK, id)
	}
}

// DeleteEvent: DELETE /v1/org/{org}/events/{eventID} — owner; hanya draft tanpa order.
func (h *Handler) DeleteEvent(w http.ResponseWriter, r *http.Request) {
	var catIDs []uuid.UUID
	_, ok := h.transition(w, r, "event.deleted", func(ctx context.Context, tx pgx.Tx, cur EventDetail) error {
		if cur.Status != "draft" {
			return statusError{"invalid_status", "Hanya event draft yang bisa dihapus"}
		}
		if cur.HasOrders {
			return statusError{"has_orders", "Event sudah punya order, jadi tidak bisa dihapus"}
		}
		rows, err := tx.Query(ctx, `DELETE FROM ticket_categories WHERE event_id = $1 AND org_id = $2 RETURNING id`, cur.ID, tenant.OrgID(ctx))
		if err != nil {
			return err
		}
		if catIDs, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM events WHERE id = $1 AND org_id = $2`, cur.ID, tenant.OrgID(ctx))
		return err
	})
	if !ok {
		return
	}
	for _, id := range catIDs {
		if err := h.inv.Drop(r.Context(), id.String()); err != nil {
			slog.ErrorContext(r.Context(), "hapus stok kategori", "category_id", id, "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
