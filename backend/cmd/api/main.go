// Command api menjalankan HTTP API RaceGo (event, checkout, webhook, tiket, dashboard mitra).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gavinadlan/racego/backend/internal/checkout"
	"github.com/gavinadlan/racego/backend/internal/config"
	"github.com/gavinadlan/racego/backend/internal/notify"
	"github.com/gavinadlan/racego/backend/internal/payment"
	"github.com/gavinadlan/racego/backend/internal/platform"
	"github.com/gavinadlan/racego/backend/internal/queue"
	"github.com/gavinadlan/racego/backend/internal/server"
	"github.com/gavinadlan/racego/backend/internal/ticket"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("api berhenti", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
	defer rdb.Close()
	signer, err := ticket.NewSigner(cfg.QRPrivateKey, cfg.IsDev())
	if err != nil {
		return err
	}

	passSigner, err := queue.NewPassSigner(cfg.Queue.Secret, cfg.IsDev())
	if err != nil {
		return err
	}
	inv := checkout.NewInventory(rdb)

	var snap payment.SnapClient
	if cfg.MidtransMock() {
		slog.Warn("MIDTRANS_SERVER_KEY kosong → mode MOCK: pembayaran disimulasikan")
		snap = payment.NewMockSnap(cfg.FrontendURL)
	} else {
		snap = payment.NewSnapClient(cfg.MidtransServerKey, cfg.MidtransIsProduction)
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: server.NewRouter(server.Deps{
			Cfg: cfg, DB: db, Redis: rdb, Inv: inv, Snap: snap, Signer: signer,
			Queue:  queue.New(rdb, inv, passSigner, cfg.Queue),
			Mailer: notify.NewMailer(cfg.SMTPAddr, cfg.SMTPUsername, cfg.SMTPPassword, cfg.MailFrom),
		}),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("API berjalan", "addr", cfg.HTTPAddr, "env", cfg.Env)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("mematikan API dengan rapi...")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
