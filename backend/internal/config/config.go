// Package config membaca konfigurasi aplikasi dari environment variable.
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Env         string
	HTTPAddr    string
	FrontendURL string
	// TrustedProxyHops: jumlah proxy tepercaya di depan API (0 = langsung terekspos, header IP diabaikan).
	TrustedProxyHops int

	DatabaseURL   string
	RedisAddr     string
	RedisPassword string

	MidtransServerKey    string
	MidtransIsProduction bool

	QRPrivateKey   string // base64 seed Ed25519 (32 byte)
	ReservationTTL time.Duration

	Queue QueueConfig

	AutoVerifyOrgs bool   // org baru langsung verified (default true hanya di development)
	UploadDir      string // folder gambar unggahan (poster, logo)

	SMTPAddr     string
	SMTPUsername string // kosong untuk Mailpit; isi untuk Resend/penyedia SMTP lain
	SMTPPassword string
	MailFrom     string
}

// QueueConfig mengatur antrian virtual (waiting room). Lihat docs/03-war-tiket.md.
type QueueConfig struct {
	Secret       string        // kunci HMAC pass antrian; wajib di luar development
	LobbyMinutes int           // lobi dibuka sekian menit sebelum penjualan
	AdmitPerSec  int           // laju pemanggilan per event
	MaxActive    int           // pass aktif maksimum per event
	PassTTL      time.Duration // masa berlaku pass
	HeartbeatTTL time.Duration // tanpa polling selama ini = keluar antrian
}

// MockServerKey dipakai untuk menandatangani notifikasi palsu di mode mock.
const MockServerKey = "mock-server-key"

func Load() Config {
	env := get("APP_ENV", "development")
	return Config{
		Env:                  env,
		HTTPAddr:             get("HTTP_ADDR", ":8080"),
		FrontendURL:          get("FRONTEND_URL", "http://localhost:3000"),
		TrustedProxyHops:     getInt("TRUSTED_PROXY_HOPS", 0),
		DatabaseURL:          get("DATABASE_URL", "postgres://racego:racego@localhost:55432/racego?sslmode=disable"),
		RedisAddr:            get("REDIS_ADDR", "localhost:6380"),
		RedisPassword:        os.Getenv("REDIS_PASSWORD"),
		MidtransServerKey:    os.Getenv("MIDTRANS_SERVER_KEY"),
		MidtransIsProduction: getBool("MIDTRANS_IS_PRODUCTION", false),
		QRPrivateKey:         os.Getenv("QR_PRIVATE_KEY"),
		ReservationTTL:       getDuration("RESERVATION_TTL", 15*time.Minute),
		Queue: QueueConfig{
			Secret:       os.Getenv("QUEUE_SECRET"),
			LobbyMinutes: getInt("QUEUE_LOBBY_MINUTES", 15),
			AdmitPerSec:  getInt("QUEUE_ADMIT_PER_SEC", 20),
			MaxActive:    getInt("QUEUE_MAX_ACTIVE", 300),
			PassTTL:      getDuration("QUEUE_PASS_TTL", 10*time.Minute),
			HeartbeatTTL: getDuration("QUEUE_HEARTBEAT_TTL", 45*time.Second),
		},
		AutoVerifyOrgs: getBool("AUTO_VERIFY_ORGS", env == "development"),
		UploadDir:      get("UPLOAD_DIR", "./data/uploads"),
		SMTPAddr:       get("SMTP_ADDR", "localhost:1025"),
		SMTPUsername:   os.Getenv("SMTP_USERNAME"),
		SMTPPassword:   os.Getenv("SMTP_PASSWORD"),
		MailFrom:       get("MAIL_FROM", "RaceGo <no-reply@racego.local>"),
	}
}

func (c Config) IsDev() bool { return c.Env == "development" }

// MidtransMock true bila server key kosong: pembayaran disimulasikan tanpa Midtrans.
func (c Config) MidtransMock() bool { return c.MidtransServerKey == "" }

// SigningKey adalah kunci yang dipakai memverifikasi signature notifikasi pembayaran.
func (c Config) SigningKey() string {
	if c.MidtransMock() {
		return MockServerKey
	}
	return c.MidtransServerKey
}

func get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func getDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
