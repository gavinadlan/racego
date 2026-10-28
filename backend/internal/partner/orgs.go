package partner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

func takenOrgSlugs(q querier) func(ctx context.Context, base string) ([]string, error) {
	return func(ctx context.Context, base string) ([]string, error) {
		var out []string
		err := q.QueryRow(ctx, `SELECT COALESCE(array_agg(slug), '{}') FROM organizations WHERE slug = $1 OR slug LIKE $2`,
			base, base+"-%").Scan(&out)
		return out, err
	}
}

// CreateOrg: POST /v1/orgs — siapa pun yang login bisa mendaftarkan penyelenggara dan otomatis jadi owner.
func (h *Handler) CreateOrg(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, _ := auth.UserID(ctx)
	var in OrgInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	in.trim()
	if f := in.validate(false); len(f) > 0 {
		httpx.Validation(w, f)
		return
	}
	var created int
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE created_by = $1`, userID).Scan(&created); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if created >= maxOrgsPerUser {
		httpx.Error(w, http.StatusTooManyRequests, "too_many_orgs",
			fmt.Sprintf("Satu akun maksimal mendaftarkan %d penyelenggara.", maxOrgsPerUser))
		return
	}

	slug := in.Slug
	if slug == "" {
		base := Slugify(in.Name)
		if len(base) < 3 {
			base = "penyelenggara"
		}
		var err error
		if slug, err = uniqueSlug(ctx, base, reservedOrgSlugs, takenOrgSlugs(h.db)); err != nil {
			httpx.Internal(w, r, err)
			return
		}
	}
	kyc := "pending"
	if h.autoVerify {
		kyc = "verified"
	}
	var orgID uuid.UUID
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO organizations (slug, name, kyc_status, city, description, instagram, website, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			slug, in.Name, kyc, in.City, in.Description, nullIfEmpty(in.Instagram), nullIfEmpty(in.Website), userID,
		).Scan(&orgID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'owner')`, orgID, userID); err != nil {
			return err
		}
		return audit(ctx, tx, orgID, "org.created", "organization", orgID.String(), nil,
			map[string]any{"slug": slug, "name": in.Name, "kyc_status": kyc})
	})
	if uniqueViolation(err) != "" {
		httpx.Validation(w, httpx.Fields{"slug": "Alamat ini sudah dipakai penyelenggara lain"})
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"slug": slug, "name": in.Name, "kyc_status": kyc})
}

// SlugCheck: GET /v1/orgs/slug-check?slug= — untuk form pendaftaran (cek sambil mengetik).
func (h *Handler) SlugCheck(w http.ResponseWriter, r *http.Request) {
	slug := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("slug")))
	if msg := checkOrgSlug(slug); msg != "" {
		httpx.JSON(w, http.StatusOK, map[string]any{"available": false, "message": msg})
		return
	}
	var taken bool
	if err := h.db.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM organizations WHERE slug = $1)`, slug).Scan(&taken); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	resp := map[string]any{"available": !taken}
	if taken {
		resp["message"] = "Alamat ini sudah dipakai penyelenggara lain"
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type Profile struct {
	Slug        string      `json:"slug"`
	Name        string      `json:"name"`
	City        string      `json:"city"`
	Description string      `json:"description"`
	Instagram   *string     `json:"instagram"`
	Website     *string     `json:"website"`
	ThemeColor  string      `json:"theme_color"`
	LogoURL     *string     `json:"logo_url"`
	KYCStatus   string      `json:"kyc_status"`
	Role        tenant.Role `json:"role"` // peran user yang sedang melihat
}

func (h *Handler) loadProfile(ctx context.Context, q querier) (Profile, error) {
	m := membership(ctx)
	p := Profile{Role: m.Role}
	err := q.QueryRow(ctx, `
		SELECT slug, name, city, description, instagram, website, theme_color, logo_url, kyc_status
		FROM organizations WHERE id = $1`, m.OrgID,
	).Scan(&p.Slug, &p.Name, &p.City, &p.Description, &p.Instagram, &p.Website, &p.ThemeColor, &p.LogoURL, &p.KYCStatus)
	return p, err
}

// GetProfile: GET /v1/org/{org}/profile — semua anggota.
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	p, err := h.loadProfile(r.Context(), h.db)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

// UpdateProfile: PATCH /v1/org/{org}/profile — owner & admin. Slug tidak bisa diubah.
func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in OrgInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	in.trim()
	if in.Slug != "" && in.Slug != membership(ctx).OrgSlug {
		httpx.Validation(w, httpx.Fields{"slug": "Alamat penyelenggara tidak bisa diubah"})
		return
	}
	if f := in.validate(true); len(f) > 0 {
		httpx.Validation(w, f)
		return
	}
	orgID := tenant.OrgID(ctx)
	var p Profile
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		before, err := h.loadProfile(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE organizations SET name = $2, city = $3, description = $4, instagram = $5, website = $6,
			       theme_color = $7, logo_url = $8
			WHERE id = $1`, orgID, in.Name, in.City, in.Description, nullIfEmpty(in.Instagram), nullIfEmpty(in.Website),
			strings.ToUpper(in.ThemeColor), nullIfEmpty(in.LogoURL)); err != nil {
			return err
		}
		if p, err = h.loadProfile(ctx, tx); err != nil {
			return err
		}
		return audit(ctx, tx, orgID, "org.updated", "organization", orgID.String(), before, p)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "org_not_found", "Mitra tidak ditemukan")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}
