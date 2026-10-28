package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gavinadlan/racego/backend/internal/config"
)

// Rute simulasi pembayaran hanya ada di development atau mode mock (tanpa kunci Midtrans).
func TestSimulateRouteOnlyWithoutMidtrans(t *testing.T) {
	for _, tc := range []struct {
		env, serverKey string
		want           int
	}{
		{"development", "", http.StatusUnauthorized},
		{"production", "", http.StatusUnauthorized}, // demo tanpa Midtrans: rute ada, butuh login
		{"production", "SB-Mid-server-xxx", http.StatusNotFound},
	} {
		cfg := config.Config{Env: tc.env, FrontendURL: "https://racego.test", MidtransServerKey: tc.serverKey}
		h := NewRouter(Deps{Cfg: cfg})
		req := httptest.NewRequest(http.MethodPost, "/v1/dev/orders/x/simulate", nil)
		req.Header.Set("Origin", cfg.FrontendURL)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("env=%s key=%q: status %d, mau %d", tc.env, tc.serverKey, rec.Code, tc.want)
		}
	}
}
