# 13 · Skala lanjutan: batas pertama, scale-out, dan audit multi-instance

Lanjutan [11-performa.md](11-performa.md). Tiga pertanyaan: di mana batas pertama di atas 5.000 pengguna, apakah beberapa replika API benar-benar menaikkan kapasitas (dan aman), dan apakah PgBouncer serta cache baca publik membantu. Semua angka dari **satu laptop**; bukan jaminan produksi dan bukan klaim "tahan N pengguna".

## Ringkasan

| Pertanyaan | Jawaban terukur |
| --- | --- |
| Batas pertama satu instance | `war-noqueue` 5.000: p95 reservasi di sekitar/di atas ambang 300 ms pada `DB_MAX_CONNS` 20, 50, dan 100 (median 342–367 ms). Antre terlihat di pool Postgres **dan** pool Redis proses API. Dengan antrean, 10.000 lulus; 20.000 berhenti di **batas generator** |
| Scale-out (0,5 core per replika) | p95 reservasi `war-noqueue` 5.000: N=1 **421** (406–480), N=2 **184** (178–247), N=4 **131** (110–133) ms; puncak 2.997, 4.193, 5.334 req/dtk. Membaik, **tidak linear** |
| Integritas | Lulus di semua eksekusi yang diperiksa (N=1, 2, 4, dua worker, replika dimatikan) |
| Audit multi-instance | Dua temuan tidak aman, keduanya diperbaiki dengan tes; sisanya aman atau dicatat sebagai batasan |
| PgBouncer | Kompatibel, p95 **tidak** terbukti lebih baik. Tidak diterapkan |
| Cache baca di API | Memangkas CPU dan pembacaan Postgres di `browse`, tetapi p95 reservasi di titik sempit **tidak** membaik (malah lebih tinggi). Tidak diterapkan |

## Metode dan bedanya dengan docs 11

Skenario, klien, ambang (SLO), penjaga data, dan `loadgen-check` a–g sama dengan [docs 11](11-performa.md#metode). Yang berbeda:

- **Minimal 3 eksekusi** untuk setiap konfigurasi yang dibandingkan, ditulis `median (min–maks)`. Selisih yang rentangnya beririsan tidak disebut perbaikan. Baris bertanda `n=1` hanya menandai lulus/gagal yang jauh dari ambang.
- Infra uji dibuat ulang (volume baru) sebelum tiap eksekusi, satu skenario pada satu waktu, dengan jeda 15 detik.
- `loadtest/stats.py` mencuplik tiap ±2 detik: CPU/RAM semua container dan proses, CPU idle mesin, pool Postgres dan Redis, `pg_stat_activity`, status daya, status termal. Puncak CPU berarti cuplikan tertinggi; lonjakan lebih pendek dari 2 detik bisa terlewat. 100% = satu core.
- Mesin sama (Apple M2, 8 core, 16 GB, OrbStack 8 CPU). `max_connections` Postgres uji 200. Bagian B memakai API dan worker native seperti docs 11; Bagian C memakai container Linux berbatas CPU, jadi **angka B dan C tidak dibandingkan langsung**.
- Kuantil `evalsha` dibaca dari bucket histogram: "≤ x" adalah batas atas bucket.
- Perbaikan audit di bawah sudah terpasang di semua eksekusi.

**Gangguan yang diketahui.** Tiga eksekusi C pertama dibuang: `docker compose` membaca `.env` di root sehingga `RESERVATION_TTL=15m` terpakai; `run.sh` kini memakai `--env-file /dev/null`. Sejak itu tidak ada nilai `.env` development yang terbaca. Satu eksekusi PgBouncer gagal sebelum beban dimulai (API menyala sebelum PgBouncer siap); urutan startup dibetulkan dan eksekusi itu diulang. Sebagian eksekusi berjalan dengan baterai (B, C ronde 1, cuplikan akhir D dan eksperimen `evalsha`), sebagian dengan listrik; status daya tercatat per eksekusi. Tidak ada throttling termal yang dilaporkan `pmset`. Ini bagian dari derau antar-eksekusi.

## A. Audit aman-multi-instance

Nomor baris mengacu pada kode saat dokumen ini ditulis, relatif terhadap `backend/`.

| State / pekerja | Aman N salinan? | Bukti |
| --- | --- | --- |
| Pembatas laju (akun, IP, OTP, undangan, unduhan) | Aman: penghitung `INCR` di Redis, batas tetap global | `internal/ratelimit/ratelimit.go:20`; angka N=4 di bawah |
| Ticker panggil antrean | **Tidak aman (diperbaiki)**: jalan di tiap worker tiap detik. Skrip Lua atomik menjaga jatah, tetapi `last_ms` bisa mundur bila jam worker berbeda, sehingga waktu dihitung dua kali | `internal/worker/worker.go:406`, `internal/queue/lua/admit.lua:25` |
| `StockReconciler` | **Tidak aman (diperbaiki)**: worker lain mengembalikan slot di antara pembacaan batas dan stok, lalu slot sah itu dijepit | `internal/checkout/reconcile.go:70` |
| Outbox | Aman: `FOR UPDATE SKIP LOCKED`. Pesan yang gagal bisa dicoba sekali lagi oleh worker lain sebelum backoff tercatat; handler idempoten | `internal/worker/worker.go:153` |
| Kedaluwarsa order, pengembalian hold | Aman: `UPDATE` bersyarat dan `ZREM` atomik | `internal/worker/worker.go:468`, `:501` |
| Pencairan, refund, rilis saldo | Aman: `SKIP LOCKED`, sewa 2 menit, cek status di bawah kunci, jurnal unik | `internal/finance/payout.go:521`, `internal/finance/refund.go:460`, `internal/finance/release.go:50` |
| Ledger | Aman: `ON CONFLICT (ref_type, ref_id) DO NOTHING` | `internal/ledger/ledger.go:114` |
| Email | Minimal sekali kirim (bukan tepat sekali), sama dengan satu worker | outbox di atas |
| `RestoreUserLimits` | **Batasan**: foto database dibaca, lalu hitungan dinaikkan. Bila worker lain mengembalikan slot di antaranya, hitungan pembeli itu bisa terlalu tinggi sampai TTL. Arah kesalahannya ketat (pembeli tertolak), bukan melonggarkan batas | `internal/checkout/userlimits.go:22` |
| Nomor BIB | Aman: `UPDATE ... bib_next + 1` mengunci baris kategori | `internal/ticket/service.go:96` |
| Nomor order | Aman: UUID | `internal/checkout/handler.go:327` |
| Undian | Aman: `pg_advisory_xact_lock` (berlaku per transaksi) | `internal/ticket/attend.go:66` |
| Sesi login | Aman: tabel Postgres | `internal/auth/session.go:43` |
| OTP | Penyimpanan di Redis, aman dibagi. Catatan lama (bukan soal multi-instance): kode dibandingkan sebelum percobaan dihitung, jadi tebakan serentak bisa melewati batas percobaan | `internal/auth/otp.go:104` |
| CSRF, Turnstile | Aman: tanpa state (cek `Origin`; verifikasi ke penyedia) | `internal/auth/auth.go:33` |
| Cache kategori dan event antrean (3 dtk) | Aman: kebasian terbatas 3 dtk, reservasi divalidasi Lua | `internal/checkout/handler.go:48`, `internal/queue/handler.go:27` |
| Daftar event `Runner` (10 dtk) | Aman: hanya menunda event baru terlihat | `internal/queue/handler.go:287` |
| `lastCheck` status Midtrans (peta di memori) | Aman, tetapi batas frekuensi menjadi N× | `internal/payment/status.go:32`, `:85` |
| SSE / long-poll | Tidak ada | - |
| Unggahan dan dokumen KYC di disk | **Batasan**: antar-host butuh volume bersama; tanpa itu replika lain menjawab 404 | `internal/config/config.go:159`, `:161` |
| Kunci `QR_PRIVATE_KEY`, `QUEUE_SECRET`, `KYC_ENC_KEY` | Harus sama di semua replika | konfigurasi |
| `migrate up` | **Batasan**: tanpa kunci, jangan dijalankan bersamaan | `cmd/migrate/main.go:105` |
| `/healthz` | **Batasan**: ping lewat pool yang sama dengan permintaan. Pool jenuh bisa membuat LB mengeluarkan semua replika sekaligus; pakai ambang gagal yang longgar | `internal/server/router.go:94` |
| Shutdown | Graceful 10 dtk; batas waktu permintaan bisa 15 dtk dan tidak ada masa drain readiness. Uji tidak menunjukkan error (bagian C) | `cmd/api/main.go:115` |
| Metrik bisnis dari database | Terbit di tiap worker; pakai `max`, jangan `sum` | `internal/metrics/metrics.go` (`RegisterDB`) |

### Yang diperbaiki

| Temuan | Tes (merah sebelum, hijau sesudah) | Perbaikan |
| --- | --- | --- |
| Beda jam antar worker: laju panggil 10 detik menjadi 260/280, batas tes 240 (3 worker, ±10 dtk) | `TestAdmitRateSharedByWorkers`; `TestAdmitConcurrentWorkersNoDoubleCall` menjaga tanpa pemanggilan ganda | `last_ms` tidak pernah mundur; selisih > 30 dtk diabaikan supaya satu jam yang ngawur tidak menghentikan antrean |
| Reconciler menjepit 1 slot sah (undersell, bukan oversell) | `TestStockReconcilerOtherWorkerReleasesMidRound` | Sesaat sebelum menjepit, batas dihitung ulang dan diambil yang terbesar |

Keduanya benar dengan satu worker berjam normal; perbaikan ini diperlukan sebelum menambah worker. Semantik reservasi, pagar kuota, dan ledger tidak berubah, tes lama tidak diubah.

### Bukti angka dengan N > 1

- **Pembatas laju, N=4** (3 kali, hasil identik, beban tersebar ±95 permintaan per replika): 25 `join` satu akun serentak menghasilkan 20 diterima dan 5 ditolak (batas 20); 340 `join` dari 340 akun di satu IP menghasilkan 300 diterima dan 40 ditolak (batas 300); 20 `queue/me` serentak menghasilkan 1 diterima. Bila pembatas per proses, N=4 akan menerima sampai 4×.
- **Laju panggil, batas 20/dtk**, jendela ±10 dtk dari cuplikan: 1 worker 19,9 / 21,8 / 21,6 per detik; 2 worker 19,8 / 19,8 / 21,9. Tidak berlipat. Total dipanggil 700–740 di semua eksekusi (2 worker: 654+46, 340+360, 713+27).

## B. Batas pertama, satu instance

API + worker native. "Idle" adalah CPU idle terendah mesin (8 core).

### `browse` (n=1)

| VU | req/dtk | daftar event p50 / p95 / p99 (ms) | detail p95 | API / Postgres / k6 CPU | 5xx |
| --- | --- | --- | --- | --- | --- |
| 50 | 25 | 2,9 / 5,4 / 8,5 | 7,0 | 4% / 14% / 9% | 0 |
| 200 | 101 | 2,4 / 4,9 / 7,7 | 7,0 | 11% / 14% / 27% | 0 |
| 500 | 246 | 1,6 / 3,5 / 6,4 | 5,2 | 18% / 22% / 43% | 0 |
| 1.000 | 495 | 1,2 / 2,3 / 4,0 | 3,7 | 28% / 38% / 61% | 0 |

Ambang 300 ms tidak pernah dekat; batas tidak tercapai. `DB_MAX_CONNS=20`, antre pool 98 kali / 0,4 dtk pada 1.000 VU. RSS API 29–73 MiB, k6 41–459 MiB. Pembanding 1.000 VU dijalankan 3 kali lagi di bagian D: 495 (492–495) req/dtk, daftar p95 2,5 (2,5–2,6) ms.

### `war-noqueue` (kuota 500)

| Pengguna | `DB_MAX_CONNS` | n | req/dtk | Reservasi p50 / p95 / p99 (ms) | Order p95 | Antre pool (kali / dtk) | API / PG / k6 CPU | Idle | `evalsha` p95 ≤ (ms) | Ambang |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 2.000 | 20 | 1 | 280 | 2,8 / 95 / 219 | 12 | 1.978 / 107 | 30 / 22 / 64% | 65% | 2,5 | lulus |
| 2.000 | 50 | 1 | 268 | 2,6 / 69 / 220 | 11 | 1.779 / 76 | 27 / 21 / 61% | 70% | 2,5 | lulus |
| 2.000 | 100 | 1 | 282 | 3,0 / 66 / 641 | 13 | 1.665 / 40 | 31 / 40 / 62% | 39% | 50 | lulus |
| 5.000 | 20 | 3 | 685 (678–715) | 4,4 / **342 (287–342)** / 397 (387–413) | 18 (12–261) | 11.640 / 1.392 (1.279–1.402) | 76 / 57 / 318% | 25% (0–43) | 50 (10–250) | 1 dari 3 |
| 5.000 | 50 | 3 | 677 (673–687) | 4,6 / **367 (199–387)** / 432 (355–467) | 12 (11–17) | 7.410 / 1.264 (770–1.295) | 76 / 113 / 319% | 37% (19–39) | 50 (50–250) | 1 dari 3 |
| 5.000 | 100 | 3 | 675 (670–689) | 3,9 / **342 (275–443)** / 444 (398–674) | 15 (15–21) | 6.486 / 341 (331–535) | 77 / 87 / 212% | 24% (10–27) | 250 (250–500) | 1 dari 3 |
| 10.000 | 20 | 1 | 909 | 381 / 1.426 / 1.587 | 1.728 | 204.441 / 57.578 | 85 / 113 / 581% | 0% | 50 | gagal |
| 10.000 | 50 | 1 | 958 | 118 / 1.559 / 2.140 | 358 | 197.446 / 19.434 | 104 / 131 / 583% | 2% | 1.000 | gagal |
| 10.000 | 100 | 1 | 972 | 371 / 2.294 / 2.545 | 2.753 | 120.112 / 10.411 | 97 / 154 / 625% | 5% | 2.500 | gagal |

5xx nol dan integritas lulus di semua baris. Koneksi Postgres: `DB_MAX_CONNS` + 3–4 (24, 53, 103 dari 200); yang aktif 2–4 pada 2.000–5.000 (satu eksekusi c20 menyentuh 12). Goroutine API ±1.990 / 4.950 / 9.950. RSS API 99–105 / 178–203 / 305–445 MiB, k6 ±0,7 / 1,7 / 3,5 GiB. Ops Redis puncak ±3–7 ribu / 9–13 ribu / 13–15 ribu per detik.

**Batas pertama.** SLO pertama yang dilanggar: p95 reservasi < 300 ms, di 5.000 pengguna (order tetap jauh di bawah 800 ms). Ketiga `DB_MAX_CONNS` beririsan (342, 367, 342 ms), jadi menaikkan pool **tidak terbukti** memperbaiki p95 walau waktu antre pool turun dari ±1.392 menjadi ±341 detik: antrean berpindah ke pool klien Redis (lihat di bawah). Gejala pertama yang terukur adalah antre pool koneksi Postgres di dalam proses API, bukan kejenuhan Postgres: sesi aktif hanya 2–4 dan server Redis ±30 µs per `evalsha`. Pada ukuran ini beban masih semburan tunggal: semua yang menunggu menembak tepat saat penjualan dibuka.

**Batas mesin, bukan RaceGo:** 10.000 tanpa antrean. CPU mesin habis (idle 0–5%) dengan k6 sendiri memakai ±600%. p95 di baris itu tidak boleh dibaca sebagai kemampuan server.

### `war` (antrean, n=1)

| Pengguna | req/dtk | Reservasi p50 / p95 / p99 (ms) | `queue/me` p50 / p95 / p99 | Order p50 / p95 / p99 | API / PG / Redis / k6 CPU | Idle | RSS k6 | 5xx / integritas |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 5.000 | 298 | 1,3 / 3,3 / 5,2 | 1,9 / 5,4 / 8,8 | 7,1 / 14,5 / 22,0 | 28 / 26 / 11 / 243% | 35% | 1,8 GiB | 0 / lulus |
| 10.000 | 474 | 1,1 / 3,8 / 9,1 | 1,6 / 7,9 / 65,9 | 7,1 / 16,5 / 29,5 | 34 / 72 / 26 / 665% | 8% | 3,6 GiB | 0 / lulus |
| 20.000 | 377 | 1.136 / 2.948 / 4.169 | 332 / 33.082 / 60.000 | 1.216 / 56.135 / 60.201 | 51 / 92 / 46 / 995% | 0% | **5,5 GiB (batas)** | 70.933 tak terduga / lulus |

Dengan antrean, laju masuk 20/dtk membuat pekerjaan reservasi datar, jadi 5.000 dan 10.000 lulus. **20.000 adalah batas generator beban/mesin**: k6 menyentuh batas memorinya (5,5 GiB), CPU mesin habis, dan `docker stats` menunjukkan server hanya 51% core API. Kegagalan jaringan di baris itu tidak menyatakan apa pun tentang batas RaceGo. Koneksi Postgres 22–30.

Konfigurasi mana yang diulang: **hanya `war-noqueue` 5.000** (c20, c50, c100: masing-masing 3 eksekusi), karena p95-nya di sekitar ambang dan selisih antar-pool tidak bisa dibedakan dari derau pada satu eksekusi. Yang **tidak** diulang, atas keputusan pemilik proyek: `browse` (jauh di bawah ambang, p95 ≤ 7 ms), `war-noqueue` 2.000 (p95 66–95 ms, lulus jelas), `war` 5.000/10.000 (p95 ≤ 4 ms), serta `war-noqueue` 10.000 dan `war` 20.000 (gagal jelas, mesin/generator jenuh; mengulang tidak memberi informasi tentang server). Baris `n=1` karena itu hanya menyatakan lulus/gagal, bukan angka presisi.

### `evalsha` p95: antrean klien Redis, bukan Redis dan bukan scheduler

Eksperimen terpisah: `war-noqueue` 5.000, `DB_MAX_CONNS=50`, hanya ukuran pool Redis klien yang diubah (biner uji; kode repo tidak berubah). 3 eksekusi masing-masing.

| `PoolSize` Redis klien | `evalsha` p95 ≤ (ms) | Waktu antre pool Redis (dtk) | Waktu antre pool Postgres (dtk) | Server Redis per `evalsha` | Penundaan scheduler Go p99 | Reservasi p95 (ms) |
| --- | --- | --- | --- | --- | --- | --- |
| 50 (bawaan repo) | 250 (50–500) | 1.070 (102–1.753) | 596 (171–694) | ±26 µs | ≤ 0,1 ms | 306 (165–388) |
| 1.000 | 25 (10–50) | 0 | 1.365 (1.103–2.706) | ±28 µs | ≤ 0,1 ms | 379 (321–459) |

Kesimpulan: dugaan "≤ 100 ms" di docs 11 adalah **antre pool klien Redis di dalam proses API** saat semua pengguna menembak serentak. Redis (puluhan µs per skrip) dan penjadwalan goroutine (p99 ≤ 0,1 ms, tetapi mencakup semua goroutine) tidak menjelaskannya. Menghilangkan antre itu **tidak** memperbaiki p95 reservasi (306 vs 379 ms, rentang beririsan): antreannya pindah ke pool Postgres. Karena itu `PoolSize` tidak diubah.

## C. Scale-out API

Berkas uji saja: `docker-compose.loadtest.scale.yml`, `loadtest/nginx.conf`. Pemakaian: `SCALE=N` (lihat [loadtest/README.md](../loadtest/README.md)).

- **Batas CPU sama untuk semua N**: replika API 0,5 core, worker 0,5, Postgres 2, Redis 1, nginx 1, k6 3 core (RAM 5,5 GB). Jumlah batas N=4 = 9,5 core, di atas 8 core mesin, tetapi pemakaian puncak terukur jauh di bawahnya: idle terendah pada `war-noqueue` 30% (N=1), 46% (N=2), 48% (N=4). Angka `war` 10.000 N=1 menyentuh idle 0–4% saat k6 menyalakan VU: **hasil di sana dibatasi mesin**.
- **IP klien**: k6 mengirim `X-Forwarded-For` satu IP per pengguna, nginx menambah alamat k6 di kanan; `TRUSTED_PROXY_HOPS=2` membaca entri kedua dari kanan, jadi pembatas per IP tetap per pengguna.
- **Koneksi Postgres** (`N × 20` + worker 10 < `max_connections` 200): terukur 23, 43, 83 (N=1, 2, 4).
- nginx `least_conn`, DNS Docker di-resolve ulang tiap 2 detik, `max_fails=1 fail_timeout=5s`.

### `war-noqueue` 5.000 (3 eksekusi per baris)

| N | req/dtk rata-rata | Puncak req/dtk | Reservasi p50 / p95 / p99 (ms) | Order p95 | Antre pool Postgres (dtk) | CPU API total (per replika) | CPU PG / Redis / LB / k6 | 5xx | Integritas |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | 687 (682–694) | 2.997 (2.972–3.025) | 3,0 / **421 (406–480)** / 499 (497–510) | 13 (11–20) | 1.776 (1.547–3.404) | 58% (58%) | 52 / 11 / 30 / 178% | 0 | lulus 3/3 |
| 2 | 680 (679–686) | 4.193 (3.982–4.746) | 2,6 / **184 (178–247)** / 192 (189–284) | 11 (11–15) | 652 (462–694) | 82% (42%) | 63 / 13 / 30 / 174% | 0 | lulus 3/3 |
| 4 | 700 (695–702) | 5.334 (4.903–5.455) | 2,8 / **131 (110–133)** / 150 (122–162) | 15 (11–65) | 52 (39–56) | 118% (32%) | 108 / 19 / 35 / 164% | 0 | lulus 3/3 |

Rentang p95 tiap N tidak beririsan, jadi perbaikannya di luar derau. **Tidak linear**: puncak throughput 1 : 1,40 : 1,78 untuk 1 : 2 : 4 replika, p95 turun 56% lalu 29% lagi. Rata-rata req/dtk sama karena kedatangannya ditentukan skenario, bukan server. N=1 gagal ambang di ketiga eksekusi; N=2 dan N=4 lulus. Ambangnya gagal pada 0,5 core karena **CPU API jenuh** (58% dari 50%, di dalam ketelitian cuplikan); ini lebih ketat dari proses native di bagian B.

**Yang jenuh berikutnya:** pada N=4 tidak ada sumber daya yang teramati jenuh. Replika memakai 32% dari 50%, antre pool sisa 52 detik, Postgres 108% dari 200%, Redis 19%, LB 35%. Postgres paling dekat dan naik bersama N (52, 63, 108%), jadi **kandidat** berikutnya; itu bukan bukti kejenuhan, dan generator tidak mampu mendorong lebih keras di mesin ini. N > 4 tidak diuji.

### `war` 10.000 (antrean)

| Konfigurasi | req/dtk | Reservasi p95 (ms) | `queue/me` p95 | Order p95 | CPU API total / PG / k6 | Idle | 5xx | Integritas |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| N=1, 1 worker | 476 (473–481) | 3,1 (2,3–5,2) | 4,2 (3,8–5,8) | 49 (14–**1.156**) | 33 / 42 / 299% | 4% (0–33) | 0 | lulus 3/3 |
| N=2, 1 worker | 475 (475–476) | 2,2 (2,2–2,2) | 3,6 (3,5–3,7) | 12 (12–13) | 38 / 40 / 283% | 26% (20–36) | 0 | lulus 3/3 |
| N=4, 1 worker | 475 (473–476) | 2,3 (2,1–2,4) | 3,5 (3,4–3,7) | 11 (11–290) | 53 / 39 / 309% | 27% (17–41) | 0 | lulus 3/3 |
| N=2, **2 worker** | 475 (474–476) | 2,2 (2,2–2,2) | 4,1 (3,3–4,1) | 12 (11–13) | 64 / 49 / 271% | 31% (9–42) | 0 | lulus 3/3 |

Dengan antrean, semua N praktis sama: beban reservasi sudah dibatasi 20/dtk, replika tambahan tidak punya yang dikerjakan. Satu dari tiga eksekusi N=1 melewati ambang order (1.156 ms) saat idle mesin 0%, di tengah penyalaan VU k6; satu-satunya eksekusi C dengan antrean yang gagal ambang, dan rentang order p95-nya lebar. Ini kemungkinan besar batas mesin tetapi tidak dibuktikan terpisah.

### Rolling restart dan replika mati mendadak

N=2, salah satu replika dimatikan saat `war`, kembali setelah ±10 detik.

| Uji | n | 5xx / tak terduga | Pengguna gagal | Reservasi p95 (ms) | Integritas |
| --- | --- | --- | --- | --- | --- |
| Graceful (SIGTERM, `docker stop -t 15`) pada `war` 10.000 | 3 | 0 | 0 | 2,2 (2,1–2,3) | lulus 3/3 |
| Mendadak (SIGKILL) pada `war` 5.000 | 3 | 0 | 0 | 2,6 (2,1–2,7) | lulus 3/3 |

Tidak ada error yang sampai ke klien: konfigurasi nginx mengalihkan koneksi gagal ke replika lain, dan k6 tidak pernah perlu backoff (jalur backoff klien karena itu tidak teruji di sini; chaos docs 11 yang mengujinya). **Batas kesimpulan:** hanya saat semburan sedang (±470 / 300 req/dtk). Permintaan `POST` yang sedang diproses saat SIGKILL tidak diulang nginx; tidak ada yang tercatat tertangkap dalam enam eksekusi ini, tetapi jendelanya kecil (latensi milidetik), jadi ini tidak membuktikan nol. Graceful shutdown 10 detik cukup untuk lalu lintas ini; bila ada permintaan mendekati batas 15 detik, ia bisa terpotong. Tidak ada penyesuaian kode yang terbukti perlu.

## D. Dua intervensi

### PgBouncer (mode transaction) — diuji, tidak terbukti, tidak diterapkan

**Kompatibilitas** (sebelum mengukur): seluruh `go test ./...` hijau dan `migrate up` + `seed` berhasil melalui PgBouncer 1.26.1 mode transaction (`MAX_PREPARED_STATEMENTS=200`, pgx memakai prepared statement bernama), termasuk `FOR UPDATE`, `SKIP LOCKED`, dan `pg_advisory_xact_lock`. Kode tidak memakai `SET` sesi, `LISTEN`, atau advisory lock sesi.

`war-noqueue` 5.000, 3 eksekusi per baris, pool server PgBouncer 20:

| Konfigurasi | Reservasi p95 (ms) | Antre pool API (dtk) | Koneksi Postgres | Integritas |
| --- | --- | --- | --- | --- |
| N=1 langsung, pool 20 (acuan C) | 421 (406–480) | 1.776 (1.547–3.404) | 23 | 3/3 |
| N=1 langsung, pool 100 | 669 (479–966) | 962 (580–1.869) | 103 | 3/3 |
| N=1 PgBouncer, pool 100 ke 20 | 490 (433–589) | 1.035 (754–1.426) | 23 | 3/3 |
| N=4 langsung, pool 20 (acuan C) | 131 (110–133) | 52 (39–56) | 83 | 3/3 |
| N=4 PgBouncer, pool 50 ke 20 | 133 (118–148) | 215 (60–235) | 23 | 3/3 |

p95 N=1 dan N=4 beririsan dengan acuan langsung: **tidak ada perbaikan latensi**. Manfaat nyatanya hanya jumlah koneksi Postgres (83 menjadi 23 pada N=4), dan itu belum menjadi masalah pada ≤ 200 koneksi. Menaikkan `DB_MAX_CONNS` ke 100 pada satu replika 0,5 core memberi p95 yang rentangnya sebagian besar di atas acuan: menambah koneksi tidak menolong CPU yang jenuh. Definisi PgBouncer tetap di berkas uji (`PGBOUNCER=1`) untuk mengulang pengukuran.

### Cache baca publik di API — diuji, tidak terbukti untuk titik sempit, tidak diterapkan

Yang diuji: middleware memori proses (biner uji, tidak masuk repo) untuk `GET /v1/events` (daftar, kalender), detail event, filter, dan penyelenggara; TTL mengikuti `max-age` yang **sudah** dikirim handler (2–60 detik), `singleflight`, cookie dan identitas dibuang sebelum handler, hanya `200` ber-`Cache-Control: public`.

| Skenario | Tanpa cache | Dengan cache | Penilaian |
| --- | --- | --- | --- |
| `war-noqueue` 5.000, N=1: reservasi p95 (ms) | 421 (406–480) | 571 (540–688) | **Lebih buruk**, di luar rentang (acuan diukur beberapa jam sebelumnya) |
| idem, antre pool Postgres (dtk) | 1.776 (1.547–3.404) | 1.908 (1.722–2.359) | Beririsan |
| `browse` 1.000 VU native: detail p95 (ms) | 4,1 (3,8–4,1) | 2,7 (2,5–2,7) | Di luar derau, keduanya jauh di bawah ambang |
| idem, CPU API / Postgres | 32% / 26% (25–66) | 18% (18–19) / 5% (5–5) | Di luar derau |
| `browse` 2.500 VU, N=1 (0,5 core): daftar p95 (ms) | 2,5 (2,3–2,5) | 2,6 (2,4–2,8) | Beririsan |
| idem, CPU API / Postgres | 46% / 56% | 28% / 9% | Di luar derau |
| `browse` 4.000 VU, N=1: daftar p95 (ms) | 25 (11–1.008) | 2,8 (2,8–155) | Beririsan; mesin habis (idle 0%) di satu eksekusi tiap sisi |
| idem, CPU Postgres | 93% (85–94) | 9% (7–9) | Di luar derau |

Penghematan Postgres nyata, tetapi tidak ada ambang baca yang gagal tanpa cache pada beban yang diukur dengan sehat, dan pada titik sempit (reservasi) tidak membantu. Sesuai aturan tugas, kodenya tidak diterapkan; tidak ada perubahan pada tampilan ketersediaan. Header `Cache-Control` yang sudah ada tetap memungkinkan cache di CDN tanpa perubahan kode (langkah 2 peta skala docs 11). Kebenaran stok tidak terkait cache ini: reservasi tetap diputuskan Redis/Lua.

## Rekomendasi berurutan

1. **Pakai antrean** untuk event yang diburu. Di laptop ini 10.000 pengguna lulus dengan p95 reservasi 3,8 ms pada satu instance, sedangkan tanpa antrean 5.000 pengguna sudah berada di sekitar ambang 300 ms.
2. **Bila tetap tanpa antrean, tambah CPU atau replika API** sebelum pool. N=2 sudah memenuhi ambang di 5.000 (421 menjadi 184 ms), N=4 menjadi 131 ms. Jangan menaikkan `DB_MAX_CONNS` (20, 50, 100 tidak terbedakan) atau pool Redis klien (antrean hanya pindah). Syarat: `TRUSTED_PROXY_HOPS` benar, kunci sama di semua replika, volume bersama untuk unggahan dan KYC, migrasi dijalankan sekali.
3. **Tambah worker boleh** (2 worker: laju panggil tetap 20/dtk, integritas lulus), tetapi tidak menaikkan kapasitas pada beban ini.
4. **Pantau CPU Postgres** sebagai kandidat batas sesudah N=4; PgBouncer baru bila jumlah koneksi mendekati `max_connections`.
5. **Cache CDN** untuk `GET /v1/events*` (header sudah ada) bila `browse` menjadi beban Postgres yang nyata; cache dalam proses tidak diperlukan sekarang.

## Batasan yang jujur

- Satu laptop; k6, API, worker, Postgres, Redis, dan LB berbagi core dan RAM. Tidak ada server, jaringan, TLS, atau CDN nyata. Midtrans mock, bukan sandbox dan bukan produksi.
- Tidak ada klaim kapasitas produksi. Bila k6 atau mesin jenuh lebih dulu (`war-noqueue` 10.000, `war` 20.000, sebagian `war` 10.000 N=1), yang tertulis adalah batas alat ukur.
- 0,5 core per replika adalah pembatas buatan agar perbandingan adil; bukan ukuran server yang dianjurkan. Skala di atas N=4 dan lebih dari satu host tidak diuji, begitu juga Redis Cluster, Postgres mati, atau jaringan terpisah sebagian.
- Derau besar: p95 5.000 tanpa antrean bisa 199–387 ms pada konfigurasi yang sama. Tiga eksekusi adalah minimum, bukan interval kepercayaan. Baris `n=1` ditandai.
- Beban `war-noqueue` adalah semburan serempak di detik pembukaan, lebih tajam dari kedatangan nyata. Acuan C (N=1, N=4) dan intervensi D diukur berurutan, bukan diselang-seling, dan sebagian ronde memakai baterai.
- Sisa dari audit (`RestoreUserLimits`, penyimpanan berkas lokal, `migrate up`, `/healthz`, urutan OTP) dicatat, tidak diperbaiki di sini.
