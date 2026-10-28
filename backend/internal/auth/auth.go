// Package auth menentukan siapa user yang sedang memanggil API.
//
// SEMENTARA: di APP_ENV=development, identitas diambil dari header X-User-ID
// (dikirim frontend dari pemilih user dev). Ganti dengan login OTP/Google + JWT
// sebelum aplikasi dibuka ke publik — lihat docs/02-arsitektur.md.
package auth

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/gavinadlan/racego/backend/internal/httpx"
)

type ctxKey struct{}

// Middleware mengisi user ID ke context bila ada; tidak menolak request anonim.
func Middleware(devMode bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if devMode {
				if id, err := uuid.Parse(r.Header.Get("X-User-ID")); err == nil {
					r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, id))
				}
			}
			// TODO(auth): verifikasi JWT dari header Authorization di sini.
			next.ServeHTTP(w, r)
		})
	}
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
