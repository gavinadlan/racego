package kyc

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/httpx"
)

// RequireSuperAdmin: selain super admin dibalas 404 agar keberadaan halaman admin tidak bocor.
// Dipasang setelah auth.Require (anonim → 401).
func RequireSuperAdmin(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, _ := auth.UserID(r.Context())
			var ok bool
			err := db.QueryRow(r.Context(), `SELECT is_super_admin FROM users WHERE id = $1`, userID).Scan(&ok)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				httpx.Internal(w, r, err)
				return
			}
			if !ok {
				httpx.Error(w, http.StatusNotFound, "not_found", "Halaman tidak ditemukan")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

const adminPageSize = 20

type ListItem struct {
	ID          uuid.UUID  `json:"id"`
	OrgName     string     `json:"org_name"`
	OrgSlug     string     `json:"org_slug"`
	OrgType     *string    `json:"org_type"`
	LegalName   string     `json:"legal_name"`
	Attempt     int        `json:"attempt"`
	Status      string     `json:"status"`
	SubmittedAt *time.Time `json:"submitted_at"`
	ReviewedAt  *time.Time `json:"reviewed_at"`
}

// AdminList: GET /v1/admin/verifications?status=submitted|approved|rejected&q=&page=
// Menunggu: terlama dikirim dulu. Disetujui/ditolak: terbaru ditinjau dulu.
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "submitted"
	}
	if status != "submitted" && status != "approved" && status != "rejected" {
		httpx.Validation(w, httpx.Fields{"status": "Status harus submitted, approved, atau rejected"})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	order := "v.submitted_at ASC"
	if status != "submitted" {
		order = "v.reviewed_at DESC"
	}
	where := `v.status = $1 AND ($2 = '' OR o.name ILIKE '%' || $2 || '%' OR v.legal_name ILIKE '%' || $2 || '%')`
	rows, err := h.db.Query(ctx, `
		SELECT v.id, o.name, o.slug, v.org_type, v.legal_name, v.attempt, v.status, v.submitted_at, v.reviewed_at
		FROM org_verifications v JOIN organizations o ON o.id = v.org_id
		WHERE `+where+` ORDER BY `+order+`, v.id LIMIT $3 OFFSET $4`, status, q, adminPageSize, (page-1)*adminPageSize)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ListItem])
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var total int
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM org_verifications v JOIN organizations o ON o.id = v.org_id WHERE `+where, status, q).Scan(&total); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	counts := map[string]int{"submitted": 0, "approved": 0, "rejected": 0}
	crows, err := h.db.Query(ctx, `SELECT status, count(*) FROM org_verifications WHERE status <> 'draft' GROUP BY status`)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	defer crows.Close()
	for crows.Next() {
		var s string
		var n int
		if err := crows.Scan(&s, &n); err != nil {
			httpx.Internal(w, r, err)
			return
		}
		counts[s] = n
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "counts": counts, "total": total, "page": page, "page_size": adminPageSize})
}

// reviewable memuat pengajuan yang sudah dikirim (draft tidak pernah terlihat oleh admin).
func (h *Handler) reviewable(r *http.Request, q querier, lock bool) (*verification, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return nil, errNotFound
	}
	sql := `SELECT ` + vCols + ` FROM org_verifications WHERE id = $1 AND status <> 'draft'`
	if lock {
		sql += ` FOR UPDATE`
	}
	return scanV(q.QueryRow(r.Context(), sql, id))
}

type adminDoc struct {
	doc
	DuplicateOrgs []string `json:"duplicate_orgs"` // org lain yang mengunggah berkas identik
}

type member struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// AdminGet: GET /v1/admin/verifications/{id}
func (h *Handler) AdminGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v, err := h.reviewable(r, h.db, false)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var org struct {
		Slug      string    `json:"slug"`
		Name      string    `json:"name"`
		City      string    `json:"city"`
		KYCStatus string    `json:"kyc_status"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := h.db.QueryRow(ctx, `SELECT slug, name, city, kyc_status, created_at FROM organizations WHERE id = $1`, v.OrgID).
		Scan(&org.Slug, &org.Name, &org.City, &org.KYCStatus, &org.CreatedAt); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	docs, err := loadDocs(ctx, h.db, v.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	adocs := make([]adminDoc, 0, len(docs))
	for _, d := range docs {
		ad := adminDoc{doc: d}
		rows, err := h.db.Query(ctx, `
			SELECT DISTINCT o.name FROM org_documents d
			JOIN org_verifications v ON v.id = d.verification_id JOIN organizations o ON o.id = v.org_id
			WHERE d.sha256 = $1 AND v.org_id <> $2 ORDER BY o.name`, d.sha256, v.OrgID)
		if err == nil {
			ad.DuplicateOrgs, err = pgx.CollectRows(rows, pgx.RowTo[string])
		}
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		adocs = append(adocs, ad)
	}
	rows, err := h.db.Query(ctx, `
		SELECT u.name, u.email, m.role FROM org_members m JOIN users u ON u.id = m.user_id
		WHERE m.org_id = $1 ORDER BY m.role = 'owner' DESC, u.name`, v.OrgID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	members, err := pgx.CollectRows(rows, pgx.RowToStructByPos[member])
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	hist, err := history(ctx, h.db, v.OrgID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var reviewer *string
	if v.ReviewedBy != nil {
		_ = h.db.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, *v.ReviewedBy).Scan(&reviewer)
	}
	userID, _ := auth.UserID(ctx)
	var conflict bool
	if err := h.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM org_members WHERE org_id = $1 AND user_id = $2)`, v.OrgID, userID).Scan(&conflict); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	view := v.view(nil)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"verification": view, "documents": adocs, "org": org, "members": members, "history": hist,
		"name_match": matchHolder(v.orgType(), v.AccountHolder, v.PICName, v.LegalName),
		"reviewer":   reviewer, "conflict_of_interest": conflict, "requirements": requirements(),
	})
}

// AdminReveal: POST /v1/admin/verifications/{id}/reveal {"field":"pic_id"|"account"|"npwp"} — nilai lengkap, tercatat.
func (h *Handler) AdminReveal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Field string `json:"field"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	v, err := h.reviewable(r, h.db, false)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	enc := map[string]*string{"pic_id": v.PICIDEnc, "account": v.AccountEnc, "npwp": v.NPWPEnc}
	sealed, known := enc[in.Field]
	if !known {
		httpx.Validation(w, httpx.Fields{"field": "Kolom harus pic_id, account, atau npwp"})
		return
	}
	if sealed == nil {
		writeErr(w, r, errNotFound)
		return
	}
	value, err := h.box.OpenString(*sealed)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := audit(ctx, h.db, r, v.OrgID, "kyc.field.reveal", v.ID.String(), map[string]any{"field": in.Field}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, map[string]string{"field": in.Field, "value": value})
}

// AdminDocument: GET /v1/admin/verifications/{id}/documents/{kind} — tercatat.
func (h *Handler) AdminDocument(w http.ResponseWriter, r *http.Request) {
	v, err := h.reviewable(r, h.db, false)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h.serveDocument(w, r, v, DocKind(chi.URLParam(r, "kind")), "super_admin")
}

// AdminApprove: POST /v1/admin/verifications/{id}/approve
func (h *Handler) AdminApprove(w http.ResponseWriter, r *http.Request) { h.review(w, r, true, "") }

// AdminReject: POST /v1/admin/verifications/{id}/reject {"reason"}
func (h *Handler) AdminReject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	reason := strings.TrimSpace(in.Reason)
	if n := utf8.RuneCountInString(reason); n < 10 || n > 1000 {
		httpx.Validation(w, httpx.Fields{"reason": "Alasan penolakan 10–1.000 karakter"})
		return
	}
	h.review(w, r, false, reason)
}

func (h *Handler) review(w http.ResponseWriter, r *http.Request, approve bool, reason string) {
	ctx := r.Context()
	userID, _ := auth.UserID(ctx)
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		v, err := h.reviewable(r, tx, true)
		if err != nil {
			return err
		}
		var member bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM org_members WHERE org_id = $1 AND user_id = $2)`, v.OrgID, userID).Scan(&member); err != nil {
			return err
		}
		if member {
			return apiError{status: http.StatusForbidden, code: "conflict_of_interest",
				msg: "Kamu anggota penyelenggara ini, jadi tidak boleh meninjau pengajuannya. Minta super admin lain untuk meninjau."}
		}
		if v.Status != "submitted" {
			return apiError{status: http.StatusConflict, code: "invalid_status", msg: "Pengajuan ini sudah ditinjau."}
		}
		status, kyc, action := "approved", "verified", "kyc.approved"
		var rr *string
		if !approve {
			status, kyc, action, rr = "rejected", "rejected", "kyc.rejected", &reason
		}
		if _, err := tx.Exec(ctx, `
			UPDATE org_verifications SET status = $2, reviewed_by = $3, reviewed_at = now(), reject_reason = $4, updated_at = now()
			WHERE id = $1`, v.ID, status, userID, rr); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE organizations SET kyc_status = $2 WHERE id = $1`, v.OrgID, kyc); err != nil {
			return err
		}
		after := map[string]any{"attempt": v.Attempt, "kyc_status": kyc}
		if rr != nil {
			after["reason"] = reason
		}
		if approve {
			// Rekening pencairan = rekening dari pengajuan yang disetujui (ciphertext disalin apa adanya).
			if _, err := tx.Exec(ctx, `
				INSERT INTO payout_accounts (org_id, bank_code, bank_name, account_holder, account_enc, account_last4, verified_at, verification_id)
				SELECT org_id, bank_code, bank_name, account_holder, account_enc, account_last4, now(), id
				FROM org_verifications WHERE id = $1 AND account_enc IS NOT NULL AND account_last4 IS NOT NULL
				ON CONFLICT (org_id) DO UPDATE SET bank_code = EXCLUDED.bank_code, bank_name = EXCLUDED.bank_name,
				  account_holder = EXCLUDED.account_holder, account_enc = EXCLUDED.account_enc, account_last4 = EXCLUDED.account_last4,
				  verified_at = EXCLUDED.verified_at, verification_id = EXCLUDED.verification_id`, v.ID); err != nil {
				return err
			}
		}
		if err := audit(ctx, tx, r, v.OrgID, action, v.ID.String(), after); err != nil {
			return err
		}
		return queueEmail(ctx, tx, v.ID, status)
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h.AdminGet(w, r)
}
