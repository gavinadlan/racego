package event

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/httpx"
)

type Organizer struct {
	Slug           string  `json:"slug"`
	Name           string  `json:"name"`
	City           string  `json:"city"`
	Description    string  `json:"description"`
	ThemeColor     string  `json:"theme_color"`
	LogoURL        *string `json:"logo_url"`
	Instagram      *string `json:"instagram"`
	Website        *string `json:"website"`
	UpcomingEvents int     `json:"upcoming_events"`
	PastEvents     int     `json:"past_events"`
	Runners        int     `json:"runners"` // total tiket terbit di semua event
}

const organizerSelect = `
	SELECT o.slug, o.name, o.city, o.description, o.theme_color, o.logo_url, o.instagram, o.website,
	       (SELECT count(*) FROM events e WHERE e.org_id = o.id AND e.status IN ('published', 'closed') AND e.race_date >= current_date),
	       (SELECT count(*) FROM events e WHERE e.org_id = o.id AND e.status IN ('published', 'closed') AND e.race_date < current_date),
	       (SELECT count(*) FROM tickets t WHERE t.org_id = o.id AND t.status IN ('issued', 'collected'))
	FROM organizations o
	WHERE o.kyc_status = 'verified'`

func scanOrganizer(row pgx.Row) (Organizer, error) {
	var o Organizer
	err := row.Scan(&o.Slug, &o.Name, &o.City, &o.Description, &o.ThemeColor, &o.LogoURL, &o.Instagram, &o.Website,
		&o.UpcomingEvents, &o.PastEvents, &o.Runners)
	return o, err
}

// Organizers: GET /v1/organizers — direktori penyelenggara terverifikasi.
func (h *Handler) Organizers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), organizerSelect+` ORDER BY o.name`)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Organizer, error) { return scanOrganizer(row) })
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	httpx.JSON(w, http.StatusOK, map[string]any{"organizers": list})
}

// Organizer: GET /v1/organizers/{slug} — profil + event mendatang + arsip.
func (h *Handler) Organizer(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	o, err := scanOrganizer(h.db.QueryRow(r.Context(), organizerSelect+` AND o.slug = $1`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "organizer_not_found", "Penyelenggara tidak ditemukan")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	upcoming, err := h.listSummaries(r.Context(), Filter{Scope: "upcoming", OrgSlug: slug})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	past, err := h.listSummaries(r.Context(), Filter{Scope: "past", OrgSlug: slug, Limit: 24})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	httpx.JSON(w, http.StatusOK, map[string]any{"organizer": o, "upcoming": upcoming, "past": past})
}
