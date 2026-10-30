# Tugas 06 · Saldo mitra, pencairan dana, dan refund

> Prompt untuk AI coding agent (Claude Code CLI). Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/06-keuangan-pencairan-refund.md sampai semua kriteria selesai terpenuhi"`
> Prasyarat: tugas 01–05 sudah ter-commit. Migrasi terakhir yang ada: `0006`. Migrasi baru di tugas ini: `0007`.

## Konteks

RaceGo: backend Go (`backend/`), frontend Next.js 16 (`frontend/`), Postgres, Redis. Baca dulu `README.md`, `docs/02-arsitektur.md`, `docs/04-pembayaran-dan-ledger.md` (ledger + rencana pencairan), `docs/07-api.md`, `docs/08-development.md`, `docs/09-roadmap.md` (Fase 2 keuangan), dan **`frontend/AGENTS.md`** (Next.js 16 berbeda dari versi lama; docs di `frontend/node_modules/next/dist/docs/`). **Baca kodenya langsung**: ringkasan di bawah ditulis dari ingatan dan bisa meleset dari kode sebenarnya; kode yang menang, catat bedanya di laporan.

Keadaan sekarang:

- Ledger double-entry append-only (`internal/ledger`: `ledger_journals` unik per `(ref_type, ref_id)`, `ledger_entries` dengan `org_id`, `debit`/`credit`). Saat order lunas, worker menulis jurnal `order.paid`: debit `kas_midtrans` (total − biaya PG estimasi), kredit `pendapatan_platform` (fee platform), kredit `saldo_mitra_tertahan` (sisanya, dengan `org_id`). Konstanta akun lain sudah ada tetapi belum dipakai: `saldo_mitra_tersedia`, `payout_dalam_proses`, `refund_terutang`.
- Tabel `payouts` (status `requested|approved|processing|completed|failed|rejected`, `reference_no` unik, `requested_by`, `approved_by`) dan `payout_accounts` **ada tetapi belum dipakai**; `payout_accounts.account_number` berupa teks biasa, tidak boleh dipakai begitu saja (lihat keputusan 2).
- Tugas 05 membuat verifikasi mitra: `org_verifications` menyimpan rekening **terenkripsi** (`account_enc`, `account_last4`, `bank_code`, `bank_name`, `account_holder`), `internal/secretbox`, `internal/kyc`, super admin (`users.is_super_admin`, area `/admin`, `/v1/admin/*`, 404 untuk non-super-admin), aturan benturan kepentingan (super admin yang anggota org tidak boleh memutuskan), audit akses, email lewat outbox (`notify`, `worker`).
- Rekonsiliasi pembayaran (`internal/payment/status.go`) memakai pola "worker memeriksa status tiap menit" karena webhook tidak sampai ke localhost. Pola yang sama dipakai di sini untuk status pencairan.
- Orders: status `pending|paid|expired|cancelled|refunded|needs_refund`. Tiket: `issued|collected|cancelled|refunded`. Scanner sudah menolak tiket `refunded`. Order `needs_refund` (bayar telat, stok habis) sudah ada tetapi belum ada alur yang menyelesaikannya.
- Midtrans sandbox untuk pembayaran sudah ada di `.env`. **Payouts (Iris) memakai kunci API terpisah dan harus didaftarkan sendiri oleh pemilik proyek**, jadi tugas ini harus bisa selesai dan lulus tanpa kunci Iris (mode mock).

## Tujuan

1. Uang mitra bergerak otomatis `tertahan → tersedia` setelah masa tahan.
2. Mitra melihat saldo + mutasi, lalu mengajukan **pencairan** ke rekening yang sudah terverifikasi; **super admin menyetujui** (maker-checker); sistem mengeksekusi lewat pencairan (mock atau Midtrans Payouts) dan membukukan semuanya di ledger tanpa ada rupiah yang hilang atau ganda.
3. **Refund** per order: diajukan mitra atau dibuat super admin, disetujui super admin, tiket dibatalkan, ledger dikoreksi.
4. Mitra mengunduh laporan penjualan/mutasi (CSV). Super admin punya ringkasan keuangan platform + pemeriksaan integritas ledger.

## Keputusan desain (ikuti)

1. **Semua perubahan uang lewat ledger dalam satu transaksi dengan perubahan statusnya.** Satu kejadian = satu jurnal dengan `(ref_type, ref_id)` yang deterministik, sehingga pengulangan (worker jalan dua kali, klik ganda, retry) tidak menggandakan uang. Jurnal tidak pernah diubah/dihapus; koreksi = jurnal baru. Saldo selalu **dihitung dari `ledger_entries`** (bukan kolom saldo terpisah).
2. **Rekening pencairan = rekening dari verifikasi yang disetujui.** Saat super admin menyetujui pengajuan (alur Tugas 05), dalam transaksi yang sama buat/ganti baris `payout_accounts` untuk org itu: `bank_code`, `bank_name`, `account_holder`, nomor rekening **terenkripsi** (sama `secretbox`) + `account_last4`, `verified_at = now()`, `verification_id`. Migrasi 0007 mengubah `payout_accounts` (tabel belum berisi data): ganti `account_number`/`account_name` teks biasa menjadi kolom terenkripsi + `last4`, tambah `bank_name`, `verification_id`, dan batasan **satu rekening aktif per org**. Org yang sudah `verified` sebelum tugas ini (seed, `AUTO_VERIFY_ORGS`, `make verify`) **tidak punya rekening** → tidak bisa mencairkan, dengan pesan jelas "Lengkapi verifikasi penyelenggara dan rekening". Mengganti rekening **di luar cakupan**; tombolnya tidak ada.
3. **Masa tahan.** Hak mitra dari satu order pindah `saldo_mitra_tertahan → saldo_mitra_tersedia` bila `events.starts_at` (hari lomba; cek nama kolom sebenarnya) + `PAYOUT_HOLD_DAYS` (default 3) sudah lewat dan order masih `paid`. Jurnal `balance.release`, `ref_id = order_id`, jumlah = bagian mitra dari jurnal `order.paid` order itu (ambil dari entri `saldo_mitra_tertahan` jurnal aslinya, jangan dihitung ulang). Dijalankan worker berkala (tiap jam, idempotent) dan juga lewat perintah dev (keputusan 12). Order yang di-refund sebelum rilis tidak dirilis.
4. **Pengajuan pencairan.** Peran `owner` dan `finance` yang boleh mengajukan; `admin` dan `rpc_staff` ditolak. Syarat: org `verified`, punya rekening, jumlah bilangan bulat rupiah, `PAYOUT_MIN` (default 50000) ≤ jumlah ≤ saldo tersedia, saldo tersedia tidak negatif. Dalam **satu transaksi** dengan `SELECT … FOR UPDATE` pada baris org: hitung saldo tersedia dari ledger, buat `payouts` (`requested`), tulis jurnal `payout.request` (debit `saldo_mitra_tersedia` org, kredit `payout_dalam_proses` org). Dua pengajuan bersamaan tidak boleh melewati total saldo (wajib ada tes konkurensi). Maksimal 1 pengajuan berstatus `requested|approved|processing` per org pada satu waktu (sederhana dan aman).
5. **Persetujuan (maker-checker).** Super admin memutuskan (404 bagi non-super-admin; benturan kepentingan seperti Tugas 05 → `conflict_of_interest`). Yang mengajukan bukan yang menyetujui (otomatis terpenuhi, tetap diperiksa di kode: `approved_by <> requested_by` untuk jaga-jaga bila super admin kebetulan juga pengaju). Tolak wajib alasan 10–1.000 karakter → status `rejected` + jurnal pembalik `payout.reject` (debit `payout_dalam_proses`, kredit `saldo_mitra_tersedia`). Setuju → status `approved` lalu eksekusi (keputusan 6).
6. **Eksekusi lewat antarmuka `Disburser`** dengan dua implementasi dipilih env `DISBURSE_MODE=mock|midtrans`:
   - `mock` (default di development dan tes): tidak ada panggilan jaringan; menyimulasikan siklus `processing → completed` setelah jeda singkat yang bisa diatur (`DISBURSE_MOCK_DELAY`), dan mendukung memaksa gagal untuk tes (mis. nomor rekening khusus yang berakhiran tertentu atau header/variabel khusus tes; pilih cara paling sederhana dan tulis di dokumen).
   - `midtrans`: klien HTTP ke **Midtrans Payouts (Iris)** sandbox: `IRIS_BASE_URL` (default `https://app.sandbox.midtrans.com/iris/api/v1`), `IRIS_CREATOR_KEY`, `IRIS_APPROVER_KEY` (Basic auth), membuat payout (`reference_no` = `payouts.id` agar idempoten), menyetujuinya, lalu memeriksa status. Cocokkan nama endpoint/field dengan dokumentasi resmi Midtrans bila bisa diakses; **jika tidak bisa memastikan, tandai asumsi di kode dan laporan**, jangan mengarang diam-diam. Wajib diuji dengan **server HTTP palsu** (tanpa jaringan nyata): sukses, ditolak, timeout, respons rusak, respons ganda/idempoten. Kunci tidak boleh muncul di log. `DISBURSE_MODE=midtrans` tanpa kunci → API menolak start dengan pesan jelas; di produksi default tetap `mock` **dengan peringatan keras di log dan di halaman admin** ("mode mock: tidak ada uang sungguhan yang dikirim").
7. **Status dan pembukuan eksekusi.** `approved → processing` (permintaan ke Disburser diterima, simpan `reference_no`) → `completed` atau `failed`. Worker memeriksa status payout `processing` tiap menit (pola `payment/status.go`; tidak bergantung webhook). `completed`: jurnal `payout.complete` (debit `payout_dalam_proses`, kredit `kas_midtrans`), simpan `completed_at`. `failed`: jurnal `payout.fail` (debit `payout_dalam_proses`, kredit `saldo_mitra_tersedia`) sehingga saldo kembali; simpan alasan singkat. Kegagalan sementara (timeout/5xx) **tidak** langsung `failed`: retry dengan backoff sampai batas, baru `failed`. Setiap transisi idempotent dan hanya maju (tidak ada `completed → failed`). Panggilan eksekusi tidak boleh terjadi di dalam transaksi DB yang memegang kunci lama; gunakan pola outbox/worker yang sudah ada.
8. **Refund.**
   - Satuan = **seluruh order** (tidak ada refund parsial) yang berstatus `paid` (atau `needs_refund`).
   - Siapa: `owner`/`finance` org mengajukan (alasan wajib 10–500 karakter); super admin bisa membuat refund langsung untuk order mana pun atau menyetujui yang diajukan. Keputusan akhir selalu super admin (benturan kepentingan berlaku). Tabel `refunds`: `id`, `order_id` (unik per refund aktif/selesai), `org_id`, `amount`, `status` (`requested|approved|processing|manual_required|completed|rejected|failed`), `reason`, `requested_by`, `approved_by`, `reference`, `reject_reason`, timestamp.
   - Efek saat disetujui, dalam satu transaksi: order → `refunded`, semua tiket order → `refunded` (scanner otomatis menolaknya), jurnal `order.refund`: **kredit `kas_midtrans` sebesar `total`** (pembeli menerima penuh); debit bagian mitra dari order itu (dari akun `saldo_mitra_tersedia` bila order sudah dirilis, selain itu `saldo_mitra_tertahan`; **saldo boleh menjadi negatif** = utang mitra yang menghalangi pencairan berikutnya sampai tertutup penjualan baru); debit `pendapatan_platform` sebesar fee platform; selisih (biaya PG yang tidak kembali, = `total` − nilai debit `kas_midtrans` pada jurnal `order.paid` aslinya) dibukukan ke akun baru `beban_refund_pg` (ditanggung platform). Tulis tes angka untuk kedua kasus (sebelum dan sesudah rilis).
   - Eksekusi uang ke pembeli lewat antarmuka `Refunder` (`mock` dan `midtrans`, mengikuti `DISBURSE_MODE`; `midtrans` memakai Core API refund dengan kunci server yang sudah ada, diuji dengan server palsu). Banyak metode (mis. transfer VA) **tidak bisa di-refund lewat API**: bila gateway menjawab tidak didukung → status `manual_required`; super admin mengirim dana di luar sistem lalu menekan "Tandai sudah dikembalikan" dengan nomor referensi → `completed`. Jurnal ledger-nya tetap ditulis saat disetujui (kejadian ekonominya sudah pasti), bukan saat selesai.
   - Stok kuota **tidak dikembalikan** ke penjualan (sederhana dan aman terhadap Redis); catat di dokumen sebagai keputusan, plus saran untuk fase berikutnya.
   - Refund order yang sudah punya tiket `collected` (paket sudah diambil) tetap boleh oleh super admin tetapi UI memberi peringatan.
   - Email ke pembeli (tanpa data sensitif) saat refund disetujui/selesai/ditolak dan ke owner/finance saat status berubah, lewat outbox.
9. **Laporan.** Mitra: ringkasan per event (jumlah order lunas, bruto, fee platform, biaya PG estimasi, refund, bersih) dan **mutasi saldo** (daftar entri ledger org dengan keterangan yang manusiawi), keduanya bisa diunduh sebagai CSV. CSV wajib aman dari injeksi rumus (sel yang diawali `=`, `+`, `-`, `@`, tab, atau CR diawali `'`), memakai pemisah/pengkodean yang terbuka benar di Excel Indonesia (UTF-8 BOM) dan tidak memuat data pembeli yang tidak perlu (tanpa NIK/ID peserta).
10. **Integritas.** Endpoint admin `GET /v1/admin/ledger/check`: total debit = total kredit global; setiap jurnal seimbang; tidak ada saldo `payout_dalam_proses` negatif; jumlah `payouts` aktif = saldo `payout_dalam_proses`; hasil ditampilkan di `/admin/keuangan`. Fungsi yang sama dipanggil oleh tes setelah tiap skenario.
11. **Audit & email.** Setiap pengajuan, persetujuan, penolakan, eksekusi, kegagalan, refund, dan unduh laporan tercatat di `audit_logs` (aktor, org, entitas, sebelum/sesudah ringkas; tanpa nomor rekening). Email (outbox): pencairan diajukan/disetujui/ditolak/selesai/gagal ke owner+finance; isinya hanya nama org, jumlah, status, alasan, `last4` rekening.
12. **Perintah development** `make dev-release ORG=<slug>` (`cmd/migrate release`): merilis semua saldo tertahan org itu tanpa menunggu masa tahan; **menolak jalan bila `APP_ENV` bukan `development`**. Tidak ada tombol/endpoint web untuk ini.
13. **Konfigurasi baru** (semua didokumentasikan di `.env.example` dan `.env.prod.example`): `PAYOUT_HOLD_DAYS=3`, `PAYOUT_MIN=50000`, `DISBURSE_MODE=mock`, `DISBURSE_MOCK_DELAY`, `IRIS_BASE_URL`, `IRIS_CREATOR_KEY`, `IRIS_APPROVER_KEY`. Nilai tidak valid → API menolak start dengan pesan jelas.

## Backend

### Migrasi `0007_finance.up.sql` + `.down.sql` (jangan ubah migrasi lama)

- `payout_accounts` (keputusan 2), `payouts` tambah: `account_snapshot` (bank/nama/`last4` saja untuk riwayat; **tanpa nomor lengkap**), `reject_reason`, `fail_reason`, `attempts`, `next_attempt_at`, `completed_at`; indeks unik parsial satu payout aktif per org.
- `refunds` (keputusan 8) dengan indeks unik agar satu order tidak punya dua refund aktif/selesai.
- Akun ledger baru `beban_refund_pg` (konstanta di `internal/ledger`).
- Pastikan tidak ada cara mengubah/menghapus `ledger_entries`/`ledger_journals` dari aplikasi (jika belum ada, pasang trigger `BEFORE UPDATE OR DELETE` yang menolak, dengan tes).

### Paket

- `internal/ledger/`: konstruktor jurnal baru (`BalanceRelease`, `PayoutRequest`, `PayoutReject`, `PayoutComplete`, `PayoutFail`, `OrderRefund`), fungsi saldo per org (`Balances(ctx, orgID)` → tertahan, tersedia, dalam proses, sudah dicairkan), pemeriksaan integritas. Semua uang `int64` rupiah, tanpa float.
- `internal/payout/` (service, `Disburser` mock + midtrans, worker steps), `internal/refund/` (service, `Refunder`), `internal/report/` (ringkasan + CSV), atau struktur setara yang konsisten dengan modul lain dan dependensinya satu arah (tidak melingkar).
- `server/router.go`, `config`, `worker` (rilis saldo tiap jam; eksekusi dan pemeriksaan status payout; refund), `notify` (email baru), `cmd/migrate` (`release`), `Makefile` (`dev-release`), integrasi dengan persetujuan KYC (keputusan 2).

### Endpoint

Mitra (`/v1/org/{org}/…`; non-anggota 404; peran di luar yang diizinkan ditolak sesuai pola `RequireRole`):

| Method | Path | Peran | Keterangan |
| --- | --- | --- | --- |
| GET | `/finance/summary` | owner, finance | saldo tertahan/tersedia/dalam proses/sudah dicairkan, rekening tersamar, aturan (min, masa tahan), apakah boleh mencairkan + alasan bila tidak |
| GET | `/finance/ledger?page=` | owner, finance | mutasi saldo |
| GET | `/payouts` | owner, finance | riwayat pencairan |
| POST | `/payouts` | owner, finance | `{amount}`; header `Idempotency-Key` wajib (klik ganda/retry aman) |
| GET | `/orders?event=&q=&status=&page=` | owner, finance | order milik org (cari email/ID order) untuk refund; tanpa data sensitif peserta |
| POST | `/orders/{id}/refund` | owner, finance | `{reason}` mengajukan refund |
| GET | `/refunds` | owner, finance | status refund org |
| GET | `/reports/sales.csv`, `/reports/ledger.csv` | owner, finance | CSV, tercatat di audit |

Super admin (`/v1/admin/…`; 404 bila bukan super admin):

| Method | Path | Keterangan |
| --- | --- | --- |
| GET | `/payouts?status=&page=` | antrean, default `requested` terlama dulu |
| GET | `/payouts/{id}` | detail: org, jumlah, rekening tersamar + "tampilkan" (tercatat seperti Tugas 05), saldo org saat ini, riwayat pencairan org |
| POST | `/payouts/{id}/approve`, `/payouts/{id}/reject` | keputusan 5 |
| GET | `/refunds?status=&page=` · POST `/refunds` (buat langsung) · POST `/refunds/{id}/approve` · `/reject` · `/mark-done` | keputusan 8 |
| GET | `/finance/overview` | total tertahan/tersedia/dalam proses seluruh mitra, pendapatan platform, mode pencairan (peringatan bila mock) |
| GET | `/ledger/check` | keputusan 10 |

Kode error baru memakai format `httpx.Error` yang ada: `insufficient_balance`, `below_minimum`, `payout_in_progress`, `no_payout_account`, `org_not_verified`, `order_not_refundable`, `already_refunded`, `conflict_of_interest`, `invalid_state`.

### Tes backend (wajib, `go test ./...` hijau, tes Postgres/Redis **tidak** di-skip)

- Jurnal: tiap konstruktor seimbang; angka contoh benar (Rp350.000 via VA: bagian mitra Rp335.060 dari `ledger_test.go`; refund sebelum rilis, sesudah rilis, saldo menjadi negatif, selisih biaya PG ke `beban_refund_pg`); jumlah tidak valid ditolak; trigger menolak UPDATE/DELETE ledger.
- Rilis saldo: sebelum masa tahan tidak dirilis, sesudahnya dirilis tepat sekali meski worker dijalankan berkali-kali dan paralel; order ter-refund tidak dirilis; jumlah rilis = bagian mitra jurnal asli.
- Pengajuan: matriks peran (owner/finance ok; admin/rpc_staff ditolak; non-anggota 404; anonim 401); org belum `verified`/tanpa rekening ditolak; di bawah minimum, melebihi saldo, tidak bulat, saldo negatif ditolak; satu payout aktif per org; **tes konkurensi** (≥ 20 permintaan paralel dengan total melebihi saldo → total yang lolos ≤ saldo; sisanya ditolak rapi); `Idempotency-Key` yang sama tidak membuat dua payout.
- Persetujuan: non-super-admin 404; super admin anggota org → `conflict_of_interest`; tolak tanpa alasan → 422; tolak mengembalikan saldo persis; setujui memanggil Disburser sekali meski endpoint diklik ganda/paralel.
- Eksekusi: mock sukses → `completed` + jurnal dan saldo benar; mock gagal → `failed` + saldo kembali; timeout sementara → retry dengan backoff lalu sukses; gagal permanen setelah batas; tidak ada transisi mundur; klien Midtrans diuji dengan server HTTP palsu (sukses, ditolak, 5xx, timeout, respons rusak, respons ganda); kunci tidak muncul di log atau pesan error.
- Refund: tiket jadi `refunded` dan scanner menolaknya; order jadi `refunded`; tidak bisa dua kali (paralel juga); order bukan `paid` ditolak; org lain tidak bisa mengajukan refund order org ini (404); `manual_required` → `mark-done` → `completed`; email pembeli tidak memuat data sensitif.
- Laporan: angka ringkasan cocok dengan ledger; CSV aman dari injeksi rumus (sel `=cmd`, `+1`, `-1`, `@x` ber-awalan `'`), BOM ada, tanpa NIK/ID peserta; unduhan tercatat di audit.
- Integritas: `ledger/check` hijau setelah setiap skenario di atas, dan **merah** bila tes sengaja merusak data (mis. menyisipkan entri tidak seimbang lewat SQL langsung pada DB tes).
- Konfigurasi: nilai tidak valid / `DISBURSE_MODE=midtrans` tanpa kunci → gagal start; `dev-release` menolak di luar development.
- Audit memuat semua aksi keuangan, **tidak memuat nomor rekening**; log aplikasi bersih dari nilai sensitif.
- Persetujuan KYC (Tugas 05) kini juga membuat `payout_accounts` terenkripsi; ganti rekening tidak ada; tes lama Tugas 05 tetap hijau.

## Frontend

Gaya visual dan komponen mengikuti yang ada. Form: label selalu terlihat (`Field`), pesan error per kolom, tombol disabled saat memproses, nyaman di HP 390px, dialog konfirmasi yang bisa dipakai keyboard. **Catatan dari Tugas 05:** tautan di dalam `<dialog>` modal membuat halaman tujuan tidak bisa diklik; selalu tutup dialog sebelum navigasi, dan periksa dialog yang sudah ada di portal mitra untuk pola yang sama.

**Mitra (`/org/[org]/keuangan`, tab baru "Keuangan" di `org-shell`, hanya owner/finance):**
- Kartu saldo: Tertahan, Tersedia, Dalam proses, Sudah dicairkan, masing-masing dengan penjelasan satu kalimat (mis. "Tertahan: dirilis 3 hari setelah hari lomba"). Saldo negatif ditampilkan jelas dengan penjelasan.
- Panel pencairan: rekening tersamar (`BCA ••••1234 a.n. …`), input jumlah (format rupiah, tombol "Cairkan semua"), batas minimum, pesan jelas bila tidak bisa (belum terverifikasi, tanpa rekening, saldo kurang, ada pencairan berjalan), dialog konfirmasi yang menampilkan jumlah dan tujuan, kunci `Idempotency-Key` dibuat per niat (bukan per klik).
- Riwayat pencairan dengan lencana status dan alasan penolakan/kegagalan; mutasi saldo (tabel di layar lebar, kartu di HP) dengan paginasi; tombol unduh CSV.
- Tab/seksi **Pesanan & refund**: daftar order lunas (filter event, cari email/ID), tombol "Ajukan refund" (dialog dengan alasan + peringatan bahwa tiket langsung tidak berlaku setelah disetujui), status refund.
- Ringkasan penjualan per event dengan unduh CSV.

**Super admin (`/admin/pencairan`, `/admin/pencairan/[id]`, `/admin/refund`, `/admin/keuangan`; menu Admin diperluas):**
- Antrean pencairan dengan jumlah, org, waktu, saldo org; detail dengan rekening tersamar + "Tampilkan", riwayat org, tombol Setujui/Tolak (alasan wajib + penghitung karakter); pesan jelas untuk `conflict_of_interest`.
- Antrean refund (buat langsung, setujui/tolak, tandai selesai manual dengan referensi), peringatan bila ada tiket `collected`.
- `/admin/keuangan`: ringkasan platform, hasil `ledger/check` (hijau/merah dengan rinciannya), **banner merah mencolok bila `DISBURSE_MODE=mock`** ("tidak ada uang sungguhan yang dikirim").
- Semua halaman admin tetap 404 bagi non-super-admin.
- `lib/api.ts`: tipe + fungsi baru.

## Skrip, produksi, dan dokumen

- `docker-compose.prod.yml` dan `.env.prod.example`: variabel baru; `gen-secrets.sh` tidak perlu menghasilkan kunci Iris (diisi manual), tetapi `scripts` tidak boleh mencetak kunci tersebut; periksa bahwa backup/restore tetap benar dengan tabel baru.
- Perbarui: `docs/04-pembayaran-dan-ledger.md` (alur lengkap, tabel jurnal untuk setiap kejadian beserta contoh angka, keputusan masa tahan/kuota/biaya PG, catatan bahwa skema platform menampung dan mencairkan dana perlu dikonfirmasi ke Midtrans dan konsultan hukum sebelum uang sungguhan, **tanpa mengklaim kepatuhan**), `docs/07-api.md`, `docs/02-arsitektur.md`, `docs/01-prd.md`, `docs/08-development.md` (`make dev-release`, cara menguji pencairan dengan mock), `docs/10-deploy.md`, `docs/09-roadmap.md` (centang yang selesai; tambahkan: ganti rekening, refund parsial, refund massal saat event dibatalkan, pengembalian kuota, rekonsiliasi dengan laporan settlement Midtrans, biaya transfer pencairan, 2FA super admin, notifikasi webhook Iris, pembatasan batas harian), `README.md`, `.env.example`, `.env.prod.example`, `make help`.

## Di luar cakupan

Mengganti rekening, refund parsial, pembatalan event massal, pengembalian kuota ke penjualan, rekonsiliasi dengan laporan settlement Midtrans (biaya PG masih estimasi), biaya transfer pencairan, webhook Iris, pajak/PPN/faktur, multi-mata uang, pembayaran mitra lewat metode selain transfer bank, 2FA, batas harian pencairan, dispute/chargeback. Jangan dikerjakan sekarang.

## Kriteria selesai (cek satu per satu dan laporkan dengan bukti nyata)

1. `make test` hijau (vet, tes Go dengan Postgres/Redis, lint, typegen, tsc), `cd frontend && npm run build` sukses, tes lama tidak diubah dan hijau. Semua tes baru di atas ada dan lulus (laporkan jumlah PASS dan 0 SKIP).
2. **Bukti pembukuan:** setelah rangkaian skenario nyata (beberapa order lunas, rilis, pencairan sukses, pencairan gagal, pencairan ditolak, refund sebelum dan sesudah rilis), tunjukkan hasil `ledger/check` hijau, saldo tiap akun, dan rekonsiliasi manual: jumlah uang masuk dari pembeli − refund − pencairan selesai − biaya PG estimasi = saldo `kas_midtrans`, dan `pendapatan_platform` + bagian mitra = pendapatan kotor setelah refund. Tampilkan angkanya (tanpa data pribadi).
3. **Uji konkurensi nyata:** jalankan 20+ permintaan pencairan paralel ke API sungguhan (`curl`/skrip) dengan total melebihi saldo; buktikan saldo tidak pernah menjadi negatif dan jumlah yang lolos benar. Lakukan hal sama untuk persetujuan ganda dan refund ganda.
4. **Uji browser sungguhan** (Playwright di folder sementara di luar repo; jangan mengubah `package.json`; OTP dari Mailpit; override env di baris perintah dengan `AUTO_VERIFY_ORGS=false`, `DISBURSE_MODE=mock`, `PAYOUT_HOLD_DAYS` sesuai kebutuhan; **jangan menyentuh `.env` pribadi**; semua dokumen uji bertuliskan "CONTOH — TIDAK ASLI"):
   - Mitra baru lolos verifikasi (alur Tugas 05) → pelari membeli beberapa tiket lewat pembayaran mock → saldo tertahan di dashboard mitra bertambah dengan angka yang cocok → `make dev-release` → saldo tersedia → pencairan diajukan → super admin menyetujui → status `processing` lalu `completed` otomatis → saldo, riwayat, dan email benar.
   - Pencairan ditolak (alasan terlihat, saldo kembali), pencairan gagal (mock), pencairan di bawah minimum / melebihi saldo / org tanpa rekening (pesan error jelas).
   - Refund: mitra mengajukan → super admin menyetujui → tiket pelari tidak lagi valid di scanner, order `refunded`, saldo mitra berkurang sesuai; refund kedua ditolak; satu refund `manual_required` → ditandai selesai dengan referensi.
   - Kasus negatif: pelari ke `/org/.../keuangan` dan `/admin/...` → ditolak/404; `admin` dan `rpc_staff` org ke endpoint keuangan → ditolak; mitra org B ke order/payout org A → 404; super admin anggota org → `conflict_of_interest`.
   - Unduh dua CSV, buka isinya dan buktikan injeksi rumus dinetralkan dan angkanya cocok dengan layar.
   - Screenshot tiap halaman/dialog baru di layar laptop **dan** HP 390px: **lihat sendiri** dan perbaiki yang rusak; tanpa geser horizontal.
   - Konsol browser dan log API/worker bersih dari error/warning baru; log tidak memuat nomor rekening atau kunci.
5. `DISBURSE_MODE=midtrans` dijalankan terhadap server HTTP palsu lokal yang meniru Iris (sukses, ditolak, timeout): hasilnya tepat; laporkan endpoint dan field yang dipakai, mana yang sudah dicocokkan dengan dokumentasi resmi dan mana yang masih asumsi.
6. Produksi lokal (`make prod-up` dengan `SITE_ADDRESS=localhost`, tanpa tunnel): satu putaran pencairan dan refund lewat Caddy/HTTPS berfungsi; banner mode mock tampil; backup → hancurkan volume → restore menjaga ledger dan `ledger/check` tetap hijau. Matikan dan bersihkan stack setelahnya.
7. Audit: cuplikan `audit_logs` yang membuktikan pengajuan, persetujuan, penolakan, eksekusi, kegagalan, refund, dan unduh laporan tercatat, tanpa nomor rekening.
8. Semua dokumen di atas diperbarui; `make help` menampilkan `dev-release`; `git status` bersih dari berkas uji, `.env`, atau data.

## Cara kerja

- Bertahap, uji tiap tahap: migrasi + konfigurasi → ledger (jurnal, saldo, integritas) + tes → rilis saldo + worker → pengajuan/persetujuan pencairan + Disburser mock + tes konkurensi → klien Midtrans + server palsu → refund → laporan CSV → frontend mitra → frontend admin → skrip/produksi → dokumentasi → uji browser, konkurensi nyata, dan produksi lokal.
- Uang adalah area paling berisiko: utamakan kebenaran dan idempotensi di atas kecepatan. Setiap perubahan saldo harus lewat jurnal; jangan menambah kolom saldo yang bisa menyimpang dari ledger.
- Jangan mengubah perilaku checkout, pembayaran (selain yang disebut), tiket, scanner, login, antrian, atau verifikasi (selain pembuatan `payout_accounts` saat disetujui). Bug di luar cakupan: catat di laporan akhir, jangan diperbaiki diam-diam kecuali menghalangi tugas ini.
- Jangan pernah menulis nomor rekening lengkap, kunci (`KYC_ENC_KEY`, kunci Iris, kunci server Midtrans) ke log, pesan error, email, audit, screenshot, atau laporan akhir.
- Bila keputusan di dokumen ini bertabrakan dengan kode atau kenyataan teknis, pilih opsi paling sederhana yang aman dan catat alasannya.
- Tanpa rahasia di repo, tanpa email sungguhan, tanpa panggilan ke Midtrans Payouts sungguhan, tanpa tunnel ke internet, tanpa menyentuh `.env` pribadi.
- Di akhir: ringkasan perubahan per file, tabel hasil tiap kriteria (berdasarkan uji nyata; tandai yang tidak bisa diuji), keputusan/penyimpangan, asumsi API Iris yang belum terverifikasi, dan usulan pesan commit. **Jangan commit dan jangan push.**
