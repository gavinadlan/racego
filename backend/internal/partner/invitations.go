package partner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/notify"
	"github.com/gavinadlan/racego/backend/internal/ratelimit"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

// Undangan anggota tim lewat email.
//
// Token acak 32 byte (base64url) hanya ada di email; yang disimpan SHA-256-nya. Email dikirim
// langsung di dalam transaksi pembuatan/kirim ulang (seperti kode OTP), bukan lewat outbox, jadi
// token mentah tidak pernah tersimpan di database mana pun. Gagal kirim = transaksi batal.
const (
	InviteTTL       = 7 * 24 * time.Hour
	resendGap       = 5 * time.Minute
	maxSends        = 5  // kirim pertama + ulang
	invitesPerHour  = 20 // undangan + kirim ulang, per org dan per pengundang
	tokenTriesPerIP = 30 // buka/terima tautan per IP per 10 menit
)

type Mailer interface {
	Send(to, subject, htmlBody string) error
}

type Invitations struct {
	db          *pgxpool.Pool
	rdb         redis.Cmdable
	mailer      Mailer
	frontendURL string
}

func NewInvitations(db *pgxpool.Pool, rdb redis.Cmdable, mailer Mailer, frontendURL string) *Invitations {
	return &Invitations{db: db, rdb: rdb, mailer: mailer, frontendURL: frontendURL}
}

var roleLabel = map[tenant.Role]string{tenant.RoleAdmin: "Admin", tenant.RoleFinance: "Keuangan", tenant.RoleRPCStaff: "Staf race pack"}

// canInvite: owner mengundang admin/finance/rpc_staff; admin hanya rpc_staff.
func canInvite(by, role tenant.Role) bool {
	switch by {
	case tenant.RoleOwner:
		return invitable(role)
	case tenant.RoleAdmin:
		return role == tenant.RoleRPCStaff
	}
	return false
}

func newToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// MaskEmail: "budi.santoso@contoh.id" → "b•••@contoh.id".
func MaskEmail(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" {
		return "•••"
	}
	return local[:1] + "•••@" + domain
}

type Invitation struct {
	ID          uuid.UUID   `json:"id"`
	Email       string      `json:"email"`
	Role        tenant.Role `json:"role"`
	InvitedBy   string      `json:"invited_by"`
	CreatedAt   time.Time   `json:"created_at"`
	LastSentAt  time.Time   `json:"last_sent_at"`
	ExpiresAt   time.Time   `json:"expires_at"`
	SendCount   int         `json:"send_count"`
	Status      string      `json:"status"` // pending | expired
	CanManage   bool        `json:"can_manage"`
	ResendAfter *time.Time  `json:"resend_after"` // null = boleh kirim ulang sekarang (bila send_count < 5)
}

func (h *Invitations) writeList(w http.ResponseWriter, r *http.Request, status int) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	rows, err := h.db.Query(ctx, `
		SELECT i.id, i.email, i.role, u.name, i.created_at, i.last_sent_at, i.expires_at, i.send_count,
		       CASE WHEN i.expires_at < now() THEN 'expired' ELSE 'pending' END
		FROM org_invitations i JOIN users u ON u.id = i.invited_by
		WHERE i.org_id = $1 AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.created_at > now() - interval '30 days'
		ORDER BY i.created_at DESC`, m.OrgID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	list := []Invitation{}
	for rows.Next() {
		var i Invitation
		if err := rows.Scan(&i.ID, &i.Email, &i.Role, &i.InvitedBy, &i.CreatedAt, &i.LastSentAt, &i.ExpiresAt, &i.SendCount, &i.Status); err != nil {
			rows.Close()
			httpx.Internal(w, r, err)
			return
		}
		i.CanManage = canInvite(m.Role, i.Role)
		if next := i.LastSentAt.Add(resendGap); time.Now().Before(next) {
			i.ResendAfter = &next
		}
		list = append(list, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	roles := []tenant.Role{}
	for _, role := range []tenant.Role{tenant.RoleAdmin, tenant.RoleFinance, tenant.RoleRPCStaff} {
		if canInvite(m.Role, role) {
			roles = append(roles, role)
		}
	}
	httpx.JSON(w, status, map[string]any{"invitations": list, "invitable_roles": roles, "max_sends": maxSends})
}

// List: GET /v1/org/{org}/invitations — owner, admin. Undangan belum diterima/dicabut (30 hari terakhir).
func (h *Invitations) List(w http.ResponseWriter, r *http.Request) { h.writeList(w, r, http.StatusOK) }

// limited menulis 429 bila pembatas laju terlampaui; true = respons sudah ditulis.
func limited(w http.ResponseWriter, r *http.Request, err error, msg string) bool {
	var ex ratelimit.Exceeded
	if err == nil {
		return false
	}
	if !errors.As(err, &ex) {
		httpx.Internal(w, r, err)
		return true
	}
	secs := int(math.Ceil(ex.RetryAfter.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	httpx.JSON(w, http.StatusTooManyRequests, map[string]any{"error": "too_many_requests", "message": fmt.Sprintf("%s Coba lagi dalam %s.", msg, waitText(secs)), "retry_after": secs})
	return true
}

func waitText(secs int) string {
	if secs < 90 {
		return strconv.Itoa(secs) + " detik"
	}
	return strconv.Itoa((secs+59)/60) + " menit"
}

// limitSend: undangan & kirim ulang dibatasi per org dan per pengundang.
func (h *Invitations) limitSend(w http.ResponseWriter, r *http.Request, orgID, userID uuid.UUID) bool {
	ctx := r.Context()
	const msg = "Terlalu banyak undangan dikirim."
	if limited(w, r, ratelimit.Allow(ctx, h.rdb, "invite_rl:org:"+orgID.String(), invitesPerHour, time.Hour), msg) {
		return true
	}
	return limited(w, r, ratelimit.Allow(ctx, h.rdb, "invite_rl:user:"+userID.String(), invitesPerHour, time.Hour), msg)
}

func (h *Invitations) send(ctx context.Context, q querier, inviteID uuid.UUID, token string) error {
	var email, orgName, inviter string
	var role tenant.Role
	if err := q.QueryRow(ctx, `
		SELECT i.email, i.role, o.name, u.name FROM org_invitations i
		JOIN organizations o ON o.id = i.org_id JOIN users u ON u.id = i.invited_by WHERE i.id = $1`, inviteID).
		Scan(&email, &role, &orgName, &inviter); err != nil {
		return err
	}
	subject, body := notify.InvitationEmail(orgName, roleLabel[role], inviter, h.frontendURL+"/undangan/"+token)
	if err := h.mailer.Send(email, subject, body); err != nil {
		return errMail{err}
	}
	return nil
}

type errMail struct{ err error }

func (e errMail) Error() string { return "kirim email undangan: " + e.err.Error() }

// finishInvite: error transaksi undangan → respons HTTP. true = sudah membalas.
func finishInvite(w http.ResponseWriter, r *http.Request, err error) bool {
	var em errMail
	if errors.As(err, &em) {
		slog.ErrorContext(r.Context(), "kirim email undangan gagal", "err", em.err)
		httpx.Error(w, http.StatusBadGateway, "mail_failed", "Email undangan gagal dikirim. Coba lagi sebentar lagi.")
		return true
	}
	return finish(w, r, err)
}

// Create: POST /v1/org/{org}/invitations {"email","role"} — owner (admin/finance/rpc_staff), admin (rpc_staff).
func (h *Invitations) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	userID, _ := auth.UserID(ctx)
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
		f["role"] = "Pilih peran admin, keuangan, atau staf race pack"
	}
	if len(f) > 0 {
		httpx.Validation(w, f)
		return
	}
	if !canInvite(m.Role, in.Role) {
		httpx.Error(w, http.StatusForbidden, "role_not_allowed", "Admin hanya bisa mengundang staf race pack")
		return
	}
	if h.limitSend(w, r, m.OrgID, userID) {
		return
	}
	token, hash, err := newToken()
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	err = pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		var member bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM org_members m JOIN users u ON u.id = m.user_id WHERE m.org_id = $1 AND u.email = $2)`,
			m.OrgID, email).Scan(&member); err != nil {
			return err
		}
		if member {
			return validationError{httpx.Fields{"email": "Orang dengan email ini sudah menjadi anggota tim"}}
		}
		// Undangan lama yang sudah kedaluwarsa tidak menghalangi undangan baru.
		if _, err := tx.Exec(ctx, `UPDATE org_invitations SET revoked_at = now()
			WHERE org_id = $1 AND email = $2 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at < now()`, m.OrgID, email); err != nil {
			return err
		}
		var id uuid.UUID
		err := tx.QueryRow(ctx, `INSERT INTO org_invitations (org_id, email, role, token_hash, invited_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			m.OrgID, email, in.Role, hash, userID, time.Now().Add(InviteTTL)).Scan(&id)
		if uniqueViolation(err) == "org_invitations_active_uniq" {
			return validationError{httpx.Fields{"email": "Undangan untuk email ini masih aktif. Kirim ulang dari daftar undangan."}}
		}
		if err != nil {
			return err
		}
		if err := audit(ctx, tx, m.OrgID, "invite.created", "invitation", id.String(), nil, map[string]any{"email": email, "role": in.Role}); err != nil {
			return err
		}
		return h.send(ctx, tx, id, token)
	})
	if finishInvite(w, r, err) {
		return
	}
	h.writeList(w, r, http.StatusCreated)
}

var errInviteNotFound = errors.New("undangan tidak ditemukan")

// manage mengunci undangan aktif milik org ini dan memeriksa hak pengelola atas perannya.
func manage(ctx context.Context, tx pgx.Tx, id uuid.UUID) (role tenant.Role, lastSent time.Time, sends int, err error) {
	m, _ := tenant.FromContext(ctx)
	err = tx.QueryRow(ctx, `SELECT role, last_sent_at, send_count FROM org_invitations
		WHERE id = $1 AND org_id = $2 AND accepted_at IS NULL AND revoked_at IS NULL FOR UPDATE`, id, m.OrgID).Scan(&role, &lastSent, &sends)
	if errors.Is(err, pgx.ErrNoRows) {
		return role, lastSent, sends, errInviteNotFound
	}
	if err == nil && !canInvite(m.Role, role) {
		err = errRoleNotAllowed
	}
	return role, lastSent, sends, err
}

var errRoleNotAllowed = errors.New("peran tidak boleh dikelola")

func (h *Invitations) finishManage(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case errors.Is(err, errInviteNotFound):
		httpx.Error(w, http.StatusNotFound, "invitation_not_found", "Undangan tidak ditemukan atau sudah tidak aktif")
		return true
	case errors.Is(err, errRoleNotAllowed):
		httpx.Error(w, http.StatusForbidden, "role_not_allowed", "Admin hanya bisa mengelola undangan staf race pack")
		return true
	}
	return finishInvite(w, r, err)
}

type resendLimit struct {
	code, msg string
	retry     int
}

func (e resendLimit) Error() string { return e.msg }

// Resend: POST /v1/org/{org}/invitations/{id}/resend — token baru (tautan lama tidak berlaku), masa
// berlaku diperpanjang 7 hari. Maks. sekali per 5 menit dan 5 kirim total.
func (h *Invitations) Resend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	userID, _ := auth.UserID(ctx)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.finishManage(w, r, errInviteNotFound)
		return
	}
	if h.limitSend(w, r, m.OrgID, userID) {
		return
	}
	token, hash, err := newToken()
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	err = pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		_, lastSent, sends, err := manage(ctx, tx, id)
		if err != nil {
			return err
		}
		if sends >= maxSends {
			return resendLimit{"resend_limit", fmt.Sprintf("Undangan ini sudah dikirim %d kali. Cabut lalu undang ulang bila perlu.", maxSends), 0}
		}
		if wait := time.Until(lastSent.Add(resendGap)); wait > 0 {
			secs := int(math.Ceil(wait.Seconds()))
			return resendLimit{"resend_too_soon", "Undangan baru saja dikirim. Kirim ulang lagi dalam " + waitText(secs) + ".", secs}
		}
		if _, err := tx.Exec(ctx, `UPDATE org_invitations SET token_hash = $2, last_sent_at = now(), send_count = send_count + 1,
			expires_at = $3 WHERE id = $1`, id, hash, time.Now().Add(InviteTTL)); err != nil {
			return err
		}
		if err := audit(ctx, tx, m.OrgID, "invite.resent", "invitation", id.String(), nil, map[string]any{"send_count": sends + 1}); err != nil {
			return err
		}
		return h.send(ctx, tx, id, token)
	})
	var rl resendLimit
	if errors.As(err, &rl) {
		body := map[string]any{"error": rl.code, "message": rl.msg}
		if rl.retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(rl.retry))
			body["retry_after"] = rl.retry
		}
		httpx.JSON(w, http.StatusTooManyRequests, body)
		return
	}
	if h.finishManage(w, r, err) {
		return
	}
	h.writeList(w, r, http.StatusOK)
}

// Revoke: DELETE /v1/org/{org}/invitations/{id} — tautan langsung tidak berlaku.
func (h *Invitations) Revoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.finishManage(w, r, errInviteNotFound)
		return
	}
	err = pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		if _, _, _, err := manage(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE org_invitations SET revoked_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		return audit(ctx, tx, m.OrgID, "invite.revoked", "invitation", id.String(), nil, nil)
	})
	if h.finishManage(w, r, err) {
		return
	}
	h.writeList(w, r, http.StatusOK)
}

// ===== Penerima (publik / login) =====

type pendingInvite struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	OrgSlug   string
	OrgName   string
	Email     string
	Role      tenant.Role
	Inviter   string
	ExpiresAt time.Time
	hash      []byte
	active    bool
}

const inviteSelect = `
	SELECT i.id, i.org_id, o.slug, o.name, i.email, i.role, u.name, i.expires_at, i.token_hash,
	       i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > now()
	FROM org_invitations i JOIN organizations o ON o.id = i.org_id JOIN users u ON u.id = i.invited_by
	WHERE i.token_hash = $1`

// lookup: (nil, nil) bila token tidak cocok dengan undangan mana pun.
func lookup(ctx context.Context, q querier, token, lock string) (*pendingInvite, error) {
	if len(token) != 43 { // 32 byte base64url tanpa padding
		return nil, nil
	}
	want := hashToken(token)
	var p pendingInvite
	err := q.QueryRow(ctx, inviteSelect+lock, want).Scan(&p.ID, &p.OrgID, &p.OrgSlug, &p.OrgName, &p.Email, &p.Role, &p.Inviter, &p.ExpiresAt, &p.hash, &p.active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(p.hash, want) != 1 {
		return nil, nil
	}
	return &p, nil
}

func invalidInvite(w http.ResponseWriter) {
	httpx.Error(w, http.StatusNotFound, "invitation_invalid", "Tautan undangan tidak berlaku: mungkin sudah dipakai, dicabut, atau kedaluwarsa. Minta pengundang mengirim undangan baru.")
}

func (h *Invitations) limitToken(w http.ResponseWriter, r *http.Request) bool {
	return limited(w, r, ratelimit.Allow(r.Context(), h.rdb, "invite_rl:ip:"+auth.ClientIP(r), tokenTriesPerIP, 10*time.Minute),
		"Terlalu banyak percobaan membuka undangan.")
}

// Show: GET /v1/invitations/{token} — publik. Ringkasan undangan aktif; selain itu 404 generik.
func (h *Invitations) Show(w http.ResponseWriter, r *http.Request) {
	if h.limitToken(w, r) {
		return
	}
	ctx := r.Context()
	p, err := lookup(ctx, h.db, chi.URLParam(r, "token"), "")
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if p != nil && !p.active {
		// Halaman /undangan memanggil endpoint ini (bukan /accept), jadi percobaan tautan basi dicatat di sini juga.
		if err := audit(ctx, h.db, p.OrgID, "invite.expired_attempt", "invitation", p.ID.String(), nil, nil); err != nil {
			slog.ErrorContext(ctx, "audit undangan", "err", err)
		}
	}
	if p == nil || !p.active {
		invalidInvite(w)
		return
	}
	out := map[string]any{
		"org_name": p.OrgName, "org_slug": p.OrgSlug, "role": p.Role, "invited_by": p.Inviter,
		"email_masked": MaskEmail(p.Email), "expires_at": p.ExpiresAt, "email_matches": nil,
	}
	if userID, ok := auth.UserID(ctx); ok {
		var email string
		if err := h.db.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email); err == nil {
			out["email_matches"] = strings.EqualFold(email, p.Email)
		}
	}
	httpx.JSON(w, http.StatusOK, out)
}

// Accept: POST /v1/invitations/{token}/accept — wajib login dengan email yang sama dengan undangan.
func (h *Invitations) Accept(w http.ResponseWriter, r *http.Request) {
	if h.limitToken(w, r) {
		return
	}
	ctx := r.Context()
	userID, _ := auth.UserID(ctx)
	var (
		p             *pendingInvite
		mismatch      bool
		alreadyMember bool
	)
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		var err error
		if p, err = lookup(ctx, tx, chi.URLParam(r, "token"), " FOR UPDATE OF i"); err != nil || p == nil || !p.active {
			return err
		}
		var email string
		if err := tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email); err != nil {
			return err
		}
		if !strings.EqualFold(email, p.Email) {
			mismatch = true
			return nil
		}
		tag, err := tx.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, p.OrgID, userID, p.Role)
		if err != nil {
			return err
		}
		alreadyMember = tag.RowsAffected() == 0 // peran yang sudah ada tidak diubah
		if _, err := tx.Exec(ctx, `UPDATE org_invitations SET accepted_at = now(), accepted_by = $2 WHERE id = $1`, p.ID, userID); err != nil {
			return err
		}
		return audit(ctx, tx, p.OrgID, "invite.accepted", "invitation", p.ID.String(), nil, map[string]any{"role": p.Role, "already_member": alreadyMember})
	})
	switch {
	case err != nil:
		httpx.Internal(w, r, err)
	case p == nil:
		invalidInvite(w)
	case !p.active:
		// Tautan pernah sah tetapi sudah dipakai/dicabut/kedaluwarsa: dicatat, jawaban tetap generik.
		if err := audit(ctx, h.db, p.OrgID, "invite.expired_attempt", "invitation", p.ID.String(), nil, nil); err != nil {
			slog.ErrorContext(ctx, "audit undangan", "err", err)
		}
		invalidInvite(w)
	case mismatch:
		httpx.Error(w, http.StatusForbidden, "invitation_email_mismatch",
			"Undangan ini untuk "+MaskEmail(p.Email)+". Keluar, lalu masuk dengan email itu untuk menerimanya.")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"org_slug": p.OrgSlug, "org_name": p.OrgName, "role": p.Role, "already_member": alreadyMember})
	}
}

// CleanupInvitations: buang hash token undangan berumur > 30 hari (dipanggil worker).
func CleanupInvitations(ctx context.Context, db *pgxpool.Pool) (int64, error) {
	tag, err := db.Exec(ctx, `UPDATE org_invitations SET token_hash = NULL WHERE token_hash IS NOT NULL AND created_at < now() - interval '30 days'`)
	return tag.RowsAffected(), err
}
