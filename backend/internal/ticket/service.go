package ticket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

// IssueForOrder menerbitkan satu tiket per peserta order. Idempotent: peserta yang
// sudah punya tiket dilewati (UNIQUE participant_id), jadi aman bila worker mengulang.
func IssueForOrder(ctx context.Context, tx pgx.Tx, signer *Signer, orderID uuid.UUID) (int, error) {
	rows, err := tx.Query(ctx, `
		SELECT p.id, p.category_id, p.event_id, p.org_id
		FROM participants p
		WHERE p.order_id = $1
		  AND NOT EXISTS (SELECT 1 FROM tickets t WHERE t.participant_id = p.id)
		ORDER BY p.id`, orderID)
	if err != nil {
		return 0, err
	}
	type pending struct{ participantID, categoryID, eventID, orgID uuid.UUID }
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.participantID, &p.categoryID, &p.eventID, &p.orgID); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, p := range list {
		// Nomor BIB berurutan per kategori; baris kategori terkunci selama transaksi.
		var prefix string
		var seq int
		if err := tx.QueryRow(ctx, `
			UPDATE ticket_categories SET bib_next = bib_next + 1
			WHERE id = $1 RETURNING bib_prefix, bib_next - 1`, p.categoryID).Scan(&prefix, &seq); err != nil {
			return 0, fmt.Errorf("alokasi BIB: %w", err)
		}
		ticketID := uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO tickets (id, participant_id, order_id, org_id, event_id, category_id, bib_number, qr_token)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (participant_id) DO NOTHING`,
			ticketID, p.participantID, orderID, p.orgID, p.eventID, p.categoryID,
			fmt.Sprintf("%s%04d", prefix, seq), signer.Sign(ticketID, p.eventID),
		); err != nil {
			return 0, err
		}
	}
	return len(list), nil
}

// ===== HTTP =====

type Handler struct {
	db     *pgxpool.Pool
	signer *Signer
}

func NewHandler(db *pgxpool.Pool, signer *Signer) *Handler { return &Handler{db: db, signer: signer} }

type MyTicket struct {
	ID           uuid.UUID `json:"id"`
	Status       string    `json:"status"`
	BibNumber    string    `json:"bib_number"`
	QRToken      string    `json:"qr_token"`
	Participant  string    `json:"participant_name"`
	JerseySize   string    `json:"jersey_size"`
	EventID      uuid.UUID `json:"event_id"`
	EventName    string    `json:"event_name"`
	EventVenue   string    `json:"event_venue"`
	RaceDate     time.Time `json:"race_date"`
	CategoryCode string    `json:"category_code"`
	CategoryName string    `json:"category_name"`
	OrgName      string    `json:"org_name"`
	OrderID      uuid.UUID `json:"order_id"`
}

// MyTickets: GET /v1/me/tickets — semua tiket dari order milik user yang login.
func (h *Handler) MyTickets(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserID(r.Context())
	rows, err := h.db.Query(r.Context(), `
		SELECT t.id, t.status, t.bib_number, t.qr_token, p.full_name, p.jersey_size,
		       e.id, e.name, e.venue, e.race_date, c.code, c.name, o.name, t.order_id
		FROM tickets t
		JOIN participants p ON p.id = t.participant_id
		JOIN orders ord ON ord.id = t.order_id
		JOIN events e ON e.id = t.event_id
		JOIN ticket_categories c ON c.id = t.category_id
		JOIN organizations o ON o.id = t.org_id
		WHERE ord.buyer_id = $1
		ORDER BY e.race_date, t.bib_number`, userID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (MyTicket, error) {
		var t MyTicket
		err := row.Scan(&t.ID, &t.Status, &t.BibNumber, &t.QRToken, &t.Participant, &t.JerseySize,
			&t.EventID, &t.EventName, &t.EventVenue, &t.RaceDate, &t.CategoryCode, &t.CategoryName, &t.OrgName, &t.OrderID)
		return t, err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tickets": out})
}

// PublicKey: GET /v1/qr/public-key — untuk verifikasi QR offline di scanner.
func (h *Handler) PublicKey(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]string{"algorithm": "Ed25519", "public_key": h.signer.PublicKeyBase64()})
}

type ScanRequest struct {
	Token      string `json:"token,omitempty"`        // isi QR
	TicketID   string `json:"ticket_id,omitempty"`    // dari pencarian manual
	PickedUpBy string `json:"picked_up_by,omitempty"` // nama pengambil bila diwakilkan
}

type ScanTicket struct {
	ID              uuid.UUID  `json:"id"`
	Participant     string     `json:"participant_name"`
	CategoryCode    string     `json:"category_code"`
	BibNumber       string     `json:"bib_number"`
	JerseySize      string     `json:"jersey_size"`
	Status          string     `json:"status"`
	CollectedAt     *time.Time `json:"collected_at,omitempty"`
	CollectedByName *string    `json:"collected_by_staff,omitempty"`
	PickedUpBy      *string    `json:"picked_up_by,omitempty"`
}

type ScanResponse struct {
	Result  string      `json:"result"` // ok | already_collected | invalid | wrong_event | not_found | refunded | cancelled
	Message string      `json:"message"`
	Ticket  *ScanTicket `json:"ticket,omitempty"`
}

// Scan: POST /v1/org/{org}/events/{eventID}/scan — dipanggil staf mitra di lokasi race pack.
func (h *Handler) Scan(w http.ResponseWriter, r *http.Request) {
	m, _ := tenant.FromContext(r.Context())
	staffID, _ := auth.UserID(r.Context())
	eventID, err := uuid.Parse(chi.URLParam(r, "eventID"))
	if err != nil || !h.eventBelongsToOrg(r.Context(), eventID, m.OrgID) {
		httpx.Error(w, http.StatusNotFound, "event_not_found", "Event tidak ditemukan")
		return
	}
	var req ScanRequest
	if !httpx.Decode(w, r, &req) {
		return
	}

	resp, ticketID := h.scan(r.Context(), m.OrgID, eventID, staffID, req)
	_, _ = h.db.Exec(r.Context(), `
		INSERT INTO scan_logs (org_id, event_id, ticket_id, staff_id, result) VALUES ($1, $2, $3, $4, $5)`,
		m.OrgID, eventID, ticketID, staffID, resp.Result)
	httpx.JSON(w, http.StatusOK, resp)
}

func (h *Handler) scan(ctx context.Context, orgID, eventID, staffID uuid.UUID, req ScanRequest) (ScanResponse, *uuid.UUID) {
	var ticketID uuid.UUID
	switch {
	case req.Token != "":
		tid, qrEvent, err := Verify(h.signer.PublicKey(), req.Token)
		if err != nil {
			return ScanResponse{Result: "invalid", Message: "QR tidak dikenali atau palsu"}, nil
		}
		if qrEvent != eventID {
			return ScanResponse{Result: "wrong_event", Message: "Tiket ini untuk event lain"}, &tid
		}
		ticketID = tid
	case req.TicketID != "":
		tid, err := uuid.Parse(req.TicketID)
		if err != nil {
			return ScanResponse{Result: "invalid", Message: "ID tiket tidak valid"}, nil
		}
		ticketID = tid
	default:
		return ScanResponse{Result: "invalid", Message: "Isi token QR atau ticket_id"}, nil
	}

	var pickedUp *string
	if s := strings.TrimSpace(req.PickedUpBy); s != "" {
		pickedUp = &s
	}
	// Atomik: hanya scan pertama yang mengubah status. Dobel scan → 0 baris.
	tag, err := h.db.Exec(ctx, `
		UPDATE tickets SET status = 'collected', collected_at = now(), collected_by = $4, collected_by_name = $5
		WHERE id = $1 AND event_id = $2 AND org_id = $3 AND status = 'issued'`,
		ticketID, eventID, orgID, staffID, pickedUp)
	if err != nil {
		return ScanResponse{Result: "error", Message: "Gagal memproses, coba lagi"}, &ticketID
	}

	t, err := h.ticketDetail(ctx, ticketID, eventID, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScanResponse{Result: "not_found", Message: "Tiket tidak terdaftar di event ini"}, &ticketID
	}
	if err != nil {
		return ScanResponse{Result: "error", Message: "Gagal memuat tiket"}, &ticketID
	}
	if tag.RowsAffected() == 1 {
		return ScanResponse{Result: "ok", Message: "Valid — serahkan race pack", Ticket: &t}, &ticketID
	}
	switch t.Status {
	case "collected":
		return ScanResponse{Result: "already_collected", Message: "Race pack sudah diambil sebelumnya", Ticket: &t}, &ticketID
	default:
		return ScanResponse{Result: t.Status, Message: "Tiket berstatus " + t.Status, Ticket: &t}, &ticketID
	}
}

func (h *Handler) ticketDetail(ctx context.Context, ticketID, eventID, orgID uuid.UUID) (ScanTicket, error) {
	var t ScanTicket
	err := h.db.QueryRow(ctx, `
		SELECT t.id, p.full_name, c.code, t.bib_number, p.jersey_size, t.status, t.collected_at, u.name, t.collected_by_name
		FROM tickets t
		JOIN participants p ON p.id = t.participant_id
		JOIN ticket_categories c ON c.id = t.category_id
		LEFT JOIN users u ON u.id = t.collected_by
		WHERE t.id = $1 AND t.event_id = $2 AND t.org_id = $3`, ticketID, eventID, orgID,
	).Scan(&t.ID, &t.Participant, &t.CategoryCode, &t.BibNumber, &t.JerseySize, &t.Status, &t.CollectedAt, &t.CollectedByName, &t.PickedUpBy)
	return t, err
}

func (h *Handler) eventBelongsToOrg(ctx context.Context, eventID, orgID uuid.UUID) bool {
	var ok bool
	_ = h.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id = $1 AND org_id = $2)`, eventID, orgID).Scan(&ok)
	return ok
}

// EventTickets: GET /v1/org/{org}/events/{eventID}/tickets?q= — pencarian manual + progres RPC.
func (h *Handler) EventTickets(w http.ResponseWriter, r *http.Request) {
	m, _ := tenant.FromContext(r.Context())
	eventID, err := uuid.Parse(chi.URLParam(r, "eventID"))
	if err != nil || !h.eventBelongsToOrg(r.Context(), eventID, m.OrgID) {
		httpx.Error(w, http.StatusNotFound, "event_not_found", "Event tidak ditemukan")
		return
	}
	var issued, collected int
	if err := h.db.QueryRow(r.Context(), `
		SELECT count(*) FILTER (WHERE status IN ('issued', 'collected')), count(*) FILTER (WHERE status = 'collected')
		FROM tickets WHERE event_id = $1 AND org_id = $2`, eventID, m.OrgID).Scan(&issued, &collected); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var list []ScanTicket
	if q != "" {
		rows, err := h.db.Query(r.Context(), `
			SELECT t.id, p.full_name, c.code, t.bib_number, p.jersey_size, t.status, t.collected_at, u.name, t.collected_by_name
			FROM tickets t
			JOIN participants p ON p.id = t.participant_id
			JOIN ticket_categories c ON c.id = t.category_id
			LEFT JOIN users u ON u.id = t.collected_by
			WHERE t.event_id = $1 AND t.org_id = $2
			  AND (p.full_name ILIKE '%' || $3 || '%' OR t.bib_number = $3 OR right(p.id_number, 4) = $3)
			ORDER BY p.full_name LIMIT 20`, eventID, m.OrgID, q)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		list, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (ScanTicket, error) {
			var t ScanTicket
			err := row.Scan(&t.ID, &t.Participant, &t.CategoryCode, &t.BibNumber, &t.JerseySize, &t.Status, &t.CollectedAt, &t.CollectedByName, &t.PickedUpBy)
			return t, err
		})
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"stats":   map[string]int{"issued": issued, "collected": collected},
		"results": list,
	})
}
