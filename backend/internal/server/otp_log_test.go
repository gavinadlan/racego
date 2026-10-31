package server

import (
	"bufio"
	"bytes"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/config"
	"github.com/gavinadlan/racego/backend/internal/notify"
)

// fakeSMTP: server SMTP minimal di 127.0.0.1 yang menyimpan isi DATA terakhir.
type fakeSMTP struct {
	addr string
	mu   sync.Mutex
	last string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &fakeSMTP{addr: ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	rd := bufio.NewReader(c)
	reply := func(l string) { _, _ = c.Write([]byte(l + "\r\n")) }
	reply("220 fake")
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "DATA"):
			reply("354 go")
			var b strings.Builder
			for {
				l, err := rd.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.mu.Lock()
			s.last = b.String()
			s.mu.Unlock()
			reply("250 ok")
		case strings.HasPrefix(cmd, "QUIT"):
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func (s *fakeSMTP) lastMail() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// captureLogs mengalihkan slog & log bawaan ke buffer selama tes.
func captureLogs(t *testing.T) *syncBuf {
	t.Helper()
	buf := &syncBuf{}
	prev, prevOut, prevFlags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.SetOutput(buf)
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return buf
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

var otpInMail = regexp.MustCompile(`\b(\d{6})\b`)

// Kode OTP hanya boleh dicatat di log saat APP_ENV=development; di produksi tidak pernah,
// juga tidak di respons API.
func TestOTPCodeNeverLoggedOutsideDevelopment(t *testing.T) {
	for _, env := range []string{"production", "development"} {
		t.Run(env, func(t *testing.T) {
			logs := captureLogs(t)
			smtpSrv := newFakeSMTP(t)
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { rdb.Close() })
			cfg := config.Config{Env: env, FrontendURL: "https://racego.test"}
			h := NewRouter(Deps{Cfg: cfg, Redis: rdb, Mailer: notify.NewMailer(smtpSrv.addr, "", "", "RaceGo <no-reply@racego.test>")})

			req := httptest.NewRequest(http.MethodPost, "/v1/auth/otp/request", strings.NewReader(`{"email":"contoh.otp@racego.test"}`))
			req.Header.Set("Origin", cfg.FrontendURL)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			m := otpInMail.FindStringSubmatch(smtpSrv.lastMail())
			if m == nil {
				t.Fatalf("kode tidak ditemukan di email: %q", smtpSrv.lastMail())
			}
			code := m[1]
			if strings.Contains(rec.Body.String(), code) {
				t.Fatal("kode OTP bocor di respons API")
			}
			inLog := strings.Contains(logs.String(), code)
			if env == "production" && inLog {
				t.Fatalf("kode OTP muncul di log produksi: %s", logs)
			}
			if env == "development" && !inLog { // memastikan tangkapan log benar-benar bekerja
				t.Fatalf("development seharusnya mencatat kode (dev): %s", logs)
			}
		})
	}
}
