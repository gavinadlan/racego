// Command worker memproses outbox (tiket, ledger, email) dan job berkala kuota/order.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gavinadlan/racego/backend/internal/checkout"
	"github.com/gavinadlan/racego/backend/internal/config"
	"github.com/gavinadlan/racego/backend/internal/finance"
	"github.com/gavinadlan/racego/backend/internal/notify"
	"github.com/gavinadlan/racego/backend/internal/payment"
	"github.com/gavinadlan/racego/backend/internal/platform"
	"github.com/gavinadlan/racego/backend/internal/queue"
	"github.com/gavinadlan/racego/backend/internal/secretbox"
	"github.com/gavinadlan/racego/backend/internal/ticket"
	"github.com/gavinadlan/racego/backend/internal/worker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		slog.Error("konfigurasi tidak valid", "err", err)
		os.Exit(1)
	}
	// Kunci yang sama dengan API: nomor rekening pencairan dibuka tepat sebelum dikirim ke gateway.
	box, err := secretbox.FromEnv(cfg.KYCEncKey, cfg.IsDev())
	if err != nil {
		slog.Error("kunci KYC", "err", err)
		os.Exit(1)
	}

	db, err := platform.NewDB(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	rdb, err := platform.NewRedis(ctx, cfg.RedisAddr, cfg.RedisPassword)
	if err != nil {
		slog.Error("redis", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()
	signer, err := ticket.NewSigner(cfg.QRPrivateKey, cfg.IsDev())
	if err != nil {
		slog.Error("kunci QR", "err", err)
		os.Exit(1)
	}

	passSigner, err := queue.NewPassSigner(cfg.Queue.Secret, cfg.IsDev())
	if err != nil {
		slog.Error("kunci antrian", "err", err)
		os.Exit(1)
	}
	inv := checkout.NewInventory(rdb)
	w := worker.New(db, inv, signer,
		notify.NewMailer(cfg.SMTPAddr, cfg.SMTPUsername, cfg.SMTPPassword, cfg.MailFrom), cfg.FrontendURL).
		WithQueue(queue.NewRunner(db, queue.New(rdb, inv, passSigner, cfg.Queue))).
		WithFinance(finance.FromConfig(db, box, cfg))
	if !cfg.MidtransMock() {
		// Cadangan webhook: cek status order ke Midtrans tiap menit (wajib saat development di localhost).
		w.WithReconciler(payment.NewReconciler(
			payment.NewProcessor(db, cfg.SigningKey()), cfg.MidtransServerKey, cfg.MidtransIsProduction))
	}
	slog.Info("worker berjalan")
	w.Run(ctx)
	slog.Info("worker berhenti")
}
