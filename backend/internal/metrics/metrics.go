// Package metrics berisi metrik Prometheus RaceGo (awalan racego_) dan listener /metrics yang
// terpisah dari server publik. Lihat docs/11-performa.md.
//
// Aturan label: hanya nilai dari himpunan kecil dan tetap (pola rute chi, metode, kelas status,
// topik outbox, nama perintah Redis). Tidak pernah user ID, email, slug, ID event/order, token, IP,
// atau teks bebas. TestMetricsOutputSafe (internal/server) menjaga aturan ini.
package metrics

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
)

// Registry sendiri (bukan default global) supaya isi /metrics hanya yang didaftarkan di sini.
var Registry = prometheus.NewRegistry()

var f = promauto.With(Registry)

// Bucket latensi: 1 ms sampai 10 detik (di atas itu request sudah kena batas waktu 15 detik).
var latencyBuckets = []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

var (
	httpRequests = f.NewCounterVec(prometheus.CounterOpts{
		Name: "racego_http_requests_total", Help: "Jumlah request HTTP per metode, pola rute, dan kelas status.",
	}, []string{"method", "route", "status"})
	httpDuration = f.NewHistogramVec(prometheus.HistogramOpts{
		Name: "racego_http_request_duration_seconds", Help: "Latensi request HTTP per metode dan pola rute.",
		Buckets: latencyBuckets,
	}, []string{"method", "route"})

	// Reservations: ok | sold_out | limit_exceeded | queue_rejected | sale_not_open | error.
	Reservations = f.NewCounterVec(prometheus.CounterOpts{
		Name: "racego_reservations_total", Help: "Hasil percobaan reservasi stok.",
	}, []string{"result"})
	// Orders: created | paid | released | expired.
	Orders = f.NewCounterVec(prometheus.CounterOpts{
		Name: "racego_orders_total", Help: "Peristiwa order: dibuat, lunas, dilepas gateway, kedaluwarsa.",
	}, []string{"event"})

	StockClamped = f.NewCounter(prometheus.CounterOpts{
		Name: "racego_stock_clamped_slots_total",
		Help: "Slot stok Redis yang diturunkan paksa karena melebihi kuota − order aktif (tanda Redis kehilangan tulisan).",
	})

	QueueAdmitted = f.NewCounter(prometheus.CounterOpts{
		Name: "racego_queue_admitted_total", Help: "Peserta antrean yang dipanggil (semua event).",
	})
	QueueSize = f.NewGauge(prometheus.GaugeOpts{
		Name: "racego_queue_size", Help: "Peserta di lobi/antrean saat ini (jumlah semua event ber-antrean).",
	})
	QueueActive = f.NewGauge(prometheus.GaugeOpts{
		Name: "racego_queue_active_passes", Help: "Pass antrean yang sedang aktif (jumlah semua event).",
	})
	QueueOldestSeen = f.NewGauge(prometheus.GaugeOpts{
		Name: "racego_queue_oldest_heartbeat_age_seconds",
		Help: "Umur tanda hidup (polling terakhir) tertua di antrean; besar = ada klien yang berhenti polling.",
	})

	outboxProcessed = f.NewCounterVec(prometheus.CounterOpts{
		Name: "racego_outbox_processed_total", Help: "Pesan outbox yang diproses worker per topik; result ok | retry.",
	}, []string{"topic", "result"})
	workerStep = f.NewHistogramVec(prometheus.HistogramOpts{
		Name: "racego_worker_step_duration_seconds", Help: "Durasi satu putaran langkah worker.",
		Buckets: latencyBuckets,
	}, []string{"step"})

	Emails = f.NewCounterVec(prometheus.CounterOpts{
		Name: "racego_emails_total", Help: "Email yang dikirim; result sent | failed.",
	}, []string{"result"})

	redisDuration = f.NewHistogramVec(prometheus.HistogramOpts{
		Name: "racego_redis_command_duration_seconds", Help: "Latensi perintah Redis per nama perintah (evalsha = skrip Lua stok/antrean).",
		Buckets: latencyBuckets,
	}, []string{"cmd"})
	redisErrors = f.NewCounterVec(prometheus.CounterOpts{
		Name: "racego_redis_errors_total", Help: "Perintah Redis yang gagal (bukan redis.Nil).",
	}, []string{"cmd"})
)

func init() {
	Registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
}

// HTTP mencatat jumlah dan latensi tiap request. Label rute = pola chi ("/v1/events/{eventID}"),
// bukan path mentah; path yang tidak cocok rute mana pun = "unmatched". Pasang paling luar.
func HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		method := method(r.Method)
		httpRequests.WithLabelValues(method, route, string(rune('0'+status/100))+"xx").Inc()
		httpDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
	})
}

// method: klien bisa mengirim nama metode apa saja; di luar daftar ini digabung jadi OTHER.
func method(m string) string {
	switch m {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodHead:
		return m
	}
	return "OTHER"
}

var outboxTopics = map[string]bool{
	"order.paid": true, "order.released": true, "ticket.email": true,
	"kyc.email": true, "payout.email": true, "refund.email": true,
}

// Outbox mencatat satu pesan outbox yang diproses. Topik tak dikenal digabung jadi "other".
func Outbox(topic string, err error) {
	if !outboxTopics[topic] {
		topic = "other"
	}
	result := "ok"
	if err != nil {
		result = "retry"
	}
	outboxProcessed.WithLabelValues(topic, result).Inc()
}

// Step menjalankan satu langkah worker dan mencatat durasinya. name harus konstanta di kode.
func Step(name string, fn func()) {
	start := time.Now()
	fn()
	workerStep.WithLabelValues(name).Observe(time.Since(start).Seconds())
}

// RedisHook mengukur latensi dan error tiap perintah Redis. Nama perintah berasal dari kode
// (get, evalsha, zadd, ...), jadi himpunannya tetap.
type RedisHook struct{}

func (RedisHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (RedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		observeRedis(cmd.Name(), start, err)
		return err
	}
}

func (RedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)
		observeRedis("pipeline", start, err)
		return err
	}
}

func observeRedis(cmd string, start time.Time, err error) {
	redisDuration.WithLabelValues(cmd).Observe(time.Since(start).Seconds())
	// redis.Nil = kunci tidak ada; NOSCRIPT = skrip belum di-cache (go-redis mengulang dengan EVAL).
	if err != nil && !errors.Is(err, redis.Nil) && !errors.Is(err, context.Canceled) && !strings.HasPrefix(err.Error(), "NOSCRIPT") {
		redisErrors.WithLabelValues(cmd).Inc()
	}
}

// RegisterPool mengekspor statistik pool koneksi Postgres (pgxpool.Stat). Panggil sekali per proses.
func RegisterPool(db *pgxpool.Pool) {
	gauge := func(name, help string, v func(*pgxpool.Stat) float64) {
		Registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, func() float64 { return v(db.Stat()) }))
	}
	counter := func(name, help string, v func(*pgxpool.Stat) float64) {
		Registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: name, Help: help}, func() float64 { return v(db.Stat()) }))
	}
	gauge("racego_db_pool_acquired_conns", "Koneksi Postgres yang sedang dipakai.", func(s *pgxpool.Stat) float64 { return float64(s.AcquiredConns()) })
	gauge("racego_db_pool_idle_conns", "Koneksi Postgres menganggur.", func(s *pgxpool.Stat) float64 { return float64(s.IdleConns()) })
	gauge("racego_db_pool_total_conns", "Koneksi Postgres terbuka.", func(s *pgxpool.Stat) float64 { return float64(s.TotalConns()) })
	gauge("racego_db_pool_max_conns", "Batas koneksi pool.", func(s *pgxpool.Stat) float64 { return float64(s.MaxConns()) })
	counter("racego_db_pool_wait_total", "Berapa kali permintaan koneksi harus menunggu karena pool penuh.", func(s *pgxpool.Stat) float64 { return float64(s.EmptyAcquireCount()) })
	counter("racego_db_pool_acquire_total", "Jumlah pengambilan koneksi dari pool.", func(s *pgxpool.Stat) float64 { return float64(s.AcquireCount()) })
	counter("racego_db_pool_acquire_duration_seconds_total", "Total waktu menunggu koneksi dari pool.", func(s *pgxpool.Stat) float64 { return s.AcquireDuration().Seconds() })
}

var (
	outboxBacklog = prometheus.NewDesc("racego_outbox_backlog", "Pesan outbox yang belum diproses.", nil, nil)
	outboxDead    = prometheus.NewDesc("racego_outbox_dead", "Pesan outbox yang berhenti dicoba (percobaan habis).", nil, nil)
	outboxOldest  = prometheus.NewDesc("racego_outbox_oldest_age_seconds", "Umur pesan outbox tertua yang belum diproses.", nil, nil)
	payoutCount   = prometheus.NewDesc("racego_payouts", "Jumlah pencairan per status.", []string{"status"}, nil)
	refundCount   = prometheus.NewDesc("racego_refunds", "Jumlah refund per status.", []string{"status"}, nil)
)

// dbCollector membaca angka bisnis dari Postgres tiap scrape (worker saja). Label status berasal
// dari kolom ber-CHECK (himpunan tetap). Gagal membaca = deret itu tidak diekspor pada scrape ini.
type dbCollector struct {
	db          *pgxpool.Pool
	maxAttempts int
}

// RegisterDB mengekspor backlog outbox dan jumlah pencairan/refund per status. Panggil sekali per proses.
func RegisterDB(db *pgxpool.Pool, outboxMaxAttempts int) {
	Registry.MustRegister(dbCollector{db, outboxMaxAttempts})
}

func (dbCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{outboxBacklog, outboxDead, outboxOldest, payoutCount, refundCount} {
		ch <- d
	}
}

func (c dbCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var backlog, dead int64
	var oldest float64
	if err := c.db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE attempts < $1), count(*) FILTER (WHERE attempts >= $1),
		       coalesce(extract(epoch FROM now() - min(created_at) FILTER (WHERE attempts < $1)), 0)::float8
		FROM outbox WHERE processed_at IS NULL`, c.maxAttempts).Scan(&backlog, &dead, &oldest); err == nil {
		ch <- prometheus.MustNewConstMetric(outboxBacklog, prometheus.GaugeValue, float64(backlog))
		ch <- prometheus.MustNewConstMetric(outboxDead, prometheus.GaugeValue, float64(dead))
		ch <- prometheus.MustNewConstMetric(outboxOldest, prometheus.GaugeValue, oldest)
	}
	for table, desc := range map[string]*prometheus.Desc{"payouts": payoutCount, "refunds": refundCount} {
		rows, err := c.db.Query(ctx, `SELECT status, count(*) FROM `+table+` GROUP BY status`)
		if err != nil {
			continue
		}
		var status string
		var n int64
		for rows.Next() {
			if rows.Scan(&status, &n) == nil {
				ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(n), status)
			}
		}
		rows.Close()
	}
}
