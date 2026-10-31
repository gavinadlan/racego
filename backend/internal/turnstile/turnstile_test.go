package turnstile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const secret = "rahasia-turnstile-uji-0000"

// fake: server siteverify palsu; reply menentukan balasan untuk tiap token.
func fake(t *testing.T, reply func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		reply(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestVerify(t *testing.T) {
	ctx := context.Background()
	if err := (*Verifier)(nil).Verify(ctx, "", ""); err != nil {
		t.Fatal("fitur mati harus selalu lolos")
	}
	if New("", "") != nil {
		t.Fatal("secret kosong = nil")
	}

	var got struct{ secret, response, remoteip, ctype string }
	srv, calls := fake(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got.secret, got.response, got.remoteip, got.ctype = r.PostForm.Get("secret"), r.PostForm.Get("response"), r.PostForm.Get("remoteip"), r.Header.Get("Content-Type")
		switch r.PostForm.Get("response") {
		case "ok":
			w.Write([]byte(`{"success":true,"challenge_ts":"2026-11-01T00:00:00Z","hostname":"localhost","error-codes":[]}`)) //nolint:errcheck
		case "bekas":
			w.Write([]byte(`{"success":false,"error-codes":["timeout-or-duplicate"]}`)) //nolint:errcheck
		case "cf-error":
			w.Write([]byte(`{"success":false,"error-codes":["internal-error"]}`)) //nolint:errcheck
		case "5xx":
			w.WriteHeader(http.StatusBadGateway)
		case "rusak":
			w.Write([]byte(`<html>bukan json`)) //nolint:errcheck
		case "lambat":
			time.Sleep(300 * time.Millisecond)
		default:
			w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`)) //nolint:errcheck
		}
	})
	v := New(secret, srv.URL)
	v.Client.Timeout = 100 * time.Millisecond

	if err := v.Verify(ctx, "ok", "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if got.secret != secret || got.response != "ok" || got.remoteip != "203.0.113.7" || got.ctype != "application/x-www-form-urlencoded" {
		t.Fatalf("form siteverify: %+v", got)
	}
	n := calls.Load()
	for _, tok := range []string{"", strings.Repeat("x", 2049)} {
		if err := v.Verify(ctx, tok, ""); !errors.Is(err, ErrFailed) {
			t.Fatalf("token %d karakter: %v", len(tok), err)
		}
	}
	if calls.Load() != n {
		t.Fatal("token kosong/terlalu panjang tidak boleh memanggil siteverify")
	}
	for tok, want := range map[string]error{"salah": ErrFailed, "bekas": ErrFailed, "cf-error": ErrUnavailable, "5xx": ErrUnavailable, "rusak": ErrUnavailable, "lambat": ErrUnavailable} {
		err := v.Verify(ctx, tok, "")
		if !errors.Is(err, want) {
			t.Errorf("%s: %v, mau %v", tok, err, want)
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("%s: secret bocor di error", tok)
		}
	}
	// Server mati (tidak terjangkau).
	srv.Close()
	if err := v.Verify(ctx, "ok", ""); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), secret) {
		t.Fatalf("server mati: %v", err)
	}
}

func TestCheckResponses(t *testing.T) {
	srv, _ := fake(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("response") == "ok" {
			w.Write([]byte(`{"success":true}`)) //nolint:errcheck
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	v := New(secret, srv.URL)
	for tok, want := range map[string]int{"ok": 0, "": 422, "x": 503} {
		rec := httptest.NewRecorder()
		ok := v.Check(rec, httptest.NewRequest("POST", "/x", nil), tok, "")
		if (want == 0) != ok || (want != 0 && rec.Code != want) {
			t.Errorf("%q: ok=%v kode=%d, mau %d", tok, ok, rec.Code, want)
		}
		if want == 422 && (!strings.Contains(rec.Body.String(), `"turnstile_token"`) || !strings.Contains(rec.Body.String(), "Verifikasi keamanan gagal, coba lagi")) {
			t.Errorf("422: %s", rec.Body)
		}
	}
}
