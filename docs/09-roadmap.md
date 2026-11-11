# 09 · Roadmap

Urut dari yang paling berdampak. Centang bila selesai.

## Fase 1 — melengkapi MVP

- [x] **Login email OTP**: kode 6 digit, sesi cookie HttpOnly di Postgres, `X-User-ID` hanya di development
- [ ] **Login Google**
- [ ] **Profil pelari** (`runner_profiles`): isi sekali, prefill semua peserta
- [x] **Antrian virtual (waiting room)** per event: lobi + urutan acak, pemanggilan bertahap, pass HMAC, ringkasan langsung untuk mitra
- [x] **Turnstile (opsional)** di permintaan kode OTP & saat masuk antrean (bukan di checkout); menyulitkan skrip sederhana, tidak menghentikan bot canggih
- [ ] **Anti multi-akun**: batas per NIK, deteksi akun/perangkat ganda (antrian & Turnstile belum mencegahnya)
- [x] **CRUD event & kategori** dari dashboard mitra (draft → published → closed, kunci harga setelah ada penjualan, kuota disesuaikan atomik di Redis)
- [x] **Onboarding mitra versi dasar**: daftar dari web, `kyc_status` + `make verify`
- [x] **Verifikasi mitra (KYC)**: identitas, dokumen per jenis penyelenggara, rekening bank; terenkripsi (AES-256-GCM), akses tercatat; super admin meninjau di `/admin`, tolak dengan alasan + ajukan ulang
- [ ] Kebijakan retensi & job penghapusan/anonimisasi data verifikasi
- [ ] Rotasi `KYC_ENC_KEY` (format `v1:` sudah siap untuk versi kunci baru)
- [ ] Pencabutan/penangguhan org terverifikasi
- [ ] Verifikasi ulang saat owner mengganti rekening bank
- [ ] Notifikasi super admin saat ada pengajuan baru
- [ ] **Row-Level Security** Postgres untuk tabel milik mitra
- [x] Export peserta CSV untuk mitra: halaman Peserta (cari, filter, ringkasan jersey/status), nomor identitas tersamar, unduhan di-stream, dibatasi & tercatat di audit
- [ ] Export XLSX, kolom peserta yang bisa dikustomisasi
- [x] Upload poster event & logo penyelenggara (disk lokal, EXIF dibuang)
- [ ] Pembersihan berkala gambar yang tidak dipakai; pindah ke S3/R2
- [x] Undangan staf via email: owner (admin/keuangan/staf race pack) dan admin (staf race pack), akun belum ada pun bisa; tautan sekali pakai 7 hari, kirim ulang/cabut, terima dengan email yang sama (menggantikan "tambah anggota yang sudah pernah login")
- [ ] Undangan massal (CSV), undangan via WhatsApp/SMS

## Fase 2 — keuangan

- [x] Saldo mitra: tertahan → tersedia `PAYOUT_HOLD_DAYS` setelah hari lomba (worker tiap jam, `make dev-release`)
- [x] Pengajuan pencairan + persetujuan maker-checker super admin + Midtrans Payouts (mock & klien Iris teruji server palsu; belum dicoba ke sandbox Iris asli)
- [x] Refund per order (diajukan mitra / dibuat super admin, disetujui super admin), tiket batal, jurnal ledger refund, refund manual untuk metode tanpa API
- [x] Laporan penjualan & mutasi saldo (CSV aman injeksi rumus), ringkasan keuangan platform + pemeriksaan integritas ledger
- [x] Ledger append-only dijaga trigger database
- [ ] Ganti rekening pencairan (verifikasi ulang)
- [ ] Refund parsial (per peserta)
- [ ] Refund massal saat event dibatalkan
- [ ] Pengembalian kuota ke penjualan setelah refund
- [ ] Rekonsiliasi settlement: harian dengan laporan settlement Midtrans; ganti estimasi biaya PG dengan angka asli
- [ ] Biaya transfer pencairan (siapa menanggung, jurnal sendiri)
- [ ] 2FA super admin (wajib sebelum uang sungguhan)
- [ ] Notifikasi webhook Iris (status pencairan tanpa menunggu cek berkala)
- [ ] Batas harian pencairan per org & platform
- [ ] Kupon & harga bertahap (early bird)

## Fase 3 — skala & operasional

- [x] Scanner offline: manifest minimal ke IndexedDB, verifikasi Ed25519 di perangkat, antrean + sinkron batch idempoten, bentrokan antar-perangkat "Perlu ditinjau", data dihapus saat logout/kedaluwarsa
- [ ] Enkripsi data scanner offline dengan PIN
- [ ] Halaman scanner yang bisa dimuat ulang tanpa internet (service worker; menunggu dukungan offline resmi Next.js)
- [ ] QR dinamis hari-H
- [x] Metrik dasar: `/metrics` Prometheus di listener terpisah (HTTP per pola rute, reservasi, antrean, order, outbox, pool DB, Redis, email, pencairan/refund), dashboard Grafana lokal opsional (`make obs-up`)
- [x] Uji beban k6 dengan angka: `browse`, `war`, `war-noqueue`, `soak`, chaos ringan, pemeriksaan integritas otomatis; hasil di [11-performa.md](11-performa.md)
- [ ] Sisa observabilitas: alerting (Alertmanager), tracing terdistribusi (OpenTelemetry), log terpusat, metrik per event untuk mitra
- [ ] Uji beban dari mesin terpisah dan lewat jaringan nyata (sekarang generator dan server berbagi CPU)
- [x] CI GitHub Actions: tes backend dengan Postgres, lint/build frontend, build image
- [x] Paket deploy satu server: Docker Compose + Caddy (HTTPS otomatis), backup/restore teruji, `make demo` via Cloudflare quick tunnel
- [ ] Deploy otomatis (CD) dari GitHub ke server
- [ ] Content-Security-Policy, mulai dari mode `Report-Only` (perlu mengizinkan skrip & iframe Midtrans Snap)
- [ ] Pemantauan stack produksi: uptime check + alert (Grafana, lihat butir observabilitas)
- [x] Absen hari-H (scanner mode absen, offline, jendela waktu) dan undian doorprize spin BIB ([12-undian](12-undian.md))
- [ ] Sisa undian: ballot/undian tiket untuk event super-favorit, ritual undian dengan komit-ungkap acak publik, undian dengan syarat finis/waktu tempuh, notifikasi pemenang (email/WhatsApp)
- [ ] Antrian lintas event (satu antrian untuk beberapa event yang dibuka bersamaan)
- [ ] Halaman tunggu statis dari CDN; notifikasi "giliranmu" lewat email/WhatsApp
- [ ] Notifikasi WhatsApp

## Sudah selesai (scaffold awal)

- [x] Monorepo, docker-compose, Makefile, migrasi + seed
- [x] Reservasi atomik Redis (Lua) + timer + pengembalian kuota + bayar telat
- [x] Order idempotent, Midtrans Snap (sandbox) & mode mock
- [x] Webhook idempotent + signature + transactional outbox + worker
- [x] Tiket QR Ed25519 + BIB otomatis + email
- [x] Scanner race pack (kamera, manual, anti dobel scan) + isolasi multi-tenant
- [x] Ledger double-entry
- [x] Skrip uji beban k6 (diganti skenario lengkap di `loadtest/`, lihat Fase 3)
- [x] Beranda baru, kalender bulanan/daftar, arsip, direktori & profil penyelenggara, halaman untuk penyelenggara
- [x] Identitas visual RaceGo (motif BIB)
