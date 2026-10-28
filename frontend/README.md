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
| `/masuk` | Masuk/daftar dengan email + kode 6 digit; `?next=/path` (hanya path relatif) untuk kembali setelah login |
| `/akun` | Profil (nama, no. HP), peran mitra, keluar; `?baru=1&next=…` = "Lengkapi profil" setelah daftar |
| `/tickets` | Tiket saya: tiket berbentuk BIB dengan QR |
| `/daftar-mitra` | Daftarkan penyelenggara (login wajib): nama, alamat/slug dengan cek ketersediaan, kota, profil |
| `/org/[org]` | Dashboard mitra, tab **Event**: status (Draft/Terbit/Ditutup), penjualan, progres race pack, Buat/Edit (owner/admin), scanner. `?baru=1` = banner setelah mendaftar |
| `/org/[org]/tim` | Tab **Tim** (owner): tambah anggota by email, ubah peran, hapus |
| `/org/[org]/profil` | Tab **Profil**: deskripsi, kota, instagram, situs, warna, logo (slug tidak bisa diubah) |
| `/org/[org]/events/new`, `/org/[org]/events/[eventId]/edit` | Form event (`components/event-form.tsx`): informasi, poster, penjualan (WIB), tabel kategori (jadi kartu di HP), pratinjau kartu BIB, Simpan draft / Terbitkan / Tutup penjualan / Hapus draft dengan dialog konfirmasi |
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

- **Login**: email + kode OTP; sesi berupa cookie HttpOnly dari backend, jadi `apiFetch` selalu memakai `credentials: "include"`. `components/session.tsx` (`useSession()`: `ready`, `userId`, `me`, `refresh`, `logout`) membaca `GET /v1/auth/session` (selalu 200, `me: null` bila belum masuk).
- **Akun contoh (dev)**: pemilih di header hanya muncul bila `NEXT_PUBLIC_DEV_LOGIN=true`; identitas dikirim lewat header `X-User-ID` yang hanya diterima backend saat `APP_ENV=development`. Set `false`/hapus saat deploy.
- **Kamera scanner** butuh HTTPS atau `localhost`. Untuk mencoba dari HP di jaringan yang sama, pakai tunnel HTTPS (mis. `cloudflared tunnel --url http://localhost:3000`).
- `cacheComponents` aktif (bawaan Next 16): komponen yang membaca `useParams`/`useSearchParams` dibungkus `<Suspense>` di `page.tsx`.
- Baca `AGENTS.md` sebelum memakai AI coding agent — API Next 16 berbeda dari versi lama.
- **Gambar unggahan** (poster, logo) dirender lewat `UploadedImage` (`components/ui-client.tsx`) dengan `API_URL + url`. Ukuran kotak ditentukan pemanggil, jadi gambar yang gagal dimuat tidak menggeser tata letak (fallback inisial/warna tema).
- **Peran mitra** dibaca dari `me.orgs` (`roleIn`, `canEdit` di `lib/api.ts`); tombol yang butuh peran owner/admin disembunyikan untuk staf, server tetap memeriksa ulang.
- **Uji browser**: Next 16 menyimpan halaman sebelumnya tersembunyi di DOM (`<Activity>`), jadi locator Playwright perlu `.filter({ visible: true })` setelah navigasi di sisi klien.
