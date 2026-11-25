# Tugas 13 · Skala lanjutan: cari batas pertama, uji scale-out, audit multi-instance

> Prompt untuk AI coding agent (Claude Code CLI). Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/13-skala-lanjutan.md sampai semua kriteria selesai terpenuhi"`
> Prasyarat: tugas 01–11 sudah ter-commit. Tugas 12 (hosting) belum dikerjakan dan **tidak** disentuh di sini.

## Konteks

RaceGo: backend Go modular monolith (`backend/`, proses API dan worker terpisah), frontend Next.js 16 (`frontend/`), Postgres, Redis. Baca dulu `README.md`, `docs/02-arsitektur.md`, `docs/03-war-tiket.md`, `docs/06-infra-gratis.md`, `docs/08-development.md`, `docs/11-performa.md` (seluruhnya: metode, hasil, titik sempit, chaos, pengerasan stok), `loadtest/README.md`, dan **`frontend/AGENTS.md`**. **Baca kodenya langsung**: ringkasan di bawah dari ingatan dan bisa meleset; kode yang menang (catat bedanya di laporan).

Yang **sudah terukur** (jangan diulang tanpa alasan, jadikan titik awal): 5.000 pengguna berebut 500 tiket dengan antrean lulus semua ambang; tanpa antrean p95 reservasi 425 ms, dan titik sempitnya adalah **antre pool koneksi Postgres (`DB_MAX_CONNS`)**, bukan kueri lambat dan bukan Redis; `StockReconciler` dan pagar kuota di database sudah ada; soak 10 menit tanpa kebocoran. Semua angka itu dari **satu proses API** di **satu laptop** (k6, API, worker, Postgres, Redis berbagi mesin), satu eksekusi per konfigurasi, dengan derau antar-eksekusi sampai ±26%.

Yang **belum terukur dan belum diaudit** (isi tugas ini):

1. Di mana batas pertama bila beban dinaikkan di atas 5.000, dan apa yang jenuh lebih dulu.
2. Apakah **menjalankan lebih dari satu replika API** benar-benar menaikkan kapasitas, dan apakah kodenya **aman multi-instance** (pembatas laju, antrean virtual, ticker, cache, state di memori, pekerja latar).
3. Dampak **connection pooler** (PgBouncer) dan **cache untuk halaman baca publik** terhadap titik sempit yang sudah ditemukan.

## Aturan keras

- **Hanya infra uji lokal**: `docker-compose.loadtest.yml` (proyek `racego-loadtest`) dan skrip `loadtest/`. **Jangan** menyentuh data development, `.env`, `.env.obs`, stack produksi, atau layanan di luar `127.0.0.1`. Penjaga `loadgen-seed` (`APP_ENV=development`, database `*_loadtest`) tidak boleh dilonggarkan.
- **Tidak ada layanan berbayar, akun, atau target di luar `localhost`.** Pembayaran tetap mock.
- **Jangan commit, jangan push.** Biarkan perubahan di working tree; laporkan `git status` di akhir.
- **Satu eksekusi tidak cukup.** Setiap konfigurasi yang dibandingkan dijalankan **minimal 3 kali**, dilaporkan sebagai median dan rentang (min–maks). Selisih di dalam rentang derau **tidak boleh** dinyatakan sebagai perbaikan.
- **Jujur tentang batas alat ukur.** Catat CPU/RAM k6 pada tiap eksekusi. Bila k6 atau mesin jenuh lebih dulu daripada sistem yang diuji, tulis "batas generator beban/mesin", bukan batas RaceGo. Jangan menyimpulkan di luar yang diukur, dan jangan menulis klaim "tahan N pengguna" untuk produksi.
- **Jaga mesin.** Jalankan skenario berat satu per satu, beri jeda pendinginan di antaranya, hentikan bila terlihat throttling termal (catat), dan pastikan `make loadtest-clean` dijalankan di akhir. Jangan menyalakan Prometheus/Grafana lebih lama dari perlu.
- **Perubahan kode hanya bila terbukti perlu oleh data.** Setiap perubahan: kecil, punya tes, dan punya angka sebelum/sesudah (median dari ≥3 eksekusi). Semantik reservasi, ledger, pagar kuota, dan keamanan tidak boleh berubah. Bila sebuah ide tidak terbukti membantu, **jangan diterapkan**, tulis saja hasilnya.
- Semua dokumentasi dalam Bahasa Indonesia, istilah teknis seperlunya, tanpa data pribadi, token, atau kunci.

## Bagian A · Audit aman-multi-instance (kode, sebelum mengukur)

Sebelum menjalankan lebih dari satu replika, baca kodenya dan **buat tabel audit** (di dokumen hasil, bagian A): untuk setiap state atau pekerja latar, apakah aman dijalankan N salinan, dan bukti (berkas:baris). Minimal periksa:

- **Pembatas laju** (per IP / per akun): di memori proses atau di Redis? Bila di memori, batas efektif menjadi N× jumlah replika dan tidak konsisten antar replika; catat dampaknya dan apakah itu masalah.
- **Antrean virtual waiting room** (admit per detik, `QUEUE_MAX_ACTIVE`): siapa yang menjalankan ticker admit, di API atau worker? Aman bila ada N salinan (admit dobel melebihi laju yang ditetapkan)? Perlu pemimpin tunggal (kunci Redis/advisory lock)?
- **Worker**: outbox memakai `FOR UPDATE SKIP LOCKED` (periksa), `StockReconciler`, pengembali kuota, ledger, pengirim email: aman bila 2+ worker hidup? Reconciler yang berjalan dobel aman (idempoten, hanya menurunkan)?
- **Cache/state di memori** lain (cache sesi, kunci kuota, peta di handler, `sync.Map`, SSE/long-poll): apakah ada state yang harus dibagi antar instance?
- **Sesi dan CSRF/Turnstile/OTP**: lokasi state (Postgres vs memori).
- **Penugasan nomor** (BIB, nomor order, jurnal): hanya lewat database dengan kunci yang benar?
- **Kesehatan dan shutdown**: `/healthz`, graceful shutdown (penting untuk rolling restart di belakang load balancer).

Untuk setiap temuan yang **tidak aman**: tulis tes yang **mereproduksinya** (dua instance/proses, atau dua goroutine yang meniru dua instance), perbaiki seminimal mungkin, dan buktikan tes hijau setelahnya. Temuan yang tidak diperbaiki (karena di luar cakupan) dicatat jelas sebagai batasan, bukan disembunyikan.

## Bagian B · Peta batas pertama (satu instance)

Dengan infra uji, API+worker seperti Tugas 09 (`make loadtest`, bantu pakai `make obs-up` bila perlu metrik). Kumpulkan tabel dengan **median dan rentang dari ≥3 eksekusi** per baris:

| Skenario | Ukuran | Catatan |
| --- | --- | --- |
| `war` (antrean) | 5.000, 10.000, 20.000 | naikkan sampai ambang gagal atau generator/mesin jenuh; kuota tetap 500 |
| `war-noqueue` | 2.000, 5.000, 10.000 | `DB_MAX_CONNS` 20 (bawaan), 50, 100 |
| `browse` | 50, 200, 500, 1.000 VU | baca-berat; ini yang dominan saat tiket baru dibuka |

Untuk **tiap baris** catat: throughput, p50/p95/p99 reservasi, `queue/me`, order, 5xx, hasil integritas, serta sumber daya (CPU API/Postgres/Redis/k6, RSS, goroutine API, koneksi Postgres aktif vs `max_connections`, `racego_db_pool_wait_total` dan lama tunggu, ops Redis, `evalsha` p95). Tentukan **batas pertama**: ambang SLO pertama yang gagal dan sumber daya pertama yang jenuh, beserta buktinya (metrik, bukan dugaan). Pisahkan tegas: *batas sistem* vs *batas generator beban/mesin*. Bila `evalsha` p95 ≤100 ms di docs 11 masih berupa dugaan, selesaikan: buktikan apakah itu antrean klien Redis (pool), penjadwalan goroutine saat API jenuh, atau Redis, dengan satu eksperimen terpisah.

## Bagian C · Uji scale-out API (inti tugas)

Tambahkan (di berkas **uji saja**, mis. `docker-compose.loadtest.scale.yml` dan/atau `loadtest/run.sh`) kemampuan menjalankan **N replika API** di belakang **satu load balancer** (Caddy atau nginx, di localhost), dengan N = 1, 2, 4, dan 1 worker (lalu satu eksperimen dengan 2 worker, sesuai hasil Bagian A). Syarat agar perbandingan adil:

- **Setiap replika dibatasi sumber dayanya sama** (mis. `--cpus` / limit container yang sama), supaya penambahan replika benar-benar menambah kapasitas dan bukan sekadar berebut core yang sama. Catat berapa core yang dipakai k6, LB, Postgres, Redis, dan replika; bila total melampaui core mesin, katakan bahwa hasil scale-out **dibatasi mesin** dan pakai pembatasan CPU yang membuat perbandingan masih bermakna.
- `TRUSTED_PROXY_HOPS` dan header `X-Forwarded-For` disesuaikan agar pembatas laju per IP tetap meniru kondisi nyata; jelaskan penyesuaiannya.
- Total koneksi Postgres semua replika (`N × DB_MAX_CONNS` + worker) **tetap di bawah `max_connections`**; catat nilainya per konfigurasi.
- Skenario: `war-noqueue` pada ukuran yang di Bagian B menjadi batas (supaya terlihat apakah replika memindahkan batasnya) dan `war` dengan antrean pada ukuran terbesar yang lulus.

Laporkan: p95 reservasi/order dan throughput terhadap N (median + rentang, ≥3 eksekusi), **apakah skala mendekati linear**, di mana ia berhenti naik, dan **sumber daya mana yang jenuh berikutnya** (kemungkinan Postgres). Periksa juga **perilaku antrean virtual dan pembatas laju** dengan N>1 (temuan Bagian A dibuktikan dengan angka) dan **integritas penuh** (`loadgen-check` a–g) pada semua konfigurasi N. Satu pelanggaran integritas = berhenti dan laporkan, bukan diabaikan.

Sertakan uji **rolling restart**: matikan satu replika saat `war` berjalan; ukur error yang dialami pengguna dan apakah klien dengan backoff pulih (gunakan graceful shutdown yang sudah ada; catat bila perlu penyesuaian LB seperti health check).

## Bagian D · Dua intervensi, hanya bila terbukti

Uji masing-masing sebagai eksperimen terisolasi (≥3 eksekusi, pada konfigurasi titik sempit dari Bagian B). Terapkan ke kode/konfigurasi utama **hanya bila** terbukti memperbaiki di luar derau **dan** tidak menurunkan keamanan/kebenaran.

1. **Connection pooler (PgBouncer, mode transaction)** di infra uji. Periksa kompatibilitas driver (prepared statement/`pgx`, advisory lock, `SET`, transaksi `FOR UPDATE` dan `SKIP LOCKED`) **sebelum** mengukur; bila ada fitur yang rusak di mode transaction, catat dan jangan memaksakan. Ukur: waktu tunggu pool dan p95 reservasi di `war-noqueue`, dengan N=1 dan N=4.
2. **Cache untuk halaman baca publik** (daftar event, detail event, kalender). Pilih yang paling sederhana dan benar (header `Cache-Control` + cache LB, atau cache Redis TTL pendek di API). **Aturan kebenaran:** data stok/ketersediaan yang menentukan tombol beli tidak boleh membuat pembeli salah informasi secara berbahaya; bila ketersediaan ikut di-cache, tampilannya harus bersifat "perkiraan" dan reservasi tetap divalidasi server (sudah begitu; buktikan lewat tes). Ukur skenario `browse` dan `war-noqueue` (pembaca berat) sebelum/sesudah. Jangan cache respons ber-auth atau yang berisi data pribadi (tes).

Bila salah satu tidak membantu, hasilnya tetap ditulis lengkap ("diuji, tidak terbukti, tidak diterapkan") dan tidak ada perubahan kode untuk itu.

## Bagian E · Dokumentasi

- Tulis **`docs/13-skala-lanjutan.md`** (Bahasa Indonesia): tujuan, metode (sama dengan docs 11, sebut perbedaannya), tabel audit multi-instance (A), peta batas pertama (B), hasil scale-out (C), hasil intervensi (D), **rekomendasi berurutan** (apa yang dilakukan lebih dulu bila trafik naik, dengan bukti), dan **batasan yang jujur** (satu laptop, generator berbagi mesin, derau, Midtrans sandbox bukan produksi, tidak diuji di infra nyata). Tambahkan ringkasan dan tautan di `docs/11-performa.md`, dan tautan dari `docs/README.md` serta `docs/09-roadmap.md` (pindahkan butir yang kini terjawab, jangan menghapus yang masih terbuka).
- Perbarui `loadtest/README.md` untuk skenario dan parameter baru (N replika, LB, PgBouncer bila jadi dipertahankan).
- **Jangan menambah klaim di `README.md` utama** yang tidak terbukti; bila menambah satu baris hasil, harus sama dengan angka di docs 13 dan menyebut kondisi pengukurannya (satu laptop).

## Kriteria selesai

1. Tabel audit multi-instance lengkap dengan bukti berkas:baris; setiap temuan tidak aman punya tes yang mereproduksi dan perbaikannya hijau, atau dicatat sebagai batasan yang dijelaskan.
2. Peta batas pertama (B) terisi dari ≥3 eksekusi per baris; batas pertama tiap skenario dinyatakan dengan bukti metrik, dan batas sistem dipisahkan dari batas generator/mesin.
3. Uji scale-out (C) untuk N = 1, 2, 4 selesai dengan perbandingan adil (batas sumber daya per replika), integritas penuh lulus di semua konfigurasi, dan uji rolling restart dilaporkan.
4. Dua intervensi (D) diuji; yang diterapkan punya tes dan angka sebelum/sesudah di luar derau; yang tidak terbukti **tidak** diterapkan dan hasilnya dicatat.
5. `make test` hijau (backend dan lint/tipe frontend), tes lama tidak diubah kecuali dengan alasan tertulis.
6. Dokumentasi (E) selesai, konsisten dengan angka; tanpa klaim di luar yang diukur.
7. Tidak ada data development, `.env`, atau stack produksi yang tersentuh; `make loadtest-clean` dijalankan; tidak ada proses/container uji yang tertinggal (`docker ps`, `ps`).
8. `git status` hanya berisi perubahan yang disengaja (laporkan daftarnya); tidak ada commit/push.

## Laporan akhir (tempel ke percakapan)

1. Ringkasan satu paragraf: batas pertama RaceGo pada satu instance, dan seberapa jauh scale-out menggesernya.
2. Tabel: skenario/konfigurasi, median (rentang) throughput dan p95, 5xx, integritas.
3. Temuan audit multi-instance dan apa yang diperbaiki.
4. Intervensi yang diterapkan vs yang tidak terbukti.
5. Rekomendasi berurutan (maksimal 5) dan batasan jujur.
6. Daftar berkas yang berubah, hasil `make test`, bukti pembersihan (`docker ps`), dan `git status`.
