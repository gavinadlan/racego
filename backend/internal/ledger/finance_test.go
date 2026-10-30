package ledger

import (
	"testing"

	"github.com/google/uuid"
)

func sumAcc(j Journal, acc string) (d, c int64) {
	for _, e := range j.Entries {
		if e.Account == acc {
			d += e.Debit
			c += e.Credit
		}
	}
	return
}

func TestFinanceJournalsBalancedAndRejectInvalid(t *testing.T) {
	id, org := uuid.New(), uuid.New()
	ctors := map[string]func(uuid.UUID, uuid.UUID, int64) (Journal, error){
		"balance.release": BalanceRelease, "payout.request": PayoutRequest, "payout.reject": PayoutReject,
		"payout.fail": PayoutFail, "payout.complete": PayoutComplete,
	}
	for name, f := range ctors {
		j, err := f(id, org, 100000)
		if err != nil || j.RefType != name || j.RefID != id.String() || j.Validate() != nil {
			t.Fatalf("%s: %v %+v", name, err, j)
		}
		for _, bad := range []int64{0, -1} {
			if _, err := f(id, org, bad); err == nil {
				t.Fatalf("%s menerima jumlah %d", name, bad)
			}
		}
	}
	// Arah uang: request memindahkan tersedia → dalam proses; complete mengurangi kas.
	j, _ := PayoutRequest(id, org, 5)
	if d, _ := sumAcc(j, AccPartnerAvailable); d != 5 {
		t.Fatal("payout.request harus mendebit saldo tersedia")
	}
	j, _ = PayoutComplete(id, org, 5)
	if _, c := sumAcc(j, AccMidtransCash); c != 5 {
		t.Fatal("payout.complete harus mengkredit kas")
	}
}

// Contoh PRD Rp350.000 via VA: bagian mitra Rp335.060, fee 10.500, biaya PG 4.440.
func prdPaid(t *testing.T, org uuid.UUID) (uuid.UUID, Paid) {
	order := uuid.New()
	j, err := OrderPaid(order, org, 350000, PlatformFee(350000, 300), EstimatePGFee("bank_transfer", 350000))
	if err != nil {
		t.Fatal(err)
	}
	var p Paid
	p.Cash, _ = sumAcc(j, AccMidtransCash)
	_, p.PlatformFee = sumAcc(j, AccPlatformRevenue)
	_, p.Partner = sumAcc(j, AccPartnerHeld)
	if p.Partner != 335060 || p.Cash != 345560 || p.PlatformFee != 10500 {
		t.Fatalf("order.paid: %+v", p)
	}
	return order, p
}

func TestOrderRefundBeforeAndAfterRelease(t *testing.T) {
	org := uuid.New()
	order, paid := prdPaid(t, org)
	for _, released := range []bool{false, true} {
		j, err := OrderRefund(order, org, 350000, paid, released)
		if err != nil {
			t.Fatal(err)
		}
		_, kas := sumAcc(j, AccMidtransCash)
		held, _ := sumAcc(j, AccPartnerHeld)
		avail, _ := sumAcc(j, AccPartnerAvailable)
		fee, _ := sumAcc(j, AccPlatformRevenue)
		pg, _ := sumAcc(j, AccRefundPGExpense)
		if kas != 350000 || fee != 10500 || pg != 4440 {
			t.Fatalf("released=%v: kas %d fee %d pg %d", released, kas, fee, pg)
		}
		if (!released && (held != 335060 || avail != 0)) || (released && (avail != 335060 || held != 0)) {
			t.Fatalf("released=%v: tertahan %d tersedia %d", released, held, avail)
		}
		if j.RefType != "order.refund" || j.RefID != order.String() {
			t.Fatalf("ref: %s %s", j.RefType, j.RefID)
		}
	}
	// Saldo menjadi negatif setelah rilis + pencairan: refund tetap membukukan debit penuh.
	avail := int64(335060)
	payout, _ := PayoutRequest(uuid.New(), org, 300000)
	d, _ := sumAcc(payout, AccPartnerAvailable)
	j, _ := OrderRefund(order, org, 350000, paid, true)
	rd, _ := sumAcc(j, AccPartnerAvailable)
	if got := avail - d - rd; got != -300000 {
		t.Fatalf("saldo tersedia = %d, mau -300000 (utang mitra)", got)
	}
	if _, err := OrderRefund(order, org, 0, paid, false); err == nil {
		t.Fatal("refund 0 harus ditolak")
	}
	if _, err := OrderRefund(order, org, 1000, Paid{Cash: 2000}, false); err == nil {
		t.Fatal("kas > total harus ditolak")
	}
}
