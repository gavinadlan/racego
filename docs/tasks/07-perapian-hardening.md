# Tugas 07 · Perapian, hardening, export peserta, dan anti-bot

> Prompt untuk AI coding agent (Claude Code CLI). Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/07-perapian-hardening.md sampai semua kriteria selesai terpenuhi"`
> Prasyarat: tugas 01–06 sudah ter-commit. Migrasi terakhir: `0007`. Bila butuh migrasi baru, pakai `0008`.

## Konteks

RaceGo: backend Go (`backend/`), frontend Next.js 16 (`frontend/`), Postgres, Redis. Baca dulu `README.md`, `docs/02-arsitektur.md`, `docs/07-api.md`, `docs/08-development.md`, `docs/09-roadmap.md`, dan **`frontend/AGENTS.md`** (Next.js 16 berbeda dari versi lama; docs di `frontend/node_modules/next/dist/docs/`). **Baca kodenya langsung**: ringkasan di bawah ditulis dari ingatan dan bisa meleset; kode yang menang, catat bedanya di laporan.

Tugas 01–06 sudah jadi dan lulus uji. Tugas ini mengumpulkan sisa temuan dari laporan-laporan tugas sebelumnya (perbaikan kecil, bukan fitur besar) ditambah dua fitur kecil: export peserta untuk mitra dan perlindungan anti-bot.

## Tujuan

Enam kelompok pekerjaan yang saling bebas (kerjakan berurutan, tiap kelompok diuji sebelum lanjut):

- **A.** Perbaikan CSV laporan keuangan (angka negatif).
- **B.** Hardening log dan kode sisa development.
- **C.** Audit pola dialog modal.
- **D.** Naik versi Go dan dependensi.
- **E.** Export data peserta (CSV) untuk mitra.
- **F.** Anti-bot Cloudflare Turnstile (opsional dinyalakan lewat env).

## A · CSV laporan keuangan

Di Tugas 06 semua sel CSV yang diawali `-` diberi awalan `'`, termasuk angka negatif sungguhan, sehingga Excel membacanya sebagai teks.

- Pisahkan **sel angka** dari **sel teks** di penulis CSV (`internal/finance/report.go` atau tempat yang sebenarnya): kolom bertipe angka (int64 rupiah, jumlah, saldo) ditulis apa adanya, termasuk negatif, tanpa awalan. Hanya sel **teks** yang disanitasi (diawali `=`, `+`, `-`, `@`, tab, atau CR → awalan `'`).
- Penulis CSV tidak boleh menerima "string yang kebetulan berupa angka" dari input pengguna sebagai sel angka: jenis sel ditentukan oleh kode (tipe Go), bukan oleh isinya.
- Tes: angka negatif ditulis utuh (`-150000`); teks `=cmd|' /C calc'!A0`, `+1+1`, `-2+3`, `@SUM(1)`, `\t=1`, `\r=1` ter-netralkan; teks biasa tidak berubah; BOM UTF-8 tetap ada; kutip ganda dan koma dalam teks di-escape benar.
- Bukti nyata: buka CSV hasil unduhan dengan skrip yang mem-parse kolom angka sebagai angka (mis. Python `csv`) dan jumlahkan; hasilnya sama dengan layar.

## B · Hardening log dan sisa development

1. **Kode OTP di log.** API mencatat kode OTP ("kode OTP (dev)") di log. Pastikan baris itu **hanya** aktif bila `APP_ENV=development`, dan **tidak pernah** di produksi (tulis tes yang menjalankan jalur kirim OTP dengan `APP_ENV=production` dan menangkap seluruh keluaran log: kode tidak boleh muncul). Periksa juga sumber lain yang bisa membocorkan kode/token: pesan error, audit, respons API, outbox payload di log.
2. **Cari kebocoran lain di log secara sistematis**: `grep` seluruh pemanggilan log (`log.`, `slog.`, `fmt.Print*`) di `backend/` untuk token sesi, `X-Queue-Pass`, kunci (`*_KEY`, `*_SECRET`, `SMTP_PASSWORD`), nomor rekening, NIK, kode OTP, isi email. Laporkan hasil pemeriksaan (daftar yang dicek dan yang diperbaiki). Perbaiki apa pun yang membocorkan.
3. **`make db-reset` setelah trigger ledger (Tugas 06).** Buktikan `make db-reset && make migrate && make seed` masih berjalan dari DB yang berisi data ledger (trigger menolak UPDATE/DELETE/TRUNCATE). Jika tidak, perbaiki dengan cara yang **tidak** melemahkan trigger di produksi (mis. reset = drop schema/recreate database di development saja, dan menolak jalan bila `APP_ENV` bukan development).
4. **`session_replication_role=replica`** hanya boleh ada di kode tes dan perintah reset development; buktikan lewat `grep` bahwa tidak ada di kode aplikasi produksi.
5. **Dedupe audit pratinjau PDF.** Penampil PDF Chrome meminta berkas dua kali sehingga `kyc.document.view` tercatat dua kali. Gabungkan pencatatan untuk (aktor, dokumen) yang sama dalam jendela 10 detik menjadi satu baris (tes: dua permintaan berurutan → satu baris audit; permintaan 11 detik kemudian atau oleh aktor lain → baris baru). Jangan mengurangi catatan akses oleh aktor berbeda.
6. **TTL kunci Redis `{cat:<id>}:user:<uid>`** (catatan lama: tanpa TTL). Beri TTL yang masuk akal (mis. sampai 7 hari setelah hari lomba/penutupan kategori, atau TTL tetap panjang yang dapat diatur) tanpa mengubah perilaku pembatasan "satu pembeli per kategori"; tes menguji kunci memiliki TTL dan pembatas tetap bekerja selama TTL.

## C · Audit pola dialog modal

Di Tugas 05 ditemukan bug: tautan di dalam `<dialog>` modal membuat halaman tujuan tidak bisa diklik.

- Daftar **semua** pemakaian `<dialog>`/modal di `frontend/src` (konfirmasi terbitkan, hapus event, anggota tim, tolak/setujui admin, pencairan, refund, dst.). Untuk tiap dialog yang berisi `Link`/`router.push`/navigasi, pastikan dialog ditutup (`close()` dan state) **sebelum** navigasi. Lebih baik satu helper bersama daripada perbaikan tersebar (mis. komponen `Modal` di `ui-client.tsx` dengan `onNavigate`).
- Pastikan fokus kembali ke pemicu setelah ditutup, `Esc` menutup, dialog tidak tertinggal terbuka setelah navigasi/back, dan latar tidak bisa di-scroll saat terbuka.
- Bukti: skrip Playwright yang membuka setiap dialog yang punya tautan/navigasi, mengklik tautannya, dan memastikan halaman tujuan dapat diklik (klik tombol/tautan di halaman tujuan berhasil). Laporkan jumlah dialog yang diperiksa dan yang diperbaiki.

## D · Versi Go dan dependensi

- Naikkan Go ke versi stabil **yang masih didukung** (cek `go.dev/doc/devel/release`; jika jaringan tidak bisa, pakai rilis stabil terbaru yang tersedia di mesin dan laporkan nomornya): `backend/go.mod` (`go` dan `toolchain`), `Dockerfile`, workflow CI, `docs/08-development.md`, `docs/10-deploy.md`.
- `go mod tidy`; jalankan `go vet`, seluruh tes, dan `govulncheck ./...` bila bisa dipasang (laporkan temuan; perbaiki yang bisa diselesaikan dengan menaikkan dependensi minor/patch, catat yang tidak). Jangan melakukan lompatan versi mayor dependensi tanpa alasan.
- `cd frontend && npm audit --omit=dev`: laporkan; perbaiki yang bisa lewat pembaruan minor/patch tanpa merusak build (`npm run build` dan lint harus hijau); jangan memakai `npm audit fix --force`.
- Bangun ulang image (`docker compose -f docker-compose.prod.yml build`) dan buktikan stack produksi lokal naik dan lulus pemeriksaan kesehatan.

## E · Export peserta (CSV) untuk mitra

Pengelola event butuh daftar peserta (pengambilan paket, jersey, kontak darurat).

- Endpoint `GET /v1/org/{org}/events/{event}/participants.csv?category=&status=` dan tampilan ringan di portal: halaman **Peserta** per event (`/org/[org]/events/[event]/peserta`) dengan tabel berpaginasi, cari (nama/email/BIB), filter kategori, status tiket (diterbitkan/diambil/refund), ringkasan (total, per kategori, per ukuran jersey) dan tombol "Unduh CSV".
- **Peran:** `owner` dan `admin` dapat melihat/mengunduh; `rpc_staff` hanya melihat ringkasan jersey/status dan **tidak** dapat mengunduh daftar lengkap (pertimbangkan scanner-nya tetap seperti sekarang); `finance` ditolak. Non-anggota 404.
- **Minimisasi data:** kolom CSV: BIB, nama, email, kategori, ukuran jersey, gender, tanggal lahir (bila ada), nama + telepon kontak darurat, status tiket, waktu terbit/ambil. **Nomor identitas ditampilkan hanya 4 digit terakhir** (`••••1234`), tidak pernah penuh. Tidak ada data pembayaran.
- **Keamanan:** pakai penulis CSV aman Tugas A (sel teks disanitasi, BOM UTF-8), `Content-Disposition: attachment` dengan nama berkas disanitasi, `Cache-Control: no-store`, pembatas laju per pengguna (mis. 10 unduhan/menit), batas ukuran/streaming agar event 10.000 peserta tidak menghabiskan memori (stream per baris dengan kursor, bukan memuat semua). Setiap unduhan tercatat di `audit_logs` (aktor, org, event, filter, jumlah baris; tanpa isi data).
- **Tes:** matriks peran dan tenant (event org lain → 404); isolasi (peserta org lain tidak pernah muncul); injeksi rumus pada nama peserta (`=HYPERLINK(...)`) ter-netralkan; nomor identitas selalu tersamar; filter bekerja; 10.000 peserta tersaji tanpa lonjakan memori yang tidak wajar (ukur dan laporkan); audit tercatat; refund → status tiket tercermin.

## F · Anti-bot: Cloudflare Turnstile

Tujuan: menyulitkan skrip yang membanjiri permintaan OTP dan pendaftaran antrean. **Tidak** mengklaim menghentikan bot canggih atau multi-akun (catat itu di dokumen).

- **Konfigurasi:** `TURNSTILE_SITE_KEY` (publik, untuk frontend, `NEXT_PUBLIC_TURNSTILE_SITE_KEY`), `TURNSTILE_SECRET_KEY` (rahasia). **Bila kosong, fitur mati total** (development tetap mudah dan tes lama tidak berubah). Bila salah satunya terisi tanpa pasangannya → API menolak start dengan pesan jelas. Di produksi, `.env.prod.example` memuat petunjuk membuat situs di Cloudflare (gratis) dan peringatan agar kunci tidak dikomit.
- **Verifikasi di server** ke `https://challenges.cloudflare.com/turnstile/v0/siteverify` (POST form `secret`, `response`, `remoteip` bila tersedia) dengan timeout pendek, klien HTTP yang bisa diganti saat tes. Respons ditolak bila `success=false`; token dipakai sekali. **Cocokkan field dan perilaku dengan dokumentasi resmi Cloudflare bila bisa diakses; jika tidak, tandai asumsi di kode dan laporan.** Cloudflare menyediakan kunci uji (selalu lolos/selalu gagal); sebutkan sumbernya di dokumen bila dipakai.
- **Titik pasang (hanya ini):** permintaan kode OTP (`POST /v1/auth/otp` atau yang sebenarnya) dan langkah **masuk ke antrean** (`join` waiting room) — satu tantangan per pengguna per sesi antrean, **bukan** di setiap polling atau di pembayaran. Jangan menambah tantangan pada checkout agar tidak menambah gesekan di momen genting.
- **Perilaku gagal:** token kosong/salah/kedaluwarsa → 422 dengan `fields` dan pesan Indonesia jelas ("Verifikasi keamanan gagal, coba lagi"); layanan Cloudflare tidak terjangkau/timeout → **fail closed** dengan pesan sementara yang jelas dan catatan di log (tanpa token), bukan lolos diam-diam; pembatas laju yang sudah ada (OTP dan antrean) tetap berlaku di depan verifikasi supaya verifikasi tidak bisa dipakai membebani server.
- **Frontend:** komponen widget Turnstile dimuat hanya bila site key ada (skrip dari domain Cloudflare resmi dengan `async defer`, tidak memblokir render), mode "managed"/non-interaktif, token dikirim bersama permintaan, widget di-reset setelah gagal/dipakai, teks bantuan dan aksesibilitas (label, pesan error terbaca screen reader), tombol tidak bisa diklik sebelum token tersedia dengan indikator "memeriksa…". Tidak memuat skrip bila fitur mati. Periksa dan sesuaikan CSP di `deploy/Caddyfile` agar `challenges.cloudflare.com` diizinkan untuk script/frame **hanya bila** CSP aktif, dan tanpa melonggarkan hal lain; bila Caddy belum memasang CSP, jangan menambahkannya sekarang.
- **Tes backend:** fitur mati → permintaan lolos tanpa token; fitur hidup → token kosong/salah/`success=false`/timeout/respons rusak/respons 5xx ditolak dengan kode yang benar; token valid diteruskan; secret tidak muncul di log atau pesan error; urutan pembatas laju sebelum verifikasi terbukti (setelah batas, tidak ada panggilan keluar ke server palsu).
- **Bukti browser:** dengan server siteverify palsu lokal (jangan memanggil Cloudflare sungguhan dalam uji otomatis): alur login dan masuk antrean berfungsi dengan widget uji; kasus gagal menampilkan pesan; fitur mati = perilaku identik dengan sebelumnya.

## Dokumen

Perbarui: `docs/07-api.md` (endpoint export peserta, kode error baru), `docs/08-development.md` (versi Go, cara menyalakan Turnstile dengan kunci uji, cara menguji export), `docs/10-deploy.md` (Turnstile, versi Go, catatan CSP), `docs/09-roadmap.md` (centang export peserta; tambahkan yang tersisa: undang staf RPC via email, Row-Level Security, scanner offline, observabilitas, anti multi-akun, rekonsiliasi settlement), `docs/03-war-tiket.md` bila perilaku antrean berubah, `.env.example`, `.env.prod.example`, `README.md`, `frontend/README.md`.

## Di luar cakupan

Tantangan anti-bot di checkout, deteksi multi-akun/perangkat, rate limit berbasis reputasi IP, XLSX, kolom peserta yang bisa dikustomisasi, import data, notifikasi ke peserta massal, scanner offline, Row-Level Security, observabilitas, perubahan perilaku pembayaran/ledger/KYC selain yang disebut. Jangan dikerjakan sekarang.

## Kriteria selesai (cek satu per satu dan laporkan dengan bukti nyata)

1. `make test` hijau (vet, tes Go dengan Postgres/Redis, lint, typegen, tsc), `cd frontend && npm run build` sukses, tes lama tidak diubah dan hijau (kecuali tes CSV Tugas 06 yang memang berubah perilakunya: jelaskan perubahan). Semua tes baru ada dan lulus (laporkan jumlah PASS dan 0 SKIP).
2. **A:** bukti CSV nyata (angka negatif utuh, teks berbahaya ternetralkan, jumlah kolom cocok dengan layar).
3. **B:** daftar hasil pemeriksaan log (apa yang dicari, apa yang ditemukan, apa yang diperbaiki); tes membuktikan kode OTP tidak muncul di log dengan `APP_ENV=production`; `make db-reset && make migrate && make seed` berhasil dari DB berisi data ledger; bukti `grep` untuk `session_replication_role`; tes dedupe audit; tes TTL kunci Redis.
4. **C:** daftar dialog yang diperiksa dan hasil uji Playwright navigasi dari tiap dialog bertautan; fokus/Esc/scroll lock benar.
5. **D:** versi Go lama → baru, `govulncheck` dan `npm audit` (hasil sebelum dan sesudah, yang belum bisa diperbaiki beserta alasannya), image produksi terbangun dan stack produksi lokal sehat; CI workflow memakai versi yang sama.
6. **E:** uji browser (Playwright di folder sementara di luar repo; jangan mengubah `package.json`; OTP dari Mailpit; **jangan menyentuh `.env` pribadi**; data uji fiktif "CONTOH"): halaman Peserta (tabel, cari, filter, ringkasan, unduh), CSV dibuka dan diperiksa (kolom, nomor identitas tersamar, injeksi ternetralkan), peran `rpc_staff`/`finance`/non-anggota/org lain ditolak, event 10.000 peserta (data sintetis lewat skrip, dibersihkan setelahnya) diunduh tanpa lonjakan memori yang tidak wajar (laporkan angkanya), audit tercatat. Screenshot di laptop **dan** HP 390px, **dilihat sendiri**, tanpa geser horizontal.
7. **F:** tes backend (daftar di atas) hijau; uji browser dengan server siteverify palsu: login dan masuk antrean dengan widget uji, kasus gagal, fitur mati identik dengan sebelumnya; API menolak start bila hanya satu kunci terisi; secret tidak muncul di log. Laporkan asumsi yang belum terverifikasi terhadap dokumentasi Cloudflare.
8. Konsol browser dan log API/worker bersih dari error/warning baru; semua dokumen diperbarui; `git status` bersih dari berkas uji, `.env`, atau data.

## Cara kerja

- Kerjakan berurutan A → B → C → D → E → F; jalankan `make test` di akhir tiap kelompok. D sebaiknya dikerjakan sebelum E/F supaya kode baru langsung diuji di toolchain baru; bila D menimbulkan kegagalan besar yang tidak terselesaikan, tunda D, selesaikan lainnya, dan laporkan.
- Jangan mengubah perilaku checkout, pembayaran, ledger, pencairan, verifikasi, atau scanner selain yang disebut. Bug di luar cakupan: catat di laporan akhir, jangan diperbaiki diam-diam kecuali menghalangi tugas ini.
- Jangan menulis data sensitif (nomor identitas lengkap, kode OTP, kunci, token) ke log, pesan error, audit, screenshot, atau laporan.
- Bila keputusan di dokumen ini bertabrakan dengan kode atau kenyataan teknis, pilih opsi paling sederhana yang aman dan catat alasannya.
- Tanpa rahasia di repo, tanpa email sungguhan, tanpa memanggil Cloudflare/Midtrans sungguhan dalam uji otomatis, tanpa tunnel ke internet, tanpa menyentuh `.env` pribadi.
- Di akhir: ringkasan perubahan per file, tabel hasil tiap kriteria (berdasarkan uji nyata; tandai yang tidak bisa diuji), keputusan/penyimpangan, asumsi yang belum terverifikasi, dan usulan pesan commit. **Jangan commit dan jangan push.**
