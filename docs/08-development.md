# 08 · Panduan development

## Pasang alat (macOS)

```bash
# Homebrew: https://brew.sh
brew install go node k6 cloudflared
brew install --cask orbstack        # atau Docker Desktop; keduanya menyediakan `docker compose`
```

Versi minimal: Go 1.24, Node.js 20, Docker Compose v2.

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
| Verifikasi penyelenggara (bila `AUTO_VERIFY_ORGS=false`) | `make verify ORG=slug` |
| Lihat gambar unggahan | folder `backend/data/uploads/` (`UPLOAD_DIR`, tidak di-commit) |
| Data contoh terasa basi | `make db-reset` — tanggal seed relatif terhadap hari ini |
| Uji beban | `make db-reset && make loadtest` |

## Masuk (login) di laptop

1. Buka http://localhost:3000, klik **Masuk**, isi email apa saja.
2. Ambil kodenya: di development kode **dicatat di log API** (`make dev` / `make api`, baris `kode OTP (dev)`). Email-nya juga terkirim ke Mailpit (http://localhost:8025) bila `SMTP_ADDR=localhost:1025`, atau ke inbox sungguhan bila `.env` memakai SMTP Gmail/Resend.
3. Akun baru diarahkan ke **Lengkapi profil**, lalu kembali ke halaman asal.

Akun baru hanyalah pelari. Untuk jadi penyelenggara: menu akun → **Jadi penyelenggara** (`/daftar-mitra`). Di development `AUTO_VERIFY_ORGS=true`, jadi org baru langsung bisa menerbitkan event; set `false` untuk mencoba alur verifikasi, lalu `make verify ORG=slug`. Staf ditambahkan owner dari tab **Tim** (orangnya harus sudah pernah login).

Cara cepat lewat terminal (email harus sudah pernah login):

```bash
make grant EMAIL=kamu@gmail.com ORG=jakarta-runners ROLE=owner   # owner | admin | finance | rpc_staff
```

Pemilih **Akun contoh (dev)** di header (Budi, Sari, Andi, Rina dari seed) hanya muncul bila `NEXT_PUBLIC_DEV_LOGIN=true` dan hanya diterima backend saat `APP_ENV=development`. Memilih akun contoh otomatis keluar dari sesi sungguhan.

Tes yang butuh Postgres (`SessionStore`, reconciler, dan uji portal mitra di `internal/server/portal_test.go`: isolasi tenant, aturan terbit & penjualan, kuota bersamaan) jalan bila `RACEGO_TEST_DATABASE_URL` diisi. Uji portal membuat lalu menghapus datanya sendiri (email `…@uji.test`):

```bash
cd backend && RACEGO_TEST_DATABASE_URL=$DATABASE_URL go test ./...
```

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
