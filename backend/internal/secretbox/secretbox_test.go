package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func newBox(t *testing.T) *Box {
	t.Helper()
	key := make([]byte, 32)
	rand.Read(key) //nolint:errcheck
	b, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRoundtrip(t *testing.T) {
	b := newBox(t)
	plain := []byte("%PDF-1.4 isi dokumen")
	sealed := b.Seal(plain)
	if !bytes.HasPrefix(sealed, []byte("v1:")) || bytes.Contains(sealed, plain) {
		t.Fatalf("format keluaran salah atau plaintext terlihat")
	}
	got, err := b.Open(sealed)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("Open: %v", err)
	}
	s := b.SealString("3201010101010001")
	if v, err := b.OpenString(s); err != nil || v != "3201010101010001" {
		t.Fatalf("OpenString: %v", err)
	}
	if v, err := b.OpenString(b.SealString("")); err != nil || v != "" {
		t.Fatalf("string kosong: %v", err)
	}
}

func TestNonceDiffers(t *testing.T) {
	b := newBox(t)
	if bytes.Equal(b.Seal([]byte("sama")), b.Seal([]byte("sama"))) {
		t.Fatal("dua enkripsi data sama menghasilkan ciphertext sama")
	}
	if b.SealString("x") == b.SealString("x") {
		t.Fatal("SealString deterministik")
	}
}

func TestTamperDetected(t *testing.T) {
	b := newBox(t)
	sealed := b.Seal([]byte("rahasia"))
	for i := 3; i < len(sealed); i++ {
		c := bytes.Clone(sealed)
		c[i] ^= 1
		if _, err := b.Open(c); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("byte %d diubah: err %v", i, err)
		}
	}
	if _, err := b.Open(sealed[:10]); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("data terpotong: %v", err)
	}
	s := b.SealString("1234567890")
	raw, _ := base64.StdEncoding.DecodeString(s[3:])
	raw[len(raw)-1] ^= 1
	if _, err := b.OpenString("v1:" + base64.StdEncoding.EncodeToString(raw)); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("teks diubah: %v", err)
	}
}

func TestWrongKey(t *testing.T) {
	a, b := newBox(t), newBox(t)
	_, err := b.Open(a.Seal([]byte("dokumen")))
	if !errors.Is(err, ErrDecrypt) {
		t.Fatalf("kunci salah: %v", err)
	}
	if err.Error() == "" || !bytes.Contains([]byte(err.Error()), []byte("KYC_ENC_KEY")) {
		t.Fatalf("pesan error tidak jelas: %q", err)
	}
	if _, err := b.OpenString(a.SealString("123")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("kunci salah (teks): %v", err)
	}
}

func TestUnknownVersion(t *testing.T) {
	b := newBox(t)
	sealed := b.Seal([]byte("x"))
	copy(sealed, "v9:")
	if _, err := b.Open(sealed); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("v9 biner: %v", err)
	}
	if _, err := b.OpenString("v2:" + b.SealString("x")[3:]); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("v2 teks: %v", err)
	}
	if _, err := b.Open([]byte("%PDF-1.4")); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("plaintext: %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	if _, err := FromEnv("", false); err == nil {
		t.Fatal("kunci kosong di produksi harus ditolak")
	}
	if _, err := FromEnv("", true); err != nil {
		t.Fatalf("development tanpa kunci: %v", err)
	}
	if _, err := FromEnv("bukan base64!!", false); err == nil {
		t.Fatal("base64 rusak diterima")
	}
	if _, err := FromEnv(base64.StdEncoding.EncodeToString(make([]byte, 16)), false); err == nil {
		t.Fatal("kunci 16 byte diterima")
	}
	if _, err := FromEnv(base64.StdEncoding.EncodeToString(make([]byte, 32)), false); err != nil {
		t.Fatalf("kunci valid: %v", err)
	}
}
