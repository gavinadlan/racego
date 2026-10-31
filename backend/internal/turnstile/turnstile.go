// Package turnstile memverifikasi token anti-bot Cloudflare Turnstile di server (siteverify).
//
// Dipasang hanya di permintaan kode OTP dan masuk antrean, di BELAKANG pembatas laju yang sudah ada.
// Tujuannya menyulitkan skrip yang membanjiri permintaan; tidak menghentikan bot canggih atau multi-akun.
//
// Perilaku mengikuti https://developers.cloudflare.com/turnstile/get-started/server-side-validation/
// (dicek 2026-11-01): POST form secret, response, remoteip (opsional); respons JSON {success, error-codes};
// token maks. 2048 karakter, berlaku 300 detik, sekali pakai (dipakai ulang → timeout-or-duplicate).
package turnstile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gavinadlan/racego/backend/internal/httpx"
)

// DefaultURL: endpoint siteverify resmi Cloudflare.
const DefaultURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

const maxTokenLen = 2048

var (
	// ErrFailed: token kosong/salah/kedaluwarsa/sudah dipakai → kesalahan pengguna (422).
	ErrFailed = errors.New("verifikasi turnstile gagal")
	// ErrUnavailable: Cloudflare tidak terjangkau, timeout, 5xx, atau respons rusak → fail closed (503).
	ErrUnavailable = errors.New("siteverify tidak terjangkau")
)

// Verifier memanggil siteverify. nil = fitur mati: Verify selalu lolos tanpa panggilan keluar.
type Verifier struct {
	secret string
	url    string
	Client *http.Client // bisa diganti saat tes
}

// New: secret kosong → nil (fitur mati). verifyURL kosong → DefaultURL.
func New(secret, verifyURL string) *Verifier {
	if secret == "" {
		return nil
	}
	if verifyURL == "" {
		verifyURL = DefaultURL
	}
	return &Verifier{secret: secret, url: verifyURL, Client: &http.Client{Timeout: 5 * time.Second}}
}

// Verify memeriksa token. Error dibungkus ErrFailed atau ErrUnavailable dan tidak pernah memuat
// secret maupun token (aman dicatat di log).
func (v *Verifier) Verify(ctx context.Context, token, remoteIP string) error {
	if v == nil {
		return nil
	}
	if token == "" || len(token) > maxTokenLen {
		return fmt.Errorf("%w: token kosong atau terlalu panjang", ErrFailed)
	}
	form := url.Values{"secret": {v.secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.url, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := v.Client.Do(req)
	if err != nil {
		// *url.Error memuat metode + URL + sebab; secret & token ada di body, jadi tidak ikut.
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	var out struct {
		Success bool     `json:"success"`
		Codes   []string `json:"error-codes"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return fmt.Errorf("%w: respons tidak bisa dibaca", ErrUnavailable)
	}
	if out.Success {
		return nil
	}
	codes := strings.Join(clean(out.Codes), ",")
	// internal-error: masalah di sisi Cloudflare, bukan pengguna → perlakukan seperti tidak terjangkau.
	if slices.Contains(out.Codes, "internal-error") {
		return fmt.Errorf("%w: %s", ErrUnavailable, codes)
	}
	return fmt.Errorf("%w: %s", ErrFailed, codes)
}

// clean: hanya kode error pendek berhuruf aman (isi respons tidak dipercaya mentah-mentah ke log).
func clean(codes []string) []string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		if len(c) <= 40 && strings.Trim(c, "abcdefghijklmnopqrstuvwxyz-") == "" {
			out = append(out, c)
		}
	}
	return out
}

// Check memverifikasi lalu menulis respons error bila gagal. false = respons sudah ditulis.
func (v *Verifier) Check(w http.ResponseWriter, r *http.Request, token, remoteIP string) bool {
	err := v.Verify(r.Context(), token, remoteIP)
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrUnavailable):
		slog.WarnContext(r.Context(), "turnstile: siteverify gagal, permintaan ditolak (fail closed)", "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusServiceUnavailable, "captcha_unavailable", "Verifikasi keamanan sedang tidak bisa dihubungi. Coba lagi dalam beberapa saat.")
	default:
		if strings.Contains(err.Error(), "input-secret") { // salah konfigurasi server, bukan salah pengguna
			slog.ErrorContext(r.Context(), "turnstile: TURNSTILE_SECRET_KEY ditolak Cloudflare", "err", err)
		}
		httpx.JSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "captcha_failed", "message": "Verifikasi keamanan gagal, coba lagi",
			"fields": httpx.Fields{"turnstile_token": "Verifikasi keamanan gagal, coba lagi"},
		})
	}
	return false
}
