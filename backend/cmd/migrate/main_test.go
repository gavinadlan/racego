package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func TestDevReleaseOnlyInDevelopment(t *testing.T) {
	if err := DevOnly("development"); err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{"production", "staging", ""} {
		if DevOnly(env) == nil {
			t.Fatalf("APP_ENV=%q harus ditolak", env)
		}
	}
}

func TestLoadgenOnlyDevelopmentAndLoadtestDB(t *testing.T) {
	for _, db := range []string{"racego_loadtest", "loadtest_racego"} {
		if err := LoadgenAllowed("development", db); err != nil {
			t.Fatalf("development + %s harus diizinkan: %v", db, err)
		}
	}
	for _, c := range [][2]string{
		{"production", "racego_loadtest"}, {"staging", "racego_loadtest"}, {"", "racego_loadtest"},
		{"development", "racego"}, {"development", "racego_test"}, {"development", "loadtest"}, {"development", "racego_loadtest_x"},
	} {
		if LoadgenAllowed(c[0], c[1]) == nil {
			t.Fatalf("APP_ENV=%q database=%q harus ditolak", c[0], c[1])
		}
	}
}

// stock-check: hijau pada keadaan normal, gagal saat Redis dilonggarkan (Tugas 11).
func TestStockCheckDetectsLooseRedis(t *testing.T) {
	dsn := os.Getenv("RACEGO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RACEGO_TEST_DATABASE_URL kosong")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	tag := uuid.NewString()[:8]
	var user, org, ev, cat uuid.UUID
	for _, q := range []struct {
		dst  *uuid.UUID
		sql  string
		args []any
	}{
		{&user, `INSERT INTO users (email, name) VALUES ($1, 'Uji') RETURNING id`, []any{"sc-" + tag + "@uji.test"}},
		{&org, `INSERT INTO organizations (slug, name, created_by) VALUES ($1, 'Uji', $2) RETURNING id`, []any{"sc-" + tag, &user}},
		{&ev, `INSERT INTO events (org_id, slug, name, race_date, sale_open_at, sale_close_at, status) VALUES ($1, $2, 'Uji', current_date + 30, now(), now() + interval '9 days', 'published') RETURNING id`, []any{&org, "sc-" + tag}},
		{&cat, `INSERT INTO ticket_categories (event_id, org_id, code, name, distance_km, price, quota, bib_prefix) VALUES ($1, $2, '5K', '5K', 5, 1000, 5, '50') RETURNING id`, []any{&ev, &org}},
	} {
		args := make([]any, len(q.args))
		for i, a := range q.args {
			if p, ok := a.(*uuid.UUID); ok {
				a = *p
			}
			args[i] = a
		}
		if err := db.QueryRow(ctx, q.sql, args...).Scan(q.dst); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM ticket_categories WHERE id = $1`, `DELETE FROM events WHERE id = $1`, `DELETE FROM organizations WHERE id = $1`, `DELETE FROM users WHERE id = $1`} {
			id := map[string]uuid.UUID{"ticket_categories": cat, "events": ev, "organizations": org, "users": user}[strings.Fields(q)[2]]
			db.Exec(ctx, q, id) //nolint:errcheck
		}
	})
	key := "{cat:" + cat.String() + "}:stock"
	rdb.Set(ctx, key, 5, 0)
	if err := stockCheck(ctx, db, rdb, []string{"--event", ev.String()}); err != nil {
		t.Fatalf("keadaan normal harus hijau: %v", err)
	}
	rdb.Set(ctx, key, 9, 0) // Redis dilonggarkan manual
	if err := stockCheck(ctx, db, rdb, []string{"--event", ev.String()}); err == nil {
		t.Fatal("Redis longgar harus terdeteksi")
	}
	rdb.Del(ctx, key) // belum dimuat bukan kegagalan
	if err := stockCheck(ctx, db, rdb, []string{"--event", ev.String()}); err != nil {
		t.Fatalf("belum dimuat: %v", err)
	}
}
