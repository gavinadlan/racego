package ticket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

// Absen hari-H: staf memindai QR di lokasi lomba untuk menandai peserta yang benar-benar hadir.
// Terpisah dari pengambilan race pack: tidak mengubah tickets.status maupun collected_*.
// Lihat docs/05-tiket-qr-dan-scan.md dan docs/12-undian.md.

// wibZone: jendela absen bawaan dihitung dalam waktu Indonesia Barat (events.race_date adalah tanggal lokal).
var wibZone = time.FixedZone("WIB", 7*3600)

type attendErr struct {
	status    int
	code, msg string
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// AttendanceWindow: kapan absen (dan undian) boleh berjalan. Bawaan: dari awal tanggal lomba (WIB)
// sampai akhir hari berikutnya; attendance_opens_at/closes_at pada event menimpa salah satu ujungnya.
func AttendanceWindow(ctx context.Context, q rowQuerier, eventID uuid.UUID) (opens, closes time.Time, err error) {
	var race time.Time
	var o, c *time.Time
	if err = q.QueryRow(ctx, `SELECT race_date::timestamp, attendance_opens_at, attendance_closes_at FROM events WHERE id = $1`, eventID).Scan(&race, &o, &c); err != nil {
		return
	}
	day := time.Date(race.Year(), race.Month(), race.Day(), 0, 0, 0, 0, wibZone)
	opens, closes = day, day.AddDate(0, 0, 2)
	if o != nil {
		opens = *o
	}
	if c != nil {
		closes = *c
	}
	return
}

func windowMessage(opens, closes time.Time) string {
	f := "02-01-2006 15.04"
	return fmt.Sprintf("Absen hari-H dibuka %s sampai %s WIB", opens.In(wibZone).Format(f), closes.In(wibZone).Format(f))
}

// inWindow: true bila t di dalam jendela; message dipakai untuk respons attend_not_open.
func inWindow(t, opens, closes time.Time) bool { return !t.Before(opens) && t.Before(closes) }

// LockEventDraw mengambil kunci advisory per event sampai transaksi selesai. Pengundian dan pembatalan
// absen memakainya, jadi "undi" dan "batalkan absen" tidak pernah berjalan bersamaan pada event yang sama.
func LockEventDraw(ctx context.Context, tx pgx.Tx, eventID uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('draw:' || $1::text, 0))`, eventID.String())
	return err
}

// attend: UPDATE atomik, hanya pemindai pertama yang berhasil. Hasil: ok | already_attended | invalid |
// wrong_event | not_found | refunded | cancelled.
func attend(ctx context.Context, db dbtx, signer *Signer, orgID, eventID, staffID uuid.UUID, req ScanRequest) (ScanResponse, *uuid.UUID) {
	var ticketID uuid.UUID
	switch {
	case req.Token != "":
		tid, qrEvent, err := Verify(signer.PublicKey(), req.Token)
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
	tag, err := db.Exec(ctx, `
		UPDATE tickets SET attended_at = now(), attended_by = $4
		WHERE id = $1 AND event_id = $2 AND org_id = $3 AND status IN ('issued', 'collected') AND attended_at IS NULL`,
		ticketID, eventID, orgID, staffID)
	if err != nil {
		return ScanResponse{Result: "error", Message: "Gagal memproses, coba lagi"}, &ticketID
	}
	t, err := ticketDetail(ctx, db, ticketID, eventID, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScanResponse{Result: "not_found", Message: "Tiket tidak terdaftar di event ini"}, &ticketID
	}
	if err != nil {
		return ScanResponse{Result: "error", Message: "Gagal memuat tiket"}, &ticketID
	}
	switch {
	case tag.RowsAffected() == 1:
		return ScanResponse{Result: "ok", Message: "Hadir tercatat", Ticket: &t}, &ticketID
	case t.Status == "refunded" || t.Status == "cancelled":
		return ScanResponse{Result: t.Status, Message: "Tiket berstatus " + t.Status + ", tidak bisa absen", Ticket: &t}, &ticketID
	default:
		return ScanResponse{Result: "already_attended", Message: "Peserta sudah diabsen sebelumnya", Ticket: &t}, &ticketID
	}
}

// Attend: POST /v1/org/{org}/events/{eventID}/attend — owner, admin, rpc_staff.
func (h *Handler) Attend(w http.ResponseWriter, r *http.Request) {
	m, _ := tenant.FromContext(r.Context())
	staffID, _ := auth.UserID(r.Context())
	eventID, ok := h.eventFromURL(w, r)
	if !ok {
		return
	}
	var req ScanRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	opens, closes, err := AttendanceWindow(r.Context(), h.db, eventID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if !inWindow(time.Now(), opens, closes) {
		httpx.Error(w, http.StatusConflict, "attend_not_open", windowMessage(opens, closes))
		return
	}
	resp, ticketID := attend(r.Context(), h.db, h.signer, m.OrgID, eventID, staffID, req)
	_, _ = h.db.Exec(r.Context(), `
		INSERT INTO scan_logs (org_id, event_id, ticket_id, staff_id, result, kind) VALUES ($1, $2, $3, $4, $5, 'attend')`,
		m.OrgID, eventID, ticketID, staffID, resp.Result)
	httpx.JSON(w, http.StatusOK, resp)
}

// CancelAttend: POST /v1/org/{org}/events/{eventID}/attend/cancel {"ticket_id","reason"} — owner & admin saja.
// Salah scan: absen dihapus, alasan 5–300 karakter wajib dan tercatat di audit. Ditolak bila tiket itu
// pemenang undian aktif (attendance_in_use): pemenang tidak boleh kehilangan kelayakannya diam-diam.
func (h *Handler) CancelAttend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	staffID, _ := auth.UserID(ctx)
	eventID, ok := h.eventFromURL(w, r)
	if !ok {
		return
	}
	var in struct {
		TicketID string `json:"ticket_id"`
		Reason   string `json:"reason"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	reason := strings.TrimSpace(in.Reason)
	f := httpx.Fields{}
	if n := utf8.RuneCountInString(reason); n < 5 || n > 300 {
		f["reason"] = "Alasan 5–300 karakter"
	}
	tid, err := uuid.Parse(in.TicketID)
	if err != nil {
		f["ticket_id"] = "ID tiket tidak valid"
	}
	if len(f) > 0 {
		httpx.Validation(w, f)
		return
	}
	var apiErr *attendErr
	err = pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		if err := LockEventDraw(ctx, tx, eventID); err != nil {
			return err
		}
		var attendedAt *time.Time
		err := tx.QueryRow(ctx, `SELECT attended_at FROM tickets WHERE id = $1 AND event_id = $2 AND org_id = $3 FOR UPDATE`,
			tid, eventID, m.OrgID).Scan(&attendedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			apiErr = &attendErr{http.StatusNotFound, "ticket_not_found", "Tiket tidak ditemukan di event ini"}
			return nil
		}
		if err != nil {
			return err
		}
		if attendedAt == nil {
			apiErr = &attendErr{http.StatusConflict, "not_attended", "Peserta ini belum diabsen"}
			return nil
		}
		var inUse bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM draw_winners WHERE ticket_id = $1 AND status IN ('winner', 'claimed'))`, tid).Scan(&inUse); err != nil {
			return err
		}
		if inUse {
			apiErr = &attendErr{http.StatusConflict, "attendance_in_use", "Peserta ini pemenang undian aktif. Ubah status pemenangnya (Tidak hadir) dulu bila absen memang salah."}
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE tickets SET attended_at = NULL, attended_by = NULL WHERE id = $1`, tid); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO audit_logs (actor_id, org_id, action, entity, entity_id, before, after) VALUES ($1, $2, 'attendance.cancelled', 'ticket', $3, $4, $5)`,
			staffID, m.OrgID, tid.String(), map[string]any{"attended_at": attendedAt}, map[string]any{"reason": reason, "event_id": eventID})
		return err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if apiErr != nil {
		httpx.Error(w, apiErr.status, apiErr.code, apiErr.msg)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ===== Pengaturan jendela absen =====

type attendanceSettings struct {
	OpensAt          *time.Time `json:"opens_at"`
	ClosesAt         *time.Time `json:"closes_at"`
	EffectiveOpensAt time.Time  `json:"effective_opens_at"`
	EffectiveCloses  time.Time  `json:"effective_closes_at"`
	Open             bool       `json:"open"`
}

func (h *Handler) loadAttendanceSettings(ctx context.Context, eventID, orgID uuid.UUID) (attendanceSettings, error) {
	var s attendanceSettings
	if err := h.db.QueryRow(ctx, `SELECT attendance_opens_at, attendance_closes_at FROM events WHERE id = $1 AND org_id = $2`, eventID, orgID).Scan(&s.OpensAt, &s.ClosesAt); err != nil {
		return s, err
	}
	var err error
	s.EffectiveOpensAt, s.EffectiveCloses, err = AttendanceWindow(ctx, h.db, eventID)
	s.Open = inWindow(time.Now(), s.EffectiveOpensAt, s.EffectiveCloses)
	return s, err
}

// AttendanceSettings: GET /v1/org/{org}/events/{eventID}/attendance/settings — owner, admin, rpc_staff.
func (h *Handler) AttendanceSettings(w http.ResponseWriter, r *http.Request) {
	m, _ := tenant.FromContext(r.Context())
	eventID, ok := h.eventFromURL(w, r)
	if !ok {
		return
	}
	s, err := h.loadAttendanceSettings(r.Context(), eventID, m.OrgID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// SetAttendanceSettings: PUT …/attendance/settings {"opens_at","closes_at"} (null = bawaan) — owner & admin.
func (h *Handler) SetAttendanceSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := tenant.FromContext(ctx)
	userID, _ := auth.UserID(ctx)
	eventID, ok := h.eventFromURL(w, r)
	if !ok {
		return
	}
	var in struct {
		OpensAt  *time.Time `json:"opens_at"`
		ClosesAt *time.Time `json:"closes_at"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if in.OpensAt != nil && in.ClosesAt != nil && !in.ClosesAt.After(*in.OpensAt) {
		httpx.Validation(w, httpx.Fields{"closes_at": "Harus setelah waktu buka absen"})
		return
	}
	var before attendanceSettings
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT attendance_opens_at, attendance_closes_at FROM events WHERE id = $1 AND org_id = $2 FOR UPDATE`, eventID, m.OrgID).Scan(&before.OpensAt, &before.ClosesAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE events SET attendance_opens_at = $2, attendance_closes_at = $3 WHERE id = $1`, eventID, in.OpensAt, in.ClosesAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO audit_logs (actor_id, org_id, action, entity, entity_id, before, after) VALUES ($1, $2, 'attendance.settings.changed', 'event', $3, $4, $5)`,
			userID, m.OrgID, eventID.String(), map[string]any{"opens_at": before.OpensAt, "closes_at": before.ClosesAt}, map[string]any{"opens_at": in.OpensAt, "closes_at": in.ClosesAt})
		return err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.AttendanceSettings(w, r)
}
