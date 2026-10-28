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
	"github.com/gavinadlan/racego/backend/internal/notify"
	"github.com/gavinadlan/racego/backend/internal/partner"
	"github.com/gavinadlan/racego/backend/internal/payment"
	"github.com/gavinadlan/racego/backend/internal/tenant"
	"github.com/gavinadlan/racego/backend/internal/ticket"
	"github.com/gavinadlan/racego/backend/internal/upload"
)

type Deps struct {
	Cfg    config.Config
	DB     *pgxpool.Pool
	Redis  *redis.Client
	Inv    *checkout.Inventory
	Snap   payment.SnapClient
	Signer *ticket.Signer
	Mailer *notify.Mailer
}

func NewRouter(d Deps) http.Handler {
	events := event.NewHandler(d.DB, d.Inv)
	co := checkout.NewHandler(d.DB, d.Inv, d.Snap, d.Cfg.ReservationTTL, d.Cfg.FrontendURL)
	pay := payment.NewProcessor(d.DB, d.Cfg.SigningKey())
	if !d.Cfg.MidtransMock() {
		co.SetRefresher(payment.NewReconciler(pay, d.Cfg.MidtransServerKey, d.Cfg.MidtransIsProduction))
	}
	tickets := ticket.NewHandler(d.DB, d.Signer)
	sessions := auth.NewSessionStore(d.DB)
	authH := auth.NewHandler(d.DB, auth.NewOTPStore(d.Redis), sessions, d.Mailer, d.Cfg.IsDev())
	portal := partner.NewHandler(d.DB, d.Inv, d.Cfg.AutoVerifyOrgs)
	uploads := upload.NewHandler(upload.Disk{Dir: d.Cfg.UploadDir})

	r := chi.NewRouter()
	r.Use(middleware.RequestID, realIP(d.Cfg.TrustedProxyHops), middleware.Recoverer)
	r.Use(middleware.Timeout(15 * time.Second))
	r.Use(cors(d.Cfg.FrontendURL))
	r.Use(auth.Middleware(sessions.Lookup, d.Cfg.FrontendURL, d.Cfg.IsDev()))

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

	r.Get("/uploads/{name}", uploads.Serve)

	r.Route("/v1", func(r chi.Router) {
		// Publik
		r.Get("/events", events.List)
		r.Get("/events/filters", events.Filters)
		r.Get("/events/{eventID}", events.Get)
		r.Get("/organizers", events.Organizers)
		r.Get("/organizers/{slug}", events.Organizer)
		r.Get("/qr/public-key", tickets.PublicKey)
		r.Post("/webhooks/midtrans", pay.WebhookHandler)
		r.Post("/auth/otp/request", authH.RequestOTP)
		r.Post("/auth/otp/verify", authH.VerifyOTP)
		r.Post("/auth/logout", authH.Logout)
		r.Get("/auth/session", authH.Session)

		// Pelari (wajib login)
		r.Group(func(r chi.Router) {
			r.Use(auth.Require)
			r.Get("/me", authH.Me)
			r.Patch("/me", authH.UpdateMe)
			r.Get("/me/tickets", tickets.MyTickets)
			r.Post("/categories/{categoryID}/reservations", co.Reserve)
			r.Post("/orders", co.CreateOrder)
			r.Get("/orders/{orderID}", co.GetOrder)
			r.Post("/orgs", portal.CreateOrg)
			r.Get("/orgs/slug-check", portal.SlugCheck)
		})

		// Mitra (multi-tenant): setiap route di sini sudah terfilter ke satu org_id
		r.Route("/org/{org}", func(r chi.Router) {
			anyMember := tenant.RequireRole(d.DB, tenant.RoleOwner, tenant.RoleAdmin, tenant.RoleFinance, tenant.RoleRPCStaff)
			editors := tenant.RequireRole(d.DB, tenant.RoleOwner, tenant.RoleAdmin)
			ownerOnly := tenant.RequireRole(d.DB, tenant.RoleOwner)

			r.With(anyMember).Get("/events", events.OrgEvents)
			r.With(anyMember).Get("/profile", portal.GetProfile)
			r.With(anyMember).Get("/events/{eventID}", portal.GetEvent)
			r.Group(func(r chi.Router) {
				r.Use(tenant.RequireRole(d.DB, tenant.RoleOwner, tenant.RoleAdmin, tenant.RoleRPCStaff))
				r.Post("/events/{eventID}/scan", tickets.Scan)
				r.Get("/events/{eventID}/tickets", tickets.EventTickets)
			})
			r.Group(func(r chi.Router) {
				r.Use(editors)
				r.Patch("/profile", portal.UpdateProfile)
				r.Post("/uploads", uploads.Upload)
				r.Post("/events", portal.CreateEvent)
				r.Patch("/events/{eventID}", portal.UpdateEvent)
				r.Post("/events/{eventID}/publish", portal.Publish)
				r.Post("/events/{eventID}/unpublish", portal.Unpublish)
				r.Post("/events/{eventID}/close", portal.Close)
				r.Post("/events/{eventID}/categories", portal.CreateCategory)
				r.Patch("/events/{eventID}/categories/{categoryID}", portal.UpdateCategory)
				r.Delete("/events/{eventID}/categories/{categoryID}", portal.DeleteCategory)
			})
			r.Group(func(r chi.Router) {
				r.Use(ownerOnly)
				r.Delete("/events/{eventID}", portal.DeleteEvent)
				r.Get("/members", portal.ListMembers)
				r.Post("/members", portal.AddMember)
				r.Patch("/members/{userID}", portal.UpdateMember)
				r.Delete("/members/{userID}", portal.RemoveMember)
			})
		})

		// Simulasi pembayaran tanpa Midtrans: development, atau mode mock (MIDTRANS_SERVER_KEY kosong,
		// mis. demo). Di mode mock kunci tanda tangan webhook sudah publik, jadi rute ini tidak menambah celah.
		if d.Cfg.IsDev() || d.Cfg.MidtransMock() {
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
			h.Set("Access-Control-Allow-Credentials", "true")
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
