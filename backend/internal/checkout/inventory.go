// Package checkout menangani jalur panas war tiket: kuota di Redis, reservasi, dan order.
package checkout

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	//go:embed lua/reserve.lua
	reserveSrc string
	//go:embed lua/release.lua
	releaseSrc string
	//go:embed lua/confirm.lua
	confirmSrc string

	reserveScript = redis.NewScript(reserveSrc)
	releaseScript = redis.NewScript(releaseSrc)
	confirmScript = redis.NewScript(confirmSrc)
)

var (
	ErrSoldOut       = errors.New("kuota habis")
	ErrLimitExceeded = errors.New("melebihi batas slot per akun")
	ErrNotOpen       = errors.New("penjualan belum dibuka")
)

// ConfirmResult adalah hasil mengunci slot setelah pembayaran lunas.
type ConfirmResult int

const (
	ConfirmNeedsRefund   ConfirmResult = 0 // hold kedaluwarsa & stok habis
	ConfirmOK            ConfirmResult = 1
	ConfirmReacquired    ConfirmResult = 2 // bayar telat tapi stok masih ada
	ConfirmAlreadyLocked ConfirmResult = 3
)

const (
	activeCategoriesKey = "inventory:categories"
	holdKeyGrace        = time.Hour           // detail hold disimpan lebih lama dari hold-nya
	confirmedMarkerTTL  = 90 * 24 * time.Hour // penanda konfirmasi (idempotensi)
)

// Inventory menyimpan stok tiap kategori di Redis.
//
// Semua key satu kategori memakai hash tag {cat:<id>} sehingga di Redis Cluster
// berada di slot yang sama dan bisa dipakai bersama dalam satu skrip Lua.
type Inventory struct {
	rdb *redis.Client
}

func NewInventory(rdb *redis.Client) *Inventory { return &Inventory{rdb: rdb} }

type catKeys struct{ base string }

func keysFor(categoryID string) catKeys       { return catKeys{base: "{cat:" + categoryID + "}"} }
func (k catKeys) stock() string               { return k.base + ":stock" }
func (k catKeys) holds() string               { return k.base + ":holds" }
func (k catKeys) user(userID string) string   { return k.base + ":user:" + userID }
func (k catKeys) hold(rid string) string      { return k.base + ":hold:" + rid }
func (k catKeys) confirmed(rid string) string { return k.base + ":confirmed:" + rid }

// Load memuat stok awal bila belum ada (SETNX), lalu mendaftarkan kategori untuk dipantau worker.
// Mengembalikan true bila stok baru saja dimuat.
func (inv *Inventory) Load(ctx context.Context, categoryID string, available int) (bool, error) {
	if available < 0 {
		available = 0
	}
	ok, err := inv.rdb.SetNX(ctx, keysFor(categoryID).stock(), available, 0).Result()
	if err != nil {
		return false, err
	}
	if err := inv.rdb.SAdd(ctx, activeCategoriesKey, categoryID).Err(); err != nil {
		return false, err
	}
	return ok, nil
}

// Available mengembalikan stok tersisa; ok=false bila kategori belum dimuat ke Redis.
func (inv *Inventory) Available(ctx context.Context, categoryID string) (int, bool, error) {
	v, err := inv.rdb.Get(ctx, keysFor(categoryID).stock()).Int()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}

type ReserveRequest struct {
	CategoryID string
	UserID     string
	Qty        int
	MaxPerUser int
	TTL        time.Duration
	Now        time.Time
}

type Reservation struct {
	ID         string    `json:"reservation_id"`
	CategoryID string    `json:"category_id"`
	Qty        int       `json:"qty"`
	ExpiresAt  time.Time `json:"expires_at"`
	Remaining  int       `json:"remaining"`
}

// Reserve memotong stok secara atomik dan membuat hold sementara.
func (inv *Inventory) Reserve(ctx context.Context, req ReserveRequest) (Reservation, error) {
	if req.Qty <= 0 {
		return Reservation{}, fmt.Errorf("qty harus > 0")
	}
	if req.Now.IsZero() {
		req.Now = time.Now()
	}
	k := keysFor(req.CategoryID)
	rid := uuid.NewString()
	expiresAt := req.Now.Add(req.TTL)
	holdTTL := int64((req.TTL + holdKeyGrace).Seconds())

	res, err := reserveScript.Run(ctx, inv.rdb,
		[]string{k.stock(), k.user(req.UserID), k.holds(), k.hold(rid)},
		req.Qty, req.MaxPerUser, expiresAt.Unix(), rid, req.UserID+"|"+strconv.Itoa(req.Qty), holdTTL,
	).Int()
	if err != nil {
		return Reservation{}, err
	}
	switch res {
	case -1:
		return Reservation{}, ErrSoldOut
	case -2:
		return Reservation{}, ErrLimitExceeded
	case -3:
		return Reservation{}, ErrNotOpen
	}
	return Reservation{ID: rid, CategoryID: req.CategoryID, Qty: req.Qty, ExpiresAt: expiresAt, Remaining: res}, nil
}

// Hold adalah detail reservasi yang tersimpan di Redis.
type Hold struct {
	UserID string
	Qty    int
}

// GetHold membaca detail hold; ok=false bila sudah tidak ada.
func (inv *Inventory) GetHold(ctx context.Context, categoryID, rid string) (Hold, bool, error) {
	v, err := inv.rdb.Get(ctx, keysFor(categoryID).hold(rid)).Result()
	if errors.Is(err, redis.Nil) {
		return Hold{}, false, nil
	}
	if err != nil {
		return Hold{}, false, err
	}
	uid, qtyStr, found := strings.Cut(v, "|")
	qty, convErr := strconv.Atoi(qtyStr)
	if !found || convErr != nil {
		return Hold{}, false, fmt.Errorf("format hold rusak: %q", v)
	}
	return Hold{UserID: uid, Qty: qty}, true, nil
}

// HoldExpiry mengembalikan waktu kedaluwarsa hold; ok=false bila hold sudah tidak aktif
// (sudah dikembalikan atau sudah dikonfirmasi).
func (inv *Inventory) HoldExpiry(ctx context.Context, categoryID, rid string) (time.Time, bool, error) {
	score, err := inv.rdb.ZScore(ctx, keysFor(categoryID).holds(), rid).Result()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.Unix(int64(score), 0), true, nil
}

// Release mengembalikan slot ke stok. Aman dipanggil berulang kali.
func (inv *Inventory) Release(ctx context.Context, categoryID, rid string, h Hold) (bool, error) {
	k := keysFor(categoryID)
	n, err := releaseScript.Run(ctx, inv.rdb,
		[]string{k.stock(), k.user(h.UserID), k.holds(), k.hold(rid)}, rid, h.Qty).Int()
	return n == 1, err
}

// Confirm mengunci slot setelah pembayaran lunas. Aman dipanggil berulang kali.
func (inv *Inventory) Confirm(ctx context.Context, categoryID, rid, userID string, qty int) (ConfirmResult, error) {
	k := keysFor(categoryID)
	n, err := confirmScript.Run(ctx, inv.rdb,
		[]string{k.stock(), k.user(userID), k.holds(), k.hold(rid), k.confirmed(rid)},
		rid, qty, int64(confirmedMarkerTTL.Seconds())).Int()
	return ConfirmResult(n), err
}

// ExpiredHolds mengembalikan id reservasi yang sudah lewat waktu.
func (inv *Inventory) ExpiredHolds(ctx context.Context, categoryID string, now time.Time, limit int64) ([]string, error) {
	return inv.rdb.ZRangeByScore(ctx, keysFor(categoryID).holds(), &redis.ZRangeBy{
		Min: "-inf", Max: strconv.FormatInt(now.Unix(), 10), Count: limit,
	}).Result()
}

// ActiveCategories mengembalikan semua kategori yang stoknya dikelola di Redis.
func (inv *Inventory) ActiveCategories(ctx context.Context) ([]string, error) {
	return inv.rdb.SMembers(ctx, activeCategoriesKey).Result()
}
