package checkout

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/ledger"
	"github.com/gavinadlan/racego/backend/internal/payment"
)

type Handler struct {
	db          *pgxpool.Pool
	inv         *Inventory
	snap        payment.SnapClient
	ttl         time.Duration
	frontendURL string
	refresher   StatusRefresher // nil di mode mock
	queue       QueueGate

	// Cache info kategori beberapa detik: saat war tiket ribuan request membaca baris yang sama,
	// jadi cukup satu query per kategori per periode, bukan satu query per request.
	cacheMu sync.Mutex
	cache   map[uuid.UUID]cachedCategory
}

type cachedCategory struct {
	info    categoryInfo
	expires time.Time
}

const categoryCacheTTL = 3 * time.Second

func NewHandler(db *pgxpool.Pool, inv *Inventory, snap payment.SnapClient, ttl time.Duration, frontendURL string) *Handler {
	return &Handler{db: db, inv: inv, snap: snap, ttl: ttl, frontendURL: frontendURL, cache: map[uuid.UUID]cachedCategory{}}
}

func (h *Handler) cachedCategory(ctx context.Context, id uuid.UUID) (categoryInfo, error) {
	now := time.Now()
	h.cacheMu.Lock()
	c, ok := h.cache[id]
	h.cacheMu.Unlock()
	if ok && now.Before(c.expires) {
		return c.info, nil
	}
	info, err := h.loadCategory(ctx, id)
	if err != nil {
		return info, err
	}
	h.cacheMu.Lock()
	h.cache[id] = cachedCategory{info: info, expires: now.Add(categoryCacheTTL)}
	h.cacheMu.Unlock()
	return info, nil
}

type categoryInfo struct {
	ID          uuid.UUID
	EventID     uuid.UUID
	OrgID       uuid.UUID
	Code        string
	Name        string
	Price       int64
	MaxPerUser  int
	EventName   string
	EventStatus string
	SaleOpenAt  time.Time
	SaleCloseAt time.Time
	FeeBps      int
	WaitingRoom bool
}

func (h *Handler) loadCategory(ctx context.Context, id uuid.UUID) (categoryInfo, error) {
	var c categoryInfo
	err := h.db.QueryRow(ctx, `
		SELECT c.id, c.event_id, c.org_id, c.code, c.name, c.price, c.max_per_user,
		       e.name, e.status, e.sale_open_at, e.sale_close_at, o.fee_bps, e.waiting_room
		FROM ticket_categories c
		JOIN events e ON e.id = c.event_id
		JOIN organizations o ON o.id = c.org_id
		WHERE c.id = $1`, id,
	).Scan(&c.ID, &c.EventID, &c.OrgID, &c.Code, &c.Name, &c.Price, &c.MaxPerUser,
		&c.EventName, &c.EventStatus, &c.SaleOpenAt, &c.SaleCloseAt, &c.FeeBps, &c.WaitingRoom)
	return c, err
}

// QueueGate memeriksa pass antrian virtual (paket queue).
type QueueGate interface {
	// Check: nil bila pass sah & aktif; ErrQueueRequired / ErrQueuePassExpired bila tidak.
	Check(ctx context.Context, eventID, userID, token string, now time.Time) error
	ReleasePass(ctx context.Context, eventID, userID string) error
}

// SetQueue mengaktifkan pemeriksaan pass untuk event ber-antrian.
func (h *Handler) SetQueue(g QueueGate) { h.queue = g }

// Reserve: POST /v1/categories/{categoryID}/reservations {"qty": 1}
// Jalur paling panas saat war tiket: satu query baca + satu skrip Lua di Redis.
// Event ber-antrian: wajib header X-Queue-Pass; setelah reservasi berhasil pass dilepas.
func (h *Handler) Reserve(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserID(r.Context())
	categoryID, err := uuid.Parse(chi.URLParam(r, "categoryID"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "category_not_found", "Kategori tidak ditemukan")
		return
	}
	var body struct {
		Qty int `json:"qty"`
	}
	if !httpx.Decode(w, r, &body) {
		return
	}
	if body.Qty <= 0 {
		body.Qty = 1
	}

	cat, err := h.cachedCategory(r.Context(), categoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "category_not_found", "Kategori tidak ditemukan")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := time.Now()
	if cat.EventStatus != "published" || now.Before(cat.SaleOpenAt) || !now.Before(cat.SaleCloseAt) {
		// Status event di cache paling lama basi categoryCacheTTL (3 detik) setelah mitra menutup penjualan.
		httpx.Error(w, http.StatusConflict, "sale_not_open", "Penjualan kategori ini sedang tidak dibuka")
		return
	}

	if cat.WaitingRoom && !h.queuePass(w, r, cat, userID.String(), now) {
		return
	}

	req := ReserveRequest{CategoryID: categoryID.String(), UserID: userID.String(), Qty: body.Qty, MaxPerUser: cat.MaxPerUser, TTL: h.ttl, Now: now}
	res, err := h.inv.Reserve(r.Context(), req)
	if errors.Is(err, ErrNotOpen) {
		// Stok belum ada di Redis (worker belum sinkron / Redis baru restart): muat lalu coba sekali lagi.
		if syncErr := SyncCategory(r.Context(), h.db, h.inv, categoryID); syncErr != nil {
			httpx.Internal(w, r, syncErr)
			return
		}
		res, err = h.inv.Reserve(r.Context(), req)
	}
	switch {
	case errors.Is(err, ErrSoldOut):
		httpx.Error(w, http.StatusConflict, "sold_out", "Maaf, kuota kategori ini sudah habis")
	case errors.Is(err, ErrLimitExceeded):
		httpx.Error(w, http.StatusConflict, "limit_exceeded", fmt.Sprintf("Maksimal %d slot per akun untuk kategori ini", cat.MaxPerUser))
	case err != nil:
		httpx.Internal(w, r, err)
	default:
		if cat.WaitingRoom {
			// Slot sudah aman: kembalikan kapasitas antrian. Untuk kategori lain pembeli antri lagi.
			if err := h.queue.ReleasePass(r.Context(), cat.EventID.String(), userID.String()); err != nil {
				slog.WarnContext(r.Context(), "lepas pass antrian", "event_id", cat.EventID, "err", err)
			}
		}
		httpx.JSON(w, http.StatusCreated, res)
	}
}

// queuePass memeriksa header X-Queue-Pass; false bila sudah membalas 403/500.
func (h *Handler) queuePass(w http.ResponseWriter, r *http.Request, cat categoryInfo, userID string, now time.Time) bool {
	err := ErrQueueRequired
	if h.queue != nil {
		err = h.queue.Check(r.Context(), cat.EventID.String(), userID, r.Header.Get("X-Queue-Pass"), now)
	}
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrQueuePassExpired):
		httpx.Error(w, http.StatusForbidden, "queue_pass_expired", "Waktu giliranmu sudah habis. Masuk antrian lagi untuk mendaftar.")
	case errors.Is(err, ErrQueueRequired):
		httpx.Error(w, http.StatusForbidden, "queue_required", "Event ini memakai antrian. Masuk antrian dan tunggu giliranmu sebelum mendaftar.")
	default:
		httpx.Internal(w, r, err)
	}
	return false
}

type ParticipantInput struct {
	FullName       string `json:"full_name"`
	Email          string `json:"email"`
	IDNumber       string `json:"id_number"`
	BirthDate      string `json:"birth_date,omitempty"` // YYYY-MM-DD
	Gender         string `json:"gender,omitempty"`     // M | F
	JerseySize     string `json:"jersey_size"`
	EmergencyName  string `json:"emergency_name,omitempty"`
	EmergencyPhone string `json:"emergency_phone,omitempty"`
}

func (p ParticipantInput) validate(i int) error {
	prefix := fmt.Sprintf("peserta %d: ", i+1)
	if n := utf8.RuneCountInString(strings.TrimSpace(p.FullName)); n < 2 || n > 100 {
		return errors.New(prefix + "nama wajib diisi")
	}
	if _, err := mail.ParseAddress(p.Email); err != nil {
		return errors.New(prefix + "email tidak valid")
	}
	if l := len(strings.TrimSpace(p.IDNumber)); l < 6 || l > 32 {
		return errors.New(prefix + "nomor identitas tidak valid")
	}
	if p.JerseySize == "" {
		return errors.New(prefix + "ukuran jersey wajib dipilih")
	}
	if p.Gender != "" && p.Gender != "M" && p.Gender != "F" {
		return errors.New(prefix + "gender harus M atau F")
	}
	if p.BirthDate != "" {
		if _, err := time.Parse("2006-01-02", p.BirthDate); err != nil {
			return errors.New(prefix + "tanggal lahir harus berformat YYYY-MM-DD")
		}
	}
	return nil
}

type CreateOrderRequest struct {
	CategoryID    string             `json:"category_id"`
	ReservationID string             `json:"reservation_id"`
	Participants  []ParticipantInput `json:"participants"`
}

type OrderResponse struct {
	OrderID     uuid.UUID `json:"order_id"`
	Status      string    `json:"status"`
	Total       int64     `json:"total"`
	ExpiresAt   time.Time `json:"expires_at"`
	SnapToken   string    `json:"snap_token,omitempty"`
	RedirectURL string    `json:"redirect_url,omitempty"`
	Mock        bool      `json:"mock"`
	Qty         int       `json:"qty"`
	// Tiket terbit beberapa detik setelah lunas (worker); frontend polling sampai = qty.
	TicketsIssued int `json:"tickets_issued"`
}

// CreateOrder: POST /v1/orders (header Idempotency-Key wajib).
// Mengubah reservasi menjadi order pending + peserta, lalu meminta token Snap.
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, _ := auth.UserID(ctx)
	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idemKey == "" || len(idemKey) > 100 {
		httpx.Error(w, http.StatusBadRequest, "idempotency_key_required", "Header Idempotency-Key wajib diisi")
		return
	}
	// Klik ganda / retry jaringan → kembalikan order yang sama, jangan buat baru.
	if existing, ok, err := h.findOrder(ctx, `buyer_id = $1 AND idempotency_key = $2`, userID, idemKey); err != nil {
		httpx.Internal(w, r, err)
		return
	} else if ok {
		httpx.JSON(w, http.StatusOK, existing)
		return
	}

	var req CreateOrderRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	categoryID, err := uuid.Parse(req.CategoryID)
	if err != nil || req.ReservationID == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "category_id dan reservation_id wajib diisi")
		return
	}
	for i, p := range req.Participants {
		if err := p.validate(i); err != nil {
			httpx.Error(w, http.StatusUnprocessableEntity, "invalid_participant", err.Error())
			return
		}
	}

	hold, found, err := h.inv.GetHold(ctx, categoryID.String(), req.ReservationID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	expiresAt, active, err := h.inv.HoldExpiry(ctx, categoryID.String(), req.ReservationID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if !found || !active || hold.UserID != userID.String() || time.Now().After(expiresAt) {
		httpx.Error(w, http.StatusGone, "reservation_expired", "Reservasi sudah kedaluwarsa, silakan pilih ulang")
		return
	}
	if len(req.Participants) != hold.Qty {
		httpx.Error(w, http.StatusUnprocessableEntity, "participant_count", fmt.Sprintf("Isi data untuk %d peserta", hold.Qty))
		return
	}

	cat, err := h.loadCategory(ctx, categoryID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	// Reservasi dibuat saat event terbit; bila sejak itu event ditutup atau ditarik ke draft, tolak.
	if cat.EventStatus != "published" {
		httpx.Error(w, http.StatusConflict, "sale_not_open", "Penjualan event ini sudah ditutup penyelenggara")
		return
	}
	subtotal := cat.Price * int64(hold.Qty)
	fee := ledger.PlatformFee(subtotal, cat.FeeBps) // ditanggung mitra, tidak menambah harga
	orderID := uuid.New()

	if err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO orders (id, org_id, event_id, category_id, buyer_id, reservation_id, idempotency_key,
			                    qty, subtotal, platform_fee, total, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $9, $11)`,
			orderID, cat.OrgID, cat.EventID, cat.ID, userID, req.ReservationID, idemKey,
			hold.Qty, subtotal, fee, expiresAt); err != nil {
			return err
		}
		for _, p := range req.Participants {
			var birth *string
			if p.BirthDate != "" {
				birth = &p.BirthDate
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO participants (order_id, org_id, event_id, category_id, full_name, email, id_number,
				                          birth_date, gender, jersey_size, emergency_name, emergency_phone)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), $10, $11, $12)`,
				orderID, cat.OrgID, cat.EventID, cat.ID, strings.TrimSpace(p.FullName), strings.TrimSpace(p.Email),
				strings.TrimSpace(p.IDNumber), birth, p.Gender, p.JerseySize, p.EmergencyName, p.EmergencyPhone); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique violation
			httpx.Error(w, http.StatusConflict, "reservation_used", "Reservasi ini sudah dipakai untuk order lain")
			return
		}
		httpx.Internal(w, r, err)
		return
	}

	resp := OrderResponse{OrderID: orderID, Status: "pending", Total: subtotal, ExpiresAt: expiresAt, Qty: hold.Qty}
	snapRes, err := h.snap.CreateTransaction(ctx, payment.SnapRequest{
		OrderID:       orderID.String(),
		GrossAmount:   subtotal,
		Items:         []payment.SnapItem{{ID: cat.ID.String(), Price: cat.Price, Quantity: hold.Qty, Name: truncate(cat.EventName+" "+cat.Code, 50)}},
		CustomerName:  req.Participants[0].FullName,
		CustomerEmail: req.Participants[0].Email,
		ExpiryMinutes: max(1, int(time.Until(expiresAt).Minutes())),
		FinishURL:     h.frontendURL + "/orders/" + orderID.String(),
	})
	if err != nil {
		// Order tetap pending; pembeli bisa mencoba bayar lagi dari halaman order sebelum timer habis.
		slog.ErrorContext(ctx, "snap gagal", "order_id", orderID, "err", err)
		httpx.Error(w, http.StatusBadGateway, "payment_unavailable", "Layanan pembayaran sedang sibuk, coba lagi sebentar")
		return
	}
	if _, err := h.db.Exec(ctx, `UPDATE orders SET snap_token = $2, payment_url = $3 WHERE id = $1`,
		orderID, snapRes.Token, snapRes.RedirectURL); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	resp.SnapToken, resp.RedirectURL, resp.Mock = snapRes.Token, snapRes.RedirectURL, snapRes.Mock
	httpx.JSON(w, http.StatusCreated, resp)
}

// GetOrder: GET /v1/orders/{orderID} — hanya pemilik order. Dipakai frontend untuk polling status.
func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) {
	userID, _ := auth.UserID(r.Context())
	orderID, err := uuid.Parse(chi.URLParam(r, "orderID"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "order_not_found", "Order tidak ditemukan")
		return
	}
	o, ok, err := h.findOrder(r.Context(), `id = $1 AND buyer_id = $2`, orderID, userID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "order_not_found", "Order tidak ditemukan")
		return
	}
	// Belum lunas menurut kita? Tanyakan langsung ke Midtrans (berguna saat webhook tidak sampai,
	// mis. development di localhost). Gagal bertanya bukan error bagi pembeli: tampilkan status lama.
	if h.refresher != nil && !o.Mock && (o.Status == "pending" || o.Status == "expired") {
		changed, err := h.refresher.Refresh(r.Context(), orderID.String())
		if err != nil {
			slog.WarnContext(r.Context(), "cek status midtrans gagal", "order_id", orderID, "err", err)
		} else if changed {
			if o2, ok2, err2 := h.findOrder(r.Context(), `id = $1`, orderID); err2 == nil && ok2 {
				o = o2
			}
		}
	}
	httpx.JSON(w, http.StatusOK, o)
}

// StatusRefresher menanyakan status pembayaran langsung ke payment gateway.
type StatusRefresher interface {
	Refresh(ctx context.Context, orderID string) (changed bool, err error)
}

// SetRefresher mengaktifkan pengecekan status langsung di GetOrder (tidak dipakai di mode mock).
func (h *Handler) SetRefresher(r StatusRefresher) { h.refresher = r }

func (h *Handler) findOrder(ctx context.Context, where string, args ...any) (OrderResponse, bool, error) {
	var o OrderResponse
	var token, url *string
	err := h.db.QueryRow(ctx, `
		SELECT id, status, total, expires_at, snap_token, payment_url, qty,
		       (SELECT count(*) FROM tickets t WHERE t.order_id = orders.id)
		FROM orders WHERE `+where, args...).
		Scan(&o.OrderID, &o.Status, &o.Total, &o.ExpiresAt, &token, &url, &o.Qty, &o.TicketsIssued)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, false, nil
	}
	if err != nil {
		return o, false, err
	}
	if token != nil {
		o.SnapToken = *token
		o.Mock = strings.HasPrefix(*token, "mock-")
	}
	if url != nil {
		o.RedirectURL = *url
	}
	return o, true, nil
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
