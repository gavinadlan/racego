package draw

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fixedSource mengembalikan nilai berurutan dari daftar (mengulang), dipotong ke rentang.
type fixedSource struct {
	vals []int
	i    int
	raw  bool // true = kembalikan apa adanya (untuk menguji penjagaan rentang)
}

func (f *fixedSource) Intn(n int) (int, error) {
	v := f.vals[f.i%len(f.vals)]
	f.i++
	if f.raw {
		return v, nil
	}
	return v % n, nil
}

func TestPickBoundsAndNoRepeat(t *testing.T) {
	// Selalu indeks pertama / terakhir: tetap permutasi sah tanpa pengulangan.
	for name, src := range map[string]Source{"awal": &fixedSource{vals: []int{0}}, "akhir": &fixedSource{vals: []int{1 << 30}}} {
		got, err := pick(src, 6, 6)
		if err != nil || len(got) != 6 {
			t.Fatalf("%s: %v %v", name, got, err)
		}
		seen := map[int]bool{}
		for _, v := range got {
			if v < 0 || v >= 6 || seen[v] {
				t.Fatalf("%s: indeks tidak sah/berulang: %v", name, got)
			}
			seen[v] = true
		}
	}
	// Sumber yang melanggar kontrak (di luar [0,n)) ditolak, bukan dipakai untuk mengindeks.
	for _, bad := range []int{-1, 5, 99} {
		if _, err := pick(&fixedSource{vals: []int{bad}, raw: true}, 5, 1); err == nil {
			t.Errorf("nilai %d di luar rentang harus ditolak", bad)
		}
	}
	if _, err := pick(errSource{}, 5, 1); err == nil {
		t.Error("galat sumber harus diteruskan")
	}
}

type errSource struct{}

func (errSource) Intn(int) (int, error) { return 0, errors.New("rusak") }

// Sumber asli (crypto/rand): undian berulang pada kolam kecil mendekati seragam, tidak bias ke awal/akhir.
// Batas ±1,5 poin persen ≈ 7 simpangan baku pada 20.000 undian: lulus stabil, tidak flaky.
func TestPickUniformCrypto(t *testing.T) {
	const n, rounds = 10, 20000
	src := cryptoSource{}
	one := make([]int, n)
	for i := 0; i < rounds; i++ {
		p, err := pick(src, n, 1)
		if err != nil {
			t.Fatal(err)
		}
		one[p[0]]++
	}
	for i, c := range one {
		if f := float64(c) / rounds; f < 0.085 || f > 0.115 {
			t.Errorf("indeks %d terpilih %.1f%% (harapan 10%%): %v", i, f*100, one)
		}
	}
	// Tiga pemenang dari 10: tiap indeks muncul ±30%, dan tidak pernah ada duplikat dalam satu undian.
	three := make([]int, n)
	for i := 0; i < rounds; i++ {
		p, _ := pick(src, n, 3)
		if p[0] == p[1] || p[0] == p[2] || p[1] == p[2] {
			t.Fatalf("duplikat dalam satu undian: %v", p)
		}
		for _, v := range p {
			three[v]++
		}
	}
	for i, c := range three {
		if f := float64(c) / rounds; f < 0.27 || f > 0.33 {
			t.Errorf("3-dari-10 indeks %d muncul %.1f%% (harapan 30%%): %v", i, f*100, three)
		}
	}
	t.Logf("distribusi 1-dari-10 (20.000 undian): %v", one)
	t.Logf("distribusi 3-dari-10 (20.000 undian): %v", three)
}

func TestPoolHashDeterministicAndOrderIndependent(t *testing.T) {
	a, b, c := uuid.MustParse("00000000-0000-0000-0000-00000000000a"), uuid.MustParse("00000000-0000-0000-0000-00000000000b"), uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	h1, h2 := PoolHash([]uuid.UUID{a, b, c}), PoolHash([]uuid.UUID{c, a, b})
	if h1 != h2 {
		t.Fatal("hash harus tidak bergantung urutan masukan")
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{a.String(), b.String(), c.String()}, "\n")))
	if h1 != hex.EncodeToString(sum[:]) {
		t.Fatalf("definisi hash: SHA-256 hex dari ticket_id terurut dipisah \\n; dapat %s", h1)
	}
	if PoolHash([]uuid.UUID{a, b}) == h1 {
		t.Fatal("kolam berbeda harus berhash berbeda")
	}
}

func TestFirstName(t *testing.T) {
	for in, want := range map[string]string{"Budi Santoso": "Budi", "  Sari ": "Sari", "": "", "Satu": "Satu",
		"Abcdefghijklmnopqrstuvwxyz Z": "Abcdefghijklmnopqrst", "Ñandú López": "Ñandú"} {
		if got := FirstName(in); got != want {
			t.Errorf("FirstName(%q) = %q, mau %q", in, got, want)
		}
	}
}
