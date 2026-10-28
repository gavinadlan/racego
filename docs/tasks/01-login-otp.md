# Tugas 01 · Login sungguhan (email OTP) untuk RaceGo

> Prompt untuk AI coding agent (Claude Code CLI). Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/01-login-otp.md sampai semua kriteria selesai terpenuhi"`

## Konteks

RaceGo adalah platform tiket event lari multi-penyelenggara: backend Go (modular monolith) di `backend/`, frontend Next.js 16 di `frontend/`. Baca dulu `README.md`, `docs/02-arsitektur.md`, `docs/08-development.md`, dan **`frontend/AGENTS.md`** (Next.js 16 berbeda dari versi lama; dokumentasinya ada di `frontend/node_modules/next/dist/docs/`).

Saat ini **belum ada login**. Identitas dikirim lewat header `X-User-ID` yang diisi pemilih akun contoh (menu "Dev") di frontend:

- `backend/internal/auth/auth.go`: `Middleware(devMode)` membaca `X-User-ID` hanya bila `APP_ENV=development`; `Require` menolak request tanpa user; `UserID(ctx)` dipakai semua handler. Ada `TODO(auth)`.
- `frontend/src/lib/api.ts`: `getDevUserId`/`setDevUserId` (localStorage `racego.devUser`), `apiFetch` menambahkan `X-User-ID`.
- `frontend/src/components/session.tsx`: `SessionProvider` dengan `{ ready, userId, me, switchUser }`, `me` dari `GET /v1/me`. Dipakai di ±9 komponen lewat `useSession()`.
- `frontend/src/components/header.tsx`: pemilih Dev (`aria-label="Masuk sebagai akun contoh"`, daftar `DEV_USERS` dari `lib/format.ts`).
- `frontend/src/components/ui.tsx`: `LoginHint` menyuruh memilih akun di menu Dev.
- Tabel `users (id, email UNIQUE, name, phone, created_at)` dan `org_members (org_id, user_id, role ∈ owner|admin|finance|rpc_staff)` di `backend/migrations/0001_init.up.sql`.
- Email dikirim lewat `backend/internal/notify/mail.go` (`Mailer.Send`, SMTP dari `.env`; development memakai Mailpit http://localhost:8025 atau Gmail bila diisi).
- Redis tersedia (`platform.NewRedis`), tes Redis memakai `miniredis` (lihat `internal/checkout/inventory_test.go`).
- CORS di `backend/internal/server/router.go` (`cors(origin)`), origin = `FRONTEND_URL`.

## Tujuan

Pengguna sungguhan bisa **daftar & masuk dengan email + kode OTP 6 digit**, tetap masuk walau browser ditutup, dan keluar. Mode Dev tetap ada untuk development, tetapi **mustahil dipakai** di luar development.

## Keputusan desain (ikuti, jangan diganti tanpa alasan kuat)

1. **Tanpa password.** Alur: isi email → kode 6 digit dikirim ke email → isi kode → masuk. Akun dibuat otomatis saat verifikasi pertama kali (daftar = masuk).
2. **Sesi = token acak di cookie HttpOnly**, bukan JWT di localStorage.
   - Token 32 byte acak (`crypto/rand`), dikirim sebagai cookie `racego_session`; di DB hanya disimpan **SHA-256** token-nya.
   - Atribut cookie: `HttpOnly`, `Path=/`, `SameSite=Lax`, `Max-Age` 30 hari, `Secure` **hanya** bila `APP_ENV != development` (localhost pakai http). Jangan set `Domain` (host-only).
   - `localhost:3000` dan `localhost:8080` dianggap satu *site* oleh browser, begitu juga `racego.example` dan `api.racego.example` nanti, jadi cookie SameSite=Lax tetap terkirim lewat `fetch(..., { credentials: "include" })`.
3. **Kode OTP disimpan di Redis**, bukan Postgres: key `otp:{email}` berisi hash SHA-256 dari `kode + email`, jumlah percobaan, TTL **10 menit**. Bandingkan dengan `subtle.ConstantTimeCompare`. Maks **5** percobaan salah, lalu kode hangus. Setelah berhasil, kode langsung dihapus (sekali pakai).
4. **Rate limit (Redis INCR + EXPIRE):** kirim ulang kode ke email yang sama paling cepat 60 detik sekali dan maks 5 kali/jam; per IP maks 20 permintaan kode/jam. Bila kena limit, balas 429 `{"error":"too_many_requests","message":"…","retry_after":<detik>}`.
5. **Email dinormalisasi**: `strings.ToLower(strings.TrimSpace(email))`, validasi dengan `net/mail`. Respons `request` selalu sukses untuk email valid (tidak membocorkan apakah akun sudah ada).
6. **CSRF**: selain SameSite=Lax, untuk request yang mengubah data (`POST/PUT/PATCH/DELETE`) yang diautentikasi lewat **cookie**, tolak bila header `Origin` ada dan tidak sama dengan `FRONTEND_URL` (403 `bad_origin`). Webhook Midtrans tidak memakai cookie, jadi tidak terpengaruh.
7. **Prioritas identitas di middleware**: cookie sesi yang valid → dipakai. Bila tidak ada **dan** `APP_ENV=development` → boleh `X-User-ID`. Di luar development, `X-User-ID` diabaikan total (sudah begitu; pertahankan dan uji).
8. **Kode OTP dikirim langsung dari API** (timeout 10 detik) memakai `notify.Mailer` yang sama dengan worker. Kalau SMTP gagal, balas 502 `mail_failed` dengan pesan ramah dan hapus OTP-nya. Di development, **log kodenya ke stdout** (`slog.Info("kode OTP (dev)", ...)`) supaya bisa login tanpa membuka email; jangan pernah log kode di luar development.

## Backend

### Migrasi `backend/migrations/0003_auth.up.sql` + `.down.sql`

```sql
CREATE TABLE sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash    bytea NOT NULL UNIQUE,
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    user_agent    text,
    ip            text
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
```

Cek cara `cmd/migrate` mendeteksi file migrasi dan ikuti polanya. Jangan ubah `0001`/`0002`.

### Paket `backend/internal/auth`

Pecah jadi beberapa file yang rapi, misalnya:

- `otp.go`: `OTPStore` (Redis) dengan `Issue(ctx, email) (code string, err)`, `Verify(ctx, email, code) error`; error bernama: `ErrOTPInvalid`, `ErrOTPExpired`, `ErrTooManyAttempts`, `ErrRateLimited{RetryAfter}`. Kode dibuat dengan `crypto/rand` (000000–999999, selalu 6 digit).
- `session.go`: `SessionStore` (Postgres): `Create(ctx, userID, ua, ip) (token string, expires time.Time, err)`, `Lookup(ctx, token) (userID, err)` (abaikan yang kedaluwarsa; update `last_seen_at` paling sering sekali per 5 menit), `Delete(ctx, token)`. Helper `setCookie(w, token, expires, secure)` dan `clearCookie(w, secure)`.
- `handler.go`: handler HTTP (lihat endpoint di bawah).
- `auth.go`: `Middleware` diubah sesuai keputusan no. 7, menerima dependensi yang dibutuhkan (mis. `Middleware(sessions *SessionStore, devMode bool)`). `Require`, `UserID`, `WithUser` tetap dengan signature yang sama supaya handler lain tidak perlu diubah.

### Endpoint baru

| Method | Path | Body | Respons |
| --- | --- | --- | --- |
| POST | `/v1/auth/otp/request` | `{"email"}` | 200 `{"ok":true,"resend_after":60}` · 422 `invalid_email` · 429 · 502 `mail_failed` |
| POST | `/v1/auth/otp/verify` | `{"email","code"}` | 200 + set cookie + body sama dengan `GET /v1/me` ditambah `"is_new": bool` · 400 `otp_invalid` (sertakan sisa percobaan) · 410 `otp_expired` · 429 `too_many_attempts` |
| POST | `/v1/auth/logout` | – | 204, hapus sesi di DB & cookie (tetap 204 walau belum login) |
| PATCH | `/v1/me` | `{"name","phone"}` | 200 profil terbaru · 422 bila nama < 2 atau > 100 karakter, atau HP bukan format Indonesia (`^(\+62|62|0)8[0-9]{7,12}$`, simpan dinormalisasi ke `08…`) |

- User baru dibuat dengan `name` = bagian sebelum `@` di email (huruf pertama kapital), `is_new: true`, supaya frontend mengarahkan ke halaman lengkapi profil.
- Pasang route `/v1/auth/*` di grup publik `router.go`; `PATCH /v1/me` di grup `auth.Require`.
- `GET /v1/me` tambahkan `phone`.
- CORS: tambah `Access-Control-Allow-Credentials: true` (origin tetap persis `FRONTEND_URL`, jangan `*`).
- Email OTP: subjek `Kode masuk RaceGo: 123456`, isi HTML sederhana seperti `notify.TicketEmail` (kode besar, berlaku 10 menit, "abaikan bila bukan kamu"). Taruh pembuat isi email di `internal/notify`.

### Memberi peran mitra ke akun sungguhan

Akun baru hanyalah pelari. Tambahkan perintah agar pemilik repo bisa menjadikan akunnya owner/staf sebuah penyelenggara:

- Subcommand baru `go run ./cmd/migrate grant <email> <org-slug> <role>`: user harus sudah ada (pernah login), org harus ada, role valid; upsert ke `org_members`; cetak hasilnya.
- Target Makefile `grant` (tambah juga ke `.PHONY` dan `## komentar` untuk `make help`):
  `make grant EMAIL=gavinadlan@gmail.com ORG=jakarta-runners ROLE=owner`

Catatan: Makefile di repo ini ditulis dengan **tab**; pertahankan.

### Tes (wajib, `go test ./...` harus hijau)

- `otp_test.go` dengan miniredis: kode 6 digit; verifikasi benar sekali lalu kode hangus; salah 5× → `ErrTooManyAttempts`; kedaluwarsa (miniredis `FastForward`) → `ErrOTPExpired`; rate limit kirim ulang < 60 detik → `ErrRateLimited`; email beda huruf besar/kecil dianggap sama.
- `auth_test.go` untuk middleware memakai `httptest`: cookie valid → user terisi; cookie tidak dikenal → anonim; `X-User-ID` diterima hanya saat `devMode=true`; cookie mengalahkan `X-User-ID`; POST dengan cookie + `Origin` asing → 403.
- Tes `SessionStore` yang butuh Postgres boleh di-skip bila env `RACEGO_TEST_DATABASE_URL` kosong (ikuti pola `internal/payment/status_test.go`).

## Frontend

### `lib/api.ts`

- `apiFetch` selalu `credentials: "include"`.
- `X-User-ID` hanya dikirim bila **`NEXT_PUBLIC_DEV_LOGIN === "true"`** dan ada dev user di localStorage.
- Tambah fungsi `requestOtp(email)`, `verifyOtp(email, code)`, `logout()`, `updateMe({name, phone})`, dan tipe `Me` diberi `phone`.

### `components/session.tsx`

- Sumber kebenaran sekarang `GET /v1/me` (cookie). `ready` = request `/v1/me` pertama sudah selesai (sukses atau 401). `userId` = `me?.id ?? null`.
- Sediakan `refresh()` (dipanggil setelah login/logout/ubah profil) dan `logout()`.
- `switchUser` (Dev) tetap ada hanya bila dev login aktif; memilih akun contoh **memanggil logout dulu** supaya tidak ada dua identitas; login sungguhan menghapus dev user dari localStorage.
- Pertahankan nama field yang dipakai komponen lain (`ready`, `userId`, `me`) agar perubahan di komponen lain minimal. Jangan memanggil `setState` sinkron di dalam `useEffect` (aturan lint `react-hooks/set-state-in-effect` aktif); pola yang sudah dipakai: `useSyncExternalStore` dan `lib/use-api.ts`.

### Halaman baru

- **`/masuk`** (`app/masuk/page.tsx` tipis + komponen klien dalam `<Suspense>` karena membaca `useSearchParams`):
  - Langkah 1: input email (`type="email"`, `autoComplete="email"`), tombol "Kirim kode".
  - Langkah 2: "Kode dikirim ke **email**", satu input 6 digit (`inputMode="numeric"`, `autoComplete="one-time-code"`, `maxLength=6`, kirim otomatis saat 6 digit terisi), tombol "Masuk", tautan "Ganti email", tombol "Kirim ulang" dengan hitung mundur 60 detik dari `resend_after`.
  - Pesan error dari backend ditampilkan apa adanya (sudah Bahasa Indonesia), termasuk sisa percobaan.
  - Setelah sukses: bila `is_new` → `/akun?baru=1&next=…`, selain itu → `next` (default `/`). **Validasi `next`**: hanya path relatif yang diawali `/` dan bukan `//`, untuk mencegah open redirect.
  - Bila sudah login dan membuka `/masuk`, langsung arahkan ke `next`.
- **`/akun`**: tampilkan email (tidak bisa diubah), ubah nama & no. HP, daftar peran mitra (dari `me.orgs`, tautan ke `/org/{slug}`), tombol Keluar. Dengan `?baru=1`, tampilkan judul "Lengkapi profil" dan tombol "Simpan & lanjut" yang menuju `next`.

### Komponen yang diubah

- **Header**: belum login → tombol "Masuk" (ke `/masuk?next=<path sekarang>`). Sudah login → tombol inisial nama (pakai `initials()` dari `lib/format.ts`) yang membuka menu: Tiket saya, satu baris per mitra di `me.orgs` ("Dashboard {nama}"), Akun, Keluar. Menu harus bisa dipakai keyboard dan tertutup saat klik di luar/Escape. Pemilih Dev hanya dirender bila `NEXT_PUBLIC_DEV_LOGIN === "true"`, dan diberi label jelas "Akun contoh (dev)".
- **`LoginHint`**: ganti teksnya menjadi ajakan masuk dengan tombol ke `/masuk?next=<path sekarang>`.
- **Detail event**: klik "Daftar" saat belum login → arahkan ke `/masuk?next=/events/{id}` (jangan reservasi dulu). Pastikan prefill peserta 1 di `CheckoutForm` tetap memakai `me.name`, `me.email`, dan sekarang juga bisa memakai `me.phone` bila relevan.
- Semua teks UI dalam Bahasa Indonesia, mengikuti gaya visual yang ada (`globals.css`, `components/ui.tsx`: `btnPrimary`, `inputCls`, dst.). Lihat bagian "Bahasa visual" di `frontend/README.md`.

### Env

- `.env.example`: tambah `NEXT_PUBLIC_DEV_LOGIN=true` dengan komentar "isi false / hapus saat deploy". Tambahkan juga ke `.env` lokal bila belum ada (file ini diabaikan Git; jangan ubah isi lain).
- Backend tidak perlu env baru; jangan menambah secret.

## Dokumentasi

Perbarui: `docs/07-api.md` (endpoint auth + `PATCH /v1/me` + catatan cookie), `docs/02-arsitektur.md` (bagian auth/sesi), `docs/08-development.md` (cara login di laptop: kode muncul di log API & Mailpit/Gmail; `make grant`), `README.md` (tabel "Coba ini" dan Status: login sudah jalan), `frontend/README.md` (halaman `/masuk`, `/akun`), `docs/09-roadmap.md` (centang login email OTP; login Google tetap belum).

## Di luar cakupan

Login Google, verifikasi nomor HP, halaman daftar mitra, hapus akun, "keluar dari semua perangkat". Jangan dikerjakan sekarang.

## Kriteria selesai (cek satu per satu dan laporkan)

1. `make test` hijau (tes Go + lint + type-check frontend), dan `cd frontend && npm run build` sukses.
2. `make db-reset` lalu `make dev`; di browser biasa (bukan Dev): buka `/`, klik "Masuk", isi email asli, kode diterima (log API / Mailpit / Gmail), masuk, diarahkan ke "Lengkapi profil", simpan, kembali ke halaman asal.
3. Tutup lalu buka lagi browser → masih masuk. Klik Keluar → `/tickets` menampilkan ajakan masuk.
4. Alur beli: belum login → klik Daftar di event → `/masuk` → setelah login kembali ke event yang sama → reservasi → bayar → tiket muncul di Tiket saya.
5. Kode salah 5× → kode hangus dengan pesan yang jelas; minta kode lagi < 60 detik → ditolak dengan sisa waktu.
6. `make grant EMAIL=<email kamu> ORG=jakarta-runners ROLE=rpc_staff` → menu header menampilkan "Dashboard Jakarta Runners" dan scanner bisa dibuka.
7. Dengan `APP_ENV=production` (tes cepat lewat `curl` ke API), header `X-User-ID` tidak memberi akses (`GET /v1/me` → 401).
8. `curl -X POST` dengan cookie sesi valid dan `Origin: https://evil.example` ke endpoint yang mengubah data → 403.
9. Tidak ada error/warning baru di konsol browser dan di log `make dev`.

## Cara kerja

- Kerjakan bertahap: migrasi → paket auth + tes → endpoint → frontend → dokumentasi. Jalankan tes setelah tiap tahap.
- Jangan mengubah perilaku checkout, pembayaran, tiket, atau scanner selain yang disebut di atas.
- Ikuti konvensi di `docs/08-development.md`: identifier bahasa Inggris, teks untuk pengguna Bahasa Indonesia, uang `int64`, handler tipis.
- Di akhir: tampilkan ringkasan perubahan per file, hasil tiap kriteria selesai, dan usulan pesan commit. **Jangan push** ke GitHub.
