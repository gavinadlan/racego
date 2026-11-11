package checkout

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/metrics"
)

// RestoreUserLimits membangun ulang hitungan slot per pembeli ({cat:<id>}:user:<uid>) dari database setelah
// Redis kehilangan kunci (restart tanpa AOF, SIGKILL, FLUSHALL). Sumber: order lunas dan order pending yang masih
// berlaku (reservasinya belum lewat). Hanya MENAIKKAN (RaiseUserLimit), jadi idempoten dan tidak pernah melonggarkan
// batas. TTL kunci mengikuti aturan reservasi: penutupan penjualan + UserLimitGrace, hanya diperpanjang.
//
// Pilihan: pemulihan di worker (bukan pemeriksaan DB di jalur checkout) supaya jalur panas tetap satu skrip Lua.
// Jendela tersisa: pembelian oleh pembeli yang kuncinya hilang antara kehilangan dan putaran pemulihan berikutnya.
// Dijalankan saat stok kategori dimuat ulang (SyncCategory) dan berkala oleh StockReconciler.
func RestoreUserLimits(ctx context.Context, db *pgxpool.Pool, inv *Inventory, categoryID uuid.UUID) (raised int, err error) {
	rows, err := db.Query(ctx, `
		SELECT o.buyer_id::text, sum(o.qty)::int, e.sale_close_at
		FROM orders o JOIN events e ON e.id = o.event_id
		WHERE o.category_id = $1 AND (o.status = 'paid' OR (o.status = 'pending' AND o.expires_at > now()))
		GROUP BY o.buyer_id, e.sale_close_at`, categoryID)
	if err != nil {
		return 0, err
	}
	type row struct {
		user  string
		qty   int
		close time.Time
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.user, &r.qty, &r.close); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	now := time.Now()
	for _, r := range list {
		ttl := max(r.close.Add(UserLimitGrace).Sub(now), 2*time.Hour)
		ok, err := inv.RaiseUserLimit(ctx, categoryID.String(), r.user, r.qty, ttl)
		if err != nil {
			return raised, err
		}
		if ok {
			raised++
		}
	}
	if raised > 0 {
		metrics.UserLimitsRestored.Add(float64(raised))
		slog.WarnContext(ctx, "batas per pembeli dipulihkan dari database (Redis kehilangan kunci?)", "category_id", categoryID, "pembeli", raised)
	}
	return raised, nil
}
