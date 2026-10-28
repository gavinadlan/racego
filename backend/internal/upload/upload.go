// Package upload menyimpan gambar unggahan mitra (poster event, logo penyelenggara).
//
// Gambar selalu di-decode lalu di-encode ulang: metadata (EXIF, termasuk lokasi GPS) hilang,
// dan file yang hanya menyamar sebagai gambar ditolak. Nama file = sha256 isi + ekstensi,
// jadi tidak bisa ditebak dan tidak bisa menimpa file lain.
package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const (
	MaxBytes = 3 << 20 // 3 MB
	MaxSide  = 4000    // piksel, sisi terpanjang
)

var (
	ErrNotImage  = errors.New("file harus gambar JPEG atau PNG")
	ErrTooLarge  = errors.New("ukuran file maksimal 3 MB")
	ErrTooBig    = errors.New("sisi terpanjang gambar maksimal 4.000 piksel")
	ErrNotFound  = errors.New("file tidak ditemukan")
	validNameRe  = regexp.MustCompile(`^[a-f0-9]{64}\.(jpg|png)$`)
	contentTypes = map[string]string{".jpg": "image/jpeg", ".png": "image/png"}
)

// ValidName: hanya nama hasil Process yang boleh dibaca (menutup path traversal).
func ValidName(name string) bool { return validNameRe.MatchString(name) }

// Process memeriksa isi file dan menghasilkan gambar bersih beserta nama filenya.
// Tipe ditentukan dari isi (magic bytes + decode), bukan dari ekstensi atau header klien.
func Process(data []byte) (name string, clean []byte, err error) {
	if len(data) > MaxBytes {
		return "", nil, ErrTooLarge
	}
	var ext string
	switch http.DetectContentType(data) {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	default:
		return "", nil, ErrNotImage
	}
	// Cek dimensi dari header dulu agar gambar raksasa (decompression bomb) tidak sempat di-decode.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return "", nil, ErrNotImage
	}
	if cfg.Width > MaxSide || cfg.Height > MaxSide {
		return "", nil, ErrTooBig
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", nil, ErrNotImage
	}
	var buf bytes.Buffer
	if ext == ".jpg" {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88})
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img)
	}
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]) + ext, buf.Bytes(), nil
}

// Storage menyimpan file berdasarkan nama. Implementasi sekarang disk lokal; nanti bisa S3/R2.
type Storage interface {
	Put(ctx context.Context, name string, data []byte) error
	// Open mengembalikan isi file; ErrNotFound bila tidak ada.
	Open(ctx context.Context, name string) (io.ReadSeekCloser, time.Time, error)
}

// Disk menyimpan file di satu folder lokal (UPLOAD_DIR).
type Disk struct{ Dir string }

func (d Disk) Put(_ context.Context, name string, data []byte) error {
	if !ValidName(name) {
		return ErrNotFound
	}
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(d.Dir, name)
	if _, err := os.Stat(path); err == nil {
		return nil // isi sama = nama sama; tidak perlu ditulis ulang
	}
	// Tulis ke file sementara lalu rename, agar pembaca tidak pernah melihat file setengah jadi.
	tmp, err := os.CreateTemp(d.Dir, ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (d Disk) Open(_ context.Context, name string) (io.ReadSeekCloser, time.Time, error) {
	if !ValidName(name) {
		return nil, time.Time{}, ErrNotFound
	}
	f, err := os.Open(filepath.Join(d.Dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, time.Time{}, ErrNotFound
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, time.Time{}, err
	}
	return f, st.ModTime(), nil
}
