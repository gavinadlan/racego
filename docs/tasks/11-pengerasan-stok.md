# Tugas 11 · Pengerasan stok (pelindung di database) + perapian

> Prompt untuk AI coding agent (Claude Code CLI). Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/11-pengerasan-stok.md sampai semua kriteria selesai terpenuhi"`
> Prasyarat: tugas 01–10 sudah ter-commit. Bila butuh migrasi: pakai nomor bebas berikutnya di `backend/migrations/` (cek folder).

## Konteks

RaceGo: backend Go (`backend/`), frontend Next.js 16 (`frontend/`), Postgres, Redis. Baca dulu `README.md`, `docs/03-war-tiket.md`, `docs/04-pembayaran-dan-ledger.md`, `docs/11-performa.md` (hasil uji beban dan chaos Tugas 09), `docs/08-development.md`, dan **`frontend/AGENTS.md`**. **Baca kodenya langsung**: ringkasan di bawah dari ingatan dan bisa meleset, kode yang menang (catat bedanya di laporan).

Latar belakang. Stok tiket dijaga di **Redis** (Lua atomik: `reserve.lua`, `confirm.lua`, `release.lua`, `adjust.lua`, `clamp.lua`; `internal/checkout/inventory.go`, `sync.go`, `reconcile.go`). Saat order lunas, worker (`internal/worker/worker.go`, `onOrderPaid`) memanggil `Confirm` di Redis, dan **hanya bila Redis mengizinkan** menerbitkan tiket (`ticket.IssueForOrder`, yang saat ini mengalokasikan BIB dengan `UPDATE ticket_categories SET bib_next = bib_next + 1` tetapi **tidak memeriksa kuota di database**). Bila Redis menjawab `ConfirmNeedsRefund`, order menjadi `needs_refund`.

Uji chaos Tugas 09 menemukan bahwa setelah Redis di-`SIGKILL`, stok di Redis bisa kembali **lebih longgar dari kuota** (255 vs 250 pada uji). Itu diperbaiki dengan `StockReconciler` + `clamp.lua` (worker, tiap 15 detik, bertindak setelah dua putaran konsisten). Celah yang **dicatat jujur di laporan** dan tugas ini tutup:

1. **Selama jendela sebelum rekonsiliasi (≈15–30 detik)**, Redis yang longgar bisa mengizinkan lebih banyak tiket daripada kuota, dan **tidak ada pelindung kedua di database**: kuota hanya ditegakkan Redis.
2. **Kunci batas per pembeli** (`{cat:<id>}:user:<uid>`) yang hilang akibat crash belum direkonsiliasi dari database.
3. Pengaturan persistensi Redis (`appendonly yes`, `appendfsync` bawaan) belum pernah dievaluasi terhadap seberapa banyak data yang bisa hilang.

Prinsip yang dipakai: **database adalah sumber kebenaran jumlah tiket yang sah; Redis hanya jalur cepat.** Kalau keduanya berbeda, database menang dan pembeli yang terlambat diselamatkan lewat jalur `needs_refund` yang sudah ada (refund, Tugas 06), bukan lewat tiket berlebih.

Selain itu ada beberapa perapian kecil yang tercatat di laporan tugas sebelumnya.

## Tujuan

- **Bagian A:** tidak ada tiket melebihi kuota kategori **apa pun keadaan Redis** (kosong, longgar, kehilangan data, di-restart saat beban).
- **Bagian B:** kunci batas per pembeli dapat dipulihkan dari database; evaluasi persistensi Redis berdasarkan pengukuran.
- **Bagian C:** perapian kecil.

## Bagian A · Pelindung kuota di database

### Keputusan desain

1. **Pagar di titik penerbitan tiket**, di dalam transaksi yang sama dengan penerbitan: `ticket.IssueForOrder` (atau fungsi pembungkusnya; pilih yang membuat **semua** pemanggil terlindungi) mengunci baris kategori (`SELECT … FOR UPDATE` pada `ticket_categories`, atau memanfaatkan `UPDATE … RETURNING` yang sudah ada dengan syarat `WHERE bib_next - 1 + $n <= quota`, mana yang paling sederhana dan benar), menghitung tiket yang sudah **terbit** untuk kategori itu, dan **menolak** penerbitan bila `terbit + jumlah peserta order > quota`. Penolakan mengembalikan galat khusus (mis. `ErrQuotaExceeded`), **bukan** panik atau tiket parsial: order itu **tidak boleh menerbitkan sebagian**.
2. **Definisi "terbit":** semua tiket kategori itu **apa pun statusnya** (`issued|collected|cancelled|refunded`), karena stok yang terjual tidak dikembalikan ke penjualan (keputusan Tugas 06; kecuali kode saat ini mengembalikannya, bila begitu ikuti kode dan catat). Pastikan perhitungan memakai `quota` **saat ini** di database (mitra bisa mengubah kuota; perilaku penyesuaian kuota Tugas 02 tidak boleh berubah) dan memakai indeks yang ada atau indeks baru bila perlu (ukur; jangan memperlambat jalur panas: bandingkan p95 `war` sebelum/sesudah dengan skenario k6 dari Tugas 09).
3. **Penanganan saat pagar menolak** (di `onOrderPaid`): order menjadi `needs_refund` persis seperti jalur `ConfirmNeedsRefund` yang ada (tanpa menerbitkan tiket, tanpa jurnal `order.paid`, log peringatan dengan order ID, **tanpa data pribadi**), dan **kompensasi di Redis** supaya hitungan Redis tidak menyimpang: hold/penjualan yang sudah dikonfirmasi untuk order itu dikembalikan/dibatalkan dengan cara yang paling sederhana dan benar (periksa urutan `Confirm` lalu `IssueForOrder` di `onOrderPaid`; bila pagar bisa dijalankan **sebelum** `Confirm` Redis tanpa mengubah semantik lain, itu pilihan yang lebih bersih; jelaskan keputusan). Operasi ini harus idempoten (pesan outbox yang diproses ulang tidak menggandakan kompensasi dan tidak menggandakan `needs_refund`).
4. **Konkurensi:** banyak order lunas bersamaan pada kategori yang hampir penuh tidak boleh menerbitkan lebih dari sisa kuota; BIB tetap berurutan tanpa celah yang disebabkan penolakan (penolakan terjadi **sebelum** BIB dialokasikan, atau alokasi dibatalkan bersama transaksi; buktikan lewat tes bahwa tidak ada BIB ganda maupun lompatan karena penolakan).
5. **Pemeriksaan konsistensi untuk operator:** perluas `cmd/migrate check`/`loadgen-check` yang ada (atau tambah perintah `stock-check` yang paling cocok dengan kode) untuk membandingkan, per kategori, `kuota` vs `tiket terbit` vs stok Redis (tersisa/terjual/hold); keluaran hanya angka dan ID kategori. Tambahkan metrik/gauge tanpa label berisiko (mis. `racego_stock_guard_rejections_total`, `racego_stock_reconciler_clamps_total` bila belum ada) agar penolakan pagar terlihat di dashboard.
6. **Pembeli yang terdampak** mendapat jalur yang sudah ada: order `needs_refund` → refund (Tugas 06) dan email yang sudah ada bila ada. Tidak ada perubahan alur uang selain memastikan `needs_refund` dari pagar ini ditangani sama seperti dari `ConfirmNeedsRefund` (periksa bahwa refund `needs_refund` berfungsi untuk kasus ini: ledger, tanpa tiket).

### Tes (wajib; tes Postgres/Redis tidak di-skip)

- Pagar: kuota 5, lima order terbit; order keenam (lunas) → `needs_refund`, **tidak ada tiket**, tidak ada jurnal `order.paid`; order multi-peserta yang hanya muat sebagian ditolak **seluruhnya** (tidak parsial).
- **Simulasi Redis rusak:** pakai `Inventory` palsu/Redis yang dikosongkan (`FLUSHALL`) atau dibuat longgar sehingga `Confirm` selalu mengizinkan; kuota 3, 12 order lunas paralel → tepat 3 tiket (atau sesuai kuota), sisanya `needs_refund`; tanpa BIB ganda/lompat; total tiket ≤ kuota di semua eksekusi (ulang ≥ 20 kali untuk menangkap balapan).
- Idempotensi: pesan `order.paid` diproses dua kali (berurutan dan paralel) → satu penerbitan, satu `needs_refund`, kompensasi sekali.
- Kuota yang diubah mitra (naik/turun) tetap dihormati pagar; tes lama penyesuaian kuota hijau.
- Refund order `needs_refund` karena pagar berfungsi (ledger seimbang, order jadi `refunded`).
- Metrik penolakan bertambah dan tidak memuat label berisiko.
- Pemeriksaan konsistensi mendeteksi penyimpangan buatan (Redis dilonggarkan manual) dan melaporkan hijau pada keadaan normal.
- **Regresi performa:** skenario `war` 2.000 pengguna dari Tugas 09 dijalankan sebelum dan sesudah; laporkan p95 reservasi dan waktu penerbitan tiket; tidak boleh memburuk berarti (jelaskan bila ada).

## Bagian B · Kunci per pembeli dan persistensi Redis

1. **Rekonsiliasi kunci batas per pembeli** (`{cat:<id>}:user:<uid>`): periksa di kode bagaimana kunci ini dipakai (batas jumlah per pembeli di kategori). Tambahkan pemulihan dari database: bila kunci hilang (Redis restart/`SIGKILL`/kehilangan data), pembelian baru oleh pembeli yang **sudah punya order `paid`/`pending` aktif** pada kategori itu tidak boleh melewati batas. Cara yang disarankan: langkah rekonsiliasi di worker (pola `StockReconciler`) yang membangun ulang kunci dari `orders` (status `paid` dan `pending` yang masih berlaku) hanya **menambah** (tidak pernah mengurangi di bawah apa yang sah) dan idempoten; atau pemeriksaan di database pada saat checkout bila itu lebih sederhana dan tidak memperlambat jalur panas. Jelaskan pilihan dan alasannya. TTL kunci (Tugas 07) tidak boleh berubah perilakunya.
2. **Tes:** pembeli dengan order aktif lalu `FLUSHALL` Redis → rekonsiliasi memulihkan batas dan pembelian melebihi batas ditolak; pembeli tanpa order tidak terdampak; tidak ada kunci untuk pembeli yang order-nya sudah `expired`/`cancelled`.
3. **Evaluasi `appendfsync` Redis** (`docker-compose.prod.yml` dan, bila perlu, compose pengembangan): ukur, jangan menebak. Jalankan `war` 2.000 dengan `everysec` (bawaan) dan `always` pada mesin yang sama (dua kali masing-masing bila deraunya besar) dan laporkan p95 reservasi, throughput, dan data yang hilang pada uji `SIGKILL` Redis (berapa kenaikan stok yang hilang). **Pilih** nilai berdasarkan angka dan prinsip "database adalah sumber kebenaran, Redis boleh kehilangan ≤1 detik karena pagar dan rekonsiliasi sudah menutupnya", dokumentasikan alasannya; ubah bawaan produksi hanya bila ada alasan kuat dan perubahan itu terukur aman. Bila `appendfsync` dijadikan variabel, nilai tidak valid → gagal start dengan pesan jelas.

## Bagian C · Perapian

1. **`TestScanOnlineUnchanged` flaky:** tes merusak dua karakter terakhir token base64url; karakter terakhir kadang tidak mengubah byte hasil decode sehingga token yang "rusak" tetap sah. Ubah tes agar merusak token di bagian tengah (atau memodifikasi ID tiket); **jangan melemahkan asersinya**. Jalankan tes itu ≥ 30 kali untuk membuktikan stabil.
2. **`gofmt`:** `gofmt -l backend/` harus kosong (termasuk `offline_scan_test.go`); tambahkan pemeriksaan `gofmt -l` ke `make lint`/target tes (dan CI bila belum ada) sehingga berkas yang belum rapi menggagalkan lint.
3. **Header `X-Robots-Tag`** pada hasil undian publik: tambahkan asersi di tes Go (`TestDraw*`) untuk memastikan header `noindex` ada saat hasil publik hidup.
4. **Kosmetik scanner:** saat `indexedDB.open` gagal/diblokir, tombol "Siapkan mode offline" harus **dinonaktifkan** (konsisten dengan pesan "dimatikan"); scan online tetap jalan. Verifikasi di browser dengan IndexedDB diblokir.
5. **Dokumentasi log produksi:** tambahkan catatan di `docs/10-deploy.md` (jika belum ada) bahwa access log Caddy **tidak boleh dinyalakan tanpa menyaring `/undangan/`** (token undangan di path), dan bahwa `next dev` mencetak path itu hanya di mode pengembangan.

## Dokumen

Perbarui: `docs/03-war-tiket.md` (pelindung berlapis: Redis jalur cepat, database sumber kebenaran, rekonsiliasi; apa yang terlihat pembeli bila pagar menolak), `docs/04-pembayaran-dan-ledger.md` bila alur `needs_refund` berubah, `docs/11-performa.md` (tambahan: hasil Tugas 11, perbandingan sebelum/sesudah, hasil evaluasi `appendfsync`, celah yang sudah tertutup; perbarui bagian "celah tersisa" agar jujur), `docs/08-development.md` (cara menjalankan pemeriksaan konsistensi stok), `docs/09-roadmap.md`, `docs/10-deploy.md`, `README.md`, `.env.example`/`.env.prod.example` bila ada variabel baru, `make help` bila ada target baru.

## Di luar cakupan

Mengembalikan kuota ke penjualan setelah refund, mengganti Redis sebagai penjaga stok, sharding/replika, perubahan alur antrean, perubahan semantik reservasi dan harga, notifikasi baru, fitur baru. Jangan dikerjakan sekarang.

## Kriteria selesai (cek satu per satu dan laporkan dengan bukti nyata)

1. `make test` hijau (vet, tes Go dengan Postgres/Redis, lint termasuk `gofmt`, typegen, tsc), `cd frontend && npm run build` sukses, tes lama tidak diubah dan hijau (kecuali `TestScanOnlineUnchanged` yang diperbaiki rancangannya: jelaskan). Semua tes baru ada dan lulus (jumlah PASS, 0 SKIP).
2. **Bukti pagar di bawah beban nyata** (memakai skenario k6 dan skrip integritas dari Tugas 09, DB/Redis uji terpisah, lokal saja): `war` 2.000 pengguna dengan **Redis dikosongkan (`FLUSHALL`) di tengah beban**, lalu dengan **Redis di-`SIGKILL`** di tengah beban. Hasil: tiket terbit per kategori **≤ kuota** pada semua eksekusi, tanpa BIB ganda, ledger seimbang, order berlebih menjadi `needs_refund` (laporkan jumlahnya), tidak ada 5xx tak terduga kepada pembeli yang gagal. Tunjukkan keluaran pemeriksaan integritas sebelum (jika bisa direproduksi pada kode lama di cabang sementara) dan sesudah; bila tidak bisa direproduksi, jelaskan.
3. Regresi performa: p95 reservasi dan waktu penerbitan tiket sebelum/sesudah (angka nyata, ukuran yang sama dengan Tugas 09).
4. Rekonsiliasi kunci per pembeli terbukti (tes dan satu uji manual dengan `FLUSHALL`).
5. Evaluasi `appendfsync`: tabel angka nyata dan keputusan beralasan; perubahan konfigurasi (bila ada) terdokumentasi dan teruji.
6. Perapian C1–C5 selesai dan terbukti (hasil 30× tes yang tadinya flaky, `gofmt -l` kosong, tes header, tombol offline nonaktif saat IndexedDB gagal diverifikasi di browser, catatan Caddy di dokumen).
7. Konsol browser dan log API/worker bersih dari error/warning baru; log tidak memuat data pribadi; dokumen diperbarui; `git status` bersih dari berkas uji, `.env`, atau data; semua container/proses uji dimatikan; **data dev dan `.env`/`.env.prod` pribadi tidak tersentuh**.

## Cara kerja

- Urutan: baca kode alur `onOrderPaid`/`Inventory`/`reconcile` → pagar + tes unit/integrasi (A) → kompensasi Redis + tes konkurensi → metrik dan pemeriksaan konsistensi → rekonsiliasi per pembeli (B1) → uji beban/chaos nyata (kriteria 2–3) → evaluasi `appendfsync` (B3) → perapian (C) → dokumen. Jalankan `make test` di akhir tiap tahap.
- **Hemat jatah pemakaian**: mulai dari ukuran kecil, naikkan bertahap, jangan mengulang uji besar tanpa alasan; bila sesi terputus, tulis progres di `docs/tasks/11-progress.md` (kriteria tercentang + temuan + angka) dan hapus di akhir.
- Uang dan stok adalah area berisiko: utamakan kebenaran dan idempotensi di atas kecepatan. Jangan melemahkan tes yang ada. Jangan mengubah perilaku pembayaran, ledger, pencairan, KYC, scanner, login, antrean, atau undian selain yang disebut.
- Jangan menulis data pribadi, token, atau kunci ke log, metrik, laporan, atau screenshot.
- Bila keputusan di dokumen ini bertabrakan dengan kode atau kenyataan teknis, pilih opsi paling sederhana yang aman dan catat alasannya.
- Tanpa rahasia di repo, tanpa email/pembayaran sungguhan, tanpa tunnel ke internet, tanpa akun atau layanan berbayar, tanpa menyentuh `.env` pribadi.
- Di akhir: ringkasan perubahan per file, tabel hasil tiap kriteria (bukti nyata; tandai yang tidak bisa diuji), angka-angka utama, keputusan/penyimpangan, asumsi, dan usulan pesan commit. **Jangan commit dan jangan push.**
