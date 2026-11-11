# Tugas 09 · Uji beban war tiket + metrik dasar (semua gratis)

> Prompt untuk AI coding agent (Claude Code CLI). Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/09-uji-beban-metrik.md sampai semua kriteria selesai terpenuhi"`
> Prasyarat: tugas 01–08 sudah ter-commit. Bila butuh migrasi baru: `0009`.

## Konteks

RaceGo: backend Go (`backend/`), frontend Next.js 16 (`frontend/`), Postgres, Redis, Caddy, Docker Compose. Baca dulu `README.md`, `docs/02-arsitektur.md`, `docs/03-war-tiket.md`, `docs/04-pembayaran-dan-ledger.md`, `docs/08-development.md`, `docs/09-roadmap.md`, `docs/10-deploy.md`, `scripts/queue-sim.mjs` (simulator antrean Tugas 04, jangan ditimpa), dan **`frontend/AGENTS.md`**. **Baca kodenya langsung**; ringkasan di bawah dari ingatan dan bisa meleset, kode yang menang (catat bedanya di laporan).

Sistem sudah punya: reservasi stok atomik di Redis (Lua, hash tag `{cat:<id>}`), waiting room (admit per detik, batas aktif, pass HMAC), checkout idempoten, webhook + rekonsiliasi pembayaran, worker outbox (`SKIP LOCKED`), ledger double-entry, rate limit Redis, mode pembayaran mock (rute simulasi di non-produksi). **Belum ada**: uji beban dengan angka, metrik, dan bukti kuantitatif bahwa tidak ada oversell di bawah beban. Anggapan "bisa di-scale" masih belum terukur.

## Batasan utama: SEMUA harus gratis dan lokal

- Hanya perangkat lunak open source yang berjalan **lokal di Docker/Mac**: **k6** (image `grafana/k6`), **Prometheus**, **Grafana OSS**, pustaka Go `prometheus/client_golang`. **Tanpa akun cloud, tanpa layanan berbayar, tanpa trial, tanpa kartu, tanpa Grafana Cloud, tanpa SaaS pemantauan, tanpa agen yang mengirim data keluar.**
- Beban dibangkitkan dari mesin yang sama; **dilarang menyasar host selain `localhost`/jaringan Docker lokal** (skrip k6 menolak `BASE_URL` selain itu kecuali flag eksplisit yang tidak boleh dipakai dalam tugas ini), **dilarang memanggil Midtrans/Cloudflare/SMTP sungguhan** (pembayaran mock, SMTP ke Mailpit atau dinonaktifkan, Turnstile mati).
- **Tumpukan produksi (`docker-compose.prod.yml`) tidak boleh bertambah service atau memori** karena tugas ini: Prometheus/Grafana hanya di berkas terpisah `docker-compose.obs.yml` yang opsional dan tidak dipakai `make prod-up`. Satu-satunya tambahan di produksi: endpoint metrik di API/worker (overhead kecil, dan **tidak terbuka ke publik**).
- Laporkan **jejak sumber daya** tumpukan observabilitas (RAM/CPU Prometheus + Grafana, diukur nyata) supaya jelas bahwa itu bisa dimatikan di VPS kecil.

## Tujuan

1. Metrik dasar yang aman (`/metrics`) + dashboard Grafana siap pakai, opsional.
2. Skrip uji beban war tiket yang realistis dan dapat diulang, dengan **pemeriksaan integritas otomatis setelahnya** (tidak ada oversell, ledger seimbang, tanpa duplikat).
3. Angka nyata: latensi p50/p95/p99, throughput, tingkat error, perilaku antrean, titik sempit.
4. Penyetelan **hanya berdasarkan temuan**, tiap perubahan dengan angka sebelum/sesudah.
5. Laporan `docs/11-performa.md` yang jujur.

## Keputusan desain (ikuti)

### A · Metrik

1. **Pustaka resmi** `github.com/prometheus/client_golang` (pastikan lisensi Apache-2.0 dan versi stabil terbaru; naikkan `go.mod` seperlunya dan jalankan `govulncheck` bila ada).
2. **Endpoint terpisah dari server publik.** Listener metrik sendiri di `METRICS_ADDR` (default **kosong = mati**; contoh dev `127.0.0.1:9090` untuk API, port berbeda untuk worker, atau satu proses/port per biner). **Tidak** dilewatkan Caddy; buktikan `curl https://localhost/metrics` lewat Caddy = 404. Bila di-bind ke alamat non-loopback, wajib token (`METRICS_TOKEN`, bandingkan konstan-waktu) atau jaringan Docker internal. Tanpa token + non-loopback → API menolak start dengan pesan jelas.
3. **Metrik wajib** (nama berawalan `racego_`): HTTP (jumlah + histogram latensi per **pola rute** chi, bukan path mentah, + status kelas), reservasi stok (sukses/ habis/ ditolak batas per akun), antrean (panjang lobi, jumlah aktif, laju admit, usia tertua), checkout/order (dibuat, lunas, kedaluwarsa), outbox (backlog, usia tertua, gagal/retry per topik), worker (durasi langkah), pool DB (`pgxpool.Stat`: acquired, idle, total, wait count/duration), Redis (latensi perintah kunci, error), email (terkirim/gagal), payout/refund (jumlah per status), proses Go (`go_*`, `process_*` bawaan).
4. **Larangan kardinalitas dan data sensitif:** label hanya dari himpunan kecil dan tetap (rute pola, metode, status, topik). **Dilarang** label berisi user ID, email, org slug, event ID, order ID, token, IP, atau teks bebas. Tes otomatis yang memeriksa seluruh keluaran `/metrics` setelah lalu lintas sintetis: jumlah deret terbatas (tetapkan batas) dan tidak memuat pola email/UUID/token.
5. Overhead: ukur latensi sebelum/sesudah instrumentasi pada endpoint panas (reservasi); laporkan, tidak boleh merusak p95 secara berarti.
6. **Dashboard Grafana** di-*provision* lewat berkas (datasource Prometheus + 1 dashboard JSON: kedalaman antrean, admit/s, reservasi sukses/habis, p95/p99 per rute utama, error rate, pool DB, backlog outbox, memori/GC). Tanpa plugin berbayar atau yang butuh akun; kata sandi admin Grafana dari env (bukan default `admin/admin` yang tersimpan di repo), hanya bind `127.0.0.1`.

### B · Uji beban (k6)

7. Folder `loadtest/` (skrip k6 + pembantu + README singkat; hasil mentah ke `loadtest/results/` yang di-`.gitignore`). Target `make`: `loadtest-up` (menyiapkan DB uji terpisah + stack), `loadtest-seed`, `loadtest SCENARIO=<nama> USERS=<n>`, `obs-up`/`obs-down`, `loadtest-clean`. Gunakan **database dan Redis DB-index terpisah** dari data dev Anda (jangan menimpa data dev; tampilkan jelas target yang dipakai dan minta flag konfirmasi bila mendekati data dev).
8. **Menyiapkan pengguna sintetis tanpa membuka pintu belakang:** perintah `cmd/migrate` (mis. `loadgen-seed`) yang membuat N pengguna + sesi langsung di DB dan menulis berkas token untuk k6, **menolak jalan bila `APP_ENV` bukan `development`** dan hanya untuk DB yang namanya berawalan/berakhiran `_loadtest` (cek nama DB). Jangan menambah flag "lewati autentikasi" di server. Pembatas laju per-IP: **jangan dimatikan**; sebarkan sumber IP lewat `X-Forwarded-For` berbeda per pengguna dengan `TRUSTED_PROXY_HOPS` yang sesuai (uji cara ini lebih dulu), supaya rate limit tetap ikut teruji. Bila ada pembatas yang tetap menghalangi beban wajar, catat sebagai temuan, bukan dimatikan diam-diam.
9. **Skenario (parameter lewat env, nilai awal kecil, dinaikkan bertahap):**
   - `browse`: baca-berat (daftar event, detail, kalender) kombinasi realistis → baseline.
   - `war`: event uji dengan kuota kecil (mis. 500 tiket di 3 kategori) dan N pengguna (mulai 500 → 2.000 → 5.000 bila mesin kuat) datang dalam jendela ±60 detik: masuk antrean → polling dengan jitter sesuai `poll_after_ms` → admit → reservasi → checkout → bayar (rute simulasi mock) → menunggu tiket terbit. Setiap pengguna berperilaku seperti klien nyata (menghormati `poll_after_ms`, mencoba ulang dengan backoff, sebagian menyerah di tengah).
   - `war-noqueue`: sama tetapi antrean dinonaktifkan, untuk menunjukkan **kenapa antrean ada** (latensi/error naik) sambil membuktikan **reservasi atomik tetap tidak oversell**.
   - `soak`: beban sedang 10–15 menit untuk melihat kebocoran memori/goroutine/koneksi, backlog outbox, pertumbuhan kunci Redis.
10. **Pemeriksaan integritas otomatis setelah tiap `war*`** (skrip yang gagal = uji gagal): (a) tiket terbit per kategori ≤ kuota, dan jumlah order lunas konsisten dengan tiket; (b) **tidak ada BIB ganda**, tidak ada tiket ganda per peserta; (c) batas "satu pembeli per kategori" tidak terlampaui; (d) `ledger/check` hijau dan jurnal `order.paid` tepat satu per order lunas; (e) stok Redis sesuai kuota − terjual − reservasi aktif (rekonsiliasi Redis vs DB); (f) tidak ada order `pending` tanpa reservasi yang masih berlaku setelah masa tunggu, tidak ada backlog outbox tertinggal setelah drain; (g) pelari yang gagal membeli mendapat respons jelas ("habis"), bukan error 5xx tak terduga. Laporkan hasilnya per skenario.
11. **Chaos ringan (lokal, terkontrol):** saat `war` berjalan, (i) hentikan lalu nyalakan Redis (container restart) dan (ii) matikan lalu nyalakan worker; sesudahnya jalankan pemeriksaan integritas. Tujuan: membuktikan pemulihan tanpa oversell/ledger rusak, dan mencatat apa yang terlihat pengguna selama gangguan. Temuan kerusakan = bug yang harus dilaporkan dan, bila di dalam cakupan keamanan data uang/stok, diperbaiki dengan tes.
12. **Ambang yang dilaporkan, bukan klaim:** tentukan SLO uji yang masuk akal (mis. p95 reservasi, p95 poll antrean, error rate) di skrip k6 (`thresholds`), laporkan lolos/tidak per skenario dan di ukuran mana mulai gagal. Jangan mengarang angka; semua dari eksekusi nyata.

### C · Penyetelan (hanya berbasis temuan)

13. Urutan: ukur → temukan titik sempit (metrik, `pg_stat_statements` bila diaktifkan lokal, `EXPLAIN ANALYZE` pada kueri panas, profil Go `pprof` di listener metrik/dev saja) → ubah satu hal → ukur ulang. Kandidat yang boleh diperiksa (jangan diterapkan bila tidak terbukti perlu): ukuran pool koneksi DB (jadikan env, mis. `DB_MAX_CONNS`), pencarian sesi per request (cache pendek di Redis/memori hanya bila terbukti jadi beban dan tanpa melemahkan pencabutan sesi), indeks yang hilang, antrean outbox/batch worker, konfigurasi Redis, jumlah worker paralel. **Tiap perubahan wajib punya angka sebelum/sesudah dan tes yang mencegah regresi keamanan/korektnes**. Jangan mengubah semantik reservasi, ledger, atau keamanan demi kecepatan.
14. Bila temuan menunjukkan batas arsitektural (mis. satu Postgres, satu kategori panas di satu slot Redis), **jangan** merancang ulang sekarang; dokumentasikan di laporan dengan rekomendasi bertahap dan perkiraan biaya (gratis vs berbayar).

### D · Laporan `docs/11-performa.md`

15. Isi: spesifikasi mesin dan alokasi Docker (RAM/CPU), versi, metode, skenario + parameter, **tabel hasil** (ukuran → p50/p95/p99, throughput, error, oversell = 0/≠0, hasil integritas), grafik/tangkapan dashboard (boleh simpan gambar di `docs/img/` bila kecil), titik sempit yang ditemukan + perbaikan (sebelum/sesudah), hasil chaos, **batasan jujur** (generator beban dan server berbagi CPU sehingga angka adalah batas bawah; bukan jaminan produksi; belum diuji di jaringan nyata; mock pembayaran), dan **peta skala bertahap**: apa yang dilakukan pertama, kedua, ketiga bila beban naik (cache sesi, PgBouncer, replika baca, banyak instance API/worker, Redis terkelola/cluster, CDN Cloudflare gratis) dengan catatan mana yang berbiaya. Tanpa klaim kepatuhan, tanpa klaim "tahan N pengguna" di luar yang diukur.

## Di luar cakupan

Pemantauan produksi berbayar/SaaS, alerting (Alertmanager), tracing terdistribusi (OpenTelemetry), log terpusat, uji dari beberapa wilayah, uji terhadap Midtrans/Cloudflare sungguhan, Kubernetes/autoscaling, perubahan arsitektur besar (sharding, replika), mengubah alur bisnis. Jangan dikerjakan sekarang.

## Kriteria selesai (cek satu per satu dan laporkan dengan bukti nyata)

1. `make test` hijau (vet, tes Go dengan Postgres/Redis, lint, typegen, tsc), `cd frontend && npm run build` sukses, tes lama tidak diubah dan hijau. Tes baru ada dan lulus (jumlah PASS, 0 SKIP): metrik tersedia dan label aman (batas deret, tanpa UUID/email/token), `METRICS_ADDR` kosong = tidak ada listener, non-loopback tanpa token = gagal start, token salah = 401/403, perintah `loadgen-seed` menolak di luar development dan pada DB yang bukan `_loadtest`.
2. **Gratis dibuktikan:** daftar semua komponen baru (nama, versi, lisensi, jalan di mana), pernyataan bahwa tidak ada akun/layanan berbayar yang dipakai, dan bukti bahwa `docker-compose.prod.yml` tidak berubah selain variabel metrik opsional (tampilkan `git diff` berkas itu). Jejak sumber daya Prometheus + Grafana (RAM/CPU nyata).
3. **Keamanan metrik:** `curl` ke `https://localhost/metrics` lewat Caddy = 404; listener metrik hanya di loopback/jaringan internal; keluaran metrik tidak memuat data sensitif (tunjukkan hasil pemeriksaan otomatis).
4. **Skenario dijalankan nyata** (`browse`, `war` minimal pada 3 ukuran, `war-noqueue`, `soak`): tabel angka p50/p95/p99, throughput, error, antrean (admit/s, panjang, waktu tunggu), ukuran mulai gagal, dan **pemeriksaan integritas (a)–(g) lulus pada setiap eksekusi `war*`** (bukti keluaran skrip).
5. **Chaos ringan** (restart Redis, matikan worker saat `war`): hasil integritas dan perilaku yang terlihat pengguna; kerusakan data = bug dilaporkan/diperbaiki.
6. **Penyetelan:** daftar temuan, tiap perubahan dengan angka sebelum/sesudah, dan tes regresi; atau pernyataan jelas bahwa tidak ada yang terbukti perlu diubah.
7. **Dashboard Grafana** tampil benar saat beban berjalan (screenshot, dilihat sendiri); `make obs-up`/`obs-down` bersih; kata sandi tidak di repo.
8. `docs/11-performa.md` lengkap; dokumen lain diperbarui (`docs/08-development.md`: cara menjalankan `make loadtest`/`obs-up`; `docs/09-roadmap.md`: metrik dasar dan uji beban dicentang, sisa observabilitas dicatat; `docs/10-deploy.md`: `METRICS_ADDR`/`METRICS_TOKEN`, bahwa Prometheus/Grafana tidak dipakai di VPS kecil dan metrik tidak dibuka publik; `README.md`; `.env.example`; `.env.prod.example`; `make help`).
9. Bersih-bersih: DB/Redis uji dan data sintetis dihapus (`make loadtest-clean`), proses/container yang dinyalakan dimatikan, **data dev dan `.env`/`.env.prod` pribadi tidak tersentuh**, `loadtest/results/` tidak masuk Git, `git status` bersih dari berkas uji/rahasia/data.

## Cara kerja

- Urutan: metrik (backend) + tes → `docker-compose.obs.yml` + dashboard → `loadgen-seed` + skrip k6 `browse` → `war` + pemeriksaan integritas → `war-noqueue`, `soak` → chaos ringan → penyetelan berbasis temuan → laporan dan dokumen → pembersihan. Jalankan `make test` di akhir tiap tahap.
- **Hemat sumber daya mesin dan jatah pemakaian:** mulai dari beban kecil, naikkan bertahap, jangan menjalankan skenario besar berulang tanpa alasan; hentikan proses yang tidak dipakai. Bila sesi terputus, tulis progres di `docs/tasks/09-progress.md` (centang kriteria + temuan + angka sementara) dan hapus di akhir.
- Jangan mengubah perilaku pembayaran, ledger, pencairan, KYC, scanner, login, atau antrean selain yang terbukti perlu (dan bila ya, dengan tes dan penjelasan). Bug di luar cakupan: catat saja.
- Jangan menulis data sensitif (token sesi, kunci, NIK, rekening, kode OTP) ke log, metrik, laporan, atau screenshot.
- Tanpa rahasia di repo, tanpa email/pembayaran sungguhan, tanpa tunnel ke internet, tanpa akun atau layanan berbayar.
- Bila keputusan di dokumen ini bertabrakan dengan kode atau kenyataan teknis, pilih opsi paling sederhana yang aman dan catat alasannya.
- Di akhir: ringkasan perubahan per file, tabel hasil tiap kriteria (bukti nyata; tandai yang tidak bisa diuji), angka-angka utama, titik sempit, keputusan/penyimpangan, asumsi, dan usulan pesan commit. **Jangan commit dan jangan push.**
