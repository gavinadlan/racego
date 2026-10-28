// Package event menyajikan katalog event lari, kalender, arsip, dan profil penyelenggara.
package event

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/checkout"
	"github.com/gavinadlan/racego/backend/internal/httpx"
)

type Handler struct {
	db    *pgxpool.Pool
	inv   *checkout.Inventory
	lobby time.Duration // antrian virtual: lobi dibuka sekian lama sebelum penjualan
}

func NewHandler(db *pgxpool.Pool, inv *checkout.Inventory, lobby time.Duration) *Handler {
	return &Handler{db: db, inv: inv, lobby: lobby}
}

// Summary adalah satu event di daftar, kalender, arsip, dan halaman penyelenggara.
type Summary struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Tagline     string    `json:"tagline"`
	City        string    `json:"city"`
	Venue       string    `json:"venue"`
	RaceDate    time.Time `json:"race_date"`
	StartTime   string    `json:"start_time"`
	SaleOpenAt  time.Time `json:"sale_open_at"`
	SaleCloseAt time.Time `json:"sale_close_at"`
	Status      string    `json:"status"` // open | upcoming | closed | finished
	ThemeColor  string    `json:"theme_color"`
	CoverURL    *string   `json:"cover_url"`
	OrgName     string    `json:"org_name"`
	OrgSlug     string    `json:"org_slug"`
	MinPrice    int64     `json:"min_price"`
	Distances   []string  `json:"distances"`
}

// saleStatus diturunkan dari tanggal, bukan disimpan, supaya selalu akurat.
// dbStatus "closed" = mitra menutup penjualan lebih awal.
func saleStatus(now, raceDate, open, close time.Time, dbStatus string) string {
	switch {
	case !raceDate.Add(24 * time.Hour).After(now):
		return "finished"
	case dbStatus == "closed":
		return "closed"
	case now.Before(open):
		return "upcoming"
	case now.Before(close):
		return "open"
	default:
		return "closed"
	}
}

const summarySelect = `
	SELECT e.id, e.slug, e.name, e.tagline, e.city, e.venue, e.race_date, e.start_time,
	       e.sale_open_at, e.sale_close_at, COALESCE(e.theme_color, o.theme_color), e.cover_url,
	       o.name, o.slug, min(c.price), array_agg(c.code ORDER BY c.distance_km), e.status
	FROM events e
	JOIN organizations o ON o.id = e.org_id
	JOIN ticket_categories c ON c.event_id = e.id
	WHERE e.status IN ('published', 'closed') AND o.kyc_status = 'verified'`

func scanSummaries(rows pgx.Rows) ([]Summary, error) {
	now := time.Now()
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Summary, error) {
		var s Summary
		var dbStatus string
		err := row.Scan(&s.ID, &s.Slug, &s.Name, &s.Tagline, &s.City, &s.Venue, &s.RaceDate, &s.StartTime,
			&s.SaleOpenAt, &s.SaleCloseAt, &s.ThemeColor, &s.CoverURL, &s.OrgName, &s.OrgSlug, &s.MinPrice, &s.Distances, &dbStatus)
		s.Status = saleStatus(now, s.RaceDate, s.SaleOpenAt, s.SaleCloseAt, dbStatus)
		return s, err
	})
}

// Filter untuk daftar event. Semua opsional.
type Filter struct {
	Scope    string // upcoming (default) | past | all
	City     string
	Distance string // kode kategori, mis. 21K
	Month    string // YYYY-MM
	From, To string // YYYY-MM-DD (inklusif), untuk tampilan kalender
	OrgSlug  string
	Query    string
	Limit    int
}

func (h *Handler) listSummaries(ctx context.Context, f Filter) ([]Summary, error) {
	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	order := "e.race_date ASC"
	switch f.Scope {
	case "past":
		where = append(where, "e.race_date < current_date")
		order = "e.race_date DESC"
	case "all":
	default:
		where = append(where, "e.race_date >= current_date")
	}
	if f.City != "" {
		where = append(where, "lower(e.city) = lower("+arg(f.City)+")")
	}
	if f.Distance != "" {
		where = append(where, "EXISTS (SELECT 1 FROM ticket_categories x WHERE x.event_id = e.id AND upper(x.code) = upper("+arg(f.Distance)+"))")
	}
	if f.Month != "" {
		if m, err := time.Parse("2006-01", f.Month); err == nil {
			where = append(where, "e.race_date >= "+arg(m)+" AND e.race_date < "+arg(m.AddDate(0, 1, 0)))
		}
	}
	if d, err := time.Parse("2006-01-02", f.From); err == nil {
		where = append(where, "e.race_date >= "+arg(d))
	}
	if d, err := time.Parse("2006-01-02", f.To); err == nil {
		where = append(where, "e.race_date <= "+arg(d))
	}
	if f.OrgSlug != "" {
		where = append(where, "o.slug = "+arg(f.OrgSlug))
	}
	if f.Query != "" {
		p := arg("%" + f.Query + "%")
		where = append(where, "(e.name ILIKE "+p+" OR e.city ILIKE "+p+" OR o.name ILIKE "+p+")")
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	sql := summarySelect
	if len(where) > 0 {
		sql += " AND " + strings.Join(where, " AND ")
	}
	sql += fmt.Sprintf(" GROUP BY e.id, o.id ORDER BY %s LIMIT %d", order, limit)
	rows, err := h.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return scanSummaries(rows)
}

// List: GET /v1/events?scope=upcoming|past|all&city=&distance=&month=YYYY-MM&from=&to=&org=&q=&limit=
// Respons aman di-cache CDN beberapa detik.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := h.listSummaries(r.Context(), Filter{
		Scope: q.Get("scope"), City: q.Get("city"), Distance: q.Get("distance"), Month: q.Get("month"),
		From: q.Get("from"), To: q.Get("to"), OrgSlug: q.Get("org"), Query: strings.TrimSpace(q.Get("q")), Limit: limit,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=10, stale-while-revalidate=30")
	httpx.JSON(w, http.StatusOK, map[string]any{"events": list})
}

// Filters: GET /v1/events/filters — pilihan kota & jarak untuk kolom pencarian.
func (h *Handler) Filters(w http.ResponseWriter, r *http.Request) {
	var cities, distances []string
	if err := h.db.QueryRow(r.Context(), `
		SELECT COALESCE(array_agg(DISTINCT e.city ORDER BY e.city), '{}')
		FROM events e JOIN organizations o ON o.id = e.org_id
		WHERE e.status IN ('published', 'closed') AND o.kyc_status = 'verified' AND e.race_date >= current_date`).Scan(&cities); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := h.db.QueryRow(r.Context(), `
		SELECT COALESCE(array_agg(code ORDER BY km), '{}') FROM (
			SELECT c.code, min(c.distance_km) AS km FROM ticket_categories c
			JOIN events e ON e.id = c.event_id
			JOIN organizations o ON o.id = e.org_id
			WHERE e.status IN ('published', 'closed') AND o.kyc_status = 'verified' AND e.race_date >= current_date
			GROUP BY c.code) d`).Scan(&distances); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	httpx.JSON(w, http.StatusOK, map[string]any{"cities": cities, "distances": distances})
}

type Category struct {
	ID         uuid.UUID `json:"id"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	DistanceKm float64   `json:"distance_km"`
	Price      int64     `json:"price"`
	Quota      int       `json:"quota"`
	Remaining  *int      `json:"remaining"` // null = stok belum dimuat (penjualan belum dibuka)
	MaxPerUser int       `json:"max_per_user"`
}

type OrgBrief struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	City        string  `json:"city"`
	Description string  `json:"description"`
	ThemeColor  string  `json:"theme_color"`
	LogoURL     *string `json:"logo_url"`
}

type Detail struct {
	Summary
	Description  string     `json:"description"`
	RacepackInfo string     `json:"racepack_info"`
	Org          OrgBrief   `json:"org"`
	Categories   []Category `json:"categories"`
	WaitingRoom  bool       `json:"waiting_room"`
	// Hanya bila waiting_room: kapan lobi antrian dibuka.
	LobbyOpensAt *time.Time `json:"lobby_opens_at,omitempty"`
}

// Get: GET /v1/events/{eventID}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "eventID"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "event_not_found", "Event tidak ditemukan")
		return
	}
	var d Detail
	var dbStatus string
	err = h.db.QueryRow(r.Context(), `
		SELECT e.id, e.slug, e.name, e.tagline, e.city, e.venue, e.race_date, e.start_time,
		       e.sale_open_at, e.sale_close_at, COALESCE(e.theme_color, o.theme_color), e.cover_url,
		       o.name, o.slug, e.description, e.racepack_info,
		       o.slug, o.name, o.city, o.description, o.theme_color, o.logo_url, e.status, e.waiting_room
		FROM events e JOIN organizations o ON o.id = e.org_id
		WHERE e.id = $1 AND e.status IN ('published', 'closed') AND o.kyc_status = 'verified'`, id,
	).Scan(&d.ID, &d.Slug, &d.Name, &d.Tagline, &d.City, &d.Venue, &d.RaceDate, &d.StartTime,
		&d.SaleOpenAt, &d.SaleCloseAt, &d.ThemeColor, &d.CoverURL, &d.OrgName, &d.OrgSlug, &d.Description, &d.RacepackInfo,
		&d.Org.Slug, &d.Org.Name, &d.Org.City, &d.Org.Description, &d.Org.ThemeColor, &d.Org.LogoURL, &dbStatus, &d.WaitingRoom)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "event_not_found", "Event tidak ditemukan")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	d.Status = saleStatus(time.Now(), d.RaceDate, d.SaleOpenAt, d.SaleCloseAt, dbStatus)
	if d.WaitingRoom {
		t := d.SaleOpenAt.Add(-h.lobby)
		d.LobbyOpensAt = &t
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT id, code, name, distance_km::float8, price, quota, max_per_user
		FROM ticket_categories WHERE event_id = $1 ORDER BY distance_km`, id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	d.Categories, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Category, error) {
		var c Category
		err := row.Scan(&c.ID, &c.Code, &c.Name, &c.DistanceKm, &c.Price, &c.Quota, &c.MaxPerUser)
		return c, err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	d.Distances = make([]string, 0, len(d.Categories))
	for i := range d.Categories {
		c := &d.Categories[i]
		d.Distances = append(d.Distances, c.Code)
		if d.MinPrice == 0 || c.Price < d.MinPrice {
			d.MinPrice = c.Price
		}
		if left, ok, err := h.inv.Available(r.Context(), c.ID.String()); err == nil && ok {
			c.Remaining = &left
		}
	}
	w.Header().Set("Cache-Control", "public, max-age=2")
	httpx.JSON(w, http.StatusOK, d)
}
