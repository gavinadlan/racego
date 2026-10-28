package partner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/checkout"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

var errCategoryNotFound = errors.New("kategori tidak ditemukan")

const lockedMsg = "Terkunci karena sudah ada penjualan"

func (h *Handler) loadCategory(ctx context.Context, q querier, eventID, catID uuid.UUID) (OrgCategory, error) {
	c, err := scanCategory(q.QueryRow(ctx, categorySelect+` WHERE c.id = $1 AND c.event_id = $2 AND c.org_id = $3`,
		catID, eventID, tenant.OrgID(ctx)))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, errCategoryNotFound
	}
	return c, err
}

func categoryID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "categoryID"))
	return id, err == nil
}

// CreateCategory: POST /v1/org/{org}/events/{eventID}/categories — owner & admin.
//
// Draft: selalu boleh. Sudah terbit: hanya selama penjualan belum dibuka, supaya daftar
// kategori tidak berubah di tengah penjualan (pembeli yang sedang memilih tidak dikejutkan,
// dan laporan penjualan per kategori tetap utuh). Ditutup: tidak boleh.
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	evID, ok := eventID(r)
	if !ok {
		eventNotFound(w)
		return
	}
	var in CategoryInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	in.trim()
	var newID uuid.UUID
	var published bool
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		cur, err := h.loadEvent(ctx, tx, evID, true)
		if err != nil {
			return err
		}
		switch {
		case cur.Status == "closed":
			return statusError{"invalid_status", "Penjualan event ini sudah ditutup"}
		case cur.Status == "published" && !h.now().Before(cur.SaleOpenAt):
			return statusError{"sale_started", "Kategori baru hanya bisa ditambah sebelum penjualan dibuka"}
		}
		published = cur.Status == "published"
		f := httpx.Fields{}
		in.validate("", f)
		if len(f) > 0 {
			return validationError{f}
		}
		if newID, err = insertCategory(ctx, tx, evID, tenant.OrgID(ctx), in); err != nil {
			return err
		}
		return audit(ctx, tx, tenant.OrgID(ctx), "category.created", "category", newID.String(), nil, in)
	})
	if finish(w, r, err) {
		return
	}
	if published {
		h.loadStock(ctx, []uuid.UUID{newID})
	}
	h.writeEvent(w, r, http.StatusCreated, evID)
}

// UpdateCategory: PATCH /v1/org/{org}/events/{eventID}/categories/{categoryID} — owner & admin.
//
// Setelah ada order aktif: kode, harga, jarak, dan prefix BIB terkunci. Kuota boleh naik, atau
// turun selama tidak di bawah slot terpakai. Stok Redis disesuaikan dengan SELISIH kuota lewat
// skrip Lua atomik (checkout.Inventory.AdjustStock), bukan ditimpa angka absolut: pembelian bisa
// sedang berjalan, dan angka absolut yang dihitung sedetik sebelumnya sudah basi.
func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	evID, ok1 := eventID(r)
	catID, ok2 := categoryID(r)
	if !ok1 || !ok2 {
		eventNotFound(w)
		return
	}
	var in CategoryInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	in.trim()
	var adjusted int // selisih yang sudah diterapkan ke Redis; dibatalkan bila commit gagal
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		// Mengunci baris event membuat semua perubahan kategori di event ini berjalan bergantian.
		if _, err := h.loadEvent(ctx, tx, evID, true); err != nil {
			return err
		}
		cur, err := h.loadCategory(ctx, tx, evID, catID)
		if err != nil {
			return err
		}
		f := httpx.Fields{}
		in.validate("", f)
		if cur.Locked {
			if in.Code != cur.Code {
				f["code"] = lockedMsg
			}
			if in.Price != cur.Price {
				f["price"] = lockedMsg
			}
			if in.BibPrefix != cur.BibPrefix {
				f["bib_prefix"] = lockedMsg
			}
			if in.DistanceKm != cur.DistanceKm {
				f["distance_km"] = lockedMsg
			}
		}
		if used := cur.Sold + cur.Pending; in.Quota < used && f["quota"] == "" {
			f["quota"] = fmt.Sprintf("Kuota minimal %d (sudah terjual atau menunggu pembayaran)", used)
		}
		if len(f) > 0 {
			return validationError{f}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE ticket_categories SET code = $4, name = $5, distance_km = $6, price = $7, quota = $8,
			       max_per_user = $9, bib_prefix = $10
			WHERE id = $1 AND event_id = $2 AND org_id = $3`,
			catID, evID, tenant.OrgID(ctx), in.Code, in.Name, in.DistanceKm, in.Price, in.Quota, in.MaxPerUser, in.BibPrefix); err != nil {
			return err
		}
		if err := audit(ctx, tx, tenant.OrgID(ctx), "category.updated", "category", catID.String(), cur, in); err != nil {
			return err
		}
		// Langkah terakhir sebelum commit: sesuaikan stok Redis. Skrip memeriksa "stok + selisih >= 0"
		// secara atomik, termasuk slot yang sedang ditahan pembeli tapi belum jadi order.
		if delta := in.Quota - cur.Quota; delta != 0 {
			stock, _, err := h.inv.AdjustStock(ctx, catID.String(), delta)
			if errors.Is(err, checkout.ErrBelowUsed) {
				return validationError{httpx.Fields{"quota": fmt.Sprintf(
					"Kuota minimal %d: slot itu sudah terjual atau sedang ditahan pembeli", cur.Quota-stock)}}
			}
			if err != nil {
				return err
			}
			adjusted = delta
		}
		return nil
	})
	if err != nil && adjusted != 0 {
		if _, _, cErr := h.inv.AdjustStock(context.WithoutCancel(ctx), catID.String(), -adjusted); cErr != nil {
			slog.ErrorContext(ctx, "batalkan penyesuaian stok gagal; jalankan sinkron stok manual", "category_id", catID, "delta", adjusted, "err", cErr)
		}
	}
	if finish(w, r, err) {
		return
	}
	h.writeEvent(w, r, http.StatusOK, evID)
}

// DeleteCategory: DELETE /v1/org/{org}/events/{eventID}/categories/{categoryID} — owner & admin.
// Hanya kategori tanpa order; event terbit harus tetap punya minimal satu kategori.
func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	evID, ok1 := eventID(r)
	catID, ok2 := categoryID(r)
	if !ok1 || !ok2 {
		eventNotFound(w)
		return
	}
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		ev, err := h.loadEvent(ctx, tx, evID, true)
		if err != nil {
			return err
		}
		cur, err := h.loadCategory(ctx, tx, evID, catID)
		if err != nil {
			return err
		}
		if cur.HasOrders {
			return statusError{"has_orders", "Kategori ini sudah punya order, jadi tidak bisa dihapus"}
		}
		if ev.Status != "draft" {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM ticket_categories WHERE event_id = $1`, evID).Scan(&n); err != nil {
				return err
			}
			if n <= 1 {
				return statusError{"last_category", "Event yang sudah terbit harus punya minimal satu kategori"}
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM ticket_categories WHERE id = $1 AND event_id = $2 AND org_id = $3`,
			catID, evID, tenant.OrgID(ctx)); err != nil {
			return err
		}
		return audit(ctx, tx, tenant.OrgID(ctx), "category.deleted", "category", catID.String(), cur, nil)
	})
	if finish(w, r, err) {
		return
	}
	if err := h.inv.Drop(ctx, catID.String()); err != nil {
		slog.ErrorContext(ctx, "hapus stok kategori", "category_id", catID, "err", err)
	}
	h.writeEvent(w, r, http.StatusOK, evID)
}
