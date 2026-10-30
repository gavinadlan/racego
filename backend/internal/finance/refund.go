package finance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/ledger"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

type Refund struct {
	ID               uuid.UUID  `json:"id"`
	OrderID          uuid.UUID  `json:"order_id"`
	OrgID            uuid.UUID  `json:"-"`
	OrgName          string     `json:"org_name"`
	OrgSlug          string     `json:"org_slug"`
	EventName        string     `json:"event_name"`
	BuyerEmail       string     `json:"buyer_email"`
	Amount           int64      `json:"amount"`
	Status           string     `json:"status"`
	Reason           string     `json:"reason"`
	RequestedByName  string     `json:"requested_by_name"`
	ApprovedByName   *string    `json:"approved_by_name"`
	Reference        *string    `json:"reference"`
	RejectReason     *string    `json:"reject_reason"`
	FailReason       *string    `json:"fail_reason"`
	CollectedTickets int        `json:"collected_tickets"` // paket sudah diambil: UI memberi peringatan
	CreatedAt        time.Time  `json:"created_at"`
	ApprovedAt       *time.Time `json:"approved_at"`
	CompletedAt      *time.Time `json:"completed_at"`
}

const refundSelect = `SELECT f.id, f.order_id, f.org_id, o.name, o.slug, e.name, u.email, f.amount, f.status, f.reason,
	ru.name, au.name, f.reference, f.reject_reason, f.fail_reason,
	(SELECT count(*) FROM tickets t WHERE t.order_id = f.order_id AND t.collected_at IS NOT NULL)::int,
	f.created_at, f.approved_at, f.completed_at
	FROM refunds f JOIN organizations o ON o.id = f.org_id JOIN orders od ON od.id = f.order_id
	JOIN events e ON e.id = od.event_id JOIN users u ON u.id = od.buyer_id JOIN users ru ON ru.id = f.requested_by
	LEFT JOIN users au ON au.id = f.approved_by `

func (s *Service) refunds(ctx context.Context, q querier, where string, args ...any) ([]Refund, error) {
	rows, err := q.Query(ctx, refundSelect+where, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Refund])
}

func (s *Service) refund(ctx context.Context, q querier, id uuid.UUID) (Refund, error) {
	list, err := s.refunds(ctx, q, `WHERE f.id = $1`, id)
	if err != nil {
		return Refund{}, err
	}
	if len(list) == 0 {
		return Refund{}, errNotFound
	}
	return list[0], nil
}

var (
	errNotRefundable   = apiError{status: http.StatusConflict, code: "order_not_refundable", msg: "Hanya order berstatus lunas yang bisa di-refund."}
	errAlreadyRefunded = apiError{status: http.StatusConflict, code: "already_refunded", msg: "Order ini sudah di-refund atau sudah punya pengajuan refund."}
)

type lockedOrder struct {
	ID, OrgID uuid.UUID
	Status    string
	Total     int64
}

// lockOrder mengunci order. orgID != nil membatasi ke satu org (order org lain = 404).
func lockOrder(ctx context.Context, tx pgx.Tx, id uuid.UUID, orgID *uuid.UUID) (lockedOrder, error) {
	var o lockedOrder
	err := tx.QueryRow(ctx, `SELECT id, org_id, status, total FROM orders WHERE id = $1 AND ($2::uuid IS NULL OR org_id = $2) FOR UPDATE`, id, orgID).
		Scan(&o.ID, &o.OrgID, &o.Status, &o.Total)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, errNotFound
	}
	return o, err
}

func refundableCheck(o lockedOrder) error {
	switch o.Status {
	case "paid", "needs_refund":
		return nil
	case "refunded":
		return errAlreadyRefunded
	default:
		return errNotRefundable
	}
}

func readReason(w http.ResponseWriter, r *http.Request, maxLen int, extra any) (string, bool) {
	var in struct {
		Reason  string    `json:"reason"`
		OrderID uuid.UUID `json:"order_id"`
	}
	if !httpx.Decode(w, r, &in) {
		return "", false
	}
	reason := strings.TrimSpace(in.Reason)
	if n := utf8.RuneCountInString(reason); n < 10 || n > maxLen {
		httpx.Validation(w, httpx.Fields{"reason": fmt.Sprintf("Alasan 10–%s karakter", thousands(maxLen))})
		return "", false
	}
	if p, ok := extra.(*uuid.UUID); ok {
		*p = in.OrderID
	}
	return reason, true
}

func thousands(n int) string { return strings.TrimPrefix(rupiah(int64(n)), "Rp") }

// RequestRefund: POST /v1/org/{org}/orders/{id}/refund {reason} — owner/finance mengajukan, super admin memutuskan.
func (s *Service) RequestRefund(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := tenant.OrgID(ctx)
	id, err := pathID(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	reason, ok := readReason(w, r, 500, nil)
	if !ok {
		return
	}
	var refundID uuid.UUID
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		o, err := lockOrder(ctx, tx, id, &orgID)
		if err != nil {
			return err
		}
		if err := refundableCheck(o); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO refunds (order_id, org_id, amount, reason, requested_by) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			o.ID, o.OrgID, o.Total, reason, userID(ctx)).Scan(&refundID); err != nil {
			if isUnique(err, "refunds_order_active_uniq") {
				return errAlreadyRefunded
			}
			return err
		}
		if err := audit(ctx, tx, r, o.OrgID, "refund.requested", "refund", refundID.String(), nil,
			map[string]any{"order_id": o.ID, "amount": o.Total, "reason": reason}); err != nil {
			return err
		}
		return queueOrgEmail(ctx, tx, "refund.email", "refund_id", refundID, o.OrgID, "requested")
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out, err := s.refund(ctx, s.db, refundID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// OrgRefunds: GET /v1/org/{org}/refunds?page=
func (s *Service) OrgRefunds(w http.ResponseWriter, r *http.Request) {
	p := page(r)
	list, err := s.refunds(r.Context(), s.db, `WHERE f.org_id = $1 ORDER BY f.created_at DESC LIMIT $2 OFFSET $3`,
		tenant.OrgID(r.Context()), pageSize, (p-1)*pageSize)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list, "page": p, "page_size": pageSize})
}

var refundStatuses = []string{"requested", "approved", "processing", "manual_required", "completed", "rejected", "failed"}

// AdminRefunds: GET /v1/admin/refunds?status=&page=
func (s *Service) AdminRefunds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "requested"
	}
	if status != "all" && !slices.Contains(refundStatuses, status) {
		httpx.Validation(w, httpx.Fields{"status": "Status tidak dikenal"})
		return
	}
	order := "f.created_at ASC"
	if status == "all" || status == "completed" || status == "rejected" {
		order = "f.updated_at DESC"
	}
	p := page(r)
	list, err := s.refunds(ctx, s.db, `WHERE ($1 = 'all' OR f.status = $1) ORDER BY `+order+`, f.id LIMIT $2 OFFSET $3`, status, pageSize, (p-1)*pageSize)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	counts, err := statusCounts(ctx, s.db, "refunds")
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list, "counts": counts, "page": p, "page_size": pageSize, "mock": s.mock})
}

// AdminCreateRefund: POST /v1/admin/refunds {order_id, reason} — super admin membuat & langsung menyetujui.
func (s *Service) AdminCreateRefund(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var orderID uuid.UUID
	reason, ok := readReason(w, r, 1000, &orderID)
	if !ok {
		return
	}
	if orderID == uuid.Nil {
		httpx.Validation(w, httpx.Fields{"order_id": "ID order wajib diisi"})
		return
	}
	actor := userID(ctx)
	var refundID uuid.UUID
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		o, err := lockOrder(ctx, tx, orderID, nil)
		if err != nil {
			return err
		}
		if member, err := isMember(ctx, tx, o.OrgID, actor); err != nil {
			return err
		} else if member {
			return errConflict
		}
		if err := refundableCheck(o); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO refunds (order_id, org_id, amount, reason, requested_by) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			o.ID, o.OrgID, o.Total, reason, actor).Scan(&refundID); err != nil {
			if isUnique(err, "refunds_order_active_uniq") {
				return errAlreadyRefunded
			}
			return err
		}
		if err := audit(ctx, tx, r, o.OrgID, "refund.created", "refund", refundID.String(), nil,
			map[string]any{"order_id": o.ID, "amount": o.Total, "reason": reason}); err != nil {
			return err
		}
		return s.applyRefund(ctx, tx, r, refundID, o)
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out, err := s.refund(ctx, s.db, refundID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// AdminApproveRefund: POST /v1/admin/refunds/{id}/approve
func (s *Service) AdminApproveRefund(w http.ResponseWriter, r *http.Request) {
	s.decideRefund(w, r, "approve", "")
}

// AdminRejectRefund: POST /v1/admin/refunds/{id}/reject {reason}
func (s *Service) AdminRejectRefund(w http.ResponseWriter, r *http.Request) {
	reason, ok := readReason(w, r, 1000, nil)
	if ok {
		s.decideRefund(w, r, "reject", reason)
	}
}

// AdminMarkRefundDone: POST /v1/admin/refunds/{id}/mark-done {reference} — dana dikirim manual di luar sistem.
func (s *Service) AdminMarkRefundDone(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reference string `json:"reference"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	ref := strings.TrimSpace(in.Reference)
	if n := utf8.RuneCountInString(ref); n < 3 || n > 100 {
		httpx.Validation(w, httpx.Fields{"reference": "Nomor referensi transfer 3–100 karakter"})
		return
	}
	s.decideRefund(w, r, "done", ref)
}

func (s *Service) decideRefund(w http.ResponseWriter, r *http.Request, action, text string) {
	ctx := r.Context()
	id, err := pathID(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	actor := userID(ctx)
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var orgID, orderID uuid.UUID
		var status string
		var amount int64
		err := tx.QueryRow(ctx, `SELECT org_id, order_id, status, amount FROM refunds WHERE id = $1 FOR UPDATE`, id).Scan(&orgID, &orderID, &status, &amount)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		if member, err := isMember(ctx, tx, orgID, actor); err != nil {
			return err
		} else if member {
			return errConflict
		}
		before := map[string]any{"status": status}
		switch action {
		case "approve":
			if status != "requested" {
				return invalidState("Refund ini sudah diputuskan (status " + status + ").")
			}
			o, err := lockOrder(ctx, tx, orderID, nil)
			if err != nil {
				return err
			}
			if err := refundableCheck(o); err != nil {
				return err
			}
			return s.applyRefund(ctx, tx, r, id, o)
		case "reject":
			if status != "requested" {
				return invalidState("Refund ini sudah diputuskan (status " + status + ").")
			}
			if _, err := tx.Exec(ctx, `UPDATE refunds SET status = 'rejected', approved_by = $2, reject_reason = $3, updated_at = now() WHERE id = $1`, id, actor, text); err != nil {
				return err
			}
			if err := audit(ctx, tx, r, orgID, "refund.rejected", "refund", id.String(), before, map[string]any{"status": "rejected", "reason": text}); err != nil {
				return err
			}
			return s.queueRefundEmails(ctx, tx, id, orgID, "rejected")
		default: // done
			if status != "manual_required" && status != "failed" {
				return invalidState("Hanya refund yang perlu dikirim manual yang bisa ditandai selesai (status " + status + ").")
			}
			if _, err := tx.Exec(ctx, `UPDATE refunds SET status = 'completed', reference = $2, completed_at = now(), updated_at = now() WHERE id = $1`, id, text); err != nil {
				return err
			}
			if err := audit(ctx, tx, r, orgID, "refund.completed_manual", "refund", id.String(), before,
				map[string]any{"status": "completed", "reference": text, "amount": amount}); err != nil {
				return err
			}
			return s.queueRefundEmails(ctx, tx, id, orgID, "completed")
		}
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out, err := s.refund(ctx, s.db, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// applyRefund: kejadian ekonomi refund, dalam transaksi pemanggil (order sudah terkunci & refundable):
// order & semua tiket → refunded, jurnal order.refund, refund → approved (worker mengirim uangnya).
func (s *Service) applyRefund(ctx context.Context, tx pgx.Tx, r *http.Request, refundID uuid.UUID, o lockedOrder) error {
	paid, ok, err := ledger.PaidOf(ctx, tx, o.ID)
	if err != nil {
		return err
	}
	if !ok {
		// needs_refund (bayar telat, kuota habis): worker tidak pernah membukukan order.paid. Bukukan dulu
		// penerimaan uangnya agar kas dan refund tercatat lengkap.
		var fee int64
		var payType string
		if err := tx.QueryRow(ctx, `
			SELECT o.platform_fee, coalesce((SELECT pe.payment_type FROM payment_events pe WHERE pe.order_id = o.id
			  AND pe.transaction_status IN ('settlement', 'capture') ORDER BY pe.id LIMIT 1), '')
			FROM orders o WHERE o.id = $1`, o.ID).Scan(&fee, &payType); err != nil {
			return err
		}
		j, err := ledger.OrderPaid(o.ID, o.OrgID, o.Total, fee, ledger.EstimatePGFee(payType, o.Total))
		if err != nil {
			return err
		}
		if _, err := ledger.Post(ctx, tx, j); err != nil {
			return err
		}
		if paid, _, err = ledger.PaidOf(ctx, tx, o.ID); err != nil {
			return err
		}
	}
	var released bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ledger_journals WHERE ref_type = 'balance.release' AND ref_id = $1)`, o.ID.String()).Scan(&released); err != nil {
		return err
	}
	j, err := ledger.OrderRefund(o.ID, o.OrgID, o.Total, paid, released)
	if err != nil {
		return err
	}
	posted, err := ledger.Post(ctx, tx, j)
	if err != nil {
		return err
	}
	if !posted {
		return errAlreadyRefunded
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status = 'refunded' WHERE id = $1`, o.ID); err != nil {
		return err
	}
	tickets, err := tx.Exec(ctx, `UPDATE tickets SET status = 'refunded' WHERE order_id = $1`, o.ID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE refunds SET status = 'approved', approved_by = $2, approved_at = now(), next_attempt_at = now(), updated_at = now()
		WHERE id = $1`, refundID, userID(ctx)); err != nil {
		return err
	}
	if err := audit(ctx, tx, r, o.OrgID, "refund.approved", "refund", refundID.String(), map[string]any{"order_status": o.Status},
		map[string]any{"status": "approved", "order_id": o.ID, "amount": o.Total, "tickets_refunded": tickets.RowsAffected(),
			"partner_debit": paid.Partner, "from_available": released}); err != nil {
		return err
	}
	return s.queueRefundEmails(ctx, tx, refundID, o.OrgID, "approved")
}

// queueRefundEmails: satu email ke pembeli (bila relevan) + satu per owner/finance org.
func (s *Service) queueRefundEmails(ctx context.Context, q querier, refundID, orgID uuid.UUID, event string) error {
	if event != "requested" {
		if err := enqueue(ctx, q, "refund.email", map[string]any{"refund_id": refundID, "event": event, "audience": "buyer"}); err != nil {
			return err
		}
	}
	return queueOrgEmail(ctx, q, "refund.email", "refund_id", refundID, orgID, event)
}

// ExecuteRefunds mengirim refund approved ke Refunder (di luar transaksi, klaim dengan sewa waktu).
// Status processing + next_attempt_at kedaluwarsa = worker sebelumnya mati di tengah jalan → diulang
// dengan refund_key yang sama (idempoten di gateway).
func (s *Service) ExecuteRefunds(ctx context.Context) error {
	for range 20 {
		var id, orderID, orgID uuid.UUID
		var amount int64
		var attempts int
		var reason string
		err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			err := tx.QueryRow(ctx, `
				SELECT id, order_id, org_id, amount, attempts, reason FROM refunds
				WHERE status IN ('approved', 'processing') AND coalesce(next_attempt_at, now()) <= now()
				ORDER BY approved_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id, &orderID, &orgID, &amount, &attempts, &reason)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE refunds SET status = 'processing', next_attempt_at = now() + interval '2 minutes', updated_at = now() WHERE id = $1`, id)
			return err
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		res, callErr := s.refunder.Refund(callCtx, RefundRequest{Key: id.String(), OrderID: orderID.String(), Amount: amount, Reason: reason})
		cancel()
		if err := s.afterRefund(ctx, id, orgID, attempts+1, res, callErr); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) afterRefund(ctx context.Context, id, orgID uuid.UUID, attempt int, res RefundResult, callErr error) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM refunds WHERE id = $1 FOR UPDATE`, id).Scan(&status); err != nil {
			return err
		}
		if status != "processing" {
			return nil
		}
		before := map[string]any{"status": status}
		var newStatus, action string
		switch {
		case callErr != nil && attempt < maxAttempts:
			slog.WarnContext(ctx, "refund gagal sementara, akan dicoba lagi", "refund_id", id, "attempt", attempt, "err", callErr)
			_, err := tx.Exec(ctx, `
				UPDATE refunds SET status = 'approved', attempts = $2, next_attempt_at = now() + make_interval(secs => $3),
				  fail_reason = $4, updated_at = now() WHERE id = $1`, id, attempt, backoff(attempt).Seconds(), clip(callErr.Error(), 200))
			return err
		case callErr != nil:
			newStatus, action = "failed", "refund.failed"
			_, err := tx.Exec(ctx, `UPDATE refunds SET status = 'failed', attempts = $2, fail_reason = $3, next_attempt_at = NULL, updated_at = now() WHERE id = $1`,
				id, attempt, fmt.Sprintf("Gateway tidak merespons setelah %d percobaan; kirim manual", attempt))
			if err != nil {
				return err
			}
		case res.State == RefundManual:
			newStatus, action = "manual_required", "refund.manual_required"
			if _, err := tx.Exec(ctx, `UPDATE refunds SET status = 'manual_required', attempts = $2, fail_reason = $3, next_attempt_at = NULL, updated_at = now() WHERE id = $1`,
				id, attempt, clip(res.Reason, 200)); err != nil {
				return err
			}
		default:
			newStatus, action = "completed", "refund.completed"
			if _, err := tx.Exec(ctx, `UPDATE refunds SET status = 'completed', attempts = $2, reference = $3, fail_reason = NULL, completed_at = now(), next_attempt_at = NULL, updated_at = now() WHERE id = $1`,
				id, attempt, res.Reference); err != nil {
				return err
			}
		}
		slog.InfoContext(ctx, "refund dieksekusi", "refund_id", id, "status", newStatus)
		if err := audit(ctx, tx, nil, orgID, action, "refund", id.String(), before, map[string]any{"status": newStatus, "reference": res.Reference}); err != nil {
			return err
		}
		if newStatus == "completed" {
			return s.queueRefundEmails(ctx, tx, id, orgID, "completed")
		}
		return queueOrgEmail(ctx, tx, "refund.email", "refund_id", id, orgID, newStatus)
	})
}
