package finance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/ledger"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

// AccountView: rekening tersamar (tanpa nomor lengkap). Juga bentuk payouts.account_snapshot.
type AccountView struct {
	BankCode      string `json:"bank_code"`
	BankName      string `json:"bank_name"`
	AccountHolder string `json:"account_holder"`
	Last4         string `json:"last4"`
}

type Payout struct {
	ID              uuid.UUID   `json:"id"`
	OrgID           uuid.UUID   `json:"-"`
	OrgName         string      `json:"org_name"`
	OrgSlug         string      `json:"org_slug"`
	Amount          int64       `json:"amount"`
	Status          string      `json:"status"`
	Account         AccountView `json:"account"`
	ReferenceNo     *string     `json:"reference_no"`
	RejectReason    *string     `json:"reject_reason"`
	FailReason      *string     `json:"fail_reason"`
	Attempts        int         `json:"attempts"`
	RequestedBy     uuid.UUID   `json:"-"`
	RequestedByName string      `json:"requested_by_name"`
	ApprovedByName  *string     `json:"approved_by_name"`
	CreatedAt       time.Time   `json:"created_at"`
	ApprovedAt      *time.Time  `json:"approved_at"`
	CompletedAt     *time.Time  `json:"completed_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

const payoutCols = `p.id, p.org_id, o.name, o.slug, p.amount, p.status, p.account_snapshot, p.reference_no, p.reject_reason,
	p.fail_reason, p.attempts, p.requested_by, ru.name, au.name, p.created_at, p.approved_at, p.completed_at, p.updated_at`

const payoutFrom = ` FROM payouts p JOIN organizations o ON o.id = p.org_id JOIN users ru ON ru.id = p.requested_by
	LEFT JOIN users au ON au.id = p.approved_by `

func scanPayout(row pgx.CollectableRow) (Payout, error) {
	var p Payout
	var snap []byte
	err := row.Scan(&p.ID, &p.OrgID, &p.OrgName, &p.OrgSlug, &p.Amount, &p.Status, &snap, &p.ReferenceNo, &p.RejectReason,
		&p.FailReason, &p.Attempts, &p.RequestedBy, &p.RequestedByName, &p.ApprovedByName, &p.CreatedAt, &p.ApprovedAt, &p.CompletedAt, &p.UpdatedAt)
	if err == nil {
		_ = json.Unmarshal(snap, &p.Account)
	}
	return p, err
}

func (s *Service) payouts(ctx context.Context, q querier, where string, args ...any) ([]Payout, error) {
	rows, err := q.Query(ctx, `SELECT `+payoutCols+payoutFrom+where, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanPayout)
}

func (s *Service) payout(ctx context.Context, q querier, id uuid.UUID, lock bool) (Payout, error) {
	sql := `WHERE p.id = $1`
	if lock {
		sql += ` FOR UPDATE OF p`
	}
	list, err := s.payouts(ctx, q, sql, id)
	if err != nil {
		return Payout{}, err
	}
	if len(list) == 0 {
		return Payout{}, errNotFound
	}
	return list[0], nil
}

// payoutAccount: rekening aktif org (nil bila belum ada).
func payoutAccount(ctx context.Context, q querier, orgID uuid.UUID) (*AccountView, *uuid.UUID, error) {
	var a AccountView
	var id uuid.UUID
	err := q.QueryRow(ctx, `SELECT id, bank_code, bank_name, account_holder, account_last4 FROM payout_accounts WHERE org_id = $1`, orgID).
		Scan(&id, &a.BankCode, &a.BankName, &a.AccountHolder, &a.Last4)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	return &a, &id, err
}

// Blocker: alasan org tidak bisa mencairkan saat ini (nil = boleh).
type Blocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var (
	blockNotVerified = Blocker{"org_not_verified", "Lengkapi verifikasi penyelenggara dan rekening sebelum mencairkan dana."}
	blockNoAccount   = Blocker{"no_payout_account", "Lengkapi verifikasi penyelenggara dan rekening: belum ada rekening pencairan yang terverifikasi."}
	blockInProgress  = Blocker{"payout_in_progress", "Masih ada pencairan yang sedang berjalan. Tunggu sampai selesai."}
)

// blocker memeriksa syarat pencairan selain jumlah. available ikut dicek bila negatif / di bawah minimum.
func (s *Service) blocker(ctx context.Context, q querier, orgID uuid.UUID, kyc string, hasAccount bool, available int64) (*Blocker, error) {
	if kyc != "verified" {
		return &blockNotVerified, nil
	}
	if !hasAccount {
		return &blockNoAccount, nil
	}
	var active bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payouts WHERE org_id = $1 AND status IN ('requested', 'approved', 'processing'))`, orgID).Scan(&active); err != nil {
		return nil, err
	}
	if active {
		return &blockInProgress, nil
	}
	if available < 0 {
		return &Blocker{"insufficient_balance", "Saldo tersedia negatif karena refund melebihi saldo. Pencairan bisa lagi setelah tertutup penjualan baru."}, nil
	}
	if available < s.cfg.PayoutMin {
		return &Blocker{"below_minimum", fmt.Sprintf("Saldo tersedia belum mencapai minimum pencairan %s.", rupiah(s.cfg.PayoutMin))}, nil
	}
	return nil, nil
}

// Summary: GET /v1/org/{org}/finance/summary
func (s *Service) Summary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := tenant.OrgID(ctx)
	var kyc string
	if err := s.db.QueryRow(ctx, `SELECT kyc_status FROM organizations WHERE id = $1`, orgID).Scan(&kyc); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	bal, err := ledger.OrgBalances(ctx, s.db, orgID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	acc, _, err := payoutAccount(ctx, s.db, orgID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	block, err := s.blocker(ctx, s.db, orgID, kyc, acc != nil, bal.Available)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"balances": bal, "account": acc, "kyc_status": kyc,
		"rules":      map[string]any{"payout_min": s.cfg.PayoutMin, "hold_days": s.cfg.HoldDays},
		"can_payout": block == nil, "blocker": block, "mock": s.mock,
	})
}

// OrgPayouts: GET /v1/org/{org}/payouts?page=
func (s *Service) OrgPayouts(w http.ResponseWriter, r *http.Request) {
	p := page(r)
	list, err := s.payouts(r.Context(), s.db, `WHERE p.org_id = $1 ORDER BY p.created_at DESC LIMIT $2 OFFSET $3`,
		tenant.OrgID(r.Context()), pageSize, (p-1)*pageSize)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list, "page": p, "page_size": pageSize})
}

// RequestPayout: POST /v1/org/{org}/payouts {amount} + header Idempotency-Key (wajib).
func (s *Service) RequestPayout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := tenant.OrgID(ctx)
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 100 {
		httpx.Error(w, http.StatusBadRequest, "idempotency_key_required", "Header Idempotency-Key wajib diisi (maks. 100 karakter)")
		return
	}
	var in struct {
		Amount json.RawMessage `json:"amount"` // angka JSON bulat; string, pecahan, dan eksponen ditolak
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	amount, err := strconv.ParseInt(string(in.Amount), 10, 64)
	if err != nil || amount <= 0 {
		httpx.Validation(w, httpx.Fields{"amount": "Jumlah harus bilangan bulat rupiah lebih dari 0"})
		return
	}
	var out Payout
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		// Kunci baris org: pengajuan paralel org yang sama antre di sini, jadi saldo dihitung berurutan.
		var kyc string
		if err := tx.QueryRow(ctx, `SELECT kyc_status FROM organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&kyc); err != nil {
			return err
		}
		// Kunci idempoten yang sama → kembalikan pengajuan yang sudah ada.
		if existing, err := s.payouts(ctx, tx, `WHERE p.org_id = $1 AND p.idempotency_key = $2`, orgID, key); err != nil {
			return err
		} else if len(existing) > 0 {
			out = existing[0]
			return nil
		}
		bal, err := ledger.OrgBalances(ctx, tx, orgID)
		if err != nil {
			return err
		}
		acc, accID, err := payoutAccount(ctx, tx, orgID)
		if err != nil {
			return err
		}
		block, err := s.blocker(ctx, tx, orgID, kyc, acc != nil, bal.Available)
		if err != nil {
			return err
		}
		if block != nil && block.Code != "below_minimum" {
			status := http.StatusConflict
			if block.Code == "insufficient_balance" {
				status = http.StatusUnprocessableEntity
			}
			return apiError{status: status, code: block.Code, msg: block.Message}
		}
		if amount < s.cfg.PayoutMin {
			return apiError{status: http.StatusUnprocessableEntity, code: "below_minimum",
				msg: "Jumlah pencairan minimum " + rupiah(s.cfg.PayoutMin) + "."}
		}
		if amount > bal.Available {
			return apiError{status: http.StatusUnprocessableEntity, code: "insufficient_balance",
				msg: "Jumlah melebihi saldo tersedia " + rupiah(bal.Available) + "."}
		}
		snap, _ := json.Marshal(acc)
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO payouts (org_id, payout_account_id, amount, requested_by, idempotency_key, account_snapshot)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, orgID, accID, amount, userID(ctx), key, snap).Scan(&id); err != nil {
			if isUnique(err, "payouts_active_uniq") {
				return apiError{status: http.StatusConflict, code: blockInProgress.Code, msg: blockInProgress.Message}
			}
			return err
		}
		j, err := ledger.PayoutRequest(id, orgID, amount)
		if err != nil {
			return err
		}
		if _, err := ledger.Post(ctx, tx, j); err != nil {
			return err
		}
		if err := audit(ctx, tx, r, orgID, "payout.requested", "payout", id.String(), nil,
			map[string]any{"amount": amount, "available_before": bal.Available, "last4": acc.Last4}); err != nil {
			return err
		}
		if err := queueOrgEmail(ctx, tx, "payout.email", "payout_id", id, orgID, "requested"); err != nil {
			return err
		}
		out, err = s.payout(ctx, tx, id, false)
		return err
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// ===== Super admin =====

var payoutStatuses = []string{"requested", "approved", "processing", "completed", "failed", "rejected"}

type adminPayout struct {
	Payout
	OrgAvailable int64 `json:"org_available"`
}

// AdminPayouts: GET /v1/admin/payouts?status=&page= (default requested, terlama dulu).
func (s *Service) AdminPayouts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "requested"
	}
	if status != "all" && !slices.Contains(payoutStatuses, status) {
		httpx.Validation(w, httpx.Fields{"status": "Status tidak dikenal"})
		return
	}
	order := "p.created_at ASC"
	if status != "requested" && status != "approved" {
		order = "p.updated_at DESC"
	}
	p := page(r)
	list, err := s.payouts(ctx, s.db, `WHERE ($1 = 'all' OR p.status = $1) ORDER BY `+order+`, p.id LIMIT $2 OFFSET $3`, status, pageSize, (p-1)*pageSize)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	items := make([]adminPayout, 0, len(list))
	for _, it := range list {
		bal, err := ledger.OrgBalances(ctx, s.db, it.OrgID)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		items = append(items, adminPayout{it, bal.Available})
	}
	counts, err := statusCounts(ctx, s.db, "payouts")
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "counts": counts, "page": p, "page_size": pageSize, "mock": s.mock})
}

func statusCounts(ctx context.Context, q querier, table string) (map[string]int, error) {
	rows, err := q.Query(ctx, `SELECT status, count(*) FROM `+table+` GROUP BY status`)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	var st string
	var n int
	_, err = pgx.ForEachRow(rows, []any{&st, &n}, func() error { out[st] = n; return nil })
	return out, err
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, errNotFound
	}
	return id, nil
}

// AdminPayout: GET /v1/admin/payouts/{id}
func (s *Service) AdminPayout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	p, err := s.payout(ctx, s.db, id, false)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	bal, err := ledger.OrgBalances(ctx, s.db, p.OrgID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	hist, err := s.payouts(ctx, s.db, `WHERE p.org_id = $1 AND p.id <> $2 ORDER BY p.created_at DESC LIMIT 20`, p.OrgID, p.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	conflict, err := isMember(ctx, s.db, p.OrgID, userID(ctx))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var kyc string
	_ = s.db.QueryRow(ctx, `SELECT kyc_status FROM organizations WHERE id = $1`, p.OrgID).Scan(&kyc)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"payout": p, "balances": bal, "history": hist, "org_kyc_status": kyc,
		"conflict_of_interest": conflict, "mock": s.mock,
	})
}

// AdminRevealAccount: POST /v1/admin/payouts/{id}/reveal — nomor rekening lengkap, tercatat di audit.
func (s *Service) AdminRevealAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var orgID uuid.UUID
	var sealed string
	err = s.db.QueryRow(ctx, `SELECT p.org_id, a.account_enc FROM payouts p JOIN payout_accounts a ON a.id = p.payout_account_id WHERE p.id = $1`, id).
		Scan(&orgID, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, r, errNotFound)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	value, err := s.box.OpenString(sealed)
	if err != nil {
		slog.ErrorContext(ctx, "dekripsi rekening pencairan gagal", "payout_id", id, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "decrypt_failed", "Rekening tidak bisa dibuka: kunci enkripsi server tidak cocok.")
		return
	}
	if err := audit(ctx, s.db, r, orgID, "payout.account.reveal", "payout", id.String(), nil, map[string]any{"field": "account"}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, map[string]string{"field": "account", "value": value})
}

// AdminApprovePayout: POST /v1/admin/payouts/{id}/approve — requested → approved; worker mengeksekusi.
func (s *Service) AdminApprovePayout(w http.ResponseWriter, r *http.Request) {
	s.decidePayout(w, r, true, "")
}

// AdminRejectPayout: POST /v1/admin/payouts/{id}/reject {reason} — saldo kembali tersedia.
func (s *Service) AdminRejectPayout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	reason := strings.TrimSpace(in.Reason)
	if n := utf8.RuneCountInString(reason); n < 10 || n > 1000 {
		httpx.Validation(w, httpx.Fields{"reason": "Alasan penolakan 10–1.000 karakter"})
		return
	}
	s.decidePayout(w, r, false, reason)
}

func (s *Service) decidePayout(w http.ResponseWriter, r *http.Request, approve bool, reason string) {
	ctx := r.Context()
	id, err := pathID(r)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	actor := userID(ctx)
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		p, err := s.payout(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if member, err := isMember(ctx, tx, p.OrgID, actor); err != nil {
			return err
		} else if member {
			return errConflict
		}
		if p.RequestedBy == actor {
			return errMakerCheck
		}
		if p.Status != "requested" {
			return invalidState("Pencairan ini sudah diputuskan (status " + p.Status + ").")
		}
		before := map[string]any{"status": p.Status}
		if approve {
			if _, err := tx.Exec(ctx, `
				UPDATE payouts SET status = 'approved', approved_by = $2, approved_at = now(), next_attempt_at = now(), updated_at = now()
				WHERE id = $1`, id, actor); err != nil {
				return err
			}
			if err := audit(ctx, tx, r, p.OrgID, "payout.approved", "payout", id.String(), before,
				map[string]any{"status": "approved", "amount": p.Amount, "last4": p.Account.Last4}); err != nil {
				return err
			}
			return queueOrgEmail(ctx, tx, "payout.email", "payout_id", id, p.OrgID, "approved")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE payouts SET status = 'rejected', approved_by = $2, reject_reason = $3, updated_at = now() WHERE id = $1`,
			id, actor, reason); err != nil {
			return err
		}
		j, err := ledger.PayoutReject(id, p.OrgID, p.Amount)
		if err != nil {
			return err
		}
		if _, err := ledger.Post(ctx, tx, j); err != nil {
			return err
		}
		if err := audit(ctx, tx, r, p.OrgID, "payout.rejected", "payout", id.String(), before,
			map[string]any{"status": "rejected", "amount": p.Amount, "reason": reason}); err != nil {
			return err
		}
		return queueOrgEmail(ctx, tx, "payout.email", "payout_id", id, p.OrgID, "rejected")
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.AdminPayout(w, r)
}

// ===== Eksekusi (worker) =====

type claimed struct {
	id, orgID         uuid.UUID
	amount            int64
	attempts          int
	bank, holder, enc string
	last4, orgName    string
}

// ExecutePayouts mengirim pencairan approved ke Disburser. Baris diklaim dengan transaksi pendek
// (sewa 2 menit lewat next_attempt_at), panggilan gateway terjadi di luar transaksi, lalu hasilnya
// dicatat di transaksi baru. Diulang dengan id payout yang sama aman (idempoten di gateway).
func (s *Service) ExecutePayouts(ctx context.Context) error {
	for range 20 {
		var c claimed
		err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			err := tx.QueryRow(ctx, `
				SELECT p.id, p.org_id, p.amount, p.attempts, a.bank_code, a.account_holder, a.account_enc, a.account_last4, o.name
				FROM payouts p JOIN payout_accounts a ON a.id = p.payout_account_id JOIN organizations o ON o.id = p.org_id
				WHERE p.status = 'approved' AND coalesce(p.next_attempt_at, now()) <= now()
				ORDER BY p.approved_at LIMIT 1 FOR UPDATE OF p SKIP LOCKED`).
				Scan(&c.id, &c.orgID, &c.amount, &c.attempts, &c.bank, &c.holder, &c.enc, &c.last4, &c.orgName)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE payouts SET next_attempt_at = now() + interval '2 minutes' WHERE id = $1`, c.id)
			return err
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ref, callErr := s.callDisburser(ctx, c)
		if err := s.afterCreate(ctx, c, ref, callErr); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) callDisburser(ctx context.Context, c claimed) (string, error) {
	account, err := s.box.OpenString(c.enc)
	if err != nil {
		return "", fmt.Errorf("dekripsi rekening: %w", err) // masalah kunci server: diulang, bukan gagal
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.disb.Create(callCtx, DisburseRequest{
		ID: c.id.String(), Amount: c.amount, BankCode: c.bank, AccountNumber: account, AccountHolder: c.holder,
		Notes: "Pencairan RaceGo " + c.orgName,
	})
}

func (s *Service) afterCreate(ctx context.Context, c claimed, ref string, callErr error) error {
	if callErr == nil {
		return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `
				UPDATE payouts SET status = 'processing', reference_no = $2, attempts = attempts + 1, next_attempt_at = NULL, updated_at = now()
				WHERE id = $1 AND status = 'approved'`, c.id, ref)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			slog.InfoContext(ctx, "pencairan dikirim ke gateway", "payout_id", c.id, "amount", c.amount)
			return audit(ctx, tx, nil, c.orgID, "payout.processing", "payout", c.id.String(),
				map[string]any{"status": "approved"}, map[string]any{"status": "processing", "reference_no": ref})
		})
	}
	if reason, perm := isPermanent(callErr); perm {
		return s.failPayout(ctx, c.id, reason)
	}
	attempt := c.attempts + 1
	slog.WarnContext(ctx, "pencairan gagal sementara, akan dicoba lagi", "payout_id", c.id, "attempt", attempt, "err", callErr)
	if attempt >= maxAttempts {
		return s.failPayout(ctx, c.id, fmt.Sprintf("Gateway tidak merespons setelah %d percobaan", attempt))
	}
	_, err := s.db.Exec(ctx, `
		UPDATE payouts SET attempts = $2, next_attempt_at = now() + make_interval(secs => $3), fail_reason = $4, updated_at = now()
		WHERE id = $1 AND status = 'approved'`, c.id, attempt, backoff(attempt).Seconds(), clip(callErr.Error(), 200))
	return err
}

// CheckPayouts menanyakan status payout processing ke gateway (pola payment/status.go; tidak bergantung webhook).
func (s *Service) CheckPayouts(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT id, reference_no FROM payouts WHERE status = 'processing' AND reference_no IS NOT NULL ORDER BY updated_at LIMIT 50`)
	if err != nil {
		return err
	}
	type item struct {
		ID  uuid.UUID
		Ref string
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[item])
	if err != nil {
		return err
	}
	for _, it := range items {
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		st, err := s.disb.Status(callCtx, it.Ref)
		cancel()
		if err != nil {
			slog.WarnContext(ctx, "cek status pencairan", "payout_id", it.ID, "err", err)
			continue
		}
		switch st.State {
		case DisburseCompleted:
			err = s.completePayout(ctx, it.ID)
		case DisburseFailed:
			err = s.failPayout(ctx, it.ID, st.Reason)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) completePayout(ctx context.Context, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var orgID uuid.UUID
		var amount int64
		var status string
		if err := tx.QueryRow(ctx, `SELECT org_id, amount, status FROM payouts WHERE id = $1 FOR UPDATE`, id).Scan(&orgID, &amount, &status); err != nil {
			return err
		}
		if status != "processing" { // hanya maju; completed/failed tidak pernah berubah lagi
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE payouts SET status = 'completed', completed_at = now(), fail_reason = NULL, updated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		j, err := ledger.PayoutComplete(id, orgID, amount)
		if err != nil {
			return err
		}
		if _, err := ledger.Post(ctx, tx, j); err != nil {
			return err
		}
		slog.InfoContext(ctx, "pencairan selesai", "payout_id", id, "amount", amount)
		if err := audit(ctx, tx, nil, orgID, "payout.completed", "payout", id.String(),
			map[string]any{"status": status}, map[string]any{"status": "completed", "amount": amount}); err != nil {
			return err
		}
		return queueOrgEmail(ctx, tx, "payout.email", "payout_id", id, orgID, "completed")
	})
}

func (s *Service) failPayout(ctx context.Context, id uuid.UUID, reason string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var orgID uuid.UUID
		var amount int64
		var status string
		if err := tx.QueryRow(ctx, `SELECT org_id, amount, status FROM payouts WHERE id = $1 FOR UPDATE`, id).Scan(&orgID, &amount, &status); err != nil {
			return err
		}
		if status != "approved" && status != "processing" {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE payouts SET status = 'failed', fail_reason = $2, next_attempt_at = NULL, updated_at = now() WHERE id = $1`, id, clip(reason, 200)); err != nil {
			return err
		}
		j, err := ledger.PayoutFail(id, orgID, amount)
		if err != nil {
			return err
		}
		if _, err := ledger.Post(ctx, tx, j); err != nil {
			return err
		}
		slog.WarnContext(ctx, "pencairan gagal, saldo dikembalikan", "payout_id", id, "amount", amount)
		if err := audit(ctx, tx, nil, orgID, "payout.failed", "payout", id.String(),
			map[string]any{"status": status}, map[string]any{"status": "failed", "amount": amount, "reason": clip(reason, 200)}); err != nil {
			return err
		}
		return queueOrgEmail(ctx, tx, "payout.email", "payout_id", id, orgID, "failed")
	})
}

// rupiah: 1234567 → "Rp1.234.567".
func rupiah(v int64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := strconv.FormatInt(v, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-Rp" + b.String()
	}
	return "Rp" + b.String()
}
