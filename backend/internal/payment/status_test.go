package payment

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeMidtrans membalas GET /v2/{id}/status dengan body dari fungsi respond.
func fakeMidtrans(t *testing.T, key string, respond func(orderID string) (int, string)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte(key+":"))
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"status_code":"401"}`)
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/"), "/status")
		code, body := respond(id)
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func newTestReconciler(proc *Processor, key, base string) *Reconciler {
	r := NewReconciler(proc, key, false)
	r.baseURL = base
	return r
}

func TestReconcilerNotFoundIsIgnored(t *testing.T) {
	srv, _ := fakeMidtrans(t, "k", func(string) (int, string) {
		return http.StatusOK, `{"status_code":"404","status_message":"Transaction doesn't exist."}`
	})
	r := newTestReconciler(NewProcessor(nil, "k"), "k", srv.URL)
	out, err := r.Check(context.Background(), uuid.NewString())
	if err != nil || out != OutcomeIgnored {
		t.Fatalf("want ignored, got %v %v", out, err)
	}
}

func TestReconcilerRejectsMismatchedOrderAndBadKey(t *testing.T) {
	srv, _ := fakeMidtrans(t, "right", func(string) (int, string) {
		return http.StatusOK, `{"status_code":"200","order_id":"another","transaction_status":"settlement","gross_amount":"1.00"}`
	})
	if _, err := newTestReconciler(NewProcessor(nil, "right"), "right", srv.URL).Check(context.Background(), "mine"); err == nil {
		t.Fatal("order_id berbeda harus ditolak")
	}
	if _, err := newTestReconciler(NewProcessor(nil, "wrong"), "wrong", srv.URL).Check(context.Background(), "mine"); err == nil {
		t.Fatal("server key salah harus menghasilkan error")
	}
}

func TestReconcilerThrottle(t *testing.T) {
	srv, hits := fakeMidtrans(t, "k", func(string) (int, string) { return http.StatusNotFound, `{}` })
	r := newTestReconciler(NewProcessor(nil, "k"), "k", srv.URL)
	for i := 0; i < 5; i++ {
		if _, err := r.Refresh(context.Background(), "ord-1"); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("5 refresh beruntun harus jadi 1 panggilan ke Midtrans, dapat %d", hits.Load())
	}
}

// Uji dengan database sungguhan (data seed). Jalan bila RACEGO_TEST_DATABASE_URL diisi.
func TestReconcilerMarksPaidOnce(t *testing.T) {
	dsn := os.Getenv("RACEGO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RACEGO_TEST_DATABASE_URL kosong")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	orderID := uuid.New()
	_, err = db.Exec(ctx, `
		INSERT INTO orders (id, org_id, event_id, category_id, buyer_id, reservation_id, idempotency_key,
		                    qty, subtotal, platform_fee, total, status, snap_token, expires_at, created_at)
		SELECT $1, e.org_id, e.id, c.id, '11111111-1111-1111-1111-111111111111', $2, $2,
		       1, c.price, 0, c.price, 'expired', 'tok-real', now() - interval '5 minutes', now() - interval '20 minutes'
		FROM ticket_categories c JOIN events e ON e.id = c.event_id
		WHERE c.id = 'cccccccc-0000-0000-0000-000000000021'`, orderID, "test-"+orderID.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(ctx, `DELETE FROM outbox WHERE payload->>'order_id' = $1`, orderID.String()) //nolint:errcheck
		db.Exec(ctx, `DELETE FROM payment_events WHERE order_id = $1`, orderID)              //nolint:errcheck
		db.Exec(ctx, `DELETE FROM orders WHERE id = $1`, orderID)                            //nolint:errcheck
	})
	var total int64
	if err := db.QueryRow(ctx, `SELECT total FROM orders WHERE id = $1`, orderID).Scan(&total); err != nil {
		t.Fatal(err)
	}

	srv, _ := fakeMidtrans(t, "k", func(id string) (int, string) {
		// Respons status Midtrans tidak wajib membawa signature yang cocok: datanya kita ambil sendiri.
		return http.StatusOK, fmt.Sprintf(`{"status_code":"200","order_id":%q,"transaction_id":"tx-1",
			"transaction_status":"settlement","fraud_status":"accept","gross_amount":%q,"payment_type":"bank_transfer"}`,
			id, FormatAmount(total))
	})
	r := newTestReconciler(NewProcessor(db, "k"), "k", srv.URL)

	if out, err := r.Check(ctx, orderID.String()); err != nil || out != OutcomePaid {
		t.Fatalf("cek pertama: want paid, got %v %v", out, err)
	}
	if out, err := r.Check(ctx, orderID.String()); err != nil || out != OutcomeDuplicate {
		t.Fatalf("cek kedua: want duplicate, got %v %v", out, err)
	}
	var status string
	var paidMsgs int
	db.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, orderID).Scan(&status)                                                         //nolint:errcheck
	db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE topic = 'order.paid' AND payload->>'order_id' = $1`, orderID.String()).Scan(&paidMsgs) //nolint:errcheck
	if status != "paid" || paidMsgs != 1 {
		t.Fatalf("status=%s pesan order.paid=%d; want paid & 1", status, paidMsgs)
	}
}
