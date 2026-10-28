package checkout

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PrewarmWindow: stok dimuat ke Redis sejak 30 menit sebelum penjualan dibuka.
const PrewarmWindow = 30 * time.Minute

// SyncCategory memuat stok kategori ke Redis bila belum ada (tidak pernah menimpa).
// Stok = kuota − slot milik order pending/lunas. Dipakai worker dan sebagai cadangan
// bila Redis kehilangan data.
func SyncCategory(ctx context.Context, db *pgxpool.Pool, inv *Inventory, categoryID uuid.UUID) error {
	var available int
	err := db.QueryRow(ctx, `
		SELECT c.quota - COALESCE((
			SELECT sum(o.qty) FROM orders o
			WHERE o.category_id = c.id AND o.status IN ('pending', 'paid')
		), 0)
		FROM ticket_categories c WHERE c.id = $1`, categoryID).Scan(&available)
	if err != nil {
		return err
	}
	_, err = inv.Load(ctx, categoryID.String(), available)
	return err
}

// SyncOpenEvents memuat stok semua kategori dari event yang penjualannya (hampir) dibuka.
func SyncOpenEvents(ctx context.Context, db *pgxpool.Pool, inv *Inventory) (int, error) {
	rows, err := db.Query(ctx, `
		SELECT c.id FROM ticket_categories c
		JOIN events e ON e.id = c.event_id
		WHERE e.status = 'published'
		  AND e.sale_open_at - make_interval(secs => $1) <= now()
		  AND e.sale_close_at > now()`, PrewarmWindow.Seconds())
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := SyncCategory(ctx, db, inv, id); err != nil {
			return 0, err
		}
	}
	return len(ids), rows.Err()
}
