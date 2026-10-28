package partner

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

type Member struct {
	UserID   uuid.UUID   `json:"user_id"`
	Name     string      `json:"name"`
	Email    string      `json:"email"`
	Role     tenant.Role `json:"role"`
	JoinedAt time.Time   `json:"joined_at"`
}

func (h *Handler) listMembers(ctx context.Context) ([]Member, error) {
	rows, err := h.db.Query(ctx, `
		SELECT u.id, u.name, u.email, m.role, m.created_at
		FROM org_members m JOIN users u ON u.id = m.user_id
		WHERE m.org_id = $1
		ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'finance' THEN 2 ELSE 3 END, u.name`,
		tenant.OrgID(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Member])
}

func (h *Handler) writeMembers(w http.ResponseWriter, r *http.Request, status int) {
	list, err := h.listMembers(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, status, map[string]any{"members": list})
}

// ListMembers: GET /v1/org/{org}/members — owner.
func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	h.writeMembers(w, r, http.StatusOK)
}

// Peran yang bisa diberikan lewat undangan. Owner baru hanya lewat ubah peran anggota yang sudah ada.
func invitable(role tenant.Role) bool {
	return role == tenant.RoleAdmin || role == tenant.RoleFinance || role == tenant.RoleRPCStaff
}

// AddMember: POST /v1/org/{org}/members {"email","role"} — owner. Akunnya harus sudah pernah login
// (belum ada undangan via email).
func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Email string      `json:"email"`
		Role  tenant.Role `json:"role"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	email, ok := auth.NormalizeEmail(in.Email)
	f := httpx.Fields{}
	if !ok {
		f["email"] = "Format email tidak valid"
	}
	if !invitable(in.Role) {
		f["role"] = "Pilih peran admin, finance, atau staf race pack"
	}
	if len(f) > 0 {
		httpx.Validation(w, f)
		return
	}
	var userID uuid.UUID
	err := h.db.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "user_not_found",
			"Belum ada akun RaceGo dengan email ini. Minta orang itu masuk ke RaceGo dulu, lalu tambahkan lagi.")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	orgID := tenant.OrgID(ctx)
	err = pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			orgID, userID, in.Role)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return statusError{"already_member", "Orang ini sudah menjadi anggota tim"}
		}
		return audit(ctx, tx, orgID, "member.added", "member", userID.String(), nil, map[string]any{"email": email, "role": in.Role})
	})
	if finish(w, r, err) {
		return
	}
	h.writeMembers(w, r, http.StatusCreated)
}

// changeMember menjalankan perubahan pada anggota lain dengan baris org terkunci, sehingga dua
// owner yang saling menurunkan secara bersamaan tidak bisa menghabiskan semua owner.
func (h *Handler) changeMember(w http.ResponseWriter, r *http.Request, action string, newRole *tenant.Role) {
	ctx := r.Context()
	target, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "member_not_found", "Anggota tidak ditemukan")
		return
	}
	if me, _ := auth.UserID(ctx); me == target {
		httpx.Error(w, http.StatusConflict, "self_change", "Kamu tidak bisa mengubah atau menghapus peranmu sendiri. Minta owner lain.")
		return
	}
	orgID := tenant.OrgID(ctx)
	err = pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return err
		}
		var cur tenant.Role
		err := tx.QueryRow(ctx, `SELECT role FROM org_members WHERE org_id = $1 AND user_id = $2`, orgID, target).Scan(&cur)
		if errors.Is(err, pgx.ErrNoRows) {
			return errMemberNotFound
		}
		if err != nil {
			return err
		}
		if cur == tenant.RoleOwner && (newRole == nil || *newRole != tenant.RoleOwner) {
			var owners int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM org_members WHERE org_id = $1 AND role = 'owner'`, orgID).Scan(&owners); err != nil {
				return err
			}
			if owners <= 1 {
				return statusError{"last_owner", "Penyelenggara harus punya minimal satu owner"}
			}
		}
		var after any
		if newRole == nil {
			_, err = tx.Exec(ctx, `DELETE FROM org_members WHERE org_id = $1 AND user_id = $2`, orgID, target)
		} else {
			_, err = tx.Exec(ctx, `UPDATE org_members SET role = $3 WHERE org_id = $1 AND user_id = $2`, orgID, target, *newRole)
			after = map[string]any{"role": *newRole}
		}
		if err != nil {
			return err
		}
		return audit(ctx, tx, orgID, action, "member", target.String(), map[string]any{"role": cur}, after)
	})
	if errors.Is(err, errMemberNotFound) {
		httpx.Error(w, http.StatusNotFound, "member_not_found", "Anggota tidak ditemukan")
		return
	}
	if finish(w, r, err) {
		return
	}
	h.writeMembers(w, r, http.StatusOK)
}

var errMemberNotFound = errors.New("anggota tidak ditemukan")

// UpdateMember: PATCH /v1/org/{org}/members/{userID} {"role"} — owner; tidak untuk diri sendiri.
func (h *Handler) UpdateMember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role tenant.Role `json:"role"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !invitable(in.Role) && in.Role != tenant.RoleOwner {
		httpx.Validation(w, httpx.Fields{"role": "Peran tidak valid"})
		return
	}
	h.changeMember(w, r, "member.updated", &in.Role)
}

// RemoveMember: DELETE /v1/org/{org}/members/{userID} — owner; tidak untuk diri sendiri.
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	h.changeMember(w, r, "member.removed", nil)
}
