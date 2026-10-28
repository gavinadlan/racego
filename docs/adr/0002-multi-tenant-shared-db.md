# ADR 0002 · Multi-tenant dengan satu database bersama

**Status:** diterima · 2026-10

## Konteks

Banyak mitra memakai platform yang sama. Pilihan: database per mitra, schema per mitra, atau satu database dengan kolom `org_id`. Pelari bersifat global — satu akun membeli di event mitra mana pun.

## Keputusan

Satu database, kolom `org_id` di setiap tabel milik mitra. Isolasi ditegakkan oleh:

1. Route mitra hanya di `/v1/org/{org}/…`, melewati `tenant.RequireRole` (slug → `org_id` + cek peran).
2. Handler memakai `org_id` dari context, tidak pernah dari body request.
3. Bukan anggota → 404 agar keberadaan mitra lain tidak bocor.

## Konsekuensi

- (+) Paling murah dioperasikan; satu migrasi untuk semua; laporan lintas mitra mudah.
- (+) Fitur "daftar sekali, dipakai di semua event" alami.
- (−) Satu query yang lupa filter `org_id` bisa membocorkan data → **rencana**: Row-Level Security Postgres (`SET app.org_id` per transaksi + policy `org_id = current_setting('app.org_id')::uuid`) sebagai jaring pengaman, plus tes otomatis lintas tenant.
- Mitra enterprise yang mensyaratkan data terpisah bisa ditawari database terpisah sebagai paket khusus nanti.
