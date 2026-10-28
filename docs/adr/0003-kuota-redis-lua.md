# ADR 0003 · Kuota di Redis dengan skrip Lua

**Status:** diterima · 2026-10

## Konteks

Saat war tiket ribuan request memperebutkan baris kuota yang sama. Mengunci baris di Postgres (`SELECT … FOR UPDATE`) membuat semua request antre di satu baris dan koneksi DB habis.

## Keputusan

Stok tiap kategori disimpan di Redis. Reservasi, pengembalian, dan konfirmasi masing-masing satu skrip Lua (`checkout/lua/*.lua`) yang dijalankan Redis secara atomik. Postgres tetap sumber kebenaran untuk order dan tiket; Redis bisa dibangun ulang dari Postgres (`SyncCategory`).

## Konsekuensi

- (+) Nol oversell tanpa lock database; satu operasi Redis hanya beberapa mikrodetik.
- (+) Batas per akun, hold ber-timer, dan idempotensi konfirmasi ada di satu tempat.
- (−) Dua penyimpanan harus konsisten: diatasi dengan hold ber-indeks ZSET, job pengembalian, `confirm` yang menangani bayar telat, dan sinkron ulang `SETNX`.
- Redis wajib `appendonly yes` + `noeviction`.
