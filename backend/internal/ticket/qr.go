// Package ticket menerbitkan tiket ber-QR dan menangani scan race pack oleh mitra.
package ticket

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Format QR: v1.<ticket_id>.<event_id>.<signature base64url>
// QR TIDAK berisi data pribadi — hanya ID dan tanda tangan. Siapa pun yang memegang
// kunci publik bisa memverifikasi keasliannya (termasuk scanner saat offline),
// tapi hanya server yang memegang kunci privat yang bisa membuat QR baru.
const qrVersion = "v1"

var ErrInvalidQR = errors.New("QR tidak valid")

type Signer struct {
	priv ed25519.PrivateKey
}

// NewSigner membuat signer dari seed base64 (32 byte). Seed kosong = kunci dev deterministik.
func NewSigner(seedB64 string, devMode bool) (*Signer, error) {
	if seedB64 == "" {
		if !devMode {
			return nil, errors.New("QR_PRIVATE_KEY wajib diisi di luar development (jalankan `make keygen`)")
		}
		seed := sha256.Sum256([]byte("racego-dev-only-qr-key"))
		return &Signer{priv: ed25519.NewKeyFromSeed(seed[:])}, nil
	}
	seed, err := base64.StdEncoding.DecodeString(seedB64)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("QR_PRIVATE_KEY harus base64 dari %d byte", ed25519.SeedSize)
	}
	return &Signer{priv: ed25519.NewKeyFromSeed(seed)}, nil
}

func (s *Signer) Sign(ticketID, eventID uuid.UUID) string {
	msg := qrVersion + "." + ticketID.String() + "." + eventID.String()
	sig := ed25519.Sign(s.priv, []byte(msg))
	return msg + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (s *Signer) PublicKey() ed25519.PublicKey { return s.priv.Public().(ed25519.PublicKey) }

func (s *Signer) PublicKeyBase64() string { return base64.StdEncoding.EncodeToString(s.PublicKey()) }

// Verify memeriksa tanda tangan dan mengembalikan ID tiket & event dari token QR.
func Verify(pub ed25519.PublicKey, token string) (ticketID, eventID uuid.UUID, err error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 4 || parts[0] != qrVersion {
		return uuid.Nil, uuid.Nil, ErrInvalidQR
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return uuid.Nil, uuid.Nil, ErrInvalidQR
	}
	msg := strings.Join(parts[:3], ".")
	if !ed25519.Verify(pub, []byte(msg), sig) {
		return uuid.Nil, uuid.Nil, ErrInvalidQR
	}
	if ticketID, err = uuid.Parse(parts[1]); err != nil {
		return uuid.Nil, uuid.Nil, ErrInvalidQR
	}
	if eventID, err = uuid.Parse(parts[2]); err != nil {
		return uuid.Nil, uuid.Nil, ErrInvalidQR
	}
	return ticketID, eventID, nil
}

// GenerateSeed membuat seed acak base64 untuk QR_PRIVATE_KEY.
func GenerateSeed() (string, string, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv.Seed()), base64.StdEncoding.EncodeToString(pub), nil
}
