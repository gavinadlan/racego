package finance

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/ledger"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

// ===== Order org (untuk refund) =====

type OrgOrder struct {
	ID               uuid.UUID  `json:"id"`
	EventID          uuid.UUID  `json:"event_id"`
	EventName        string     `json:"event_name"`
	Category         string     `json:"category"`
	BuyerName        string     `json:"buyer_name"`
	BuyerEmail       string     `json:"buyer_email"`
	Qty              int        `json:"qty"`
	Total            int64      `json:"total"`
	Status           string     `json:"status"`
	PaidAt           *time.Time `json:"paid_at"`
	RefundStatus     *string    `json:"refund_status"`
	CollectedTickets int        `json:"collected_tickets"`
}

// OrgOrders: GET /v1/org/{org}/orders?event=&q=&status=&page= — tanpa data peserta (NIK, kontak darurat).
func (s *Service) OrgOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	status := q.Get("status")
	if status == "" {
		status = "all"
	}
	if status != "all" && !slices.Contains([]string{"paid", "refunded", "needs_refund"}, status) {
		httpx.Validation(w, httpx.Fields{"status": "Status harus paid, refunded, atau needs_refund"})
		return
	}
	var eventID *uuid.UUID
	if ev := q.Get("event"); ev != "" {
		id, err := uuid.Parse(ev)
		if err != nil {
			httpx.Validation(w, httpx.Fields{"event": "Event tidak valid"})
			return
		}
		eventID = &id
	}
	search := strings.TrimSpace(q.Get("q"))
	p := page(r)
	where := `o.org_id = $1 AND o.status IN ('paid', 'refunded', 'needs_refund') AND ($2 = 'all' OR o.status = $2)
		AND ($3::uuid IS NULL OR o.event_id = $3)
		AND ($4 = '' OR u.email ILIKE '%' || $4 || '%' OR o.id::text ILIKE $4 || '%')`
	args := []any{tenant.OrgID(ctx), status, eventID, search}
	rows, err := s.db.Query(ctx, `
		SELECT o.id, o.event_id, e.name, c.code, u.name, u.email, o.qty, o.total, o.status, o.paid_at,
		       (SELECT f.status FROM refunds f WHERE f.order_id = o.id ORDER BY f.created_at DESC LIMIT 1),
		       (SELECT count(*) FROM tickets t WHERE t.order_id = o.id AND t.collected_at IS NOT NULL)::int
		FROM orders o JOIN events e ON e.id = o.event_id JOIN ticket_categories c ON c.id = o.category_id JOIN users u ON u.id = o.buyer_id
		WHERE `+where+` ORDER BY o.paid_at DESC NULLS LAST, o.id LIMIT $5 OFFSET $6`, append(args, pageSize, (p-1)*pageSize)...)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[OrgOrder])
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM orders o JOIN users u ON u.id = o.buyer_id WHERE `+where, args...).Scan(&total); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": p, "page_size": pageSize})
}

// ===== Ringkasan penjualan per event (dari ledger) =====

type EventSales struct {
	EventID        uuid.UUID `json:"event_id"`
	EventName      string    `json:"event_name"`
	RaceDate       string    `json:"race_date"`
	OrdersPaid     int       `json:"orders_paid"`     // order yang pernah lunas (punya jurnal order.paid)
	OrdersRefunded int       `json:"orders_refunded"` // di antaranya yang di-refund
	Gross          int64     `json:"gross"`           // total dibayar pembeli
	PlatformFee    int64     `json:"platform_fee"`
	PGFee          int64     `json:"pg_fee"` // estimasi biaya payment gateway
	Refunds        int64     `json:"refunds"`
	Net            int64     `json:"net"` // hak mitra bersih (setelah refund)
}

func (s *Service) eventSales(ctx context.Context, orgID uuid.UUID) ([]EventSales, error) {
	rows, err := s.db.Query(ctx, `
		WITH j AS (
		  SELECT o.event_id, j.ref_type, j.ref_id, e.account, e.debit, e.credit, o.total
		  FROM ledger_journals j JOIN ledger_entries e ON e.journal_id = j.id
		  JOIN orders o ON o.id::text = j.ref_id
		  WHERE j.ref_type IN ('order.paid', 'order.refund') AND o.org_id = $1)
		SELECT ev.id, ev.name, ev.race_date::text,
		  count(DISTINCT j.ref_id) FILTER (WHERE j.ref_type = 'order.paid')::int,
		  count(DISTINCT j.ref_id) FILTER (WHERE j.ref_type = 'order.refund')::int,
		  coalesce(sum(j.total) FILTER (WHERE j.ref_type = 'order.paid' AND j.account = $2), 0)::bigint,
		  coalesce(sum(j.credit) FILTER (WHERE j.ref_type = 'order.paid' AND j.account = $3), 0)::bigint,
		  coalesce(sum(j.total - j.debit) FILTER (WHERE j.ref_type = 'order.paid' AND j.account = $2), 0)::bigint,
		  coalesce(sum(j.credit) FILTER (WHERE j.ref_type = 'order.refund' AND j.account = $2), 0)::bigint,
		  coalesce(sum(j.credit - j.debit) FILTER (WHERE j.account IN ($4, $5)), 0)::bigint
		FROM events ev JOIN j ON j.event_id = ev.id
		WHERE ev.org_id = $1
		GROUP BY ev.id, ev.name, ev.race_date ORDER BY ev.race_date DESC, ev.name`,
		orgID, ledger.AccMidtransCash, ledger.AccPlatformRevenue, ledger.AccPartnerHeld, ledger.AccPartnerAvailable)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[EventSales])
}

// SalesSummary: GET /v1/org/{org}/reports/sales
func (s *Service) SalesSummary(w http.ResponseWriter, r *http.Request) {
	list, err := s.eventSales(r.Context(), tenant.OrgID(r.Context()))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list})
}

// ===== Mutasi saldo =====

type Mutation struct {
	ID          int64     `json:"id"`
	At          time.Time `json:"at"`
	Kind        string    `json:"kind"` // ref_type jurnal
	Description string    `json:"description"`
	Account     string    `json:"account"` // tertahan | tersedia | dalam_proses
	Amount      int64     `json:"amount"`  // + masuk, − keluar (sisi mitra)
	Ref         string    `json:"ref"`
}

var accountLabel = map[string]string{
	ledger.AccPartnerHeld: "tertahan", ledger.AccPartnerAvailable: "tersedia", ledger.AccPayoutInProgress: "dalam_proses",
}

var kindLabel = map[string]string{
	"order.paid":      "Penjualan tiket",
	"balance.release": "Dana dirilis (masa tahan selesai)",
	"payout.request":  "Pencairan diajukan",
	"payout.reject":   "Pencairan ditolak, saldo dikembalikan",
	"payout.fail":     "Pencairan gagal, saldo dikembalikan",
	"payout.complete": "Pencairan selesai ditransfer",
	"order.refund":    "Refund order",
}

func (s *Service) mutations(ctx context.Context, orgID uuid.UUID, limit, offset int) ([]Mutation, int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT e.id, j.created_at, j.ref_type, coalesce(ev.name, ''), e.account, e.credit - e.debit, j.ref_id
		FROM ledger_entries e JOIN ledger_journals j ON j.id = e.journal_id
		LEFT JOIN orders o ON j.ref_type IN ('order.paid', 'balance.release', 'order.refund') AND o.id::text = j.ref_id
		LEFT JOIN events ev ON ev.id = o.event_id
		WHERE e.org_id = $1 ORDER BY e.id DESC LIMIT $2 OFFSET $3`, orgID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var out []Mutation
	var m Mutation
	var event string
	_, err = pgx.ForEachRow(rows, []any{&m.ID, &m.At, &m.Kind, &event, &m.Account, &m.Amount, &m.Ref}, func() error {
		mm := m
		mm.Description = kindLabel[mm.Kind]
		if mm.Description == "" {
			mm.Description = mm.Kind
		}
		short := mm.Ref
		if len(short) > 8 {
			short = short[:8]
		}
		if event != "" {
			mm.Description += " · " + event + " · order #" + short
		} else {
			mm.Description += " · #" + short
		}
		if l, ok := accountLabel[mm.Account]; ok {
			mm.Account = l
		}
		out = append(out, mm)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	var total int
	err = s.db.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE org_id = $1`, orgID).Scan(&total)
	return out, total, err
}

// Ledger: GET /v1/org/{org}/finance/ledger?page=
func (s *Service) Ledger(w http.ResponseWriter, r *http.Request) {
	p := page(r)
	items, total, err := s.mutations(r.Context(), tenant.OrgID(r.Context()), pageSize, (p-1)*pageSize)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if items == nil {
		items = []Mutation{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": p, "page_size": pageSize})
}

// ===== CSV =====

// CSVSafe menetralkan injeksi rumus: sel yang diawali = + - @ tab atau CR diberi awalan '.
func CSVSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// writeCSV: UTF-8 dengan BOM dan pemisah titik koma, agar terbuka benar di Excel berlokal Indonesia.
func writeCSV(header []string, rows [][]string) []byte {
	var buf bytes.Buffer
	buf.WriteString("\uFEFF")
	cw := csv.NewWriter(&buf)
	cw.Comma = ';'
	cw.UseCRLF = true
	_ = cw.Write(header)
	for _, row := range rows {
		safe := make([]string, len(row))
		for i, v := range row {
			safe[i] = CSVSafe(v)
		}
		_ = cw.Write(safe)
	}
	cw.Flush()
	return buf.Bytes()
}

func (s *Service) sendCSV(w http.ResponseWriter, r *http.Request, name, kind string, rows int, data []byte) {
	m, _ := tenant.FromContext(r.Context())
	if err := audit(r.Context(), s.db, r, m.OrgID, "report.download", "report", kind, nil, map[string]any{"rows": rows}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s-%s.csv"`, name, m.OrgSlug, time.Now().Format("20060102")))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func i64(v int64) string { return strconv.FormatInt(v, 10) }

// SalesCSV: GET /v1/org/{org}/reports/sales.csv — tercatat di audit.
func (s *Service) SalesCSV(w http.ResponseWriter, r *http.Request) {
	list, err := s.eventSales(r.Context(), tenant.OrgID(r.Context()))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	rows := make([][]string, 0, len(list))
	for _, e := range list {
		rows = append(rows, []string{e.EventName, e.RaceDate, strconv.Itoa(e.OrdersPaid), strconv.Itoa(e.OrdersRefunded),
			i64(e.Gross), i64(e.PlatformFee), i64(e.PGFee), i64(e.Refunds), i64(e.Net)})
	}
	s.sendCSV(w, r, "penjualan", "sales", len(rows), writeCSV([]string{"Event", "Tanggal lomba", "Order lunas", "Order di-refund",
		"Bruto (Rp)", "Fee platform (Rp)", "Biaya PG estimasi (Rp)", "Refund (Rp)", "Bersih mitra (Rp)"}, rows))
}

// LedgerCSV: GET /v1/org/{org}/reports/ledger.csv — tercatat di audit.
func (s *Service) LedgerCSV(w http.ResponseWriter, r *http.Request) {
	list, _, err := s.mutations(r.Context(), tenant.OrgID(r.Context()), 100000, 0)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	wib := time.FixedZone("WIB", 7*3600)
	rows := make([][]string, 0, len(list))
	for _, m := range list {
		rows = append(rows, []string{m.At.In(wib).Format("2006-01-02 15:04:05"), m.Description, m.Account, i64(m.Amount), m.Ref})
	}
	s.sendCSV(w, r, "mutasi-saldo", "ledger", len(rows), writeCSV([]string{"Waktu (WIB)", "Keterangan", "Saldo", "Jumlah (Rp)", "Referensi"}, rows))
}

// ===== Super admin: ringkasan platform & integritas =====

// AdminOverview: GET /v1/admin/finance/overview
func (s *Service) AdminOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	totals, err := ledger.Totals(ctx, s.db)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	payouts, err := statusCounts(ctx, s.db, "payouts")
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	refunds, err := statusCounts(ctx, s.db, "refunds")
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	// Akun kewajiban/pendapatan bersaldo normal kredit → dibalik agar tampil positif.
	httpx.JSON(w, http.StatusOK, map[string]any{
		"held":             -totals[ledger.AccPartnerHeld],
		"available":        -totals[ledger.AccPartnerAvailable],
		"in_process":       -totals[ledger.AccPayoutInProgress],
		"platform_revenue": -totals[ledger.AccPlatformRevenue],
		"refund_pg_cost":   totals[ledger.AccRefundPGExpense],
		"cash":             totals[ledger.AccMidtransCash],
		"accounts":         totals,
		"payouts":          payouts, "refunds": refunds,
		"disburse_mode": map[bool]string{true: "mock", false: "midtrans"}[s.mock], "mock": s.mock,
		"payout_min": s.cfg.PayoutMin, "hold_days": s.cfg.HoldDays,
	})
}

// AdminLedgerCheck: GET /v1/admin/ledger/check
func (s *Service) AdminLedgerCheck(w http.ResponseWriter, r *http.Request) {
	rep, err := ledger.Check(r.Context(), s.db)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rep)
}
