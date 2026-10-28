package payment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type SnapItem struct {
	ID       string `json:"id"`
	Price    int64  `json:"price"`
	Quantity int    `json:"quantity"`
	Name     string `json:"name"` // maks. 50 karakter
}

type SnapRequest struct {
	OrderID       string
	GrossAmount   int64
	Items         []SnapItem
	CustomerName  string
	CustomerEmail string
	CustomerPhone string
	ExpiryMinutes int
	FinishURL     string
}

type SnapResult struct {
	Token       string `json:"token"`
	RedirectURL string `json:"redirect_url"`
	Mock        bool   `json:"mock"`
}

// SnapClient membuat transaksi Snap. Ada dua implementasi: HTTP (sandbox/produksi) dan mock.
type SnapClient interface {
	CreateTransaction(ctx context.Context, req SnapRequest) (SnapResult, error)
}

// ===== Implementasi HTTP ke Midtrans =====

type httpSnap struct {
	serverKey string
	baseURL   string
	client    *http.Client
}

func NewSnapClient(serverKey string, production bool) SnapClient {
	base := "https://app.sandbox.midtrans.com"
	if production {
		base = "https://app.midtrans.com"
	}
	return &httpSnap{serverKey: serverKey, baseURL: base, client: &http.Client{Timeout: 10 * time.Second}}
}

func (s *httpSnap) CreateTransaction(ctx context.Context, req SnapRequest) (SnapResult, error) {
	body := map[string]any{
		"transaction_details": map[string]any{"order_id": req.OrderID, "gross_amount": req.GrossAmount},
		"item_details":        req.Items,
		"customer_details": map[string]any{
			"first_name": req.CustomerName, "email": req.CustomerEmail, "phone": req.CustomerPhone,
		},
		"expiry":    map[string]any{"unit": "minutes", "duration": req.ExpiryMinutes},
		"callbacks": map[string]any{"finish": req.FinishURL},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return SnapResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/snap/v1/transactions", bytes.NewReader(buf))
	if err != nil {
		return SnapResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(s.serverKey+":")))

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return SnapResult{}, fmt.Errorf("snap request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return SnapResult{}, fmt.Errorf("snap status %d: %s", resp.StatusCode, raw)
	}
	var out SnapResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return SnapResult{}, fmt.Errorf("snap response: %w", err)
	}
	return out, nil
}

// ===== Mock: tanpa Midtrans, untuk development & uji beban =====

type mockSnap struct{ frontendURL string }

// NewMockSnap mengembalikan token palsu; pembayaran disimulasikan lewat endpoint /v1/dev/....
func NewMockSnap(frontendURL string) SnapClient { return mockSnap{frontendURL: frontendURL} }

func (m mockSnap) CreateTransaction(_ context.Context, req SnapRequest) (SnapResult, error) {
	return SnapResult{
		Token:       "mock-" + req.OrderID,
		RedirectURL: m.frontendURL + "/orders/" + url.PathEscape(req.OrderID),
		Mock:        true,
	}, nil
}
