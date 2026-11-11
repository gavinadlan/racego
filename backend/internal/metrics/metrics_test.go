package metrics

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestEmptyAddrMeansNoListener(t *testing.T) {
	ln, err := Start("METRICS_ADDR", "", "", false)
	if err != nil || ln != nil {
		t.Fatalf("METRICS_ADDR kosong harus tanpa listener: ln=%v err=%v", ln, err)
	}
}

func TestNonLoopbackNeedsToken(t *testing.T) {
	for _, addr := range []string{":9464", "0.0.0.0:9464", "192.168.1.10:9464", "[::]:9464", "api:9464"} {
		if err := CheckAddr("METRICS_ADDR", addr, ""); err == nil || !strings.Contains(err.Error(), "METRICS_TOKEN") {
			t.Errorf("%s tanpa token harus ditolak dengan pesan jelas, dapat: %v", addr, err)
		}
		if ln, err := Start("METRICS_ADDR", addr, "", false); err == nil {
			ln.Close()
			t.Errorf("%s tanpa token tidak boleh membuka listener", addr)
		}
		if err := CheckAddr("METRICS_ADDR", addr, "rahasia"); err != nil {
			t.Errorf("%s dengan token harus diterima: %v", addr, err)
		}
	}
	for _, addr := range []string{"", "127.0.0.1:9464", "localhost:9464", "[::1]:9464"} {
		if err := CheckAddr("METRICS_ADDR", addr, ""); err != nil {
			t.Errorf("%q (loopback/mati) tanpa token harus diterima: %v", addr, err)
		}
	}
	if err := CheckAddr("METRICS_ADDR", "9464", ""); err == nil {
		t.Error("alamat tanpa host:port harus ditolak")
	}
}

func get(t *testing.T, url, auth string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestListenerServesMetricsAndChecksToken(t *testing.T) {
	ln, err := Start("METRICS_ADDR", "127.0.0.1:0", "rahasia-uji", false)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	url := "http://" + ln.Addr().String()
	for _, auth := range []string{"", "Bearer salah", "Bearer rahasia-uj", "Bearer rahasia-uji2", "rahasia-uji", "Basic rahasia-uji"} {
		if code, body := get(t, url+"/metrics", auth); code != http.StatusUnauthorized || strings.Contains(body, "racego_") {
			t.Errorf("Authorization=%q: status %d (harus 401 tanpa isi metrik)", auth, code)
		}
	}
	code, body := get(t, url+"/metrics", "Bearer rahasia-uji")
	if code != http.StatusOK || !strings.Contains(body, "go_goroutines") || !strings.Contains(body, "process_") {
		t.Fatalf("token benar: status %d, metrik bawaan Go/proses tidak ada", code)
	}
	// pprof hanya ada bila debug=true (development).
	if code, _ := get(t, url+"/debug/pprof/", "Bearer rahasia-uji"); code != http.StatusNotFound {
		t.Errorf("pprof tanpa mode debug: status %d, harus 404", code)
	}
	if code, _ := get(t, url+"/", "Bearer rahasia-uji"); code != http.StatusNotFound {
		t.Errorf("path selain /metrics: status %d, harus 404", code)
	}
}

func TestLoopbackWithoutTokenIsOpenAndLocalOnly(t *testing.T) {
	ln, err := Start("METRICS_ADDR", "127.0.0.1:0", "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !ln.Addr().(*net.TCPAddr).IP.IsLoopback() {
		t.Fatalf("listener harus di loopback, dapat %s", ln.Addr())
	}
	if code, _ := get(t, "http://"+ln.Addr().String()+"/metrics", ""); code != http.StatusOK {
		t.Fatalf("loopback tanpa token: status %d", code)
	}
	if code, _ := get(t, "http://"+ln.Addr().String()+"/debug/pprof/cmdline", ""); code != http.StatusOK {
		t.Fatalf("pprof mode debug: status %d", code)
	}
}

func TestRedisHookAndLabelNormalization(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rdb.AddHook(RedisHook{})
	ctx := context.Background()
	rdb.Set(ctx, "user:someone@example.com", "x", 0)
	rdb.Get(ctx, "tidak-ada") // redis.Nil bukan error
	redis.NewScript(`return 1`).Run(ctx, rdb, []string{"k"})
	_, _ = rdb.Pipelined(ctx, func(p redis.Pipeliner) error { p.Get(ctx, "a"); return nil })
	Outbox("order.paid", nil)
	Outbox("topik-liar-3f2b6c1e-9d0a-4c55-8a51-0d6f3a2b1c9e", context.Canceled)
	Step("outbox", func() {})

	rec := httptest.NewRecorder()
	Handler("", false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	out := rec.Body.String()
	for _, want := range []string{
		`racego_redis_command_duration_seconds_count{cmd="set"}`, `cmd="evalsha"`, `cmd="pipeline"`,
		`racego_outbox_processed_total{result="ok",topic="order.paid"}`, `racego_outbox_processed_total{result="retry",topic="other"}`,
		`racego_worker_step_duration_seconds_count{step="outbox"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("keluaran metrik tidak memuat %s", want)
		}
	}
	for _, bad := range []string{"example.com", "topik-liar", "tidak-ada", `racego_redis_errors_total{cmd="get"}`} {
		if strings.Contains(out, bad) {
			t.Errorf("keluaran metrik memuat %q", bad)
		}
	}
	if method("PROPFIND") != "OTHER" || method("x@y.com") != "OTHER" || method(http.MethodPost) != "POST" {
		t.Error("metode tak dikenal harus digabung menjadi OTHER")
	}
}
