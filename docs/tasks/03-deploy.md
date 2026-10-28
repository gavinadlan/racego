# Tugas 03 · Deploy: paket produksi yang tidak bergantung penyedia + mode demo dari laptop

> Prompt untuk AI coding agent (Claude Code CLI). Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/03-deploy.md sampai semua kriteria selesai terpenuhi"`
> Prasyarat: tugas 01 (login) dan 02 (portal mitra) sudah ter-commit.

## Konteks

RaceGo: backend Go (`backend/`, binary `api`, `worker`, `migrate`, `keygen`), frontend Next.js 16 (`frontend/`), Postgres, Redis. Sekarang semuanya hanya jalan untuk development (`docker-compose.yml` hanya berisi Postgres/Redis/Mailpit; `make dev` menjalankan proses di host). Baca dulu `README.md`, `docs/02-arsitektur.md`, `docs/06-infra-gratis.md`, `docs/08-development.md`, dan **`frontend/AGENTS.md`** (Next.js 16 berbeda dari versi lama; docs ada di `frontend/node_modules/next/dist/docs/`, termasuk panduan `output: "standalone"` dan self-hosting/Docker).

Fakta yang sudah ada dan harus dihormati:

- `backend/Dockerfile` sudah ada (satu image berisi semua binary; base `golang:1.26-alpine` + distroless). **Verifikasi bahwa image-nya benar-benar ter-build** dan versi Go sesuai `go.mod`.
- Konfigurasi lewat environment (`backend/internal/config/config.go`, `.env.example`): `APP_ENV`, `FRONTEND_URL` (dipakai untuk CORS, pengecekan header `Origin` pada request yang mengubah data, tautan di email, redirect Midtrans), `TRUSTED_PROXY_HOPS` (lihat `backend/internal/server/realip.go`), `DATABASE_URL`, `REDIS_ADDR`/`REDIS_PASSWORD`, `MIDTRANS_*`, `QR_PRIVATE_KEY`, `AUTO_VERIFY_ORGS`, `UPLOAD_DIR`, `SMTP_*`, `MAIL_FROM`. Di `APP_ENV` selain `development`: header `X-User-ID` diabaikan, cookie sesi bertanda `Secure`, kunci QR wajib diisi (bukan kunci dev).
- Frontend membaca `NEXT_PUBLIC_API_URL`, `NEXT_PUBLIC_MIDTRANS_CLIENT_KEY`, `NEXT_PUBLIC_DEV_LOGIN` **saat build** (tertanam ke bundle). `lib/api.ts`: `API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080"` dan semua panggilan memakai `fetch(`${API_URL}${path}`, { credentials: "include" })`; gambar unggahan dipakai sebagai `API_URL + "/uploads/…"`.
- Poster/logo unggahan disimpan di `UPLOAD_DIR` (file lokal) dan dilayani API di `/uploads/{nama}`.
- Webhook Midtrans: `POST /v1/webhooks/midtrans`; selain itu backend sudah mengecek status ke Midtrans sendiri (halaman order + worker tiap menit), jadi tetap jalan walau webhook tidak sampai.
- Migrasi: `cmd/migrate up` (idempoten); data contoh: `cmd/migrate seed`; `cmd/migrate verify <slug>` dan `grant`.
- Target pengguna: proyek pribadi/portofolio, **semua gratis**. Penyedia server belum ditentukan (pendaftaran Oracle Cloud gagal; mungkin nanti Oracle, VPS murah, atau tidak sama sekali), jadi paket ini harus berjalan di **server Linux mana pun yang punya Docker**, dan di laptop untuk demo sementara. Jangan menulis apa pun yang mengikat ke satu penyedia kecuali sebagai contoh di dokumentasi.

## Tujuan

1. Satu perintah menjalankan **seluruh aplikasi dalam mode produksi** (Docker Compose) di server mana pun, dengan HTTPS otomatis dan satu alamat web publik.
2. Satu perintah `make demo` di laptop menjalankan stack yang sama dan membukanya ke internet lewat **Cloudflare quick tunnel** (gratis, tanpa akun, tanpa domain) untuk demo sementara.
3. Backup/restore yang teruji, dokumentasi deploy langkah demi langkah, dan CI di GitHub.

## Keputusan desain (ikuti)

1. **Satu origin.** Caddy menjadi satu-satunya pintu masuk: `/v1/*`, `/uploads/*`, `/healthz` → `api:8080`; semuanya lain → `web:3000`. Karena satu origin, **tidak ada masalah CORS/cookie lintas domain**. Build frontend untuk produksi memakai `NEXT_PUBLIC_API_URL=""` (string kosong, jadi panggilan relatif ke origin yang sama); pastikan `?? "http://localhost:8080"` memang menghasilkan string kosong (bukan fallback) dan semua pemakaian `API_URL` (termasuk gambar unggahan) tetap benar. Tidak perlu `rewrites` Next.js.
2. **Alamat web adalah pengaturan, bukan keputusan kode:** env `SITE_ADDRESS` untuk Caddy (domain asli seperti `racego.example.com`, subdomain DuckDNS, `localhost`, atau `:80` di belakang tunnel). Caddy mengurus sertifikat Let's Encrypt otomatis bila `SITE_ADDRESS` adalah domain publik. `FRONTEND_URL` diturunkan darinya (`https://$SITE_ADDRESS`) di skrip/compose supaya tidak bisa tidak sinkron.
3. **Hanya Caddy yang membuka port ke luar** (80/443). Postgres, Redis, api, worker, web tidak boleh punya `ports:` ke host; semua lewat jaringan internal Docker. Redis wajib password; Postgres memakai user/password acak, bukan `racego/racego`.
4. **Rahasia** hanya di `.env.prod` (di-ignore Git). Sediakan `.env.prod.example` (tanpa nilai rahasia, dengan komentar) dan `scripts/gen-secrets.sh` yang mengisi otomatis: `POSTGRES_PASSWORD`, `REDIS_PASSWORD`, `QR_PRIVATE_KEY` (dari `keygen` di dalam image). Skrip tidak boleh menimpa nilai yang sudah ada tanpa konfirmasi, dan **kunci QR yang sudah dipakai tidak boleh berubah diam-diam** (tiket yang sudah terbit akan tidak valid); beri peringatan keras di skrip dan dokumentasi.
5. **Pengaturan produksi:** `APP_ENV=production`, `NEXT_PUBLIC_DEV_LOGIN=false`, `AUTO_VERIFY_ORGS=false` (default aman; di mode demo boleh `true` lewat `.env.prod`), `MIDTRANS_IS_PRODUCTION=false` (sandbox) dengan kunci diisi pemilik, `UPLOAD_DIR=/data/uploads` (volume), SMTP diisi pemilik (Gmail App Password atau Resend, port 587).
6. **IP klien dan `TRUSTED_PROXY_HOPS`:** baca `server/realip.go`, tentukan nilai yang benar untuk dua topologi dan dokumentasikan dengan tabel: (a) Caddy langsung di depan API; (b) cloudflared → Caddy → API. Pastikan tidak ada jalur di mana klien bisa memalsukan `CF-Connecting-IP`/`X-Forwarded-For` (mis. pada mode tunnel Caddy tidak boleh terbuka ke publik; pada mode langsung Caddy harus menimpa header, bukan menambahkan). Tulis tes atau skrip uji kecil yang membuktikannya.
7. **Migrasi otomatis** saat deploy lewat service sekali-jalan `migrate` yang harus sukses sebelum `api`/`worker` start (`depends_on: condition: service_completed_successfully`). Seed data contoh **tidak** otomatis; sediakan `make prod-seed` eksplisit (untuk demo) dan jelaskan bahwa tanggal seed relatif terhadap hari itu dan seed tidak menyegarkan data yang sudah ada.
8. **Keandalan:** `restart: unless-stopped`, healthcheck untuk semua service (api memakai `/healthz`), log berotasi (`json-file` dengan `max-size`/`max-file`), proses non-root, `read_only` + tmpfs di mana praktis, batas memori kasar per service (total harus muat di mesin 2 CPU/8–12 GB), dan `stop_grace_period` cukup untuk API/worker berhenti rapi.
9. **Keamanan HTTP di Caddy:** HSTS, `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `X-Frame-Options: DENY`, `Permissions-Policy` minimal (kamera diizinkan untuk origin sendiri karena scanner memakai kamera), kompresi (zstd/gzip), dan batas ukuran body (mis. 4 MB, sedikit di atas batas unggah 3 MB). **Jangan memasang CSP ketat sekarang** (popup Midtrans Snap memuat skrip/iframe dari domain Midtrans; catat CSP Report-Only sebagai pekerjaan lanjutan di roadmap).
10. **Cloudflared** hanya sebagai profil opsional/skrip demo, bukan bagian wajib stack produksi.

## Yang harus dibuat

### Image & compose

- `frontend/Dockerfile` (multi-stage, `output: "standalone"` di `next.config.ts`; periksa kompatibilitas dengan `cacheComponents`/`partialPrefetching` dan `turbopack.root` yang sudah ada; ARG build untuk `NEXT_PUBLIC_*`; user non-root; `.dockerignore`). Tambahkan `.dockerignore` untuk `backend/` juga (jangan menyalin `data/`, `bin/`, `.env`).
- `docker-compose.prod.yml` (nama proyek `racego-prod`, supaya tidak bentrok dengan `docker-compose.yml` development): `postgres`, `redis`, `migrate` (sekali jalan), `api`, `worker`, `web`, `caddy`, dan `cloudflared` (profile `tunnel`). Volume bernama: `pgdata`, `redisdata`, `uploads`, `caddy_data`, `caddy_config`. `api` dan `worker` memakai image backend yang sama; `uploads` terpasang di `api` (menulis) dan hanya itu yang perlu.
- `deploy/Caddyfile` memakai `{$SITE_ADDRESS}` dan aturan no. 1 & 9.
- `.env.prod.example`, `scripts/gen-secrets.sh`, `.gitignore` menambah `.env.prod` dan `backups/`.

### Makefile (pertahankan **tab**, tambah ke `.PHONY` dan komentar `##` agar muncul di `make help`)

`prod-build`, `prod-up` (build + up -d + tunggu sehat), `prod-down`, `prod-logs`, `prod-migrate`, `prod-seed`, `prod-grant` (`EMAIL=… ORG=… ROLE=…`), `prod-verify` (`ORG=…`), `backup`, `restore` (`FILE=…`), `demo`, `demo-down`.

### Mode demo `make demo`

Skrip `scripts/demo.sh`: butuh `docker` dan `cloudflared` (cek, beri petunjuk `brew install cloudflared` bila belum ada). Urutan: jalankan quick tunnel ke Caddy lokal, **baca URL `https://….trycloudflare.com` dari output cloudflared**, tulis ke env sementara (`SITE_ADDRESS=:80` di belakang tunnel, `FRONTEND_URL` = URL tunnel, `TRUSTED_PROXY_HOPS` sesuai keputusan no. 6), lalu naikkan stack produksi, jalankan migrasi (+ seed bila `.env.prod` belum punya data), tunggu `healthz` lewat URL publik, dan cetak URL, cara login, dan pengingat bahwa alamat berubah tiap demo dinyalakan ulang serta tautan di email lama akan mati. Hentikan rapi dengan Ctrl+C atau `make demo-down`. Karena `FRONTEND_URL` berubah tiap demo dan build frontend memakai API relatif, **frontend tidak perlu di-build ulang** per demo (periksa dan pastikan; `NEXT_PUBLIC_*` yang tertanam hanya yang tidak bergantung URL).

### Backup & restore

- `scripts/backup.sh`: `pg_dump -Fc` dari container + arsip volume `uploads` → `backups/racego-YYYYMMDD-HHMMSS/…`; simpan 7 backup terakhir; kode keluar non-nol bila gagal; bisa dipanggil cron (beri contoh baris cron di dokumentasi). `scripts/restore.sh`: pulihkan ke stack yang berjalan dengan konfirmasi eksplisit.
- **Uji nyata**: isi data (buat akun, event, tiket, unggah poster), backup, hancurkan volume (`down -v`), restore, dan buktikan data, poster, dan tiket (QR tetap valid dengan kunci QR yang sama) kembali utuh.

### Dokumentasi `docs/10-deploy.md`

Langkah demi langkah untuk pemula (Bahasa Indonesia), memuat:
1. Prasyarat server (Ubuntu 24.04/Debian, 2 CPU/4 GB+ cukup, Docker + Compose plugin), pasang Docker, firewall (hanya 22/80/443; contoh `ufw`), user non-root, kunci SSH.
2. Mendapatkan alamat web: DuckDNS (gratis; contoh mengarahkan subdomain ke IP server) **atau** domain sendiri (A record). Catat bahwa port 80/443 harus terbuka agar sertifikat bisa terbit.
3. `git clone`, `cp .env.prod.example .env.prod`, `scripts/gen-secrets.sh`, isi `SITE_ADDRESS`, SMTP, Midtrans; `make prod-up`; `make prod-seed` (opsional); `make prod-grant`/`prod-verify` untuk menjadikan akun sendiri owner.
4. Mengisi **Notification URL Midtrans** (`https://<alamat>/v1/webhooks/midtrans`) dan menguji pembayaran sandbox; catat bahwa pengecekan status sudah menjadi cadangan.
5. Update aplikasi (`git pull && make prod-up`), rollback ke commit sebelumnya, melihat log, restart, membersihkan image lama.
6. Backup terjadwal (cron) dan menyalin backup ke luar server; cara restore.
7. Checklist keamanan sebelum dibuka ke publik (dev login mati, `AUTO_VERIFY_ORGS`, kunci QR baru dan dicadangkan, password acak, port tertutup, `TRUSTED_PROXY_HOPS` benar, update OS otomatis).
8. Bagian "Di mana menjalankannya": daftar pilihan **tanpa janji** (Oracle Cloud Always Free: jatah ARM sejak Juni 2026 hanya 2 CPU/12 GB, pendaftaran menolak kartu virtual/prepaid, wajib dicek aturan reklamasi instance idle di dokumentasi resmi Oracle; VPS murah mana pun; laptop + `make demo`). Cantumkan tautan dokumentasi resmi, jangan menyalin angka dari ingatan; beri tanggal "dicek terakhir".
9. Troubleshooting (sertifikat tidak terbit, 502, cookie login hilang, email tidak terkirim, webhook tidak masuk, uploads hilang).

Perbarui juga `docs/06-infra-gratis.md` (arsitektur satu server, tanpa dua VM; Cloudflare Tunnel hanya opsional), `docs/02-arsitektur.md` (diagram deploy), `docs/08-development.md` (rujuk `docs/10-deploy.md`), `docs/09-roadmap.md` (centang deploy/CI; tambah: CSP Report-Only, deploy otomatis, pemantauan/Grafana, waiting room), `docs/README.md` (daftar dokumen), `README.md` (bagian "Deploy"), dan `.env.example` bila perlu.

### CI GitHub Actions `.github/workflows/ci.yml`

Pada push dan pull request: job **backend** (Go sesuai `go.mod`, service container Postgres dan Redis, jalankan migrasi lalu `go vet` + `go test ./...` dengan `RACEGO_TEST_DATABASE_URL` terisi sehingga tes Postgres tidak di-skip), job **frontend** (Node 20/22, `npm ci`, `npm run lint`, `npx next typegen && npx tsc --noEmit`, `npm run build`), job **images** (build kedua Dockerfile tanpa push, dengan cache). Tambahkan lencana status CI di `README.md`. Tidak ada rahasia yang dibutuhkan. Karena tidak bisa dijalankan dari sini, validasi sintaks YAML (mis. `actionlint` bila tersedia) dan pastikan setiap perintah di dalamnya sudah kamu jalankan sendiri secara lokal.

## Di luar cakupan

Terraform/Kubernetes, beberapa server, load balancer, Grafana/Prometheus, deploy otomatis (CD), waiting room, Turnstile, CDN untuk uploads, domain email/SPF/DKIM, pembayaran produksi Midtrans. Jangan dikerjakan sekarang.

## Kriteria selesai (cek satu per satu dan laporkan; tunjukkan bukti nyata)

1. `docker build` backend **dan** frontend sukses dari nol (tanpa cache) dan ukuran image dilaporkan. `make test` dan `cd frontend && npm run build` tetap hijau (tes Postgres tidak di-skip).
2. Stack produksi jalan di laptop: `SITE_ADDRESS=localhost` (Caddy `tls internal` bila perlu), `make prod-up` dari volume kosong sampai semua service sehat, tanpa satu pun `ports:` ke host selain Caddy (tunjukkan hasil `docker compose -f docker-compose.prod.yml ps` dan `docker port`/`lsof`). Postgres dan Redis tidak bisa dijangkau dari host.
3. **Uji browser nyata (Playwright, di folder sementara di luar repo, `ignoreHTTPSErrors` bila perlu; jangan mengubah `package.json`; ambil kode OTP dari log API atau pakai SMTP uji, jangan menyentuh `.env` pribadi maupun mengirim email sungguhan)**: buka `/` → daftar & login → `/daftar-mitra` → buat event, unggah poster, terbitkan (set `AUTO_VERIFY_ORGS=true` untuk uji ini, lalu uji juga `false` + `make prod-verify`) → akun lain membeli tiket (Midtrans mock, kosongkan kunci) → tiket terbit → scanner mengesahkan. Poster tampil (artinya `/uploads` dan `API_URL` kosong benar). Cookie bertanda `Secure`+`HttpOnly`. Konsol browser dan log bersih.
4. Keamanan: `X-User-ID` ditolak (401) di stack produksi; menu "Akun contoh (dev)" tidak ada di bundle; header keamanan dari keputusan no. 9 ada pada respons; `curl` dengan `X-Forwarded-For`/`CF-Connecting-IP` palsu tidak bisa mengakali batas OTP per IP pada **kedua** topologi (buktikan dengan 25 permintaan kode beruntun dari "IP" berbeda yang tetap kena 429 setelah kuota); `.env.prod` dan `backups/` tidak ada di `git status`; tidak ada rahasia di image (`docker history`/`grep`).
5. Backup→hancurkan→restore terbukti (kriteria di bagian Backup), termasuk QR tiket lama tetap valid.
6. Restart tahan banting: `docker compose … restart` seluruh stack (dan `kill -9` pada `api`) saat ada reservasi/ order berjalan → layanan pulih sendiri, kuota di Redis tetap benar (Redis `appendonly`), tidak ada oversell; ulangi burst 3.000 request ke kategori berkuota 200 melalui Caddy (HTTPS lokal) dan hasilnya tepat 200 sukses.
7. `make demo` dicoba bila `cloudflared` terpasang di laptop ini: tampilkan URL tunnel, buka lewat Playwright dari URL publik itu (bukan localhost), login dan lihat beranda berfungsi, lalu `make demo-down` membersihkan semuanya. Bila `cloudflared` tidak terpasang atau jaringan menolak, **katakan itu dengan jelas** dan jangan mengklaim lulus; tunjukkan bagian skrip yang sudah diuji tanpa tunnel (mis. dengan URL palsu).
8. Semua dokumen di atas diperbarui; `make help` menampilkan target baru; dokumentasi `docs/10-deploy.md` bisa diikuti seseorang yang belum pernah deploy (periksa konsistensi nama berkas, variabel, dan perintah dengan yang benar-benar ada).
9. `.github/workflows/ci.yml` valid; setiap perintahnya sudah dijalankan lokal dengan hasil yang sama.

## Cara kerja

- Bertahap, jalankan/uji setiap tahap: Dockerfile frontend + backend → compose + Caddy → env/rahasia → migrasi otomatis → Makefile → keamanan/IP → backup/restore → demo → CI → dokumentasi → verifikasi akhir (kriteria 1–9).
- Jangan mengubah perilaku checkout, pembayaran, tiket, scanner, login, atau portal mitra, kecuali yang mutlak diperlukan agar jalan di produksi (mis. `API_URL` kosong, `output: "standalone"`); semua perubahan semacam itu wajib dilaporkan dan dites.
- Jangan membuat atau mengubah `.env` pribadi, jangan mengirim email sungguhan, jangan memakai kunci Midtrans milik pengguna dalam uji (pakai mock), jangan membuka port ke jaringan luar selain yang dibutuhkan uji (dan matikan setelahnya), jangan menyimpan rahasia di repo atau di log.
- Bila keputusan di dokumen ini bertabrakan dengan kenyataan di kode atau di Docker, pilih opsi paling sederhana yang aman dan catat alasannya di laporan akhir.
- Di akhir: ringkasan perubahan per file, tabel hasil tiap kriteria (berdasarkan uji nyata, tandai yang tidak bisa diuji), daftar keputusan/penyimpangan, dan usulan pesan commit. **Jangan commit dan jangan push.**
