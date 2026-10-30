// Package finance adalah keuangan mitra: saldo, rilis dana setelah masa tahan, pencairan
// (maker-checker + Disburser), refund per order (Refunder), dan laporan CSV.
// Lihat docs/04-pembayaran-dan-ledger.md.
//
// Aturan utama: setiap perubahan uang ditulis sebagai jurnal ledger di transaksi yang sama
// dengan perubahan statusnya, dengan (ref_type, ref_id) deterministik. Saldo selalu dihitung
// dari ledger_entries. Panggilan ke gateway (Disburser/Refunder) hanya dilakukan worker,
// di luar transaksi yang memegang kunci.
//
// Nomor rekening lengkap hanya ada terenkripsi di payout_accounts.account_enc dan hanya dibuka
// tepat sebelum dikirim ke gateway atau saat super admin menekan "Tampilkan" (tercatat di audit).
// Nomor itu tidak pernah ditulis ke log, audit, email, atau pesan error.
package finance

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/config"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/secretbox"
)

// Service dipakai handler API dan worker.
type Service struct {
	db       *pgxpool.Pool
	box      *secretbox.Box
	cfg      config.FinanceConfig
	mock     bool // DISBURSE_MODE=mock: tidak ada uang sungguhan yang dikirim
	disb     Disburser
	refunder Refunder
}

func NewService(db *pgxpool.Pool, box *secretbox.Box, cfg config.FinanceConfig, disb Disburser, refunder Refunder) *Service {
	return &Service{db: db, box: box, cfg: cfg, mock: cfg.DisburseMode != config.DisburseMidtrans, disb: disb, refunder: refunder}
}

// FromConfig memilih Disburser & Refunder sesuai DISBURSE_MODE (cfg sudah lolos Validate).
func FromConfig(db *pgxpool.Pool, box *secretbox.Box, cfg config.Config) *Service {
	if cfg.DisburseMock() {
		if !cfg.IsDev() {
			slog.Warn("DISBURSE_MODE=mock di luar development: PENCAIRAN & REFUND HANYA SIMULASI, tidak ada uang sungguhan yang dikirim")
		}
		return NewService(db, box, cfg.Finance, MockDisburser{Delay: cfg.Finance.MockDelay}, MockRefunder{})
	}
	coreURL := "https://api.sandbox.midtrans.com"
	if cfg.MidtransIsProduction {
		coreURL = "https://api.midtrans.com"
	}
	return NewService(db, box, cfg.Finance,
		NewIris(cfg.Finance.IrisBaseURL, cfg.Finance.IrisCreatorKey, cfg.Finance.IrisApproverKey),
		NewMidtransRefunder(coreURL, cfg.MidtransServerKey))
}

// apiError: penolakan aturan bisnis yang dikirim apa adanya ke klien.
type apiError struct {
	status    int
	code, msg string
	fields    httpx.Fields
}

func (e apiError) Error() string { return e.msg }

var (
	errNotFound   = apiError{status: http.StatusNotFound, code: "not_found", msg: "Data tidak ditemukan"}
	errConflict   = apiError{status: http.StatusForbidden, code: "conflict_of_interest", msg: "Kamu anggota penyelenggara ini, jadi tidak boleh memutuskan pengajuannya. Minta super admin lain."}
	errMakerCheck = apiError{status: http.StatusForbidden, code: "conflict_of_interest", msg: "Pengaju tidak boleh menyetujui pengajuannya sendiri."}
)

func invalidState(msg string) apiError {
	return apiError{status: http.StatusConflict, code: "invalid_state", msg: msg}
}

func validation(f httpx.Fields) apiError {
	return apiError{status: http.StatusUnprocessableEntity, code: "validation", fields: f}
}

func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae apiError
	switch {
	case errors.As(err, &ae) && ae.code == "validation":
		httpx.Validation(w, ae.fields)
	case errors.As(err, &ae):
		httpx.Error(w, ae.status, ae.code, ae.msg)
	default:
		httpx.Internal(w, r, err)
	}
}

func isUnique(err error, constraint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == constraint
}

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// audit menulis audit_logs. after TIDAK BOLEH berisi nomor rekening atau kunci: hanya id, jumlah,
// status, alasan, last4. r boleh nil (aksi worker, tanpa aktor).
func audit(ctx context.Context, q querier, r *http.Request, orgID uuid.UUID, action, entity, entityID string, before, after map[string]any) error {
	var actor *uuid.UUID
	if id, ok := auth.UserID(ctx); ok {
		actor = &id
	}
	if after == nil {
		after = map[string]any{}
	}
	if r != nil {
		after["ip"] = auth.ClientIP(r)
	}
	var b any
	if before != nil {
		b = before
	}
	_, err := q.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, org_id, action, entity, entity_id, before, after)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, actor, orgID, action, entity, entityID, b, after)
	return err
}

// isMember: super admin yang anggota org tidak boleh memutuskan (benturan kepentingan).
func isMember(ctx context.Context, q querier, orgID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM org_members WHERE org_id = $1 AND user_id = $2)`, orgID, userID).Scan(&ok)
	return ok, err
}

// queueOrgEmail mengantrekan satu email per owner/finance org (topik payout.email / refund.email).
func queueOrgEmail(ctx context.Context, q querier, topic, idKey string, id, orgID uuid.UUID, event string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO outbox (topic, payload)
		SELECT $1, jsonb_build_object($2::text, $3::uuid, 'event', $5::text, 'user_id', m.user_id, 'audience', 'org')
		FROM org_members m WHERE m.org_id = $4 AND m.role IN ('owner', 'finance')`, topic, idKey, id, orgID, event)
	return err
}

func enqueue(ctx context.Context, q querier, topic string, payload map[string]any) error {
	b, _ := json.Marshal(payload)
	_, err := q.Exec(ctx, `INSERT INTO outbox (topic, payload) VALUES ($1, $2)`, topic, b)
	return err
}

func page(r *http.Request) int {
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	return max(p, 1)
}

const pageSize = 20

// backoff untuk percobaan ke-n (1, 2, ...): 30 dtk, 1, 2, 4, 8 menit.
func backoff(attempt int) time.Duration {
	return 30 * time.Second << min(max(attempt-1, 0), 8)
}

// maxAttempts: batas percobaan eksekusi ke gateway untuk kegagalan sementara (timeout/5xx).
const maxAttempts = 5

func userID(ctx context.Context) uuid.UUID {
	id, _ := auth.UserID(ctx)
	return id
}
