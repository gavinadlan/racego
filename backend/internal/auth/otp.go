package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/ratelimit"
)

const (
	OTPTTL         = 10 * time.Minute
	OTPMaxAttempts = 5
	ResendAfter    = 60 * time.Second
	maxPerHour     = 5  // kode per email per jam
	maxPerIPHour   = 20 // permintaan kode per IP per jam
)

var (
	ErrOTPExpired      = errors.New("kode kedaluwarsa atau sudah dipakai")
	ErrTooManyAttempts = errors.New("terlalu banyak percobaan salah")
)

// ErrOTPInvalid: kode salah, masih boleh mencoba Remaining kali lagi.
type ErrOTPInvalid struct{ Remaining int }

func (e ErrOTPInvalid) Error() string {
	return fmt.Sprintf("kode salah, sisa %d percobaan", e.Remaining)
}

// ErrRateLimited: terlalu sering meminta kode; boleh lagi setelah RetryAfter.
type ErrRateLimited = ratelimit.Exceeded

// NormalizeEmail: huruf kecil tanpa spasi; false bila bukan alamat email polos yang valid.
func NormalizeEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	a, err := mail.ParseAddress(s)
	return s, err == nil && a.Address == s
}

// OTPStore menyimpan kode masuk sekali pakai di Redis: otp:{email} = {h: sha256(kode+email), n: salah}.
type OTPStore struct{ rdb *redis.Client }

func NewOTPStore(rdb *redis.Client) *OTPStore { return &OTPStore{rdb: rdb} }

func otpKey(email string) string { return "otp:" + email }

func otpHash(code, email string) string {
	h := sha256.Sum256([]byte(code + email))
	return hex.EncodeToString(h[:])
}

// LimitIP membatasi permintaan kode per alamat IP.
func (s *OTPStore) LimitIP(ctx context.Context, ip string) error {
	return s.limit(ctx, "otp_rl:ip:"+ip, maxPerIPHour, time.Hour)
}

// Issue membuat kode 6 digit baru (menggantikan kode lama) setelah lolos batas kirim ulang.
func (s *OTPStore) Issue(ctx context.Context, email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if err := s.limit(ctx, "otp_rl:min:"+email, 1, ResendAfter); err != nil {
		return "", err
	}
	if err := s.limit(ctx, "otp_rl:hour:"+email, maxPerHour, time.Hour); err != nil {
		return "", err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	key := otpKey(email)
	_, err = s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, key, "h", otpHash(code, email), "n", 0)
		p.Expire(ctx, key, OTPTTL)
		return nil
	})
	return code, err
}

// Discard menghapus kode (mis. email gagal terkirim).
func (s *OTPStore) Discard(ctx context.Context, email string) error {
	return s.rdb.Del(ctx, otpKey(strings.ToLower(strings.TrimSpace(email)))).Err()
}

// Tambah hitungan salah hanya bila kode masih ada, agar tidak tercipta key tanpa TTL.
var failScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return -1 end
local n = redis.call('HINCRBY', KEYS[1], 'n', 1)
if n >= tonumber(ARGV[1]) then redis.call('DEL', KEYS[1]) end
return n`)

// Verify mencocokkan kode. Berhasil = kode langsung dihapus (sekali pakai).
func (s *OTPStore) Verify(ctx context.Context, email, code string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	key := otpKey(email)
	stored, err := s.rdb.HGet(ctx, key, "h").Result()
	if errors.Is(err, redis.Nil) {
		return ErrOTPExpired
	}
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(otpHash(code, email))) == 1 {
		deleted, err := s.rdb.Del(ctx, key).Result()
		if err != nil {
			return err
		}
		if deleted == 0 { // dipakai request lain di saat yang sama
			return ErrOTPExpired
		}
		// Kode sudah terpakai: jeda kirim ulang tidak perlu lagi (keluar lalu langsung masuk lagi).
		// Batas per jam tetap berlaku.
		return s.rdb.Del(ctx, "otp_rl:min:"+email).Err()
	}
	n, err := failScript.Run(ctx, s.rdb, []string{key}, OTPMaxAttempts).Int()
	switch {
	case err != nil:
		return err
	case n < 0:
		return ErrOTPExpired
	case n >= OTPMaxAttempts:
		return ErrTooManyAttempts
	}
	return ErrOTPInvalid{Remaining: OTPMaxAttempts - n}
}

func (s *OTPStore) limit(ctx context.Context, key string, allowed int64, window time.Duration) error {
	return ratelimit.Allow(ctx, s.rdb, key, allowed, window)
}
