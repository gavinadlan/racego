// Package auth menentukan siapa user yang sedang memanggil API: login email + OTP,
// sesi di cookie HttpOnly racego_session. Lihat docs/02-arsitektur.md.
//
// Khusus APP_ENV=development, request tanpa cookie sesi boleh membawa header X-User-ID
// (pemilih akun contoh di frontend). Di luar development header itu diabaikan.
package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/gavinadlan/racego/backend/internal/httpx"
)

type ctxKey struct{}

// LookupFunc mencari pemilik token sesi; biasanya (*SessionStore).Lookup.
type LookupFunc func(ctx context.Context, token string) (uuid.UUID, error)

// Middleware mengisi user ID ke context bila ada; tidak menolak request anonim.
// Prioritas: cookie sesi valid → X-User-ID (hanya devMode).
//
// CSRF: selain SameSite=Lax, request yang mengubah data dari Origin selain frontendURL ditolak.
// Webhook server-ke-server (Midtrans) tidak mengirim Origin, jadi tidak terpengaruh.
func Middleware(lookup LookupFunc, frontendURL string, devMode bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if unsafeMethod(r.Method) {
				if o := r.Header.Get("Origin"); o != "" && o != frontendURL {
					httpx.Error(w, http.StatusForbidden, "bad_origin", "Permintaan ditolak: asal halaman tidak dikenal")
					return
				}
			}
			if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
				id, err := lookup(r.Context(), c.Value)
				if err == nil {
					next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), id)))
					return
				}
				if !errors.Is(err, ErrNoSession) {
					slog.ErrorContext(r.Context(), "cek sesi gagal", "err", err)
				}
			}
			if devMode {
				if id, err := uuid.Parse(r.Header.Get("X-User-ID")); err == nil {
					r = r.WithContext(WithUser(r.Context(), id))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func unsafeMethod(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch || m == http.MethodDelete
}

// Require menolak request yang belum login.
func Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserID(r.Context()); !ok {
			httpx.Error(w, http.StatusUnauthorized, "unauthenticated", "Silakan login terlebih dahulu")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func UserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxKey{}).(uuid.UUID)
	return id, ok
}

// WithUser dipakai di tes untuk menyuntikkan user ke context.
func WithUser(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}
