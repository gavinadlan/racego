package queue

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Pass adalah bukti "sudah dipanggil" dari antrian: <payload>.<tanda tangan>, keduanya base64url.
// Payload = "<event_id>.<user_id>.<exp unix ms>", tanda tangan = HMAC-SHA256(payload, QUEUE_SECRET).
// Stateless, tapi tetap dicocokkan dengan himpunan pass aktif di Redis agar bisa dicabut.

var (
	errPassInvalid = errors.New("pass tidak sah")
	errPassExpired = errors.New("pass kedaluwarsa")
)

type PassSigner struct{ key []byte }

// NewPassSigner: kunci kosong hanya boleh di development (kunci bawaan yang diketahui umum).
func NewPassSigner(secret string, devMode bool) (*PassSigner, error) {
	if secret == "" {
		if !devMode {
			return nil, errors.New("QUEUE_SECRET wajib diisi di luar development (jalankan scripts/gen-secrets.sh)")
		}
		secret = "racego-dev-only-queue-secret"
	} else if len(secret) < 32 {
		return nil, errors.New("QUEUE_SECRET minimal 32 karakter")
	}
	return &PassSigner{key: []byte(secret)}, nil
}

func (s *PassSigner) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

func (s *PassSigner) Sign(eventID, userID string, exp time.Time) string {
	payload := eventID + "." + userID + "." + strconv.FormatInt(exp.UnixMilli(), 10)
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(payload)) + "." + enc.EncodeToString(s.mac(payload))
}

// Verify memeriksa tanda tangan, event, user, dan masa berlaku.
func (s *PassSigner) Verify(token, eventID, userID string, now time.Time) error {
	enc := base64.RawURLEncoding
	p64, sig64, ok := strings.Cut(token, ".")
	if !ok {
		return errPassInvalid
	}
	payload, err1 := enc.DecodeString(p64)
	sig, err2 := enc.DecodeString(sig64)
	if err1 != nil || err2 != nil || !hmac.Equal(sig, s.mac(string(payload))) {
		return errPassInvalid
	}
	parts := strings.Split(string(payload), ".")
	if len(parts) != 3 || parts[0] != eventID || parts[1] != userID {
		return errPassInvalid
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return errPassInvalid
	}
	if now.UnixMilli() >= exp {
		return errPassExpired
	}
	return nil
}
