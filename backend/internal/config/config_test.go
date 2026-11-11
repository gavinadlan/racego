package config

import (
	"strings"
	"testing"
	"time"
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

func TestScanManifestTTL(t *testing.T) {
	if c := Load(); c.ScanManifestTTL != 24*time.Hour || c.Validate() != nil {
		t.Fatalf("default: %v", c.ScanManifestTTL)
	}
	t.Setenv("SCAN_MANIFEST_TTL_HOURS", "6")
	if c := Load(); c.ScanManifestTTL != 6*time.Hour {
		t.Fatalf("6 jam: %v", c.ScanManifestTTL)
	}
	t.Setenv("SCAN_MANIFEST_TTL_HOURS", "0")
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "SCAN_MANIFEST_TTL_HOURS") {
		t.Fatalf("0 harus ditolak: %v", err)
	}
}

func TestMetricsAddrNonLoopbackNeedsToken(t *testing.T) {
	t.Setenv("METRICS_ADDR", "0.0.0.0:9464")
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "METRICS_TOKEN") {
		t.Fatalf("METRICS_ADDR non-loopback tanpa token harus menggagalkan start, dapat: %v", err)
	}
	t.Setenv("METRICS_TOKEN", "rahasia")
	if err := Load().Validate(); err != nil {
		t.Fatalf("dengan token harus sah: %v", err)
	}
	t.Setenv("METRICS_TOKEN", "")
	t.Setenv("METRICS_ADDR", "127.0.0.1:9464")
	t.Setenv("WORKER_METRICS_ADDR", ":9465")
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "WORKER_METRICS_ADDR") {
		t.Fatalf("WORKER_METRICS_ADDR non-loopback tanpa token harus ditolak, dapat: %v", err)
	}
	t.Setenv("WORKER_METRICS_ADDR", "")
	if c := Load(); c.Validate() != nil || c.MetricsAddr != "127.0.0.1:9464" {
		t.Fatalf("loopback tanpa token harus sah")
	}
}
