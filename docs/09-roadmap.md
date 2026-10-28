# 09 · Roadmap

Urut dari yang paling berdampak. Centang bila selesai.

## Fase 1 — melengkapi MVP

- [x] **Login email OTP**: kode 6 digit, sesi cookie HttpOnly di Postgres, `X-User-ID` hanya di development
- [ ] **Login Google**
- [ ] **Profil pelari** (`runner_profiles`): isi sekali, prefill semua peserta
- [ ] **Virtual waiting room** (Redis ZSET + token HMAC) dan Turnstile di tombol Daftar
- [x] **CRUD event & kategori** dari dashboard mitra (draft → published → closed, kunci harga setelah ada penjualan, kuota disesuaikan atomik di Redis)
- [x] **Onboarding mitra versi dasar**: daftar dari web, `kyc_status` + `make verify`
- [ ] Verifikasi dokumen & rekening mitra, peran super admin
- [ ] **Row-Level Security** Postgres untuk tabel milik mitra
- [ ] Export peserta (CSV/XLSX) untuk mitra
- [x] Upload poster event & logo penyelenggara (disk lokal, EXIF dibuang)
- [ ] Pembersihan berkala gambar yang tidak dipakai; pindah ke S3/R2
- [x] Undang staf versi dasar (tambah anggota yang sudah pernah login)
- [ ] Undangan staf via email

## Fase 2 — keuangan

- [ ] Saldo mitra: tertahan → tersedia (job harian sesuai kebijakan)
- [ ] Pengajuan payout + persetujuan maker-checker + Midtrans Payouts (sandbox)
- [ ] Rekonsiliasi harian dengan laporan Midtrans; ganti estimasi biaya PG dengan angka asli
- [ ] Refund (manual oleh mitra) + jurnal ledger refund

## Fase 3 — skala & operasional

- [ ] Scanner offline (IndexedDB + verifikasi kunci publik)
- [ ] QR dinamis hari-H
- [ ] Observabilitas: OpenTelemetry → Prometheus/Grafana, dashboard live war tiket
- [x] CI GitHub Actions: tes backend dengan Postgres, lint/build frontend, build image
- [x] Paket deploy satu server: Docker Compose + Caddy (HTTPS otomatis), backup/restore teruji, `make demo` via Cloudflare quick tunnel
- [ ] Deploy otomatis (CD) dari GitHub ke server
- [ ] Content-Security-Policy, mulai dari mode `Report-Only` (perlu mengizinkan skrip & iframe Midtrans Snap)
- [ ] Pemantauan stack produksi: uptime check + alert (Grafana, lihat butir observabilitas)
- [ ] Ballot/undian untuk event super-favorit
- [ ] Notifikasi WhatsApp

## Sudah selesai (scaffold awal)

- [x] Monorepo, docker-compose, Makefile, migrasi + seed
- [x] Reservasi atomik Redis (Lua) + timer + pengembalian kuota + bayar telat
- [x] Order idempotent, Midtrans Snap (sandbox) & mode mock
- [x] Webhook idempotent + signature + transactional outbox + worker
- [x] Tiket QR Ed25519 + BIB otomatis + email
- [x] Scanner race pack (kamera, manual, anti dobel scan) + isolasi multi-tenant
- [x] Ledger double-entry
- [x] Skrip uji beban k6
- [x] Beranda baru, kalender bulanan/daftar, arsip, direktori & profil penyelenggara, halaman untuk penyelenggara
- [x] Identitas visual RaceGo (motif BIB)
