# Tugas 10 · Absen hari-H + undian doorprize (spin BIB)

> Prompt untuk AI coding agent (Claude Code CLI). Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/10-undian-doorprize.md sampai semua kriteria selesai terpenuhi"`
> Prasyarat: tugas 01–08 sudah ter-commit (Tugas 09 uji beban boleh sebelum atau sesudah; tidak saling bergantung). Migrasi baru: pakai **nomor bebas berikutnya** di `backend/migrations/` (cek folder; bisa `0009` atau `0010`).

## Konteks

RaceGo: backend Go (`backend/`), frontend Next.js 16 (`frontend/`), Postgres, Redis. Baca dulu `README.md`, `docs/02-arsitektur.md`, `docs/05-tiket-qr-dan-scan.md` (scanner + mode offline), `docs/07-api.md`, `docs/08-development.md`, `docs/09-roadmap.md`, dan **`frontend/AGENTS.md`** (Next.js 16 berbeda dari versi lama; docs di `frontend/node_modules/next/dist/docs/`). **Baca kodenya langsung**; ringkasan di bawah dari ingatan dan bisa meleset, kode yang menang (catat bedanya).

Keadaan sekarang:

- Tiket per peserta (`tickets`: BIB unik per kategori, status `issued|collected|cancelled|refunded`). Scanner (`frontend/src/components/scanner.tsx`, `ticket.Handler.Scan`, `POST …/scan`) saat ini menandai **pengambilan race pack** (`collected`, biasanya H-1 atau pagi hari). **Itu belum membuktikan peserta benar-benar ikut lari.** Scanner juga punya mode offline (manifest IndexedDB, antrean `client_op_id`, `POST …/scan/batch`, bentrokan "Perlu ditinjau", Tugas 08).
- Peran: `owner`, `admin`, `finance`, `rpc_staff`. Isolasi tenant lewat `tenant.RequireRole`; audit lewat `audit_logs`; CSV aman lewat `internal/csvx`; `ConfirmDialog` bersama (perbaikan navigasi dialog); export peserta (`internal/participant`, halaman Peserta).
- **Belum ada** absen hari-H, dan belum ada undian apa pun (di roadmap hanya "ballot/undian untuk event super-favorit", itu hal lain: undian tiket, bukan doorprize).
- Teks pengguna Bahasa Indonesia, identifier Inggris. Aturan lint `react-hooks/set-state-in-effect` aktif; halaman yang memakai `useParams`/`useSearchParams` dibungkus `<Suspense>`.

## Tujuan

- **Bagian A: Absen hari-H.** Staf memindai QR peserta **di lokasi lomba pada hari-H** untuk menandai peserta yang benar-benar hadir/ikut. Terpisah dari pengambilan race pack.
- **Bagian B: Undian doorprize.** Penyelenggara mengundi hadiah dengan layar "spin" nomor BIB, **hanya dari peserta yang sudah diabsen hari-H** (tiket yang dibeli tetapi peserta tidak datang tidak ikut), adil, tercatat, dan dapat diaudit.

## Bagian A · Absen hari-H

### Keputusan desain

1. **Kolom baru di `tickets`:** `attended_at timestamptz`, `attended_by uuid` (staf). Jangan menambah nilai ke enum `status` (absen ≠ status tiket; bisa `collected` atau tetap `issued` bila peserta tidak mengambil pack terpisah). Absen hanya sah untuk tiket `issued` atau `collected`; `refunded`/`cancelled` ditolak (merah). Absen tidak mengubah `status`/`collected_*`.
2. **Mode scanner baru "Absen hari-H"** di halaman scanner yang ada: pemilih mode **Ambil race pack | Absen hari-H** (mode terakhir diingat per event, dengan indikator besar yang jelas di layar supaya staf tidak salah mode). Hasil: hijau `ok` (tercatat hadir: nama, BIB, kategori), kuning `already_attended` (kapan & oleh siapa), merah (`invalid`, `wrong_event`, `not_found`, `refunded`, `cancelled`). Pencarian manual berdasarkan nama/BIB juga tersedia di mode ini (peserta yang HP-nya mati). Progres "hadir X dari Y".
3. **Server:** `POST /v1/org/{org}/events/{event}/attend` (`{token}` atau `{ticket_id}` untuk manual), peran `owner|admin|rpc_staff`; pembaruan atomik `UPDATE … SET attended_at=now(), attended_by=… WHERE id=… AND event_id=… AND org_id=… AND status IN ('issued','collected') AND attended_at IS NULL`; scan kedua → `already_attended`. Catat di `scan_logs` dengan kolom/jenis baru (mis. `kind`: `collect|attend`, default `collect` agar data lama valid) dan di audit untuk pembatalan (butir 5).
4. **Waktu yang masuk akal:** absen hanya boleh pada tanggal lomba (`events.race_date`, zona WIB; cek nama kolom sebenarnya) sampai akhir hari berikutnya, supaya tidak tercatat jauh sebelum hari-H; di luar itu tolak dengan pesan jelas (`attend_not_open`). Batas ini harus bisa diubah penyelenggara untuk event multi-hari: pengaturan event sederhana (mis. `attendance_opens_at`/`attendance_closes_at` opsional; kosong = aturan bawaan). Di development, pengaturan ini memudahkan uji.
5. **Pembatalan absen** (salah scan): `owner|admin` saja, wajib alasan 5–300 karakter, tercatat di audit, **ditolak bila tiket itu sudah menjadi pemenang undian aktif** (pesan jelas). `rpc_staff` tidak bisa membatalkan.
6. **Mode offline harus ikut:** hari-H sering tanpa sinyal. Perluas Tugas 08: manifest memuat `attended` per tiket (dan `attended_at` bila ada); entri antrean lokal punya `kind` (`collect|attend`); `POST …/scan/batch` menerima `kind` per entri, tetap idempoten per `client_op_id`, bentrokan antar-perangkat (`already_attended`) masuk "Perlu ditinjau" dengan cara yang sama; pembaruan inkremental `?since=` menyertakan perubahan absen. Verifikasi QR offline memakai kode yang sama. **Tanpa mengubah perilaku mode ambil race pack.** Perbarui kebijakan penghapusan/kedaluwarsa data offline bila perlu (tidak melemahkannya).
7. **Tampilan data:** halaman Peserta memperlihatkan kolom/ringkasan "Hadir hari-H" dan filter (Hadir/Belum hadir); CSV export peserta menambah kolom "Hadir hari-H (waktu)". Manifest tetap tanpa email/NIK/telepon/kontak darurat/tanggal lahir.

### Tes backend (wajib)

Absen: matriks peran/tenant (event org lain → 404; `finance` ditolak); atomik dan idempoten (paralel dua scan tiket sama → satu `ok`); status `refunded|cancelled` ditolak; di luar jendela waktu ditolak; QR palsu/diubah/event salah → merah; pembatalan oleh `rpc_staff` ditolak, oleh owner/admin berhasil dengan audit, ditolak bila tiket pemenang aktif; `status`/`collected_*` tidak berubah oleh absen; mode ambil race pack tidak berubah (tes lama hijau); batch offline `kind=attend` idempoten, paralel dua perangkat → satu `ok` + satu bentrokan, manifest/`?since` memuat perubahan absen; CSV peserta memuat kolom baru dan tetap aman dari injeksi rumus.

## Bagian B · Undian doorprize

### Keputusan desain

1. **Peserta layak** (kolam undian): tiket event itu yang **`attended_at IS NOT NULL`** dan `status IN ('issued','collected')` (bukan refund/batal). **Yang membeli tetapi tidak hadir tidak ikut.** Filter opsional per hadiah: kategori tertentu (mis. hanya 21K). Pengaturan event `draw_eligibility`: `attended` (**bawaan**) atau `collected` (untuk penyelenggara yang tidak memakai absen hari-H; peringatkan di UI bahwa ini tidak membuktikan peserta lari). Setiap tiket **hanya boleh menang sekali per event** secara bawaan (`allow_multiple_wins=false`, dapat diubah owner/admin sebelum undian pertama dimulai).
2. **Tabel** (migrasi): `draw_prizes` (`id`, `event_id`, `org_id`, `name` 2–100 karakter, `quantity` 1–1.000, `category_id` opsional, `position` urutan, `created_at`), `draw_winners` (`id`, `prize_id`, `ticket_id`, `seq` (urutan di hadiah), `status` `winner|claimed|forfeited`, `drawn_at`, `drawn_by`, `pool_size`, `pool_hash`, `note`), unik `(prize_id, ticket_id)`; bila `allow_multiple_wins=false`, unik per `(event_id, ticket_id)` untuk status `winner|claimed` (indeks parsial/penegakan di transaksi). **Append-only:** pemenang tidak pernah dihapus; "tidak hadir di panggung" = ubah status `forfeited` (dengan alasan) lalu undi pengganti sebagai baris baru. Pengaturan event baru: `draw_eligibility`, `allow_multiple_wins`, `draw_public` (hasil publik, bawaan mati).
3. **Pengacakan adil, di server, dapat diaudit:** pemenang dipilih di server dengan `crypto/rand` (pemilihan seragam tanpa bias modulo, mis. `rand.Int` dengan batas) dari **snapshot kolam saat itu**; hasil ditentukan sebelum animasi, layar hanya menganimasikan hasil. Setiap pengundian menyimpan `pool_size` dan `pool_hash` (SHA-256 dari daftar `ticket_id` kolam yang diurutkan) agar bisa diaudit ulang (cukup mereproduksi hash dari data absen saat itu, dan memverifikasi bahwa pemenang ada di kolam). **Tidak ada jalur untuk memilih pemenang secara manual**, tidak ada parameter "seed" dari klien, dan tidak ada cara menaruh peserta di urutan tertentu. Sumber acak dapat diganti di tes (interface kecil), tidak lewat konfigurasi produksi.
4. **Konkurensi dan batas:** satu undian per hadiah berjalan sekaligus (kunci transaksi/`SELECT … FOR UPDATE` pada hadiah atau advisory lock); jumlah pemenang aktif tidak boleh melebihi `quantity` meski dua admin menekan Undi bersamaan; tombol/endpoint "Undi berikutnya" mengundi **satu pemenang per permintaan** (mendukung pengundian satu per satu di panggung) dan ada opsi "Undi semua sisa" untuk hadiah banyak; kolam habis → pesan jelas (`pool_empty`), bukan galat.
5. **Waktu:** undian baru bisa dijalankan bila ada peserta hadir; untuk mencegah undian prematur, hadiah baru bisa diundi mulai tanggal lomba (aturan sama seperti jendela absen) dan hanya oleh `owner|admin`. `rpc_staff` hanya membaca hasil; `finance` tidak punya akses. Non-anggota 404.
6. **Privasi:** layar panggung/hasil publik menampilkan **BIB + nama depan (atau inisial) + kategori**; tidak ada email, telepon, NIK, tanggal lahir, atau kontak darurat. Panel panitia boleh menampilkan nama lengkap saja. Hasil publik (`draw_public`) per event di `/events/<slug>/hasil-undian` (atau path sepadan) hanya bila diaktifkan; tidak mengindeks data pribadi, dengan `noindex` dan cache pendek.
7. **Status pemenang:** `winner` → `claimed` (hadiah diterima, dicatat panitia) atau `forfeited` (tidak hadir di panggung/ditolak). Perubahan status oleh `owner|admin`, alasan untuk `forfeited`, semuanya tercatat. Pengganti hanya lewat undian ulang yang sah.
8. **Audit:** `draw.prize.created|updated|deleted` (hanya sebelum ada pemenang), `draw.drawn` (hadiah, BIB, `pool_size`, `pool_hash`; tanpa data pribadi), `draw.claimed`, `draw.forfeited`, `draw.settings.changed`, `draw.results.downloaded`. Hadiah yang sudah punya pemenang tidak bisa dihapus/dikurangi di bawah jumlah pemenang aktif.
9. **Ekspor:** hasil undian CSV (hadiah, urutan, BIB, nama, kategori, status, waktu, `pool_size`), memakai penulis CSV aman, hanya `owner|admin`, tercatat di audit.

### Endpoint (mitra; peran sesuai di atas; non-anggota 404)

| Method | Path | Peran | Keterangan |
| --- | --- | --- | --- |
| GET/PUT | `/v1/org/{org}/events/{event}/draw/settings` | owner, admin (GET: + rpc_staff) | `draw_eligibility`, `allow_multiple_wins`, `draw_public`; perubahan eligibilitas/`allow_multiple_wins` dikunci setelah pemenang pertama |
| GET/POST | `…/draw/prizes` · PUT/DELETE `…/draw/prizes/{id}` | owner, admin (GET: + rpc_staff) | kelola hadiah, validasi per kolom (422 `fields`) |
| GET | `…/draw/pool` | owner, admin | ukuran kolam layak sekarang (total, per kategori), tanpa membuka daftar pribadi |
| POST | `…/draw/prizes/{id}/draw` | owner, admin | `{count}` (1 bawaan; maks sisa kuota); mengembalikan pemenang yang baru diundi |
| GET | `…/draw/winners` | owner, admin, rpc_staff | semua pemenang + status |
| POST | `…/draw/winners/{id}/claim`, `/forfeit` | owner, admin | `forfeit` wajib alasan |
| GET | `…/draw/results.csv` | owner, admin | ekspor |
| GET | `/v1/events/{slug}/draw-results` | publik | hanya bila `draw_public`; BIB + nama depan + kategori + hadiah |

Kode error baru memakai format `httpx.Error`: `draw_not_open`, `pool_empty`, `prize_exhausted`, `settings_locked`, `attend_not_open`, `already_attended`, `attendance_in_use`.

### Tes backend (wajib)

- **Kelayakan:** hanya tiket hadir yang masuk kolam; tiket `issued`/`collected` yang tidak diabsen **tidak** masuk (mode `attended`); `refunded`/`cancelled` tidak masuk; mode `collected` bekerja; filter kategori; pemenang tidak masuk kolam lagi bila `allow_multiple_wins=false`.
- **Keadilan statistik:** dengan sumber acak asli, undi berulang (mis. 20.000 pengundian pada kolam 10 peserta dengan pengembalian untuk uji) → tiap peserta mendekati 10% dalam toleransi lebar yang tetap lulus stabil (jangan membuat tes flaky: gunakan batas longgar dan seed yang tidak dipakai produksi, atau uji penggunaan `rand.Int` secara unit pada batas rentang). Tes bahwa pemilihan tidak pernah di luar kolam dan tidak bias ke indeks awal/akhir.
- **Konkurensi:** dua admin menekan Undi bersamaan pada hadiah berkuota 1 → tepat satu pemenang; tiket yang sama tidak bisa menang dua hadiah paralel; `count` > sisa ditolak; kolam habis → `pool_empty`.
- **Audit/jejak:** `pool_hash` dapat direproduksi dari data; pemenang ada di kolam saat undian; baris pemenang tidak pernah dihapus; `forfeit` + undi pengganti menghasilkan baris baru; perubahan pengaturan terkunci setelah pemenang pertama.
- **Otorisasi:** matriks peran (`owner|admin` ya, `rpc_staff` baca saja, `finance` ditolak, non-anggota/org lain 404, anonim 401); endpoint publik hanya mengembalikan field aman dan 404 bila `draw_public` mati; tidak ada email/telepon/NIK di respons publik maupun log.
- **Pembatalan absen** pemenang aktif ditolak (`attendance_in_use`).
- Tes lama (scanner online, offline, peserta, refund) tetap hijau.

## Frontend

Gaya visual dan komponen mengikuti yang ada. Form: label selalu terlihat (`Field`), pesan error per kolom, tombol disabled saat memproses, dialog konfirmasi `ConfirmDialog` (tutup dialog sebelum navigasi), nyaman di HP 390px **dan** layar besar/proyektor.

- **Scanner:** pemilih mode (Ambil race pack | Absen hari-H) dengan penanda warna/teks besar mode aktif; hasil absen memakai komponen hasil yang sama; progres "hadir X dari Y"; mode offline mendukung kedua mode; "Perlu ditinjau" menangani bentrokan absen. Pembatalan absen (owner/admin) dari halaman Peserta atau panel hasil, dengan dialog + alasan.
- **Halaman Peserta:** kolom/lencana "Hadir", filter, ringkasan hadir per kategori, unduh CSV diperbarui.
- **Halaman Undian** (`/org/[org]/events/[event]/undian`, tab/tautan baru di event): pengaturan (kelayakan, satu-menang, hasil publik, dengan penjelasan satu kalimat tiap opsi), daftar hadiah (tambah/ubah/hapus; hapus terkunci bila sudah ada pemenang), ringkasan kolam ("N peserta hadir layak diundi" + per kategori), daftar pemenang dengan status dan aksi Diterima/Tidak hadir, unduh CSV, peringatan bila belum ada yang diabsen atau belum hari-H.
- **Layar panggung (mode presentasi):** tombol "Layar panggung" membuka tampilan penuh yang bersih: nama hadiah besar, **reel BIB** yang berputar lalu berhenti pada hasil dari server (durasi 4–6 detik, melambat natural), BIB sangat besar + nama depan + kategori, tombol **Undi**, **Undi lagi**, **Tidak hadir → undi pengganti**, dan daftar pemenang yang sudah keluar. Kontras tinggi, terbaca dari jauh, kontrol utama bisa dipakai keyboard dan layar sentuh; **hormati `prefers-reduced-motion`** (tanpa animasi putar; tampilkan hasil dengan transisi sederhana) dan jangan memuat suara otomatis. Layar tetap bekerja bila koneksi putus sesaat (tombol Undi menunjukkan galat jelas, tidak mengundi dua kali; permintaan bersifat idempoten lewat kunci per-klik yang dibuat klien).
- **Hasil publik:** halaman sederhana per event bila `draw_public`, `noindex`.
- `lib/api.ts` dengan tipe dan fungsi baru; teks Indonesia; tanpa mengakses `window` saat render server.

## Dokumen

Perbarui: `docs/05-tiket-qr-dan-scan.md` (mode absen hari-H, offline, bentrokan), `docs/07-api.md` (semua endpoint + kode error), `docs/02-arsitektur.md`, `docs/01-prd.md` (fitur undian), **`docs/12-undian.md` baru** (cara kerja kelayakan, pengacakan, audit `pool_hash`, privasi, batasan jujur: sumber acak server, bukan ritual publik yang dapat disaksikan secara kriptografis; tidak ada klaim legal; **catatan bahwa aturan hukum undian/doorprize bisa berlaku dan penyelenggara perlu memastikan sendiri; ini bukan nasihat hukum**), `docs/08-development.md` (cara menguji absen dan undian, termasuk mengatur `race_date`/jendela absen di dev), `docs/09-roadmap.md` (centang absen hari-H dan undian doorprize; catat sisa: undian tiket/ballot, ritual undian dengan komit-ungkap acak publik, undian dengan syarat finis/waktu tempuh, notifikasi pemenang), `README.md`, `frontend/README.md`.

## Di luar cakupan

Undian tiket/ballot untuk pembelian, pencatatan waktu tempuh/finis dan hasil lomba, notifikasi email/WhatsApp ke pemenang, undian lintas event, penggunaan RFID/chip, verifikasi jarak lari, sumber acak publik yang dapat diverifikasi pihak luar (drand/blockchain), peran baru, perubahan alur pembayaran/ledger/KYC. Jangan dikerjakan sekarang.

## Kriteria selesai (cek satu per satu dan laporkan dengan bukti nyata)

1. `make test` hijau (vet, tes Go dengan Postgres/Redis, lint, typegen, tsc), `cd frontend && npm run build` sukses, tes lama tidak diubah dan hijau. Semua tes baru ada dan lulus (jumlah PASS, 0 SKIP).
2. **Absen hari-H, uji browser sungguhan** (Playwright di folder sementara di luar repo; jangan mengubah `package.json`; OTP dari Mailpit; **jangan menyentuh `.env` pribadi**; data fiktif): beli tiket mock beberapa peserta (campuran: ambil pack saja, ambil pack + hadir, hadir tanpa ambil pack, tidak datang, refund) → mode ambil race pack tetap bekerja → mode absen: hijau, kuning (ulang), merah (palsu, event lain, refund) → progres hadir benar → di luar jendela waktu ditolak → pembatalan absen (owner ya, rpc_staff tidak) → **offline**: manifest, absen offline, antrean bertahan, sinkron, dua perangkat memindai tiket sama (satu `ok`, satu "Perlu ditinjau") → CSV peserta berkolom hadir.
3. **Undian, uji browser sungguhan:** atur hadiah (beberapa dengan kuota >1 dan satu dengan filter kategori) → ringkasan kolam hanya menghitung yang hadir → peserta yang membeli tetapi tidak hadir **tidak pernah muncul** dalam puluhan pengundian → layar panggung: reel berhenti pada hasil server, Undi lagi, "Tidak hadir → pengganti", kuota habis, kolam habis → pemenang tidak menang dua kali → hasil CSV → hasil publik hidup/mati (404 saat mati) tanpa data pribadi → `prefers-reduced-motion` (emulasi di Playwright) tanpa animasi putar → kasus negatif: `rpc_staff` hanya baca, `finance` ditolak, org lain 404, dua admin menekan Undi bersamaan (dua konteks) menghasilkan jumlah pemenang sesuai kuota.
4. **Bukti pengacakan:** jalankan sampel besar pada kolam kecil dan tunjukkan distribusi hampir seragam (angka nyata) dan bahwa `pool_hash` dapat direproduksi dari data DB (skrip kecil yang membuktikan); tunjukkan tidak ada endpoint/parameter untuk memilih pemenang manual.
5. Screenshot layar panggung di 1920×1080 dan HP 390px, scanner dua mode, halaman Undian dan Peserta di laptop dan HP: **lihat sendiri** dan perbaiki yang rusak; tanpa geser horizontal.
6. Konsol browser dan log API/worker bersih dari error/warning baru; log tidak memuat token QR, data pribadi, atau isi manifest; audit tercatat untuk absen, pembatalan absen, undian, status pemenang, pengaturan, dan unduhan.
7. Semua dokumen di atas diperbarui; `make help` bila ada target baru; `git status` bersih dari berkas uji, `.env`, atau data. Data uji dihapus dan proses yang dinyalakan dimatikan.

## Cara kerja

- Urutan: migrasi + pengaturan → Bagian A backend + tes (termasuk batch/manifest offline) → Bagian A frontend (mode scanner, offline, Peserta) → Bagian B backend + tes → Bagian B frontend (pengaturan, hadiah, layar panggung, hasil publik) → dokumen → uji browser. Jalankan `make test` di akhir tiap tahap. **Hemat jatah pemakaian**: jangan mengulang uji besar tanpa alasan; bila sesi terputus tulis progres di `docs/tasks/10-progress.md` (kriteria tercentang + temuan) dan hapus di akhir.
- Jangan mengubah perilaku checkout, pembayaran, ledger, pencairan, KYC, login, antrean, atau scan ambil race pack. Bug di luar cakupan: catat saja.
- Jangan menulis data pribadi, token QR, atau kunci ke log, audit, pesan error, screenshot, atau laporan.
- Bila keputusan di dokumen ini bertabrakan dengan kode atau kenyataan teknis, pilih opsi paling sederhana yang aman dan catat alasannya.
- Tanpa rahasia di repo, tanpa email sungguhan, tanpa tunnel ke internet, tanpa menyentuh `.env` pribadi.
- Di akhir: ringkasan perubahan per file, tabel hasil tiap kriteria (bukti nyata; tandai yang tidak bisa diuji), keputusan/penyimpangan, asumsi yang belum terverifikasi, dan usulan pesan commit. **Jangan commit dan jangan push.**
