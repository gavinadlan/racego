package ledger

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Contoh di PRD: tiket half marathon Rp350.000 via VA, fee platform 3%.
func TestOrderPaid_PRDExample(t *testing.T) {
	orgID := uuid.New()
	total := int64(350000)
	pg := EstimatePGFee("bank_transfer", total)
	fee := PlatformFee(total, 300)
	if pg != 4440 || fee != 10500 {
		t.Fatalf("biaya PG = %d (harus 4440), fee = %d (harus 10500)", pg, fee)
	}
	j, err := OrderPaid(uuid.New(), orgID, total, fee, pg)
	if err != nil {
		t.Fatal(err)
	}
	var partner int64
	for _, e := range j.Entries {
		if e.Account == AccPartnerHeld {
			partner = e.Credit
			if e.OrgID == nil || *e.OrgID != orgID {
				t.Fatal("hak mitra harus tercatat dengan org_id")
			}
		}
	}
	if partner != 335060 {
		t.Fatalf("hak mitra = %d, seharusnya 335060", partner)
	}
}

func TestValidate(t *testing.T) {
	unbalanced := Journal{Entries: []Entry{{Account: AccMidtransCash, Debit: 100}, {Account: AccPlatformRevenue, Credit: 90}}}
	if err := unbalanced.Validate(); !errors.Is(err, ErrUnbalanced) {
		t.Fatalf("seharusnya ErrUnbalanced, dapat %v", err)
	}
	bothSides := Journal{Entries: []Entry{{Account: "x", Debit: 10, Credit: 10}}}
	if err := bothSides.Validate(); err == nil {
		t.Fatal("baris dengan debit & kredit sekaligus seharusnya ditolak")
	}
	if _, err := OrderPaid(uuid.New(), uuid.New(), 1000, 900, 500); err == nil {
		t.Fatal("fee melebihi total seharusnya error")
	}
}
