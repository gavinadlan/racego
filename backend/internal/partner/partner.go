// Package partner adalah portal mitra: daftar penyelenggara, kelola profil, event, kategori
// tiket, penerbitan, dan anggota tim.
//
// Route mitra (/v1/org/{org}/...) selalu lewat tenant.RequireRole. Setiap query WAJIB
// memakai tenant.OrgID(ctx); org_id dari body tidak pernah dipakai, dan data milik org lain
// selalu dibalas 404.
package partner

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/checkout"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

const (
	maxOrgsPerUser  = 3  // org yang dibuat sendiri oleh satu akun
	maxDraftsPerOrg = 20 // event draft per org
)

type Handler struct {
	db         *pgxpool.Pool
	inv        *checkout.Inventory
	autoVerify bool
	now        func() time.Time
}

func NewHandler(db *pgxpool.Pool, inv *checkout.Inventory, autoVerify bool) *Handler {
	return &Handler{db: db, inv: inv, autoVerify: autoVerify, now: time.Now}
}

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// audit mencatat perubahan oleh mitra ke audit_logs (before/after disimpan sebagai JSON).
func audit(ctx context.Context, q querier, orgID uuid.UUID, action, entity, entityID string, before, after any) error {
	actor, _ := auth.UserID(ctx)
	_, err := q.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, org_id, action, entity, entity_id, before, after)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, actor, orgID, action, entity, entityID, before, after)
	return err
}

// uniqueViolation mengembalikan nama constraint bila err adalah pelanggaran UNIQUE.
func uniqueViolation(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName
	}
	return ""
}

func membership(ctx context.Context) tenant.Membership {
	m, _ := tenant.FromContext(ctx)
	return m
}
