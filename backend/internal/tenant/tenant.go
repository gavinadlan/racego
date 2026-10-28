// Package tenant menjaga isolasi data antar mitra (multi-tenant).
//
// Setiap route di bawah /v1/org/{org}/... melewati RequireRole: slug mitra di URL
// diubah menjadi org_id, lalu keanggotaan user dicek. Handler WAJIB memakai
// tenant.OrgID(ctx) di setiap query — jangan pernah memakai org_id dari body request.
package tenant

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/httpx"
)

type Role string

const (
	RoleOwner    Role = "owner"
	RoleAdmin    Role = "admin"
	RoleFinance  Role = "finance"
	RoleRPCStaff Role = "rpc_staff"
)

type Membership struct {
	OrgID   uuid.UUID
	OrgSlug string
	OrgName string
	Role    Role
}

type ctxKey struct{}

// RequireRole memastikan user adalah anggota mitra {org} dengan salah satu peran yang diizinkan.
func RequireRole(db *pgxpool.Pool, allowed ...Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := auth.UserID(r.Context())
			if !ok {
				httpx.Error(w, http.StatusUnauthorized, "unauthenticated", "Silakan login terlebih dahulu")
				return
			}
			slug := chi.URLParam(r, "org")
			var m Membership
			err := db.QueryRow(r.Context(), `
				SELECT o.id, o.slug, o.name, m.role
				FROM organizations o
				JOIN org_members m ON m.org_id = o.id
				WHERE o.slug = $1 AND m.user_id = $2`, slug, userID,
			).Scan(&m.OrgID, &m.OrgSlug, &m.OrgName, &m.Role)
			if errors.Is(err, pgx.ErrNoRows) {
				// 404 (bukan 403) agar keberadaan mitra lain tidak bocor
				httpx.Error(w, http.StatusNotFound, "org_not_found", "Mitra tidak ditemukan")
				return
			}
			if err != nil {
				httpx.Internal(w, r, err)
				return
			}
			if !slices.Contains(allowed, m.Role) {
				httpx.Error(w, http.StatusForbidden, "forbidden", "Peran Anda tidak diizinkan untuk aksi ini")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, m)))
		})
	}
}

// FromContext mengembalikan keanggotaan mitra yang sudah diverifikasi middleware.
func FromContext(ctx context.Context) (Membership, bool) {
	m, ok := ctx.Value(ctxKey{}).(Membership)
	return m, ok
}

func OrgID(ctx context.Context) uuid.UUID {
	m, _ := FromContext(ctx)
	return m.OrgID
}
