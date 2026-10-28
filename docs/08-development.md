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
make up         # Postgres :5433, Redis :6380, Mailpit :8025
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
| Data contoh terasa basi | `make db-reset` — tanggal seed relatif terhadap hari ini |
| Uji beban | `make db-reset && make loadtest` |

## Konvensi kode

- **Backend**: satu folder per modul di `internal/`. Handler HTTP tipis; logika yang perlu dites dipisah (lihat `checkout/inventory.go`, `ledger/ledger.go`). Semua query mitra memakai `tenant.FromContext(ctx).OrgID`. Uang selalu `int64` rupiah.
- **Migrasi**: tambah file baru `000N_nama.up.sql` + `.down.sql` di `backend/migrations/`; jangan ubah migrasi yang sudah pernah dijalankan di lingkungan lain.
- **Frontend**: `page.tsx` tipis (server component) yang membungkus komponen klien dengan `<Suspense>`; panggilan API lewat `lib/api.ts`. Next.js 16 punya API yang berbeda dari versi lama — baca `frontend/AGENTS.md`.
- Bahasa: teks UI & pesan error untuk pengguna dalam Bahasa Indonesia; nama variabel/fungsi dalam bahasa Inggris.

## Troubleshooting

| Gejala | Penyebab / solusi |
| --- | --- |
| `koneksi database gagal` | Infra belum jalan → `make up`; cek `docker compose ps` |
| `role "racego" does not exist` | Program tersambung ke Postgres lain di Mac, bukan yang di Docker. Pastikan `.env` memakai port **5433** (`cp .env.example .env`), lalu `make down && make up` |
| Port 5433/6380 sudah dipakai | Ubah port di `docker-compose.yml` dan `DATABASE_URL`/`REDIS_ADDR` di `.env` |
| Halaman web "Tidak bisa terhubung ke server" | API belum jalan atau `NEXT_PUBLIC_API_URL` salah |
| Tiket tidak muncul setelah bayar | Worker belum jalan (`make worker`) |
| Kategori selalu `sale_not_open` | Jam penjualan seed sudah lewat 60 hari → `make db-reset` |
| Kamera scanner tidak mau menyala | Browser butuh HTTPS atau `localhost`; dari HP pakai `cloudflared tunnel --url http://localhost:3000` |
| `go: missing go.sum entry` | Jalankan `cd backend && go mod tidy` |
