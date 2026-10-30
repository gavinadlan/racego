// Package config membaca konfigurasi aplikasi dari environment variable.
package config

import (
	"errors"
	"fmt"
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

	KYCEncKey string // base64 32 byte; kunci enkripsi dokumen & data sensitif verifikasi mitra
	KYCDir    string // folder privat dokumen KYC terenkripsi (bukan di dalam UploadDir)

	Finance FinanceConfig
	errs    []error // nilai env tidak valid; dilaporkan Validate

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

// FinanceConfig mengatur saldo mitra, pencairan, dan refund. Lihat docs/04-pembayaran-dan-ledger.md.
type FinanceConfig struct {
	HoldDays        int           // PAYOUT_HOLD_DAYS: hari setelah hari lomba sebelum dana dirilis
	PayoutMin       int64         // PAYOUT_MIN: jumlah pencairan minimum (rupiah)
	DisburseMode    string        // DISBURSE_MODE: mock | midtrans
	MockDelay       time.Duration // DISBURSE_MOCK_DELAY: jeda mock processing → completed
	IrisBaseURL     string
	IrisCreatorKey  string
	IrisApproverKey string
}

const (
	DisburseMock     = "mock"
	DisburseMidtrans = "midtrans"
)

// MockServerKey dipakai untuk menandatangani notifikasi palsu di mode mock.
const MockServerKey = "mock-server-key"

func Load() Config {
	env := get("APP_ENV", "development")
	var errs []error
	strictInt := func(key string, def, min int64) int64 {
		raw := os.Getenv(key)
		if raw == "" {
			return def
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < min {
			errs = append(errs, fmt.Errorf("%s=%q tidak valid: harus bilangan bulat ≥ %d", key, raw, min))
			return def
		}
		return v
	}
	mockDelay := 5 * time.Second
	if raw := os.Getenv("DISBURSE_MOCK_DELAY"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			errs = append(errs, fmt.Errorf("DISBURSE_MOCK_DELAY=%q tidak valid: pakai durasi seperti 5s atau 1m", raw))
		} else {
			mockDelay = d
		}
	}
	mode := get("DISBURSE_MODE", DisburseMock)
	if mode != DisburseMock && mode != DisburseMidtrans {
		errs = append(errs, fmt.Errorf("DISBURSE_MODE=%q tidak valid: pilih mock atau midtrans", mode))
	}
	return Config{
		errs: errs,
		Finance: FinanceConfig{
			HoldDays:        int(strictInt("PAYOUT_HOLD_DAYS", 3, 0)),
			PayoutMin:       strictInt("PAYOUT_MIN", 50000, 1),
			DisburseMode:    mode,
			MockDelay:       mockDelay,
			IrisBaseURL:     get("IRIS_BASE_URL", "https://app.sandbox.midtrans.com/iris/api/v1"),
			IrisCreatorKey:  os.Getenv("IRIS_CREATOR_KEY"),
			IrisApproverKey: os.Getenv("IRIS_APPROVER_KEY"),
		},
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
		KYCEncKey:      os.Getenv("KYC_ENC_KEY"),
		KYCDir:         get("KYC_DIR", "./data/kyc"),
		SMTPAddr:       get("SMTP_ADDR", "localhost:1025"),
		SMTPUsername:   os.Getenv("SMTP_USERNAME"),
		SMTPPassword:   os.Getenv("SMTP_PASSWORD"),
		MailFrom:       get("MAIL_FROM", "RaceGo <no-reply@racego.local>"),
	}
}

// Validate menolak konfigurasi keuangan yang tidak valid; API dan worker tidak start bila gagal.
// Pesan tidak pernah memuat nilai kunci.
func (c Config) Validate() error {
	errs := append([]error{}, c.errs...)
	if c.Finance.DisburseMode == DisburseMidtrans {
		if c.Finance.IrisCreatorKey == "" || c.Finance.IrisApproverKey == "" {
			errs = append(errs, errors.New("DISBURSE_MODE=midtrans butuh IRIS_CREATOR_KEY dan IRIS_APPROVER_KEY (kunci Midtrans Payouts/Iris); kosongkan DISBURSE_MODE untuk mode mock"))
		}
		if c.MidtransMock() {
			errs = append(errs, errors.New("DISBURSE_MODE=midtrans butuh MIDTRANS_SERVER_KEY untuk refund lewat Core API"))
		}
	}
	return errors.Join(errs...)
}

// DisburseMock true bila pencairan & refund hanya disimulasikan (tidak ada uang sungguhan yang dikirim).
func (c Config) DisburseMock() bool { return c.Finance.DisburseMode != DisburseMidtrans }

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
