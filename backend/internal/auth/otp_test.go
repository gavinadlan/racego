package auth

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newOTPStore(t *testing.T) (*OTPStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewOTPStore(rdb), mr
}

func TestOTPSixDigitsAndSingleUse(t *testing.T) {
	s, _ := newOTPStore(t)
	ctx := context.Background()
	code, err := s.Issue(ctx, "budi@x.com")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d{6}$`).MatchString(code) {
		t.Fatalf("kode bukan 6 digit: %q", code)
	}
	if err := s.Verify(ctx, "budi@x.com", code); err != nil {
		t.Fatalf("verifikasi pertama: %v", err)
	}
	if err := s.Verify(ctx, "budi@x.com", code); !errors.Is(err, ErrOTPExpired) {
		t.Fatalf("kode kedua kali harus hangus, dapat %v", err)
	}
	if _, err := s.Issue(ctx, "budi@x.com"); err != nil {
		t.Fatalf("setelah berhasil masuk, minta kode lagi tidak perlu menunggu 60 detik: %v", err)
	}
}

func TestOTPCaseInsensitiveEmail(t *testing.T) {
	s, _ := newOTPStore(t)
	ctx := context.Background()
	code, _ := s.Issue(ctx, "  Budi@X.com ")
	if err := s.Verify(ctx, "budi@x.com", code); err != nil {
		t.Fatal(err)
	}
}

func TestOTPTooManyAttempts(t *testing.T) {
	s, _ := newOTPStore(t)
	ctx := context.Background()
	code, _ := s.Issue(ctx, "a@x.com")
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := 1; i < OTPMaxAttempts; i++ {
		var inv ErrOTPInvalid
		if err := s.Verify(ctx, "a@x.com", wrong); !errors.As(err, &inv) || inv.Remaining != OTPMaxAttempts-i {
			t.Fatalf("percobaan %d: %v", i, err)
		}
	}
	if err := s.Verify(ctx, "a@x.com", wrong); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("percobaan ke-5: %v", err)
	}
	if err := s.Verify(ctx, "a@x.com", code); !errors.Is(err, ErrOTPExpired) {
		t.Fatalf("kode benar setelah hangus: %v", err)
	}
}

func TestOTPExpires(t *testing.T) {
	s, mr := newOTPStore(t)
	ctx := context.Background()
	code, _ := s.Issue(ctx, "a@x.com")
	mr.FastForward(OTPTTL + time.Second)
	if err := s.Verify(ctx, "a@x.com", code); !errors.Is(err, ErrOTPExpired) {
		t.Fatalf("want expired, got %v", err)
	}
}

func TestOTPResendRateLimit(t *testing.T) {
	s, mr := newOTPStore(t)
	ctx := context.Background()
	if _, err := s.Issue(ctx, "a@x.com"); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(20 * time.Second)
	var rl ErrRateLimited
	if _, err := s.Issue(ctx, "A@x.com"); !errors.As(err, &rl) || rl.RetryAfter > 40*time.Second || rl.RetryAfter <= 0 {
		t.Fatalf("kirim ulang < 60 detik harus ditolak ±40 detik, dapat %v", err)
	}
	mr.FastForward(41 * time.Second)
	if _, err := s.Issue(ctx, "a@x.com"); err != nil {
		t.Fatalf("setelah 60 detik harus boleh: %v", err)
	}
	// maks 5 per jam
	for i := 0; i < 3; i++ {
		mr.FastForward(61 * time.Second)
		if _, err := s.Issue(ctx, "a@x.com"); err != nil {
			t.Fatalf("kirim ke-%d: %v", i+3, err)
		}
	}
	mr.FastForward(61 * time.Second)
	if _, err := s.Issue(ctx, "a@x.com"); !errors.As(err, &rl) {
		t.Fatalf("kirim ke-6 dalam sejam harus ditolak, dapat %v", err)
	}
}

func TestNormalize(t *testing.T) {
	if e, ok := NormalizeEmail(" Budi@X.COM "); !ok || e != "budi@x.com" {
		t.Fatal(e, ok)
	}
	for _, bad := range []string{"", "budi", "Budi <b@x.com>", "a@"} {
		if _, ok := NormalizeEmail(bad); ok {
			t.Fatalf("%q harus ditolak", bad)
		}
	}
	for in, want := range map[string]string{"+6281234567890": "081234567890", "62 812-3456-789": "08123456789", "081234567": "081234567"} {
		if got, ok := NormalizePhone(in); !ok || got != want {
			t.Fatalf("%q → %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"0212345678", "08123", "+6591234567"} {
		if _, ok := NormalizePhone(bad); ok {
			t.Fatalf("%q harus ditolak", bad)
		}
	}
}
