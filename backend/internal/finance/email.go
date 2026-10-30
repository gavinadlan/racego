package finance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/notify"
)

// Mail adalah satu email siap kirim yang dibentuk dari pesan outbox.
type Mail struct{ To, Subject, Body string }

// PayoutMail membentuk email topik payout.email. ok=false bila penerima/payout sudah tidak ada.
func PayoutMail(ctx context.Context, tx pgx.Tx, payload json.RawMessage, frontendURL string) (Mail, bool, error) {
	var p struct {
		PayoutID uuid.UUID `json:"payout_id"`
		Event    string    `json:"event"`
		UserID   uuid.UUID `json:"user_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return Mail{}, false, fmt.Errorf("payload: %w", err)
	}
	var to, orgName, slug string
	var amount int64
	var snap []byte
	var reject, fail *string
	err := tx.QueryRow(ctx, `
		SELECT u.email, o.name, o.slug, p.amount, p.account_snapshot, p.reject_reason, p.fail_reason
		FROM payouts p JOIN organizations o ON o.id = p.org_id, users u WHERE p.id = $1 AND u.id = $2`, p.PayoutID, p.UserID).
		Scan(&to, &orgName, &slug, &amount, &snap, &reject, &fail)
	if errors.Is(err, pgx.ErrNoRows) {
		return Mail{}, false, nil
	}
	if err != nil {
		return Mail{}, false, err
	}
	var acc AccountView
	_ = json.Unmarshal(snap, &acc)
	reason := ""
	if p.Event == "rejected" && reject != nil {
		reason = *reject
	}
	if p.Event == "failed" && fail != nil {
		reason = *fail
	}
	subject, body := notify.PayoutEmail(orgName, p.Event, rupiah(amount), reason, acc.Last4, frontendURL+"/org/"+slug+"/keuangan")
	return Mail{to, subject, body}, true, nil
}

// RefundMail membentuk email topik refund.email (pembeli atau owner/finance).
func RefundMail(ctx context.Context, tx pgx.Tx, payload json.RawMessage, frontendURL string) (Mail, bool, error) {
	var p struct {
		RefundID uuid.UUID `json:"refund_id"`
		Event    string    `json:"event"`
		UserID   uuid.UUID `json:"user_id"`
		Audience string    `json:"audience"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return Mail{}, false, fmt.Errorf("payload: %w", err)
	}
	var buyerEmail, eventName, slug string
	var orderID uuid.UUID
	var amount int64
	var reject *string
	err := tx.QueryRow(ctx, `
		SELECT u.email, e.name, og.slug, f.order_id, f.amount, f.reject_reason
		FROM refunds f JOIN orders o ON o.id = f.order_id JOIN users u ON u.id = o.buyer_id
		JOIN events e ON e.id = o.event_id JOIN organizations og ON og.id = f.org_id WHERE f.id = $1`, p.RefundID).
		Scan(&buyerEmail, &eventName, &slug, &orderID, &amount, &reject)
	if errors.Is(err, pgx.ErrNoRows) {
		return Mail{}, false, nil
	}
	if err != nil {
		return Mail{}, false, err
	}
	to, url := buyerEmail, frontendURL+"/tickets"
	if p.Audience != "buyer" {
		url = frontendURL + "/org/" + slug + "/keuangan"
		err := tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, p.UserID).Scan(&to)
		if errors.Is(err, pgx.ErrNoRows) {
			return Mail{}, false, nil
		}
		if err != nil {
			return Mail{}, false, err
		}
	}
	reason := ""
	if reject != nil {
		reason = *reject
	}
	subject, body := notify.RefundEmail(p.Audience, eventName, orderID.String()[:8], rupiah(amount), p.Event, reason, url)
	return Mail{to, subject, body}, true, nil
}
