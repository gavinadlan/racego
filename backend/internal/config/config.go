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

	DatabaseURL   string
	RedisAddr     string
	RedisPassword string

	MidtransServerKey    string
	MidtransIsProduction bool

	QRPrivateKey   string // base64 seed Ed25519 (32 byte)
	ReservationTTL time.Duration

	SMTPAddr     string
	SMTPUsername string // kosong untuk Mailpit; isi untuk Resend/penyedia SMTP lain
	SMTPPassword string
	MailFrom     string
}

// MockServerKey dipakai untuk menandatangani notifikasi palsu di mode mock.
const MockServerKey = "mock-server-key"

func Load() Config {
	return Config{
		Env:                  get("APP_ENV", "development"),
		HTTPAddr:             get("HTTP_ADDR", ":8080"),
		FrontendURL:          get("FRONTEND_URL", "http://localhost:3000"),
		DatabaseURL:          get("DATABASE_URL", "postgres://racego:racego@localhost:5433/racego?sslmode=disable"),
		RedisAddr:            get("REDIS_ADDR", "localhost:6380"),
		RedisPassword:        os.Getenv("REDIS_PASSWORD"),
		MidtransServerKey:    os.Getenv("MIDTRANS_SERVER_KEY"),
		MidtransIsProduction: getBool("MIDTRANS_IS_PRODUCTION", false),
		QRPrivateKey:         os.Getenv("QR_PRIVATE_KEY"),
		ReservationTTL:       getDuration("RESERVATION_TTL", 15*time.Minute),
		SMTPAddr:             get("SMTP_ADDR", "localhost:1025"),
		SMTPUsername:         os.Getenv("SMTP_USERNAME"),
		SMTPPassword:         os.Getenv("SMTP_PASSWORD"),
		MailFrom:             get("MAIL_FROM", "RaceGo <no-reply@racego.local>"),
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

func getDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
