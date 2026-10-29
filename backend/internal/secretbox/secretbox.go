// Package secretbox mengenkripsi data sensitif (dokumen KYC, NIK, nomor rekening, NPWP) dengan
// AES-256-GCM. Keluaran berawalan versi kunci ("v1:") agar kunci bisa diganti nanti tanpa
// kehilangan data lama: versi baru ditambahkan, versi lama tetap bisa dibuka.
//
// Format biner (berkas): "v1:" + nonce (12 byte acak) + ciphertext+tag.
// Format teks (kolom DB): "v1:" + base64(nonce + ciphertext+tag).
package secretbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

const version = "v1"

var (
	// ErrDecrypt: GCM menolak data, artinya kunci salah atau data diubah/rusak. Tidak pernah
	// mengembalikan plaintext sebagian.
	ErrDecrypt        = errors.New("data terenkripsi tidak bisa dibuka: KYC_ENC_KEY tidak cocok atau data rusak")
	ErrUnknownVersion = errors.New("versi kunci enkripsi tidak dikenal")
)

// devKey hanya untuk development (diketahui umum, jangan dipakai untuk data asli).
var devKey = []byte("racego-dev-only-kyc-key-32-bytes")

type Box struct{ aead cipher.AEAD }

// New membuat Box dari kunci 32 byte.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("kunci enkripsi harus 32 byte, dapat %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// FromEnv membaca KYC_ENC_KEY (base64, 32 byte). Kosong hanya boleh di development (kunci bawaan + peringatan).
func FromEnv(b64 string, devMode bool) (*Box, error) {
	if b64 == "" {
		if !devMode {
			return nil, errors.New("KYC_ENC_KEY wajib diisi di luar development (jalankan scripts/gen-secrets.sh, lalu cadangkan kuncinya terpisah dari backup data)")
		}
		slog.Warn("KYC_ENC_KEY kosong → memakai kunci development bawaan; JANGAN dipakai untuk dokumen asli")
		return New(devKey)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, errors.New("KYC_ENC_KEY harus base64 (buat dengan: openssl rand -base64 32)")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("KYC_ENC_KEY harus 32 byte setelah decode base64, dapat %d", len(key))
	}
	return New(key)
}

// Seal mengenkripsi data biner. Nonce acak per panggilan.
func (b *Box) Seal(plain []byte) []byte {
	out := make([]byte, 0, len(version)+1+b.aead.NonceSize()+len(plain)+b.aead.Overhead())
	out = append(out, version+":"...)
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err) // crypto/rand tidak pernah gagal di platform yang didukung
	}
	out = append(out, nonce...)
	return b.aead.Seal(out, nonce, plain, nil)
}

// Open membuka hasil Seal.
func (b *Box) Open(data []byte) ([]byte, error) {
	prefix := []byte(version + ":")
	if !bytes.HasPrefix(data, prefix) {
		return nil, ErrUnknownVersion
	}
	data = data[len(prefix):]
	n := b.aead.NonceSize()
	if len(data) < n+b.aead.Overhead() {
		return nil, ErrDecrypt
	}
	plain, err := b.aead.Open(nil, data[:n], data[n:], nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}

// SealString untuk kolom teks di database.
func (b *Box) SealString(plain string) string {
	sealed := b.Seal([]byte(plain))
	return version + ":" + base64.StdEncoding.EncodeToString(sealed[len(version)+1:])
}

func (b *Box) OpenString(s string) (string, error) {
	ver, body, ok := strings.Cut(s, ":")
	if !ok || ver != version {
		return "", ErrUnknownVersion
	}
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return "", ErrDecrypt
	}
	plain, err := b.Open(append([]byte(version+":"), raw...))
	return string(plain), err
}
