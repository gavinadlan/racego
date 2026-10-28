package partner

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const maxSlugLen = 60

// Slugify: "Café Run Été 2027!" → "cafe-run-ete-2027". Bisa kosong bila nama tanpa huruf/angka.
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r): // tanda aksen hasil dekomposisi NFD
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	out := b.String()
	if len(out) > maxSlugLen {
		out = strings.TrimRight(out[:maxSlugLen], "-")
	}
	return out
}

// Slug org menjadi bagian URL frontend (/org/{slug}), jadi nama route frontend dicadangkan.
var reservedOrgSlugs = map[string]bool{
	"masuk": true, "akun": true, "org": true, "events": true, "kalender": true, "penyelenggara": true,
	"untuk-penyelenggara": true, "tickets": true, "orders": true, "api": true, "admin": true,
	"daftar-mitra": true, "uploads": true,
}

var orgSlugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// checkOrgSlug mengembalikan pesan error untuk slug yang diketik pengguna, atau "" bila formatnya valid.
func checkOrgSlug(s string) string {
	switch {
	case len(s) < 3 || len(s) > 40:
		return "Alamat harus 3 sampai 40 karakter"
	case !orgSlugRe.MatchString(s):
		return "Pakai huruf kecil, angka, dan tanda strip saja (mis. lari-pagi-bogor)"
	case reservedOrgSlugs[s]:
		return "Alamat ini dipakai sistem, pilih yang lain"
	}
	return ""
}

// uniqueSlug memilih base, base-2, base-3, … yang belum dipakai. taken mengembalikan semua slug
// yang sudah ada dengan awalan base (satu query, bukan satu query per percobaan).
func uniqueSlug(ctx context.Context, base string, reserved map[string]bool, taken func(ctx context.Context, base string) ([]string, error)) (string, error) {
	used, err := taken(ctx, base)
	if err != nil {
		return "", err
	}
	set := make(map[string]bool, len(used))
	for _, s := range used {
		set[s] = true
	}
	for n := 1; ; n++ {
		s := base
		if n > 1 {
			suffix := "-" + strconv.Itoa(n)
			s = strings.TrimRight(base[:min(len(base), maxSlugLen-len(suffix))], "-") + suffix
		}
		if !set[s] && !reserved[s] {
			return s, nil
		}
	}
}
