package partner

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Jakarta Runners Club":       "jakarta-runners-club",
		"Café Run Été 2027!":         "cafe-run-ete-2027",
		"  --Lari__Pagi!!  Bogor-- ": "lari-pagi-bogor",
		"Ñandú & Søn":                "nandu-s-n", // ø bukan huruf beraksen (tidak terurai NFD)
		"!!!":                        "",
		strings.Repeat("a", 80):      strings.Repeat("a", 60),
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, mau %q", in, got, want)
		}
	}
}

func TestUniqueSlug(t *testing.T) {
	taken := func(used ...string) func(context.Context, string) ([]string, error) {
		return func(context.Context, string) ([]string, error) { return used, nil }
	}
	ctx := context.Background()
	cases := []struct {
		base     string
		used     []string
		reserved map[string]bool
		want     string
	}{
		{"lari", nil, nil, "lari"},
		{"lari", []string{"lari"}, nil, "lari-2"},
		{"lari", []string{"lari", "lari-2", "lari-3"}, nil, "lari-4"},
		{"admin", nil, reservedOrgSlugs, "admin-2"},
		{strings.Repeat("a", 60), []string{strings.Repeat("a", 60)}, nil, strings.Repeat("a", 58) + "-2"},
	}
	for _, c := range cases {
		got, err := uniqueSlug(ctx, c.base, c.reserved, taken(c.used...))
		if err != nil || got != c.want {
			t.Errorf("uniqueSlug(%q, %v) = %q, mau %q", c.base, c.used, got, c.want)
		}
	}
}

func TestCheckOrgSlug(t *testing.T) {
	for _, ok := range []string{"lari-pagi", "abc", "run2027"} {
		if msg := checkOrgSlug(ok); msg != "" {
			t.Errorf("%q harus valid: %s", ok, msg)
		}
	}
	for _, bad := range []string{"ab", "Lari", "lari--pagi", "-lari", "lari_pagi", "masuk", "daftar-mitra", "uploads", strings.Repeat("a", 41)} {
		if checkOrgSlug(bad) == "" {
			t.Errorf("%q harus ditolak", bad)
		}
	}
}

type eventCase struct {
	EventInput
	open, close time.Time
	rawClose    string // dipakai apa adanya bila diisi
}

func iso(t time.Time) string { return t.Format(time.RFC3339) }

func TestValidateEvent(t *testing.T) {
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, wib)
	race := time.Date(2027, 1, 10, 0, 0, 0, 0, wib)
	cases := []struct {
		name   string
		mod    func(*eventCase)
		future bool
		field  string // "" = valid
	}{
		{"valid", func(*eventCase) {}, true, ""},
		{"nama 2 huruf", func(e *eventCase) { e.Name = "Ab" }, true, "name"},
		{"nama 101 huruf", func(e *eventCase) { e.Name = strings.Repeat("é", 101) }, true, "name"},
		{"nama 100 huruf beraksen", func(e *eventCase) { e.Name = strings.Repeat("é", 100) }, true, ""},
		{"race hari ini", func(e *eventCase) { e.RaceDate = "2026-10-28" }, true, "race_date"},
		{"race hari ini tapi tanggal tidak diubah", func(e *eventCase) {
			e.RaceDate = "2026-10-28"
			e.open, e.close = now.Add(-48*time.Hour), now.Add(-24*time.Hour)
		}, false, ""},
		{"race besok", func(e *eventCase) {
			e.RaceDate = "2026-10-29"
			e.close = now.Add(2 * time.Hour)
			e.open = now.Add(time.Hour)
		}, true, ""},
		{"tanggal salah format", func(e *eventCase) { e.RaceDate = "10/01/2027" }, true, "race_date"},
		{"buka = tutup", func(e *eventCase) { e.close = e.open }, true, "sale_close_at"},
		{"tutup tepat akhir hari race (24.00 WIB)", func(e *eventCase) { e.close = race.AddDate(0, 0, 1) }, true, ""},
		{"tutup 1 menit lewat hari race", func(e *eventCase) { e.close = race.AddDate(0, 0, 1).Add(time.Minute) }, true, "sale_close_at"},
		{"tutup akhir hari race dalam UTC", func(e *eventCase) { e.close = time.Date(2027, 1, 10, 16, 59, 0, 0, time.UTC) }, true, ""},
		{"tutup lewat hari race dalam UTC", func(e *eventCase) { e.close = time.Date(2027, 1, 10, 17, 1, 0, 0, time.UTC) }, true, "sale_close_at"},
		{"tanpa waktu buka", func(e *eventCase) { e.open = time.Time{} }, true, "sale_open_at"},
		{"jam 24:00", func(e *eventCase) { e.StartTime = "24:00" }, true, "start_time"},
		{"jam 5:30", func(e *eventCase) { e.StartTime = "5:30" }, true, "start_time"},
		{"warna tanpa #", func(e *eventCase) { e.ThemeColor = "FF5B23" }, true, "theme_color"},
		{"warna pendek", func(e *eventCase) { e.ThemeColor = "#FFF" }, true, "theme_color"},
		{"tagline 121", func(e *eventCase) { e.Tagline = strings.Repeat("x", 121) }, true, "tagline"},
		{"deskripsi 5001", func(e *eventCase) { e.Description = strings.Repeat("x", 5001) }, true, "description"},
		{"race pack 2001", func(e *eventCase) { e.RacepackInfo = strings.Repeat("x", 2001) }, true, "racepack_info"},
		{"poster dari luar", func(e *eventCase) { e.CoverURL = "https://evil.example/a.jpg" }, true, "cover_url"},
		{"poster traversal", func(e *eventCase) { e.CoverURL = "/uploads/../../etc/passwd" }, true, "cover_url"},
		{"tanpa kota", func(e *eventCase) { e.City = "" }, true, "city"},
		{"waktu tanpa zona", func(e *eventCase) { e.rawClose = "2027-01-05T23:00" }, true, "sale_close_at"},
	}
	for _, c := range cases {
		e := eventCase{EventInput{Name: "Lari Pagi Bogor", City: "Bogor", RaceDate: "2027-01-10", StartTime: "05:30"},
			now.Add(24 * time.Hour), time.Date(2027, 1, 5, 23, 0, 0, 0, wib), ""}
		c.mod(&e)
		e.SaleOpenAt, e.SaleCloseAt = iso(e.open), iso(e.close)
		if e.open.IsZero() {
			e.SaleOpenAt = ""
		}
		if e.rawClose != "" {
			e.SaleCloseAt = e.rawClose
		}
		f := e.validate(now, c.future)
		if c.field == "" && len(f) > 0 {
			t.Errorf("%s: harus valid, dapat %v", c.name, f)
		}
		if c.field != "" && f[c.field] == "" {
			t.Errorf("%s: harus ada error di %s, dapat %v", c.name, c.field, f)
		}
	}
}

func TestValidateCategory(t *testing.T) {
	base := CategoryInput{Code: "10K", Name: "10K", DistanceKm: 10, Price: 150000, Quota: 500, MaxPerUser: 2, BibPrefix: "10"}
	cases := []struct {
		name  string
		mod   func(*CategoryInput)
		field string
	}{
		{"valid", func(*CategoryInput) {}, ""},
		{"gratis", func(c *CategoryInput) { c.Price = 0 }, ""},
		{"harga maks", func(c *CategoryInput) { c.Price = 50_000_000 }, ""},
		{"harga lewat maks", func(c *CategoryInput) { c.Price = 50_000_001 }, "price"},
		{"harga negatif", func(c *CategoryInput) { c.Price = -1 }, "price"},
		{"kode 1 huruf", func(c *CategoryInput) { c.Code = "K" }, "code"},
		{"kode 9 huruf", func(c *CategoryInput) { c.Code = "ABCDEFGHI" }, "code"},
		{"kode huruf kecil", func(c *CategoryInput) { c.Code = "10k" }, "code"},
		{"kode spasi", func(c *CategoryInput) { c.Code = "10 K" }, "code"},
		{"jarak 0", func(c *CategoryInput) { c.DistanceKm = 0 }, "distance_km"},
		{"jarak 250", func(c *CategoryInput) { c.DistanceKm = 250 }, ""},
		{"jarak 250.01", func(c *CategoryInput) { c.DistanceKm = 250.01 }, "distance_km"},
		{"kuota 0", func(c *CategoryInput) { c.Quota = 0 }, "quota"},
		{"kuota 100001", func(c *CategoryInput) { c.Quota = 100_001 }, "quota"},
		{"maks per akun 11", func(c *CategoryInput) { c.MaxPerUser = 11 }, "max_per_user"},
		{"maks per akun 0", func(c *CategoryInput) { c.MaxPerUser = 0 }, "max_per_user"},
		{"prefix 4 digit", func(c *CategoryInput) { c.BibPrefix = "1000" }, "bib_prefix"},
		{"prefix huruf", func(c *CategoryInput) { c.BibPrefix = "A" }, "bib_prefix"},
		{"prefix kosong", func(c *CategoryInput) { c.BibPrefix = "" }, "bib_prefix"},
	}
	for _, c := range cases {
		in := base
		c.mod(&in)
		f := map[string]string{}
		in.validate("", f)
		if (c.field == "") != (len(f) == 0) || (c.field != "" && f[c.field] == "") {
			t.Errorf("%s: dapat %v, mau error di %q", c.name, f, c.field)
		}
	}
	f := map[string]string{}
	checkDuplicates([]CategoryInput{base, {Code: "5K", BibPrefix: "10"}, {Code: "10K", BibPrefix: "21"}}, f)
	if f["categories.1.bib_prefix"] == "" || f["categories.2.code"] == "" {
		t.Errorf("duplikat tidak terdeteksi: %v", f)
	}
}

func TestValidateOrg(t *testing.T) {
	ok := OrgInput{Name: "Lari Pagi Bogor", City: "Bogor", Instagram: "lari.pagi_bogor", Website: "https://laripagi.id"}
	if f := ok.validate(false); len(f) > 0 {
		t.Fatalf("valid: %v", f)
	}
	cases := []struct {
		mod   func(*OrgInput)
		field string
	}{
		{func(o *OrgInput) { o.Instagram = "@laripagi" }, "instagram"},
		{func(o *OrgInput) { o.Instagram = "https://instagram.com/laripagi" }, "instagram"},
		{func(o *OrgInput) { o.Website = "http://laripagi.id" }, "website"},
		{func(o *OrgInput) { o.Website = "laripagi.id" }, "website"},
		{func(o *OrgInput) { o.Slug = "admin" }, "slug"},
		{func(o *OrgInput) { o.Name = "AB" }, "name"},
	}
	for i, c := range cases {
		in := ok
		c.mod(&in)
		if in.validate(false)[c.field] == "" {
			t.Errorf("kasus %d: harus ada error di %s", i, c.field)
		}
	}
}
