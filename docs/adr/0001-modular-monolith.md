# ADR 0001 · Modular monolith, bukan microservice

**Status:** diterima · 2026-10

## Konteks

Satu developer, proyek pribadi, tetapi harus tahan lonjakan war tiket. Microservice menambah banyak pekerjaan (banyak repo/pipeline, komunikasi antar layanan, konsistensi data lintas database) tanpa menyelesaikan masalah lonjakan itu sendiri.

## Keputusan

Satu codebase Go dengan batas modul tegas di `internal/` (checkout, payment, ticket, ledger, tenant, event, worker). Di-deploy sebagai dua proses dari binary yang sama: `api` dan `worker`. Bila perlu, `checkout` bisa dijalankan sebagai deployment terpisah yang diskalakan sendiri.

## Konsekuensi

- (+) Satu deploy, satu database, transaksi ACID biasa (order + outbox dalam satu transaksi).
- (+) Masalah lonjakan diselesaikan oleh Redis Lua + antrian + asinkron, bukan oleh pemecahan layanan.
- (−) Disiplin batas modul harus dijaga manual (tidak ada impor melingkar).
- Kapan dipecah: tim > ±20 engineer, atau satu modul butuh siklus rilis/skala yang sangat berbeda. Kandidat pertama: `checkout` dan `payout`.
