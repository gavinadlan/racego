// Package worker menjalankan pekerjaan asinkron: memproses outbox (tiket, ledger, email)
// dan job berkala (muat stok, kembalikan slot kedaluwarsa, tandai order expired).
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/checkout"
	"github.com/gavinadlan/racego/backend/internal/ledger"
	"github.com/gavinadlan/racego/backend/internal/notify"
	"github.com/gavinadlan/racego/backend/internal/payment"
	"github.com/gavinadlan/racego/backend/internal/queue"
	"github.com/gavinadlan/racego/backend/internal/ticket"
)

const (
	maxAttempts = 10
	// Grace period: slot baru dikembalikan 2 menit setelah timer habis, memberi waktu
	// webhook settlement yang terlambat datang lebih dulu.
	releaseGrace = 2 * time.Minute
)

type Worker struct {
	db          *pgxpool.Pool
	inv         *checkout.Inventory
	signer      *ticket.Signer
	mailer      *notify.Mailer
	frontendURL string
	reconciler  *payment.Reconciler // nil di mode mock
	queue       *queue.Runner
}

// WithQueue mengaktifkan pemanggilan antrian virtual tiap detik.
func (w *Worker) WithQueue(r *queue.Runner) *Worker {
	w.queue = r
	return w
}

// WithReconciler mengaktifkan pengecekan berkala status order ke Midtrans.
func (w *Worker) WithReconciler(r *payment.Reconciler) *Worker {
	w.reconciler = r
	return w
}

func New(db *pgxpool.Pool, inv *checkout.Inventory, signer *ticket.Signer, mailer *notify.Mailer, frontendURL string) *Worker {
	return &Worker{db: db, inv: inv, signer: signer, mailer: mailer, frontendURL: frontendURL}
}

// Run berjalan sampai ctx dibatalkan.
func (w *Worker) Run(ctx context.Context) {
	outbox := time.NewTicker(500 * time.Millisecond)
	holds := time.NewTicker(15 * time.Second)
	sync := time.NewTicker(30 * time.Second)
	reconcile := time.NewTicker(time.Minute)
	defer outbox.Stop()
	defer holds.Stop()
	defer sync.Stop()
	defer reconcile.Stop()

	// Antrian di goroutine sendiri: email/outbox yang lambat tidak boleh menahan pemanggilan.
	go w.runQueues(ctx)
	w.syncStock(ctx)
	w.reconcilePayments(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-outbox.C:
			w.drainOutbox(ctx)
		case <-reconcile.C:
			w.reconcilePayments(ctx)
		case <-holds.C:
			w.expireOrders(ctx)
			w.releaseExpiredHolds(ctx)
		case <-sync.C:
			w.syncStock(ctx)
		}
	}
}

// ===== Outbox =====

type message struct {
	ID      int64
	Topic   string
	Payload json.RawMessage
}

// drainOutbox memproses pesan satu per satu, masing-masing dalam transaksinya sendiri.
// FOR UPDATE SKIP LOCKED membuat beberapa instance worker aman berjalan bersamaan.
func (w *Worker) drainOutbox(ctx context.Context) {
	for i := 0; i < 100; i++ {
		done, err := w.processOne(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "outbox", "err", err)
			return
		}
		if done {
			return
		}
	}
}

func (w *Worker) processOne(ctx context.Context) (empty bool, err error) {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var m message
	err = tx.QueryRow(ctx, `
		SELECT id, topic, payload FROM outbox
		WHERE processed_at IS NULL AND attempts < $1 AND available_at <= now()
		ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`, maxAttempts).Scan(&m.ID, &m.Topic, &m.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	if handleErr := w.handle(ctx, tx, m); handleErr != nil {
		_ = tx.Rollback(ctx)
		slog.WarnContext(ctx, "outbox gagal, akan dicoba lagi", "id", m.ID, "topic", m.Topic, "err", handleErr)
		// Backoff eksponensial: 2, 4, 8 ... detik, maksimal 10 menit.
		_, err = w.db.Exec(ctx, `
			UPDATE outbox SET attempts = attempts + 1, last_error = $2,
			       available_at = now() + make_interval(secs => least(power(2, attempts + 1), 600))
			WHERE id = $1`, m.ID, handleErr.Error())
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE outbox SET processed_at = now(), attempts = attempts + 1 WHERE id = $1`, m.ID); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}

func (w *Worker) handle(ctx context.Context, tx pgx.Tx, m message) error {
	var p struct {
		OrderID     uuid.UUID `json:"order_id"`
		PaymentType string    `json:"payment_type"`
	}
	if err := json.Unmarshal(m.Payload, &p); err != nil {
		return fmt.Errorf("payload: %w", err)
	}
	switch m.Topic {
	case "order.paid":
		return w.onOrderPaid(ctx, tx, p.OrderID, p.PaymentType)
	case "order.released":
		return w.onOrderReleased(ctx, tx, p.OrderID)
	case "ticket.email":
		return w.onTicketEmail(ctx, tx, p.OrderID)
	default:
		slog.WarnContext(ctx, "topic outbox tidak dikenal", "topic", m.Topic)
		return nil
	}
}

type orderRow struct {
	ID, OrgID, CategoryID, BuyerID uuid.UUID
	ReservationID, Status          string
	Qty                            int
	Total, PlatformFee             int64
}

func loadOrder(ctx context.Context, tx pgx.Tx, id uuid.UUID) (orderRow, error) {
	var o orderRow
	err := tx.QueryRow(ctx, `
		SELECT id, org_id, category_id, buyer_id, reservation_id, status, qty, total, platform_fee
		FROM orders WHERE id = $1 FOR UPDATE`, id,
	).Scan(&o.ID, &o.OrgID, &o.CategoryID, &o.BuyerID, &o.ReservationID, &o.Status, &o.Qty, &o.Total, &o.PlatformFee)
	return o, err
}

// onOrderPaid: kunci slot di Redis → terbitkan tiket → catat ledger → antrekan email.
// Setiap langkah idempotent, jadi aman bila pesan diproses ulang.
func (w *Worker) onOrderPaid(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, paymentType string) error {
	o, err := loadOrder(ctx, tx, orderID)
	if err != nil {
		return err
	}
	if o.Status != "paid" {
		return nil
	}
	res, err := w.inv.Confirm(ctx, o.CategoryID.String(), o.ReservationID, o.BuyerID.String(), o.Qty)
	if err != nil {
		return err
	}
	if res == checkout.ConfirmNeedsRefund {
		slog.WarnContext(ctx, "bayar terlambat & kuota habis → perlu refund", "order_id", o.ID)
		_, err := tx.Exec(ctx, `UPDATE orders SET status = 'needs_refund' WHERE id = $1`, o.ID)
		return err
	}

	n, err := ticket.IssueForOrder(ctx, tx, w.signer, o.ID)
	if err != nil {
		return fmt.Errorf("terbitkan tiket: %w", err)
	}
	j, err := ledger.OrderPaid(o.ID, o.OrgID, o.Total, o.PlatformFee, ledger.EstimatePGFee(paymentType, o.Total))
	if err != nil {
		return err
	}
	if _, err := ledger.Post(ctx, tx, j); err != nil {
		return fmt.Errorf("ledger: %w", err)
	}
	if n > 0 {
		payload, _ := json.Marshal(map[string]any{"order_id": o.ID})
		if _, err := tx.Exec(ctx, `INSERT INTO outbox (topic, payload) VALUES ('ticket.email', $1)`, payload); err != nil {
			return err
		}
	}
	slog.InfoContext(ctx, "tiket terbit", "order_id", o.ID, "jumlah", n, "confirm", res)
	return nil
}

func (w *Worker) onOrderReleased(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	o, err := loadOrder(ctx, tx, orderID)
	if err != nil {
		return err
	}
	hold, found, err := w.inv.GetHold(ctx, o.CategoryID.String(), o.ReservationID)
	if err != nil || !found {
		return err
	}
	_, err = w.inv.Release(ctx, o.CategoryID.String(), o.ReservationID, hold)
	return err
}

func (w *Worker) onTicketEmail(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	var buyerName, buyerEmail, eventName string
	if err := tx.QueryRow(ctx, `
		SELECT u.name, u.email, e.name FROM orders o
		JOIN users u ON u.id = o.buyer_id JOIN events e ON e.id = o.event_id
		WHERE o.id = $1`, orderID).Scan(&buyerName, &buyerEmail, &eventName); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT p.full_name, c.code, t.bib_number FROM tickets t
		JOIN participants p ON p.id = t.participant_id JOIN ticket_categories c ON c.id = t.category_id
		WHERE t.order_id = $1 ORDER BY t.bib_number`, orderID)
	if err != nil {
		return err
	}
	lines, err := pgx.CollectRows(rows, pgx.RowToStructByPos[notify.TicketLine])
	if err != nil {
		return err
	}
	subject, body := notify.TicketEmail(buyerName, eventName, w.frontendURL+"/tickets", lines)
	return w.mailer.Send(buyerEmail, subject, body)
}

// ===== Job berkala =====

// runQueues memanggil peserta antrian virtual tiap detik. Gagal (mis. Redis sesaat putus) hanya
// dicatat; putaran berikutnya mencoba lagi.
func (w *Worker) runQueues(ctx context.Context) {
	if w.queue == nil {
		return
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			n, errs := w.queue.Tick(ctx, now)
			for _, err := range errs {
				slog.ErrorContext(ctx, "antrian", "err", err)
			}
			if n > 0 {
				slog.DebugContext(ctx, "antrian: dipanggil", "jumlah", n)
			}
		}
	}
}

func (w *Worker) syncStock(ctx context.Context) {
	if n, err := checkout.SyncOpenEvents(ctx, w.db, w.inv); err != nil {
		slog.ErrorContext(ctx, "sinkron stok", "err", err)
	} else if n > 0 {
		slog.DebugContext(ctx, "sinkron stok", "kategori", n)
	}
}

// expireOrders menandai order pending yang lewat waktu lalu mengantrekan pengembalian slot.
// reconcilePayments menanyakan ke Midtrans status order yang belum lunas dalam 24 jam terakhir,
// selama Midtrans belum memberi status akhir. Menangkap pembayaran yang webhook-nya tidak sampai.
func (w *Worker) reconcilePayments(ctx context.Context) {
	if w.reconciler == nil {
		return
	}
	rows, err := w.db.Query(ctx, `
		SELECT o.id FROM orders o
		WHERE o.status IN ('pending', 'expired')
		  AND o.created_at > now() - interval '24 hours'
		  AND o.snap_token IS NOT NULL AND o.snap_token NOT LIKE 'mock-%'
		  AND NOT EXISTS (
		    SELECT 1 FROM payment_events pe
		    WHERE pe.order_id = o.id
		      AND pe.transaction_status IN ('settlement', 'capture', 'expire', 'cancel', 'deny', 'failure'))
		ORDER BY o.created_at DESC
		LIMIT 50`)
	if err != nil {
		slog.ErrorContext(ctx, "reconcile: daftar order", "err", err)
		return
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		slog.ErrorContext(ctx, "reconcile: daftar order", "err", err)
		return
	}
	for _, id := range ids {
		if _, err := w.reconciler.Check(ctx, id.String()); err != nil {
			slog.WarnContext(ctx, "reconcile: cek status midtrans", "order_id", id, "err", err)
		}
	}
}

func (w *Worker) expireOrders(ctx context.Context) {
	err := pgx.BeginFunc(ctx, w.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE orders SET status = 'expired'
			WHERE status = 'pending' AND expires_at < now() - make_interval(secs => $1)
			RETURNING id`, releaseGrace.Seconds())
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, id := range ids {
			payload, _ := json.Marshal(map[string]any{"order_id": id})
			if _, err := tx.Exec(ctx, `INSERT INTO outbox (topic, payload) VALUES ('order.released', $1)`, payload); err != nil {
				return err
			}
		}
		if len(ids) > 0 {
			slog.InfoContext(ctx, "order kedaluwarsa", "jumlah", len(ids))
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "expire orders", "err", err)
	}
}

// releaseExpiredHolds mengembalikan slot dari reservasi yang tidak pernah dijadikan order.
func (w *Worker) releaseExpiredHolds(ctx context.Context) {
	cats, err := w.inv.ActiveCategories(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "daftar kategori", "err", err)
		return
	}
	cutoff := time.Now().Add(-releaseGrace)
	released := 0
	for _, cat := range cats {
		ids, err := w.inv.ExpiredHolds(ctx, cat, cutoff, 500)
		if err != nil {
			slog.ErrorContext(ctx, "hold kedaluwarsa", "kategori", cat, "err", err)
			continue
		}
		for _, rid := range ids {
			hold, found, err := w.inv.GetHold(ctx, cat, rid)
			if err != nil || !found {
				continue
			}
			if ok, err := w.inv.Release(ctx, cat, rid, hold); err == nil && ok {
				released++
			}
		}
	}
	if released > 0 {
		slog.InfoContext(ctx, "slot dikembalikan ke stok", "jumlah", released)
	}
}
