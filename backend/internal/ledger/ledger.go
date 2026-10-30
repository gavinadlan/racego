// Package ledger mencatat setiap rupiah secara double-entry (debit = kredit), append-only.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Akun buku besar.
const (
	AccMidtransCash     = "kas_midtrans"         // dana di Midtrans milik platform + mitra
	AccPartnerHeld      = "saldo_mitra_tertahan" // hak mitra, belum bisa dicairkan
	AccPartnerAvailable = "saldo_mitra_tersedia" // hak mitra, siap dicairkan
	AccPlatformRevenue  = "pendapatan_platform"  // fee platform
	AccPayoutInProgress = "payout_dalam_proses"
	AccRefundPayable    = "refund_terutang"
	AccRefundPGExpense  = "beban_refund_pg" // biaya PG yang tidak kembali saat refund (ditanggung platform)
)

type Entry struct {
	Account string
	OrgID   *uuid.UUID
	Debit   int64
	Credit  int64
}

type Journal struct {
	RefType string // mis. "order.paid"
	RefID   string // mis. id order → jurnal untuk satu kejadian hanya sekali
	Memo    string
	Entries []Entry
}

var ErrUnbalanced = errors.New("jurnal tidak seimbang")

// Validate memastikan total debit = total kredit dan tiap baris hanya satu sisi.
func (j Journal) Validate() error {
	var d, c int64
	for _, e := range j.Entries {
		if e.Debit < 0 || e.Credit < 0 || (e.Debit > 0 && e.Credit > 0) {
			return fmt.Errorf("baris %s tidak valid", e.Account)
		}
		d += e.Debit
		c += e.Credit
	}
	if d != c || d == 0 {
		return fmt.Errorf("%w: debit %d, kredit %d", ErrUnbalanced, d, c)
	}
	return nil
}

// OrderPaid membuat jurnal untuk order lunas. Biaya payment gateway dan fee platform
// ditanggung mitra (bisa diubah per event nanti):
//
//	Debit  kas_midtrans          total - biaya PG   (yang benar-benar diterima)
//	Kredit pendapatan_platform   fee platform
//	Kredit saldo_mitra_tertahan  sisanya
func OrderPaid(orderID, orgID uuid.UUID, total, platformFee, pgFee int64) (Journal, error) {
	net := total - pgFee
	partner := net - platformFee
	if partner < 0 {
		return Journal{}, fmt.Errorf("fee melebihi nilai order %s", orderID)
	}
	j := Journal{
		RefType: "order.paid",
		RefID:   orderID.String(),
		Memo:    fmt.Sprintf("Order %s lunas (biaya PG %d)", orderID, pgFee),
		Entries: []Entry{
			{Account: AccMidtransCash, Debit: net},
			{Account: AccPlatformRevenue, Credit: platformFee},
			{Account: AccPartnerHeld, OrgID: &orgID, Credit: partner},
		},
	}
	return j, j.Validate()
}

// PlatformFee menghitung fee dari basis poin (300 = 3%), dibulatkan ke rupiah terdekat.
func PlatformFee(subtotal int64, feeBps int) int64 {
	return int64(math.Round(float64(subtotal) * float64(feeBps) / 10000))
}

// EstimatePGFee memperkirakan biaya Midtrans + PPN 11% per metode bayar.
// Ini ESTIMASI; angka final dicocokkan dengan laporan settlement Midtrans saat rekonsiliasi.
func EstimatePGFee(paymentType string, amount int64) int64 {
	var fee float64
	switch paymentType {
	case "bank_transfer", "echannel", "permata":
		fee = 4000
	case "qris":
		fee = float64(amount) * 0.007
	case "gopay", "shopeepay":
		fee = float64(amount) * 0.02
	case "credit_card":
		fee = float64(amount)*0.029 + 2000
	default:
		fee = 4000
	}
	return int64(math.Round(fee * 1.11))
}

// Post menyimpan jurnal. Idempotent: (ref_type, ref_id) unik, jadi pemanggilan ulang diabaikan.
func Post(ctx context.Context, tx pgx.Tx, j Journal) (bool, error) {
	if err := j.Validate(); err != nil {
		return false, err
	}
	var journalID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO ledger_journals (ref_type, ref_id, memo) VALUES ($1, $2, $3)
		ON CONFLICT (ref_type, ref_id) DO NOTHING RETURNING id`, j.RefType, j.RefID, j.Memo).Scan(&journalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range j.Entries {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (journal_id, org_id, account, debit, credit) VALUES ($1, $2, $3, $4, $5)`,
			journalID, e.OrgID, e.Account, e.Debit, e.Credit); err != nil {
			return false, err
		}
	}
	return true, nil
}
