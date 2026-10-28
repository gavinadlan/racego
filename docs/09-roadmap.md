# 09 · Roadmap

Urut dari yang paling berdampak. Centang bila selesai.

## Fase 1 — melengkapi MVP

- [ ] **Login sungguhan**: email OTP + Google, JWT pendek + refresh token; hapus ketergantungan `X-User-ID`
- [ ] **Profil pelari** (`runner_profiles`): isi sekali, prefill semua peserta
- [ ] **Virtual waiting room** (Redis ZSET + token HMAC) dan Turnstile di tombol Daftar
- [ ] **CRUD event & kategori** dari dashboard mitra (draft → review → published)
- [ ] **Onboarding mitra**: daftar, upload dokumen, rekening, verifikasi super admin
- [ ] **Row-Level Security** Postgres untuk tabel milik mitra
- [ ] Export peserta (CSV/XLSX) untuk mitra
- [ ] Upload poster event & logo penyelenggara (kolom `cover_url`/`logo_url` sudah ada)
- [ ] Undang staf RPC via email

## Fase 2 — keuangan

- [ ] Saldo mitra: tertahan → tersedia (job harian sesuai kebijakan)
- [ ] Pengajuan payout + persetujuan maker-checker + Midtrans Payouts (sandbox)
- [ ] Rekonsiliasi harian dengan laporan Midtrans; ganti estimasi biaya PG dengan angka asli
- [ ] Refund (manual oleh mitra) + jurnal ledger refund

## Fase 3 — skala & operasional

- [ ] Scanner offline (IndexedDB + verifikasi kunci publik)
- [ ] QR dinamis hari-H
- [ ] Observabilitas: OpenTelemetry → Prometheus/Grafana, dashboard live war tiket
- [ ] CI GitHub Actions: `make test` + build image
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
