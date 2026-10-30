package finance

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/ledger"
)

// Release memindahkan hak mitra tertahan → tersedia untuk order lunas yang masa tahannya lewat
// (hari lomba + PAYOUT_HOLD_DAYS, tanggal WIB). orgID != nil: hanya org itu dan TANPA menunggu masa
// tahan (perintah development `make dev-release`). Idempotent dan aman paralel: jurnal unik per order,
// baris order dikunci, dan order yang sudah di-refund tidak dirilis.
func (s *Service) Release(ctx context.Context, orgID *uuid.UUID) (orders int, amount int64, err error) {
	rows, err := s.db.Query(ctx, `
		SELECT o.id FROM orders o JOIN events e ON e.id = o.event_id
		WHERE o.status = 'paid'
		  AND (($1::uuid IS NULL AND e.race_date + $2::int <= (now() AT TIME ZONE 'Asia/Jakarta')::date) OR o.org_id = $1)
		  AND EXISTS (SELECT 1 FROM ledger_journals j WHERE j.ref_type = 'order.paid' AND j.ref_id = o.id::text)
		  AND NOT EXISTS (SELECT 1 FROM ledger_journals j WHERE j.ref_type = 'balance.release' AND j.ref_id = o.id::text)
		ORDER BY o.paid_at LIMIT 1000`, orgID, s.cfg.HoldDays)
	if err != nil {
		return 0, 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, 0, err
	}
	for _, id := range ids {
		n, err := s.releaseOrder(ctx, id)
		if err != nil {
			return orders, amount, err
		}
		if n > 0 {
			orders++
			amount += n
		}
	}
	return orders, amount, nil
}

func (s *Service) releaseOrder(ctx context.Context, orderID uuid.UUID) (int64, error) {
	var released int64
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var orgID uuid.UUID
		var status string
		err := tx.QueryRow(ctx, `SELECT org_id, status FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&orgID, &status)
		if errors.Is(err, pgx.ErrNoRows) || status != "paid" {
			return nil
		}
		if err != nil {
			return err
		}
		paid, ok, err := ledger.PaidOf(ctx, tx, orderID)
		if err != nil || !ok || paid.Partner <= 0 {
			return err
		}
		j, err := ledger.BalanceRelease(orderID, orgID, paid.Partner)
		if err != nil {
			return err
		}
		posted, err := ledger.Post(ctx, tx, j)
		if posted {
			released = paid.Partner
		}
		return err
	})
	return released, err
}
