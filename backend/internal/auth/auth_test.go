package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const frontend = "http://localhost:3000"

var (
	cookieUser = uuid.MustParse("aaaaaaaa-1111-1111-1111-111111111111")
	headerUser = uuid.MustParse("bbbbbbbb-2222-2222-2222-222222222222")
)

func fakeLookup(_ context.Context, token string) (uuid.UUID, error) {
	if token == "good" {
		return cookieUser, nil
	}
	return uuid.Nil, ErrNoSession
}

// serve menjalankan middleware dan mengembalikan status + user yang terlihat handler.
func serve(devMode bool, req *http.Request) (int, uuid.UUID, bool) {
	var got uuid.UUID
	var ok bool
	h := Middleware(fakeLookup, frontend, devMode)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = UserID(r.Context())
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, got, ok
}

func get(cookie, header string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	}
	if header != "" {
		r.Header.Set("X-User-ID", header)
	}
	return r
}

func TestMiddlewareIdentity(t *testing.T) {
	cases := []struct {
		name           string
		dev            bool
		cookie, header string
		want           uuid.UUID
		wantOK         bool
	}{
		{"cookie valid", false, "good", "", cookieUser, true},
		{"cookie tak dikenal", false, "bad", "", uuid.Nil, false},
		{"X-User-ID di produksi diabaikan", false, "", headerUser.String(), uuid.Nil, false},
		{"X-User-ID di development", true, "", headerUser.String(), headerUser, true},
		{"cookie mengalahkan X-User-ID", true, "good", headerUser.String(), cookieUser, true},
		{"cookie tak dikenal + dev header", true, "bad", headerUser.String(), headerUser, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, got, ok := serve(c.dev, get(c.cookie, c.header))
			if ok != c.wantOK || got != c.want {
				t.Fatalf("got %v %v, want %v %v", got, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestMiddlewareRejectsForeignOrigin(t *testing.T) {
	post := func(origin string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/orders", nil)
		r.AddCookie(&http.Cookie{Name: CookieName, Value: "good"})
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	if code, _, _ := serve(false, post("https://evil.example")); code != http.StatusForbidden {
		t.Fatalf("Origin asing: want 403, got %d", code)
	}
	if code, _, ok := serve(false, post(frontend)); code != http.StatusOK || !ok {
		t.Fatalf("Origin frontend: got %d %v", code, ok)
	}
	if code, _, ok := serve(false, post("")); code != http.StatusOK || !ok {
		t.Fatalf("tanpa Origin: got %d %v", code, ok)
	}
	r := get("good", "")
	r.Header.Set("Origin", "https://evil.example")
	if code, _, _ := serve(false, r); code != http.StatusOK {
		t.Fatalf("GET tidak mengubah data, harus lolos: %d", code)
	}
}

// Butuh Postgres (data seed). Jalan bila RACEGO_TEST_DATABASE_URL diisi.
func TestSessionStore(t *testing.T) {
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
	s := NewSessionStore(db)
	budi := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	token, _, err := s.Create(ctx, budi, "go-test", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if id, err := s.Lookup(ctx, token); err != nil || id != budi {
		t.Fatalf("lookup: %v %v", id, err)
	}
	if _, err := db.Exec(ctx, `UPDATE sessions SET expires_at = now() - interval '1 second' WHERE token_hash = $1`, tokenHash(token)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(ctx, token); err != ErrNoSession {
		t.Fatalf("sesi kedaluwarsa harus ditolak: %v", err)
	}
	if err := s.Delete(ctx, token); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token_hash = $1`, tokenHash(token)).Scan(&n)
	if n != 0 {
		t.Fatal("sesi belum terhapus")
	}
}
