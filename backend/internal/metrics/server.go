package metrics

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// CheckAddr menolak listener metrik yang terbuka ke luar tanpa token. addr kosong = mati (sah).
func CheckAddr(name, addr, token string) error {
	if addr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s=%q tidak valid: pakai host:port, mis. 127.0.0.1:9464", name, addr)
	}
	if loopback(host) || token != "" {
		return nil
	}
	return fmt.Errorf("%s=%q bukan alamat loopback: isi METRICS_TOKEN, atau pakai 127.0.0.1:<port> (metrik tidak boleh terbuka tanpa token)", name, addr)
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Handler: GET /metrics. Token terisi = wajib "Authorization: Bearer <token>" (banding konstan-waktu).
// debug=true (development saja) menambah /debug/pprof/ untuk profil saat uji beban.
func Handler(token string, debug bool) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(Registry, promhttp.HandlerOpts{}))
	if debug {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}
	if token == "" {
		return mux
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// Start membuka listener metrik sendiri (bukan di server publik, tidak dilewatkan Caddy).
// addr kosong = tidak ada listener: mengembalikan nil. Alamat non-loopback tanpa token = error.
func Start(name, addr, token string, debug bool) (net.Listener, error) {
	if addr == "" {
		return nil, nil
	}
	if err := CheckAddr(name, addr, token); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listener metrik %s: %w", addr, err)
	}
	srv := &http.Server{Handler: Handler(token, debug), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			slog.Error("listener metrik berhenti", "err", err)
		}
	}()
	slog.Info("metrik berjalan", "addr", ln.Addr().String(), "token", token != "", "pprof", debug)
	return ln, nil
}
