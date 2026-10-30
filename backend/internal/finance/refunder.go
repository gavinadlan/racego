package finance

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type RefundRequest struct {
	Key     string // refunds.id: refund_key idempoten ke gateway
	OrderID string // order_id yang dikirim ke Midtrans saat checkout
	Amount  int64
	Reason  string
}

type RefundState string

const (
	RefundCompleted RefundState = "completed"
	RefundManual    RefundState = "manual_required" // metode bayar tidak bisa di-refund lewat API
)

type RefundResult struct {
	State     RefundState
	Reference string // id refund gateway
	Reason    string // alasan bila manual_required
}

// Refunder mengembalikan uang ke pembeli. Error = kegagalan sementara (diulang dengan backoff).
type Refunder interface {
	Refund(ctx context.Context, req RefundRequest) (RefundResult, error)
}

// MockManualMarker: di mode mock, refund yang alasannya memuat kata ini (tanpa beda huruf besar/kecil)
// menjadi manual_required, meniru metode bayar yang tidak bisa di-refund lewat API (mis. transfer VA).
const MockManualMarker = "manual"

// MockRefunder: tanpa panggilan jaringan; langsung selesai kecuali alasan memuat MockManualMarker.
type MockRefunder struct{}

func (MockRefunder) Refund(_ context.Context, req RefundRequest) (RefundResult, error) {
	if strings.Contains(strings.ToLower(req.Reason), MockManualMarker) {
		return RefundResult{State: RefundManual, Reason: "Simulasi: metode bayar tidak mendukung refund lewat API (mode mock)"}, nil
	}
	return RefundResult{State: RefundCompleted, Reference: "mock-refund-" + req.Key}, nil
}

// MidtransRefunder memakai Core API: POST {base}/v2/{order_id}/refund (Basic auth server key),
// body {refund_key, amount, reason}. Dicocokkan dengan https://docs.midtrans.com/reference/refund-transaction:
// status_code "200" = berhasil; 412 (status transaksi tidak bisa diubah, mis. transfer VA), 414
// (jumlah tidak valid), 406 (refund_key duplikat) dan 4xx lain → manual_required; 5xx/timeout → diulang.
type MidtransRefunder struct {
	base, serverKey string
	client          *http.Client
}

func NewMidtransRefunder(base, serverKey string) *MidtransRefunder {
	return &MidtransRefunder{base: strings.TrimRight(base, "/"), serverKey: serverKey, client: &http.Client{Timeout: 15 * time.Second}}
}

func (m *MidtransRefunder) Refund(ctx context.Context, req RefundRequest) (RefundResult, error) {
	body, _ := json.Marshal(map[string]any{"refund_key": req.Key, "amount": req.Amount, "reason": clip(req.Reason, 250)})
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.base+"/v2/"+url.PathEscape(req.OrderID)+"/refund", bytes.NewReader(body))
	if err != nil {
		return RefundResult{}, err
	}
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(m.serverKey+":")))
	resp, err := m.client.Do(hreq)
	if err != nil {
		return RefundResult{}, fmt.Errorf("refund midtrans: %w", redactURLErr(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return RefundResult{}, fmt.Errorf("refund midtrans: HTTP %d", resp.StatusCode)
	}
	var out struct {
		StatusCode         string `json:"status_code"`
		StatusMessage      string `json:"status_message"`
		RefundChargebackID any    `json:"refund_chargeback_id"`
		RefundKey          string `json:"refund_key"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return RefundResult{}, fmt.Errorf("refund midtrans: respons tidak bisa dibaca")
	}
	switch {
	case out.StatusCode == "200":
		ref := req.Key
		if out.RefundChargebackID != nil {
			ref = fmt.Sprint(out.RefundChargebackID)
		}
		return RefundResult{State: RefundCompleted, Reference: ref}, nil
	case strings.HasPrefix(out.StatusCode, "5"):
		return RefundResult{}, fmt.Errorf("refund midtrans: status_code %s", out.StatusCode)
	case strings.HasPrefix(out.StatusCode, "4") || resp.StatusCode >= 400:
		reason := clip(out.StatusMessage, 200)
		if reason == "" {
			reason = "Ditolak Midtrans (HTTP " + fmt.Sprint(resp.StatusCode) + ")"
		}
		return RefundResult{State: RefundManual, Reason: out.StatusCode + " " + reason}, nil
	default:
		return RefundResult{}, fmt.Errorf("refund midtrans: status_code tidak dikenal %q", clip(out.StatusCode, 10))
	}
}
