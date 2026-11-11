// Package draw: undian doorprize (spin BIB) untuk peserta yang hadir hari-H. Lihat docs/12-undian.md.
//
// Prinsip: pemenang dipilih di server dengan crypto/rand dari snapshot kolam saat itu; klien hanya
// menganimasikan hasil. Tidak ada parameter/endpoint untuk memilih pemenang secara manual. Setiap undian
// menyimpan pool_size dan pool_hash (SHA-256 daftar ticket_id kolam yang terurut) agar bisa diaudit ulang.
// Baris pemenang append-only (trigger di migrasi 0009): pengganti = baris baru.
package draw

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/csvx"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/partner"
	"github.com/gavinadlan/racego/backend/internal/tenant"
	"github.com/gavinadlan/racego/backend/internal/ticket"
)

// Source: sumber acak. Intn mengembalikan bilangan seragam di [0, n). Hanya tes yang menggantinya
// (WithSource); produksi selalu crypto/rand, tidak lewat konfigurasi.
type Source interface{ Intn(n int) (int, error) }

type cryptoSource struct{}

// Intn memakai rand.Int dengan batas: seragam, tanpa bias modulo.
func (cryptoSource) Intn(n int) (int, error) {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, err
	}
	return int(v.Int64()), nil
}

type Handler struct {
	db  *pgxpool.Pool
	src Source
}

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db, src: cryptoSource{}} }

// WithSource mengganti sumber acak (hanya untuk tes).
func (h *Handler) WithSource(s Source) *Handler {
	h.src = s
	return h
}

const (
	maxPrizes = 100
	maxCount  = 1000
)

// ===== Dasar =====

type apiErr struct {
	status    int
	code, msg string
}

func (e *apiErr) Error() string { return e.code }

func fail(status int, code, msg string) *apiErr { return &apiErr{status, code, msg} }

// respond menulis error terstruktur; false = tidak ada error.
func respond(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	var ae *apiErr
	if errors.As(err, &ae) {
		httpx.Error(w, ae.status, ae.code, ae.msg)
		return true
	}
	httpx.Internal(w, r, err)
	return true
}

// event memuat {eventID} milik org ini; false = 404 sudah ditulis.
func (h *Handler) event(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	m, _ := tenant.FromContext(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "eventID"))
	var ok bool
	if err == nil {
		if e := h.db.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM events WHERE id = $1 AND org_id = $2)`, id, m.OrgID).Scan(&ok); e != nil {
			httpx.Internal(w, r, e)
			return uuid.Nil, false
		}
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "event_not_found", "Event tidak ditemukan")
		return uuid.Nil, false
	}
	return id, true
}

type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func sortStrings(s []string) { sort.Strings(s) }

func audit(ctx context.Context, q execer, org, actor uuid.UUID, action, entity, entityID string, before, after any) error {
	_, err := q.Exec(ctx, `INSERT INTO audit_logs (actor_id, org_id, action, entity, entity_id, before, after) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		actor, org, action, entity, entityID, before, after)
	return err
}

// ===== Pengaturan =====

type settings struct {
	Eligibility       string `json:"draw_eligibility"`
	AllowMultipleWins bool   `json:"allow_multiple_wins"`
	Public            bool   `json:"draw_public"`
	Locked            bool   `json:"locked"` // eligibilitas & satu-menang terkunci setelah pemenang pertama
}

func (h *Handler) loadSettings(ctx context.Context, q pgxQuerier, eventID uuid.UUID) (settings, error) {
	var s settings
	err := q.QueryRow(ctx, `SELECT e.draw_eligibility, e.allow_multiple_wins, e.draw_public,
		EXISTS (SELECT 1 FROM draw_winners w WHERE w.event_id = e.id) FROM events e WHERE e.id = $1`, eventID).
		Scan(&s.Eligibility, &s.AllowMultipleWins, &s.Public, &s.Locked)
	return s, err
}

// Settings: GET …/draw/settings.
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	eventID, ok := h.event(w, r)
	if !ok {
		return
	}
	s, err := h.loadSettings(r.Context(), h.db, eventID)
	if respond(w, r, err) {
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// SetSettings: PUT …/draw/settings {"draw_eligibility","allow_multiple_wins","draw_public"}.
func (h *Handler) SetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	actor, _ := auth.UserID(ctx)
	eventID, ok := h.event(w, r)
	if !ok {
		return
	}
	var in struct {
		Eligibility       string `json:"draw_eligibility"`
		AllowMultipleWins bool   `json:"allow_multiple_wins"`
		Public            bool   `json:"draw_public"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if in.Eligibility != "attended" && in.Eligibility != "collected" {
		httpx.Validation(w, httpx.Fields{"draw_eligibility": "Pilih attended (hadir hari-H) atau collected (race pack diambil)"})
		return
	}
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		if err := ticket.LockEventDraw(ctx, tx, eventID); err != nil {
			return err
		}
		before, err := h.loadSettings(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if before.Locked && (before.Eligibility != in.Eligibility || before.AllowMultipleWins != in.AllowMultipleWins) {
			return fail(http.StatusConflict, "settings_locked", "Kelayakan dan aturan satu-menang terkunci karena sudah ada pemenang undian.")
		}
		if _, err := tx.Exec(ctx, `UPDATE events SET draw_eligibility = $2, allow_multiple_wins = $3, draw_public = $4 WHERE id = $1`,
			eventID, in.Eligibility, in.AllowMultipleWins, in.Public); err != nil {
			return err
		}
		return audit(ctx, tx, m.OrgID, actor, "draw.settings.changed", "event", eventID.String(),
			map[string]any{"draw_eligibility": before.Eligibility, "allow_multiple_wins": before.AllowMultipleWins, "draw_public": before.Public},
			map[string]any{"draw_eligibility": in.Eligibility, "allow_multiple_wins": in.AllowMultipleWins, "draw_public": in.Public})
	})
	if respond(w, r, err) {
		return
	}
	h.Settings(w, r)
}

// ===== Hadiah =====

type Prize struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Quantity     int        `json:"quantity"`
	CategoryID   *uuid.UUID `json:"category_id"`
	CategoryCode *string    `json:"category_code"`
	Position     int        `json:"position"`
	Drawn        int        `json:"drawn"`     // pemenang aktif (winner + claimed)
	Winners      int        `json:"winners"`   // semua baris, termasuk yang gugur
	Remaining    int        `json:"remaining"` // quantity − drawn
}

const prizeSelect = `
	SELECT p.id, p.name, p.quantity, p.category_id, c.code, p.position,
	       count(w.id) FILTER (WHERE w.status IN ('winner', 'claimed'))::int, count(w.id)::int
	FROM draw_prizes p
	LEFT JOIN ticket_categories c ON c.id = p.category_id
	LEFT JOIN draw_winners w ON w.prize_id = p.id`

func scanPrize(row pgx.Row) (Prize, error) {
	var p Prize
	err := row.Scan(&p.ID, &p.Name, &p.Quantity, &p.CategoryID, &p.CategoryCode, &p.Position, &p.Drawn, &p.Winners)
	p.Remaining = max(p.Quantity-p.Drawn, 0)
	return p, err
}

// Prizes: GET …/draw/prizes.
func (h *Handler) Prizes(w http.ResponseWriter, r *http.Request) {
	eventID, ok := h.event(w, r)
	if !ok {
		return
	}
	rows, err := h.db.Query(r.Context(), prizeSelect+` WHERE p.event_id = $1 GROUP BY p.id, c.code ORDER BY p.position, p.created_at`, eventID)
	if respond(w, r, err) {
		return
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Prize, error) { return scanPrize(row) })
	if respond(w, r, err) {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"prizes": items})
}

type prizeInput struct {
	Name       string     `json:"name"`
	Quantity   int        `json:"quantity"`
	CategoryID *uuid.UUID `json:"category_id"`
	Position   *int       `json:"position"`
}

func (in *prizeInput) validate(ctx context.Context, q pgxQuerier, eventID uuid.UUID) httpx.Fields {
	f := httpx.Fields{}
	in.Name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(in.Name); n < 2 || n > 100 {
		f["name"] = "Nama hadiah 2–100 karakter"
	}
	if in.Quantity < 1 || in.Quantity > 1000 {
		f["quantity"] = "Jumlah 1–1.000"
	}
	if in.CategoryID != nil {
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ticket_categories WHERE id = $1 AND event_id = $2)`, *in.CategoryID, eventID).Scan(&ok); err != nil || !ok {
			f["category_id"] = "Kategori tidak ditemukan di event ini"
		}
	}
	return f
}

// CreatePrize: POST …/draw/prizes.
func (h *Handler) CreatePrize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	actor, _ := auth.UserID(ctx)
	eventID, ok := h.event(w, r)
	if !ok {
		return
	}
	var in prizeInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if f := in.validate(ctx, h.db, eventID); len(f) > 0 {
		httpx.Validation(w, f)
		return
	}
	var id uuid.UUID
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM draw_prizes WHERE event_id = $1`, eventID).Scan(&n); err != nil {
			return err
		}
		if n >= maxPrizes {
			return fail(http.StatusConflict, "too_many_prizes", fmt.Sprintf("Maksimal %d hadiah per event", maxPrizes))
		}
		pos := n + 1
		if in.Position != nil {
			pos = *in.Position
		}
		if err := tx.QueryRow(ctx, `INSERT INTO draw_prizes (event_id, org_id, name, quantity, category_id, position) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			eventID, m.OrgID, in.Name, in.Quantity, in.CategoryID, pos).Scan(&id); err != nil {
			return err
		}
		return audit(ctx, tx, m.OrgID, actor, "draw.prize.created", "draw_prize", id.String(), nil,
			map[string]any{"event_id": eventID, "name": in.Name, "quantity": in.Quantity, "category_id": in.CategoryID})
	})
	if respond(w, r, err) {
		return
	}
	p, err := scanPrize(h.db.QueryRow(ctx, prizeSelect+` WHERE p.id = $1 GROUP BY p.id, c.code`, id))
	if respond(w, r, err) {
		return
	}
	httpx.JSON(w, http.StatusCreated, p)
}

func (h *Handler) prizeID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "prizeID"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "prize_not_found", "Hadiah tidak ditemukan")
		return uuid.Nil, false
	}
	return id, true
}

// UpdatePrize: PUT …/draw/prizes/{prizeID}. Jumlah tidak boleh di bawah pemenang aktif;
// filter kategori terkunci setelah ada pemenang (kolam pemenang lama tidak boleh berubah artinya).
func (h *Handler) UpdatePrize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	actor, _ := auth.UserID(ctx)
	eventID, ok := h.event(w, r)
	if !ok {
		return
	}
	pid, ok := h.prizeID(w, r)
	if !ok {
		return
	}
	var in prizeInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	f := in.validate(ctx, h.db, eventID)
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		if err := ticket.LockEventDraw(ctx, tx, eventID); err != nil {
			return err
		}
		cur, err := scanPrize(tx.QueryRow(ctx, prizeSelect+` WHERE p.id = $1 AND p.event_id = $2 GROUP BY p.id, c.code`, pid, eventID))
		if errors.Is(err, pgx.ErrNoRows) {
			return fail(http.StatusNotFound, "prize_not_found", "Hadiah tidak ditemukan")
		}
		if err != nil {
			return err
		}
		if in.Quantity < cur.Drawn && f["quantity"] == "" {
			f["quantity"] = fmt.Sprintf("Tidak boleh di bawah jumlah pemenang aktif (%d)", cur.Drawn)
		}
		sameCat := (cur.CategoryID == nil && in.CategoryID == nil) || (cur.CategoryID != nil && in.CategoryID != nil && *cur.CategoryID == *in.CategoryID)
		if cur.Winners > 0 && !sameCat {
			f["category_id"] = "Filter kategori terkunci karena hadiah ini sudah punya pemenang"
		}
		if len(f) > 0 {
			return &fieldsErr{f}
		}
		pos := cur.Position
		if in.Position != nil {
			pos = *in.Position
		}
		if _, err := tx.Exec(ctx, `UPDATE draw_prizes SET name = $2, quantity = $3, category_id = $4, position = $5 WHERE id = $1`, pid, in.Name, in.Quantity, in.CategoryID, pos); err != nil {
			return err
		}
		return audit(ctx, tx, m.OrgID, actor, "draw.prize.updated", "draw_prize", pid.String(),
			map[string]any{"name": cur.Name, "quantity": cur.Quantity, "category_id": cur.CategoryID},
			map[string]any{"name": in.Name, "quantity": in.Quantity, "category_id": in.CategoryID})
	})
	var fe *fieldsErr
	if errors.As(err, &fe) {
		httpx.Validation(w, fe.f)
		return
	}
	if respond(w, r, err) {
		return
	}
	p, err := scanPrize(h.db.QueryRow(ctx, prizeSelect+` WHERE p.id = $1 GROUP BY p.id, c.code`, pid))
	if respond(w, r, err) {
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

type fieldsErr struct{ f httpx.Fields }

func (e *fieldsErr) Error() string { return "validation" }

// DeletePrize: DELETE …/draw/prizes/{prizeID} — hanya hadiah yang belum pernah punya pemenang.
func (h *Handler) DeletePrize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	actor, _ := auth.UserID(ctx)
	eventID, ok := h.event(w, r)
	if !ok {
		return
	}
	pid, ok := h.prizeID(w, r)
	if !ok {
		return
	}
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		if err := ticket.LockEventDraw(ctx, tx, eventID); err != nil {
			return err
		}
		cur, err := scanPrize(tx.QueryRow(ctx, prizeSelect+` WHERE p.id = $1 AND p.event_id = $2 GROUP BY p.id, c.code`, pid, eventID))
		if errors.Is(err, pgx.ErrNoRows) {
			return fail(http.StatusNotFound, "prize_not_found", "Hadiah tidak ditemukan")
		}
		if err != nil {
			return err
		}
		if cur.Winners > 0 {
			return fail(http.StatusConflict, "prize_has_winners", "Hadiah yang sudah punya pemenang tidak bisa dihapus.")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM draw_prizes WHERE id = $1`, pid); err != nil {
			return err
		}
		return audit(ctx, tx, m.OrgID, actor, "draw.prize.deleted", "draw_prize", pid.String(), map[string]any{"name": cur.Name, "quantity": cur.Quantity}, nil)
	})
	if respond(w, r, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ===== Kolam =====

// poolIDs: tiket layak untuk satu hadiah (atau semua hadiah bila prizeID = nil → tanpa filter kategori hadiah),
// terurut. Dipakai undian dan ringkasan kolam: satu definisi kelayakan di satu tempat.
func poolIDs(ctx context.Context, q pgxQuerier, orgID, eventID uuid.UUID, s settings, prizeID *uuid.UUID, category *uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `
		SELECT t.id FROM tickets t
		WHERE t.org_id = $1 AND t.event_id = $2 AND t.status IN ('issued', 'collected')
		  AND (($3 = 'attended' AND t.attended_at IS NOT NULL) OR ($3 = 'collected' AND t.status = 'collected'))
		  AND ($4::uuid IS NULL OR t.category_id = $4)
		  AND NOT EXISTS (
		    SELECT 1 FROM draw_winners w WHERE w.ticket_id = t.id
		      AND (w.prize_id = $5 OR ($6 = false AND w.status IN ('winner', 'claimed'))))
		ORDER BY t.id`, orgID, eventID, s.Eligibility, category, prizeID, s.AllowMultipleWins)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// PoolHash: SHA-256 (hex) dari ticket_id (huruf kecil) terurut, dipisah baris baru. Bisa direproduksi dari data.
func PoolHash(ids []uuid.UUID) string {
	sorted := make([]string, len(ids))
	for i, id := range ids {
		sorted[i] = id.String()
	}
	sortStrings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:])
}

// Pool: GET …/draw/pool — ukuran kolam layak sekarang (tanpa daftar pribadi).
func (h *Handler) Pool(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	eventID, ok := h.event(w, r)
	if !ok {
		return
	}
	s, err := h.loadSettings(ctx, h.db, eventID)
	if respond(w, r, err) {
		return
	}
	opens, closes, err := ticket.AttendanceWindow(ctx, h.db, eventID)
	if respond(w, r, err) {
		return
	}
	ids, err := poolIDs(ctx, h.db, m.OrgID, eventID, s, nil, nil)
	if respond(w, r, err) {
		return
	}
	type cat struct {
		ID    uuid.UUID `json:"id"`
		Code  string    `json:"code"`
		Name  string    `json:"name"`
		Count int       `json:"count"`
	}
	rows, err := h.db.Query(ctx, `
		SELECT c.id, c.code, c.name, count(t.id)::int FROM ticket_categories c
		LEFT JOIN tickets t ON t.category_id = c.id AND t.id = ANY($3)
		WHERE c.event_id = $1 AND c.org_id = $2 GROUP BY c.id ORDER BY c.distance_km, c.code`, eventID, m.OrgID, ids)
	if respond(w, r, err) {
		return
	}
	cats, err := pgx.CollectRows(rows, pgx.RowToStructByPos[cat])
	if respond(w, r, err) {
		return
	}
	var attended int
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM tickets WHERE event_id = $1 AND org_id = $2 AND status IN ('issued', 'collected') AND attended_at IS NOT NULL`, eventID, m.OrgID).Scan(&attended); respond(w, r, err) {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"eligibility": s.Eligibility, "total": len(ids), "attended": attended, "by_category": cats,
		"draw_opens_at": opens, "draw_open": !time.Now().Before(opens), "attendance_closes_at": closes,
	})
}

// ===== Undi =====

type Winner struct {
	ID          uuid.UUID `json:"id"`
	PrizeID     uuid.UUID `json:"prize_id"`
	PrizeName   string    `json:"prize_name"`
	Seq         int       `json:"seq"`
	Bib         string    `json:"bib"`
	Name        string    `json:"name"`         // nama lengkap: hanya panel panitia
	DisplayName string    `json:"display_name"` // nama depan: layar panggung
	Category    string    `json:"category"`
	Status      string    `json:"status"`
	DrawnAt     time.Time `json:"drawn_at"`
	PoolSize    int       `json:"pool_size"`
	PoolHash    string    `json:"pool_hash"`
	Note        *string   `json:"note,omitempty"`
}

// FirstName: kata pertama nama (maks. 20 karakter), untuk layar publik/panggung.
func FirstName(full string) string {
	f := strings.Fields(full)
	if len(f) == 0 {
		return ""
	}
	r := []rune(f[0])
	if len(r) > 20 {
		r = r[:20]
	}
	return string(r)
}

const winnerSelect = `
	SELECT w.id, w.prize_id, p.name, w.seq, t.bib_number, pa.full_name, c.code, w.status, w.drawn_at, w.pool_size, w.pool_hash, w.note
	FROM draw_winners w
	JOIN draw_prizes p ON p.id = w.prize_id
	JOIN tickets t ON t.id = w.ticket_id
	JOIN participants pa ON pa.id = t.participant_id
	JOIN ticket_categories c ON c.id = t.category_id`

func scanWinner(row pgx.Row) (Winner, error) {
	var x Winner
	err := row.Scan(&x.ID, &x.PrizeID, &x.PrizeName, &x.Seq, &x.Bib, &x.Name, &x.Category, &x.Status, &x.DrawnAt, &x.PoolSize, &x.PoolHash, &x.Note)
	x.DisplayName = FirstName(x.Name)
	return x, err
}

// pick: pilih k indeks berbeda dari n secara seragam (Fisher-Yates parsial). Hanya memakai Source.Intn.
func pick(src Source, n, k int) ([]int, error) {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	for i := 0; i < k; i++ {
		j, err := src.Intn(n - i)
		if err != nil {
			return nil, err
		}
		if j < 0 || j >= n-i {
			return nil, fmt.Errorf("sumber acak di luar rentang: %d dari %d", j, n-i)
		}
		idx[i], idx[i+j] = idx[i+j], idx[i]
	}
	return idx[:k], nil
}

// Draw: POST …/draw/prizes/{prizeID}/draw {"count":1} atau {"all":true}; header Idempotency-Key opsional.
// Satu permintaan = satu atau beberapa pemenang dari snapshot kolam saat itu, di bawah kunci advisory per
// event: dua admin menekan Undi bersamaan tidak pernah melebihi jumlah hadiah atau memenangkan tiket yang sama.
func (h *Handler) Draw(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	actor, _ := auth.UserID(ctx)
	eventID, ok := h.event(w, r)
	if !ok {
		return
	}
	pid, ok := h.prizeID(w, r)
	if !ok {
		return
	}
	var in struct {
		Count int  `json:"count"`
		All   bool `json:"all"`
	}
	if r.ContentLength != 0 && !httpx.Decode(w, r, &in) {
		return
	}
	if in.Count == 0 {
		in.Count = 1
	}
	if !in.All && (in.Count < 1 || in.Count > maxCount) {
		httpx.Validation(w, httpx.Fields{"count": fmt.Sprintf("Jumlah 1–%d", maxCount)})
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 100 {
		httpx.Validation(w, httpx.Fields{"idempotency_key": "Idempotency-Key maksimal 100 karakter"})
		return
	}

	var winners []Winner
	replayed := false
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		if err := ticket.LockEventDraw(ctx, tx, eventID); err != nil {
			return err
		}
		prize, err := scanPrize(tx.QueryRow(ctx, prizeSelect+` WHERE p.id = $1 AND p.event_id = $2 GROUP BY p.id, c.code`, pid, eventID))
		if errors.Is(err, pgx.ErrNoRows) {
			return fail(http.StatusNotFound, "prize_not_found", "Hadiah tidak ditemukan")
		}
		if err != nil {
			return err
		}
		// Klik yang dikirim ulang (koneksi putus, klik ganda) mengembalikan hasil yang sama, tidak mengundi lagi.
		if key != "" {
			rows, err := tx.Query(ctx, winnerSelect+` WHERE w.prize_id = $1 AND w.request_key = $2 ORDER BY w.seq`, pid, key)
			if err != nil {
				return err
			}
			if winners, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Winner, error) { return scanWinner(row) }); err != nil {
				return err
			}
			if len(winners) > 0 {
				replayed = true
				return nil
			}
		}
		opens, _, err := ticket.AttendanceWindow(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if time.Now().Before(opens) {
			return fail(http.StatusConflict, "draw_not_open", "Undian baru bisa dijalankan mulai hari lomba (saat absen hari-H dibuka).")
		}
		s, err := h.loadSettings(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if prize.Remaining <= 0 {
			return fail(http.StatusConflict, "prize_exhausted", "Hadiah ini sudah terundi semua.")
		}
		count := in.Count
		if in.All {
			count = prize.Remaining
		}
		if count > prize.Remaining {
			return fail(http.StatusConflict, "prize_exhausted", fmt.Sprintf("Sisa hadiah ini %d, tidak bisa mengundi %d.", prize.Remaining, count))
		}
		pool, err := poolIDs(ctx, tx, m.OrgID, eventID, s, &pid, prize.CategoryID)
		if err != nil {
			return err
		}
		if len(pool) == 0 {
			return fail(http.StatusConflict, "pool_empty", emptyPoolMessage(s.Eligibility))
		}
		if count > len(pool) {
			return fail(http.StatusConflict, "pool_empty", fmt.Sprintf("Kolam hanya berisi %d peserta, tidak cukup untuk mengundi %d.", len(pool), count))
		}
		hash := PoolHash(pool)
		picks, err := pick(h.src, len(pool), count)
		if err != nil {
			return err
		}
		var seq int
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(seq), 0) FROM draw_winners WHERE prize_id = $1`, pid).Scan(&seq); err != nil {
			return err
		}
		var reqKey *string
		if key != "" {
			reqKey = &key
		}
		var ids []uuid.UUID
		bibs := make([]string, 0, count)
		for i, ix := range picks {
			var wid uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO draw_winners (prize_id, event_id, org_id, ticket_id, seq, drawn_by, pool_size, pool_hash, request_key)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
				pid, eventID, m.OrgID, pool[ix], seq+i+1, actor, len(pool), hash, reqKey).Scan(&wid); err != nil {
				return err
			}
			ids = append(ids, wid)
		}
		for _, wid := range ids {
			x, err := scanWinner(tx.QueryRow(ctx, winnerSelect+` WHERE w.id = $1`, wid))
			if err != nil {
				return err
			}
			winners = append(winners, x)
			bibs = append(bibs, x.Bib)
		}
		// Audit: hadiah, BIB, ukuran dan hash kolam. Tanpa nama atau data pribadi.
		return audit(ctx, tx, m.OrgID, actor, "draw.drawn", "draw_prize", pid.String(), nil,
			map[string]any{"event_id": eventID, "prize": prize.Name, "bibs": bibs, "pool_size": len(pool), "pool_hash": hash})
	})
	if respond(w, r, err) {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"winners": winners, "replayed": replayed})
}

func emptyPoolMessage(elig string) string {
	if elig == "collected" {
		return "Belum ada peserta layak: tidak ada tiket yang race pack-nya sudah diambil dan belum menang."
	}
	return "Belum ada peserta layak: belum ada yang diabsen hari-H, atau semua peserta hadir sudah menang."
}

// ===== Pemenang =====

// Winners: GET …/draw/winners.
func (h *Handler) Winners(w http.ResponseWriter, r *http.Request) {
	eventID, ok := h.event(w, r)
	if !ok {
		return
	}
	rows, err := h.db.Query(r.Context(), winnerSelect+` WHERE w.event_id = $1 ORDER BY p.position, p.created_at, w.seq`, eventID)
	if respond(w, r, err) {
		return
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Winner, error) { return scanWinner(row) })
	if respond(w, r, err) {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"winners": items})
}

// setStatus mengubah status pemenang winner → claimed | forfeited (alasan wajib untuk forfeited).
func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, to string) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	actor, _ := auth.UserID(ctx)
	eventID, ok := h.event(w, r)
	if !ok {
		return
	}
	wid, err := uuid.Parse(chi.URLParam(r, "winnerID"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "winner_not_found", "Pemenang tidak ditemukan")
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 && !httpx.Decode(w, r, &in) {
		return
	}
	reason := strings.TrimSpace(in.Reason)
	if to == "forfeited" {
		if n := utf8.RuneCountInString(reason); n < 5 || n > 300 {
			httpx.Validation(w, httpx.Fields{"reason": "Alasan 5–300 karakter"})
			return
		}
	}
	var out Winner
	err = pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		if err := ticket.LockEventDraw(ctx, tx, eventID); err != nil {
			return err
		}
		var cur, bib string
		err := tx.QueryRow(ctx, `SELECT w.status, t.bib_number FROM draw_winners w JOIN tickets t ON t.id = w.ticket_id WHERE w.id = $1 AND w.event_id = $2 AND w.org_id = $3 FOR UPDATE OF w`,
			wid, eventID, m.OrgID).Scan(&cur, &bib)
		if errors.Is(err, pgx.ErrNoRows) {
			return fail(http.StatusNotFound, "winner_not_found", "Pemenang tidak ditemukan")
		}
		if err != nil {
			return err
		}
		if cur != "winner" {
			return fail(http.StatusConflict, "invalid_status", "Status pemenang ini sudah "+cur+" dan tidak bisa diubah lagi.")
		}
		var note *string
		if to == "forfeited" {
			note = &reason
		}
		if _, err := tx.Exec(ctx, `UPDATE draw_winners SET status = $2, note = $3 WHERE id = $1`, wid, to, note); err != nil {
			return err
		}
		action := "draw.claimed"
		if to == "forfeited" {
			action = "draw.forfeited"
		}
		if err := audit(ctx, tx, m.OrgID, actor, action, "draw_winner", wid.String(), map[string]any{"status": cur},
			map[string]any{"status": to, "bib": bib, "reason": note}); err != nil {
			return err
		}
		out, err = scanWinner(tx.QueryRow(ctx, winnerSelect+` WHERE w.id = $1`, wid))
		return err
	})
	if respond(w, r, err) {
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// Claim: POST …/draw/winners/{winnerID}/claim — hadiah diterima.
func (h *Handler) Claim(w http.ResponseWriter, r *http.Request) { h.setStatus(w, r, "claimed") }

// Forfeit: POST …/draw/winners/{winnerID}/forfeit {"reason"} — tidak hadir di panggung/ditolak.
func (h *Handler) Forfeit(w http.ResponseWriter, r *http.Request) { h.setStatus(w, r, "forfeited") }

// ===== CSV =====

var statusLabel = map[string]string{"winner": "Pemenang", "claimed": "Hadiah diterima", "forfeited": "Gugur"}

// CSV: GET …/draw/results.csv — owner & admin; tercatat di audit sebelum dikirim.
func (h *Handler) CSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	actor, _ := auth.UserID(ctx)
	eventID, ok := h.event(w, r)
	if !ok {
		return
	}
	var name string
	if err := h.db.QueryRow(ctx, `SELECT name FROM events WHERE id = $1`, eventID).Scan(&name); respond(w, r, err) {
		return
	}
	rows, err := h.db.Query(ctx, winnerSelect+` WHERE w.event_id = $1 ORDER BY p.position, p.created_at, w.seq`, eventID)
	if respond(w, r, err) {
		return
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Winner, error) { return scanWinner(row) })
	if respond(w, r, err) {
		return
	}
	if err := audit(ctx, h.db, m.OrgID, actor, "draw.results.downloaded", "event", eventID.String(), nil, map[string]any{"rows": len(items), "ip": auth.ClientIP(r)}); respond(w, r, err) {
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/csv; charset=utf-8")
	hd.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="undian-%s-%s.csv"`, orEvent(partner.Slugify(name)), time.Now().Format("20060102")))
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	cw, err := csvx.New(w)
	if err == nil {
		err = cw.Header("Hadiah", "Urutan", "BIB", "Nama", "Kategori", "Status", "Waktu undi (WIB)", "Ukuran kolam", "Catatan")
	}
	wib := time.FixedZone("WIB", 7*3600)
	for _, x := range items {
		if err != nil {
			break
		}
		note := ""
		if x.Note != nil {
			note = *x.Note
		}
		err = cw.Row(x.PrizeName, x.Seq, x.Bib, x.Name, x.Category, statusLabel[x.Status], x.DrawnAt.In(wib).Format("2006-01-02 15:04:05"), x.PoolSize, note)
	}
	if err == nil {
		_ = cw.Flush()
	}
}

func orEvent(s string) string {
	if s == "" {
		return "event"
	}
	return s
}

// ===== Hasil publik =====

// PublicResults: GET /v1/events/{eventID}/draw-results — hanya bila draw_public; BIB + nama depan + kategori.
// Pemenang yang gugur tidak tampil. Tanpa data pribadi; tidak diindeks mesin pencari.
func (h *Handler) PublicResults(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(chi.URLParam(r, "eventID"))
	notFound := func() { httpx.Error(w, http.StatusNotFound, "results_not_found", "Hasil undian tidak tersedia") }
	if err != nil {
		notFound()
		return
	}
	var name string
	err = h.db.QueryRow(ctx, `
		SELECT e.name FROM events e JOIN organizations o ON o.id = e.org_id
		WHERE e.id = $1 AND e.draw_public AND e.status IN ('published', 'closed') AND o.kyc_status = 'verified'`, id).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound()
		return
	}
	if respond(w, r, err) {
		return
	}
	rows, err := h.db.Query(ctx, winnerSelect+` WHERE w.event_id = $1 AND w.status IN ('winner', 'claimed') ORDER BY p.position, p.created_at, w.seq`, id)
	if respond(w, r, err) {
		return
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Winner, error) { return scanWinner(row) })
	if respond(w, r, err) {
		return
	}
	type pub struct {
		Seq       int    `json:"seq"`
		Bib       string `json:"bib"`
		FirstName string `json:"first_name"`
		Category  string `json:"category"`
	}
	type prize struct {
		Name    string `json:"name"`
		Winners []pub  `json:"winners"`
	}
	var out []prize
	idx := map[uuid.UUID]int{}
	for _, x := range items {
		i, ok := idx[x.PrizeID]
		if !ok {
			i = len(out)
			idx[x.PrizeID] = i
			out = append(out, prize{Name: x.PrizeName, Winners: []pub{}})
		}
		out[i].Winners = append(out[i].Winners, pub{x.Seq, x.Bib, x.DisplayName, x.Category})
	}
	w.Header().Set("Cache-Control", "public, max-age=10")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	httpx.JSON(w, http.StatusOK, map[string]any{"event": name, "prizes": out})
}
