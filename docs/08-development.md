# 08 · Panduan development

## Pasang alat (macOS)

```bash
# Homebrew: https://brew.sh
brew install go node k6 cloudflared
brew install --cask orbstack        # atau Docker Desktop; keduanya menyediakan `docker compose`
```

Versi minimal: Go 1.27 (`backend/go.mod` memasang `toolchain go1.27.3`: Go ≥ 1.21 yang lebih lama otomatis mengunduhnya), Node.js 20, Docker Compose v2. Image produksi (`backend/Dockerfile`) dan CI memakai Go 1.27.3 yang sama.

## Pertama kali

```bash
cd ~/code/RaceGo
make setup      # buat .env + go.sum + node_modules
make up         # Postgres :55432, Redis :6380, Mailpit :8025
make migrate
make seed
make dev        # API :8080, worker, web :3000
```

`make dev` menjalankan tiga proses sekaligus; bila lebih suka terminal terpisah, jalankan `make api`, `make worker`, dan `make web` masing-masing.

## Sehari-hari

| Mau apa | Perintah |
| --- | --- |
| Lihat semua perintah | `make help` |
| Tes backend | `make test-backend` |
| Lint + type-check frontend | `make lint-web` |
| Mulai dari data bersih | `make db-reset` |
| Lihat email yang terkirim | http://localhost:8025 |
| Masuk ke database | `docker compose exec postgres psql -U racego` |
| Lihat stok di Redis | `docker compose exec redis redis-cli get '{cat:cccccccc-0000-0000-0000-000000000021}:stock'` |
| Beri peran mitra ke akunmu | `make grant EMAIL=… ORG=jakarta-runners ROLE=owner` |
| Jadikan akunmu super admin (halaman `/admin`) | `make superadmin EMAIL=…` (sudah pernah login) |
| Verifikasi penyelenggara tanpa alur pengajuan (jalan pintas) | `make verify ORG=slug` (catatan: org ini tidak punya rekening pencairan) |
| Rilis saldo tertahan tanpa menunggu masa tahan | `make dev-release ORG=slug` (hanya `APP_ENV=development`) |
| Lihat dokumen verifikasi (terenkripsi) | folder `backend/data/kyc/` (`KYC_DIR`, tidak di-commit; isinya ciphertext) |
| Lihat gambar unggahan | folder `backend/data/uploads/` (`UPLOAD_DIR`, tidak di-commit) |
| Data contoh terasa basi | `make db-reset` — tanggal seed relatif terhadap hari ini |
| Uji beban | `make db-reset && make loadtest` (event tanpa antrian, butuh k6) |
| Coba antrian virtual di browser | `make dev`, lalu `make queue-demo` (event demo kuota 50, penjualan 3 menit lagi) dan `node scripts/queue-sim.mjs bots --users 500 --event <id>` |
| Uji beban antrian virtual | `make queue-load` (10.000 pengguna palsu, kuota 200; ubah dengan `USERS=… QUOTA=…`). Menyalakan API & worker sendiri di port 18080 |

## Masuk (login) di laptop

1. Buka http://localhost:3000, klik **Masuk**, isi email apa saja.
2. Ambil kodenya: di development kode **dicatat di log API** (`make dev` / `make api`, baris `kode OTP (dev)`). Email-nya juga terkirim ke Mailpit (http://localhost:8025) bila `SMTP_ADDR=localhost:1025`, atau ke inbox sungguhan bila `.env` memakai SMTP Gmail/Resend.
3. Akun baru diarahkan ke **Lengkapi profil**, lalu kembali ke halaman asal.

Akun baru hanyalah pelari. Untuk jadi penyelenggara: menu akun → **Jadi penyelenggara** (`/daftar-mitra`). Di development `AUTO_VERIFY_ORGS=true`, jadi org baru langsung bisa menerbitkan event (tab **Verifikasi** menulis "Diverifikasi otomatis (mode development)").

**Mencoba alur verifikasi sungguhan** tanpa mengubah `.env`: jalankan API dengan override di baris perintah, mis. `make api AUTO_VERIFY_ORGS=false` (worker & web seperti biasa). Lalu: akun A → **Jadi penyelenggara** → tab **Verifikasi**, isi 4 langkah dan kirim; akun B masuk sekali lalu `make superadmin EMAIL=b@…`; B buka menu akun → **Admin** (`/admin`) untuk menyetujui/menolak. Email status terkirim lewat worker (lihat Mailpit). Pratinjau dokumen memakai cookie sesi, jadi gunakan login email sungguhan, bukan pemilih **Akun contoh (dev)**. `KYC_ENC_KEY` kosong di development = kunci bawaan (ada peringatan di log); dokumen yang dibuat dengan kunci itu tidak terbaca bila kuncinya diganti. Staf ditambahkan owner dari tab **Tim** (orangnya harus sudah pernah login).

Cara cepat lewat terminal (email harus sudah pernah login):

```bash
make grant EMAIL=kamu@gmail.com ORG=jakarta-runners ROLE=owner   # owner | admin | finance | rpc_staff
```

Pemilih **Akun contoh (dev)** di header (Budi, Sari, Andi, Rina dari seed) hanya muncul bila `NEXT_PUBLIC_DEV_LOGIN=true` dan hanya diterima backend saat `APP_ENV=development`. Memilih akun contoh otomatis keluar dari sesi sungguhan.

Tes yang butuh Postgres (`SessionStore`, reconciler, uji portal mitra di `internal/server/portal_test.go`: isolasi tenant, aturan terbit & penjualan, kuota bersamaan, uji antrian lewat HTTP di `internal/server/queue_test.go`, dan uji verifikasi mitra di `internal/server/kyc_test.go`) jalan bila `RACEGO_TEST_DATABASE_URL` diisi; `make test` mengisinya dengan `DATABASE_URL` (jadi butuh `make up`). Uji portal membuat lalu menghapus datanya sendiri (email `…@uji.test`):

```bash
cd backend && RACEGO_TEST_DATABASE_URL=$DATABASE_URL go test ./...
cd backend && go test -race -run TestConcurrentJoinAdmitReserve ./internal/queue/   # 5.000 join + pemanggilan + reservasi paralel
```

## Mencoba keuangan mitra (pencairan & refund, mode mock)

Default `DISBURSE_MODE=mock`: tidak ada panggilan ke Midtrans Payouts dan tidak ada uang sungguhan. Tanpa mengubah `.env`, override di baris perintah, mis. `make api AUTO_VERIFY_ORGS=false` + `make worker`:

1. Mitra mengikuti alur verifikasi sampai disetujui super admin (persetujuan membuat rekening pencairan terenkripsi). Org yang di-`make verify` tidak punya rekening.
2. Pelari membeli tiket (tombol *Simulasikan bayar berhasil*). Tab **Keuangan** (`/org/{slug}/keuangan`, owner & finance) menampilkan saldo **Tertahan**.
3. `make dev-release ORG=slug` → saldo pindah ke **Tersedia** tanpa menunggu 3 hari setelah lomba.
4. Ajukan pencairan → super admin buka `/admin/pencairan` → Setujui. Worker (tiap 15 detik) mengirim ke Disburser mock: `processing` lalu `completed` setelah `DISBURSE_MOCK_DELAY`.
5. Uji gagal: rekening berakhiran **`0000`** selalu gagal di mode mock (saldo kembali). Uji refund manual: alasan refund memuat kata **"manual"** → status *Perlu kirim manual* → `/admin/refund` → **Tandai sudah dikembalikan**.
6. `/admin/keuangan` menampilkan ringkasan platform dan pemeriksaan integritas ledger.

Tes keuangan: `internal/ledger/*_test.go` (angka jurnal), `internal/finance/gateway_test.go` (klien Iris & refund Midtrans dengan server HTTP palsu), `internal/server/finance_test.go` (alur lengkap lewat router + Postgres: peran, konkurensi, refund, laporan, integritas, trigger). Ledger append-only, jadi pembersih data tes menghapus jurnalnya dengan `session_replication_role = replica` (butuh user DB superuser, seperti `racego` di Docker).

## Daftar peserta & export CSV

Tab Event → **Peserta** (`/org/{slug}/events/{id}/peserta`): owner & admin melihat tabel (cari nama/email/BIB, filter kategori & status tiket, 50 per halaman) dan **Unduh CSV**; staf RPC hanya ringkasan (per kategori, ukuran jersey, status); keuangan ditolak. Nomor identitas selalu `••••1234`. Setiap unduhan tercatat di `audit_logs` (`participants.download`). Peserta muncul setelah tiket terbit (beli tiket + *Simulasikan bayar berhasil* + worker jalan).

Tes: `internal/server/participants_test.go` (peran, tenant, isolasi, injeksi rumus, filter, refund, audit, batas laju) dan `TestParticipantsExport10k` (10.000 peserta sintetis di-stream; log tes mencetak kenaikan heap puncak). Penulis CSV ada di `internal/csvx`: jenis sel ditentukan tipe Go (`string` = teks disanitasi, `int`/`int64` = angka apa adanya).

## Anti-bot Turnstile (opsional)

Mati secara default. Untuk mencoba di laptop pakai kunci uji resmi Cloudflare ([daftar kunci uji](https://developers.cloudflare.com/turnstile/troubleshooting/testing/)), tanpa mengubah `.env`:

```bash
make api TURNSTILE_SITE_KEY=1x00000000000000000000AA TURNSTILE_SECRET_KEY=1x0000000000000000000000000000000AA
make web NEXT_PUBLIC_TURNSTILE_SITE_KEY=1x00000000000000000000AA
```

Halaman **Masuk** dan tombol **Masuk antrian** menampilkan widget; tombol terkunci ("Memeriksa…") sampai token ada. Site key uji `2x00000000000000000000AB` selalu gagal (uji pesan error). Hanya satu kunci terisi → API menolak start. Tes otomatis tidak pernah memanggil Cloudflare: backend memakai server siteverify palsu (`internal/turnstile`, `internal/server/turnstile_test.go`); `TURNSTILE_VERIFY_URL` bisa diarahkan ke server palsu lokal untuk uji browser.

## Undangan staf

Tab **Tim** (`/org/{slug}/tim`, owner & admin) → *Undang anggota*. Email undangan terkirim ke Mailpit (http://localhost:8025): buka tautan `/undangan/<token>`, masuk dengan email yang sama, lalu *Terima undangan*. Tautan **tidak** dicatat di log API (beda dengan kode OTP dev). Untuk mencoba kedaluwarsa: `UPDATE org_invitations SET expires_at = now() - interval '1 minute' WHERE email = '…'`. Tes: `internal/server/invitations_test.go` (matriks peran, tenant, email beda, terima paralel, token tidak tersimpan mentah, batas laju, audit).

## Scanner offline

Masuk sebagai owner/admin/staf race pack → event → **Scan race pack** → *Siapkan mode offline*. Lalu:

- **Chrome DevTools**: tab *Network* → *No throttling* ganti ke *Offline*. Pindai/tempel QR (isi `qr_token` dari `GET /v1/me/tickets` pembeli), lihat "Belum terkirim" bertambah. Kembali *No throttling* → antrean terkirim otomatis. Data perangkat: *Application* → *IndexedDB* → `racego-scanner` (`manifests`, `tickets`, `queue`).
- **Jangan muat ulang saat offline**: halaman scanner hanya bisa dibuka dengan internet (peramban meminta konfirmasi). Antrean tetap tersimpan bila tab tertutup.
- **Dua perangkat**: dua profil/jendela samaran, keduanya siapkan mode offline, offline, pindai tiket yang sama → online-kan satu per satu: yang kedua mendapat "Perlu ditinjau".
- **Playwright** (di folder di luar repo): `context.setOffline(true/false)`, satu context per perangkat, `page.clock.install()` + `clock.fastForward("25:00:00")` untuk menguji kedaluwarsa, `--use-fake-device-for-media-stream --use-file-for-fake-video-capture=qr.y4m` untuk jalur kamera (video QR dibuat dengan `ffmpeg -loop 1 -i qr.png -t 3 -pix_fmt yuv420p qr.y4m`).
- Masa berlaku data: `SCAN_MANIFEST_TTL_HOURS` (bawaan 24). Tes backend: `internal/server/offline_scan_test.go` (manifest: peran, minimisasi kunci JSON, `?since`, 10.000 tiket; batch: idempoten, paralel dua perangkat, QR palsu, event salah, refund, batas).

## Menjalankan versi produksi

Untuk mencoba stack produksi (Docker Compose + Caddy, HTTPS lokal) atau men-deploy ke server, lihat [10-deploy.md](10-deploy.md). Singkatnya: `cp .env.prod.example .env.prod && scripts/gen-secrets.sh`, biarkan `SITE_ADDRESS=localhost`, lalu `make prod-up` dan buka https://localhost (sertifikat lokal Caddy; browser akan memperingatkan). `make demo` membukanya ke internet lewat Cloudflare quick tunnel. Stack produksi memakai proyek Docker `racego-prod` dan volume sendiri, jadi tidak mengganggu `make up`.

CI (`.github/workflows/ci.yml`) menjalankan migrasi + seed di Postgres baru, `go vet`, `go test` (gagal bila ada tes yang di-skip), lint/type-check/build frontend di Node 20 dan 22, serta build kedua image.

## Konvensi kode

- **Backend**: satu folder per modul di `internal/`. Handler HTTP tipis; logika yang perlu dites dipisah (lihat `checkout/inventory.go`, `ledger/ledger.go`). Semua query mitra memakai `tenant.FromContext(ctx).OrgID`. Uang selalu `int64` rupiah.
- **Migrasi**: tambah file baru `000N_nama.up.sql` + `.down.sql` di `backend/migrations/`; jangan ubah migrasi yang sudah pernah dijalankan di lingkungan lain.
- **Frontend**: `page.tsx` tipis (server component) yang membungkus komponen klien dengan `<Suspense>`; panggilan API lewat `lib/api.ts`. Next.js 16 punya API yang berbeda dari versi lama — baca `frontend/AGENTS.md`.
- Bahasa: teks UI & pesan error untuk pengguna dalam Bahasa Indonesia; nama variabel/fungsi dalam bahasa Inggris.

## Troubleshooting

| Gejala | Penyebab / solusi |
| --- | --- |
| `koneksi database gagal` | Infra belum jalan → `make up`; cek `docker compose ps` |
| `role "racego" does not exist` | Program tersambung ke Postgres lain di Mac, bukan yang di Docker. Pastikan `.env` memakai port **55432** (`cp .env.example .env`), lalu `make down && make up` |
| `password authentication failed for user "racego"` padahal password benar | Ada Postgres lain di Mac yang mendengarkan di `localhost` pada port yang sama; macOS mengarahkan koneksi ke situ, bukan ke Docker. Cek dengan `sudo lsof -nP -iTCP:55432 -sTCP:LISTEN`, lalu pindahkan port (baris di bawah) |
| Port 55432/6380 sudah dipakai | Ubah port di `docker-compose.yml` dan `DATABASE_URL`/`REDIS_ADDR` di `.env` |
| Halaman web "Tidak bisa terhubung ke server" | API belum jalan atau `NEXT_PUBLIC_API_URL` salah |
| Tiket tidak muncul setelah bayar | Worker belum jalan (`make worker`). Di sandbox: buka halaman order-nya (status dicek langsung ke Midtrans) atau tunggu ±1 menit |
| Kategori selalu `sale_not_open` | Jam penjualan seed sudah lewat 60 hari → `make db-reset` |
| Kode OTP tidak datang | Lihat log API (`kode OTP (dev)`); 502 `mail_failed` = SMTP salah (cek `SMTP_*` di `.env` atau pakai Mailpit `localhost:1025`) |
| Tetap "belum masuk" setelah login | `FRONTEND_URL` harus persis origin web (mis. `http://localhost:3000`, bukan `127.0.0.1`) agar cookie & CORS cocok |
| Kamera scanner tidak mau menyala | Browser butuh HTTPS atau `localhost`; dari HP pakai `cloudflared tunnel --url http://localhost:3000` |
| `go: missing go.sum entry` | Jalankan `cd backend && go mod tidy` |
| `go: downloading go1.27.3` saat pertama build | Normal: Go lokal lebih lama dari `toolchain` di `go.mod`, jadi toolchain diunduh sekali |
| Scanner: "Penyimpanan offline diblokir" | Mode samaran/izin situs memblokir IndexedDB; scan online tetap jalan |
| Scanner: "belum mendukung verifikasi tanda tangan Ed25519" | Peramban terlalu lama untuk mode offline (perlu Chrome/Edge 137+, Safari 17+, Firefox 129+) |
| `make db-reset` gagal karena ledger | Reset memakai migrasi `down` (DROP trigger lalu tabel), tidak menghapus baris ledger; hanya jalan di `APP_ENV=development` |
