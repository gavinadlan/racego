// Package csvx menulis CSV untuk diunduh mitra (dibuka di Excel/Sheets) dengan aman.
//
// Jenis sel ditentukan oleh tipe Go, bukan oleh isinya: string selalu sel teks dan disanitasi
// terhadap injeksi rumus, int/int64 selalu sel angka dan ditulis apa adanya (termasuk negatif).
// Masukan pengguna berupa string tidak pernah menjadi sel angka walau isinya "-150000".
package csvx

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Safe menetralkan injeksi rumus pada sel teks: diawali = + - @ tab atau CR → diberi awalan '.
func Safe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// Writer: UTF-8 dengan BOM, pemisah titik koma dan CRLF, agar terbuka benar di Excel berlokal Indonesia.
type Writer struct {
	cw  *csv.Writer
	buf []string
}

// New menulis BOM lalu mengembalikan Writer. Panggil Flush setelah baris terakhir.
func New(w io.Writer) (*Writer, error) {
	if _, err := io.WriteString(w, "\uFEFF"); err != nil {
		return nil, err
	}
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.UseCRLF = true
	return &Writer{cw: cw}, nil
}

// Row menulis satu baris. Sel: string (teks, disanitasi), int/int64 (angka), nil (kosong).
// Tipe lain = kesalahan pemrograman → error, bukan ditebak.
func (w *Writer) Row(cells ...any) error {
	w.buf = w.buf[:0]
	for i, c := range cells {
		switch v := c.(type) {
		case string:
			w.buf = append(w.buf, Safe(v))
		case int:
			w.buf = append(w.buf, strconv.Itoa(v))
		case int64:
			w.buf = append(w.buf, strconv.FormatInt(v, 10))
		case nil:
			w.buf = append(w.buf, "")
		default:
			return fmt.Errorf("csvx: sel %d bertipe %T tidak didukung", i, c)
		}
	}
	return w.cw.Write(w.buf)
}

// Header menulis baris judul (teks).
func (w *Writer) Header(names ...string) error {
	cells := make([]any, len(names))
	for i, n := range names {
		cells[i] = n
	}
	return w.Row(cells...)
}

func (w *Writer) Flush() error {
	w.cw.Flush()
	return w.cw.Error()
}
