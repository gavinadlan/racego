package kyc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/gavinadlan/racego/backend/internal/secretbox"
	"github.com/gavinadlan/racego/backend/internal/upload"
)

// DocError: dokumen ditolak; Message aman ditampilkan ke pengguna.
type DocError struct{ Code, Message string }

func (e DocError) Error() string { return e.Message }

var (
	errDocTooLarge = DocError{"file_too_large", "Ukuran berkas maksimal 5 MB."}
	errDocType     = DocError{"invalid_document", "Berkas harus JPG, PNG, atau PDF. Isi berkas tidak dikenali sebagai salah satunya."}
	errPDFBroken   = DocError{"invalid_document", "PDF tidak lengkap atau rusak (tidak diakhiri %%EOF). Simpan ulang PDF-nya lalu unggah lagi."}
	errPDFEncrypt  = DocError{"pdf_encrypted", "PDF terkunci password/terenkripsi. Unggah PDF tanpa password."}
	errPDFActive   = DocError{"pdf_active_content", "PDF berisi JavaScript atau aksi otomatis sehingga ditolak. Cetak ulang ke PDF biasa atau unggah foto/scan JPG/PNG."}
	errImageTooBig = DocError{"image_too_big", "Sisi terpanjang gambar maksimal 4.000 piksel."}
)

// ValidateDocument menentukan tipe dari ISI berkas (bukan ekstensi/header klien).
// Gambar di-encode ulang (EXIF/GPS hilang); PDF disimpan apa adanya, tidak pernah diproses/dieksekusi.
func ValidateDocument(data []byte) (contentType string, clean []byte, err error) {
	if len(data) > MaxDocBytes {
		return "", nil, errDocTooLarge
	}
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png":
		ext, clean, err := upload.Reencode(data)
		switch {
		case errors.Is(err, upload.ErrTooBig):
			return "", nil, errImageTooBig
		case err != nil:
			return "", nil, errDocType
		}
		if ext == ".png" {
			return "image/png", clean, nil
		}
		return "image/jpeg", clean, nil
	case "application/pdf":
		if err := checkPDF(data); err != nil {
			return "", nil, err
		}
		return "application/pdf", data, nil
	}
	return "", nil, errDocType
}

var pdfNameEscape = regexp.MustCompile(`#([0-9A-Fa-f]{2})`)

// checkPDF: header %PDF-, %%EOF di akhir, tanpa enkripsi dan tanpa konten aktif.
// ponytail: pencarian byte sederhana (nama ber-escape #xx dinormalkan). Objek di dalam stream
// terkompresi tidak diperiksa; pertahanan utamanya PDF hanya dipratinjau di iframe sandbox.
func checkPDF(data []byte) error {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return errDocType
	}
	tail := data[max(0, len(data)-1024):]
	if !bytes.Contains(tail, []byte("%%EOF")) {
		return errPDFBroken
	}
	norm := pdfNameEscape.ReplaceAllFunc(data, func(m []byte) []byte {
		var b byte
		fmt.Sscanf(string(m[1:]), "%02x", &b) //nolint:errcheck
		return []byte{b}
	})
	if bytes.Contains(norm, []byte("/Encrypt")) {
		return errPDFEncrypt
	}
	for _, k := range []string{"/JavaScript", "/JS", "/Launch"} {
		if bytes.Contains(norm, []byte(k)) {
			return errPDFActive
		}
	}
	return nil
}

var extFor = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "application/pdf": ".pdf"}

// SafeName membuat nama tampilan dari nama asli klien: tanpa folder, tanpa karakter kontrol,
// maks 80 karakter, ekstensi sesuai tipe hasil validasi.
func SafeName(original, contentType string) string {
	base := filepath.Base(strings.ReplaceAll(original, "\\", "/"))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(" ._-()", r) {
			return r
		}
		return '_'
	}, base)
	base = strings.Trim(base, " ._")
	if r := []rune(base); len(r) > 80 {
		base = string(r[:80])
	}
	if base == "" {
		base = "dokumen"
	}
	return base + extFor[contentType]
}

// ===== Penyimpanan berkas privat terenkripsi =====

var keyRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.bin$`)

// Files menyimpan dokumen di KYC_DIR sebagai <uuid acak>.bin berisi ciphertext secretbox.
// Tidak ada rute statis ke folder ini; berkas hanya dibaca handler yang sudah memeriksa izin.
type Files struct {
	Dir string
	Box *secretbox.Box
}

func (f Files) Put(plain []byte) (string, error) {
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return "", err
	}
	key := uuid.NewString() + ".bin"
	tmp, err := os.CreateTemp(f.Dir, ".kyc-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(f.Box.Seal(plain)); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return key, os.Rename(tmp.Name(), filepath.Join(f.Dir, key))
}

// Read mendekripsi seluruh berkas. GCM baru bisa memastikan keaslian setelah semua byte
// terbaca, jadi isi (maks 5 MB) didekripsi di memori sebelum dialirkan ke klien.
func (f Files) Read(key string) ([]byte, error) {
	if !keyRe.MatchString(key) {
		return nil, errNotFound
	}
	sealed, err := os.ReadFile(filepath.Join(f.Dir, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return f.Box.Open(sealed)
}

func (f Files) Delete(key string) error {
	if !keyRe.MatchString(key) {
		return nil
	}
	err := os.Remove(filepath.Join(f.Dir, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
