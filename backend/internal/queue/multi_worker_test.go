package queue

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Beberapa worker memanggil antrian yang sama (audit multi-instance, docs/13-skala-lanjutan.md):
// laju panggil total tetap AdmitPerSec, tidak dikalikan jumlah worker.
func TestAdmitRateSharedByWorkers(t *testing.T) {
	const seconds = 10
	for _, tc := range []struct {
		name string
		// skew: selisih jam tiap worker; worker ke-i berdetak tiap detik pada offset i/len detik.
		skew []time.Duration
	}{
		{"satu worker", []time.Duration{0}},
		{"dua worker, jam sama", []time.Duration{0, 0}},
		{"empat worker, jam sama", []time.Duration{0, 0, 0, 0}},
		{"tiga worker, satu jam tertinggal 10 detik", []time.Duration{0, 0, -10 * time.Second}},
		{"tiga worker, satu jam maju 10 detik", []time.Duration{0, 0, 10 * time.Second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, _, _ := newQueue(t, testCfg)
			ev, start := testEvent(), t0.Add(time.Minute)
			for i := 0; i < 1000; i++ {
				mustJoin(t, q, ev, uid(i), start)
			}
			total := 0
			for s := 0; s < seconds; s++ {
				for i, skew := range tc.skew {
					at := start.Add(time.Duration(s)*time.Second + time.Duration(i)*time.Second/time.Duration(len(tc.skew)) + skew)
					n, err := q.Admit(context.Background(), ev, at)
					if err != nil {
						t.Fatal(err)
					}
					total += n
				}
			}
			// + jatah awal sekali saja: satu detik (panggilan pertama) + satu detik bila jam terdepan datang kemudian.
			if limit := testCfg.AdmitPerSec * (seconds + 2); total > limit {
				t.Fatalf("%d dipanggil dalam %d detik, batas %d", total, seconds, limit)
			}
			if total < testCfg.AdmitPerSec*(seconds-1) {
				t.Fatalf("hanya %d dipanggil: antrian tersendat", total)
			}
		})
	}
}

// Empat worker di milidetik yang sama: satu jatah, dan tidak ada peserta yang dipanggil dua kali.
func TestAdmitConcurrentWorkersNoDoubleCall(t *testing.T) {
	q, _, _ := newQueue(t, testCfg)
	ev, now := testEvent(), t0.Add(time.Minute)
	for i := 0; i < 200; i++ {
		mustJoin(t, q, ev, uid(i), now)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	seen := map[string]bool{}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := q.admit(context.Background(), ev, now)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, u := range r.users {
				if seen[u] {
					t.Errorf("%s dipanggil dua kali", u)
				}
				seen[u] = true
			}
		}()
	}
	wg.Wait()
	if len(seen) != testCfg.AdmitPerSec {
		t.Fatalf("%d dipanggil, harus %d", len(seen), testCfg.AdmitPerSec)
	}
}
