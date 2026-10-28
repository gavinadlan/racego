package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	CookieName = "racego_session"
	SessionTTL = 30 * 24 * time.Hour
)

var ErrNoSession = errors.New("sesi tidak ditemukan atau kedaluwarsa")

// SessionStore menyimpan sesi login di Postgres. Token hanya ada di cookie browser.
type SessionStore struct{ db *pgxpool.Pool }

func NewSessionStore(db *pgxpool.Pool) *SessionStore { return &SessionStore{db: db} }

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Create membuat sesi baru dan mengembalikan token acak 32 byte untuk cookie.
func (s *SessionStore) Create(ctx context.Context, userID uuid.UUID, ua, ip string) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(SessionTTL)
	_, err := s.db.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, expires_at, user_agent, ip) VALUES ($1, $2, $3, $4, $5)`,
		userID, tokenHash(token), expires, ua, ip)
	return token, expires, err
}

// Lookup mengembalikan pemilik sesi yang belum kedaluwarsa.
// last_seen_at diperbarui paling sering sekali per 5 menit agar tidak menulis di setiap request.
func (s *SessionStore) Lookup(ctx context.Context, token string) (uuid.UUID, error) {
	var userID uuid.UUID
	err := s.db.QueryRow(ctx, `
		WITH s AS (
			SELECT id, user_id, last_seen_at FROM sessions WHERE token_hash = $1 AND expires_at > now()
		), touch AS (
			UPDATE sessions SET last_seen_at = now()
			FROM s WHERE sessions.id = s.id AND s.last_seen_at < now() - interval '5 minutes'
		)
		SELECT user_id FROM s`, tokenHash(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNoSession
	}
	return userID, err
}

func (s *SessionStore) Delete(ctx context.Context, token string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash(token))
	return err
}

// Cookie host-only (tanpa Domain), Secure di luar development.
func setCookie(w http.ResponseWriter, token string, expires time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", MaxAge: int(time.Until(expires).Seconds()),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}
