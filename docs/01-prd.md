# 01 · PRD ringkas

RaceGo adalah marketplace tiket event lari multi-penyelenggara: banyak company/instansi menjual tiket 5K, 10K, 21K, dan 42K di satu platform, dibayar lewat Midtrans, lalu dananya dicairkan ke rekening mitra setelah dipotong fee platform.

## Masalah

- Penyelenggara harus membuat web pendaftaran sendiri tiap event, dan sering tumbang saat war tiket.
- Pelari mengisi ulang data diri, ukuran jersey, kontak darurat di setiap web event.
- Rekonsiliasi pembayaran, refund, dan data peserta masih manual (spreadsheet).

## Peran

| Peran | Kebutuhan utama |
| --- | --- |
| Pelari | Daftar cepat saat war tiket, beli untuk teman, tiket QR |
| Owner / admin mitra | Buat event, kuota, harga; lihat penjualan & saldo; ajukan pencairan |
| Staf RPC (race pack collection) | Scan QR, cocokkan KTP, tandai race pack diambil |
| Super admin platform | Verifikasi mitra, atur fee, setujui payout, pantau war tiket |

Satu user bisa menjadi pelari sekaligus staf di beberapa mitra (RBAC per tenant).

## Ruang lingkup MVP

1. Katalog event & kategori (kuota, harga, batas slot per akun), pencarian kota/bulan/jarak, kalender race, arsip race yang sudah lewat, dan halaman profil tiap penyelenggara.
2. Reservasi slot atomik dengan timer 15 menit, pembelian untuk beberapa peserta.
3. Pembayaran Midtrans Snap (VA, QRIS, e-wallet, kartu) + webhook idempotent.
4. Tiket QR + nomor BIB terbit otomatis, dikirim via email, tampil di **Tiket saya**.
5. Dashboard mitra + scanner race pack di web yang sama.
6. **Portal mitra mandiri**: siapa pun yang login bisa mendaftarkan penyelenggara, membuat event + kategori tiket, mengunggah poster/logo, menerbitkan event (setelah penyelenggara terverifikasi), dan mengundang staf. Tanpa campur tangan tim RaceGo selain verifikasi.
7. Ledger double-entry dan saldo mitra; pencairan via Midtrans Payouts (fase berikutnya).

Di luar MVP: aplikasi native, live tracking, hasil race/timing chip, marketplace merchandise.

## Model bisnis

- Fee platform per tiket (default 3%, disimpan per mitra di `organizations.fee_bps`), saat ini ditanggung mitra.
- Biaya payment gateway mengikuti metode bayar (mis. VA Rp4.000 + PPN 11% = Rp4.440).

Contoh tiket 21K Rp350.000 via VA: biaya PG Rp4.440, fee platform Rp10.500, hak mitra **Rp335.060** (diuji di `backend/internal/ledger/ledger_test.go`).

## KPI target

| Metrik | Target |
| --- | --- |
| Oversell | 0 tiket |
| API reservasi | p95 < 300 ms saat puncak |
| Ketersediaan saat jendela penjualan | 99,9% |
| Konversi reservasi → lunas | > 70% |
| Selisih rekonsiliasi harian | Rp0 |
