package event

import (
	"testing"
	"time"
)

func TestSaleStatus(t *testing.T) {
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name              string
		race, open, close time.Time
		want              string
	}{
		{"penjualan belum dibuka", now.Add(30 * day), now.Add(2 * day), now.Add(27 * day), "upcoming"},
		{"penjualan dibuka", now.Add(30 * day), now.Add(-day), now.Add(27 * day), "open"},
		{"penjualan ditutup, race belum lewat", now.Add(2 * day), now.Add(-30 * day), now.Add(-day), "closed"},
		{"hari-H masih dihitung belum selesai", now.Truncate(day), now.Add(-30 * day), now.Add(-3 * day), "closed"},
		{"race sudah lewat", now.Add(-2 * day), now.Add(-60 * day), now.Add(-5 * day), "finished"},
	}
	for _, c := range cases {
		if got := saleStatus(now, c.race, c.open, c.close); got != c.want {
			t.Errorf("%s: dapat %q, seharusnya %q", c.name, got, c.want)
		}
	}
}
