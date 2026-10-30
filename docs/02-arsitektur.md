# 02 · Arsitektur

Modular monolith: satu codebase Go yang di-deploy sebagai dua proses (`api` dan `worker`), dengan PostgreSQL sebagai sumber kebenaran dan Redis untuk kuota yang diperebutkan.

```mermaid
flowchart TB
  subgraph Klien
    R[Pelari] & M[Mitra / admin] & S[Staf RPC]
  end
  CF[Cloudflare: CDN, WAF, Turnstile, rate limit]
  WEB[Next.js]
  API[API Go<br/>event · checkout · webhook · tiket · mitra]
  WK[Worker Go<br/>outbox · job berkala]
  PG[(PostgreSQL<br/>order, tiket, ledger, outbox)]
  RD[(Redis<br/>kuota & hold - Lua)]
  MT[Midtrans Snap]
  ML[SMTP: Mailpit / Resend]

  R & M & S --> CF --> WEB
  CF --> API
  API --> PG
  API --> RD
  API <--> MT
  MT -- webhook --> API
  WK --> PG
  WK --> RD
  WK --> ML
```

## Modul backend (`backend/internal/`)

| Modul | Tanggung jawab |
| --- | --- |
| `checkout` | Kuota di Redis (skrip Lua `reserve` / `release` / `confirm`), reservasi, pembuatan order |
| `queue` | Antrian virtual per event: lobi, urutan acak, pemanggilan (Lua `join` / `status` / `admit` / `release`), pass HMAC yang diperiksa `checkout` lewat antarmuka `QueueGate` |
| `ratelimit` | Pembatas INCR + EXPIRE di Redis, dipakai OTP dan antrian |
| `payment` | Snap client (HTTP & mock), verifikasi & pemrosesan webhook Midtrans |
| `ticket` | Tanda tangan QR Ed25519, penerbitan tiket + BIB, scan race pack |
| `ledger` | Jurnal double-entry (append-only, dijaga trigger DB), fee platform, estimasi biaya PG, saldo mitra dari entri, pemeriksaan integritas |
| `finance` | Keuangan mitra: rilis saldo setelah masa tahan, pencairan maker-checker lewat `Disburser` (mock / Midtrans Payouts Iris), refund per order lewat `Refunder` (mock / Core API), laporan & CSV. Bergantung pada `ledger`, `notify`, `secretbox`; dipakai `server` dan `worker` |
| `tenant` | Middleware multi-tenant: slug mitra → `org_id` + cek peran |
| `event` | Katalog publik (hanya event terbit dari mitra terverifikasi), ringkasan penjualan mitra |
| `partner` | Portal mitra: daftar penyelenggara, profil, CRUD event & kategori, terbitkan/tutup, anggota tim, audit |
| `upload` | Validasi & simpan gambar (poster, logo) di balik antarmuka `Storage` (sekarang disk lokal) |
| `worker` | Memproses outbox dan job berkala (muat stok, kembalikan slot, expire order, rilis saldo tiap jam, eksekusi & cek status pencairan/refund tiap 15 detik), dan pemanggilan antrian tiap detik di goroutine terpisah |
| `auth` | Login email + OTP, sesi cookie, profil `/me`, middleware identitas & CSRF |
| `notify` | Email tiket & kode masuk (SMTP, batas waktu 10 detik) |

Aturan dependensi: modul boleh saling impor ke arah "bawah" saja (`checkout` → `ledger`, `payment`), tidak melingkar. Ini membuat modul bisa dipecah jadi layanan terpisah nanti tanpa menulis ulang.

## Alur data utama

1. **Reservasi** — API membaca info kategori (cache 3 detik), memeriksa pass antrian bila event memakai antrian, lalu satu skrip Lua di Redis mengurangi stok dan membuat hold ber-timer.
2. **Order** — reservasi + data peserta disimpan sebagai order `pending` di Postgres (idempotent lewat header `Idempotency-Key`), lalu token Snap dibuat.
3. **Webhook** — Midtrans memanggil `/v1/webhooks/midtrans`. Signature diverifikasi, event dicatat unik per `(order, status)`, status order hanya bergerak maju, dan pesan `order.paid` ditulis ke tabel `outbox` **dalam transaksi yang sama**.
4. **Worker** — mengambil pesan outbox (`FOR UPDATE SKIP LOCKED`), mengunci slot di Redis, menerbitkan tiket, menulis jurnal ledger, lalu mengantrekan email. Setiap langkah idempotent; gagal → retry dengan backoff.

## Antrian virtual

Opsional per event (`events.waiting_room`). Pembeli login masuk lobi `QUEUE_LOBBY_MINUTES` sebelum penjualan; pada jam buka urutan lobi = skor acak yang diberikan saat bergabung. Worker memanggil peserta tiap detik (laju & batas pass aktif) dan membuang yang berhenti polling. Yang dipanggil mendapat pass HMAC; `checkout.Reserve` mewajibkan header `X-Queue-Pass` untuk event itu dan melepas pass setelah reservasi berhasil. Semua status antrian ada di Redis (hash tag `{ev:<id>}`), tanpa query Postgres per polling (info event di-cache 3 detik di API, daftar event aktif dibaca worker tiap 10 detik). Detail, batas, dan hasil uji: [03-war-tiket](03-war-tiket.md#antrian-virtual-waiting-room).

## Login & sesi

- **Tanpa password**: email → kode 6 digit dikirim langsung dari API (`notify.Mailer`) → verifikasi → masuk. Akun dibuat otomatis saat verifikasi pertama.
- **Kode OTP di Redis**: key `otp:{email}` berisi SHA-256 dari `kode + email` dan jumlah percobaan salah, TTL 10 menit, dibandingkan dengan `subtle.ConstantTimeCompare`, hangus setelah 5× salah atau sekali berhasil. Batas kirim (INCR + EXPIRE): 60 detik antar kirim & 5/jam per email, 20/jam per IP.
- **Sesi di Postgres** (tabel `sessions`): token acak 32 byte hanya ada di cookie `racego_session` (HttpOnly, `SameSite=Lax`, host-only, 30 hari, `Secure` di luar development); DB hanya menyimpan SHA-256-nya. `last_seen_at` diperbarui paling sering 5 menit sekali. Bukan JWT: sesi bisa dicabut seketika (logout menghapus barisnya).
- **Middleware** (`auth.Middleware`): cookie sesi valid → user; bila tidak ada dan `APP_ENV=development` → header `X-User-ID` (akun contoh). Di luar development header itu diabaikan.
- **CSRF**: `SameSite=Lax` + tolak request yang mengubah data bila `Origin` bukan `FRONTEND_URL` (403 `bad_origin`).
- **Peran mitra**: pendaftar penyelenggara otomatis `owner`; owner menambah anggota lain dari tab **Tim**. `make grant` tetap ada untuk keperluan development.

## Portal mitra: penerbitan event & stok

- **Status event**: `draft` → `published` → `closed`. Hanya `published`/`closed` dari mitra `verified` yang tampil publik; draft 404 di semua endpoint publik. `closed` tetap tampil (arsip & pemegang tiket) tetapi reservasi dan order ditolak.
- **Verifikasi**: org baru `pending` (atau langsung `verified` bila `AUTO_VERIFY_ORGS=true`, default di development). `pending`/`rejected` boleh membuat draft tapi tidak boleh menerbitkan. Alur pengajuan & peninjauan: [Verifikasi mitra](#verifikasi-mitra-kyc). `make verify ORG=slug` tetap ada sebagai jalan pintas darurat/dev.
- **Terbitkan** (`POST …/publish`): cek org terverifikasi, minimal 1 kategori, validasi ulang semua kolom, penjualan belum lewat; lalu status jadi `published` dan **stok semua kategori langsung dimuat ke Redis** (`checkout.SyncCategory`, SETNX) tanpa menunggu worker. Worker tetap menyinkronkan tiap 30 detik sebagai cadangan.
- **Setelah ada penjualan** (order selain `cancelled`/`expired`): kode, harga, jarak, prefix BIB terkunci. Kuota boleh berubah selama tidak di bawah slot terpakai. Perubahan kuota menyesuaikan stok Redis dengan **selisih** lewat skrip Lua `adjust.lua` yang atomik (cek `stok + selisih >= 0`), bukan menimpa angka absolut, karena pembelian bisa sedang berjalan. Penyesuaian Redis dilakukan tepat sebelum commit; bila commit gagal, selisihnya dibatalkan.
- **Hapus**: hanya draft / kategori tanpa order. Event berpenjualan hanya bisa **ditutup** (refund belum ada).
- **Audit**: org dibuat/diubah, event dibuat/diubah/terbit/kembali ke draft/ditutup/dihapus, kategori dibuat/diubah/dihapus, anggota ditambah/diubah/dihapus → `audit_logs` (before/after JSON).
- **Batas**: 3 org per akun pembuat (429 `too_many_orgs`), 20 draft per org (422 `too_many_drafts`).

## Verifikasi mitra (KYC)

Paket `internal/kyc` (+ `internal/secretbox`). Owner mengisi data di `/org/{slug}/verifikasi`, super admin meninjau di `/admin`.

```mermaid
stateDiagram-v2
  [*] --> draft: PUT pertama (attempt 1)
  draft --> submitted: submit (lengkap + persetujuan)
  submitted --> approved: super admin setujui
  submitted --> rejected: super admin tolak (alasan)
  rejected --> draft: ajukan ulang (attempt+1, salin isian & dokumen)
  approved --> [*]
```

- **Model data** (migrasi `0006`): `users.is_super_admin`; `org_verifications` satu baris per pengajuan (`UNIQUE (org_id, attempt)` + indeks unik parsial: satu `draft|submitted` per org); `org_documents` (`UNIQUE (verification_id, kind)`, `storage_key`, nama asli tersanitasi, `content_type` hasil validasi, `size`, `sha256`). `organizations.kyc_status` tetap sumber kebenaran penguncian: `approved` → `verified`, `rejected` → `rejected`, selain itu `pending`. Approve/reject, update org, audit, dan antrean email terjadi dalam **satu transaksi**.
- **Matriks dokumen** per jenis (`perorangan`, `komunitas`, `badan_usaha`) hanya ada di `kyc.Types`; frontend menerimanya lewat `requirements` di respons API.
- **Enkripsi**: `secretbox` = AES-256-GCM, nonce acak per data, keluaran berawalan versi kunci `v1:` (biner untuk berkas, `v1:`+base64 untuk kolom). Yang dienkripsi: seluruh isi dokumen, NIK PIC, nomor rekening, nomor NPWP (`*_enc`); `*_last4` untuk tampilan tersamar. Kunci dari `KYC_ENC_KEY` (base64, 32 byte); kosong di development = kunci bawaan + peringatan log, di luar development API menolak start. Hanya service `api` yang memegang kunci. Kunci salah/data diubah → `ErrDecrypt` (GCM), API membalas 500 `decrypt_failed`, tidak pernah data ngawur. Kunci tidak ikut backup; lihat [10-deploy.md](10-deploy.md#6-backup-dan-restore).
- **Penyimpanan privat**: `KYC_DIR` (default `backend/data/kyc`, volume `kyc` di produksi; API menolak start bila berada di dalam `UPLOAD_DIR`). Nama berkas = UUID acak + `.bin`. Tidak ada rute statis: berkas hanya lewat endpoint ber-otorisasi yang mendekripsi (≤ 5 MB di memori, karena GCM baru bisa memastikan keaslian setelah semua byte terbaca) lalu mengirim dengan `nosniff`, `Cache-Control: private, no-store`, `Content-Security-Policy: sandbox; default-src 'none'`. Caddy mengizinkan hanya rute dokumen dibingkai origin sendiri (`X-Frame-Options: SAMEORIGIN`; selain itu `DENY`) untuk pratinjau PDF. Pengajuan ulang menyalin baris dokumen (berkas sama); berkas fisik dihapus hanya bila tak ada baris lain yang merujuk.
- **Validasi unggahan**: tipe dari isi (`http.DetectContentType`), JPEG/PNG di-decode & encode ulang lewat `upload.Reencode` (EXIF/GPS hilang), PDF disimpan apa adanya tanpa diproses: wajib `%PDF-` dan `%%EOF` di akhir, ditolak bila memuat `/Encrypt`, `/JavaScript`, `/JS`, `/Launch` (nama ber-escape `#xx` dinormalkan; isi stream terkompresi tidak diperiksa, pertahanannya CSP sandbox). SHA-256 isi disimpan untuk menandai berkas identik antar-org.
- **Peninjauan**: hanya `submitted` yang bisa diputuskan; alasan tolak 10–1.000 karakter; super admin anggota org → 403 `conflict_of_interest`. Kecocokan nama pemilik rekening (huruf kecil, tanpa tanda baca/gelar umum) hanya petunjuk hijau/kuning.
- **Akses tercatat** di `audit_logs` (`entity = org_verification`, `after` berisi `kind`/`field`/`by`/`ip`, tanpa nilai sensitif): `kyc.document.upload`, `kyc.document.view` (owner & super admin), `kyc.field.reveal`, `kyc.submitted`, `kyc.reopened`, `kyc.approved`, `kyc.rejected`.
- **Email** lewat outbox topik `kyc.email` (satu pesan per owner, retry/backoff worker): diterima, disetujui, ditolak + alasan. Isinya hanya nama org, status, alasan.
- **Super admin** hanya dari `cmd/migrate superadmin <email>`. `/v1/admin/*` dan halaman `/admin` membalas 404 untuk selain super admin.

Ini ringkasan teknis, bukan nasihat hukum; kepatuhan terhadap peraturan perlindungan data belum ditinjau.

## Keuangan mitra

Rincian & tabel jurnal: [04 · Pembayaran, ledger, dan pencairan](04-pembayaran-dan-ledger.md).

- Setiap perubahan uang = satu jurnal di transaksi yang sama dengan perubahan status; `(ref_type, ref_id)` deterministik → idempotent. Saldo dihitung dari `ledger_entries`, tidak ada kolom saldo.
- Pengajuan pencairan mengunci baris org (`SELECT … FOR UPDATE`), plus indeks unik "satu pencairan aktif per org". Persetujuan mengunci baris payout; klik ganda → 409 `invalid_state`.
- Gateway hanya dipanggil worker, **di luar** transaksi: klaim baris (sewa 2 menit) → panggil → catat hasil di transaksi baru. Kunci idempoten ke gateway = id payout/refund, jadi pengulangan setelah crash aman.
- Rekening pencairan disalin (ciphertext) dari pengajuan verifikasi yang disetujui; nomor lengkap hanya dibuka worker tepat sebelum dikirim ke gateway dan saat super admin menekan "Tampilkan" (tercatat). Karena itu worker juga memegang `KYC_ENC_KEY`.

## Penyimpanan gambar

- `POST /v1/org/{org}/uploads` (owner/admin) menerima JPEG/PNG maks 3 MB, sisi terpanjang maks 4.000 px. Tipe ditentukan dari **isi file** (`http.DetectContentType` + `image.DecodeConfig`/`Decode`), lalu gambar di-**encode ulang** sehingga EXIF/GPS hilang.
- Nama file = SHA-256 isi + `.jpg`/`.png`: tidak bisa ditebak, tidak menimpa file lain, dan aman di-cache `immutable`. DB menyimpan path relatif `/uploads/<hash>.jpg`.
- `upload.Storage` (sekarang `upload.Disk` di `UPLOAD_DIR`: default `backend/data/uploads`, di stack produksi volume `uploads` yang ikut `make backup`) bisa diganti S3/R2 tanpa mengubah handler. `GET /uploads/{name}` hanya menerima nama `^[a-f0-9]{64}\.(jpg|png)$` (tanpa path traversal). File yang tidak dipakai lagi belum dibersihkan otomatis.

## Deploy

Satu server, satu `docker-compose.prod.yml` (proyek `racego-prod`). Caddy satu-satunya pintu masuk dan satu origin untuk web + API, sehingga tidak ada CORS/cookie lintas domain. Langkah lengkap: [10-deploy.md](10-deploy.md).

```mermaid
flowchart LR
  B[Browser] -- "HTTPS :443 (atau via Cloudflare Tunnel)" --> C[caddy<br/>HSTS · gzip/zstd · body maks 6 MB]
  C -- "/v1/* · /uploads/* · /healthz" --> A[api]
  C -- selain itu --> W[web<br/>Next.js standalone]
  M[migrate<br/>sekali jalan, sukses dulu] --> P[(postgres<br/>vol pgdata)]
  A --> P
  A --> R[(redis appendonly<br/>vol redisdata)]
  A --- U[(vol uploads)]
  A --- KY[(vol kyc<br/>terenkripsi)]
  K[worker] --> P & R
```

- Image backend tunggal (distroless, non-root) berisi `api`, `worker`, `migrate`, `keygen`, dan `healthcheck` (probe Docker tanpa curl). Image web memakai `output: "standalone"` dan `NEXT_PUBLIC_API_URL=""`, sehingga tidak bergantung alamat situs.
- Hanya Caddy yang punya `ports:`. Redis wajib password, Postgres memakai user/password acak dari `scripts/gen-secrets.sh`.
- `api`/`worker` baru start setelah `migrate` sukses (`service_completed_successfully`). Setiap service punya healthcheck, `restart: unless-stopped`, log berotasi, batas memori, dan `read_only` di mana praktis.

## Multi-tenancy

- Satu database bersama; setiap tabel milik mitra punya kolom `org_id`.
- Semua route mitra berada di `/v1/org/{org}/...` dan melewati `tenant.RequireRole`, yang mengubah slug menjadi `org_id` dan memeriksa keanggotaan + peran.
- Handler **wajib** memakai `tenant.FromContext(ctx).OrgID` di setiap query, tidak pernah `org_id` dari body request.
- User yang bukan anggota mendapat 404 (bukan 403) agar keberadaan mitra lain tidak bocor.
- Pelari bersifat global (bukan milik satu mitra), sehingga satu akun bisa membeli di event mitra mana pun.

Rencana penguatan: Row-Level Security Postgres sebagai jaring pengaman kedua — lihat [ADR 0002](adr/0002-multi-tenant-shared-db.md).

## Skema data

Lihat `backend/migrations/0001_init.up.sql`. Tabel inti: `organizations`, `org_members`, `org_verifications`, `org_documents` (0006), `events`, `ticket_categories`, `orders`, `participants`, `tickets`, `payment_events`, `outbox`, `ledger_journals`, `ledger_entries`, `payout_accounts`, `payouts`, `refunds` (0007), `scan_logs`, `audit_logs`. Semua nominal uang disimpan sebagai `BIGINT` rupiah.
