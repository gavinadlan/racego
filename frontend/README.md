# RaceGo — Frontend (Next.js)

Next.js 16 (App Router) + TypeScript + Tailwind CSS v4. Semua halaman memanggil API Go di `NEXT_PUBLIC_API_URL`.

```bash
npm install
npm run dev      # http://localhost:3000  (backend harus jalan di :8080)
npm run lint
npm run build
```

## Halaman

| Route | Isi |
| --- | --- |
| `/` | Beranda: pencarian (kota, bulan, jarak), flag-off terdekat dengan hitung mundur, pendaftaran dibuka, segera dibuka, kalender, cara daftar, ajakan untuk penyelenggara, arsip, daftar penyelenggara |
| `/kalender` | Kalender bulanan atau daftar per bulan, filter kota & jarak, arsip (`?view=daftar&arsip=1`) |
| `/penyelenggara` | Direktori penyelenggara |
| `/penyelenggara/[slug]` | Profil penyelenggara: deskripsi, statistik, race mendatang, arsip |
| `/untuk-penyelenggara` | Halaman penawaran untuk calon mitra |
| `/events/[eventId]` | Detail event, hitung mundur, pilih kategori → reservasi slot (timer) → isi data peserta → bayar |
| `/orders/[orderId]` | Status order (polling); di mode mock ada tombol simulasi bayar |
| `/tickets` | Tiket saya: tiket berbentuk BIB dengan QR |
| `/org/[org]` | Dashboard mitra: penjualan & progres race pack per event |
| `/org/[org]/events/[eventId]/scan` | Scanner race pack (kamera via `@zxing/browser`, input manual, pencarian nama/BIB) |

## Bahasa visual

Diambil dari race day, bukan template:

- **Motif BIB**: kartu event dan tiket berbentuk nomor dada — pita warna penyelenggara, tanggal/nomor besar, garis sobek, lubang peniti di empat sudut (`components/bib.tsx`).
- **Warna** (`app/globals.css`): putih kertas BIB `#F7F8F5`, aspal `#23272E`, oranye rompi marshal `#FF5B23` (aksen & tombol utama), hijau `#1F8A5B` untuk status dibuka. Setiap penyelenggara punya `theme_color`; warna teks di atasnya dipilih otomatis (`textOn`).
- **Huruf**: Big Shoulders Display (angka & judul, condensed seperti nomor dada) + Instrument Sans (teks), dipasang dari npm (`@fontsource-variable/*`) sehingga tidak bergantung pada Google Fonts saat build.
- Satu momen gerak saja: hitung mundur flag-off; tidak ada animasi masuk per section.

## Struktur

```
src/
  app/          route (page.tsx tipis, membungkus komponen klien dengan <Suspense>)
  components/   UI & logika halaman (client components)
  lib/          api.ts (klien API + tipe), use-api.ts, snap.ts (Midtrans), format.ts
```

## Catatan

- **Login masih versi dev**: pilih user contoh di menu DEV (kanan atas). Identitas dikirim lewat header `X-User-ID` dan hanya diterima backend saat `APP_ENV=development`.
- **Kamera scanner** butuh HTTPS atau `localhost`. Untuk mencoba dari HP di jaringan yang sama, pakai tunnel HTTPS (mis. `cloudflared tunnel --url http://localhost:3000`).
- `cacheComponents` aktif (bawaan Next 16): komponen yang membaca `useParams`/`useSearchParams` dibungkus `<Suspense>` di `page.tsx`.
- Baca `AGENTS.md` sebelum memakai AI coding agent — API Next 16 berbeda dari versi lama.
