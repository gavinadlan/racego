package upload

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func testImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, 0, color.RGBA{R: uint8(x), G: 90, B: 35, A: 255})
	}
	return img
}

// jpegWithEXIF membuat JPEG lalu menyisipkan segmen APP1 EXIF berisi "lokasi GPS" palsu setelah SOI.
func jpegWithEXIF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, testImage(40, 30), nil); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("Exif\x00\x00"), []byte("GPS-LOKASI-RAHASIA")...)
	seg := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	b := buf.Bytes()
	out := append([]byte{}, b[:2]...)
	out = append(out, seg...)
	out = append(out, payload...)
	return append(out, b[2:]...)
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(w, h)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestProcess(t *testing.T) {
	exif := jpegWithEXIF(t)
	if !bytes.Contains(exif, []byte("GPS-LOKASI-RAHASIA")) {
		t.Fatal("fixture harus mengandung EXIF")
	}
	name, clean, err := Process(exif)
	if err != nil {
		t.Fatalf("JPEG valid ditolak: %v", err)
	}
	if !strings.HasSuffix(name, ".jpg") || !ValidName(name) {
		t.Fatalf("nama %q", name)
	}
	if bytes.Contains(clean, []byte("Exif")) || bytes.Contains(clean, []byte("GPS-LOKASI-RAHASIA")) {
		t.Fatal("EXIF harus hilang setelah encode ulang")
	}

	name, _, err = Process(pngBytes(t, 20, 20))
	if err != nil || !strings.HasSuffix(name, ".png") {
		t.Fatalf("PNG valid: %q %v", name, err)
	}

	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"teks bernama .jpg", []byte("ini bukan gambar, hanya teks biasa"), ErrNotImage},
		{"header JPEG palsu", append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte("x"), 600)...), ErrNotImage},
		{"GIF", []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), ErrNotImage},
		{"lebih dari 3 MB", append(pngBytes(t, 2, 2), make([]byte, MaxBytes)...), ErrTooLarge},
		{"lebih dari 4000 px", pngBytes(t, MaxSide+1, 1), ErrTooBig},
	}
	for _, c := range cases {
		if _, _, err := Process(c.data); err != c.want {
			t.Errorf("%s: dapat %v, mau %v", c.name, err, c.want)
		}
	}
}

func TestServeName(t *testing.T) {
	dir := t.TempDir()
	h := NewHandler(Disk{Dir: dir})
	name, clean, err := Process(pngBytes(t, 3, 3))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.Put(context.Background(), name, clean); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(filepath.Dir(dir), "rahasia.txt"), []byte("x"), 0o644) //nolint:errcheck
	r := chi.NewRouter()
	r.Get("/uploads/{name}", h.Serve)

	cases := map[string]int{
		"/uploads/" + name:                        200,
		"/uploads/" + strings.ToUpper(name):       404,
		"/uploads/" + name[:64] + ".gif":          404,
		"/uploads/..%2Frahasia.txt":               404,
		"/uploads/" + name[:64] + ".jpg":          404, // nama valid tapi tidak ada
		"/uploads/" + name[:63] + ".png":          404,
		"/uploads/%2e%2e%2f%2e%2e%2fetc%2fpasswd": 404,
	}
	for path, want := range cases {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, mau %d", path, rec.Code, want)
		}
		if want == 200 {
			if rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Type") != "image/png" {
				t.Errorf("header salah: %v", rec.Header())
			}
		}
	}
}

func TestUploadHandler(t *testing.T) {
	h := NewHandler(Disk{Dir: t.TempDir()})
	send := func(kind string, data []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", "foto.jpg")
		fw.Write(data) //nolint:errcheck
		mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/?kind="+kind, &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		h.Upload(rec, req)
		return rec
	}
	if rec := send("cover", jpegWithEXIF(t)); rec.Code != 201 || !strings.Contains(rec.Body.String(), `"/uploads/`) {
		t.Fatalf("upload JPEG: %d %s", rec.Code, rec.Body)
	}
	if rec := send("cover", []byte("teks")); rec.Code != 422 || !strings.Contains(rec.Body.String(), "JPEG atau PNG") {
		t.Fatalf("teks: %d %s", rec.Code, rec.Body)
	}
	if rec := send("logo", make([]byte, MaxBytes+200<<10)); rec.Code != 413 {
		t.Fatalf("> 3 MB: %d %s", rec.Code, rec.Body)
	}
	if rec := send("avatar", pngBytes(t, 2, 2)); rec.Code != 422 {
		t.Fatalf("kind salah: %d", rec.Code)
	}
}
