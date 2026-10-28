package checkout

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newInventory(t *testing.T) (*Inventory, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewInventory(rdb), mr
}

func reserve(inv *Inventory, cat, user string, qty, max int, now time.Time) (Reservation, error) {
	return inv.Reserve(context.Background(), ReserveRequest{
		CategoryID: cat, UserID: user, Qty: qty, MaxPerUser: max, TTL: 15 * time.Minute, Now: now,
	})
}

// Inti war tiket: 1.000 pembeli berebut 50 slot secara bersamaan → tepat 50 yang berhasil.
func TestReserve_NoOversellUnderConcurrency(t *testing.T) {
	inv, _ := newInventory(t)
	ctx := context.Background()
	const slots, buyers = 50, 1000
	if _, err := inv.Load(ctx, "21k", slots); err != nil {
		t.Fatal(err)
	}

	var ok, soldOut atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < buyers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := reserve(inv, "21k", fmt.Sprintf("user-%d", i), 1, 1, time.Now())
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrSoldOut):
				soldOut.Add(1)
			default:
				t.Errorf("error tak terduga: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if ok.Load() != slots {
		t.Fatalf("reservasi berhasil = %d, seharusnya %d", ok.Load(), slots)
	}
	if soldOut.Load() != buyers-slots {
		t.Fatalf("sold out = %d, seharusnya %d", soldOut.Load(), buyers-slots)
	}
	if left, _, _ := inv.Available(ctx, "21k"); left != 0 {
		t.Fatalf("sisa stok = %d, seharusnya 0", left)
	}
}

func TestReserve_LimitPerUserAndNotOpen(t *testing.T) {
	inv, _ := newInventory(t)
	ctx := context.Background()

	if _, err := reserve(inv, "5k", "budi", 1, 2, time.Now()); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("kategori belum dimuat seharusnya ErrNotOpen, dapat %v", err)
	}
	_, _ = inv.Load(ctx, "5k", 10)
	if _, err := reserve(inv, "5k", "budi", 2, 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := reserve(inv, "5k", "budi", 1, 2, time.Now()); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("seharusnya ErrLimitExceeded, dapat %v", err)
	}
	if _, err := reserve(inv, "5k", "budi", 0, 2, time.Now()); err == nil {
		t.Fatal("qty 0 seharusnya ditolak")
	}
}

func TestLoad_DoesNotOverwriteExistingStock(t *testing.T) {
	inv, _ := newInventory(t)
	ctx := context.Background()
	_, _ = inv.Load(ctx, "10k", 5)
	_, _ = reserve(inv, "10k", "budi", 1, 1, time.Now())
	loaded, _ := inv.Load(ctx, "10k", 5) // worker sinkron ulang
	if loaded {
		t.Fatal("Load tidak boleh menimpa stok yang sudah ada")
	}
	if left, _, _ := inv.Available(ctx, "10k"); left != 4 {
		t.Fatalf("sisa stok = %d, seharusnya 4", left)
	}
}

func TestRelease_ReturnsStockOnceAndFreesUserLimit(t *testing.T) {
	inv, _ := newInventory(t)
	ctx := context.Background()
	_, _ = inv.Load(ctx, "21k", 1)
	r, err := reserve(inv, "21k", "budi", 1, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	hold, found, err := inv.GetHold(ctx, "21k", r.ID)
	if err != nil || !found || hold.UserID != "budi" || hold.Qty != 1 {
		t.Fatalf("GetHold = %+v %v %v", hold, found, err)
	}
	for i := 0; i < 3; i++ { // dipanggil berulang: hanya sekali yang mengembalikan
		released, err := inv.Release(ctx, "21k", r.ID, hold)
		if err != nil {
			t.Fatal(err)
		}
		if released != (i == 0) {
			t.Fatalf("panggilan ke-%d released=%v", i+1, released)
		}
	}
	if left, _, _ := inv.Available(ctx, "21k"); left != 1 {
		t.Fatalf("sisa stok = %d, seharusnya 1", left)
	}
	if _, err := reserve(inv, "21k", "budi", 1, 1, time.Now()); err != nil {
		t.Fatalf("setelah release, user boleh reservasi lagi: %v", err)
	}
}

func TestConfirm_Cases(t *testing.T) {
	inv, _ := newInventory(t)
	ctx := context.Background()
	_, _ = inv.Load(ctx, "42k", 2)

	// Normal: hold aktif → dikunci, panggilan ulang aman.
	r1, _ := reserve(inv, "42k", "a", 1, 1, time.Now())
	if res, _ := inv.Confirm(ctx, "42k", r1.ID, "a", 1); res != ConfirmOK {
		t.Fatalf("confirm normal = %d", res)
	}
	if res, _ := inv.Confirm(ctx, "42k", r1.ID, "a", 1); res != ConfirmAlreadyLocked {
		t.Fatalf("confirm ulang = %d", res)
	}
	if _, active, _ := inv.HoldExpiry(ctx, "42k", r1.ID); active {
		t.Fatal("hold yang sudah dikonfirmasi tidak boleh aktif lagi")
	}

	// Bayar telat: hold sudah dikembalikan, stok masih ada → diambil ulang.
	r2, _ := reserve(inv, "42k", "b", 1, 1, time.Now())
	h2, _, _ := inv.GetHold(ctx, "42k", r2.ID)
	_, _ = inv.Release(ctx, "42k", r2.ID, h2)
	if res, _ := inv.Confirm(ctx, "42k", r2.ID, "b", 1); res != ConfirmReacquired {
		t.Fatalf("confirm telat = %d", res)
	}

	// Bayar telat & stok habis → wajib refund, stok tidak boleh minus.
	_, _ = inv.Load(ctx, "full", 1)
	r3, _ := reserve(inv, "full", "c", 1, 1, time.Now())
	h3, _, _ := inv.GetHold(ctx, "full", r3.ID)
	_, _ = inv.Release(ctx, "full", r3.ID, h3)
	_, _ = reserve(inv, "full", "d", 1, 1, time.Now()) // slot diambil orang lain
	if res, _ := inv.Confirm(ctx, "full", r3.ID, "c", 1); res != ConfirmNeedsRefund {
		t.Fatalf("confirm saat stok habis = %d", res)
	}
	if left, _, _ := inv.Available(ctx, "full"); left != 0 {
		t.Fatalf("stok = %d, seharusnya 0", left)
	}
}

func TestExpiredHolds(t *testing.T) {
	inv, _ := newInventory(t)
	ctx := context.Background()
	_, _ = inv.Load(ctx, "5k", 10)
	past := time.Now().Add(-time.Hour)
	old, _ := reserve(inv, "5k", "a", 1, 1, past) // kedaluwarsa 45 menit lalu
	_, _ = reserve(inv, "5k", "b", 1, 1, time.Now())

	ids, err := inv.ExpiredHolds(ctx, "5k", time.Now(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != old.ID {
		t.Fatalf("expired = %v, seharusnya [%s]", ids, old.ID)
	}
	cats, _ := inv.ActiveCategories(ctx)
	if len(cats) != 1 || cats[0] != "5k" {
		t.Fatalf("kategori aktif = %v", cats)
	}
}
