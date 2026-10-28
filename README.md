# RaceGo

[![CI](https://github.com/gavinadlan/racego/actions/workflows/ci.yml/badge.svg)](https://github.com/gavinadlan/racego/actions/workflows/ci.yml)

Platform tiket event lari **multi-penyelenggara**: banyak company/komunitas menjual tiket fun run 5K, 10K, half marathon, dan full marathon di satu web. Pembayaran lewat **Midtrans**, tiket **QR** terbit otomatis setelah lunas, dan staf mitra men-scan QR saat pengambilan race pack — semuanya dirancang tahan **war tiket** (puluhan ribu orang di detik yang sama).

> Proyek pribadi / portofolio. Infra 100% gratis, pembayaran memakai Midtrans **sandbox** atau mode **mock**.

## Isi repo

```
RaceGo/
├── backend/            API + worker (Go, modular monolith)
│   ├── cmd/            api · worker · migrate · keygen · healthcheck
│   ├── internal/       checkout · payment · ticket · ledger · tenant · event · worker · ...
│   └── migrations/     skema SQL + seed data contoh
├── frontend/           Web Next.js 16 (pelari, dashboard mitra, scanner race pack)
├── docs/               PRD, arsitektur, war tiket, pembayaran, tiket QR, infra gratis, API, deploy, ADR
├── loadtest/           Skrip k6 uji beban war tiket
├── deploy/Caddyfile    Reverse proxy produksi (HTTPS otomatis, header keamanan)
├── scripts/            gen-secrets · backup · restore · demo · check-ip-spoof
├── docker-compose.yml       Postgres · Redis · Mailpit untuk development
├── docker-compose.prod.yml  Stack produksi lengkap (caddy, web, api, worker, postgres, redis)
├── .github/workflows/  CI: tes backend + Postgres, build frontend, build image
└── Makefile            Semua perintah sehari-hari (`make help`)
```

## Mulai cepat (macOS)

Prasyarat: **Go ≥ 1.24**, **Node.js ≥ 20**, **Docker** (Docker Desktop / OrbStack / Colima). Detail di [docs/08-development.md](docs/08-development.md).

```bash
make setup     # buat .env, unduh dependensi Go & npm (sekali saja)
make up        # nyalakan Postgres, Redis, Mailpit
make migrate   # buat tabel
make seed      # isi data contoh: 5 penyelenggara, 17 event, 4 user
make dev       # jalankan API (:8080) + worker + web (:3000)
```

Buka http://localhost:3000 lalu klik **Masuk**: isi email, kode 6 digit muncul di log `make dev` (dan di Mailpit/Gmail). Jadikan akunmu staf/owner mitra dengan `make grant EMAIL=kamu@gmail.com ORG=jakarta-runners ROLE=owner`.

Atau pakai akun contoh di menu **Akun contoh (dev)** (kanan atas):

| User | Peran | Coba ini |
| --- | --- | --- |
| Budi | Pelari | Cari race di beranda → daftar 5K untuk 2 orang → simulasikan bayar → lihat tiket BIB di **Tiket saya** |
| Sari | Owner Jakarta Runners | Dashboard mitra: penjualan & progres race pack |
| Andi | Staf RPC Jakarta | **Scan race pack**: QR valid → hijau, scan ulang → kuning |
| Rina | Owner Bandung Trail | Coba buka data Jakarta → ditolak (isolasi multi-tenant) |
| Akunmu sendiri | Calon mitra | Menu akun → **Jadi penyelenggara** → buat event (kategori, poster) → **Terbitkan** → event muncul di beranda; tambah staf di tab **Tim** |

Email tiket tertangkap di Mailpit: http://localhost:8025. Postgres berjalan di port **55432** dan Redis di **6380** (bukan port bawaan) supaya tidak bentrok dengan instalasi lain di Mac.

## Perintah penting

| Perintah | Fungsi |
| --- | --- |
| `make test` | Tes backend (termasuk 1.000 pembeli berebut 50 slot → tepat 50 sukses) + lint & type-check frontend |
| `make db-reset` | Hapus semua data lalu migrasi + seed ulang |
| `make grant EMAIL=… ORG=… ROLE=…` | Beri akun sungguhan peran di penyelenggara |
| `make verify ORG=…` | Verifikasi penyelenggara agar bisa menerbitkan event (bila `AUTO_VERIFY_ORGS=false`) |
| `make keygen` | Buat kunci Ed25519 untuk tanda tangan QR |
| `make loadtest` | Uji beban war tiket dengan k6 |
| `make prod-up` / `make demo` | Stack produksi di server / demo dari laptop (lihat [Deploy](#deploy)) |

## Deploy

Satu perintah menjalankan seluruh aplikasi dalam mode produksi di server Linux mana pun yang punya Docker, dengan HTTPS otomatis (Caddy + Let's Encrypt) di satu alamat:

```bash
cp .env.prod.example .env.prod
scripts/gen-secrets.sh        # password acak + kunci QR (cadangkan .env.prod!)
nano .env.prod                # SITE_ADDRESS, SMTP, Midtrans sandbox
make prod-up                  # build, migrasi otomatis, tunggu semua sehat
```

Demo sementara dari laptop tanpa server atau domain: `make demo` (butuh `cloudflared`; alamat `https://….trycloudflare.com` berubah setiap dinyalakan). Backup terjadwal: `make backup` / `make restore FILE=…`. Panduan langkah demi langkah untuk pemula, checklist keamanan, dan troubleshooting: [docs/10-deploy.md](docs/10-deploy.md).

## Status

| Sudah jalan | Belum (lihat [docs/09-roadmap.md](docs/09-roadmap.md)) |
| --- | --- |
| Login email + kode OTP, sesi cookie, halaman akun | Login Google |
| Beranda: pencarian, race terdekat + hitung mundur, segera dibuka, kalender, arsip | |
| Kalender bulanan & daftar, arsip race, halaman penyelenggara | Verifikasi dokumen & rekening mitra |
| Multi-tenant (mitra, peran, isolasi data) | |
| Reservasi atomik Redis + timer + pengembalian kuota | Waiting room / antrian virtual |
| Order idempotent, Midtrans Snap (sandbox) & mode mock | Pencairan dana via Midtrans Payouts |
| Webhook idempotent + verifikasi signature + outbox | Undangan staf via email |
| Portal mitra: daftar penyelenggara, buat/terbitkan event, poster & logo, tim | |
| Tiket QR bertanda tangan Ed25519 + BIB otomatis | Mode offline scanner, refund |
| Scanner race pack (kamera, manual, anti dobel scan) | Row-Level Security Postgres |
| Ledger double-entry + email tiket | Observabilitas (Grafana) |

Dokumen PRD & arsitektur lengkap: [docs/](docs/README.md).
