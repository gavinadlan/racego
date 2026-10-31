package csvx

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
)

func TestSafe(t *testing.T) {
	for in, want := range map[string]string{
		"=cmd|' /C calc'!A0": "'=cmd|' /C calc'!A0", "+1+1": "'+1+1", "-2+3": "'-2+3", "@SUM(1)": "'@SUM(1)",
		"\t=1": "'\t=1", "\r=1": "'\r=1", "aman": "aman", "Budi, S.T.": "Budi, S.T.", "": "",
	} {
		if got := Safe(in); got != want {
			t.Errorf("Safe(%q) = %q, mau %q", in, got, want)
		}
	}
}

func TestWriterTypedCells(t *testing.T) {
	var buf bytes.Buffer
	w, err := New(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Header("Teks", "Angka", "Kosong"); err != nil {
		t.Fatal(err)
	}
	rows := [][]any{
		{"-150000", int64(-150000), nil}, // string berisi angka tetap teks → dinetralkan
		{"=HYPERLINK(\"http://x\")", 42, nil},
		{`kata "kutip"; titik koma, koma`, int64(0), nil},
		{"\r=1", -1, nil},
	}
	for _, r := range rows {
		if err := w.Row(r...); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "\uFEFFTeks;Angka;Kosong\r\n") {
		t.Fatalf("BOM/header: %q", out)
	}
	if !strings.Contains(out, "'-150000;-150000;\r\n") {
		t.Fatalf("angka negatif harus utuh, teks '-' dinetralkan: %q", out)
	}
	cr := csv.NewReader(strings.NewReader(strings.TrimPrefix(out, "\uFEFF")))
	cr.Comma = ';'
	recs, err := cr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"Teks", "Angka", "Kosong"},
		{"'-150000", "-150000", ""},
		{"'=HYPERLINK(\"http://x\")", "42", ""},
		{`kata "kutip"; titik koma, koma`, "0", ""},
		{"'=1", "-1", ""}, // encoding/csv (UseCRLF) membuang CR lepas di dalam sel; awalan ' tetap ada
	}
	if len(recs) != len(want) {
		t.Fatalf("%d baris, mau %d", len(recs), len(want))
	}
	for i := range want {
		if strings.Join(recs[i], "|") != strings.Join(want[i], "|") {
			t.Errorf("baris %d = %q, mau %q", i, recs[i], want[i])
		}
	}
	if err := w.Row(3.14); err == nil {
		t.Fatal("float harus ditolak (tipe sel ditentukan kode)")
	}
}
