package kyc

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gavinadlan/racego/backend/internal/secretbox"
)

// SamplePDF membuat PDF satu halaman yang valid (xref benar) berisi teks; dipakai tes.
func SamplePDF(text string, extra string) []byte {
	var b bytes.Buffer
	var offs []int
	obj := func(s string) {
		offs = append(offs, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offs), s)
	}
	b.WriteString("%PDF-1.4\n")
	obj("<< /Type /Catalog /Pages 2 0 R " + extra + ">>")
	obj("<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	stream := "BT /F1 16 Tf 20 100 Td (" + text + ") Tj ET"
	obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return b.Bytes()
}

// jpegWithGPS: JPEG valid dengan segmen APP1 Exif berisi penanda GPS.
func jpegWithGPS(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	payload := []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08GPSLatitude-6.2000;GPSLongitude=106.8166")
	seg := append([]byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}, payload...)
	data := buf.Bytes()
	return append(append(append([]byte{}, data[:2]...), seg...), data[2:]...)
}

func TestValidateDocumentRejects(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"teks berekstensi .pdf/.jpg", []byte("ini cuma teks biasa, bukan dokumen"), errDocType},
		{"exe diganti nama", append([]byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00\x00\x00\xff\xff"), make([]byte, 200)...), errDocType},
		{"pdf tanpa %%EOF", bytes.TrimSuffix(SamplePDF("x", ""), []byte("%%EOF\n")), errPDFBroken},
		{"pdf ber-JavaScript", SamplePDF("x", "/OpenAction << /S /JavaScript /JS (app.alert(1)) >> "), errPDFActive},
		{"pdf /JS ber-escape", SamplePDF("x", "/OpenAction << /S /Java#53cript /J#53 (1) >> "), errPDFActive},
		{"pdf /Launch", SamplePDF("x", "/OpenAction << /S /Launch /F (calc.exe) >> "), errPDFActive},
		{"pdf terenkripsi", SamplePDF("x", "/Encrypt 9 0 R "), errPDFEncrypt},
		{"lebih dari 5 MB", append(SamplePDF("x", ""), make([]byte, MaxDocBytes)...), errDocTooLarge},
		{"jpeg rusak", []byte("\xff\xd8\xff\xe0 bukan jpeg"), errDocType},
	}
	for _, c := range cases {
		if _, _, err := ValidateDocument(c.data); !errors.Is(err, c.want) {
			t.Errorf("%s: err %v, mau %v", c.name, err, c.want)
		}
	}
}

func TestValidateDocumentAccepts(t *testing.T) {
	pdf := SamplePDF("CONTOH - TIDAK ASLI", "")
	ct, clean, err := ValidateDocument(pdf)
	if err != nil || ct != "application/pdf" || !bytes.Equal(clean, pdf) {
		t.Fatalf("PDF valid harus tersimpan apa adanya: %s %v", ct, err)
	}
	raw := jpegWithGPS(t)
	if !bytes.Contains(raw, []byte("GPSLatitude")) {
		t.Fatal("fixture tanpa EXIF")
	}
	ct, clean, err = ValidateDocument(raw)
	if err != nil || ct != "image/jpeg" {
		t.Fatalf("JPEG: %s %v", ct, err)
	}
	if bytes.Contains(clean, []byte("Exif")) || bytes.Contains(clean, []byte("GPS")) {
		t.Fatal("EXIF/GPS masih ada setelah encode ulang")
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"../../etc/passwd":                "passwd.pdf",
		`C:\Users\x\KTP Budi.JPG`:         "KTP Budi.pdf",
		"akta\x00<script>.exe":            "akta__script.pdf",
		"":                                "dokumen.pdf",
		strings.Repeat("a", 200) + ".pdf": strings.Repeat("a", 80) + ".pdf",
	} {
		if got := SafeName(in, "application/pdf"); got != want {
			t.Errorf("SafeName(%q) = %q, mau %q", in, got, want)
		}
	}
	if got := SafeName("foto.pdf", "image/png"); got != "foto.png" {
		t.Errorf("ekstensi harus mengikuti tipe hasil validasi: %q", got)
	}
}

func TestNameMatch(t *testing.T) {
	cases := []struct {
		t                  OrgType
		holder, pic, legal string
		ok                 bool
	}{
		{TypePerorangan, "BUDI SANTOSO", "Dr. Budi  Santoso, S.Kom.", "", true},
		{TypePerorangan, "Budi Santosa", "Budi Santoso", "", false},
		{TypeKomunitas, "Komunitas Lari Bogor", "Sari", "Komunitas Lari  Bogor", true},
		{TypeKomunitas, "Sari Dewi", "Ibu Sari Dewi", "Komunitas", true},
		{TypeBadanUsaha, "PT. Lari Sehat", "Budi", "PT Lari Sehat", true},
		{TypeBadanUsaha, "Budi", "Budi", "PT Lari Sehat", false},
	}
	for _, c := range cases {
		if m := matchHolder(c.t, c.holder, c.pic, c.legal); m.OK != c.ok {
			t.Errorf("%s %q vs %q/%q: ok=%v", c.t, c.holder, c.pic, c.legal, m.OK)
		}
	}
}

func TestMatrix(t *testing.T) {
	want := map[OrgType][2]string{
		TypePerorangan: {"ktp_pic", "npwp"},
		TypeKomunitas:  {"ktp_pic,surat_keterangan", "npwp"},
		TypeBadanUsaha: {"ktp_pic,npwp,akta_pendirian,sk_kemenkumham", "nib,surat_kuasa"},
	}
	join := func(ks []DocKind) string {
		s := make([]string, len(ks))
		for i, k := range ks {
			s[i] = string(k)
		}
		return strings.Join(s, ",")
	}
	if len(Types) != 3 {
		t.Fatalf("jumlah jenis %d", len(Types))
	}
	for _, r := range Types {
		if w := want[r.Type]; join(r.Required) != w[0] || join(r.Optional) != w[1] {
			t.Errorf("%s: wajib %v opsional %v", r.Type, r.Required, r.Optional)
		}
		for _, k := range append(r.Required, r.Optional...) {
			if DocLabels[k] == "" {
				t.Errorf("label %s kosong", k)
			}
		}
	}
}

func TestFilesEncryptedAtRest(t *testing.T) {
	box, _ := secretbox.New(bytes.Repeat([]byte{7}, 32))
	f := Files{Dir: t.TempDir(), Box: box}
	pdf := SamplePDF("CONTOH", "")
	key, err := f.Put(pdf)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(f.Dir, key))
	if bytes.HasPrefix(raw, []byte("%PDF-")) || bytes.Contains(raw, []byte("CONTOH")) {
		t.Fatal("berkas tersimpan sebagai plaintext")
	}
	if got, err := f.Read(key); err != nil || !bytes.Equal(got, pdf) {
		t.Fatalf("Read: %v", err)
	}
	if _, err := f.Read("../" + key); !errors.Is(err, errNotFound) {
		t.Fatalf("path traversal: %v", err)
	}
	other, _ := secretbox.New(bytes.Repeat([]byte{8}, 32))
	if _, err := (Files{Dir: f.Dir, Box: other}).Read(key); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("kunci lain: %v", err)
	}
}
