// Package ratelimit membatasi jumlah aksi per kunci (akun, IP, email) dengan penghitung di Redis.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Exceeded: batas terlampaui; boleh lagi setelah RetryAfter.
type Exceeded struct{ RetryAfter time.Duration }

func (e Exceeded) Error() string {
	return fmt.Sprintf("terlalu sering, coba lagi dalam %s", e.RetryAfter)
}

// Allow: INCR + EXPIRE; lebih dari allowed dalam satu jendela waktu = Exceeded.
func Allow(ctx context.Context, rdb redis.Cmdable, key string, allowed int64, window time.Duration) error {
	var incr *redis.IntCmd
	var ttl *redis.DurationCmd
	if _, err := rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		incr = p.Incr(ctx, key)
		p.ExpireNX(ctx, key, window)
		ttl = p.TTL(ctx, key)
		return nil
	}); err != nil {
		return err
	}
	if incr.Val() <= allowed {
		return nil
	}
	return Exceeded{RetryAfter: max(ttl.Val(), time.Second)}
}
