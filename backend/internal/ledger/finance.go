package ledger

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Jurnal keuangan mitra. Setiap kejadian punya (ref_type, ref_id) deterministik, jadi pengulangan
// (worker jalan dua kali, klik ganda, retry) tidak pernah menggandakan uang. Lihat docs/04.

func positive(amount int64, what string) error {
	if amount <= 0 {
		return fmt.Errorf("jumlah %s tidak valid: %d", what, amount)
	}
	return nil
}

func transfer(refType, refID, memo string, debit, credit Entry, amount int64) (Journal, error) {
	if err := positive(amount, refType); err != nil {
		return Journal{}, err
	}
	debit.Debit, credit.Credit = amount, amount
	j := Journal{RefType: refType, RefID: refID, Memo: memo, Entries: []Entry{debit, credit}}
	return j, j.Validate()
}

// BalanceRelease: hak mitra dari satu order pindah tertahan → tersedia setelah masa tahan.
func BalanceRelease(orderID, orgID uuid.UUID, amount int64) (Journal, error) {
	return transfer("balance.release", orderID.String(), fmt.Sprintf("Dana order %s dirilis", orderID),
		Entry{Account: AccPartnerHeld, OrgID: &orgID}, Entry{Account: AccPartnerAvailable, OrgID: &orgID}, amount)
}

// PayoutRequest: saldo tersedia dipesan untuk pencairan.
func PayoutRequest(payoutID, orgID uuid.UUID, amount int64) (Journal, error) {
	return transfer("payout.request", payoutID.String(), "Pencairan diajukan",
		Entry{Account: AccPartnerAvailable, OrgID: &orgID}, Entry{Account: AccPayoutInProgress, OrgID: &orgID}, amount)
}

// PayoutReject: pengajuan ditolak super admin, saldo kembali tersedia.
func PayoutReject(payoutID, orgID uuid.UUID, amount int64) (Journal, error) {
	return transfer("payout.reject", payoutID.String(), "Pencairan ditolak",
		Entry{Account: AccPayoutInProgress, OrgID: &orgID}, Entry{Account: AccPartnerAvailable, OrgID: &orgID}, amount)
}

// PayoutFail: transfer gagal, saldo kembali tersedia.
func PayoutFail(payoutID, orgID uuid.UUID, amount int64) (Journal, error) {
	return transfer("payout.fail", payoutID.String(), "Pencairan gagal",
		Entry{Account: AccPayoutInProgress, OrgID: &orgID}, Entry{Account: AccPartnerAvailable, OrgID: &orgID}, amount)
}

// PayoutComplete: uang keluar dari kas ke rekening mitra.
func PayoutComplete(payoutID, orgID uuid.UUID, amount int64) (Journal, error) {
	return transfer("payout.complete", payoutID.String(), "Pencairan selesai",
		Entry{Account: AccPayoutInProgress, OrgID: &orgID}, Entry{Account: AccMidtransCash}, amount)
}

// Paid adalah angka dari jurnal order.paid asli yang dipakai membalik order (jangan dihitung ulang).
type Paid struct {
	Cash        int64 // debit kas_midtrans (total − biaya PG estimasi)
	PlatformFee int64 // kredit pendapatan_platform
	Partner     int64 // kredit saldo_mitra_tertahan
}

// OrderRefund membalik satu order penuh. Pembeli menerima total; bagian mitra ditarik dari saldo
// tersedia (bila sudah dirilis) atau tertahan, fee platform dikembalikan, dan biaya PG yang tidak
// kembali (total − kas yang diterima) menjadi beban platform:
//
//	Kredit kas_midtrans                     total
//	Debit  saldo_mitra_tersedia/tertahan    bagian mitra   (boleh membuat saldo negatif)
//	Debit  pendapatan_platform              fee platform
//	Debit  beban_refund_pg                  total − kas
func OrderRefund(orderID, orgID uuid.UUID, total int64, paid Paid, released bool) (Journal, error) {
	if err := positive(total, "refund"); err != nil {
		return Journal{}, err
	}
	if paid.Cash < 0 || paid.PlatformFee < 0 || paid.Partner < 0 || paid.Cash > total {
		return Journal{}, fmt.Errorf("angka order.paid tidak valid untuk order %s", orderID)
	}
	partnerAcc := AccPartnerHeld
	if released {
		partnerAcc = AccPartnerAvailable
	}
	j := Journal{RefType: "order.refund", RefID: orderID.String(), Memo: fmt.Sprintf("Refund order %s", orderID)}
	add := func(e Entry) {
		if e.Debit > 0 || e.Credit > 0 {
			j.Entries = append(j.Entries, e)
		}
	}
	add(Entry{Account: AccMidtransCash, Credit: total})
	add(Entry{Account: partnerAcc, OrgID: &orgID, Debit: paid.Partner})
	add(Entry{Account: AccPlatformRevenue, Debit: paid.PlatformFee})
	add(Entry{Account: AccRefundPGExpense, Debit: total - paid.Cash})
	return j, j.Validate()
}

// Querier dipenuhi *pgxpool.Pool dan pgx.Tx.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// PaidOf membaca angka jurnal order.paid sebuah order. ok=false bila jurnalnya belum ada.
func PaidOf(ctx context.Context, q Querier, orderID uuid.UUID) (p Paid, ok bool, err error) {
	var n int
	err = q.QueryRow(ctx, `
		SELECT count(DISTINCT j.id),
		       coalesce(sum(e.debit) FILTER (WHERE e.account = $2), 0),
		       coalesce(sum(e.credit) FILTER (WHERE e.account = $3), 0),
		       coalesce(sum(e.credit) FILTER (WHERE e.account = $4), 0)
		FROM ledger_journals j JOIN ledger_entries e ON e.journal_id = j.id
		WHERE j.ref_type = 'order.paid' AND j.ref_id = $1`,
		orderID.String(), AccMidtransCash, AccPlatformRevenue, AccPartnerHeld).Scan(&n, &p.Cash, &p.PlatformFee, &p.Partner)
	return p, n > 0, err
}

// Balances: saldo mitra, selalu dihitung dari ledger_entries (tidak ada kolom saldo terpisah).
type Balances struct {
	Held      int64 `json:"held"`       // saldo_mitra_tertahan
	Available int64 `json:"available"`  // saldo_mitra_tersedia (bisa negatif = utang refund)
	InProcess int64 `json:"in_process"` // payout_dalam_proses
	PaidOut   int64 `json:"paid_out"`   // total pencairan selesai
}

// OrgBalances menghitung saldo satu org. Akun mitra bersaldo normal kredit (kredit − debit).
func OrgBalances(ctx context.Context, q Querier, orgID uuid.UUID) (Balances, error) {
	var b Balances
	err := q.QueryRow(ctx, `
		SELECT coalesce(sum(e.credit - e.debit) FILTER (WHERE e.account = $2), 0),
		       coalesce(sum(e.credit - e.debit) FILTER (WHERE e.account = $3), 0),
		       coalesce(sum(e.credit - e.debit) FILTER (WHERE e.account = $4), 0),
		       coalesce(sum(e.debit) FILTER (WHERE e.account = $4 AND j.ref_type = 'payout.complete'), 0)
		FROM ledger_entries e JOIN ledger_journals j ON j.id = e.journal_id
		WHERE e.org_id = $1`, orgID, AccPartnerHeld, AccPartnerAvailable, AccPayoutInProgress).
		Scan(&b.Held, &b.Available, &b.InProcess, &b.PaidOut)
	return b, err
}

// Totals: saldo tiap akun seluruh platform, bertanda debit − kredit (aset/beban positif,
// kewajiban/pendapatan negatif). Jumlah semua akun selalu 0 bila ledger seimbang.
func Totals(ctx context.Context, q Querier) (map[string]int64, error) {
	rows, err := q.Query(ctx, `SELECT account, sum(debit - credit)::bigint FROM ledger_entries GROUP BY account ORDER BY account`)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	var acc string
	var v int64
	_, err = pgx.ForEachRow(rows, []any{&acc, &v}, func() error { out[acc] = v; return nil })
	return out, err
}

type CheckItem struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type CheckReport struct {
	OK     bool        `json:"ok"`
	Checks []CheckItem `json:"checks"`
}

// Check memeriksa integritas ledger: seimbang global & per jurnal, tidak ada saldo
// payout_dalam_proses negatif, dan saldo itu sama dengan total pencairan yang masih berjalan.
func Check(ctx context.Context, q Querier) (CheckReport, error) {
	var r CheckReport
	add := func(name string, ok bool, detail string) {
		r.Checks = append(r.Checks, CheckItem{name, ok, detail})
	}
	var d, c int64
	if err := q.QueryRow(ctx, `SELECT coalesce(sum(debit), 0)::bigint, coalesce(sum(credit), 0)::bigint FROM ledger_entries`).Scan(&d, &c); err != nil {
		return r, err
	}
	add("Total debit = total kredit", d == c, fmt.Sprintf("debit %d, kredit %d", d, c))

	var n int
	var sample string
	if err := q.QueryRow(ctx, `
		SELECT count(*), coalesce(min(ref), '') FROM (
		  SELECT j.ref_type || ':' || j.ref_id AS ref FROM ledger_journals j LEFT JOIN ledger_entries e ON e.journal_id = j.id
		  GROUP BY j.id HAVING coalesce(sum(e.debit), 0) <> coalesce(sum(e.credit), 0) OR coalesce(sum(e.debit), 0) = 0) x`).Scan(&n, &sample); err != nil {
		return r, err
	}
	detail := "semua jurnal seimbang"
	if n > 0 {
		detail = fmt.Sprintf("%d jurnal tidak seimbang, mis. %s", n, sample)
	}
	add("Setiap jurnal seimbang", n == 0, detail)

	if err := q.QueryRow(ctx, `
		SELECT count(*) FROM (SELECT org_id FROM ledger_entries WHERE account = $1
		GROUP BY org_id HAVING sum(credit - debit) < 0) x`, AccPayoutInProgress).Scan(&n); err != nil {
		return r, err
	}
	add("Tidak ada saldo payout_dalam_proses negatif", n == 0, fmt.Sprintf("%d org negatif", n))

	var inProc, active int64
	if err := q.QueryRow(ctx, `SELECT coalesce(sum(credit - debit), 0)::bigint FROM ledger_entries WHERE account = $1`, AccPayoutInProgress).Scan(&inProc); err != nil {
		return r, err
	}
	if err := q.QueryRow(ctx, `SELECT coalesce(sum(amount), 0)::bigint FROM payouts WHERE status IN ('requested', 'approved', 'processing')`).Scan(&active); err != nil {
		return r, err
	}
	if err := q.QueryRow(ctx, `
		SELECT count(*) FROM (
		  SELECT coalesce(p.org_id, l.org_id) FROM
		    (SELECT org_id, sum(amount) AS v FROM payouts WHERE status IN ('requested', 'approved', 'processing') GROUP BY org_id) p
		    FULL JOIN (SELECT org_id, sum(credit - debit) AS v FROM ledger_entries WHERE account = $1 GROUP BY org_id) l ON l.org_id = p.org_id
		  WHERE coalesce(p.v, 0) <> coalesce(l.v, 0)) x`, AccPayoutInProgress).Scan(&n); err != nil {
		return r, err
	}
	add("Pencairan aktif = saldo payout_dalam_proses", inProc == active && n == 0,
		fmt.Sprintf("pencairan aktif %d, saldo payout_dalam_proses %d, org berbeda %d", active, inProc, n))

	r.OK = true
	for _, it := range r.Checks {
		r.OK = r.OK && it.OK
	}
	return r, nil
}
