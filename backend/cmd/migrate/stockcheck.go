package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// stockCheck membandingkan per kategori: kuota (database) vs tiket terbit (database) vs stok Redis.
// Keluaran hanya angka dan ID kategori. Gagal (exit 1) bila:
//   - tiket terbit > kuota (pelanggaran keras: pagar database seharusnya mencegahnya), atau
//   - stok Redis + slot ditahan + terjual (order lunas) > kuota (Redis terlalu longgar; StockReconciler akan menjepitnya,
//     tetapi operator perlu tahu).
//
// Kategori yang kuncinya tidak ada di Redis dilaporkan "belum dimuat" dan tidak dianggap gagal.
// Selisih ke arah sebaliknya (Redis lebih ketat dari database) hanya ditampilkan: itu arah aman.
func stockCheck(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, args []string) error {
	event := ""
	if len(args) == 2 && args[0] == "--event" {
		event = args[1]
	} else if len(args) != 0 {
		return errors.New("pemakaian: migrate stock-check [--event <event-id>]")
	}
	rows, err := db.Query(ctx, `
		SELECT c.id::text, c.quota,
		       (SELECT count(*) FROM tickets t WHERE t.category_id = c.id),
		       coalesce((SELECT sum(o.qty) FROM orders o WHERE o.category_id = c.id AND o.status = 'paid'), 0),
		       coalesce((SELECT sum(o.qty) FROM orders o WHERE o.category_id = c.id AND o.status = 'pending'), 0)
		FROM ticket_categories c JOIN events e ON e.id = c.event_id
		WHERE e.status = 'published' AND ($1 = '' OR e.id::text = $1)
		ORDER BY c.id`, event)
	if err != nil {
		return err
	}
	defer rows.Close()
	bad, n := 0, 0
	for rows.Next() {
		var id string
		var quota, issued, paid, pending int
		if err := rows.Scan(&id, &quota, &issued, &paid, &pending); err != nil {
			return err
		}
		n++
		base := "{cat:" + id + "}"
		stock, serr := rdb.Get(ctx, base+":stock").Int()
		line := fmt.Sprintf("kategori %s kuota %d tiket %d lunas %d pending %d", id, quota, issued, paid, pending)
		status := "OK"
		switch {
		case errors.Is(serr, redis.Nil):
			line += " redis: belum dimuat"
		case serr != nil:
			return serr
		default:
			rids, err := rdb.ZRange(ctx, base+":holds", 0, -1).Result()
			if err != nil {
				return err
			}
			held := 0
			for _, rid := range rids {
				v, _ := rdb.Get(ctx, base+":hold:"+rid).Result()
				_, q, _ := strings.Cut(v, "|")
				k, _ := strconv.Atoi(q)
				held += k
			}
			line += fmt.Sprintf(" redis: stok %d ditahan %d (stok+ditahan+lunas=%d)", stock, held, stock+held+paid)
			if stock+held+paid > quota {
				status = "REDIS_LONGGAR"
			}
		}
		if issued > quota {
			status = "OVERSELL"
		}
		if status != "OK" {
			bad++
		}
		fmt.Printf("%-13s %s\n", status, line)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	fmt.Printf("%d kategori diperiksa, %d bermasalah\n", n, bad)
	if bad > 0 {
		return fmt.Errorf("stock-check: %d kategori bermasalah", bad)
	}
	return nil
}
