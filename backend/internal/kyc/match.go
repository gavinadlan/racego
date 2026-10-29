package kyc

import (
	"slices"
	"strings"
	"unicode"
)

// Gelar & sapaan umum yang diabaikan saat membandingkan nama.
var honorifics = map[string]bool{
	"bapak": true, "bpk": true, "ibu": true, "sdr": true, "sdri": true, "tn": true, "ny": true, "nn": true,
	"dr": true, "drs": true, "dra": true, "ir": true, "h": true, "hj": true, "prof": true,
	"se": true, "sh": true, "st": true, "skom": true, "spd": true, "ssi": true, "sked": true, "amd": true,
	"mm": true, "mba": true, "msc": true, "mt": true, "mkom": true, "mh": true,
}

// NormalizeName: huruf kecil, tanda baca jadi spasi, gelar umum dibuang, spasi dirapikan.
// "Dr. Budi  Santoso, S.Kom." → "budi santoso"
func NormalizeName(s string) string {
	// Gelar bertitik ("S.Kom.") disatukan dulu sebelum tanda baca lain jadi spasi.
	s = strings.ReplaceAll(strings.ToLower(s), ".", "")
	words := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return strings.Join(slices.DeleteFunc(words, func(w string) bool { return honorifics[w] }), " ")
}

// NameMatch: petunjuk untuk peninjau (bukan penentu) apakah pemilik rekening sesuai aturan jenis penyelenggara.
type NameMatch struct {
	OK       bool     `json:"ok"`
	Holder   string   `json:"holder"`
	Expected []string `json:"expected"` // nama yang dianggap cocok
	Rule     string   `json:"rule"`
}

func matchHolder(t OrgType, holder, picName, legalName string) NameMatch {
	r, _ := ruleFor(t)
	m := NameMatch{Holder: holder, Rule: r.HolderRule}
	switch t {
	case TypePerorangan:
		m.Expected = []string{picName}
	case TypeKomunitas:
		m.Expected = []string{picName, legalName}
	case TypeBadanUsaha:
		m.Expected = []string{legalName}
	}
	h := NormalizeName(holder)
	for _, e := range m.Expected {
		if n := NormalizeName(e); h != "" && n == h {
			m.OK = true
		}
	}
	return m
}
