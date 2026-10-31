package checkout

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Kunci {cat:<id>}:user:<uid> punya TTL (penutupan penjualan + 7 hari) dan pembatas
// "slot per akun" tetap bekerja selama TTL itu.
func TestUserLimitKeyHasTTL(t *testing.T) {
	inv, mr := newInventory(t)
	ctx := context.Background()
	if _, err := inv.Load(ctx, "c1", 10); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	limitUntil := now.Add(48*time.Hour + UserLimitGrace)
	req := ReserveRequest{CategoryID: "c1", UserID: "u1", Qty: 1, MaxPerUser: 2, TTL: 15 * time.Minute, Now: now, LimitUntil: limitUntil}
	if _, err := inv.Reserve(ctx, req); err != nil {
		t.Fatal(err)
	}
	key := keysFor("c1").user("u1")
	want := limitUntil.Sub(now)
	if ttl := mr.TTL(key); ttl < want-time.Minute || ttl > want {
		t.Fatalf("TTL %v, mau ≈ %v", ttl, want)
	}

	// Reservasi berikutnya dengan batas lebih pendek tidak memperpendek TTL.
	short := req
	short.LimitUntil = now.Add(time.Hour)
	if _, err := inv.Reserve(ctx, short); err != nil {
		t.Fatal(err)
	}
	if ttl := mr.TTL(key); ttl < want-time.Minute {
		t.Fatalf("TTL diperpendek: %v", ttl)
	}

	// Selama TTL (8 hari kemudian, < 9 hari) pembatas tetap berlaku.
	mr.FastForward(8 * 24 * time.Hour)
	if _, err := inv.Reserve(ctx, req); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("pembatas harus tetap jalan selama TTL: %v", err)
	}
	if !mr.Exists(key) {
		t.Fatal("kunci hilang sebelum TTL habis")
	}
	mr.FastForward(2 * 24 * time.Hour)
	if mr.Exists(key) {
		t.Fatal("kunci harus kedaluwarsa setelah TTL")
	}

	// Tanpa LimitUntil: TTL cadangan 90 hari, bukan tanpa TTL.
	if _, err := inv.Reserve(ctx, ReserveRequest{CategoryID: "c1", UserID: "u2", Qty: 1, MaxPerUser: 1, TTL: time.Minute, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if ttl := mr.TTL(keysFor("c1").user("u2")); ttl <= 0 || ttl > userLimitFallback {
		t.Fatalf("TTL cadangan: %v", ttl)
	}

	// Bayar telat (hold sudah hilang, slot diambil ulang): kunci yang baru dibuat juga ber-TTL.
	if res, err := inv.Confirm(ctx, "c1", "rid-telat", "u3", 1); err != nil || res != ConfirmReacquired {
		t.Fatalf("confirm: %v %v", res, err)
	}
	if ttl := mr.TTL(keysFor("c1").user("u3")); ttl <= 0 {
		t.Fatalf("TTL setelah confirm: %v", ttl)
	}
}
