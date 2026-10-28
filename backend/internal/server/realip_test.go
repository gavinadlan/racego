package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRealIP(t *testing.T) {
	cases := []struct {
		name    string
		hops    int
		headers map[string][]string
		want    string
	}{
		{"tanpa proxy: XFF palsu diabaikan", 0, map[string][]string{"X-Forwarded-For": {"1.2.3.4"}, "X-Real-Ip": {"1.2.3.4"}}, "10.0.0.9:5555"},
		{"tanpa proxy: CF-Connecting-IP palsu diabaikan", 0, map[string][]string{"Cf-Connecting-Ip": {"1.2.3.4"}}, "10.0.0.9:5555"},
		{"1 hop: entri paling kanan", 1, map[string][]string{"X-Forwarded-For": {"6.6.6.6, 203.0.113.7"}}, "203.0.113.7"},
		{"2 hop: entri kedua dari kanan", 2, map[string][]string{"X-Forwarded-For": {"6.6.6.6, 203.0.113.7", "172.16.0.2"}}, "203.0.113.7"},
		{"rantai lebih pendek dari hops", 3, map[string][]string{"X-Forwarded-For": {"203.0.113.7"}}, "203.0.113.7"},
		{"Cloudflare", 1, map[string][]string{"Cf-Connecting-Ip": {"2001:db8::1"}, "X-Forwarded-For": {"6.6.6.6, 198.51.100.1"}}, "2001:db8::1"},
		{"XFF bukan IP", 1, map[string][]string{"X-Forwarded-For": {"bukan-ip"}}, "10.0.0.9:5555"},
		{"hop tanpa header", 1, nil, "10.0.0.9:5555"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			h := realIP(c.hops)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "10.0.0.9:5555"
			for k, vs := range c.headers {
				for _, v := range vs {
					req.Header.Add(k, v)
				}
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != c.want {
				t.Fatalf("RemoteAddr = %q, mau %q", got, c.want)
			}
		})
	}
}
