package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/httpx"
)

var ErrBadSignature = errors.New("signature notifikasi tidak valid")

type Outcome string

const (
	OutcomePaid      Outcome = "paid"      // order jadi lunas, event order.paid dikirim
	OutcomeReleased  Outcome = "released"  // order batal/kedaluwarsa, slot dikembalikan
	OutcomeDuplicate Outcome = "duplicate" // notifikasi yang sama sudah pernah diproses
	OutcomeRecorded  Outcome = "recorded"  // dicatat saja (pending, atau status tidak berubah)
	OutcomeMismatch  Outcome = "amount_mismatch"
	OutcomeIgnored   Outcome = "ignored"
)

// Processor memproses notifikasi Midtrans secara idempotent.
type Processor struct {
	db         *pgxpool.Pool
	signingKey string
}

func NewProcessor(db *pgxpool.Pool, signingKey string) *Processor {
	return &Processor{db: db, signingKey: signingKey}
}

// Process: verifikasi → catat event (unik per order+status) → ubah status order maju saja
// → tulis outbox di transaksi yang sama. Pekerjaan berat dikerjakan worker.
func (p *Processor) Process(ctx context.Context, n Notification, raw []byte) (Outcome, error) {
	if !n.Verify(p.signingKey) {
		return "", ErrBadSignature
	}
	orderID, err := uuid.Parse(n.OrderID)
	if err != nil {
		return OutcomeIgnored, nil // bukan order dari sistem ini (mis. tes dari dashboard)
	}

	tx, err := p.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var eventID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO payment_events (order_id, transaction_id, transaction_status, fraud_status, status_code, gross_amount, payment_type, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (order_id, transaction_status) DO NOTHING
		RETURNING id`,
		orderID, n.TransactionID, n.TransactionStatus, n.FraudStatus, n.StatusCode, n.GrossAmount, n.PaymentType, raw,
	).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutcomeDuplicate, nil
	}
	if err != nil {
		return "", err
	}

	outcome := OutcomeRecorded
	switch {
	case n.IsPaid():
		outcome, err = p.markPaid(ctx, tx, orderID, n)
	case n.IsFailed():
		outcome, err = p.markReleased(ctx, tx, orderID, n.TransactionStatus)
	}
	if err != nil {
		return "", err
	}
	return outcome, tx.Commit(ctx)
}

func (p *Processor) markPaid(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, n Notification) (Outcome, error) {
	var total int64
	err := tx.QueryRow(ctx, `SELECT total FROM orders WHERE id = $1`, orderID).Scan(&total)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutcomeIgnored, nil
	}
	if err != nil {
		return "", err
	}
	amount, err := n.AmountRupiah()
	if err != nil || amount != total {
		slog.WarnContext(ctx, "nominal pembayaran tidak cocok", "order_id", orderID, "gross_amount", n.GrossAmount, "total", total)
		_, err = tx.Exec(ctx, `INSERT INTO audit_logs (action, entity, entity_id, after) VALUES ('payment.amount_mismatch', 'order', $1, $2)`,
			orderID.String(), map[string]any{"gross_amount": n.GrossAmount, "expected": total})
		return OutcomeMismatch, err
	}

	// Maju saja: pending → paid, atau expired → paid (bayar telat; worker cek ulang stok).
	tag, err := tx.Exec(ctx, `
		UPDATE orders SET status = 'paid', paid_at = now()
		WHERE id = $1 AND status IN ('pending', 'expired')`, orderID)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return OutcomeRecorded, nil
	}
	return OutcomePaid, enqueue(ctx, tx, "order.paid", map[string]any{"order_id": orderID, "payment_type": n.PaymentType})
}

func (p *Processor) markReleased(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, status string) (Outcome, error) {
	newStatus := "cancelled"
	if status == "expire" {
		newStatus = "expired"
	}
	tag, err := tx.Exec(ctx, `UPDATE orders SET status = $2 WHERE id = $1 AND status = 'pending'`, orderID, newStatus)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return OutcomeRecorded, nil
	}
	return OutcomeReleased, enqueue(ctx, tx, "order.released", map[string]any{"order_id": orderID})
}

// enqueue menulis pesan ke tabel outbox (transactional outbox pattern).
func enqueue(ctx context.Context, tx pgx.Tx, topic string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox (topic, payload) VALUES ($1, $2)`, topic, b)
	return err
}

// ===== HTTP =====

// WebhookHandler: POST /v1/webhooks/midtrans. Membalas cepat; kode status menentukan retry Midtrans.
func (p *Processor) WebhookHandler(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Body tidak terbaca")
		return
	}
	var n Notification
	if err := json.Unmarshal(raw, &n); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json", "JSON tidak valid")
		return
	}
	outcome, err := p.Process(r.Context(), n, raw)
	if errors.Is(err, ErrBadSignature) {
		slog.WarnContext(r.Context(), "webhook signature tidak valid", "order_id", n.OrderID, "ip", r.RemoteAddr)
		httpx.Error(w, http.StatusForbidden, "bad_signature", "Signature tidak valid")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err) // 5xx → Midtrans akan mengirim ulang
		return
	}
	slog.InfoContext(r.Context(), "webhook midtrans", "order_id", n.OrderID, "status", n.TransactionStatus, "outcome", outcome)
	httpx.JSON(w, http.StatusOK, map[string]any{"outcome": outcome})
}

// SimulateHandler (HANYA development): POST /v1/dev/orders/{orderID}/simulate {"status":"settlement"}
// Membuat notifikasi bertanda tangan valid seolah dari Midtrans, lalu memprosesnya lewat jalur yang sama.
func (p *Processor) SimulateHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if !httpx.Decode(w, r, &body) {
		return
	}
	if body.Status == "" {
		body.Status = "settlement"
	}
	orderID := chi.URLParam(r, "orderID")
	var total int64
	err := p.db.QueryRow(r.Context(), `SELECT total FROM orders WHERE id = $1`, orderID).Scan(&total)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "order_not_found", "Order tidak ditemukan")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	n := Notification{
		OrderID:           orderID,
		TransactionID:     "sim-" + uuid.NewString(),
		TransactionStatus: body.Status,
		FraudStatus:       "accept",
		StatusCode:        statusCodeFor(body.Status),
		GrossAmount:       FormatAmount(total),
		PaymentType:       "bank_transfer",
	}
	n.SignatureKey = Signature(n.OrderID, n.StatusCode, n.GrossAmount, p.signingKey)
	raw, _ := json.Marshal(n)
	outcome, err := p.Process(r.Context(), n, raw)
	if err != nil {
		httpx.Internal(w, r, fmt.Errorf("simulate: %w", err))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"outcome": outcome})
}

func statusCodeFor(status string) string {
	switch status {
	case "settlement", "capture":
		return "200"
	case "pending":
		return "201"
	default:
		return "202"
	}
}
