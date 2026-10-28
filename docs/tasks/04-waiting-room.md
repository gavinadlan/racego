# Tugas 04 · Antrian virtual (waiting room) untuk war tiket + dua perbaikan kecil

> Prompt untuk AI coding agent (Claude Code CLI). Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/04-waiting-room.md sampai semua kriteria selesai terpenuhi"`
> Prasyarat: tugas 01–03 sudah ter-commit.

## Konteks

RaceGo: backend Go (`backend/`), frontend Next.js 16 (`frontend/`), Postgres, Redis. Baca dulu `README.md`, **`docs/03-war-tiket.md`** (arsitektur war tiket, daftar "Belum dibuat" nomor 1 = tugas ini), `docs/02-arsitektur.md`, `docs/08-development.md`, `docs/10-deploy.md`, dan **`frontend/AGENTS.md`** (Next.js 16 berbeda dari versi lama; docs di `frontend/node_modules/next/dist/docs/`).

Yang sudah ada dan **tidak boleh diubah perilakunya**:

- Reservasi atomik di Redis: `backend/internal/checkout/inventory.go`, `lua/{reserve,release,confirm,adjust}.lua`, hash tag `{cat:<id>}`; handler `Reserve` di `checkout/handler.go` (`POST /v1/categories/{categoryID}/reservations`, wajib login). Tes "1.000 pembeli berebut 50 slot → tepat 50" dan uji burst 3.000 request → tepat 200 sukses harus tetap lulus.
- Worker (`backend/internal/worker/worker.go`): loop berkala (outbox 500 ms, hold 15 detik, sinkron stok 30 detik, rekonsiliasi Midtrans 1 menit). Pre-warm stok 30 menit sebelum penjualan dibuka (`checkout/sync.go`).
- Login cookie (`auth.UserID(ctx)`, `auth.Require`), portal mitra (`internal/partner`, form event di `frontend/src/components/event-form.tsx`), multi-tenant `tenant.OrgID(ctx)`.
- Format error `httpx.Error` (kode mesin + pesan Indonesia), validasi 422 dengan `fields`.
- Pola frontend: `components/ui.tsx` (`btnPrimary`, `Field`, dst.), `clock.tsx` (`useNow`, `BigCountdown`), `lib/use-api.ts`; aturan lint `react-hooks/set-state-in-effect` aktif; halaman yang memakai `useParams`/`useSearchParams` dibungkus `<Suspense>`.
- Teks pengguna Bahasa Indonesia, identifier Inggris.

Catatan jujur untuk dokumentasi: reservasi Redis sendiri sudah sangat murah (teruji 3.000 request/200 slot). Yang diselesaikan antrian adalah (a) **melindungi API/Next.js/Postgres** dari ribuan pembeli yang memuat halaman dan menekan tombol bersamaan di server kecil, (b) **keadilan**: yang masuk antrian sebelum penjualan dibuka diacak, bukan siapa tercepat ping, dan (c) **pengalaman pengguna**: posisi dan estimasi yang jelas, bukan layar error atau refresh berulang. Tulis klaim di dokumen sesuai itu dan jangan melebih-lebihkan.

## Tujuan

Untuk event yang mengaktifkan antrian, pembeli harus masuk **antrian virtual** lebih dulu; hanya yang sudah "dipanggil" (memegang pass bertanda tangan) yang boleh memanggil reservasi. Antrian terbuka sebagai **lobi** beberapa menit sebelum penjualan dibuka, urutan lobi **diacak** saat jam buka, lalu pembeli dipanggil bertahap sesuai kapasitas. Event tanpa antrian berjalan persis seperti sekarang.

## Keputusan desain (ikuti)

1. **Per event, opsional.** Kolom baru `events.waiting_room boolean NOT NULL DEFAULT false` (migrasi `0005_waiting_room.up/down.sql`; jangan ubah migrasi lama). Mitra mengaturnya di form event (kotak centang "Aktifkan antrian virtual", dengan keterangan: disarankan untuk event dengan peminat jauh melebihi kuota; tidak bisa diubah setelah penjualan dibuka; peran owner/admin). Validasi di backend yang sama. Tambahkan ke detail event publik (`waiting_room: bool`).
2. **Wajib login untuk masuk antrian**, satu posisi per akun per event (`ZADD NX`). Tidak ada antrian anonim. (Perlindungan bot/akun ganda — Turnstile, verifikasi NIK — **di luar cakupan**; catat di roadmap.)
3. **Lobi dan keadilan.** Lobi terbuka `QUEUE_LOBBY_MINUTES` (default 15) sebelum `sale_open_at`. Peserta lobi mendapat skor acak sekali saat bergabung (`crypto/rand`, mis. 53-bit); pada jam buka urutannya = urutan skor, jadi **waktu masuk lobi tidak berpengaruh**. Yang bergabung **setelah** jam buka diberi skor di atas semua skor lobi (`1<<53 + waktu_ms`), jadi tetap di belakang peserta lobi dan berurutan menurut kedatangan. Jelaskan aturan ini di halaman antrian dengan kalimat sederhana.
4. **Pemanggilan (admission).** Dikerjakan **worker** tiap 1 detik, atomik lewat skrip Lua (aman bila ada beberapa instance). Aturan: memanggil sebanyak `min(QUEUE_ADMIT_PER_SEC × detik, QUEUE_MAX_ACTIVE − jumlah_pass_aktif)` dari depan antrian. Default `QUEUE_ADMIT_PER_SEC=20`, `QUEUE_MAX_ACTIVE=300`, `QUEUE_PASS_TTL=10m`. Peserta yang tidak menunjukkan tanda hidup (heartbeat: terakhir memanggil status dalam `QUEUE_HEARTBEAT_TTL`, default 45 detik) **dilewati dan dibuang**, supaya orang yang menutup tab tidak memakan slot.
5. **Pass.** Setelah dipanggil, status memuat `pass`: token HMAC-SHA256 stateless berisi `event_id`, `user_id`, `exp`, ditandatangani dengan kunci `QUEUE_SECRET` (env baru; di development ada nilai bawaan, di produksi **wajib** diisi dan `scripts/gen-secrets.sh` ikut membuatnya; jangan memakai kembali `QR_PRIVATE_KEY`). `Reserve` untuk event ber-antrian **wajib** header `X-Queue-Pass`; verifikasi: tanda tangan valid, belum kedaluwarsa, cocok dengan event dan user, **dan** masih terdaftar di himpunan pass aktif Redis (agar bisa dicabut). Gagal → 403 `queue_required` (atau `queue_pass_expired`) dengan pesan jelas. Bandingkan tanda tangan dengan `hmac.Equal`.
6. **Pass dilepas** (slot kapasitas kembali) saat: reservasi pertama **berhasil** (pembeli sudah mengamankan slot; ia punya 15 menit untuk checkout; untuk membeli kategori lain ia perlu antri lagi, catat ini di dokumentasi), pass kedaluwarsa, atau pembeli meninggalkan antrian lewat `DELETE`.
7. **Status akhir antrian:** `lobby` (belum buka), `waiting`, `admitted`, `sold_out` (semua kategori stok 0 **dan** tidak ada hold aktif; mengambil data dari Redis yang sama dengan reservasi, murah), `closed` (penjualan ditutup), `not_in_queue`. Ketika `sold_out`/`closed`, semua yang menunggu mendapat status itu pada polling berikutnya dan antrian dibersihkan (TTL) setelah beberapa menit.
8. **Polling dengan jitter dari server.** Respons status memuat `poll_after_ms` yang dihitung server: dekat dengan depan antrian → 2–3 detik; makin jauh makin jarang (hingga ~15 detik), ditambah jitter acak ±20%, supaya puluhan ribu klien tidak menembak serempak. Frontend wajib mengikuti nilai ini (bukan interval tetap). Endpoint status dibatasi: minimal selang 1 detik per akun (balas 429 `retry_after`); join/leave dibatasi per akun dan per IP memakai pembatas Redis (pakai ulang atau ekstrak dari kode rate-limit OTP yang sudah ada di `internal/auth/otp.go`, jangan menulis versi kedua yang berbeda).
9. **Redis**: semua key antrian satu event memakai hash tag `{ev:<id>}` (cluster-ready): `…:queue` (ZSET skor = urutan), `…:seen:<uid>` (TTL heartbeat) atau ZSET `…:seen`, `…:active` (ZSET pass aktif, skor = kedaluwarsa), `…:opened` (penanda urutan lobi sudah dibekukan). Skrip Lua baru di `internal/queue/lua/` (join, admit, release) dengan embed seperti `checkout`. Antrian harus selamat dari restart Redis (appendonly sudah aktif); bila datanya tetap hilang, klien mendapat `not_in_queue` dan UI mengajak bergabung lagi (ke belakang), tanpa error mentah.
10. **Waktu** selalu dari server; respons membawa `server_time` agar frontend bisa menghitung hitung mundur tanpa bergantung jam perangkat.
11. Nilai default ini cukup untuk server kecil; semua bisa diubah lewat env dan wajib dicantumkan di `.env.example` dan `.env.prod.example`.

## Backend

Paket baru `backend/internal/queue/`:

- `queue.go`: `Queue` (Redis) dengan `Join(ctx, eventID, userID, now) (State, error)`, `Status(ctx, eventID, userID, now) (State, error)` (sekaligus menyegarkan heartbeat), `Leave`, `ReleasePass`, `Admit(ctx, eventID, now) (admitted int, err)`, `PassValid(ctx, eventID, userID) bool`. `State` memuat: `state`, `position` (1-based, bila waiting/lobby), `ahead`, `queue_size`, `eta_seconds` (perkiraan dari laju panggil terukur terakhir, jangan angka palsu bila belum ada data → null), `pass` + `pass_expires_at` (bila admitted), `poll_after_ms`, `server_time`, `lobby_opens_at`, `sale_open_at`.
- `pass.go`: tanda tangan dan verifikasi pass.
- `lua/*.lua` dan tes.
- `handler.go`: endpoint di bawah.

Endpoint (semua `auth.Require`):

| Method | Path | Keterangan |
| --- | --- | --- |
| POST | `/v1/events/{eventID}/queue` | join; 201 bila baru, 200 bila sudah ada; 409 `queue_not_open` di luar lobi/penjualan; 404 bila event tidak ada/bukan published/tanpa antrian |
| GET | `/v1/events/{eventID}/queue/me` | status + heartbeat; `not_in_queue` bila belum bergabung |
| DELETE | `/v1/events/{eventID}/queue/me` | keluar antrian; 204 |

Integrasi:

- `checkout.Reserve`: bila event memakai antrian dan penjualan terbuka → wajib pass valid (aturan no. 5); setelah reservasi sukses → `ReleasePass`. Info `waiting_room` ikut di cache kategori 3 detik (`cachedCategory`). Event tanpa antrian: tidak ada perubahan perilaku maupun biaya Redis tambahan.
- `worker`: loop admission tiap 1 detik untuk event ber-antrian yang sedang dalam jendela lobi–penjualan (cari daftar event aktif dari Redis `queue:events` atau dari cache DB per beberapa detik; jangan query Postgres tiap detik per event), pembersihan antrian untuk event `sold_out`/`closed`, dan pembekuan urutan lobi saat jam buka. Gagal Redis sementara → catat log, lanjut iterasi berikut, jangan crash.
- `partner` (portal mitra): kolom `waiting_room` di create/update event + validasi (tidak boleh berubah setelah `sale_open_at` terlewat; hanya owner/admin) + audit log; sertakan di `GET /events/{id}` mitra.
- `config`: env `QUEUE_SECRET`, `QUEUE_LOBBY_MINUTES`, `QUEUE_ADMIT_PER_SEC`, `QUEUE_MAX_ACTIVE`, `QUEUE_PASS_TTL`, `QUEUE_HEARTBEAT_TTL`. Di luar development, `QUEUE_SECRET` kosong → API **menolak start** dengan pesan jelas (konsisten dengan kunci QR).
- Header CORS tidak perlu diubah (satu origin di produksi; di development origin `FRONTEND_URL`): tambahkan `X-Queue-Pass` ke `Access-Control-Allow-Headers`.
- `docker-compose.prod.yml`, `scripts/gen-secrets.sh`, `.env.prod.example`, `.env.example`: tambah `QUEUE_SECRET` dan parameter lain.

### Tes backend (wajib, `go test ./...` hijau, tes Postgres/Redis tidak di-skip)

- Unit dengan miniredis (Lua didukung) atau Redis uji: join idempoten; satu posisi per akun; urutan lobi **acak** (statistik: korelasi urutan masuk vs urutan antrian di 2.000 peserta mendekati nol, ambang longgar agar tidak flaky) dan peserta pasca-buka selalu di belakang peserta lobi; admission menghormati laju dan `MAX_ACTIVE`; peserta tanpa heartbeat dibuang; pass: valid, kedaluwarsa, event/user salah, tanda tangan diubah, dicabut → semua ditolak/diterima sesuai; `ReleasePass` memulihkan kapasitas; `sold_out` terdeteksi; `poll_after_ms` naik menurut posisi dan ada jitter.
- **Konkurensi:** 5.000 goroutine join + admission berjalan paralel + banyak `Reserve` dengan pass: **reservasi sukses tidak pernah melebihi kuota**, tidak ada user yang dipanggil dua kali, jumlah pass aktif tidak pernah melebihi `MAX_ACTIVE`.
- Integrasi HTTP (Postgres + Redis): event ber-antrian — `Reserve` tanpa pass → 403 `queue_required`; dengan pass sah → 201; setelah itu pass dilepas; event tanpa antrian tidak butuh pass; event bukan published → 404; limit status 429; mitra org lain tidak bisa mengubah `waiting_room` event (404) dan tidak bisa mengubahnya setelah penjualan dibuka (409/422).
- Tes lama checkout (1.000 goroutine/50 slot dll.) tetap hijau tanpa diubah.

## Frontend

- **`lib/api.ts`**: tipe `QueueState`, fungsi `joinQueue`, `queueStatus`, `leaveQueue`; `apiFetch` dapat menambah header `X-Queue-Pass`.
- **Halaman detail event** (`event-detail.tsx`): bila `event.waiting_room` dan penjualan terbuka atau lobi terbuka, tombol Daftar diganti kartu **Antrian**:
  - Sebelum lobi: hitung mundur ke "Antrian dibuka" (pakai `BigCountdown`); bila event ber-antrian tampilkan keterangan singkat cara kerjanya (urutan diacak saat jam buka bagi yang masuk lobi).
  - Belum login → ajak masuk (`/masuk?next=…`), seperti pola yang ada.
  - Lobi: tombol "Masuk lobi", lalu status "Kamu di lobi. Urutan diacak pada {jam buka}" + hitung mundur; **jangan** menampilkan nomor posisi palsu sebelum urutan dibekukan.
  - Menunggu: posisi, jumlah di depan, estimasi ("± 4 menit", tampilkan hanya bila `eta_seconds` ada, dibulatkan, dengan kata "perkiraan"), bilah progres, peringatan jelas "Biarkan halaman ini terbuka; jika ditutup lebih dari 45 detik, kamu keluar dari antrian", tombol "Keluar antrian". Polling mengikuti `poll_after_ms`; hentikan saat tab tersembunyi? **Jangan** hentikan (heartbeat), tetapi cukup tetap mengikuti `poll_after_ms`. Hitung mundur pakai `server_time`.
  - Dipanggil: banner "Giliran kamu!" + hitung mundur masa pass (`pass_expires_at`), tombol Daftar aktif; pass disimpan di `sessionStorage` per event (bukan localStorage) dan dikirim sebagai `X-Queue-Pass` ke `Reserve`; bila `queue_pass_expired`, tampilkan pesan dan tawarkan masuk antrian lagi.
  - `sold_out`/`closed`: pesan akhir yang jelas, tanpa polling lagi.
  - `not_in_queue` setelah sebelumnya di antrian (mis. Redis hilang): pesan ramah + tombol bergabung lagi.
  - Semua status memakai `aria-live="polite"` yang **diumumkan hanya saat berubah bermakna** (pindah ≥ 10 posisi, atau pindah status), bukan tiap detik; fokus tidak boleh dicuri. Tampilan HP 390px nyaman.
- **Form event mitra** (`event-form.tsx`): kotak centang + keterangan (aturan no. 1); nonaktif dengan penjelasan bila penjualan sudah dibuka. Dashboard mitra: lencana "Antrian aktif" pada event yang memakainya; **ringkasan antrian langsung** untuk mitra (jumlah di antrian, pass aktif, terpanggil total) lewat endpoint mitra baru `GET /v1/org/{org}/events/{id}/queue` (owner/admin/finance; tenant-scoped) yang dipoll tiap 5 detik di halaman event mitra.
- Jangan memperkenalkan animasi berlebihan; ikuti gaya visual yang ada (lihat "Bahasa visual" di `frontend/README.md`). Satu-satunya gerak: hitung mundur dan bilah progres.

## Dua perbaikan kecil (bagian dari tugas ini)

1. **Tiket saya tidak memuat ulang sendiri.** Tiket terbit beberapa detik setelah lunas (worker). Tambahkan ke `GET /v1/orders/{id}` field `tickets_issued` (jumlah tiket terbit untuk order itu). Di halaman order saat `status = paid` tampilkan "Tiket sedang diterbitkan…" dan polling ringan sampai `tickets_issued == qty`, lalu tampilkan tombol "Lihat tiket saya" aktif; di `/tickets` bila datang dari order yang baru lunas (`?order={id}`), polling `me/tickets` setiap 2 detik maksimal 60 detik sampai tiket order itu muncul, dengan keterangan "Menunggu tiket terbit…" dan pesan bantuan bila lewat 60 detik ("Tiket belum muncul? Muat ulang beberapa saat lagi; email tiket tetap dikirim").
2. **`/untuk-penyelenggara` menjanjikan fitur yang belum ada** (early bird, pencairan dana). Tulis ulang kartu fitur dan "Cara bergabung" agar hanya memuat yang benar-benar ada: daftar mandiri & kelola event sendiri, kategori & kuota, tiket QR + nomor BIB otomatis, scanner race pack & tim staf, laporan penjualan, tahan lonjakan (antrian virtual opsional). Fitur yang belum ada ditaruh di bagian "Segera hadir" yang jelas berlabel (pencairan dana, refund, kupon/harga bertahap), bukan di daftar fitur utama. Perbarui klaim biaya agar sesuai kenyataan (tuliskan bahwa pencairan dana belum tersedia di versi ini).

## Skrip simulasi dan uji beban

Skrip Node **`scripts/queue-sim.mjs`** (tanpa dependensi npm baru; gunakan `fetch` bawaan; **hanya jalan terhadap API development** karena memakai `X-User-ID`, tolak berjalan bila `/healthz` menunjukkan bukan development atau host bukan localhost) dengan dua mode:

- `load`: `--users 10000 --quota 200`. Membuat event uji ber-antrian (lewat SQL/endpoint mitra atau perintah di `cmd/migrate`; sebutkan caranya di dokumentasi) dengan jam buka beberapa detik lagi, memasukkan N pengguna ke lobi, menunggu buka, lalu setiap pengguna berperilaku seperti klien nyata (polling mengikuti `poll_after_ms`, bila dipanggil langsung `Reserve` dengan pass). Laporkan: jumlah reservasi sukses (**harus ≤ kuota dan = kuota bila peminat ≥ kuota**), distribusi waktu tunggu, p50/p95/p99 latensi `queue/me` dan `Reserve`, laju panggil aktual, penggunaan memori Redis, jumlah galat, uji keadilan (korelasi urutan masuk lobi vs urutan dipanggil mendekati nol), dan konsistensi akhir (stok Redis + hold + terjual = kuota).
- `bots`: `--users 500 --event <id|slug> --reserve-rate 0.7`. Mengisi antrian dengan pengguna palsu yang bertingkah seperti manusia (sebagian membatalkan, sebagian tidak membeli, sebagian mereservasi) agar saat **kamu** mencoba satu akun sungguhan di browser, posisi bergerak dan stok habis secara realistis.
- Target Makefile: `queue-demo` (membuat event demo ber-antrian: kuota 50, lobi sudah terbuka, jam buka 3 menit lagi, mencetak URL) dan `queue-load` (menjalankan mode `load` dengan nilai default yang masuk akal untuk laptop). Update `loadtest/` dan `docs/03-war-tiket.md` tentang cara memakainya; `loadtest/war-tiket.js` lama tetap berlaku untuk event tanpa antrian.

## Dokumentasi

Perbarui: `docs/03-war-tiket.md` (pindahkan waiting room ke "Sudah diimplementasikan", diagram alur baru dengan lobi → antrian → pass → reservasi, tabel key Redis baru, parameter env, jaminan keadilan dan batasnya, hasil `queue-load` **yang benar-benar terukur** di mesin ini dengan keterangan mesinnya, dan batas jujur: antrian tidak mencegah akun ganda/bot), `docs/07-api.md` (3 endpoint + endpoint mitra + header `X-Queue-Pass` + kode error + field `waiting_room`/`tickets_issued`), `docs/02-arsitektur.md`, `docs/01-prd.md` (ruang lingkup), `docs/08-development.md` (`make queue-demo`, `make queue-load`), `docs/10-deploy.md` + `.env.prod.example` (`QUEUE_*`, `QUEUE_SECRET`), `docs/09-roadmap.md` (centang waiting room; tambah: Turnstile/anti-bot, ballot, antrian lintas-event), `README.md`, `frontend/README.md`.

## Di luar cakupan

Turnstile/captcha, verifikasi akun ganda, ballot/undian, antrian lintas event, WebSocket/SSE (polling cukup), CDN untuk halaman tunggu, notifikasi WhatsApp/email "giliranmu", pencairan dana. Jangan dikerjakan sekarang.

## Kriteria selesai (cek satu per satu dan laporkan dengan bukti nyata)

1. `make test` hijau (Go vet + tes dengan Postgres/Redis, lint, typegen, tsc), `cd frontend && npm run build` sukses, tes lama (1.000 goroutine/50 slot) tidak berubah dan hijau.
2. Semua tes baru di bagian Tes backend ada dan lulus, termasuk uji konkurensi (5.000 join + admission + reserve) dijalankan **dengan `-race`** sekali dan hasilnya dilaporkan.
3. `make queue-load` (10.000 pengguna, kuota 200) pada mesin ini: tepat 200 reservasi sukses, 0 oversell, tidak ada user dipanggil dua kali, `MAX_ACTIVE` tidak pernah terlampaui, korelasi keadilan mendekati nol, galat tak terduga 0; laporkan angka p95 `queue/me` dan `Reserve`, memori Redis, durasi total, dan spesifikasi mesin. Bila 10.000 terlalu berat untuk mesin ini, turunkan dan **katakan** berapa yang benar-benar diuji.
4. Uji browser sungguhan (Playwright di folder sementara di luar repo; jangan mengubah `package.json`; OTP dari Mailpit; jangan menyentuh `.env` pribadi) dengan `make queue-demo` + `bots`: pengguna sungguhan login → melihat hitung mundur lobi → masuk lobi → jam buka → status berubah ke menunggu dengan posisi yang bergerak → dipanggil → Daftar aktif → reservasi → bayar (mock) → tiket terbit. Tangkap screenshot tiap keadaan (lobi, menunggu, dipanggil, habis, pass kedaluwarsa), **lihat sendiri** dan perbaiki yang rusak, termasuk lebar HP 390px tanpa geser horizontal. Uji juga: tutup tab >45 detik → keluar antrian; pass dicoba dipakai tanpa antrian lewat `curl` → 403 `queue_required`; pass milik user lain → 403.
5. Event tanpa antrian: alur lama tidak berubah (reservasi tanpa pass tetap jalan; burst 3.000 request → tepat 200 sukses).
6. Mitra: kotak centang "antrian virtual" muncul di form, terkunci setelah penjualan dibuka, tersimpan, lencana + ringkasan antrian langsung tampil di dashboard mitra; akun org lain tidak bisa melihat atau mengubahnya (404).
7. Perbaikan kecil: setelah bayar (mock), halaman order dan `/tickets?order=…` menampilkan tiket sendiri tanpa refresh manual (buktikan dengan Playwright, tanpa `reload()` dari skrip uji); `/untuk-penyelenggara` tidak lagi menyebut early bird atau pencairan dana sebagai fitur aktif, dan bagian "Segera hadir" berlabel jelas.
8. Stack produksi lokal (`make prod-up` dengan `SITE_ADDRESS=localhost`) menerima `QUEUE_SECRET` dari `gen-secrets.sh`, API menolak start bila kosong di produksi, dan satu putaran alur antrian lewat Caddy/HTTPS berfungsi (header `X-Queue-Pass` lolos). Matikan stack setelahnya; jangan membuka tunnel ke internet.
9. Konsol browser dan log API/worker bersih dari error/warning baru; `git status` bersih dari berkas uji, `.env`, atau data.

## Cara kerja

- Bertahap, uji tiap tahap: migrasi + config → paket `queue` + Lua + tes → integrasi `Reserve`/worker/partner → endpoint + tes HTTP → frontend → perbaikan kecil → skrip simulasi + `queue-load` → dokumentasi → uji browser dan produksi lokal.
- Jangan mengubah perilaku checkout, pembayaran, tiket, scanner, login, atau portal mitra selain yang disebut di atas. Bug di luar cakupan: catat di laporan akhir, jangan diperbaiki diam-diam kecuali menghalangi tugas ini.
- Bila keputusan di dokumen ini bertabrakan dengan kode atau kenyataan teknis, pilih opsi paling sederhana yang aman dan catat alasannya.
- Tanpa rahasia di repo, tanpa email sungguhan, tanpa tunnel ke internet, tanpa menyentuh `.env` pribadi (override lewat baris perintah).
- Di akhir: ringkasan perubahan per file, tabel hasil tiap kriteria (berdasarkan uji nyata; tandai yang tidak bisa diuji), keputusan/penyimpangan, dan usulan pesan commit. **Jangan commit dan jangan push.**
