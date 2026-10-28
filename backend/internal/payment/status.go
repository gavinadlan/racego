package payment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Reconciler menanyakan status transaksi langsung ke Midtrans (GET /v2/{order_id}/status)
// lalu memprosesnya lewat jalur yang sama dengan webhook.
//
// Gunanya:
//   - development di laptop: Midtrans tidak bisa mengirim webhook ke localhost;
//   - produksi: cadangan bila webhook terlambat atau gagal terkirim.
//
// Aman dipanggil berulang: Processor idempotent (unik per order+status, status hanya maju).
type Reconciler struct {
	proc      *Processor
	serverKey string
	baseURL   string
	client    *http.Client

	mu        sync.Mutex
	lastCheck map[string]time.Time
}

// MinInterval: jarak minimal antar-pengecekan untuk order yang sama (menahan polling berlebihan).
const MinInterval = 4 * time.Second

func NewReconciler(proc *Processor, serverKey string, production bool) *Reconciler {
	base := "https://api.sandbox.midtrans.com"
	if production {
		base = "https://api.midtrans.com"
	}
	return &Reconciler{
		proc: proc, serverKey: serverKey, baseURL: base,
		client:    &http.Client{Timeout: 8 * time.Second},
		lastCheck: map[string]time.Time{},
	}
}

var errNotFound = errors.New("transaksi belum ada di Midtrans")

// Refresh mengecek satu order bila belum dicek dalam MinInterval terakhir.
// Mengembalikan true bila status order berubah.
func (r *Reconciler) Refresh(ctx context.Context, orderID string) (bool, error) {
	if !r.allow(orderID) {
		return false, nil
	}
	outcome, err := r.Check(ctx, orderID)
	if err != nil {
		return false, err
	}
	return outcome == OutcomePaid || outcome == OutcomeReleased, nil
}

// Check selalu bertanya ke Midtrans (tanpa throttle). Dipakai worker.
func (r *Reconciler) Check(ctx context.Context, orderID string) (Outcome, error) {
	n, raw, err := r.fetch(ctx, orderID)
	if errors.Is(err, errNotFound) {
		return OutcomeIgnored, nil // pembeli belum memilih metode bayar di popup Snap
	}
	if err != nil {
		return "", err
	}
	outcome, err := r.proc.processVerified(ctx, n, raw)
	if err == nil && (outcome == OutcomePaid || outcome == OutcomeReleased) {
		slog.InfoContext(ctx, "status midtrans (cek langsung)", "order_id", orderID, "status", n.TransactionStatus, "outcome", outcome)
	}
	return outcome, err
}

func (r *Reconciler) allow(orderID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if t, ok := r.lastCheck[orderID]; ok && now.Sub(t) < MinInterval {
		return false
	}
	r.lastCheck[orderID] = now
	if len(r.lastCheck) > 10_000 { // cegah map tumbuh tanpa batas
		for k, t := range r.lastCheck {
			if now.Sub(t) > time.Minute {
				delete(r.lastCheck, k)
			}
		}
	}
	return true
}

func (r *Reconciler) fetch(ctx context.Context, orderID string) (Notification, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/v2/"+url.PathEscape(orderID)+"/status", nil)
	if err != nil {
		return Notification{}, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(r.serverKey+":")))
	resp, err := r.client.Do(req)
	if err != nil {
		return Notification{}, nil, fmt.Errorf("status midtrans: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusNotFound {
		return Notification{}, nil, errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return Notification{}, nil, fmt.Errorf("status midtrans %d: %s", resp.StatusCode, raw)
	}
	var n Notification
	if err := json.Unmarshal(raw, &n); err != nil {
		return Notification{}, nil, fmt.Errorf("status midtrans: %w", err)
	}
	// Midtrans kadang membalas HTTP 200 dengan status_code di body.
	switch n.StatusCode {
	case "404":
		return Notification{}, nil, errNotFound
	case "200", "201", "202", "407":
	default:
		return Notification{}, nil, fmt.Errorf("status midtrans %s: %s", n.StatusCode, raw)
	}
	if n.OrderID != orderID {
		return Notification{}, nil, fmt.Errorf("status midtrans: order_id %q tidak cocok", n.OrderID)
	}
	return n, raw, nil
}
