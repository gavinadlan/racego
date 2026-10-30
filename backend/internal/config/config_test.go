package config

import (
	"strings"
	"testing"
)

func TestFinanceConfig(t *testing.T) {
	c := Load()
	if err := c.Validate(); err != nil || c.Finance.HoldDays != 3 || c.Finance.PayoutMin != 50000 || !c.DisburseMock() {
		t.Fatalf("default: %v %+v", err, c.Finance)
	}
	for k, v := range map[string]string{"PAYOUT_HOLD_DAYS": "tiga", "PAYOUT_MIN": "0", "DISBURSE_MODE": "bank", "DISBURSE_MOCK_DELAY": "5 detik"} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, v)
			if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), k) {
				t.Fatalf("%s=%s harus ditolak: %v", k, v, err)
			}
		})
	}
	t.Run("midtrans tanpa kunci", func(t *testing.T) {
		t.Setenv("DISBURSE_MODE", "midtrans")
		t.Setenv("MIDTRANS_SERVER_KEY", "SB-Mid-server-rahasia")
		t.Setenv("IRIS_CREATOR_KEY", "IRIS-creator-rahasia")
		err := Load().Validate()
		if err == nil || !strings.Contains(err.Error(), "IRIS_APPROVER_KEY") || strings.Contains(err.Error(), "rahasia") {
			t.Fatalf("harus ditolak tanpa memuat kunci: %v", err)
		}
		t.Setenv("IRIS_APPROVER_KEY", "IRIS-approver-rahasia")
		if err := Load().Validate(); err != nil {
			t.Fatal(err)
		}
	})
}
