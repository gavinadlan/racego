package ticket

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSignAndVerify(t *testing.T) {
	s, err := NewSigner("", true)
	if err != nil {
		t.Fatal(err)
	}
	tid, eid := uuid.New(), uuid.New()
	token := s.Sign(tid, eid)

	gotT, gotE, err := Verify(s.PublicKey(), token)
	if err != nil || gotT != tid || gotE != eid {
		t.Fatalf("Verify = %v %v %v", gotT, gotE, err)
	}
	// Mengganti ID tiket (mis. memalsukan tiket orang lain) harus gagal.
	forged := strings.Replace(token, tid.String(), uuid.NewString(), 1)
	if _, _, err := Verify(s.PublicKey(), forged); err == nil {
		t.Fatal("token yang diubah seharusnya gagal")
	}
	// Kunci lain tidak bisa memverifikasi.
	seed, _, _ := GenerateSeed()
	other, _ := NewSigner(seed, false)
	if _, _, err := Verify(other.PublicKey(), token); err == nil {
		t.Fatal("kunci publik lain seharusnya gagal")
	}
	for _, bad := range []string{"", "v1.a.b", "v2." + token[3:], "sampah"} {
		if _, _, err := Verify(s.PublicKey(), bad); err == nil {
			t.Fatalf("token %q seharusnya gagal", bad)
		}
	}
}

func TestNewSignerRequiresKeyOutsideDev(t *testing.T) {
	if _, err := NewSigner("", false); err == nil {
		t.Fatal("tanpa kunci di produksi seharusnya error")
	}
	if _, err := NewSigner("bukan-base64!", true); err == nil {
		t.Fatal("kunci rusak seharusnya error")
	}
}
