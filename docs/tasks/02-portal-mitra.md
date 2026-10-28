# Tugas 02 · Portal mitra: daftar penyelenggara, buat & kelola event sendiri

> Prompt untuk AI coding agent (Claude Code CLI). Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/02-portal-mitra.md sampai semua kriteria selesai terpenuhi"`
> Prasyarat: tugas 01 (login OTP) sudah selesai dan ter-commit.

## Konteks

RaceGo: backend Go (modular monolith) di `backend/`, frontend Next.js 16 di `frontend/`. Baca dulu `README.md`, `docs/02-arsitektur.md`, `docs/03-war-tiket.md`, `docs/08-development.md`, dan **`frontend/AGENTS.md`** (Next.js 16 berbeda dari versi lama; docs ada di `frontend/node_modules/next/dist/docs/`).

Keadaan sekarang: login email+OTP sudah ada (cookie sesi, `auth.UserID(ctx)`, halaman `/masuk` & `/akun`). Tetapi **semua penyelenggara dan event hanya berasal dari `seed.sql`**. Dashboard mitra (`/org/[org]`) hanya bisa melihat penjualan dan membuka scanner. Akun baru hanya jadi pelari; untuk jadi mitra harus dijalankan `make grant`. Halaman `/untuk-penyelenggara` masih menyuruh kirim email.

Fondasi yang sudah ada dan **wajib dipakai, bukan dibuat ulang**:

- Skema: `organizations` (slug unik, `kyc_status` pending|verified|rejected, `fee_bps` default 300, plus kolom profil dari migrasi 0002: description, city, instagram, website, theme_color, logo_url), `events` (status `draft|review|published|closed`, UNIQUE (org_id, slug), kolom tampilan: tagline, start_time, racepack_info, theme_color, cover_url), `ticket_categories` (code, name, distance_km, price, quota, max_per_user, bib_prefix, bib_next, UNIQUE (event_id, code)), `org_members` (role owner|admin|finance|rpc_staff), `audit_logs`.
- Multi-tenant: semua route mitra di bawah `/v1/org/{org}/...` lewat `tenant.RequireRole(db, roles...)`; handler **wajib** memakai `tenant.OrgID(ctx)` di setiap query dan tidak pernah memakai `org_id` dari body. Non-anggota mendapat 404 (bukan 403).
- Kuota ada di **Redis** (skrip Lua, lihat `backend/internal/checkout/inventory.go`, `sync.go`, `lua/*.lua`, dan `docs/03-war-tiket.md`). Database menyimpan `quota`; Redis menyimpan stok yang bisa dibeli. Worker menyinkronkan stok kategori aktif tiap 30 detik (`SETNX` dari DB) dan ada pre-warm sebelum penjualan dibuka. Pelajari ini sebelum menyentuh kuota.
- Nomor BIB = `bib_prefix` + 4 digit urut (`bib_next`), jadi **`bib_prefix` harus unik di dalam satu event**.
- Frontend: komponen UI di `components/ui.tsx` (`btnPrimary`, `inputCls`, dst.), gaya visual di `globals.css` dan bagian "Bahasa visual" `frontend/README.md`; `Field` (label + keterangan) di `components/event-detail.tsx` adalah pola form yang dipakai; `lib/api.ts` (`apiFetch`, sudah `credentials: "include"`); `lib/use-api.ts`. Aturan lint `react-hooks/set-state-in-effect` aktif.
- Teks untuk pengguna dalam Bahasa Indonesia; identifier bahasa Inggris; uang `int64` rupiah.

## Tujuan

Siapa pun yang sudah login bisa **mendaftarkan penyelenggaranya sendiri, membuat event beserta kategori tiket, mengunggah poster/logo, menerbitkan event, dan mengundang staf scan**, semuanya dari web, tanpa `make grant` atau menyentuh database. Event yang diterbitkan langsung muncul di beranda, kalender, dan profil penyelenggara, dan bisa dibeli lewat alur yang sudah ada.

## Keputusan desain (ikuti)

1. **Verifikasi mitra.** Penyelenggara baru berstatus `kyc_status='pending'` dan boleh membuat event **draft**, tetapi **tidak boleh menerbitkan** sebelum `verified`. Env baru `AUTO_VERIFY_ORGS` (default `true` bila `APP_ENV=development`, `false` selain itu): bila true, org langsung `verified` saat dibuat. Tambah juga `make verify ORG=slug` (subcommand `go run ./cmd/migrate verify <slug>`) untuk memverifikasi manual. Pencairan dana/rekening bank **di luar cakupan**.
2. **Peran.** `owner` dan `admin`: buat/ubah/terbitkan event dan kategori, ubah profil penyelenggara. `finance` dan `rpc_staff`: tetap seperti sekarang (hanya baca / scan). Hanya `owner` yang mengelola anggota tim. Pembuat org otomatis `owner`.
3. **Status event:** `draft` → `published` → `closed`. (`review` tetap ada di skema tapi tidak dipakai sekarang.)
   - Hanya `published` yang tampil publik (beranda, kalender, `/v1/events`, `/v1/events/{id}`, profil penyelenggara, hitung jumlah race di direktori). **Periksa semua query publik yang ada**; yang belum memfilter status wajib diperbaiki, karena draft tidak boleh bocor. Detail event publik untuk draft → 404 (anggota org melihat draf lewat endpoint mitra).
   - Hanya `draft` tanpa satu pun order yang boleh dihapus. Event yang sudah ada order tidak bisa dihapus atau dibatalkan lewat UI (refund belum ada): sediakan hanya "Tutup penjualan" (`closed`), dan tulis keterangan itu di UI.
   - `unpublish` (kembali ke draft) hanya bila belum ada order.
4. **Aturan ubah setelah ada penjualan** (ada order berstatus selain `cancelled`/`expired` pada kategori itu):
   - `code`, `price`, `bib_prefix`, `distance_km` **terkunci**.
   - `quota` boleh naik; boleh turun tetapi tidak di bawah (terjual + sedang ditahan di Redis). Perubahan kuota harus ikut menyesuaikan stok di Redis secara atomik dengan selisihnya (jangan menimpa ke angka absolut karena ada pembelian berjalan); jelaskan pilihanmu di komentar kode dan tes.
   - `sale_open_at`/`sale_close_at`/`race_date` boleh diubah, dengan aturan validasi yang sama seperti saat membuat.
   - Kategori yang sudah ada order tidak bisa dihapus.
5. **Validasi event:** nama 3–100 karakter; `race_date` di masa depan saat dibuat; `sale_open_at < sale_close_at`; `sale_close_at` paling lambat akhir hari `race_date` (zona Asia/Jakarta); `start_time` format `HH:MM`; `theme_color` hex `#RRGGBB`; deskripsi maks 5.000 karakter, tagline maks 120, `racepack_info` maks 2.000. Semua waktu dari form dikirim sebagai ISO dengan offset; form frontend memakai zona waktu Asia/Jakarta.
6. **Validasi kategori:** `code` 2–8 karakter huruf/angka kapital, unik per event; `distance_km` > 0 dan ≤ 250; `price` 0–50.000.000; `quota` 1–100.000; `max_per_user` 1–10; `bib_prefix` 1–3 digit angka, **unik per event** (tambahkan unique index `(event_id, bib_prefix)` di migrasi). Event tidak bisa diterbitkan tanpa minimal 1 kategori.
7. **Slug:** dibuat otomatis dari nama (huruf kecil, tanpa aksen, strip), unik (org: global; event: per org) dengan akhiran `-2`, `-3` bila bentrok. Slug org bisa diatur saat mendaftar dan **tidak bisa diubah** sesudahnya. Slug org yang dicadangkan (tolak): `masuk, akun, org, events, kalender, penyelenggara, untuk-penyelenggara, tickets, orders, api, admin, daftar-mitra, uploads`.
8. **Unggah gambar** (poster event = `cover_url`, logo org = `logo_url`):
   - Simpan di **disk lokal** (env `UPLOAD_DIR`, default `./data/uploads`, tambahkan ke `.gitignore`) di balik antarmuka kecil `Storage` supaya nanti mudah diganti ke S3/R2. Tidak ada layanan berbayar.
   - Terima hanya **JPEG dan PNG**, maks **3 MB**, sisi terpanjang maks 4.000 px. Tentukan tipe dari **isi file** (`http.DetectContentType` + decode `image.Decode`), bukan dari ekstensi atau header klien. Decode lalu **encode ulang** (membuang EXIF/metadata, termasuk lokasi GPS) sebelum disimpan. Nama file = hash isi (`sha256` hex) + ekstensi sesuai tipe, jadi tidak bisa ditebak dan tidak bisa menimpa file lain.
   - Endpoint mitra: `POST /v1/org/{org}/uploads` (multipart, field `file`, parameter `kind` = `cover` | `logo`) → `{"url":"/uploads/<hash>.jpg"}`; batasi ukuran body dengan `http.MaxBytesReader`. Simpan **path relatif** di DB. Publik: `GET /uploads/{name}` dengan header `Cache-Control: public, max-age=31536000, immutable`, `X-Content-Type-Options: nosniff`, dan validasi nama (`^[a-f0-9]{64}\.(jpg|png)$`) agar tidak ada path traversal.
   - Frontend memakai `API_URL + url` untuk `<img>`. Lihat bagaimana `cover_url` dan `logo_url` dipakai sekarang (kartu event BIB, detail event, profil penyelenggara) dan tampilkan gambarnya bila ada; bila null, pertahankan sampul otomatis yang sekarang. Beri `alt` yang bermakna dan jangan sampai tampilan rusak/bergeser bila gambar gagal dimuat.
   - Gambar yang tidak dipakai lagi (diganti) boleh dibiarkan; pembersihan berkala di luar cakupan.
9. **Audit:** catat ke `audit_logs` (tabel sudah ada; lihat pemakaiannya di `payment/webhook.go`) untuk: org dibuat, event dibuat/diubah/diterbitkan/ditutup/dihapus, kategori diubah, anggota ditambah/dihapus.
10. **Rate limit ringan:** satu akun maksimal 3 org yang dia buat sendiri dan 20 event draft per org (cegah spam). Balas 429/422 dengan pesan jelas.

## Backend

### Migrasi `backend/migrations/0004_partner_portal.up.sql` + `.down.sql`

- `CREATE UNIQUE INDEX ticket_categories_bib_prefix_uniq ON ticket_categories (event_id, bib_prefix);` (cek dulu bahwa data seed tidak melanggar; bila melanggar, perbaiki `seed.sql`, bukan migrasi lama).
- Kolom `organizations.created_by uuid REFERENCES users(id)` (null untuk data seed) untuk batas 3 org per akun.
- Jangan ubah migrasi 0001–0003.

### Endpoint

Publik/login (tanpa tenant):

| Method | Path | Keterangan |
| --- | --- | --- |
| POST | `/v1/orgs` (login) | `{"name","slug"?,"city","description","instagram"?,"website"?}` → membuat org + `org_members(owner)`; `201` dengan slug & `kyc_status`. Validasi slug (aturan no. 7), nama 3–80 karakter, `instagram` tanpa `@`/URL (hanya `[A-Za-z0-9._]{1,30}`), `website` harus `https://`. |
| GET | `/v1/orgs/slug-check?slug=` (login) | `{"available":bool}` untuk form pendaftaran. |
| GET | `/uploads/{name}` | berkas gambar (lihat no. 8). |

Mitra `/v1/org/{org}/...` (peran sesuai keputusan no. 2; semua query memakai `tenant.OrgID(ctx)`):

| Method | Path | Peran | Keterangan |
| --- | --- | --- | --- |
| GET / PATCH | `/profile` | baca: semua anggota; ubah: owner, admin | profil org (nama, deskripsi, kota, instagram, website, theme_color, logo_url); slug tidak bisa diubah |
| GET | `/events` | (sudah ada) | pastikan juga mengembalikan draft dan status; urutan seperti sekarang |
| POST | `/events` | owner, admin | buat draft event + (opsional) kategori awal dalam satu transaksi |
| GET | `/events/{id}` | semua anggota | detail lengkap untuk form edit (termasuk draft, kategori, jumlah terjual per kategori, flag terkunci) |
| PATCH | `/events/{id}` | owner, admin | ubah field event (aturan no. 4–5) |
| POST | `/events/{id}/categories` | owner, admin | tambah kategori (boleh juga setelah terbit bila belum ada penjualan di event itu? **tidak**: setelah terbit hanya boleh menambah kategori baru selama penjualan belum dibuka; jelaskan di kode) |
| PATCH / DELETE | `/events/{id}/categories/{catId}` | owner, admin | aturan no. 4 & 6 |
| POST | `/events/{id}/publish` | owner, admin | syarat: org `verified`, ≥1 kategori, validasi ulang semua field, `sale_close_at` belum lewat; setelah terbit **langsung muat stok kategori ke Redis** (panggil logika sync yang sudah ada, jangan menunggu worker 30 detik) |
| POST | `/events/{id}/unpublish` | owner, admin | hanya bila belum ada order |
| POST | `/events/{id}/close` | owner, admin | status `closed` (penjualan berhenti; pastikan alur reservasi/order menolak event `closed`) |
| DELETE | `/events/{id}` | owner | hanya draft tanpa order |
| POST | `/uploads` | owner, admin | aturan no. 8 |
| GET | `/members` | owner | daftar anggota (nama, email, peran) |
| POST | `/members` | owner | `{"email","role"}`: user **harus sudah pernah login**; bila belum, balas 404 `user_not_found` dengan pesan "minta orang itu masuk ke RaceGo dulu" (tidak ada email undangan di tahap ini). Peran yang boleh: admin, finance, rpc_staff. |
| PATCH / DELETE | `/members/{userId}` | owner | ubah peran / hapus; tidak boleh menghapus atau menurunkan owner terakhir, dan owner tidak bisa mengubah dirinya sendiri lewat endpoint ini |

Format error mengikuti `httpx.Error` (kode mesin + pesan Indonesia). Validasi mengembalikan 422 dengan pesan per kolom yang bisa ditampilkan di form (mis. `{"error":"validation","message":"…","fields":{"sale_close_at":"…"}}`; sesuaikan `httpx` bila perlu tanpa merusak pemakai lama).

### Hal yang wajib dicek di kode lama

- `checkout` (reservasi, order) dan semua query publik hanya menerima event `published` dan kategori dari event itu; `closed`/`draft` ditolak dengan pesan yang jelas.
- `GET /v1/events/{id}` untuk draft → 404.
- `event/organizer.go` dan direktori `/v1/organizers`: jangan menampilkan org `pending` di direktori publik; profil org `pending` tetap bisa dibuka lewat tautan langsung hanya bila punya event terbit? Sederhanakan: org `pending` tidak tampil di direktori dan profilnya 404 untuk publik.
- Worker `syncStock` dan pre-warm menangani kategori baru tanpa perubahan lain (verifikasi dengan tes).

### Tes backend (wajib, `go test ./...` hijau)

- Unit: pembuat slug (aksen, simbol, bentrok), validasi event & kategori (tabel kasus tepi), validasi gambar (file palsu berekstensi .jpg yang isinya teks → ditolak; > 3 MB → ditolak; PNG dan JPEG valid → tersimpan, EXIF hilang), nama file `/uploads/` (traversal `../`, huruf besar, ekstensi lain → 404).
- Isolasi tenant (butuh Postgres; skip bila `RACEGO_TEST_DATABASE_URL` kosong, ikuti pola `payment/status_test.go`): anggota org A **tidak bisa** melihat/ubah/hapus/terbitkan event, kategori, anggota, atau upload milik org B (semua 404); `rpc_staff` dan `finance` tidak bisa menulis (404/403 sesuai pola `RequireRole`).
- Aturan penerbitan: org `pending` → ditolak; tanpa kategori → ditolak; sukses → stok kategori ada di Redis (miniredis atau Redis uji) tanpa menunggu worker.
- Aturan setelah ada penjualan: harga/kode/prefix terkunci; kuota turun di bawah terjual ditolak; kuota naik menambah stok Redis sebesar selisihnya; hapus kategori/event berpenjualan ditolak; **uji konkurensi** kecil: ubah kuota sambil banyak reservasi berjalan, jumlah akhir tidak pernah melebihi kuota baru.
- Draft tidak bocor di semua endpoint publik (list, detail, organizer, filters).
- Owner terakhir tidak bisa dihapus/diturunkan; batas 3 org per akun.

## Frontend

Gaya visual dan komponen mengikuti yang sudah ada. Semua form: label tetap terlihat (pakai pola `Field`), tanda wajib, pesan error per kolom dari `fields`, tombol disabled saat menyimpan, dan tidak kehilangan isian saat error. Layar HP harus dipakai nyaman (tanpa geser horizontal).

- **`/daftar-mitra`** (login wajib; bila belum, arahkan ke `/masuk?next=/daftar-mitra`): form nama, slug (otomatis dari nama, bisa diedit, cek ketersediaan dengan debounce), kota, deskripsi singkat, instagram, website. Sukses → `/org/{slug}` dengan banner "Penyelenggara dibuat" (dan keterangan "menunggu verifikasi" bila `pending`). Perbarui `me` agar menu header langsung menampilkan dashboard baru.
- **`/untuk-penyelenggara`**: ganti ajakan `mailto` dengan tombol "Daftarkan penyelenggara" → `/daftar-mitra`; sesuaikan teks "Cara bergabung" dengan alur baru. Header untuk pengguna login yang belum punya org: tambahkan satu tautan "Jadi penyelenggara" di menu akun.
- **Dashboard `/org/[org]`** (perluas yang ada, jangan hilangkan penjualan & scanner): navigasi tab/segmen **Event · Tim · Profil**; daftar event dengan lencana status (Draft/Terbit/Ditutup), tombol "Buat event" (owner/admin), tautan Edit, dan tautan scanner seperti sekarang. Banner jelas bila org `pending` ("belum bisa menerbitkan").
- **`/org/[org]/events/new`** dan **`/org/[org]/events/[eventId]/edit`**: satu komponen form dengan bagian **Informasi** (nama, tagline, deskripsi, kota, lokasi, tanggal race, jam flag-off, info race pack, warna tema), **Poster** (unggah + pratinjau + hapus), **Penjualan** (buka/tutup, input `datetime-local` berzona Asia/Jakarta, tampilkan hasilnya "Dibuka Sab, 3 Jan 2027 10.00 WIB"), **Kategori tiket** (tabel dapat diedit: tambah/hapus baris; kolom kode, nama, jarak, harga, kuota, maks per akun, prefix BIB; baris terkunci menampilkan ikon/teks "terkunci karena sudah ada penjualan" dan jumlah terjual), dan bilah aksi: **Simpan draft**, **Terbitkan** (dengan ringkasan cek: org terverifikasi, ada kategori, tanggal valid), **Tutup penjualan**, **Hapus draft**. Konfirmasi (dialog yang bisa dipakai keyboard, bukan `window.confirm`) untuk aksi yang tidak bisa dibatalkan. Tombol **Lihat halaman publik** bila sudah terbit. Pratinjau kartu event (komponen `EventBib` yang sudah ada) yang ikut berubah saat form diisi.
- **Tab Tim**: daftar anggota + peran, form tambah by email + peran, ubah peran, hapus; pesan jelas bila email belum pernah login.
- **Tab Profil**: ubah deskripsi, kota, instagram, website, warna tema, logo (unggah). Slug ditampilkan tapi tidak bisa diubah.
- Tampilkan `cover_url`/`logo_url` di tempat yang relevan (lihat keputusan no. 8). Perbarui `lib/api.ts` (tipe + fungsi), dan hindari memanggil API yang butuh peran bila `me.orgs` menunjukkan user tidak berhak (sembunyikan tombolnya).
- Pastikan semua halaman baru yang memakai `useParams`/`useSearchParams` dibungkus `<Suspense>` di `page.tsx` (aturan `cacheComponents` Next 16).

## Env & dokumentasi

- `.env.example`: `AUTO_VERIFY_ORGS=true` (komentar: false di produksi) dan `UPLOAD_DIR=./data/uploads`. Jangan mengubah `.env` pribadiku selain menambah dua baris itu bila belum ada.
- `.gitignore`: `backend/data/` (atau lokasi `UPLOAD_DIR` default).
- Perbarui: `docs/07-api.md` (semua endpoint baru + format error validasi), `docs/02-arsitektur.md` (alur penerbitan event & sinkron stok, penyimpanan gambar), `docs/01-prd.md` (ruang lingkup: mitra mandiri sudah masuk MVP), `docs/08-development.md` (`make verify`, folder uploads), `docs/09-roadmap.md` (centang CRUD event, onboarding mitra versi dasar, upload poster/logo, undang staf versi dasar; sisakan: verifikasi dokumen/rekening, super admin, undangan email, pembersihan upload), `README.md` (alur "Coba ini" untuk mitra), `frontend/README.md` (halaman baru), `docs/06-infra-gratis.md` (folder uploads harus di disk persisten dan masuk backup).

## Di luar cakupan

Rekening bank & pencairan, dokumen KYC, peran super admin, refund/pembatalan event berpenjualan, undangan via email, hapus org, galeri multi-gambar, editor teks kaya, kupon/harga bertahap, CSV peserta. Jangan dikerjakan sekarang.

## Kriteria selesai (cek satu per satu dan laporkan)

1. `make test` hijau (Go vet + tes, lint, typegen, tsc) dan `cd frontend && npm run build` sukses. Tes yang butuh Postgres dijalankan dengan `RACEGO_TEST_DATABASE_URL` terisi (jangan hanya di-skip).
2. Uji browser sungguhan dengan Playwright (pasang di folder sementara di luar repo, mis. `/tmp/racego-e2e`; jangan mengubah `package.json`; ambil kode OTP dari Mailpit dengan override env `SMTP_ADDR=localhost:1025 SMTP_USERNAME= SMTP_PASSWORD= MIDTRANS_SERVER_KEY=` di baris perintah, **jangan menyentuh `.env`**), setelah `make db-reset`:
   - Akun baru login → `/daftar-mitra` → org terbuat → dashboard muncul, menu header menampilkan dashboard baru.
   - Buat event baru (3 kategori, unggah poster JPEG dan logo PNG), simpan draft → **tidak** muncul di beranda/kalender/direktori untuk pengunjung anonim (cek juga `GET /v1/events/{id}` → 404).
   - Terbitkan → muncul di beranda, kalender, dan profil penyelenggara lengkap dengan poster; hitung stok kategori di Redis benar.
   - Akun pelari lain membeli tiket event itu sampai tiket terbit (mode mock), lalu pemilik melihat penjualan naik dan mencoba mengubah harga kategori itu → ditolak; menaikkan kuota → stok Redis ikut naik; menurunkan kuota di bawah terjual → ditolak.
   - Tambah anggota tim `rpc_staff` (akun yang sudah pernah login) → akun itu bisa membuka scanner tapi tidak melihat tombol Buat/Edit event; memanggil endpoint tulis langsung via `curl` dengan cookie-nya → ditolak.
   - Akun org lain mencoba `curl` ke `/v1/org/{slug-org-A}/events/{id}` → 404.
   - Unggah file teks yang diberi nama `.jpg` dan file > 3 MB → ditolak dengan pesan jelas; EXIF pada JPEG uji hilang dari file tersimpan.
   - `AUTO_VERIFY_ORGS=false`: org baru `pending` bisa membuat draft tetapi tombol Terbitkan ditolak dengan penjelasan; `make verify ORG=slug` lalu bisa diterbitkan.
   - Tampilan HP 390px untuk `/daftar-mitra`, form event (termasuk tabel kategori), dan tab Tim: lihat screenshot-nya dan perbaiki yang rusak (tabel kategori harus tetap bisa dipakai di HP, mis. berubah jadi kartu per kategori).
   - Konsol browser dan log API/worker bersih dari error/warning baru.
3. Uji beban singkat tetap lulus: setelah perubahan, `make db-reset` lalu skrip burst 3.000 request ke satu kategori berkuota 200 menghasilkan tepat 200 reservasi sukses (gunakan `loadtest/war-tiket.js` bila k6 tersedia; bila tidak, tes Go konkurensi yang sudah ada sudah cukup, sebutkan mana yang dipakai).
4. `git status` bersih dari file yang tidak seharusnya (tidak ada `data/uploads`, `.env`, atau berkas uji di repo).

## Cara kerja

- Bertahap, dan jalankan tes tiap tahap: migrasi → paket pengunggah + tes → endpoint org/profil → event/kategori/penerbitan + tes → anggota tim → pengecekan kode lama yang disebut di atas → frontend (daftar-mitra, dashboard, form event, tim, profil) → dokumentasi → uji browser.
- Jangan mengubah perilaku checkout, pembayaran, tiket, scanner, atau login selain yang disebut di atas. Bila menemukan bug di luar cakupan, catat di laporan akhir, jangan diperbaiki diam-diam kecuali menghalangi tugas ini.
- Bila sebuah keputusan di dokumen ini ternyata bertabrakan dengan kode yang ada, pilih opsi paling sederhana yang aman, catat alasannya di laporan akhir.
- Di akhir: ringkasan perubahan per file, tabel hasil tiap kriteria (berdasarkan uji nyata), daftar keputusan/penyimpangan, dan usulan pesan commit. **Jangan commit dan jangan push.**
