package checkout

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/metrics"
)

// StockReconciler menjaga satu invarian: stok di Redis ≤ kuota − slot milik order pending/lunas (database).
// Redis yang crash/di-kill kehilangan ≤1 detik tulisan (AOF everysec): pengurangan stok yang hilang membuat
// stok terlalu longgar, dan itu jalan menuju oversell (ditemukan di uji chaos, docs/11-performa.md).
//
// Hanya menurunkan stok. Kelebihan baru dijepit bila terlihat di dua putaran berturut-turut: perubahan
// kuota menyesuaikan Redis sebelum commit database, jadi kenaikan kuota yang sedang berjalan sempat
// terlihat sebagai kelebihan sesaat (milidetik), sedangkan kerusakan akibat crash menetap.
// ponytail: celah pembeli bisa oversell di antara crash dan putaran kedua (≤2 putaran, 30 detik);
// perkecil dengan appendfsync always di Redis bila itu tidak bisa diterima.
// userLimitEvery: pemulihan batas per pembeli dijalankan tiap N putaran rekonsiliasi.
const userLimitEvery = 8

type StockReconciler struct {
	db      *pgxpool.Pool
	inv     *Inventory
	runs    int
	caps    func(ctx context.Context, ids []string) (map[string]int, error)
	suspect map[string]bool
}

func NewStockReconciler(db *pgxpool.Pool, inv *Inventory) *StockReconciler {
	return &StockReconciler{db: db, inv: inv, suspect: map[string]bool{}, caps: func(ctx context.Context, ids []string) (map[string]int, error) {
		rows, err := db.Query(ctx, `
			SELECT c.id::text, c.quota - COALESCE(sum(o.qty) FILTER (WHERE o.status IN ('pending', 'paid')), 0)
			FROM ticket_categories c LEFT JOIN orders o ON o.category_id = c.id
			WHERE c.id::text = ANY($1) GROUP BY c.id`, ids)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var id string
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				return nil, err
			}
			out[id] = n
		}
		return out, rows.Err()
	}}
}

// Run satu putaran; mengembalikan jumlah slot yang diturunkan.
func (r *StockReconciler) Run(ctx context.Context) (slots int, err error) {
	ids, err := r.inv.ActiveCategories(ctx)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	// Tiap 8 putaran (≈2 menit): pulihkan batas per pembeli yang kuncinya hilang (kehilangan tulisan tanpa muat ulang stok).
	r.runs++
	if r.runs%userLimitEvery == 0 {
		for _, id := range ids {
			if cid, perr := uuid.Parse(id); perr == nil {
				if _, err := RestoreUserLimits(ctx, r.db, r.inv, cid); err != nil {
					slog.WarnContext(ctx, "pulihkan batas per pembeli", "category_id", id, "err", err)
				}
			}
		}
	}
	caps, err := r.caps(ctx, ids)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		cap, known := caps[id]
		stock, loaded, err := r.inv.Available(ctx, id)
		if err != nil {
			return slots, err
		}
		if !known || !loaded || stock <= cap {
			delete(r.suspect, id)
			continue
		}
		if !r.suspect[id] {
			r.suspect[id] = true
			continue
		}
		before, clamped, err := r.inv.ClampStock(ctx, id, cap)
		if err != nil {
			return slots, err
		}
		delete(r.suspect, id)
		if clamped {
			slots += before - max(cap, 0)
			slog.WarnContext(ctx, "stok Redis lebih besar dari kuota − order aktif: dijepit (Redis kehilangan tulisan?)",
				"category_id", id, "stok_redis", before, "batas", cap)
		}
	}
	if slots > 0 {
		metrics.StockClamped.Add(float64(slots))
	}
	return slots, nil
}
