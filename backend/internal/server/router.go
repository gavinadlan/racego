// Package server merangkai semua modul menjadi satu router HTTP.
package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/checkout"
	"github.com/gavinadlan/racego/backend/internal/config"
	"github.com/gavinadlan/racego/backend/internal/event"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/payment"
	"github.com/gavinadlan/racego/backend/internal/tenant"
	"github.com/gavinadlan/racego/backend/internal/ticket"
)

type Deps struct {
	Cfg    config.Config
	DB     *pgxpool.Pool
	Redis  *redis.Client
	Inv    *checkout.Inventory
	Snap   payment.SnapClient
	Signer *ticket.Signer
}

func NewRouter(d Deps) http.Handler {
	events := event.NewHandler(d.DB, d.Inv)
	co := checkout.NewHandler(d.DB, d.Inv, d.Snap, d.Cfg.ReservationTTL, d.Cfg.FrontendURL)
	pay := payment.NewProcessor(d.DB, d.Cfg.SigningKey())
	if !d.Cfg.MidtransMock() {
		co.SetRefresher(payment.NewReconciler(pay, d.Cfg.MidtransServerKey, d.Cfg.MidtransIsProduction))
	}
	tickets := ticket.NewHandler(d.DB, d.Signer)

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(middleware.Timeout(15 * time.Second))
	r.Use(cors(d.Cfg.FrontendURL))
	r.Use(auth.Middleware(d.Cfg.IsDev()))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := d.DB.Ping(r.Context()); err != nil {
			httpx.Error(w, http.StatusServiceUnavailable, "db_down", err.Error())
			return
		}
		if err := d.Redis.Ping(r.Context()).Err(); err != nil {
			httpx.Error(w, http.StatusServiceUnavailable, "redis_down", err.Error())
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok", "midtrans_mock": d.Cfg.MidtransMock()})
	})

	r.Route("/v1", func(r chi.Router) {
		// Publik
		r.Get("/events", events.List)
		r.Get("/events/filters", events.Filters)
		r.Get("/events/{eventID}", events.Get)
		r.Get("/organizers", events.Organizers)
		r.Get("/organizers/{slug}", events.Organizer)
		r.Get("/qr/public-key", tickets.PublicKey)
		r.Post("/webhooks/midtrans", pay.WebhookHandler)

		// Pelari (wajib login)
		r.Group(func(r chi.Router) {
			r.Use(auth.Require)
			r.Get("/me", events.Me)
			r.Get("/me/tickets", tickets.MyTickets)
			r.Post("/categories/{categoryID}/reservations", co.Reserve)
			r.Post("/orders", co.CreateOrder)
			r.Get("/orders/{orderID}", co.GetOrder)
		})

		// Mitra (multi-tenant): setiap route di sini sudah terfilter ke satu org_id
		r.Route("/org/{org}", func(r chi.Router) {
			r.With(tenant.RequireRole(d.DB, tenant.RoleOwner, tenant.RoleAdmin, tenant.RoleFinance, tenant.RoleRPCStaff)).
				Get("/events", events.OrgEvents)
			r.Group(func(r chi.Router) {
				r.Use(tenant.RequireRole(d.DB, tenant.RoleOwner, tenant.RoleAdmin, tenant.RoleRPCStaff))
				r.Post("/events/{eventID}/scan", tickets.Scan)
				r.Get("/events/{eventID}/tickets", tickets.EventTickets)
			})
		})

		// Khusus development: simulasi pembayaran tanpa Midtrans
		if d.Cfg.IsDev() {
			r.With(auth.Require).Post("/dev/orders/{orderID}/simulate", pay.SimulateHandler)
		}
	})
	return r
}

func cors(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key, X-User-ID")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
