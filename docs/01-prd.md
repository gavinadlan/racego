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
| Super admin platform | Verifikasi mitra (tinjau identitas, dokumen, rekening; setujui/tolak dengan alasan) di `/admin`; nanti: atur fee, setujui payout, pantau war tiket. Diberikan hanya lewat perintah server, tidak bisa meninjau org tempat dia sendiri anggota |

Satu user bisa menjadi pelari sekaligus staf di beberapa mitra (RBAC per tenant).

## Ruang lingkup MVP

1. Katalog event & kategori (kuota, harga, batas slot per akun), pencarian kota/bulan/jarak, kalender race, arsip race yang sudah lewat, dan halaman profil tiap penyelenggara.
2. Reservasi slot atomik dengan timer 15 menit, pembelian untuk beberapa peserta. **Antrian virtual opsional per event** untuk event yang peminatnya jauh melebihi kuota: lobi sebelum jam buka dengan urutan diundi, pembeli dipanggil bertahap, posisi & perkiraan waktu tampil. Belum mencegah akun ganda/bot (Turnstile & verifikasi identitas di luar cakupan saat ini).
3. Pembayaran Midtrans Snap (VA, QRIS, e-wallet, kartu) + webhook idempotent.
4. Tiket QR + nomor BIB terbit otomatis, dikirim via email, tampil di **Tiket saya**.
5. Dashboard mitra + scanner race pack di web yang sama.
6. **Portal mitra mandiri**: siapa pun yang login bisa mendaftarkan penyelenggara, membuat event + kategori tiket, mengunggah poster/logo, menerbitkan event (setelah penyelenggara terverifikasi), dan mengundang staf. Tanpa campur tangan tim RaceGo selain verifikasi.
7. **Verifikasi mitra (KYC)**: owner mengirim jenis penyelenggara (perorangan/komunitas/badan usaha), identitas PIC, dokumen sesuai jenis, dan rekening bank; super admin menyetujui atau menolak dengan alasan, mitra memperbaiki lalu mengajukan ulang. Dokumen dan NIK/rekening/NPWP disimpan terenkripsi dan setiap aksesnya tercatat. Tanpa OCR, cek rekening otomatis, atau layanan KYC pihak ketiga. Kebijakan retensi data belum ditetapkan; ini bukan klaim kepatuhan hukum.
8. **Keuangan mitra**: ledger double-entry append-only; saldo mitra tertahan → tersedia 3 hari setelah hari lomba; owner/finance mengajukan pencairan ke rekening hasil verifikasi, super admin menyetujui (maker-checker), transfer lewat Midtrans Payouts (atau mode simulasi); refund per order (diajukan mitra atau dibuat super admin, disetujui super admin; tiket langsung tidak berlaku); laporan penjualan & mutasi saldo (CSV); pemeriksaan integritas ledger. Belum ada: ganti rekening, refund parsial/massal, rekonsiliasi settlement Midtrans. Skema dana ditampung platform perlu konfirmasi Midtrans & konsultan hukum sebelum uang sungguhan.

Di luar MVP: aplikasi native, live tracking, hasil race/timing chip, marketplace merchandise.

## Model bisnis

- Fee platform per tiket (default 3%, disimpan per mitra di `organizations.fee_bps`), saat ini ditanggung mitra.
- Biaya payment gateway mengikuti metode bayar (mis. VA Rp4.000 + PPN 11% = Rp4.440). Saat refund, biaya PG yang tidak kembali ditanggung platform (`beban_refund_pg`); pembeli menerima total penuh.

Contoh tiket 21K Rp350.000 via VA: biaya PG Rp4.440, fee platform Rp10.500, hak mitra **Rp335.060** (diuji di `backend/internal/ledger/ledger_test.go`).

## KPI target

| Metrik | Target |
| --- | --- |
| Oversell | 0 tiket |
| API reservasi | p95 < 300 ms saat puncak |
| Ketersediaan saat jendela penjualan | 99,9% |
| Konversi reservasi → lunas | > 70% |
| Selisih rekonsiliasi harian | Rp0 |
