# 07 · Referensi API

Base URL development: `http://localhost:8080`. Semua respons JSON. Setiap event membawa `status` yang dihitung dari tanggal: `open` (pendaftaran dibuka), `upcoming` (segera dibuka), `closed` (ditutup), `finished` (race sudah lewat).

Error berbentuk:

```json
{ "error": "sold_out", "message": "Maaf, kuota kategori ini sudah habis" }
```

Error validasi form (422) membawa pesan per kolom; kategori dalam satu kiriman memakai kunci `categories.<indeks>.<kolom>`:

```json
{ "error": "validation", "message": "Periksa kembali isian yang ditandai.",
  "fields": { "sale_close_at": "Penjualan harus ditutup setelah dibuka", "categories.1.bib_prefix": "Prefix BIB sama dengan kategori ke-1" } }
```

Autentikasi: **cookie sesi** `racego_session` (HttpOnly, `SameSite=Lax`, `Path=/`, host-only, 30 hari, `Secure` di luar development) yang di-set oleh `POST /v1/auth/otp/verify`. Frontend memanggil API dengan `fetch(..., { credentials: "include" })`; CORS hanya mengizinkan origin `FRONTEND_URL` dengan `Access-Control-Allow-Credentials: true`.

- **CSRF**: request `POST/PUT/PATCH/DELETE` yang membawa header `Origin` selain `FRONTEND_URL` ditolak 403 `bad_origin` (webhook server-ke-server tidak mengirim `Origin`).
- **Development saja**: request tanpa cookie sesi valid boleh membawa header `X-User-ID: <uuid user>` (akun contoh). Di luar `APP_ENV=development` header ini diabaikan total.

## Login (email + kode OTP)

Tanpa password; akun dibuat otomatis saat verifikasi pertama (daftar = masuk).

| Method | Path | Body | Respons |
| --- | --- | --- | --- |
| POST | `/v1/auth/otp/request` | `{"email"}` | 200 `{"ok":true,"resend_after":60}` (juga untuk email yang belum terdaftar) · 422 `invalid_email` · 429 `too_many_requests` + `retry_after` (detik) · 502 `mail_failed` |
| POST | `/v1/auth/otp/verify` | `{"email","code"}` | 200 + `Set-Cookie`; body = `GET /v1/me` + `"is_new": bool` · 400 `otp_invalid` + `remaining` · 410 `otp_expired` (kedaluwarsa / sudah dipakai) · 429 `too_many_attempts` (5× salah, kode hangus) |
| POST | `/v1/auth/logout` | – | 204, sesi di DB & cookie dihapus (tetap 204 walau belum login) |
| GET | `/v1/auth/session` | – | 200 `{"me": <sama dengan GET /v1/me>}`, atau `{"me": null}` bila belum masuk. Dipakai frontend di setiap halaman, jadi pengunjung anonim tidak memicu 401 di konsol browser |

Aturan kode: 6 digit, berlaku 10 menit, sekali pakai, maks 5 percobaan salah. Kirim ulang ke email yang sama paling cepat 60 detik sekali (jeda ini hilang begitu kode berhasil dipakai) dan maks 5 kali/jam; per IP maks 20 permintaan/jam. Di development kode juga dicatat di log API (`kode OTP (dev)`).

## Publik

| Method | Path | Keterangan |
| --- | --- | --- |
| GET | `/healthz` | Cek DB & Redis; `midtrans_mock` menunjukkan mode pembayaran |
| GET | `/v1/events` | Daftar event. Query opsional: `scope` (`upcoming` default, `past`, `all`), `city`, `distance` (mis. `21K`), `month` (`YYYY-MM`), `from`/`to` (`YYYY-MM-DD`), `org` (slug), `q`, `limit` |
| GET | `/v1/events/filters` | Pilihan kota & jarak untuk kolom pencarian |
| GET | `/v1/events/{eventID}` | Detail + kategori + sisa stok (`remaining: null` = stok belum dimuat) + profil singkat penyelenggara |
| GET | `/v1/organizers` | Direktori penyelenggara terverifikasi + jumlah race |
| GET | `/v1/organizers/{slug}` | Profil penyelenggara + race mendatang + arsip |
| GET | `/v1/qr/public-key` | Kunci publik Ed25519 untuk verifikasi QR offline |
| GET | `/uploads/{name}` | Gambar unggahan (`<sha256>.jpg|png`); `Cache-Control: public, max-age=31536000, immutable`, `X-Content-Type-Options: nosniff`; nama lain → 404 |
| POST | `/v1/webhooks/midtrans` | HTTP notification Midtrans |

## Pelari (wajib login)

| Method | Path | Body / header | Respons penting |
| --- | --- | --- | --- |
| GET | `/v1/me` | | `id`, `name`, `email`, `phone`, `orgs` (mitra & peran) |
| PATCH | `/v1/me` | `{"name","phone"}` | 200 profil terbaru · 422 `invalid_name` (2–100 karakter) / `invalid_phone` (format Indonesia `08…`/`628…`/`+628…`, disimpan sebagai `08…`; kosong = dihapus) |
| POST | `/v1/categories/{categoryID}/reservations` | `{"qty": 2}` | 201 `reservation_id`, `expires_at`, `remaining` · 409 `sold_out` / `limit_exceeded` / `sale_not_open` |
| POST | `/v1/orders` | header `Idempotency-Key`; `{"category_id","reservation_id","participants":[…]}` | 201 `order_id`, `snap_token`, `redirect_url`, `mock` · 410 `reservation_expired` · 409 `reservation_used` · 422 validasi peserta |
| GET | `/v1/orders/{orderID}` | | status order (untuk polling); bila belum lunas & bukan mock, sekalian cek status ke Midtrans |
| GET | `/v1/me/tickets` | | tiket + `qr_token` + BIB |
| POST | `/v1/orgs` | `{"name","slug"?,"city","description","instagram"?,"website"?}` | 201 `slug`, `name`, `kyc_status` (pembuat jadi owner) · 422 `validation` (nama 3–80, slug 3–40 `a-z0-9-` & bukan nama route frontend, instagram tanpa `@`, website `https://`) · 429 `too_many_orgs` (maks 3 per akun) |
| GET | `/v1/orgs/slug-check?slug=` | | `{"available": bool, "message"?}` |

Objek peserta:

```json
{
  "full_name": "Budi Pelari", "email": "budi@racego.local", "id_number": "3171000000000001",
  "birth_date": "1995-06-15", "gender": "M", "jersey_size": "L",
  "emergency_name": "Ibu Budi", "emergency_phone": "081299999999"
}
```

## Mitra (`/v1/org/{org}/…`, terfilter ke satu mitra)

| Method | Path | Peran | Keterangan |
| --- | --- | --- | --- |
| GET | `/v1/org/{org}/events` | semua anggota | event mitra (termasuk draft) + status, terjual, diambil, penjualan |
| GET | `/v1/org/{org}/profile` | semua anggota | profil org + `kyc_status` + `role` user |
| PATCH | `/v1/org/{org}/profile` | owner, admin | `{"name","city","description","instagram","website","theme_color","logo_url"}`; slug tidak bisa diubah |
| POST | `/v1/org/{org}/uploads?kind=cover\|logo` | owner, admin | multipart field `file` → 201 `{"url":"/uploads/<hash>.jpg"}` · 413 `file_too_large` · 422 `invalid_image` / `image_too_big` |
| POST | `/v1/org/{org}/events` | owner, admin | event + `categories` (opsional) → 201 draft lengkap · 422 `validation` / `too_many_drafts` |
| GET | `/v1/org/{org}/events/{eventID}` | semua anggota | detail untuk form edit: kategori + `sold`, `pending`, `locked`, `has_orders`, `remaining` (Redis) |
| PATCH | `/v1/org/{org}/events/{eventID}` | owner, admin | semua kolom form event (lihat di bawah) |
| POST | `/v1/org/{org}/events/{eventID}/publish` | owner, admin | 200 · 409 `org_not_verified` / `invalid_status` · 422 `validation` (termasuk `categories` bila kosong) |
| POST | `/v1/org/{org}/events/{eventID}/unpublish` | owner, admin | kembali ke draft; 409 `has_orders` bila sudah ada order |
| POST | `/v1/org/{org}/events/{eventID}/close` | owner, admin | tutup penjualan permanen |
| DELETE | `/v1/org/{org}/events/{eventID}` | owner | 204; hanya draft tanpa order |
| POST | `/v1/org/{org}/events/{eventID}/categories` | owner, admin | tambah kategori (draft, atau terbit sebelum penjualan dibuka) |
| PATCH / DELETE | `/v1/org/{org}/events/{eventID}/categories/{categoryID}` | owner, admin | ubah (kode/harga/jarak/prefix terkunci setelah ada penjualan; kuota tidak di bawah terpakai) / hapus (tanpa order) |
| GET | `/v1/org/{org}/members` | owner | anggota: `user_id`, `name`, `email`, `role` |
| POST | `/v1/org/{org}/members` | owner | `{"email","role"}` (`admin`/`finance`/`rpc_staff`) · 404 `user_not_found` bila email belum pernah login · 409 `already_member` |
| PATCH / DELETE | `/v1/org/{org}/members/{userID}` | owner | ubah peran (termasuk `owner`) / hapus; 409 `self_change` untuk diri sendiri, `last_owner` bila owner terakhir |
| POST | `/v1/org/{org}/events/{eventID}/scan` | owner, admin, rpc_staff | `{"token": "v1…"}` atau `{"ticket_id": "…"}`, opsional `picked_up_by` |
| GET | `/v1/org/{org}/events/{eventID}/tickets?q=` | owner, admin, rpc_staff | statistik + pencarian nama / BIB / 4 digit akhir NIK |

Bukan anggota → 404 `org_not_found`. Peran tidak cukup → 403 `forbidden`. Event/kategori milik org lain → 404.

Form event (`POST`/`PATCH`):

```json
{ "name": "Bogor Botanic Run", "tagline": "…", "description": "…", "venue": "Kebun Raya", "city": "Bogor",
  "race_date": "2027-01-10", "start_time": "05:30",
  "sale_open_at": "2026-11-01T10:00:00+07:00", "sale_close_at": "2027-01-07T23:59:00+07:00",
  "racepack_info": "…", "theme_color": "#1F8A5B", "cover_url": "/uploads/<hash>.jpg",
  "categories": [{ "code": "10K", "name": "10K", "distance_km": 10, "price": 200000, "quota": 500, "max_per_user": 2, "bib_prefix": "10" }] }
```

Aturan: nama 3–100; `race_date` setelah hari ini (WIB) saat dibuat/diubah/terbit; `sale_open_at < sale_close_at ≤` akhir hari race WIB; `start_time` `HH:MM`; tagline ≤ 120, deskripsi ≤ 5.000, info race pack ≤ 2.000; kategori: kode 2–8 `A-Z0-9` unik, jarak 0–250 km, harga 0–50 jt, kuota 1–100.000, maks/akun 1–10, prefix BIB 1–3 digit unik per event.

## Development saja

| Method | Path | Keterangan |
| --- | --- | --- |
| POST | `/v1/dev/orders/{orderID}/simulate` | `{"status": "settlement" \| "expire" \| "cancel"}` — membuat notifikasi bertanda tangan valid lalu memprosesnya seperti webhook asli |

## Contoh dengan curl

```bash
# Login sungguhan: minta kode, lihat kodenya di log API (development) atau Mailpit, simpan cookie
curl -s -XPOST localhost:8080/v1/auth/otp/request -d '{"email":"kamu@example.com"}'
curl -s -c jar -XPOST localhost:8080/v1/auth/otp/verify -d '{"email":"kamu@example.com","code":"123456"}'
curl -s -b jar localhost:8080/v1/me

# Akun contoh (development saja)
U=11111111-1111-1111-1111-111111111111   # Budi
C=cccccccc-0000-0000-0000-000000000021   # 21K Jakarta

curl -s -XPOST localhost:8080/v1/categories/$C/reservations \
  -H "X-User-ID: $U" -H 'Content-Type: application/json' -d '{"qty":1}'
```
