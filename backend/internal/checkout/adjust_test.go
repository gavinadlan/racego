package checkout

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdjustStock(t *testing.T) {
	inv, _ := newInventory(t)
	ctx := context.Background()
	if _, loaded, err := inv.AdjustStock(ctx, "baru", 10); loaded || err != nil {
		t.Fatalf("stok belum dimuat: harus no-op, dapat loaded=%v err=%v", loaded, err)
	}
	inv.Load(ctx, "c", 10) //nolint:errcheck
	now := time.Now()
	for i := 0; i < 4; i++ {
		if _, err := reserve(inv, "c", fmt.Sprint("u", i), 1, 1, now); err != nil {
			t.Fatal(err)
		}
	}
	// stok 6, terpakai 4. Naik 5 → 11.
	if s, _, err := inv.AdjustStock(ctx, "c", 5); err != nil || s != 11 {
		t.Fatalf("naik: %d %v", s, err)
	}
	// Turun 12 (kuota 15 → 3 < terpakai 4) → ditolak, stok tidak berubah.
	if s, _, err := inv.AdjustStock(ctx, "c", -12); !errors.Is(err, ErrBelowUsed) || s != 11 {
		t.Fatalf("turun di bawah terpakai: %d %v", s, err)
	}
	// Turun tepat ke terpakai (kuota 15 → 4) → stok 0.
	if s, _, err := inv.AdjustStock(ctx, "c", -11); err != nil || s != 0 {
		t.Fatalf("turun ke terpakai: %d %v", s, err)
	}
	if _, err := reserve(inv, "c", "u9", 1, 1, now); !errors.Is(err, ErrSoldOut) {
		t.Fatalf("setelah kuota = terpakai harus habis, dapat %v", err)
	}
}

// Kuota diturunkan 100 → 60 saat 300 pembeli sedang berebut: total reservasi tidak pernah
// melebihi kuota yang berlaku (60 bila penurunan berhasil, 100 bila ditolak karena sudah lewat 60).
func TestAdjustStock_ConcurrentWithReservations(t *testing.T) {
	for round := 0; round < 20; round++ {
		inv, _ := newInventory(t)
		ctx := context.Background()
		cat := fmt.Sprint("cat", round)
		inv.Load(ctx, cat, 100) //nolint:errcheck
		now := time.Now()

		var ok atomic.Int64
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 300; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if _, err := reserve(inv, cat, fmt.Sprint("u", i), 1, 1, now); err == nil {
					ok.Add(1)
				}
			}(i)
		}
		var adjErr error
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(time.Duration(round) * 100 * time.Microsecond)
			_, _, adjErr = inv.AdjustStock(ctx, cat, -40)
		}()
		close(start)
		wg.Wait()

		quota := 100
		if adjErr == nil {
			quota = 60
		} else if !errors.Is(adjErr, ErrBelowUsed) {
			t.Fatal(adjErr)
		}
		left, _, _ := inv.Available(ctx, cat)
		if int(ok.Load()) != quota || left != 0 {
			t.Fatalf("ronde %d: kuota %d, reservasi %d, sisa %d", round, quota, ok.Load(), left)
		}
	}
}
