# 07 · Referensi API

Base URL development: `http://localhost:8080`. Semua respons JSON. Setiap event membawa `status` yang dihitung dari tanggal: `open` (pendaftaran dibuka), `upcoming` (segera dibuka), `closed` (ditutup), `finished` (race sudah lewat).

Error berbentuk:

```json
{ "error": "sold_out", "message": "Maaf, kuota kategori ini sudah habis" }
```

Autentikasi sementara (development saja): header `X-User-ID: <uuid user>`.

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
| GET | `/v1/me` | | profil + daftar mitra & peran |
| POST | `/v1/categories/{categoryID}/reservations` | `{"qty": 2}` | 201 `reservation_id`, `expires_at`, `remaining` · 409 `sold_out` / `limit_exceeded` / `sale_not_open` |
| POST | `/v1/orders` | header `Idempotency-Key`; `{"category_id","reservation_id","participants":[…]}` | 201 `order_id`, `snap_token`, `redirect_url`, `mock` · 410 `reservation_expired` · 409 `reservation_used` · 422 validasi peserta |
| GET | `/v1/orders/{orderID}` | | status order (untuk polling) |
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
U=11111111-1111-1111-1111-111111111111   # Budi
C=cccccccc-0000-0000-0000-000000000021   # 21K Jakarta

curl -s -XPOST localhost:8080/v1/categories/$C/reservations \
  -H "X-User-ID: $U" -H 'Content-Type: application/json' -d '{"qty":1}'
```
