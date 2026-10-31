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

func TestTurnstileConfigPairs(t *testing.T) {
	if c := Load(); c.Turnstile.Enabled() || c.Validate() != nil {
		t.Fatal("tanpa kunci: fitur mati dan konfigurasi sah")
	}
	for _, tc := range []struct{ site, secret string }{{"1x00000000000000000000AA", ""}, {"", "rahasia-turnstile-uji"}} {
		t.Setenv("TURNSTILE_SITE_KEY", tc.site)
		t.Setenv("TURNSTILE_SECRET_KEY", tc.secret)
		err := Load().Validate()
		if err == nil || !strings.Contains(err.Error(), "berpasangan") || strings.Contains(err.Error(), "rahasia-turnstile-uji") {
			t.Fatalf("site=%q secret terisi=%v harus ditolak tanpa memuat kunci: %v", tc.site, tc.secret != "", err)
		}
	}
	t.Setenv("TURNSTILE_SITE_KEY", "1x00000000000000000000AA")
	t.Setenv("TURNSTILE_SECRET_KEY", "1x0000000000000000000000000000000AA")
	if c := Load(); !c.Turnstile.Enabled() || c.Validate() != nil {
		t.Fatal("dua kunci: fitur hidup")
	}
}
