// Command migrate menjalankan migrasi skema, seed data contoh, atau reset database.
//
//	go run ./cmd/migrate up     # terapkan migrasi *.up.sql yang belum dijalankan
//	go run ./cmd/migrate seed   # isi data contoh (migrations/seed.sql)
//	go run ./cmd/migrate reset  # jalankan semua *.down.sql (HAPUS data!)
package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/config"
	"github.com/gavinadlan/racego/backend/internal/platform"
	"github.com/gavinadlan/racego/backend/migrations"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("pemakaian: migrate up|seed|reset")
	}
	ctx := context.Background()
	cfg := config.Load()
	db, err := platform.NewDB(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("koneksi database gagal (sudah jalankan `make up`?): %v", err)
	}
	defer db.Close()

	switch os.Args[1] {
	case "up":
		err = up(ctx, db)
	case "seed":
		err = execFile(ctx, db, "seed.sql")
		if err == nil {
			fmt.Println("✓ seed selesai")
		}
	case "reset":
		if !cfg.IsDev() {
			log.Fatal("reset hanya diizinkan di APP_ENV=development")
		}
		err = reset(ctx, db)
	default:
		err = fmt.Errorf("perintah tidak dikenal: %s", os.Args[1])
	}
	if err != nil {
		log.Fatal(err)
	}
}

func up(ctx context.Context, db *pgxpool.Pool) error {
	if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	files, err := list(".up.sql")
	if err != nil {
		return err
	}
	applied := 0
	for _, f := range files {
		version := strings.TrimSuffix(f, ".up.sql")
		var exists bool
		if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sql, err := migrations.FS.ReadFile(f)
		if err != nil {
			return err
		}
		tx, err := db.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrasi %s gagal: %w", f, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		fmt.Println("✓ migrasi", version)
		applied++
	}
	if applied == 0 {
		fmt.Println("✓ skema sudah terbaru")
	}
	return nil
}

func reset(ctx context.Context, db *pgxpool.Pool) error {
	files, err := list(".down.sql")
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	for _, f := range files {
		if err := execFile(ctx, db, f); err != nil {
			return err
		}
		fmt.Println("✓ rollback", f)
	}
	_, err = db.Exec(ctx, `DROP TABLE IF EXISTS schema_migrations`)
	return err
}

func execFile(ctx context.Context, db *pgxpool.Pool, name string) error {
	sql, err := migrations.FS.ReadFile(name)
	if err != nil {
		return err
	}
	if _, err := db.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func list(suffix string) ([]string, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}
