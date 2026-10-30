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

## Keuangan mitra: saldo, pencairan, refund

Kode: `backend/internal/ledger` (jurnal, saldo, integritas) dan `backend/internal/finance` (rilis saldo, pencairan, refund, laporan). Prinsipnya:

- **Setiap perubahan uang = satu jurnal** di transaksi yang sama dengan perubahan statusnya. `(ref_type, ref_id)` deterministik, jadi worker yang jalan dua kali, klik ganda, atau retry tidak pernah menggandakan uang.
- **Ledger append-only.** Trigger `ledger_immutable` menolak `UPDATE`/`DELETE`/`TRUNCATE` pada `ledger_journals` dan `ledger_entries`. Koreksi selalu jurnal baru.
- **Saldo tidak disimpan di kolom mana pun**; selalu dihitung dari `ledger_entries` (`ledger.OrgBalances`).
- **Panggilan ke gateway tidak pernah di dalam transaksi yang memegang kunci.** Worker mengklaim baris dengan transaksi pendek (sewa 2 menit lewat `next_attempt_at`), memanggil gateway, lalu mencatat hasilnya di transaksi baru.

### Jurnal per kejadian (contoh: tiket Rp350.000 via VA, fee platform 3%)

Biaya PG estimasi VA = Rp4.000 + PPN 11% = **Rp4.440**. Fee platform = **Rp10.500**. Bagian mitra = 350.000 − 4.440 − 10.500 = **Rp335.060**.

| Kejadian (`ref_type`, `ref_id`) | Debit | Kredit |
| --- | --- | --- |
| Order lunas `order.paid`, order | `kas_midtrans` 345.560 | `pendapatan_platform` 10.500 · `saldo_mitra_tertahan` (org) 335.060 |
| Masa tahan lewat `balance.release`, order | `saldo_mitra_tertahan` 335.060 | `saldo_mitra_tersedia` 335.060 |
| Pencairan diajukan `payout.request`, payout | `saldo_mitra_tersedia` 300.000 | `payout_dalam_proses` 300.000 |
| Pencairan ditolak `payout.reject`, payout | `payout_dalam_proses` 300.000 | `saldo_mitra_tersedia` 300.000 |
| Transfer gagal `payout.fail`, payout | `payout_dalam_proses` 300.000 | `saldo_mitra_tersedia` 300.000 |
| Transfer selesai `payout.complete`, payout | `payout_dalam_proses` 300.000 | `kas_midtrans` 300.000 |
| Refund disetujui `order.refund`, order | `saldo_mitra_tertahan` **atau** `saldo_mitra_tersedia` 335.060 · `pendapatan_platform` 10.500 · `beban_refund_pg` 4.440 | `kas_midtrans` 350.000 |

Semua akun mitra memakai `org_id`. Jumlah rilis dan refund **diambil dari entri jurnal `order.paid` aslinya** (`ledger.PaidOf`), bukan dihitung ulang.

### Masa tahan

Hak mitra satu order pindah tertahan → tersedia bila tanggal hari ini (WIB) ≥ `events.race_date` + `PAYOUT_HOLD_DAYS` (default 3) dan order masih `paid`. Worker menjalankannya saat start dan tiap jam; idempotent dan aman paralel (baris order dikunci, jurnal unik per order). Order yang di-refund sebelum rilis tidak pernah dirilis. Development: `make dev-release ORG=slug` merilis semua saldo tertahan org tanpa menunggu (ditolak bila `APP_ENV` bukan `development`; tidak ada tombol/endpoint web).

### Rekening pencairan

Rekening = rekening dari pengajuan verifikasi yang **disetujui**. Saat super admin menyetujui (Tugas 05), di transaksi yang sama baris `payout_accounts` org dibuat/diganti dengan ciphertext yang sama (`account_enc`, kunci `KYC_ENC_KEY`) + `account_last4`, `verification_id`, `verified_at`. Satu rekening per org (`UNIQUE (org_id)`). Org yang verified lewat jalur lama (seed, `AUTO_VERIFY_ORGS`, `make verify`) tidak punya rekening → pencairan ditolak `no_payout_account` dengan pesan "Lengkapi verifikasi penyelenggara dan rekening". Mengganti rekening belum ada.

### Pencairan (maker-checker)

1. **Owner/finance mengajukan** `POST /v1/org/{org}/payouts {amount}` + header `Idempotency-Key` (frontend membuat satu kunci per niat, bukan per klik). Dalam satu transaksi dengan `SELECT … FOR UPDATE` pada baris org: syarat org `verified`, punya rekening, tidak ada pencairan berjalan (indeks unik parsial `payouts_active_uniq`), saldo tersedia tidak negatif, `PAYOUT_MIN ≤ jumlah ≤ saldo tersedia`, jumlah bilangan bulat. Lalu `payouts` (`requested`, `account_snapshot` bank/nama/last4) + jurnal `payout.request`.
2. **Super admin memutuskan** di `/admin/pencairan`. Anggota org → `conflict_of_interest`; pengaju = penyetuju juga ditolak (dan dijaga constraint `payouts_maker_checker`). Tolak = alasan 10–1.000 karakter, status `rejected`, jurnal `payout.reject`. Setuju = `approved`.
3. **Worker mengeksekusi** (tiap 15 detik): `approved` → `Disburser.Create` (idempoten per id payout) → `processing` + `reference_no`. Kegagalan sementara (timeout, 5xx, 429, respons rusak) diulang dengan backoff 30 dtk, 1, 2, 4 menit; setelah 5 percobaan → `failed`. Penolakan gateway (4xx) → langsung `failed`. `failed` = jurnal `payout.fail`, saldo kembali.
4. **Cek status** payout `processing` tiap putaran worker (pola `payment/status.go`, tanpa webhook): `completed` → jurnal `payout.complete` + `completed_at`. Status hanya maju; `completed`/`failed` tidak pernah berubah lagi.

**Mode `DISBURSE_MODE`:**

| Mode | Perilaku |
| --- | --- |
| `mock` (default) | Tanpa jaringan. `processing → completed` setelah `DISBURSE_MOCK_DELAY` (default 5 dtk). **Rekening berakhiran `0000` selalu gagal** (untuk menguji alur gagal). Di luar development, API & worker mencatat peringatan keras dan `/admin/*` keuangan menampilkan banner merah "tidak ada uang sungguhan yang dikirim". |
| `midtrans` | Midtrans Payouts (Iris) sandbox, `IRIS_BASE_URL` + `IRIS_CREATOR_KEY` + `IRIS_APPROVER_KEY`. Tanpa kunci (atau tanpa `MIDTRANS_SERVER_KEY` untuk refund) API & worker menolak start. Kunci Iris didaftarkan sendiri oleh pemilik proyek dan diisi manual. |

**Klien Iris** (`internal/finance/iris.go`), dicocokkan dengan SDK resmi [`midtrans-go/iris`](https://github.com/Midtrans/midtrans-go/tree/master/iris) karena dokumentasi web Iris tidak bisa diakses saat ditulis:

| Panggilan | Endpoint | Field |
| --- | --- | --- |
| Buat payout (kunci creator) | `POST {base}/payouts` | `payouts[].beneficiary_name, beneficiary_account, beneficiary_bank, beneficiary_email, amount ("150000.00"), notes` → `payouts[].status, reference_no` |
| Setujui (kunci approver) | `POST {base}/payouts/approve` | `reference_nos[]` (`otp` tidak dikirim) |
| Status (kunci creator) | `GET {base}/payouts/{reference_no}` | `status`, `error_message` |

**Asumsi yang belum terverifikasi** (tandai ulang saat punya akses dokumentasi/sandbox Iris): Basic auth `base64("<kunci>:")`; header `X-Idempotency-Key` dihormati (kunci = `payouts.id`, karena `reference_no` dibuat Iris, bukan oleh kita); persetujuan tanpa OTP; nilai status `queued|approved|processed|completed|failed|rejected`; kode bank `bsm` untuk BSI, bank di luar daftar ditolak "kirim manual". Uji: `go test ./internal/finance` memakai server HTTP palsu (sukses, ditolak, 5xx, timeout, respons rusak, respons ganda/idempoten, kunci tidak muncul di log/error).

### Refund

- Satuan = **seluruh order** `paid` atau `needs_refund`. Owner/finance mengajukan (`POST /v1/org/{org}/orders/{id}/refund`, alasan 10–500 karakter); super admin menyetujui/menolak, atau membuat refund langsung yang otomatis disetujui. Satu refund aktif/selesai per order (`refunds_order_active_uniq`).
- **Saat disetujui, satu transaksi:** order → `refunded`, semua tiket → `refunded` (scanner menolaknya), jurnal `order.refund`. Bagian mitra ditarik dari saldo tersedia bila order sudah dirilis, selain itu dari tertahan. **Saldo boleh negatif** = utang mitra; selama negatif pencairan ditolak (`insufficient_balance`) sampai tertutup penjualan baru. Order `needs_refund` belum pernah punya `order.paid` (worker berhenti saat kuota habis), jadi penerimaan uangnya dibukukan dulu lalu langsung dibalik.
- **Biaya PG tidak kembali** saat refund: selisih `total − kas yang diterima` dibukukan ke `beban_refund_pg` (ditanggung platform), pembeli menerima `total` penuh.
- **Eksekusi** lewat `Refunder` (mengikuti `DISBURSE_MODE`). `midtrans` = Core API `POST /v2/{order_id}/refund` (`refund_key` = id refund, idempoten) dengan `MIDTRANS_SERVER_KEY`; dicocokkan dengan [dokumentasi refund](https://docs.midtrans.com/reference/refund-transaction): `status_code` 200 = selesai; 412/414/406/4xx lain (mis. transfer VA tidak bisa di-refund lewat API) → `manual_required`; 5xx/timeout → diulang, setelah 5 kali `failed`. Super admin mengirim dana di luar sistem lalu menekan **Tandai sudah dikembalikan** dengan nomor referensi → `completed`. Mode `mock`: selesai otomatis, kecuali **alasan refund memuat kata "manual"** → `manual_required` (untuk menguji alur manual).
- Jurnal ditulis saat **disetujui** (kejadian ekonominya sudah pasti), bukan saat uang sampai.
- **Kuota tidak dikembalikan** ke penjualan (sederhana dan aman terhadap Redis). Saran fase berikut: kembalikan slot lewat outbox `order.released` yang sudah ada, hanya bila penjualan masih buka.
- Super admin tetap boleh me-refund order yang tiketnya sudah `collected`; UI memberi peringatan.

### Laporan & integritas

- Mitra: ringkasan penjualan per event (order lunas, bruto, fee platform, biaya PG estimasi, refund, bersih; semua dari ledger) dan mutasi saldo; keduanya bisa diunduh CSV (UTF-8 BOM, pemisah `;`, sel berawalan `= + - @ tab CR` diberi awalan `'` — termasuk angka negatif, tanpa data peserta). Unduhan tercatat di `audit_logs` (`report.download`).
- `GET /v1/admin/ledger/check` (`ledger.Check`): total debit = kredit, setiap jurnal seimbang, tidak ada `payout_dalam_proses` negatif, total pencairan aktif = saldo `payout_dalam_proses` (global dan per org). Ditampilkan di `/admin/keuangan`; tes memanggilnya setelah tiap skenario.
- **Rekonsiliasi manual:** uang masuk dari pembeli − refund − pencairan selesai − biaya PG estimasi = saldo `kas_midtrans`; `pendapatan_platform` + bagian mitra (tertahan + tersedia + dalam proses + sudah dicairkan) + biaya PG order yang tidak di-refund = uang masuk − refund.

### Audit & email

Pengajuan, persetujuan, penolakan, eksekusi, kegagalan, refund, "tampilkan rekening", dan unduh laporan tercatat di `audit_logs` (aktor, org, entitas, sebelum/sesudah ringkas) **tanpa nomor rekening**. Email lewat outbox: pencairan diajukan/disetujui/ditolak/selesai/gagal ke owner + finance (nama org, jumlah, status, alasan, `last4`); refund ke pembeli (disetujui/selesai/ditolak) dan ke owner + finance setiap status berubah.

> **Catatan legal:** skema "platform menampung dana pembeli lalu mencairkannya ke mitra" perlu dikonfirmasi ke Midtrans dan konsultan hukum sebelum dipakai dengan uang sungguhan. Implementasi ini tidak mengklaim kepatuhan regulasi apa pun; untuk proyek pribadi cukup di sandbox dan mode mock.
