package event

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

type OrgEvent struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	RaceDate  time.Time `json:"race_date"`
	Quota     int       `json:"quota"`
	Sold      int       `json:"sold"`
	Collected int       `json:"collected"`
	Revenue   int64     `json:"revenue"`
	// Antrian virtual aktif (lencana di dashboard).
	WaitingRoom bool `json:"waiting_room"`
}

// OrgEvents: GET /v1/org/{org}/events — dashboard mitra. Hanya event milik mitra ini (tenant.OrgID).
func (h *Handler) OrgEvents(w http.ResponseWriter, r *http.Request) {
	m, _ := tenant.FromContext(r.Context())
	rows, err := h.db.Query(r.Context(), `
		SELECT e.id, e.name, e.status, e.race_date,
		       (SELECT COALESCE(sum(quota), 0) FROM ticket_categories WHERE event_id = e.id),
		       (SELECT count(*) FROM tickets WHERE event_id = e.id AND status IN ('issued', 'collected')),
		       (SELECT count(*) FROM tickets WHERE event_id = e.id AND status = 'collected'),
		       (SELECT COALESCE(sum(total), 0) FROM orders WHERE event_id = e.id AND status = 'paid'),
		       e.waiting_room
		FROM events e
		WHERE e.org_id = $1
		-- yang akan datang dulu (terdekat di atas), lalu arsip (terbaru di atas)
		ORDER BY e.race_date < current_date,
		         CASE WHEN e.race_date >= current_date THEN e.race_date END ASC,
		         e.race_date DESC`, m.OrgID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (OrgEvent, error) {
		var e OrgEvent
		err := row.Scan(&e.ID, &e.Name, &e.Status, &e.RaceDate, &e.Quota, &e.Sold, &e.Collected, &e.Revenue, &e.WaitingRoom)
		return e, err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"org":    map[string]any{"slug": m.OrgSlug, "name": m.OrgName, "role": m.Role},
		"events": list,
	})
}
