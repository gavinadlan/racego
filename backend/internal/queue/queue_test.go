package queue

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/checkout"
	"github.com/gavinadlan/racego/backend/internal/config"
)

var testCfg = config.QueueConfig{LobbyMinutes: 15, AdmitPerSec: 20, MaxActive: 300, PassTTL: 10 * time.Minute, HeartbeatTTL: 45 * time.Second}

func newQueue(t *testing.T, cfg config.QueueConfig) (*Queue, *checkout.Inventory, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 64, PoolTimeout: time.Minute})
	t.Cleanup(func() { _ = rdb.Close() })
	signer, err := NewPassSigner("", true)
	if err != nil {
		t.Fatal(err)
	}
	inv := checkout.NewInventory(rdb)
	return New(rdb, inv, signer, cfg), inv, mr
}

var t0 = time.Date(2027, 1, 3, 10, 0, 0, 0, time.UTC)

func testEvent() Event {
	return Event{ID: "ev1", SaleOpenAt: t0, SaleCloseAt: t0.Add(24 * time.Hour), CategoryIDs: []string{"c1"}}
}

func uid(i int) string { return fmt.Sprintf("u%05d", i) }

func mustJoin(t *testing.T, q *Queue, ev Event, user string, now time.Time) State {
	t.Helper()
	st, _, err := q.Join(context.Background(), ev, user, now)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestJoinIdempotentOnePositionPerAccount(t *testing.T) {
	q, _, _ := newQueue(t, testCfg)
	ctx, ev := context.Background(), testEvent()
	now := t0.Add(time.Minute)
	mustJoin(t, q, ev, "a", now)
	st, created, err := q.Join(ctx, ev, "b", now.Add(time.Second))
	if err != nil || !created || *st.Position != 2 {
		t.Fatalf("b: %+v %v %v", st, created, err)
	}
	// Bergabung ulang (juga bersamaan) tidak membuat posisi baru dan tidak memindah ke belakang.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, created, err := q.Join(ctx, ev, "a", now.Add(time.Hour)); err != nil || created {
				t.Errorf("join ulang: created=%v err=%v", created, err)
			}
		}()
	}
	wg.Wait()
	st, _ = q.Status(ctx, ev, "a", now.Add(2*time.Second))
	if st.State != StateWaiting || *st.Position != 1 || st.QueueSize != 2 {
		t.Fatalf("a setelah join ulang: %+v", st)
	}
}

func TestLobbyHasNoPositionAndClosedOutsideWindow(t *testing.T) {
	q, _, mr := newQueue(t, testCfg)
	ctx, ev := context.Background(), testEvent()
	st := mustJoin(t, q, ev, "a", t0.Add(-5*time.Minute))
	if st.State != StateLobby || st.Position != nil || st.QueueSize != 1 || !st.LobbyOpensAt.Equal(t0.Add(-15*time.Minute)) {
		t.Fatalf("lobi: %+v", st)
	}
	if ttl := mr.TTL(keysFor(ev.ID).queue()); ttl <= 0 || ttl > 7*24*time.Hour {
		t.Fatalf("antrian harus punya batas umur (anti-bocor), TTL %v", ttl)
	}
	if q.Open(ev, t0.Add(-16*time.Minute)) || !q.Open(ev, t0.Add(-15*time.Minute)) || q.Open(ev, ev.SaleCloseAt) {
		t.Fatal("jendela antrian salah")
	}
	if st, _ := q.Status(ctx, ev, "nobody", t0); st.State != StateNotInQueue {
		t.Fatalf("bukan peserta: %+v", st)
	}
	ev.Closed = true
	if st, _ := q.Status(ctx, ev, "a", t0); st.State != StateClosed || st.PollAfterMs != 0 {
		t.Fatalf("event ditutup: %+v", st)
	}
}

// Urutan lobi acak: korelasi (Spearman) urutan masuk lobi vs urutan antrian mendekati nol,
// dan yang bergabung setelah jam buka selalu di belakang semua peserta lobi, urut kedatangan.
func TestLobbyOrderIsRandomAndLateJoinersGoLast(t *testing.T) {
	q, _, _ := newQueue(t, testCfg)
	ctx, ev := context.Background(), testEvent()
	const lobby, late = 2000, 50
	for i := 0; i < lobby; i++ {
		mustJoin(t, q, ev, uid(i), t0.Add(-10*time.Minute+time.Duration(i)*time.Millisecond))
	}
	for i := lobby; i < lobby+late; i++ {
		mustJoin(t, q, ev, uid(i), t0.Add(time.Duration(i)*time.Millisecond))
	}
	order, err := q.rdb.ZRange(ctx, keysFor(ev.ID).queue(), 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, u := range order {
		pos[u] = i
	}
	var sumD2 float64
	for i := 0; i < lobby; i++ {
		if pos[uid(i)] >= lobby {
			t.Fatalf("peserta lobi %d di belakang peserta pasca-buka", i)
		}
		d := float64(i - pos[uid(i)])
		sumD2 += d * d
	}
	rho := 1 - 6*sumD2/(lobby*(lobby*lobby-1))
	t.Logf("Spearman rho urutan masuk vs urutan antrian (n=%d): %.4f", lobby, rho)
	if math.Abs(rho) > 0.1 {
		t.Fatalf("urutan lobi tidak acak: rho=%.3f", rho)
	}
	for i := lobby; i < lobby+late; i++ {
		if pos[uid(i)] != i {
			t.Fatalf("peserta pasca-buka %d di posisi %d", i, pos[uid(i)])
		}
	}
}

func seenAll(q *Queue, ev Event, n int, now time.Time) {
	for i := 0; i < n; i++ {
		_, _ = q.Status(context.Background(), ev, uid(i), now)
	}
}

func TestAdmitRespectsRateAndMaxActive(t *testing.T) {
	cfg := testCfg
	cfg.MaxActive = 30
	q, _, _ := newQueue(t, cfg)
	ctx, ev := context.Background(), testEvent()
	for i := 0; i < 100; i++ {
		mustJoin(t, q, ev, uid(i), t0.Add(-time.Minute))
	}
	if n, _ := q.Admit(ctx, ev, t0.Add(-time.Second)); n != 0 {
		t.Fatalf("sebelum jam buka dipanggil %d", n)
	}
	want := []int{20, 10, 0}
	var first string
	for i, w := range want {
		now := t0.Add(time.Duration(i) * time.Second)
		seenAll(q, ev, 100, now)
		r, err := q.admit(ctx, ev, now)
		if err != nil || len(r.users) != w || r.active > cfg.MaxActive {
			t.Fatalf("detik %d: dipanggil %d (mau %d), aktif %d, err %v", i, len(r.users), w, r.active, err)
		}
		if i == 0 {
			first = r.users[0] // urutan lobi acak: belum tentu uid(0)
		}
	}
	// Panggilan lebih rapat dari 1 detik hanya mendapat jatah sebanding waktunya.
	cfg.MaxActive = 1000
	q.cfg = cfg
	now := t0.Add(3 * time.Second)
	if n, _ := q.Admit(ctx, ev, now); n != 20 {
		t.Fatalf("setelah MAX_ACTIVE naik: %d", n)
	}
	if n, _ := q.Admit(ctx, ev, now.Add(250*time.Millisecond)); n != 5 {
		t.Fatalf("250 ms: %d, mau 5", n)
	}
	st, _ := q.Status(ctx, ev, first, now)
	if st.State != StateAdmitted || st.Pass == "" || st.PassExpiresAt == nil || !st.PassExpiresAt.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("peserta pertama: %+v", st)
	}
}

func TestAdmitDropsParticipantsWithoutHeartbeat(t *testing.T) {
	q, _, _ := newQueue(t, testCfg)
	ctx, ev := context.Background(), testEvent()
	for i := 0; i < 5; i++ {
		mustJoin(t, q, ev, uid(i), t0.Add(-time.Minute))
	}
	now := t0.Add(50 * time.Second) // semua terakhir terlihat 110 detik lalu
	_, _ = q.Status(ctx, ev, uid(3), now)
	r, err := q.admit(ctx, ev, now)
	if err != nil || len(r.users) != 1 || r.users[0] != uid(3) || r.dropped != 4 {
		t.Fatalf("hasil %+v err %v", r, err)
	}
	if st, _ := q.Status(ctx, ev, uid(0), now); st.State != StateNotInQueue {
		t.Fatalf("peserta tanpa tanda hidup harus keluar: %+v", st)
	}
}

func TestPassChecks(t *testing.T) {
	q, _, _ := newQueue(t, testCfg)
	ctx, ev := context.Background(), testEvent()
	mustJoin(t, q, ev, "a", t0)
	mustJoin(t, q, ev, "b", t0)
	if _, err := q.Admit(ctx, ev, t0); err != nil {
		t.Fatal(err)
	}
	pa, _ := q.Status(ctx, ev, "a", t0)
	pb, _ := q.Status(ctx, ev, "b", t0)
	now := t0.Add(time.Minute)
	other, _ := NewPassSigner("kunci-lain-yang-cukup-panjang-untuk-tes-123", false)
	tampered := pa.Pass[:len(pa.Pass)-2] + "AA"
	if tampered == pa.Pass {
		tampered = pa.Pass[:len(pa.Pass)-2] + "BB"
	}
	cases := []struct {
		name, event, user, token string
		at                       time.Time
		want                     error
	}{
		{"sah", ev.ID, "a", pa.Pass, now, nil},
		{"tanpa pass", ev.ID, "a", "", now, checkout.ErrQueueRequired},
		{"pass user lain", ev.ID, "a", pb.Pass, now, checkout.ErrQueueRequired},
		{"event lain", "ev2", "a", pa.Pass, now, checkout.ErrQueueRequired},
		{"tanda tangan diubah", ev.ID, "a", tampered, now, checkout.ErrQueueRequired},
		{"kunci lain", ev.ID, "a", other.Sign(ev.ID, "a", now.Add(time.Hour)), now, checkout.ErrQueueRequired},
		{"rusak", ev.ID, "a", "bukan.pass.sah", now, checkout.ErrQueueRequired},
		{"kedaluwarsa", ev.ID, "a", pa.Pass, t0.Add(10 * time.Minute), checkout.ErrQueuePassExpired},
	}
	for _, c := range cases {
		if err := q.Check(ctx, c.event, c.user, c.token, c.at); !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v, mau %v", c.name, err, c.want)
		}
	}
	// Dicabut (dilepas setelah reservasi atau keluar antrian): tanda tangan masih sah tapi ditolak.
	if err := q.ReleasePass(ctx, ev.ID, "a"); err != nil {
		t.Fatal(err)
	}
	if err := q.Check(ctx, ev.ID, "a", pa.Pass, now); !errors.Is(err, checkout.ErrQueueRequired) {
		t.Fatalf("pass dicabut: %v", err)
	}
	if err := q.Leave(ctx, ev.ID, "b"); err != nil {
		t.Fatal(err)
	}
	if err := q.Check(ctx, ev.ID, "b", pb.Pass, now); !errors.Is(err, checkout.ErrQueueRequired) {
		t.Fatalf("keluar antrian: %v", err)
	}
	if _, err := NewPassSigner("", false); err == nil {
		t.Fatal("QUEUE_SECRET kosong di produksi harus ditolak")
	}
}

func TestReleasePassRestoresCapacity(t *testing.T) {
	cfg := testCfg
	cfg.MaxActive = 1
	q, _, _ := newQueue(t, cfg)
	ctx, ev := context.Background(), testEvent()
	mustJoin(t, q, ev, "a", t0)
	mustJoin(t, q, ev, "b", t0)
	if n, _ := q.Admit(ctx, ev, t0); n != 1 {
		t.Fatalf("pertama: %d", n)
	}
	if n, _ := q.Admit(ctx, ev, t0.Add(time.Second)); n != 0 {
		t.Fatalf("kapasitas penuh tapi dipanggil %d", n)
	}
	_ = q.ReleasePass(ctx, ev.ID, "a")
	_, _ = q.Status(ctx, ev, "b", t0.Add(2*time.Second))
	if n, _ := q.Admit(ctx, ev, t0.Add(2*time.Second)); n != 1 {
		t.Fatalf("setelah pass dilepas: %d", n)
	}
	// Pass kedaluwarsa juga mengembalikan kapasitas.
	mustJoin(t, q, ev, "c", t0.Add(3*time.Second))
	later := t0.Add(2*time.Second + cfg.PassTTL)
	_, _ = q.Status(ctx, ev, "c", later)
	if n, _ := q.Admit(ctx, ev, later); n != 1 {
		t.Fatalf("setelah pass kedaluwarsa: %d", n)
	}
}

func TestTickSoldOutAndClosed(t *testing.T) {
	q, inv, mr := newQueue(t, testCfg)
	ctx, ev := context.Background(), testEvent()
	if _, err := inv.Load(ctx, "c1", 1); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"a", "b"} {
		mustJoin(t, q, ev, u, t0)
	}
	if n, err := q.Tick(ctx, ev, t0); n != 2 || err != nil {
		t.Fatalf("tick: %d %v", n, err)
	}
	res, err := inv.Reserve(ctx, checkout.ReserveRequest{CategoryID: "c1", UserID: "a", Qty: 1, MaxPerUser: 1, TTL: time.Minute, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	mustJoin(t, q, ev, "c", t0.Add(time.Second))
	// Stok 0 tapi hold masih ada: belum habis, pemanggilan ditahan.
	if n, _ := q.Tick(ctx, ev, t0.Add(2*time.Second)); n != 0 {
		t.Fatalf("stok 0 tapi dipanggil %d", n)
	}
	if st, _ := q.Status(ctx, ev, "c", t0.Add(2*time.Second)); st.State != StateWaiting {
		t.Fatalf("hold masih ada: %+v", st)
	}
	hold, _, _ := inv.GetHold(ctx, "c1", res.ID)
	if _, err := inv.Confirm(ctx, "c1", res.ID, "a", hold.Qty); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Tick(ctx, ev, t0.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"b", "c", "baru"} {
		if st, _ := q.Status(ctx, ev, u, t0.Add(3*time.Second)); st.State != StateSoldOut || st.PollAfterMs != 0 {
			t.Fatalf("%s: %+v", u, st)
		}
	}
	if ttl := mr.TTL(keysFor(ev.ID).queue()); ttl <= 0 || ttl > finalTTL {
		t.Fatalf("antrian habis harus dibersihkan dengan TTL, dapat %v", ttl)
	}
	// Mitra menambah kuota: antrian dibuka lagi.
	if _, _, err := inv.AdjustStock(ctx, "c1", 1); err != nil {
		t.Fatal(err)
	}
	_, _ = q.Status(ctx, ev, "c", t0.Add(4*time.Second))
	if _, err := q.Tick(ctx, ev, t0.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if st, _ := q.Status(ctx, ev, "c", t0.Add(4*time.Second)); st.State != StateAdmitted {
		t.Fatalf("setelah kuota ditambah: %+v", st)
	}
	// TTL pembersihan 10 menit dicabut; kembali ke batas umur 7 hari (anti-bocor).
	for _, key := range []string{keysFor(ev.ID).meta(), keysFor(ev.ID).opened()} { // antrian sudah kosong = key hilang
		if ttl := mr.TTL(key); ttl <= finalTTL || ttl > 7*24*time.Hour {
			t.Fatalf("TTL %s setelah dibuka lagi: %v", key, ttl)
		}
	}
	ev.Closed = true
	if _, err := q.Tick(ctx, ev, t0.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if v := mr.Exists(keysFor(ev.ID).final()); !v {
		t.Fatal("event ditutup harus ditandai")
	}
}

func TestPollAfterGrowsWithPositionAndHasJitter(t *testing.T) {
	q, _, _ := newQueue(t, testCfg)
	prev := 0
	for _, ahead := range []int{0, 10, 100, 500, 1200} {
		b := waitingPollBase(ahead)
		if b < prev || b < 2500 || b > 15000 {
			t.Fatalf("ahead %d: %d", ahead, b)
		}
		prev = b
	}
	if waitingPollBase(0) != 2500 || waitingPollBase(100000) != 15000 {
		t.Fatal("batas polling")
	}
	q.jitter = func() float64 { return 0 }
	lo := q.poll(2500)
	q.jitter = func() float64 { return 1 }
	hi := q.poll(2500)
	if lo != 2000 || hi != 3000 {
		t.Fatalf("jitter ±20%%: %d..%d", lo, hi)
	}
	// Jitter sungguhan menghasilkan nilai berbeda-beda.
	q.jitter = newQueueJitter()
	seen := map[int]bool{}
	for i := 0; i < 20; i++ {
		seen[q.poll(10000)] = true
	}
	if len(seen) < 10 {
		t.Fatalf("jitter terlalu seragam: %v", seen)
	}
	if p := lobbyPoll(time.Hour, 0.5); p != 15000 {
		t.Fatalf("lobi jauh: %d", p)
	}
	if p := lobbyPoll(3*time.Second, 0); p < 3000 || p > 3500 {
		t.Fatalf("menjelang buka: %d", p)
	}
}

func newQueueJitter() func() float64 { return New(nil, nil, nil, testCfg).jitter }

// Uji konkurensi: 5.000 peserta bergabung bersamaan sementara dua "worker" memanggil secara
// paralel, dan peserta yang dipanggil langsung membaca pass lalu reservasi dengan pass itu.
// Tidak boleh: reservasi melebihi kuota, user dipanggil dua kali, pass aktif > MAX_ACTIVE.
// (Peserta tidak polling terus-menerus di sini: miniredis menyortir ZSET di tiap ZRANK.)
func TestConcurrentJoinAdmitReserve(t *testing.T) {
	cfg := config.QueueConfig{LobbyMinutes: 15, AdmitPerSec: 2000, MaxActive: 50, PassTTL: time.Minute, HeartbeatTTL: time.Hour}
	q, inv, _ := newQueue(t, cfg)
	ctx := context.Background()
	const users, quota = 5000, 300
	ev := Event{ID: "evc", SaleOpenAt: time.Now().Add(-time.Second), SaleCloseAt: time.Now().Add(time.Hour), CategoryIDs: []string{"cc"}}
	if _, err := inv.Load(ctx, "cc", quota); err != nil {
		t.Fatal(err)
	}

	var reserved, soldOut, maxActive atomic.Int64
	var mu sync.Mutex
	admittedBy := map[string]int{}
	called := make(chan string, users)
	stop := make(chan struct{})
	var admitters sync.WaitGroup
	for w := 0; w < 2; w++ {
		admitters.Add(1)
		go func() {
			defer admitters.Done()
			for {
				select {
				case <-stop:
					return
				case <-time.After(2 * time.Millisecond):
				}
				r, err := q.admit(ctx, ev, time.Now())
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				for _, u := range r.users {
					admittedBy[u]++
					called <- u
				}
				mu.Unlock()
				for cur := maxActive.Load(); int64(r.active) > cur && !maxActive.CompareAndSwap(cur, int64(r.active)); cur = maxActive.Load() {
				}
			}
		}()
	}

	var buyers sync.WaitGroup
	for b := 0; b < 32; b++ {
		buyers.Add(1)
		go func() {
			defer buyers.Done()
			for u := range called {
				st, err := q.Status(ctx, ev, u, time.Now())
				if err != nil || st.State != StateAdmitted {
					t.Errorf("%s dipanggil tapi status %+v (%v)", u, st, err)
					continue
				}
				if err := q.Check(ctx, ev.ID, u, st.Pass, time.Now()); err != nil {
					t.Errorf("pass sah ditolak: %v", err)
					continue
				}
				_, err = inv.Reserve(ctx, checkout.ReserveRequest{CategoryID: "cc", UserID: u, Qty: 1, MaxPerUser: 1, TTL: time.Minute, Now: time.Now()})
				switch {
				case err == nil:
					reserved.Add(1)
					_ = q.ReleasePass(ctx, ev.ID, u)
				case errors.Is(err, checkout.ErrSoldOut):
					soldOut.Add(1)
					_ = q.Leave(ctx, ev.ID, u)
				default:
					t.Error(err)
				}
			}
		}()
	}

	var joiners sync.WaitGroup
	for i := 0; i < users; i++ {
		joiners.Add(1)
		go func(u string) {
			defer joiners.Done()
			if _, _, err := q.Join(ctx, ev, u, time.Now()); err != nil {
				t.Error(err)
			}
		}(uid(i))
	}
	joiners.Wait()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if n, ok, _ := inv.Available(ctx, "cc"); ok && n == 0 {
			break
		}
	}
	close(stop)
	admitters.Wait()
	close(called)
	buyers.Wait()

	if reserved.Load() != quota {
		t.Fatalf("reservasi sukses %d, kuota %d", reserved.Load(), quota)
	}
	for u, n := range admittedBy {
		if n > 1 {
			t.Fatalf("%s dipanggil %d kali", u, n)
		}
	}
	if maxActive.Load() > int64(cfg.MaxActive) {
		t.Fatalf("pass aktif mencapai %d > %d", maxActive.Load(), cfg.MaxActive)
	}
	size, _ := q.rdb.ZCard(ctx, keysFor(ev.ID).queue()).Result()
	t.Logf("join %d, dipanggil %d, reservasi %d, habis saat dipanggil %d, pass aktif maks %d, sisa antrian %d",
		users, len(admittedBy), reserved.Load(), soldOut.Load(), maxActive.Load(), size)
	if int(size)+len(admittedBy) != users {
		t.Fatalf("antrian %d + dipanggil %d != %d", size, len(admittedBy), users)
	}
}
