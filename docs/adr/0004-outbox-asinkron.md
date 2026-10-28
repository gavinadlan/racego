# ADR 0004 · Transactional outbox di Postgres, bukan message broker

**Status:** diterima · 2026-10

## Konteks

Setelah pembayaran lunas ada beberapa langkah lanjutan (kunci slot, terbitkan tiket, ledger, email). Webhook Midtrans harus dibalas cepat (< 5 detik) dan pesan tidak boleh hilang walau proses restart. Infra harus gratis.

## Keputusan

Webhook menulis perubahan status order **dan** pesan ke tabel `outbox` dalam satu transaksi. Worker mengambil pesan dengan `FOR UPDATE SKIP LOCKED`, memproses setiap pesan dalam transaksinya sendiri, dan mencoba ulang dengan backoff eksponensial (maks. 10 kali).

## Konsekuensi

- (+) Tidak ada pesan hilang dan tidak ada "order lunas tapi event tidak terkirim".
- (+) Tanpa komponen tambahan; beberapa instance worker aman berjalan bersamaan.
- (−) Throughput dibatasi Postgres — lebih dari cukup untuk ribuan order per menit. Bila kelak kurang, relay outbox bisa mengirim ke Pub/Sub/NATS tanpa mengubah penulis pesan.
- Semua handler pesan wajib idempotent.
