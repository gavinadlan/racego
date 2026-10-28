// Command healthcheck dipakai healthcheck Docker (image distroless tidak punya curl/wget).
//
//	healthcheck http://127.0.0.1:8080/healthz   # sehat bila respons 200 (api)
//	healthcheck deps                            # sehat bila Postgres & Redis terjangkau (worker)
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gavinadlan/racego/backend/internal/config"
	"github.com/gavinadlan/racego/backend/internal/platform"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "pakai: healthcheck <url> | healthcheck deps")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := check(ctx, os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "tidak sehat:", err)
		os.Exit(1)
	}
}

func check(ctx context.Context, target string) error {
	if target != "deps" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("status %d", res.StatusCode)
		}
		return nil
	}
	// ponytail: worker dianggap sehat bila dependensinya terjangkau; loop yang macet tidak terdeteksi.
	// Tambah file heartbeat dari worker.Run bila itu pernah terjadi.
	cfg := config.Load()
	db, err := platform.NewDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	rdb, err := platform.NewRedis(ctx, cfg.RedisAddr, cfg.RedisPassword)
	if err != nil {
		return err
	}
	return rdb.Close()
}
