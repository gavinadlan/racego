package checkout

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Redis di-kill dengan AOF everysec kehilangan pengurangan stok terakhir: stok di Redis jadi lebih besar
// daripada kuota − slot terpakai. Penjepit harus menurunkannya, tidak pernah menaikkan, dan tidak
// bertindak pada kelebihan sesaat (kenaikan kuota yang sedang berjalan).
func TestStockReconcilerClampsAfterLostWrites(t *testing.T) {
	inv, mr := newInventory(t)
	ctx := context.Background()
	inv.Load(ctx, "c", 10) //nolint:errcheck
	now := time.Now()
	for i := 0; i < 4; i++ {
		if _, err := reserve(inv, "c", fmt.Sprint("u", i), 1, 1, now); err != nil {
			t.Fatal(err)
		}
	}
	cap := 6 // kuota 10 − 4 order aktif (angka dari database)
	r := &StockReconciler{inv: inv, suspect: map[string]bool{}, caps: func(context.Context, []string) (map[string]int, error) {
		return map[string]int{"c": cap}, nil
	}}

	// Normal: stok = batas → tidak ada yang berubah, tidak ada tersangka.
	if n, err := r.Run(ctx); err != nil || n != 0 {
		t.Fatalf("stok sesuai: n=%d err=%v", n, err)
	}

	// Redis kehilangan 3 pengurangan stok → stok 9, padahal batas 6.
	mr.Set("{cat:c}:stock", "9")
	if n, _ := r.Run(ctx); n != 0 {
		t.Fatalf("putaran pertama hanya menandai, dapat dijepit %d", n)
	}
	if s, _, _ := inv.Available(ctx, "c"); s != 9 {
		t.Fatalf("putaran pertama tidak boleh mengubah stok, dapat %d", s)
	}
	if n, err := r.Run(ctx); err != nil || n != 3 {
		t.Fatalf("putaran kedua harus menurunkan 3 slot, dapat n=%d err=%v", n, err)
	}
	if s, _, _ := inv.Available(ctx, "c"); s != 6 {
		t.Fatalf("stok sesudah dijepit = %d, harus 6", s)
	}
	// Sesudah dijepit: tepat 6 slot lagi bisa dijual, yang ke-7 habis (tanpa penjepit: 9 → oversell 3).
	for i := 0; i < 6; i++ {
		if _, err := reserve(inv, "c", fmt.Sprint("v", i), 1, 1, now); err != nil {
			t.Fatalf("slot %d: %v", i, err)
		}
	}
	if _, err := reserve(inv, "c", "v9", 1, 1, now); !errors.Is(err, ErrSoldOut) {
		t.Fatalf("slot ke-7 harus habis, dapat %v", err)
	}
}

func TestStockReconcilerIgnoresTransientAndNeverRaises(t *testing.T) {
	inv, _ := newInventory(t)
	ctx := context.Background()
	inv.Load(ctx, "c", 5) //nolint:errcheck
	cap := 3
	r := &StockReconciler{inv: inv, suspect: map[string]bool{}, caps: func(context.Context, []string) (map[string]int, error) {
		return map[string]int{"c": cap}, nil
	}}
	// Kenaikan kuota sedang berjalan: stok sudah 5, database masih menghitung batas 3 → tersangka.
	r.Run(ctx) //nolint:errcheck
	cap = 5    // commit database tiba sebelum putaran kedua → kelebihan hilang
	if n, _ := r.Run(ctx); n != 0 {
		t.Fatalf("kelebihan sesaat tidak boleh dijepit, dapat %d", n)
	}
	if s, _, _ := inv.Available(ctx, "c"); s != 5 {
		t.Fatalf("stok berubah jadi %d", s)
	}
	if r.suspect["c"] {
		t.Fatal("tanda tersangka harus dihapus begitu kelebihan hilang")
	}
	// Batas jauh di atas stok: tidak pernah dinaikkan.
	cap = 100
	for i := 0; i < 3; i++ {
		r.Run(ctx) //nolint:errcheck
	}
	if s, _, _ := inv.Available(ctx, "c"); s != 5 {
		t.Fatalf("penjepit tidak boleh menaikkan stok, dapat %d", s)
	}
	// Batas negatif (data aneh) → stok 0, bukan negatif.
	cap = -2
	r.Run(ctx) //nolint:errcheck
	r.Run(ctx) //nolint:errcheck
	if s, _, _ := inv.Available(ctx, "c"); s != 0 {
		t.Fatalf("batas negatif harus menjepit ke 0, dapat %d", s)
	}
	// Kategori belum dimuat: tidak dibuat dari ketiadaan.
	if _, clamped, err := inv.ClampStock(ctx, "belum", 1); err != nil || clamped {
		t.Fatalf("kategori belum dimuat: clamped=%v err=%v", clamped, err)
	}
}
