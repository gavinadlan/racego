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
| GET | `/healthz` | Cek DB & Redis; `env` (`development`/`production`) dan `midtrans_mock` (mode pembayaran) |
| GET | `/v1/events` | Daftar event. Query opsional: `scope` (`upcoming` default, `past`, `all`), `city`, `distance` (mis. `21K`), `month` (`YYYY-MM`), `from`/`to` (`YYYY-MM-DD`), `org` (slug), `q`, `limit` |
| GET | `/v1/events/filters` | Pilihan kota & jarak untuk kolom pencarian |
| GET | `/v1/events/{eventID}` | Detail + kategori + sisa stok (`remaining: null` = stok belum dimuat) + profil singkat penyelenggara + `waiting_room` (antrian virtual) dan, bila aktif, `lobby_opens_at` |
| GET | `/v1/organizers` | Direktori penyelenggara terverifikasi + jumlah race |
| GET | `/v1/organizers/{slug}` | Profil penyelenggara + race mendatang + arsip |
| GET | `/v1/qr/public-key` | Kunci publik Ed25519 untuk verifikasi QR offline |
| GET | `/uploads/{name}` | Gambar unggahan (`<sha256>.jpg|png`); `Cache-Control: public, max-age=31536000, immutable`, `X-Content-Type-Options: nosniff`; nama lain → 404 |
| POST | `/v1/webhooks/midtrans` | HTTP notification Midtrans |

## Pelari (wajib login)

| Method | Path | Body / header | Respons penting |
| --- | --- | --- | --- |
| GET | `/v1/me` | | `id`, `name`, `email`, `phone`, `orgs` (mitra & peran), `is_super_admin` |
| PATCH | `/v1/me` | `{"name","phone"}` | 200 profil terbaru · 422 `invalid_name` (2–100 karakter) / `invalid_phone` (format Indonesia `08…`/`628…`/`+628…`, disimpan sebagai `08…`; kosong = dihapus) |
| POST | `/v1/categories/{categoryID}/reservations` | `{"qty": 2}`; event ber-antrian: header `X-Queue-Pass` | 201 `reservation_id`, `expires_at`, `remaining` (pass antrian dilepas) · 409 `sold_out` / `limit_exceeded` / `sale_not_open` · 403 `queue_required` (tanpa pass, pass tidak sah/milik orang lain/sudah dipakai/dicabut) / `queue_pass_expired` |
| POST | `/v1/orders` | header `Idempotency-Key`; `{"category_id","reservation_id","participants":[…]}` | 201 `order_id`, `snap_token`, `redirect_url`, `mock` · 410 `reservation_expired` · 409 `reservation_used` · 422 validasi peserta |
| GET | `/v1/orders/{orderID}` | | status order (untuk polling) + `qty` + `tickets_issued` (tiket yang sudah terbit; worker menerbitkannya beberapa detik setelah lunas); bila belum lunas & bukan mock, sekalian cek status ke Midtrans |
| GET | `/v1/me/tickets` | | tiket + `qr_token` + BIB + `order_id` |
| POST | `/v1/events/{eventID}/queue` | | Masuk antrian virtual. 201 baru, 200 sudah di antrian (posisi tidak berubah); body = status antrian · 409 `queue_not_open` (sebelum lobi / penjualan sudah ditutup) · 404 `event_not_found` (tidak ada, belum terbit, atau tanpa antrian) · 429 `too_many_requests` (20/menit per akun, 300/menit per IP) |
| GET | `/v1/events/{eventID}/queue/me` | | Status antrian + tanda hidup (panggil sesuai `poll_after_ms`). 429 `too_many_requests` + `retry_after` bila < 1 detik sejak panggilan sebelumnya |
| DELETE | `/v1/events/{eventID}/queue/me` | | 204; keluar antrian dan pass dicabut |
| POST | `/v1/orgs` | `{"name","slug"?,"city","description","instagram"?,"website"?}` | 201 `slug`, `name`, `kyc_status` (pembuat jadi owner) · 422 `validation` (nama 3–80, slug 3–40 `a-z0-9-` & bukan nama route frontend, instagram tanpa `@`, website `https://`) · 429 `too_many_orgs` (maks 3 per akun) |
| GET | `/v1/orgs/slug-check?slug=` | | `{"available": bool, "message"?}` |

Status antrian:

```json
{ "state": "waiting", "position": 128, "ahead": 127, "queue_size": 5400, "eta_seconds": 7,
  "poll_after_ms": 3612, "server_time": "2026-11-01T03:00:12.120Z",
  "lobby_opens_at": "2026-11-01T02:45:00Z", "sale_open_at": "2026-11-01T03:00:00Z" }
```

`state`: `lobby` (sebelum jam buka; `position` null karena urutan baru diundi saat buka), `waiting`, `admitted` (+ `pass`, `pass_expires_at`), `sold_out`, `closed`, `not_in_queue`. `eta_seconds` null bila laju panggil belum terukur. `poll_after_ms` dihitung server dan sudah berjitter; 0 = tidak perlu polling lagi. Kirim `pass` sebagai header `X-Queue-Pass` ke endpoint reservasi.

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
| GET | `/v1/org/{org}/events` | semua anggota | event mitra (termasuk draft) + status, terjual, diambil, penjualan, `waiting_room` |
| GET | `/v1/org/{org}/profile` | semua anggota | profil org + `kyc_status` + `role` user + `verification_status` (pengajuan terbaru, `null` bila belum) + `reject_reason` |
| PATCH | `/v1/org/{org}/profile` | owner, admin | `{"name","city","description","instagram","website","theme_color","logo_url"}`; slug tidak bisa diubah |
| POST | `/v1/org/{org}/uploads?kind=cover\|logo` | owner, admin | multipart field `file` → 201 `{"url":"/uploads/<hash>.jpg"}` · 413 `file_too_large` · 422 `invalid_image` / `image_too_big` |
| POST | `/v1/org/{org}/events` | owner, admin | event + `categories` (opsional) → 201 draft lengkap · 422 `validation` / `too_many_drafts` |
| GET | `/v1/org/{org}/events/{eventID}` | semua anggota | detail untuk form edit: `waiting_room` + kategori + `sold`, `pending`, `locked`, `has_orders`, `remaining` (Redis) |
| GET | `/v1/org/{org}/events/{eventID}/queue` | owner, admin, finance | ringkasan antrian langsung: `phase` (`before_lobby`/`lobby`/`selling`/`closed`), `queue_size`, `active` (pass aktif), `admitted_total`, `final`, `max_active`, `admit_per_sec` |
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

### Keuangan mitra (owner, finance)

`admin` dan `rpc_staff` → 403 `forbidden`; bukan anggota → 404. Uang selalu bilangan bulat rupiah. Lihat [04](04-pembayaran-dan-ledger.md).

| Method | Path | Keterangan |
| --- | --- | --- |
| GET | `/v1/org/{org}/finance/summary` | `balances` (`held`, `available`, `in_process`, `paid_out`; `available` bisa negatif), `account` tersamar (`bank_name`, `account_holder`, `last4`) atau `null`, `rules` (`payout_min`, `hold_days`), `can_payout`, `blocker` (`code`, `message`) bila tidak bisa, `mock` |
| GET | `/v1/org/{org}/finance/ledger?page=` | mutasi saldo: `items[]` (`at`, `kind`, `description`, `account` tertahan/tersedia/dalam_proses, `amount` ±, `ref`), `total`, 20 per halaman |
| GET | `/v1/org/{org}/payouts?page=` | riwayat pencairan (`status`, `account` snapshot, `reject_reason`, `fail_reason`, `reference_no`, waktu) |
| POST | `/v1/org/{org}/payouts` | `{"amount": 500000}` + header **`Idempotency-Key`** (wajib; kunci sama → payout yang sama). 201 · 400 `idempotency_key_required` · 409 `org_not_verified` / `no_payout_account` / `payout_in_progress` · 422 `below_minimum` / `insufficient_balance` / `validation` (bukan bilangan bulat) |
| GET | `/v1/org/{org}/orders?event=&q=&status=&page=` | order lunas/di-refund (`q` = email pembeli atau awalan ID order); tanpa data peserta; `refund_status`, `collected_tickets` |
| POST | `/v1/org/{org}/orders/{id}/refund` | `{"reason"}` 10–500 karakter → 201 refund `requested` · 404 order org lain · 409 `already_refunded` / `order_not_refundable` |
| GET | `/v1/org/{org}/refunds?page=` | status refund org |
| GET | `/v1/org/{org}/reports/sales` | ringkasan per event (`orders_paid`, `orders_refunded`, `gross`, `platform_fee`, `pg_fee`, `refunds`, `net`) |
| GET | `/v1/org/{org}/reports/sales.csv`, `/reports/ledger.csv` | CSV UTF-8 BOM, pemisah `;`, aman injeksi rumus; tercatat `report.download` |

### Verifikasi mitra (owner saja)

Semua respons sukses (kecuali unduhan dokumen) = keadaan lengkap: `kyc_status`, `auto_verify`, `verification` (pengajuan terbaru atau `null`; NIK/NPWP/rekening **tersamar** `••••1234`, `documents[]`), `history[]` (semua pengajuan: `attempt`, `status`, `submitted_at`, `reviewed_at`, `reject_reason`), `requirements` (matriks jenis & dokumen, daftar bank, teks + versi persetujuan, `max_bytes`). Status pengajuan: `draft` → `submitted` → `approved` | `rejected`.

| Method | Path | Keterangan |
| --- | --- | --- |
| GET | `/v1/org/{org}/verification` | keadaan di atas |
| PUT | `/v1/org/{org}/verification` | simpan draft (dibuat otomatis bila belum ada): `{"org_type","legal_name","address","pic_name","pic_phone","pic_id"?,"npwp"?,"bank_code","bank_name","account_number"?,"account_holder"}`. `pic_id`/`npwp`/`account_number`: tidak dikirim = tidak diubah, `""` = dihapus. Format: NIK 16 digit, NPWP 15/16 digit, rekening 6–20 digit, HP Indonesia, `bank_code` dari daftar (`lainnya` + `bank_name` bebas). Mengganti `org_type` membuang dokumen yang tidak dipakai jenis baru. 422 `validation` |
| POST | `/v1/org/{org}/verification/documents` | multipart `kind` + `file` (JPG/PNG/PDF ≤ 5 MB, tipe dari isi). Mengganti dokumen jenis yang sama. 413 `file_too_large` · 422 `invalid_document` / `pdf_encrypted` / `pdf_active_content` / `image_too_big` / `validation` (`kind` tidak dipakai jenis ini, jenis belum dipilih) · 429 `too_many_requests` (30 unggahan/jam/akun, `retry_after`) |
| DELETE | `/v1/org/{org}/verification/documents/{kind}` | hapus dokumen draft · 404 `not_found` |
| GET | `/v1/org/{org}/verification/documents/{kind}` | berkas sendiri dari pengajuan terbaru, didekripsi; tercatat `kyc.document.view` (`by: owner`). Header: `Content-Type` hasil validasi, `X-Content-Type-Options: nosniff`, `Cache-Control: private, no-store`, `Content-Security-Policy: sandbox; default-src 'none'`, `Content-Disposition: inline; filename*=…` |
| POST | `/v1/org/{org}/verification/submit` | `{"consent": true}` → `submitted`, email ke owner. 422 `validation` per kolom, mis. `documents.ktp_pic`, `account_number`, `consent` |
| POST | `/v1/org/{org}/verification/reopen` | hanya dari `rejected`: pengajuan baru (`attempt+1`, `draft`) berisi salinan isian & dokumen · 409 `invalid_status` |

Ubah/unggah/hapus saat pengajuan `submitted` → 409 `not_editable`; setelah `rejected` tanpa ajukan ulang → 409 `reopen_required`; org sudah `verified` (termasuk `AUTO_VERIFY_ORGS=true`) → 409 `already_verified`. Peran selain owner → 403 `forbidden`; status cukup dibaca dari `/profile`.

Matriks dokumen (`requirements.types`):

| `org_type` | Wajib | Opsional |
| --- | --- | --- |
| `perorangan` | `ktp_pic` | `npwp` |
| `komunitas` | `ktp_pic`, `surat_keterangan` | `npwp` |
| `badan_usaha` | `ktp_pic`, `npwp`, `akta_pendirian`, `sk_kemenkumham` (+ nomor NPWP) | `nib`, `surat_kuasa` |

## Super admin (`/v1/admin/…`)

Anonim → 401 `unauthenticated`. Login tapi bukan super admin → **404** `not_found` (keberadaan rute tidak bocor). Super admin hanya diberikan lewat `make superadmin EMAIL=…`; tidak ada endpoint untuk itu.

| Method | Path | Keterangan |
| --- | --- | --- |
| GET | `/v1/admin/verifications?status=&q=&page=` | `status` `submitted` (default, terlama dikirim dulu) / `approved` / `rejected` (terbaru ditinjau dulu); `q` = nama org/nama sesuai dokumen; 20 per halaman. → `items[]` (`id`, `org_name`, `org_slug`, `org_type`, `legal_name`, `attempt`, `status`, `submitted_at`, `reviewed_at`), `counts` per status, `total`, `page`, `page_size` |
| GET | `/v1/admin/verifications/{id}` | `verification` (tersamar), `documents[]` + `duplicate_orgs` (org lain yang mengunggah berkas identik, dari SHA-256), `org`, `members`, `history`, `name_match` (`ok`, `holder`, `expected[]`, `rule`; petunjuk, bukan penentu), `reviewer`, `conflict_of_interest`, `requirements`. Draft → 404 |
| POST | `/v1/admin/verifications/{id}/reveal` | `{"field":"pic_id"\|"account"\|"npwp"}` → `{"field","value"}` (nilai lengkap); tercatat `kyc.field.reveal` |
| GET | `/v1/admin/verifications/{id}/documents/{kind}` | dokumen terdekripsi (header sama seperti di atas); tercatat `kyc.document.view` (`by: super_admin`) |
| POST | `/v1/admin/verifications/{id}/approve` | → org `verified` + email; respons = detail |
| POST | `/v1/admin/verifications/{id}/reject` | `{"reason"}` 10–1.000 karakter (422 `fields.reason`) → org `rejected` + email berisi alasan |

Approve/reject: 403 `conflict_of_interest` bila super admin anggota org itu; 409 `invalid_status` bila bukan `submitted`. Kunci enkripsi salah/berkas rusak → 500 `decrypt_failed` (tidak pernah mengirim data rusak). Menyetujui verifikasi juga membuat rekening pencairan (`payout_accounts`, terenkripsi) org itu.

### Keuangan (super admin)

| Method | Path | Keterangan |
| --- | --- | --- |
| GET | `/v1/admin/payouts?status=&page=` | `status` `requested` (default, terlama dulu) / `approved` / `processing` / `completed` / `failed` / `rejected` / `all`; `items[]` + `org_available` (saldo tersedia org sekarang), `counts`, `mock` |
| GET | `/v1/admin/payouts/{id}` | `payout`, `balances` org, `history` (pencairan lain org), `org_kyc_status`, `conflict_of_interest`, `mock` |
| POST | `/v1/admin/payouts/{id}/reveal` | `{}` → `{"value"}` nomor rekening lengkap; tercatat `payout.account.reveal` |
| POST | `/v1/admin/payouts/{id}/approve` | `requested` → `approved` (worker mengeksekusi). 403 `conflict_of_interest` · 409 `invalid_state` |
| POST | `/v1/admin/payouts/{id}/reject` | `{"reason"}` 10–1.000 → `rejected`, saldo kembali. 422 `fields.reason` |
| GET | `/v1/admin/refunds?status=&page=` | `requested` (default) / `approved` / `processing` / `manual_required` / `completed` / `rejected` / `failed` / `all` |
| POST | `/v1/admin/refunds` | `{"order_id","reason"}` → refund dibuat **dan langsung disetujui** (201) |
| POST | `/v1/admin/refunds/{id}/approve` | order + tiket `refunded`, jurnal `order.refund`; worker mengirim dana |
| POST | `/v1/admin/refunds/{id}/reject` | `{"reason"}` 10–1.000 |
| POST | `/v1/admin/refunds/{id}/mark-done` | `{"reference"}` 3–100 karakter; hanya dari `manual_required`/`failed` → `completed` |
| GET | `/v1/admin/finance/overview` | total tertahan/tersedia/dalam proses seluruh mitra, `platform_revenue`, `refund_pg_cost`, `cash`, `accounts` (saldo debit−kredit per akun), hitungan status, `disburse_mode`, `mock` |
| GET | `/v1/admin/ledger/check` | `{"ok", "checks":[{"name","ok","detail"}]}` |

Semua keputusan keuangan: 403 `conflict_of_interest` bila super admin anggota org; 409 `invalid_state` bila status sudah berubah (klik ganda/paralel aman).

Form event (`POST`/`PATCH`):

```json
{ "name": "Bogor Botanic Run", "tagline": "…", "description": "…", "venue": "Kebun Raya", "city": "Bogor",
  "race_date": "2027-01-10", "start_time": "05:30",
  "sale_open_at": "2026-11-01T10:00:00+07:00", "sale_close_at": "2027-01-07T23:59:00+07:00",
  "racepack_info": "…", "theme_color": "#1F8A5B", "cover_url": "/uploads/<hash>.jpg", "waiting_room": true,
  "categories": [{ "code": "10K", "name": "10K", "distance_km": 10, "price": 200000, "quota": 500, "max_per_user": 2, "bib_prefix": "10" }] }
```

Aturan: nama 3–100; `race_date` setelah hari ini (WIB) saat dibuat/diubah/terbit; `sale_open_at < sale_close_at ≤` akhir hari race WIB; `start_time` `HH:MM`; tagline ≤ 120, deskripsi ≤ 5.000, info race pack ≤ 2.000; kategori: kode 2–8 `A-Z0-9` unik, jarak 0–250 km, harga 0–50 jt, kuota 1–100.000, maks/akun 1–10, prefix BIB 1–3 digit unik per event. `waiting_room` (bool) tidak dikirim saat `PATCH` = tidak diubah; mengubahnya setelah penjualan dibuka (event bukan draft) → 422 dengan `fields.waiting_room`.

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

# Event ber-antrian: masuk antrian, polling sampai "admitted", lalu reservasi dengan pass
E=<event id>
curl -s -XPOST localhost:8080/v1/events/$E/queue -H "X-User-ID: $U"
PASS=$(curl -s localhost:8080/v1/events/$E/queue/me -H "X-User-ID: $U" | jq -r .pass)
curl -s -XPOST localhost:8080/v1/categories/<kategori event itu>/reservations \
  -H "X-User-ID: $U" -H "X-Queue-Pass: $PASS" -H 'Content-Type: application/json' -d '{"qty":1}'
```
