# Tugas 08 · Undangan staf via email + scanner offline

> Prompt untuk AI coding agent (Claude Code CLI). Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/08-undangan-staf-scanner-offline.md sampai semua kriteria selesai terpenuhi"`
> Prasyarat: tugas 01–07 sudah ter-commit. Migrasi terakhir: `0007`. Migrasi baru di tugas ini: `0008`.

## Konteks

RaceGo: backend Go (`backend/`), frontend Next.js 16 (`frontend/`), Postgres, Redis. Baca dulu `README.md`, `docs/02-arsitektur.md`, `docs/05-tiket-qr-dan-scan.md` (format QR + scan), `docs/07-api.md`, `docs/08-development.md`, `docs/09-roadmap.md`, dan **`frontend/AGENTS.md`** (Next.js 16 berbeda dari versi lama; docs di `frontend/node_modules/next/dist/docs/`). **Baca kodenya langsung**: ringkasan di bawah dari ingatan dan bisa meleset; kode yang menang, catat bedanya di laporan.

Keadaan sekarang:

- **Tim mitra** (`/org/[org]/tim`, `internal/partner/members.go`): owner/admin menambah anggota dengan email; peran `owner|admin|finance|rpc_staff`. Menambah orang yang belum punya akun belum ada alur yang baik. Tidak ada undangan lewat email.
- **Scanner** (`frontend/src/components/scanner.tsx`, `ticket.Handler.Scan`, `POST /v1/org/{org}/events/{id}/scan`): QR `v1.<ticket_id>.<event_id>.<signature Ed25519>`, kunci publik di `GET /v1/qr/public-key`, kamera `@zxing/browser`, pencarian manual (nama, BIB, 4 digit akhir NIK), progres "diambil X dari Y", scan atomik (`UPDATE … WHERE status='issued'`), `scan_logs`. Hanya online. Lapangan RPC sering **tanpa sinyal stabil**, itulah alasan tugas ini.
- Tiket: `issued|collected|cancelled|refunded`; refund (Tugas 06) membuat tiket `refunded`. Peran: `rpc_staff` boleh scan; `finance` tidak.
- Fondasi yang dipakai: login cookie, `tenant.RequireRole`, `audit_logs`, outbox + worker + `notify` untuk email, `internal/ratelimit`, `internal/csvx` bila perlu, `ConfirmDialog` (perbaikan navigasi dialog di Tugas 07), format error `httpx.Error`/`Validation`.
- Teks pengguna Bahasa Indonesia, identifier Inggris. Aturan lint `react-hooks/set-state-in-effect` aktif; halaman yang memakai `useParams`/`useSearchParams` dibungkus `<Suspense>`.

## Tujuan

- **Bagian A: Undangan staf lewat email.** Owner/admin mengundang orang (akun belum ada pun bisa) dengan peran tertentu; penerima membuka tautan, masuk dengan OTP, dan bergabung.
- **Bagian B: Scanner offline.** Staf menyiapkan data event di perangkat saat online, lalu tetap bisa memindai tiket tanpa internet; hasilnya tersinkron aman saat online lagi, dengan penanganan bentrokan antar-perangkat.

## Bagian A · Undangan staf

### Keputusan desain

1. **Tabel `org_invitations`** (migrasi 0008): `id`, `org_id`, `email` (huruf kecil), `role` (CHECK `admin|finance|rpc_staff`; **`owner` tidak bisa diundang**), `token_hash` (SHA-256; token mentah hanya ada di email, **tidak pernah disimpan**), `invited_by`, `created_at`, `expires_at` (7 hari), `accepted_at`, `accepted_by`, `revoked_at`, `last_sent_at`, `send_count`. Indeks unik parsial: satu undangan **aktif** per `(org_id, email)`.
2. **Siapa boleh mengundang:** `owner` boleh mengundang `admin|finance|rpc_staff`; `admin` hanya `rpc_staff`; `finance`/`rpc_staff` tidak boleh. Non-anggota → 404. Mengubah peran atau mencabut anggota tetap mengikuti aturan yang sudah ada di `members.go` (jangan melemahkannya).
3. **Alur penerima:** email berisi tautan `/undangan/<token>` (token acak 32 byte, base64url). Halaman menampilkan nama org, peran, dan pengundang (tanpa membocorkan data lain) setelah token valid. Belum login → diarahkan ke login OTP lalu kembali ke tautan. **Email akun yang login harus sama (tanpa membedakan huruf besar/kecil) dengan email undangan**; jika beda, tampilkan pesan jelas ("Undangan ini untuk <email tersamar>; masuk dengan email itu") dan **jangan** menerima. Menerima = satu transaksi: tandai `accepted_at`, buat `org_members`, audit. Sekali pakai; kedaluwarsa/dicabut/sudah dipakai → pesan jelas dan generik (tidak membedakan "tidak ada" vs "salah").
4. **Anti-penyalahgunaan:** pembatas laju undangan (per org dan per pengundang, mis. 20/jam), kirim ulang maksimal sekali per 5 menit per undangan dan 5 kali total, tidak boleh mengundang email yang sudah menjadi anggota (pesan jelas), pembatas laju percobaan token per IP (token acak 256-bit; tetap batasi), perbandingan hash konstan-waktu. Email tidak boleh memuat apa pun selain nama org, peran, pengundang, dan tautan. Verifikasi Turnstile (Tugas 07) **tidak** ditambah di sini.
5. **Daftar & pengelolaan di `/org/[org]/tim`:** bagian "Undangan tertunda" (email, peran, dikirim kapan, kedaluwarsa, status), tombol Kirim ulang dan Cabut (dialog konfirmasi), formulir "Undang anggota" (email + peran sesuai hak pengundang). Alur lama "tambah anggota berdasarkan email" untuk akun yang sudah ada: **ganti menjadi undangan** supaya konsisten (jangan ada dua jalur dengan aturan berbeda). Anggota yang sudah ada tidak terdampak.
6. **Audit:** `invite.created|resent|revoked|accepted|expired_attempt` tanpa token; log aplikasi tidak memuat token atau email lengkap pada kegagalan token.
7. **Pembersihan:** worker menandai undangan kedaluwarsa (atau status dihitung dari `expires_at` saja; pilih yang paling sederhana) dan menghapus token lama setelah 30 hari.
8. Dev: tautan undangan tersedia di Mailpit; **jangan** mencatatnya di log (beda dari OTP dev yang sudah ada; lihat Tugas 07).

### Tes backend (wajib)

Matriks peran (owner/admin/finance/rpc_staff/non-anggota/anonim) untuk membuat, mengirim ulang, mencabut, daftar; admin tidak bisa mengundang admin/finance; `owner` tidak bisa diundang; terima dengan email beda → ditolak tanpa mengubah apa pun; terima dua kali (paralel juga) → satu keanggotaan; kedaluwarsa, dicabut, token salah → respons identik; undangan aktif ganda untuk email sama ditolak; email yang sudah anggota ditolak; pembatas laju; token tidak tersimpan mentah (cek kolom dan outbox payload: payload outbox boleh memuat token karena itu isi email, tetapi harus dihapus/diredaksi setelah terkirim atau diberi TTL; jelaskan keputusan); audit benar; isolasi tenant (undangan org A tidak terlihat/terkelola owner org B).

## Bagian B · Scanner offline

### Keputusan desain

1. **Mode kerja.** Scanner punya dua keadaan: **Online** (perilaku sekarang, tidak berubah) dan **Offline-siap**. Tombol "Siapkan mode offline" mengunduh manifest event ke IndexedDB. Setelah siap, scanner **selalu memverifikasi lokal lebih dulu**; bila online, tetap mencoba mengirim ke server di latar belakang (lihat sinkron). Indikator jelas di layar: Online / Offline / Menyinkronkan / jumlah antrean belum terkirim / waktu manifest terakhir.
2. **Manifest** (`GET /v1/org/{org}/events/{event}/scan-manifest`, peran `owner|admin|rpc_staff`; `finance` ditolak; non-anggota 404): berisi metadata event, **kunci publik QR**, `generated_at`, `expires_at` (default 24 jam, `SCAN_MANIFEST_TTL_HOURS`), `version`, dan daftar tiket: `ticket_id`, `bib`, `name`, `category`, `jersey`, `status`. **Minimisasi data:** tanpa email, tanpa nomor identitas, tanpa telepon, tanpa kontak darurat, tanpa tanggal lahir, tanpa data pembayaran. Pencarian manual offline memakai nama dan BIB saja (4 digit akhir NIK tidak tersedia offline; jelaskan di UI). Respons di-gzip, di-stream (10.000 tiket tidak boleh memakan memori besar), mendukung pembaruan inkremental `?since=<version>` yang mengembalikan perubahan status (diambil oleh perangkat lain, refund, tiket baru) dan tiket baru. Pembatas laju per pengguna; audit `scan.manifest.download` (aktor, event, jumlah tiket; tanpa isi).
3. **Verifikasi QR offline.** Di klien: parse `v1.<ticket_id>.<event_id>.<sig>`, verifikasi tanda tangan Ed25519 dengan kunci publik dari manifest (WebCrypto `Ed25519` bila tersedia; jika peramban tidak mendukung, pakai pustaka kecil terverifikasi dan laporkan pilihan serta ukurannya; **jangan menulis kripto sendiri**), cocokkan `event_id`, lalu cari di manifest. Hasil identik dengan online: hijau (`ok`), kuning (`already_collected`, tampilkan kapan/oleh siapa bila diketahui, termasuk dari scan lokal sendiri), merah (`invalid`, `wrong_event`, `not_found`, `refunded`, `cancelled`). QR bertanda tangan valid tetapi **tidak ada di manifest** (tiket terbit setelah manifest diunduh) → status khusus "Tidak ada di daftar offline" bernada kuning, **tidak ditandai diambil**, dengan saran sambungkan internet atau gunakan pencarian; jangan mengarang kebenarannya.
4. **Antrean lokal.** Tiap tindakan "tandai diambil" offline menjadi entri di IndexedDB: `client_op_id` (UUID), `ticket_id`, `scanned_at` (jam perangkat), `device_id` (UUID acak per perangkat/instalasi, tersimpan lokal), `staff` (ID pengguna), `delegated_to` ("diwakilkan oleh", bila diisi), `state` (`pending|synced|conflict|rejected`). Tiket yang sudah ditandai lokal langsung dianggap `collected` di manifest lokal (mencegah dobel scan di perangkat yang sama). Antrean bertahan saat tab ditutup/browser dimuat ulang.
5. **Sinkron.** `POST /v1/org/{org}/events/{event}/scan/batch` (peran `owner|admin|rpc_staff`), batas 200 entri per permintaan, batas ukuran body, **idempoten per `client_op_id`** (kolom unik di `scan_logs`; pengiriman ulang mengembalikan hasil yang sama tanpa efek ganda). Server memverifikasi **ulang** tanda tangan QR/ID tiket (tidak percaya klien), memakai pembaruan atomik yang sama dengan scan online, dan mengembalikan per entri: `ok`, `already_collected` (siapa/kapan, **bentrokan**), `refunded|cancelled|not_found|invalid|wrong_event`. `collected_at` di server = waktu server saat diproses; `scanned_at` perangkat disimpan terpisah (`scanned_at_device`) di `scan_logs`, beserta `device_id` dan `source = 'offline_sync'`. Jam perangkat yang ngawur (selisih jauh dari server) tidak mempengaruhi keputusan; tampilkan peringatan bila selisih > 5 menit.
   - **Bentrokan:** dua perangkat offline menandai tiket yang sama → yang pertama diproses server menang; yang kedua mendapat `already_collected` dan masuk daftar "Perlu ditinjau" di scanner (nama, BIB, siapa yang lebih dulu menurut server). Staf bisa menutup entri setelah meninjau. **Tidak ada penggabungan otomatis yang menimpa**.
   - Sinkron otomatis saat event `online` terdeteksi dan tiap 30 detik bila ada antrean (dengan backoff); tombol "Sinkronkan sekarang"; kegagalan jaringan tidak menghapus antrean; entri ditolak permanen (mis. 403 karena peran dicabut) berhenti dicoba dan ditampilkan.
6. **Keamanan data di perangkat.** Manifest berisi nama + BIB peserta di IndexedDB: 
   - Hanya diunduh setelah konfirmasi eksplisit dengan peringatan ("Data peserta akan tersimpan di perangkat ini sampai ... Gunakan hanya di perangkat panitia").
   - **Dihapus** saat logout, saat `expires_at` terlewati (kecuali masih ada antrean belum tersinkron: antrean tetap dipertahankan sampai terkirim atau dibuang manual dengan konfirmasi; manifest tetap dibersihkan), saat berganti pengguna di perangkat yang sama, dan lewat tombol "Hapus data offline".
   - Setelah `expires_at`, scan offline berhenti dengan pesan jelas dan meminta unduh ulang saat online (data basi berisiko menerima tiket yang sudah di-refund).
   - Catat jujur di dokumen: IndexedDB tidak terenkripsi oleh aplikasi; perangkat yang hilang berisiko; mitigasi di atas hanya mengurangi risiko.
7. **Muat ulang tanpa internet.** Pilih cara paling sederhana yang andal: service worker minimal yang meng-cache cangkang halaman scanner + aset statis (cakupan terbatas pada rute scanner, tidak meng-cache respons API/data sensitif), **atau** bila tidak andal dengan Next 16 (cacheComponents/Turbopack/standalone), fallback: halaman tetap berfungsi selama tab tidak dimuat ulang, dengan peringatan jelas di UI ("Jangan muat ulang / tutup halaman saat offline") dan dokumentasi. Buktikan mana yang dipilih dan alasannya di laporan. Jika memakai service worker: versi cache bertanda build, pembersihan cache lama, tidak mengganggu halaman lain, dan bisa dimatikan dengan mudah (tombol/flag).
8. **Tidak mengubah perilaku scan online** selain mengisi kolom baru di `scan_logs`. Jangan menambah kerentanan baru pada endpoint scan yang ada.

### Migrasi 0008 (tambahan untuk Bagian B)

`scan_logs`: tambah `client_op_id` (unik, boleh null untuk scan online lama), `device_id`, `scanned_at_device`, `source` (`online|offline_sync`, default `online`). Pastikan data lama tetap valid. Sediakan `.down.sql`.

### Tes backend (wajib)

- Manifest: matriks peran/tenant; isi tidak memuat email/NIK/telepon/kontak darurat/tanggal lahir (periksa kunci JSON); pembaruan inkremental benar (diambil, refund, tiket baru); 10.000 tiket tersaji tanpa lonjakan memori yang tidak wajar (ukur dan laporkan); audit tercatat; kedaluwarsa disertakan.
- Batch: idempoten (kirim dua kali → satu efek, respons sama); paralel dua perangkat pada tiket yang sama → tepat satu `ok`, sisanya `already_collected`; tiket org lain → ditolak/404 tanpa bocor; QR dipalsukan/diubah satu karakter → `invalid`; event salah; refund; batas 200 entri dan ukuran body; peran `finance`/non-anggota ditolak; `scanned_at_device` dan `device_id` tersimpan; `collected_at` memakai waktu server; log scan konsisten dengan hasil online.
- Tes lama scan online tidak diubah dan hijau.

## Frontend

Gaya visual dan komponen mengikuti yang ada. Layar scanner dipakai di **HP, satu tangan, luar ruangan**: target sentuh besar, kontras tinggi, hasil hijau/kuning/merah besar dan tidak ambigu, getar/bunyi opsional bila sudah ada pola di kode, dan teks bahasa Indonesia yang tegas.

- **Tim:** formulir undangan, daftar undangan tertunda, kirim ulang/cabut (dialog), pesan error per kolom, nyaman di HP 390px.
- **Halaman `/undangan/[token]`:** keadaan loading, token valid (ringkasan + tombol Terima / login), email tidak cocok, token tidak valid/kedaluwarsa, sukses (tautan ke dashboard org). Aksesibel dan responsif.
- **Scanner:** panel status offline (indikator, usia manifest, jumlah antrean, tombol Siapkan/Perbarui/Sinkronkan/Hapus data offline), hasil scan memakai komponen yang sama dengan online, daftar "Perlu ditinjau", pencarian manual offline (nama/BIB) dengan keterangan batasannya, peringatan jam perangkat, peringatan manifest hampir/sudah kedaluwarsa. Progres "diambil X dari Y" memakai data lokal + antrean saat offline.
- `lib/api.ts` + modul penyimpanan IndexedDB terpisah dan terbungkus `try/catch` (IndexedDB bisa tidak tersedia/penuh/diblokir: tampilkan pesan jelas dan matikan mode offline, jangan crash). Tidak ada akses `window`/IndexedDB saat render server.

## Dokumen

Perbarui: `docs/05-tiket-qr-dan-scan.md` (alur offline, bentrokan, keamanan data di perangkat, batasan jujur), `docs/07-api.md` (undangan, manifest, batch), `docs/02-arsitektur.md`, `docs/01-prd.md`, `docs/08-development.md` (cara menguji offline di Chrome DevTools dan lewat Playwright), `docs/09-roadmap.md` (centang undangan staf dan scanner offline; sisa: QR dinamis hari-H, Row-Level Security, observabilitas, anti multi-akun, rekonsiliasi settlement, enkripsi data offline dengan PIN), `docs/10-deploy.md` bila ada env/Caddy/service worker yang berubah, `.env.example`, `.env.prod.example`, `README.md`, `frontend/README.md`.

## Di luar cakupan

QR dinamis, enkripsi data offline dengan PIN, sinkron dua arah real-time (WebSocket/SSE), peran/izin baru, undangan massal (CSV), undangan via WhatsApp/SMS, cetak badge, aplikasi native, pembatalan "tandai diambil" (undo), perubahan alur pembayaran/ledger/KYC. Jangan dikerjakan sekarang.

## Kriteria selesai (cek satu per satu dan laporkan dengan bukti nyata)

1. `make test` hijau (vet, tes Go dengan Postgres/Redis, lint, typegen, tsc), `cd frontend && npm run build` sukses, tes lama tidak diubah dan hijau. Semua tes baru ada dan lulus (laporkan jumlah PASS dan 0 SKIP).
2. **Undangan, uji browser sungguhan** (Playwright di folder sementara di luar repo; jangan mengubah `package.json`; OTP dan tautan dari Mailpit; **jangan menyentuh `.env` pribadi**; data fiktif): owner mengundang email baru sebagai `rpc_staff` → email terkirim (cek isi, tanpa data lain) → penerima membuka tautan belum login → login OTP → bergabung → muncul di Tim dan bisa membuka scanner; kasus negatif: email akun berbeda, tautan dipakai ulang, kedaluwarsa (majukan `expires_at` lewat SQL di DB uji), dicabut, admin mengundang admin (ditolak), email yang sudah anggota, batas laju kirim ulang. Screenshot laptop **dan** HP 390px, **dilihat sendiri**.
3. **Scanner offline, uji browser sungguhan** (Playwright dengan `context.setOffline(true)` dan, bila berhasil, perangkat kedua sebagai context terpisah): siapkan manifest → offline → pindai QR asli (hijau), pindai ulang (kuning), QR palsu/diubah (merah), QR event lain (merah), tiket refund sebelum manifest (merah), tiket terbit setelah manifest (kuning khusus, tidak ditandai); muat ulang saat offline sesuai pilihan keputusan 7 (berhasil, atau peringatan fallback tampil); antrean bertahan setelah muat ulang; online lagi → sinkron otomatis, antrean 0, jumlah "diambil" di server benar; **dua perangkat offline memindai tiket yang sama** → satu `ok`, satu bentrokan di "Perlu ditinjau"; manifest kedaluwarsa menghentikan scan offline; logout menghapus manifest; IndexedDB diblokir/gagal tidak membuat halaman crash. Gunakan QR dari tiket yang dihasilkan alur nyata (checkout mock), bukan data palsu.
4. **Kamera:** uji jalur decode kamera dengan sumber video palsu Chrome (`--use-fake-device-for-media-stream` dengan video berisi QR) bila memungkinkan, atau tandai tidak bisa diuji; pencarian manual offline diuji.
5. **Beban manifest:** event 10.000 tiket sintetis (dibersihkan setelahnya): ukuran unduhan (gzip), waktu, memori API, waktu muat ke IndexedDB dan waktu pencarian manual di klien; laporkan angkanya; scan lokal tetap responsif (< 100 ms per verifikasi).
6. Konsol browser dan log API/worker bersih dari error/warning baru; log tidak memuat token undangan, token QR, atau isi manifest; audit tercatat untuk undangan dan unduhan manifest.
7. Semua dokumen di atas diperbarui; `git status` bersih dari berkas uji, `.env`, atau data.

## Cara kerja

- Urutan: migrasi 0008 → Bagian A backend + tes → Bagian A frontend → manifest + batch + tes → modul IndexedDB + verifikasi QR klien + tes klien (mis. uji unit dengan tes yang sudah dipakai proyek, atau skrip Playwright) → UI scanner offline → muat ulang offline (keputusan 7) → dokumen → uji browser dan beban. Jalankan `make test` di akhir tiap tahap.
- Jangan mengubah perilaku checkout, pembayaran, ledger, pencairan, KYC, login, atau antrean. Bug di luar cakupan: catat di laporan akhir, jangan diperbaiki diam-diam kecuali menghalangi tugas ini.
- Jangan menulis token undangan, token QR, isi manifest, kode OTP, atau kunci ke log, audit, pesan error, screenshot, atau laporan.
- Bila keputusan di dokumen ini bertabrakan dengan kode atau kenyataan teknis, pilih opsi paling sederhana yang aman dan catat alasannya.
- Tanpa rahasia di repo, tanpa email sungguhan, tanpa tunnel ke internet, tanpa menyentuh `.env` pribadi.
- Di akhir: ringkasan perubahan per file, tabel hasil tiap kriteria (berdasarkan uji nyata; tandai yang tidak bisa diuji), keputusan/penyimpangan (terutama keputusan 7), asumsi yang belum terverifikasi, dan usulan pesan commit. **Jangan commit dan jangan push.**
