# 06 · Infra gratis (Rp0)

Arsitektur dan kode sama dengan versi produksi; hanya tempat menjalankannya yang gratis. Komponen yang menanggung beban war tiket di-self-host di VM gratis (tanpa kuota per request); layanan hosted gratis dipakai untuk hal ringan saja.

```mermaid
flowchart TB
  U[Pengguna] --> CF[Cloudflare Free<br/>DNS · CDN · SSL · Turnstile]
  CF -- Cloudflare Tunnel --> VM1
  subgraph OCI[Oracle Cloud Always Free · total 2 OCPU, 12 GB]
    VM1[VM 1 · aplikasi<br/>Caddy · Next.js · api · worker]
    VM2[VM 2 · data<br/>PostgreSQL · Redis · Grafana]
    VM1 --> VM2
  end
  VM1 <--> MT[Midtrans sandbox]
  VM1 --> EM[Email: Resend Free]
```

| Komponen | Pilihan gratis | Batas yang perlu diingat |
| --- | --- | --- |
| Compute | Oracle Cloud Always Free, Ampere A1 dibagi 2 VM | Total 2 OCPU & 12 GB sejak 2026; daftar butuh kartu untuk verifikasi; jangan upgrade ke Pay As You Go tanpa sengaja |
| Edge | Cloudflare Free + Tunnel | Aset statis gratis tanpa batas; Workers Free hanya 100.000 request/hari (jangan taruh waiting room di Workers) |
| Database / Redis | Self-host (Docker) di VM 2 | Neon/Upstash free tier punya kuota bulanan yang bisa habis dalam satu war tiket |
| Email | Mailpit (lokal), Resend Free (online) | Resend Free 100 email/hari |
| Pembayaran | Midtrans sandbox | Tanpa uang sungguhan |
| CI | GitHub Actions | Gratis untuk repo publik |

Batas free tier sering berubah — cek ulang sebelum deploy.

## Langkah deploy (garis besar)

1. Buat 2 VM Ampere A1 (1 OCPU, 6 GB masing-masing, Ubuntu 24.04) di satu VCN; buka port 5432/6379 hanya untuk subnet privat.
2. VM 2: pasang Docker, jalankan Postgres + Redis (bisa memakai `docker-compose.yml` repo ini tanpa Mailpit), set password.
3. VM 1: build image `backend/Dockerfile` dan frontend (`npm run build && npm start`), atau jalankan binary langsung via systemd.
4. Isi `.env` produksi: `APP_ENV=production`, `QR_PRIVATE_KEY` dari `make keygen`, `DATABASE_URL`/`REDIS_ADDR` ke IP privat VM 2, SMTP Resend.
5. `cloudflared tunnel` dari VM 1 ke domain kamu (`racego.example` → web :3000, `api.racego.example` → :8080). Tidak perlu membuka port publik.
6. Arahkan Notification URL Midtrans sandbox ke `https://api.racego.example/v1/webhooks/midtrans`.

> Penting: di `APP_ENV` selain `development`, header `X-User-ID` diabaikan — artinya login sungguhan (lihat roadmap) harus dibuat dulu sebelum demo publik.

## Uji beban di infra gratis

Jalankan k6 langsung ke VM/laptop, **bukan** lewat Cloudflare, agar tidak terkena rate limit atau melanggar ketentuan layanan. Gunakan mode mock Midtrans saat uji beban. Naikkan bertahap (5 ribu → 10 ribu → 50 ribu VU) dan catat titik jenuhnya; yang boleh menurun adalah latensi, bukan kebenaran data.
