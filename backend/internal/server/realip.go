package server

import (
	"net"
	"net/http"
	"strings"
)

// realIP mengganti r.RemoteAddr dengan IP klien asli, tapi hanya bila ada proxy tepercaya
// di depan API (TRUSTED_PROXY_HOPS). hops = 0: header X-Forwarded-For/X-Real-IP diabaikan,
// karena siapa pun bisa mengisinya untuk mengakali batas per IP.
//
// hops > 0: CF-Connecting-IP dipakai bila ada (Cloudflare Tunnel menimpanya, klien tidak bisa
// memalsukannya); selain itu entri ke-hops dari kanan X-Forwarded-For, karena setiap proxy
// tepercaya menambahkan satu entri di ujung kanan.
func realIP(hops int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if hops <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ip := clientIPFromHeaders(r.Header, hops); ip != "" {
				r.RemoteAddr = ip
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIPFromHeaders(h http.Header, hops int) string {
	if ip := net.ParseIP(strings.TrimSpace(h.Get("CF-Connecting-IP"))); ip != nil {
		return ip.String()
	}
	var chain []string
	for _, v := range h.Values("X-Forwarded-For") {
		for _, part := range strings.Split(v, ",") {
			chain = append(chain, strings.TrimSpace(part))
		}
	}
	if len(chain) == 0 {
		return ""
	}
	// Rantai lebih pendek dari hops = proxy menambah lebih sedikit dari dugaan; ambil yang paling kiri.
	i := max(len(chain)-hops, 0)
	if ip := net.ParseIP(chain[i]); ip != nil {
		return ip.String()
	}
	return ""
}
