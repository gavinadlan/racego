package partner

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gavinadlan/racego/backend/internal/httpx"
)

// Semua tanggal race & jam di form memakai waktu Indonesia Barat (Asia/Jakarta, tanpa DST).
var wib = time.FixedZone("WIB", 7*60*60)

var (
	startTimeRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	hexColorRe  = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	uploadURLRe = regexp.MustCompile(`^/uploads/[a-f0-9]{64}\.(jpg|png)$`)
	instagramRe = regexp.MustCompile(`^[A-Za-z0-9._]{1,30}$`)
	catCodeRe   = regexp.MustCompile(`^[A-Z0-9]{2,8}$`)
	bibPrefixRe = regexp.MustCompile(`^[0-9]{1,3}$`)
)

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// EventInput adalah isi form event. PATCH mengirim semua kolom (form selalu lengkap).
type EventInput struct {
	Name         string `json:"name"`
	Tagline      string `json:"tagline"`
	Description  string `json:"description"`
	Venue        string `json:"venue"`
	City         string `json:"city"`
	RaceDate     string `json:"race_date"` // YYYY-MM-DD
	StartTime    string `json:"start_time"`
	SaleOpenAt   string `json:"sale_open_at"` // ISO dengan offset, mis. 2027-01-03T10:00:00+07:00
	SaleCloseAt  string `json:"sale_close_at"`
	RacepackInfo string `json:"racepack_info"`
	ThemeColor   string `json:"theme_color"` // kosong = pakai warna penyelenggara
	CoverURL     string `json:"cover_url"`   // kosong = sampul otomatis
}

func (in *EventInput) trim() {
	for _, p := range []*string{&in.Name, &in.Tagline, &in.Description, &in.Venue, &in.City, &in.RaceDate, &in.StartTime,
		&in.SaleOpenAt, &in.SaleCloseAt, &in.RacepackInfo, &in.ThemeColor, &in.CoverURL} {
		*p = strings.TrimSpace(*p)
	}
}

// saleTimes mengurai waktu penjualan (RFC 3339, wajib dengan offset zona waktu).
func (in EventInput) saleTimes() (open, close time.Time, okOpen, okClose bool) {
	open, err1 := time.Parse(time.RFC3339, in.SaleOpenAt)
	close, err2 := time.Parse(time.RFC3339, in.SaleCloseAt)
	return open, close, err1 == nil, err2 == nil
}

// raceDay mengurai race_date; ok=false bila formatnya salah.
func (in EventInput) raceDay() (time.Time, bool) {
	d, err := time.ParseInLocation("2006-01-02", in.RaceDate, wib)
	return d, err == nil
}

// validate memeriksa form event. futureRace: race_date wajib setelah hari ini (saat membuat,
// saat tanggal diubah, dan saat menerbitkan).
func (in EventInput) validate(now time.Time, futureRace bool) httpx.Fields {
	f := httpx.Fields{}
	if n := runeLen(in.Name); n < 3 || n > 100 {
		f["name"] = "Nama event harus 3 sampai 100 karakter"
	}
	if runeLen(in.Tagline) > 120 {
		f["tagline"] = "Tagline maksimal 120 karakter"
	}
	if runeLen(in.Description) > 5000 {
		f["description"] = "Deskripsi maksimal 5.000 karakter"
	}
	if runeLen(in.RacepackInfo) > 2000 {
		f["racepack_info"] = "Info race pack maksimal 2.000 karakter"
	}
	if n := runeLen(in.City); n < 2 || n > 60 {
		f["city"] = "Kota wajib diisi (2 sampai 60 karakter)"
	}
	if runeLen(in.Venue) > 100 {
		f["venue"] = "Lokasi start maksimal 100 karakter"
	}
	if !startTimeRe.MatchString(in.StartTime) {
		f["start_time"] = "Jam flag-off berformat JJ:MM, mis. 05:30"
	}
	if in.ThemeColor != "" && !hexColorRe.MatchString(in.ThemeColor) {
		f["theme_color"] = "Warna harus kode hex #RRGGBB"
	}
	if in.CoverURL != "" && !uploadURLRe.MatchString(in.CoverURL) {
		f["cover_url"] = "Poster harus diunggah lewat tombol unggah"
	}

	day, ok := in.raceDay()
	today := time.Date(now.In(wib).Year(), now.In(wib).Month(), now.In(wib).Day(), 0, 0, 0, 0, wib)
	switch {
	case !ok:
		f["race_date"] = "Tanggal race wajib diisi"
	case futureRace && !day.After(today):
		f["race_date"] = "Tanggal race harus setelah hari ini"
	}
	open, close, okOpen, okClose := in.saleTimes()
	if !okOpen {
		f["sale_open_at"] = "Waktu buka penjualan wajib diisi"
	}
	switch {
	case !okClose:
		f["sale_close_at"] = "Waktu tutup penjualan wajib diisi"
	case !okOpen:
	case !open.Before(close):
		f["sale_close_at"] = "Penjualan harus ditutup setelah dibuka"
	case ok && close.After(day.AddDate(0, 0, 1)):
		f["sale_close_at"] = "Penjualan harus ditutup paling lambat akhir hari race (" + day.Format("02-01-2006") + " 23.59 WIB)"
	}
	return f
}

// CategoryInput adalah satu kategori tiket.
type CategoryInput struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	DistanceKm float64 `json:"distance_km"`
	Price      int64   `json:"price"`
	Quota      int     `json:"quota"`
	MaxPerUser int     `json:"max_per_user"`
	BibPrefix  string  `json:"bib_prefix"`
}

func (c *CategoryInput) trim() {
	c.Code = strings.ToUpper(strings.TrimSpace(c.Code))
	c.Name = strings.TrimSpace(c.Name)
	c.BibPrefix = strings.TrimSpace(c.BibPrefix)
}

// validate memeriksa satu kategori; kunci error diawali prefix (mis. "categories.0.").
func (c CategoryInput) validate(prefix string, f httpx.Fields) {
	if !catCodeRe.MatchString(c.Code) {
		f[prefix+"code"] = "Kode 2–8 huruf kapital/angka, mis. 10K"
	}
	if n := runeLen(c.Name); n < 2 || n > 60 {
		f[prefix+"name"] = "Nama kategori 2 sampai 60 karakter"
	}
	if c.DistanceKm <= 0 || c.DistanceKm > 250 {
		f[prefix+"distance_km"] = "Jarak lebih dari 0 dan maksimal 250 km"
	}
	if c.Price < 0 || c.Price > 50_000_000 {
		f[prefix+"price"] = "Harga Rp0 sampai Rp50.000.000"
	}
	if c.Quota < 1 || c.Quota > 100_000 {
		f[prefix+"quota"] = "Kuota 1 sampai 100.000"
	}
	if c.MaxPerUser < 1 || c.MaxPerUser > 10 {
		f[prefix+"max_per_user"] = "Maksimal per akun 1 sampai 10"
	}
	if !bibPrefixRe.MatchString(c.BibPrefix) {
		f[prefix+"bib_prefix"] = "Prefix BIB 1–3 digit angka"
	}
}

// checkDuplicates menandai kode / prefix BIB yang kembar di dalam satu kiriman.
func checkDuplicates(cats []CategoryInput, f httpx.Fields) {
	codes, prefixes := map[string]int{}, map[string]int{}
	for i, c := range cats {
		p := fmt.Sprintf("categories.%d.", i)
		if j, dup := codes[c.Code]; dup && c.Code != "" {
			f[p+"code"] = fmt.Sprintf("Kode sama dengan kategori ke-%d", j+1)
		} else {
			codes[c.Code] = i
		}
		if j, dup := prefixes[c.BibPrefix]; dup && c.BibPrefix != "" {
			f[p+"bib_prefix"] = fmt.Sprintf("Prefix BIB sama dengan kategori ke-%d", j+1)
		} else {
			prefixes[c.BibPrefix] = i
		}
	}
}

// OrgInput adalah form daftar penyelenggara / ubah profil.
type OrgInput struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"` // hanya saat mendaftar; kosong = dibuat dari nama
	City        string `json:"city"`
	Description string `json:"description"`
	Instagram   string `json:"instagram"`
	Website     string `json:"website"`
	ThemeColor  string `json:"theme_color"` // hanya profil
	LogoURL     string `json:"logo_url"`    // hanya profil
}

func (in *OrgInput) trim() {
	for _, p := range []*string{&in.Name, &in.Slug, &in.City, &in.Description, &in.Instagram, &in.Website, &in.ThemeColor, &in.LogoURL} {
		*p = strings.TrimSpace(*p)
	}
	in.Slug = strings.ToLower(in.Slug)
}

func (in OrgInput) validate(profile bool) httpx.Fields {
	f := httpx.Fields{}
	if n := runeLen(in.Name); n < 3 || n > 80 {
		f["name"] = "Nama penyelenggara 3 sampai 80 karakter"
	}
	if n := runeLen(in.City); n < 2 || n > 60 {
		f["city"] = "Kota wajib diisi (2 sampai 60 karakter)"
	}
	if runeLen(in.Description) > 1000 {
		f["description"] = "Deskripsi maksimal 1.000 karakter"
	}
	if in.Instagram != "" && !instagramRe.MatchString(in.Instagram) {
		f["instagram"] = "Tulis nama akun saja tanpa @ atau tautan, mis. larikita.id"
	}
	if in.Website != "" {
		u, err := url.Parse(in.Website)
		if err != nil || u.Scheme != "https" || u.Host == "" || len(in.Website) > 200 {
			f["website"] = "Alamat situs harus diawali https://"
		}
	}
	if profile {
		if !hexColorRe.MatchString(in.ThemeColor) {
			f["theme_color"] = "Warna harus kode hex #RRGGBB"
		}
		if in.LogoURL != "" && !uploadURLRe.MatchString(in.LogoURL) {
			f["logo_url"] = "Logo harus diunggah lewat tombol unggah"
		}
	} else if in.Slug != "" {
		if msg := checkOrgSlug(in.Slug); msg != "" {
			f["slug"] = msg
		}
	}
	return f
}

// nullIfEmpty untuk kolom opsional yang disimpan NULL bila kosong.
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
