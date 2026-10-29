package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/notify"
)

type Handler struct {
	db       *pgxpool.Pool
	otps     *OTPStore
	sessions *SessionStore
	mailer   *notify.Mailer
	devMode  bool // cookie tanpa Secure (localhost http) & kode OTP dicatat ke log
}

func NewHandler(db *pgxpool.Pool, otps *OTPStore, sessions *SessionStore, mailer *notify.Mailer, devMode bool) *Handler {
	return &Handler{db: db, otps: otps, sessions: sessions, mailer: mailer, devMode: devMode}
}

// RequestOTP: POST /v1/auth/otp/request {"email"} — kirim kode 6 digit ke email.
// Selalu sukses untuk email valid, tidak membocorkan apakah akunnya sudah ada.
func (h *Handler) RequestOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	email, ok := NormalizeEmail(in.Email)
	if !ok {
		httpx.Error(w, http.StatusUnprocessableEntity, "invalid_email", "Format email tidak valid")
		return
	}
	if err := h.otps.LimitIP(r.Context(), ClientIP(r)); err != nil {
		h.otpError(w, r, err)
		return
	}
	code, err := h.otps.Issue(r.Context(), email)
	if err != nil {
		h.otpError(w, r, err)
		return
	}
	if h.devMode {
		slog.Info("kode OTP (dev)", "email", email, "code", code)
	}
	subject, body := notify.OTPEmail(code)
	if err := h.mailer.Send(email, subject, body); err != nil {
		slog.ErrorContext(r.Context(), "kirim email OTP gagal", "err", err)
		_ = h.otps.Discard(context.WithoutCancel(r.Context()), email)
		httpx.Error(w, http.StatusBadGateway, "mail_failed", "Email berisi kode gagal dikirim. Coba lagi sebentar lagi.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "resend_after": int(ResendAfter.Seconds())})
}

// VerifyOTP: POST /v1/auth/otp/verify {"email","code"} — cocokkan kode, buat akun bila baru, lalu set cookie sesi.
func (h *Handler) VerifyOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	email, ok := NormalizeEmail(in.Email)
	if !ok {
		httpx.Error(w, http.StatusUnprocessableEntity, "invalid_email", "Format email tidak valid")
		return
	}
	if err := h.otps.Verify(r.Context(), email, strings.TrimSpace(in.Code)); err != nil {
		h.otpError(w, r, err)
		return
	}

	ctx := r.Context()
	isNew := true
	var userID uuid.UUID
	err := h.db.QueryRow(ctx, `INSERT INTO users (email, name) VALUES ($1, $2) ON CONFLICT (email) DO NOTHING RETURNING id`,
		email, nameFromEmail(email)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		isNew = false
		err = h.db.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&userID)
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	token, expires, err := h.sessions.Create(ctx, userID, r.UserAgent(), ClientIP(r))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	p, err := h.profile(ctx, userID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	setCookie(w, token, expires, !h.devMode)
	httpx.JSON(w, http.StatusOK, struct {
		Profile
		IsNew bool `json:"is_new"`
	}{p, isNew})
}

// Logout: POST /v1/auth/logout — hapus sesi & cookie. Tetap 204 walau belum login.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		if err := h.sessions.Delete(r.Context(), c.Value); err != nil {
			httpx.Internal(w, r, err)
			return
		}
	}
	clearCookie(w, !h.devMode)
	w.WriteHeader(http.StatusNoContent)
}

type Membership struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type Profile struct {
	ID    uuid.UUID    `json:"id"`
	Name  string       `json:"name"`
	Email string       `json:"email"`
	Phone *string      `json:"phone"`
	Orgs  []Membership `json:"orgs"`
	// Super admin platform (diberikan lewat perintah migrate superadmin, tidak ada endpoint web).
	IsSuperAdmin bool `json:"is_super_admin"`
}

func (h *Handler) profile(ctx context.Context, userID uuid.UUID) (Profile, error) {
	p := Profile{ID: userID}
	if err := h.db.QueryRow(ctx, `SELECT name, email, phone, is_super_admin FROM users WHERE id = $1`, userID).Scan(&p.Name, &p.Email, &p.Phone, &p.IsSuperAdmin); err != nil {
		return p, err
	}
	rows, err := h.db.Query(ctx, `
		SELECT o.slug, o.name, m.role FROM org_members m JOIN organizations o ON o.id = m.org_id
		WHERE m.user_id = $1 ORDER BY o.name`, userID)
	if err != nil {
		return p, err
	}
	p.Orgs, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Membership])
	return p, err
}

// Session: GET /v1/auth/session — {"me": profil} bila sudah masuk, {"me": null} bila belum.
// Frontend memanggilnya di setiap halaman; selalu 200 agar pengunjung anonim tidak memicu 401 di konsol browser.
func (h *Handler) Session(w http.ResponseWriter, r *http.Request) {
	var me *Profile
	if userID, ok := UserID(r.Context()); ok {
		p, err := h.profile(r.Context(), userID)
		switch {
		case err == nil:
			me = &p
		case !errors.Is(err, pgx.ErrNoRows):
			httpx.Internal(w, r, err)
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"me": me})
}

// Me: GET /v1/me — profil singkat + daftar mitra tempat user menjadi anggota.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID, _ := UserID(r.Context())
	p, err := h.profile(r.Context(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusUnauthorized, "unknown_user", "User tidak ditemukan")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

var phoneRe = regexp.MustCompile(`^(\+62|62|0)8[0-9]{7,12}$`)

// NormalizePhone menerima 08…, 628…, +628… (spasi & tanda hubung diabaikan) dan mengembalikan bentuk 08….
func NormalizePhone(s string) (string, bool) {
	s = strings.NewReplacer(" ", "", "-", "").Replace(s)
	m := phoneRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return "0" + s[len(m[1]):], true
}

// UpdateMe: PATCH /v1/me {"name","phone"} — phone kosong = dihapus.
func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Phone string `json:"phone"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n < 2 || n > 100 {
		httpx.Error(w, http.StatusUnprocessableEntity, "invalid_name", "Nama harus 2 sampai 100 karakter")
		return
	}
	var phone *string
	if strings.TrimSpace(in.Phone) != "" {
		p, ok := NormalizePhone(in.Phone)
		if !ok {
			httpx.Error(w, http.StatusUnprocessableEntity, "invalid_phone", "Nomor HP harus nomor Indonesia, mis. 081234567890")
			return
		}
		phone = &p
	}
	userID, _ := UserID(r.Context())
	if _, err := h.db.Exec(r.Context(), `UPDATE users SET name = $2, phone = $3 WHERE id = $1`, userID, name, phone); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.Me(w, r)
}

func (h *Handler) otpError(w http.ResponseWriter, r *http.Request, err error) {
	var invalid ErrOTPInvalid
	var limited ErrRateLimited
	switch {
	case errors.As(err, &limited):
		secs := int(math.Ceil(limited.RetryAfter.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		httpx.JSON(w, http.StatusTooManyRequests, map[string]any{
			"error":       "too_many_requests",
			"message":     fmt.Sprintf("Terlalu sering meminta kode. Coba lagi dalam %s.", waitText(secs)),
			"retry_after": secs,
		})
	case errors.As(err, &invalid):
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"error":     "otp_invalid",
			"message":   fmt.Sprintf("Kode salah. Sisa %d percobaan lagi.", invalid.Remaining),
			"remaining": invalid.Remaining,
		})
	case errors.Is(err, ErrTooManyAttempts):
		httpx.Error(w, http.StatusTooManyRequests, "too_many_attempts", "Kode salah 5 kali, jadi kode sudah hangus. Minta kode baru.")
	case errors.Is(err, ErrOTPExpired):
		httpx.Error(w, http.StatusGone, "otp_expired", "Kode sudah kedaluwarsa atau sudah dipakai. Minta kode baru.")
	default:
		httpx.Internal(w, r, err)
	}
}

func waitText(secs int) string {
	if secs < 60 {
		return fmt.Sprintf("%d detik", secs)
	}
	return fmt.Sprintf("%d menit", (secs+59)/60)
}

// nameFromEmail: "budi.santoso@x.com" → "Budi.santoso". Pengguna bisa menggantinya di halaman akun.
func nameFromEmail(email string) string {
	local, _, _ := strings.Cut(email, "@")
	r, size := utf8.DecodeRuneInString(local)
	return strings.ToUpper(string(r)) + local[size:]
}

// ClientIP: alamat klien (sudah diganti middleware realIP bila ada proxy tepercaya).
func ClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
