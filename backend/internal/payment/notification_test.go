package payment

import "testing"

func signed(n Notification, key string) Notification {
	n.SignatureKey = Signature(n.OrderID, n.StatusCode, n.GrossAmount, key)
	return n
}

func TestVerify(t *testing.T) {
	n := signed(Notification{OrderID: "ord-1", StatusCode: "200", GrossAmount: "350000.00"}, "SB-Mid-server-abc")
	if !n.Verify("SB-Mid-server-abc") {
		t.Fatal("signature valid seharusnya lolos")
	}
	if n.Verify("kunci-lain") {
		t.Fatal("server key berbeda seharusnya gagal")
	}
	tampered := n
	tampered.GrossAmount = "1.00" // penyerang menurunkan nominal
	if tampered.Verify("SB-Mid-server-abc") {
		t.Fatal("nominal yang diubah seharusnya gagal")
	}
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		status, fraud string
		paid, failed  bool
	}{
		{"settlement", "", true, false},
		{"capture", "accept", true, false},
		{"capture", "challenge", false, false},
		{"pending", "", false, false},
		{"expire", "", false, true},
		{"cancel", "", false, true},
		{"deny", "", false, true},
	}
	for _, c := range cases {
		n := Notification{TransactionStatus: c.status, FraudStatus: c.fraud}
		if n.IsPaid() != c.paid || n.IsFailed() != c.failed {
			t.Errorf("%s/%s: paid=%v failed=%v", c.status, c.fraud, n.IsPaid(), n.IsFailed())
		}
	}
}

func TestAmount(t *testing.T) {
	n := Notification{GrossAmount: FormatAmount(350000)}
	if got, err := n.AmountRupiah(); err != nil || got != 350000 {
		t.Fatalf("AmountRupiah = %d, %v", got, err)
	}
	if _, err := (Notification{GrossAmount: "abc"}).AmountRupiah(); err == nil {
		t.Fatal("nominal tidak valid seharusnya error")
	}
}
