package finance

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	creatorKey  = "IRIS-creator-RAHASIA-uji"
	approverKey = "IRIS-approver-RAHASIA-uji"
	account     = "8877665544"
)

// fakeIris meniru Midtrans Payouts (Iris) secukupnya: idempoten per X-Idempotency-Key.
type fakeIris struct {
	mu       sync.Mutex
	mode     string // ok | reject | 5xx | timeout | garbage | approve5xx
	byKey    map[string]string
	status   map[string]string
	creates  int
	approves int
	authErr  string
}

func newFakeIris(t *testing.T, mode string) (*fakeIris, *httptest.Server) {
	f := &fakeIris{mode: mode, byKey: map[string]string{}, status: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func basic(key string) string { return "Basic " + base64.StdEncoding.EncodeToString([]byte(key+":")) }

func (f *fakeIris) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == "POST" && r.URL.Path == "/payouts":
		if r.Header.Get("Authorization") != basic(creatorKey) {
			f.authErr = "creator"
		}
		f.creates++
		switch f.mode {
		case "reject":
			w.WriteHeader(400)
			w.Write([]byte(`{"error_message":"Invalid beneficiary account ` + account + `","errors":["beneficiary_account"]}`)) //nolint:errcheck
			return
		case "5xx":
			w.WriteHeader(503)
			return
		case "timeout":
			f.mu.Unlock()
			time.Sleep(300 * time.Millisecond)
			f.mu.Lock()
		case "garbage":
			w.WriteHeader(201)
			w.Write([]byte(`<html>bukan json`)) //nolint:errcheck
			return
		}
		var body struct {
			Payouts []map[string]string `json:"payouts"`
		}
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		if len(body.Payouts) != 1 || body.Payouts[0]["beneficiary_account"] != account || body.Payouts[0]["amount"] != "150000.00" || body.Payouts[0]["beneficiary_bank"] != "bca" {
			w.WriteHeader(422)
			w.Write([]byte(`{"error_message":"body salah"}`)) //nolint:errcheck
			return
		}
		key := r.Header.Get("X-Idempotency-Key")
		ref, ok := f.byKey[key]
		if !ok {
			ref = "ref" + key[:8]
			f.byKey[key] = ref
			f.status[ref] = "queued"
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"payouts": []map[string]string{{"status": "queued", "reference_no": ref}}}) //nolint:errcheck
	case r.Method == "POST" && r.URL.Path == "/payouts/approve":
		if r.Header.Get("Authorization") != basic(approverKey) {
			f.authErr = "approver"
		}
		f.approves++
		if f.mode == "approve5xx" {
			w.WriteHeader(500)
			return
		}
		var body struct {
			Refs []string `json:"reference_nos"`
		}
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		for _, ref := range body.Refs {
			f.status[ref] = "approved"
		}
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/payouts/"):
		ref := strings.TrimPrefix(r.URL.Path, "/payouts/")
		st, ok := f.status[ref]
		if !ok {
			w.WriteHeader(404)
			w.Write([]byte(`{"error_message":"not found"}`)) //nolint:errcheck
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"reference_no": ref, "status": st, "amount": "150000.0"}) //nolint:errcheck
	default:
		w.WriteHeader(404)
	}
}

func req(id string) DisburseRequest {
	return DisburseRequest{ID: id, Amount: 150000, BankCode: "bca", AccountNumber: account, AccountHolder: "PT Uji", Notes: "uji"}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func noSecrets(t *testing.T, texts ...string) {
	t.Helper()
	for _, s := range texts {
		for _, secret := range []string{creatorKey, approverKey, account, base64.StdEncoding.EncodeToString([]byte(creatorKey + ":"))} {
			if strings.Contains(s, secret) {
				t.Fatalf("nilai rahasia bocor: %q", s)
			}
		}
	}
}

func TestIrisSuccessIdempotentAndStatus(t *testing.T) {
	logs := captureLogs(t)
	f, srv := newFakeIris(t, "ok")
	c := NewIris(srv.URL+"/", creatorKey, approverKey)
	ctx := context.Background()
	id := "11111111-2222-3333-4444-555555555555"
	ref, err := c.Create(ctx, req(id))
	if err != nil || ref == "" {
		t.Fatal(err)
	}
	// Respons ganda: kunci sama → reference sama, tidak ada transfer kedua.
	ref2, err := c.Create(ctx, req(id))
	if err != nil || ref2 != ref || len(f.byKey) != 1 {
		t.Fatalf("idempoten: %s %s %v", ref, ref2, err)
	}
	if f.authErr != "" {
		t.Fatalf("kunci salah dipakai untuk %s", f.authErr)
	}
	st, err := c.Status(ctx, ref)
	if err != nil || st.State != DisbursePending {
		t.Fatalf("approved harus pending: %+v %v", st, err)
	}
	f.status[ref] = "completed"
	if st, _ := c.Status(ctx, ref); st.State != DisburseCompleted {
		t.Fatalf("completed: %+v", st)
	}
	f.status[ref] = "failed"
	if st, _ := c.Status(ctx, ref); st.State != DisburseFailed || st.Reason == "" {
		t.Fatalf("failed: %+v", st)
	}
	f.status[ref] = "aneh"
	if _, err := c.Status(ctx, ref); err == nil {
		t.Fatal("status tidak dikenal harus error")
	}
	noSecrets(t, logs.String())
}

func TestIrisFailures(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		mode      string
		permanent bool
	}{{"reject", true}, {"5xx", false}, {"garbage", false}, {"timeout", false}, {"approve5xx", false}}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			_, srv := newFakeIris(t, tc.mode)
			c := NewIris(srv.URL, creatorKey, approverKey)
			if tc.mode == "timeout" {
				c.client.Timeout = 100 * time.Millisecond
			}
			_, err := c.Create(ctx, req("aaaaaaaa-2222-3333-4444-555555555555"))
			if err == nil {
				t.Fatal("harus error")
			}
			if _, perm := isPermanent(err); perm != tc.permanent {
				t.Fatalf("permanen=%v, mau %v: %v", perm, tc.permanent, err)
			}
			noSecrets(t, err.Error())
		})
	}
	// Bank di luar daftar: ditolak tanpa panggilan jaringan.
	_, err := NewIris("http://127.0.0.1:1", creatorKey, approverKey).Create(ctx, DisburseRequest{ID: "x", Amount: 1, BankCode: "lainnya", AccountNumber: account})
	if _, perm := isPermanent(err); !perm {
		t.Fatalf("bank lainnya: %v", err)
	}
}

func TestMockDisburser(t *testing.T) {
	now := time.Now()
	m := MockDisburser{Delay: time.Minute, Now: func() time.Time { return now }}
	ok, _ := m.Create(context.Background(), DisburseRequest{ID: "a", AccountNumber: "12345678"})
	bad, _ := m.Create(context.Background(), DisburseRequest{ID: "b", AccountNumber: "1234" + MockFailSuffix})
	if st, _ := m.Status(context.Background(), ok); st.State != DisbursePending {
		t.Fatal("sebelum jeda harus pending")
	}
	now = now.Add(time.Minute)
	if st, _ := m.Status(context.Background(), ok); st.State != DisburseCompleted {
		t.Fatal("setelah jeda harus completed")
	}
	if st, _ := m.Status(context.Background(), bad); st.State != DisburseFailed {
		t.Fatal("rekening berakhiran 0000 harus gagal")
	}
	if _, err := m.Status(context.Background(), "ngawur"); err == nil {
		t.Fatal("referensi asing harus error")
	}
}

func TestMidtransRefunder(t *testing.T) {
	const serverKey = "SB-Mid-server-RAHASIA"
	var got map[string]any
	var path, auth string
	reply := func(code int, body string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path, auth = r.URL.Path, r.Header.Get("Authorization")
			json.NewDecoder(r.Body).Decode(&got) //nolint:errcheck
			if body == "sleep" {
				time.Sleep(300 * time.Millisecond)
				return
			}
			w.WriteHeader(code)
			w.Write([]byte(body)) //nolint:errcheck
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	ctx := context.Background()
	rq := RefundRequest{Key: "rf-1", OrderID: "ord-1", Amount: 350000, Reason: "event batal"}

	res, err := NewMidtransRefunder(reply(200, `{"status_code":"200","refund_chargeback_id":123,"refund_key":"rf-1"}`).URL, serverKey).Refund(ctx, rq)
	if err != nil || res.State != RefundCompleted || res.Reference != "123" || path != "/v2/ord-1/refund" || auth != basic(serverKey) ||
		got["refund_key"] != "rf-1" || got["amount"] != float64(350000) {
		t.Fatalf("sukses: %+v %v %s %v", res, err, path, got)
	}
	res, err = NewMidtransRefunder(reply(200, `{"status_code":"412","status_message":"Merchant cannot modify the status of the transaction"}`).URL, serverKey).Refund(ctx, rq)
	if err != nil || res.State != RefundManual || !strings.Contains(res.Reason, "412") {
		t.Fatalf("412 → manual: %+v %v", res, err)
	}
	for _, body := range []string{"x", `{"status_code":"500"}`} {
		if _, err := NewMidtransRefunder(reply(200, body).URL, serverKey).Refund(ctx, rq); err == nil {
			t.Fatalf("%s harus error sementara", body)
		}
	}
	if _, err := NewMidtransRefunder(reply(502, "").URL, serverKey).Refund(ctx, rq); err == nil {
		t.Fatal("502 harus error sementara")
	}
	r := NewMidtransRefunder(reply(0, "sleep").URL, serverKey)
	r.client.Timeout = 100 * time.Millisecond
	_, err = r.Refund(ctx, rq)
	if err == nil || strings.Contains(err.Error(), serverKey) {
		t.Fatalf("timeout: %v", err)
	}
	if res, _ := (MockRefunder{}).Refund(ctx, RefundRequest{Key: "k", Reason: "Transfer MANUAL saja"}); res.State != RefundManual {
		t.Fatal("mock manual")
	}
}

func TestCSVSafeAndBOM(t *testing.T) {
	for in, want := range map[string]string{"=cmd|' /C calc'!A0": "'=cmd|' /C calc'!A0", "+1": "'+1", "-1": "'-1", "@x": "'@x",
		"\tx": "'\tx", "\rx": "'\rx", "aman": "aman", "": ""} {
		if got := CSVSafe(in); got != want {
			t.Fatalf("CSVSafe(%q) = %q", in, got)
		}
	}
	out := string(writeCSV([]string{"a", "b"}, [][]string{{"=1+1", "x;y"}}))
	if !strings.HasPrefix(out, "\uFEFFa;b\r\n") || !strings.Contains(out, `'=1+1;"x;y"`) {
		t.Fatalf("csv: %q", out)
	}
	if rupiah(1234567) != "Rp1.234.567" || rupiah(-5000) != "-Rp5.000" || rupiah(0) != "Rp0" {
		t.Fatal("rupiah")
	}
}
