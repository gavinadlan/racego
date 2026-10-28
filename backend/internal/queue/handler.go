package queue

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/ratelimit"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

const (
	eventCacheTTL = 3 * time.Second
	joinPerUser   = 20  // join + keluar per akun per menit
	joinPerIP     = 300 // per IP per menit (longgar: banyak pengguna seluler berbagi IP)
)

type Handler struct {
	db *pgxpool.Pool
	q  *Queue

	cacheMu sync.Mutex
	cache   map[uuid.UUID]cachedEvent
}

type cachedEvent struct {
	ev      Event // published/closed, mitra terverifikasi, antrian aktif
	expires time.Time
}

func NewHandler(db *pgxpool.Pool, q *Queue) *Handler {
	return &Handler{db: db, q: q, cache: map[uuid.UUID]cachedEvent{}}
}

const eventSelect = `
	SELECT e.id::text, e.sale_open_at, e.sale_close_at, e.status = 'closed',
	       COALESCE(array_agg(c.id::text) FILTER (WHERE c.id IS NOT NULL), '{}')
	FROM events e
	JOIN organizations o ON o.id = e.org_id
	LEFT JOIN ticket_categories c ON c.event_id = e.id`

func scanEvent(row pgx.Row) (Event, error) {
	var ev Event
	err := row.Scan(&ev.ID, &ev.SaleOpenAt, &ev.SaleCloseAt, &ev.Closed, &ev.CategoryIDs)
	return ev, err
}

// event membaca event ber-antrian; di-cache beberapa detik karena dipanggil di tiap polling.
func (h *Handler) event(ctx context.Context, id uuid.UUID) (Event, bool, error) {
	now := time.Now()
	h.cacheMu.Lock()
	c, hit := h.cache[id]
	h.cacheMu.Unlock()
	if hit && now.Before(c.expires) {
		return c.ev, true, nil
	}
	ev, err := scanEvent(h.db.QueryRow(ctx, eventSelect+`
		WHERE e.id = $1 AND e.waiting_room AND e.status IN ('published', 'closed') AND o.kyc_status = 'verified'
		GROUP BY e.id`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ev, false, nil // tidak di-cache: id acak tidak boleh membuat cache terus tumbuh
	}
	if err != nil {
		return ev, false, err
	}
	h.cacheMu.Lock()
	h.cache[id] = cachedEvent{ev: ev, expires: now.Add(eventCacheTTL)}
	h.cacheMu.Unlock()
	return ev, true, nil
}

// load membaca event dari URL; false bila sudah membalas (404/500).
func (h *Handler) load(w http.ResponseWriter, r *http.Request) (Event, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "eventID"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "event_not_found", "Event tidak ditemukan")
		return Event{}, false
	}
	ev, ok, err := h.event(r.Context(), id)
	if err != nil {
		httpx.Internal(w, r, err)
		return ev, false
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "event_not_found", "Event tidak ditemukan atau tidak memakai antrian")
		return ev, false
	}
	return ev, true
}

func limited(w http.ResponseWriter, r *http.Request, err error, what string) bool {
	var ex ratelimit.Exceeded
	if !errors.As(err, &ex) {
		if err != nil {
			httpx.Internal(w, r, err)
			return true
		}
		return false
	}
	secs := int(math.Ceil(ex.RetryAfter.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	httpx.JSON(w, http.StatusTooManyRequests, map[string]any{
		"error":       "too_many_requests",
		"message":     fmt.Sprintf("Terlalu sering %s. Coba lagi dalam %d detik.", what, secs),
		"retry_after": secs,
	})
	return true
}

// limitJoin: join & keluar dibatasi per akun dan per IP.
func (h *Handler) limitJoin(w http.ResponseWriter, r *http.Request, userID string) bool {
	ctx := r.Context()
	if limited(w, r, ratelimit.Allow(ctx, h.q.rdb, "queue_rl:user:"+userID, joinPerUser, time.Minute), "masuk/keluar antrian") {
		return true
	}
	return limited(w, r, ratelimit.Allow(ctx, h.q.rdb, "queue_rl:ip:"+auth.ClientIP(r), joinPerIP, time.Minute), "masuk/keluar antrian")
}

// Join: POST /v1/events/{eventID}/queue — 201 baru bergabung, 200 sudah di antrian.
func (h *Handler) Join(w http.ResponseWriter, r *http.Request) {
	ev, ok := h.load(w, r)
	if !ok {
		return
	}
	now := time.Now()
	if !h.q.Open(ev, now) {
		msg := "Penjualan event ini sudah ditutup"
		if now.Before(h.q.LobbyOpensAt(ev)) {
			msg = "Antrian belum dibuka. Antrian dibuka " + h.q.LobbyOpensAt(ev).In(wib).Format("02-01-2006 15.04") + " WIB"
		}
		httpx.Error(w, http.StatusConflict, "queue_not_open", msg)
		return
	}
	userID, _ := auth.UserID(r.Context())
	if h.limitJoin(w, r, userID.String()) {
		return
	}
	st, created, err := h.q.Join(r.Context(), ev, userID.String(), now)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	httpx.JSON(w, code, st)
}

var wib = time.FixedZone("WIB", 7*60*60)

// Status: GET /v1/events/{eventID}/queue/me — posisi + heartbeat. Minimal selang 1 detik per akun.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	ev, ok := h.load(w, r)
	if !ok {
		return
	}
	userID, _ := auth.UserID(r.Context())
	if limited(w, r, ratelimit.Allow(r.Context(), h.q.rdb, "queue_rl:status:"+ev.ID+":"+userID.String(), 1, time.Second), "memeriksa antrian") {
		return
	}
	st, err := h.q.Status(r.Context(), ev, userID.String(), time.Now())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

// Leave: DELETE /v1/events/{eventID}/queue/me — keluar antrian (pass ikut dicabut).
func (h *Handler) Leave(w http.ResponseWriter, r *http.Request) {
	ev, ok := h.load(w, r)
	if !ok {
		return
	}
	userID, _ := auth.UserID(r.Context())
	if h.limitJoin(w, r, userID.String()) {
		return
	}
	if err := h.q.Leave(r.Context(), ev.ID, userID.String()); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// OrgSummary: GET /v1/org/{org}/events/{eventID}/queue — ringkasan langsung untuk mitra.
func (h *Handler) OrgSummary(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "eventID"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "event_not_found", "Event tidak ditemukan")
		return
	}
	var waiting bool
	ev, err := func() (Event, error) {
		row := h.db.QueryRow(r.Context(), `
			SELECT e.id::text, e.sale_open_at, e.sale_close_at, e.status = 'closed', '{}'::text[], e.waiting_room
			FROM events e WHERE e.id = $1 AND e.org_id = $2`, id, tenant.OrgID(r.Context()))
		var ev Event
		err := row.Scan(&ev.ID, &ev.SaleOpenAt, &ev.SaleCloseAt, &ev.Closed, &ev.CategoryIDs, &waiting)
		return ev, err
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "event_not_found", "Event tidak ditemukan")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := time.Now()
	s, err := h.q.Summary(r.Context(), ev.ID, now)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	phase := "before_lobby"
	switch {
	case ev.ended(now):
		phase = "closed"
	case !now.Before(ev.SaleOpenAt):
		phase = "selling"
	case h.q.Open(ev, now):
		phase = "lobby"
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"waiting_room": waiting, "phase": phase, "lobby_opens_at": h.q.LobbyOpensAt(ev), "sale_open_at": ev.SaleOpenAt,
		"queue_size": s.QueueSize, "active": s.Active, "admitted_total": s.AdmittedTotal, "final": s.Final,
		"max_active": h.q.cfg.MaxActive, "admit_per_sec": h.q.cfg.AdmitPerSec, "server_time": now,
	})
}

// Runner menjalankan antrian semua event aktif dari worker, tiap detik.
// Daftar event dibaca dari Postgres paling sering tiap 10 detik, bukan tiap detik per event.
type Runner struct {
	db     *pgxpool.Pool
	q      *Queue
	events []Event
	loaded time.Time
}

func NewRunner(db *pgxpool.Pool, q *Queue) *Runner { return &Runner{db: db, q: q} }

const runnerRefresh = 10 * time.Second

// Tick: satu putaran. Error per event dicatat pemanggil lewat errs; event lain tetap jalan.
func (r *Runner) Tick(ctx context.Context, now time.Time) (admitted int, errs []error) {
	if now.Sub(r.loaded) >= runnerRefresh {
		rows, err := r.db.Query(ctx, eventSelect+`
			WHERE e.waiting_room AND e.status IN ('published', 'closed')
			  AND e.sale_open_at - make_interval(mins => $1) <= now()
			  AND e.sale_close_at > now() - interval '1 hour'
			GROUP BY e.id`, r.q.cfg.LobbyMinutes)
		if err != nil {
			return 0, []error{fmt.Errorf("daftar event antrian: %w", err)}
		}
		events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Event, error) { return scanEvent(row) })
		if err != nil {
			return 0, []error{fmt.Errorf("daftar event antrian: %w", err)}
		}
		r.events, r.loaded = events, now
	}
	for _, ev := range r.events {
		n, err := r.q.Tick(ctx, ev, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("antrian event %s: %w", ev.ID, err))
		}
		admitted += n
	}
	return admitted, errs
}
