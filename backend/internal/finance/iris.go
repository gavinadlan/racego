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
	"unicode/utf8"
)

// Iris adalah klien Midtrans Payouts (Iris).
//
// Endpoint & field dicocokkan dengan SDK resmi github.com/Midtrans/midtrans-go/iris
// (dokumentasi web Iris tidak bisa diakses saat ditulis):
//
//	POST {base}/payouts          kunci creator   {"payouts":[{beneficiary_name, beneficiary_account, beneficiary_bank, beneficiary_email, amount, notes}]}
//	                                              → {"payouts":[{"status","reference_no"}]} | {"error_message","errors"}
//	POST {base}/payouts/approve  kunci approver  {"reference_nos":[…],"otp":…} → {"status"} | {"error_message","errors"}
//	GET  {base}/payouts/{ref}    kunci creator   → {"status","reference_no","amount",…,"error_message"}
//
// ASUMSI yang belum terverifikasi dengan dokumentasi resmi (lihat docs/04):
//   - Basic auth = base64("<kunci>:") seperti Core API Midtrans.
//   - Header X-Idempotency-Key dihormati oleh POST /payouts (membalas reference_no yang sama untuk kunci sama).
//   - Persetujuan tanpa OTP berhasil untuk kunci approver API.
//   - Nilai status: queued | approved | processed | completed | failed | rejected.
//   - Kode bank Iris pada irisBanks (terutama "bsm" untuk BSI).
type Iris struct {
	base, creatorKey, approverKey string
	client                        *http.Client
}

func NewIris(base, creatorKey, approverKey string) *Iris {
	return &Iris{base: strings.TrimRight(base, "/"), creatorKey: creatorKey, approverKey: approverKey,
		client: &http.Client{Timeout: 15 * time.Second}}
}

// irisBanks: kode bank internal → kode beneficiary_bank Iris. Bank lain belum didukung otomatis.
var irisBanks = map[string]string{
	"bca": "bca", "mandiri": "mandiri", "bni": "bni", "bri": "bri", "bsi": "bsm",
	"cimb": "cimb", "permata": "permata", "danamon": "danamon",
}

type irisResp struct {
	Payouts []struct {
		Status      string `json:"status"`
		ReferenceNo string `json:"reference_no"`
	} `json:"payouts"`
	Status       string          `json:"status"`
	ReferenceNo  string          `json:"reference_no"`
	ErrorMessage string          `json:"error_message"`
	Errors       json.RawMessage `json:"errors"`
}

// call mengirim permintaan dan mengklasifikasikan kegagalan. Pesan error tidak pernah memuat kunci;
// secret (nomor rekening) disamarkan dari pesan gateway.
func (c *Iris) call(ctx context.Context, method, path, key, idemKey string, body any, secret string) (irisResp, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return irisResp{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(key+":")))
	if idemKey != "" {
		req.Header.Set("X-Idempotency-Key", idemKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return irisResp{}, fmt.Errorf("iris %s %s: %w", method, path, redactURLErr(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out irisResp
	jsonErr := json.Unmarshal(raw, &out)
	msg := clip(mask(out.ErrorMessage, secret), 200)
	switch {
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusConflict:
		return out, fmt.Errorf("iris %s %s: HTTP %d %s", method, path, resp.StatusCode, msg)
	case resp.StatusCode >= 400:
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return out, &PermanentError{Reason: msg}
	case jsonErr != nil:
		return out, fmt.Errorf("iris %s %s: respons tidak bisa dibaca", method, path)
	}
	return out, nil
}

func (c *Iris) Create(ctx context.Context, req DisburseRequest) (string, error) {
	bank, ok := irisBanks[req.BankCode]
	if !ok {
		return "", &PermanentError{Reason: "Bank tujuan belum didukung pencairan otomatis; kirim manual"}
	}
	body := map[string]any{"payouts": []map[string]string{{
		"beneficiary_name": req.AccountHolder, "beneficiary_account": req.AccountNumber, "beneficiary_bank": bank,
		"beneficiary_email": "", "amount": fmt.Sprintf("%d.00", req.Amount), "notes": clip(req.Notes, 100),
	}}}
	out, err := c.call(ctx, http.MethodPost, "/payouts", c.creatorKey, req.ID, body, req.AccountNumber)
	if err != nil {
		return "", err
	}
	if len(out.Payouts) != 1 || out.Payouts[0].ReferenceNo == "" {
		return "", fmt.Errorf("iris POST /payouts: respons tanpa reference_no")
	}
	ref := out.Payouts[0].ReferenceNo
	if _, err := c.call(ctx, http.MethodPost, "/payouts/approve", c.approverKey, "",
		map[string]any{"reference_nos": []string{ref}}, req.AccountNumber); err != nil {
		// Persetujuan bisa gagal karena sudah disetujui (percobaan ulang): cek status dulu.
		if st, serr := c.status(ctx, ref); serr == nil && st.raw != "queued" {
			return ref, nil
		}
		if reason, perm := isPermanent(err); perm {
			return "", &PermanentError{Reason: "persetujuan ditolak: " + reason}
		}
		return "", err
	}
	return ref, nil
}

type irisStatus struct {
	DisburseStatus
	raw string
}

func (c *Iris) Status(ctx context.Context, ref string) (DisburseStatus, error) {
	st, err := c.status(ctx, ref)
	return st.DisburseStatus, err
}

func (c *Iris) status(ctx context.Context, ref string) (irisStatus, error) {
	out, err := c.call(ctx, http.MethodGet, "/payouts/"+url.PathEscape(ref), c.creatorKey, "", nil, "")
	if err != nil {
		return irisStatus{}, err
	}
	st := irisStatus{raw: out.Status}
	switch out.Status {
	case "completed":
		st.State = DisburseCompleted
	case "failed", "rejected":
		st.State, st.Reason = DisburseFailed, clip(out.ErrorMessage, 200)
		if st.Reason == "" {
			st.Reason = "Transfer " + out.Status + " oleh Midtrans"
		}
	case "queued", "approved", "processed":
		st.State = DisbursePending
	default:
		return st, fmt.Errorf("iris: status tidak dikenal %q", clip(out.Status, 30))
	}
	return st, nil
}

func mask(s, secret string) string {
	if secret == "" || len(secret) < 4 {
		return s
	}
	return strings.ReplaceAll(s, secret, "••••"+secret[len(secret)-4:])
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// redactURLErr: *url.Error memuat URL saja (tanpa header), jadi kunci tidak ikut; tetap dibuang
// supaya pesan pendek.
func redactURLErr(err error) error {
	if ue, ok := err.(*url.Error); ok {
		if ue.Timeout() {
			return fmt.Errorf("timeout")
		}
		return ue.Err
	}
	return err
}
