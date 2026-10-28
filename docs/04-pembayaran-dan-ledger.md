# 04 · Pembayaran, ledger, dan pencairan

## Mode pembayaran

| Mode | Kapan | Cara |
| --- | --- | --- |
| **Mock** (default) | `MIDTRANS_SERVER_KEY` kosong | Token palsu; halaman order punya tombol *Simulasikan bayar berhasil / kedaluwarsa* yang memanggil `POST /v1/dev/orders/{id}/simulate`. Notifikasi dibuat dengan signature valid dan diproses lewat jalur yang **sama persis** dengan webhook asli. |
| **Sandbox** | Isi `MIDTRANS_SERVER_KEY` + `NEXT_PUBLIC_MIDTRANS_CLIENT_KEY` dari [dashboard sandbox](https://dashboard.sandbox.midtrans.com) | Popup Snap asli, bayar dengan simulator Midtrans. Tanpa URL publik pun order tetap terdeteksi lunas lewat cek status langsung (lihat bawah). |

### Cek status langsung (cadangan webhook)

Webhook tidak bisa sampai ke `localhost`, dan di produksi pun bisa terlambat. Karena itu backend juga **bertanya sendiri** ke Midtrans lewat `GET /v2/{order_id}/status` (`backend/internal/payment/status.go`):

- **Saat halaman order dibuka**: `GET /v1/orders/{id}` untuk order yang masih `pending`/`expired` mengecek ke Midtrans, paling sering sekali per 4 detik per order.
- **Worker tiap menit**: mengecek order belum lunas dalam 24 jam terakhir yang belum punya status akhir dari Midtrans.

Hasilnya diproses lewat fungsi yang sama dengan webhook (`processVerified`), jadi tetap idempotent: kalau webhook dan cek status datang bersamaan, tiket hanya terbit sekali. Di mode mock fitur ini mati.

### Menerima webhook sandbox di laptop (opsional)

Supaya status berubah seketika (tanpa menunggu cek status), Midtrans harus bisa memanggil API kamu dari internet:

```bash
cloudflared tunnel --url http://localhost:8080   # gratis, tanpa akun
```

Salin URL `https://….trycloudflare.com`, lalu di dashboard sandbox → *Settings → Payment → Notification URL* isi `https://….trycloudflare.com/v1/webhooks/midtrans`.

## Alur webhook (`backend/internal/payment/webhook.go`)

1. Verifikasi `signature_key` = SHA512(order_id + status_code + gross_amount + server_key), dibandingkan constant-time.
2. Simpan ke `payment_events` dengan `UNIQUE (order_id, transaction_status)` → notifikasi dobel diabaikan.
3. Lunas hanya bila `settlement`, atau `capture` dengan `fraud_status = accept`.
4. Nominal `gross_amount` harus sama dengan `orders.total`; bila beda → tidak ditandai lunas, dicatat di `audit_logs`.
5. Status order hanya bergerak maju (`pending/expired → paid`, `pending → expired/cancelled`), sehingga notifikasi yang datang tidak berurutan tidak merusak data.
6. Pesan `order.paid` / `order.released` ditulis ke `outbox` dalam transaksi yang sama, lalu handler membalas 200 dengan cepat.

Kode status ke Midtrans: 200 = selesai; 403 = signature salah; 5xx = Midtrans akan mengirim ulang.

## Ledger double-entry (`backend/internal/ledger`)

Setiap kejadian uang menjadi satu jurnal (unik per `ref_type, ref_id`) yang total debit = total kredit. Saat order lunas:

| Akun | Debit | Kredit |
| --- | --- | --- |
| `kas_midtrans` | total − biaya PG | |
| `pendapatan_platform` | | fee platform |
| `saldo_mitra_tertahan` (dengan `org_id`) | | sisanya |

Biaya PG saat webhook masih **estimasi** per metode bayar (`EstimatePGFee`). Rekonsiliasi harian dengan laporan settlement Midtrans menjadi pekerjaan berikutnya.

## Pencairan ke mitra (rencana)

Tabel `payouts` dan `payout_accounts` sudah ada. Alurnya:

1. Dana pindah `saldo_mitra_tertahan → saldo_mitra_tersedia` sesuai kebijakan (mis. 70% H+3 setelah settlement, 30% setelah race selesai).
2. Mitra (peran `owner`/`finance`) mengajukan payout ke rekening yang sudah divalidasi.
3. Maker-checker: sistem membuat payout lewat [Midtrans Payouts API](https://docs.midtrans.com/docs/disbursement-overview) (peran *creator*), finance platform menyetujui (*approver*).
4. Status dari notifikasi Payouts; gagal → saldo dikembalikan.

> Catatan legal: skema "platform menampung dana lalu mencairkan ke mitra" perlu dikonfirmasi ke Midtrans dan konsultan hukum sebelum dipakai dengan uang sungguhan. Untuk proyek pribadi, cukup di sandbox.
