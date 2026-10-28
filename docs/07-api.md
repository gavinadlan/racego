# 07 · Referensi API

Base URL development: `http://localhost:8080`. Semua respons JSON. Setiap event membawa `status` yang dihitung dari tanggal: `open` (pendaftaran dibuka), `upcoming` (segera dibuka), `closed` (ditutup), `finished` (race sudah lewat).

Error berbentuk:

```json
{ "error": "sold_out", "message": "Maaf, kuota kategori ini sudah habis" }
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
| GET | `/v1/org/{org}/events` | semua anggota | event mitra + terjual, diambil, penjualan |
| POST | `/v1/org/{org}/events/{eventID}/scan` | owner, admin, rpc_staff | `{"token": "v1…"}` atau `{"ticket_id": "…"}`, opsional `picked_up_by` |
| GET | `/v1/org/{org}/events/{eventID}/tickets?q=` | owner, admin, rpc_staff | statistik + pencarian nama / BIB / 4 digit akhir NIK |

Bukan anggota → 404 `org_not_found`. Peran tidak cukup → 403 `forbidden`.

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
