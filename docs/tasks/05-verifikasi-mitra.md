# Tugas 05 · Verifikasi mitra (KYC) + super admin

> Prompt untuk AI coding agent (Claude Code CLI). Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/05-verifikasi-mitra.md sampai semua kriteria selesai terpenuhi"`
> Prasyarat: tugas 01–04 sudah ter-commit.

## Konteks

RaceGo: backend Go (`backend/`), frontend Next.js 16 (`frontend/`), Postgres, Redis. Baca dulu `README.md`, `docs/02-arsitektur.md`, `docs/01-prd.md` (peran "Super admin platform"), `docs/08-development.md`, `docs/10-deploy.md`, dan **`frontend/AGENTS.md`** (Next.js 16 berbeda dari versi lama; docs di `frontend/node_modules/next/dist/docs/`).

Keadaan sekarang: penyelenggara dibuat sendiri lewat `/daftar-mitra` (`internal/partner`). `organizations.kyc_status` (`pending|verified|rejected`) **mengunci penerbitan event**: org belum `verified` hanya boleh membuat draft. Namun belum ada cara memverifikasi dari web: di development `AUTO_VERIFY_ORGS=true` langsung memverifikasi, dan di produksi hanya ada perintah manual `make verify ORG=…` (`cmd/migrate verify`). **Tidak ada pengumpulan berkas, tidak ada peran super admin, tidak ada halaman peninjauan.** Tugas ini membuatnya.

Fondasi yang ada dan **dipakai, bukan dibuat ulang**:

- Login cookie (`auth.UserID(ctx)`, `auth.Require`), `GET /v1/auth/session`, `GET /v1/me`; multi-tenant `tenant.RequireRole` + `tenant.OrgID(ctx)` (non-anggota → 404); format error `httpx.Error`, validasi 422 dengan `fields` (`httpx.Validation`).
- Validasi dan encode ulang gambar di `backend/internal/upload/` (JPEG/PNG; poster/logo publik di `/uploads` **tidak boleh dipakai untuk dokumen KYC**).
- `audit_logs` (dipakai di `partner` dan `payment`), outbox + worker untuk email (`worker/worker.go`, `notify/mail.go`, pola topik `ticket.email` dengan retry/backoff), pembatas laju Redis `internal/ratelimit`.
- Portal mitra di `frontend/src/components/`: `org-shell.tsx` (tab Event/Tim/Profil), `org-dashboard.tsx`, `event-form.tsx` (dialog konfirmasi, `image-upload.tsx`), `ui.tsx` + `ui-client.tsx` (`Field` dengan error, `btnPrimary`, dst.), `lib/api.ts`.
- Backup/restore (`scripts/backup.sh`, `scripts/restore.sh`), `docker-compose.prod.yml`, `scripts/gen-secrets.sh`, `.env.example`, `.env.prod.example`.
- Teks pengguna Bahasa Indonesia, identifier Inggris. Aturan lint `react-hooks/set-state-in-effect` aktif; halaman yang memakai `useParams`/`useSearchParams` dibungkus `<Suspense>`.

## Tujuan

Penyelenggara baru **mengirim data identitas, dokumen, dan rekening bank** untuk ditinjau. **Super admin** meninjau di halaman `/admin`, lalu **menyetujui atau menolak (dengan alasan)**. Disetujui → `kyc_status='verified'` (bisa menerbitkan event). Ditolak → mitra memperbaiki dan mengajukan ulang. Dokumen dan data sensitif **tersimpan terenkripsi, privat, dan setiap aksesnya tercatat**.

## Keputusan desain (ikuti)

1. **Super admin.** Kolom `users.is_super_admin boolean NOT NULL DEFAULT false`. Hanya bisa diberikan lewat perintah (`go run ./cmd/migrate superadmin <email>`, target `make superadmin EMAIL=…` dan `make prod-superadmin EMAIL=…`; user harus sudah pernah login), **tidak ada endpoint web untuk menjadikan seseorang super admin**. Semua rute `/v1/admin/*` dan halaman `/admin` untuk non-super-admin membalas **404** (bukan 403). `GET /v1/auth/session` dan `/v1/me` menyertakan `is_super_admin`.
2. **Satu pengajuan aktif per org, riwayat tersimpan.** Setiap pengajuan = satu baris `org_verifications` dengan nomor `attempt`. Status: `draft` → `submitted` → `approved` | `rejected`. Setelah `rejected`, owner memanggil "Ajukan ulang" yang membuat pengajuan baru (`attempt+1`) berstatus `draft`, **menyalin isian dan dokumen** sebelumnya (baris dokumen menunjuk berkas terenkripsi yang sama; berkas fisik baru dihapus bila tidak ada lagi baris yang merujuknya). `organizations.kyc_status` tetap sumber kebenaran untuk penguncian: `approved` → `verified`; `rejected` → `rejected`; selain itu `pending` (jangan mengubah constraint lama bila tidak perlu).
3. **Jenis penyelenggara dan dokumen wajib.** Dukung tiga jenis, dengan matriks dokumen berikut (taruh di satu tempat di kode, dipakai backend dan dikirim ke frontend lewat endpoint, jangan menduplikasi di dua sisi):

   | Jenis | Wajib | Opsional |
   | --- | --- | --- |
   | `perorangan` | `ktp_pic` | `npwp` |
   | `komunitas` | `ktp_pic`, `surat_keterangan` (SK pengurus/surat pernyataan komunitas) | `npwp` |
   | `badan_usaha` (PT/CV/yayasan) | `ktp_pic`, `npwp`, `akta_pendirian`, `sk_kemenkumham` | `nib`, `surat_kuasa` |

   Satu jenis dokumen satu berkas (mengunggah lagi menggantikan). Maks 10 berkas per pengajuan.
4. **Data yang dikumpulkan:** jenis, nama sesuai dokumen (`legal_name`), alamat, nama + no. HP penanggung jawab (PIC), **NIK PIC** (16 digit, divalidasi format), nomor NPWP bila ada (15/16 digit), dan **rekening bank**: kode & nama bank (daftar tetap kecil di kode: BCA, Mandiri, BNI, BRI, BSI, CIMB Niaga, Permata, Danamon, Jago, SeaBank, blu; tambah "Lainnya" dengan input bebas), nomor rekening (6–20 digit), nama pemilik rekening. Aturan nama pemilik: untuk `perorangan` harus sama dengan nama PIC; `komunitas` nama PIC atau nama komunitas; `badan_usaha` nama badan usaha. **Tidak dipaksakan otomatis**: tampilkan penanda kecocokan nama (normalisasi huruf besar/kecil, tanda baca, gelar umum, spasi) kepada peninjau sebagai petunjuk hijau/kuning, bukan penentu.
5. **Enkripsi data sensitif.** Paket baru `internal/secretbox`: AES-256-GCM, nonce acak per data, format keluaran berawalan versi kunci (`v1:`) agar bisa diganti kunci nanti. Kunci dari env `KYC_ENC_KEY` (base64, 32 byte). Dienkripsi: **seluruh isi berkas dokumen**, **NIK PIC**, **nomor rekening**, **nomor NPWP**. Kolom DB menyimpan ciphertext (`*_enc`) plus `account_last4`/`id_last4` untuk tampilan tersamar. Di development, bila env kosong, pakai kunci bawaan **dengan peringatan log**; di luar development kunci kosong → API **menolak start** dengan pesan jelas. Kunci yang salah/berkas rusak harus menghasilkan error yang jelas (GCM mendeteksi perubahan), tidak panic dan tidak mengembalikan data ngawur. `scripts/gen-secrets.sh` ikut membuat `KYC_ENC_KEY` **tanpa pernah menimpa nilai yang sudah ada** (kehilangan/perubahan kunci = dokumen tak terbaca; beri peringatan keras di skrip dan dokumentasi, dan jelaskan bahwa kunci harus dicadangkan **terpisah** dari backup data).
6. **Penyimpanan berkas privat.** Folder `KYC_DIR` (default `./data/kyc`, di luar folder `UPLOAD_DIR`, di-ignore Git, volume sendiri di `docker-compose.prod.yml`, ikut skrip backup/restore). Nama berkas = UUID acak + `.bin`; nama asli dan tipe disimpan di DB (nama disanitasi saat ditampilkan). **Tidak ada rute statis**: berkas hanya bisa diambil lewat endpoint ber-otorisasi yang mendekripsi sambil mengalirkan. Header respons dokumen: `Content-Type` sesuai hasil validasi (bukan dari klien), `X-Content-Type-Options: nosniff`, `Cache-Control: private, no-store`, `Content-Security-Policy: sandbox; default-src 'none'`, `Content-Disposition: inline; filename*=` (nama sudah disanitasi).
7. **Validasi unggahan:** JPEG, PNG, atau PDF; maks **5 MB**; tipe ditentukan dari **isi** (`http.DetectContentType` + decode gambar; PDF: header `%PDF-` dan `%%EOF` di akhir), bukan ekstensi/header klien. Gambar di-encode ulang (membuang EXIF/GPS) memakai kode `internal/upload`; PDF disimpan apa adanya (tidak dieksekusi/diproses). Tolak PDF terenkripsi (`/Encrypt`) dan yang berisi `/JavaScript`/`/JS`/`/Launch` dengan pesan jelas. Hash SHA-256 isi disimpan untuk deteksi unggahan ganda antar-pengajuan.
8. **Aturan peninjauan:**
   - Hanya pengajuan `submitted` yang bisa disetujui/ditolak; penolakan wajib alasan 10–1.000 karakter.
   - **Benturan kepentingan:** super admin **tidak bisa** menyetujui/menolak pengajuan org yang dia sendiri anggotanya (403 `conflict_of_interest`).
   - Pengajuan tidak bisa diubah setelah `submitted` (owner hanya bisa membacanya); satu-satunya jalan keluar adalah ditolak lalu "Ajukan ulang".
   - Menyetujui menyetel `organizations.kyc_status='verified'`, mencatat `reviewed_by/at`; menolak menyetel `rejected`. Keduanya dalam satu transaksi dengan audit log.
9. **Persetujuan data.** Sebelum mengirim, owner wajib mencentang persetujuan pemrosesan data (teks ringkas dan jelas: data dipakai hanya untuk verifikasi dan pencairan dana, disimpan terenkripsi, hanya dilihat tim peninjau, dan dihapus/ dianonimkan sesuai kebijakan retensi yang belum ditentukan di versi ini). Simpan `consent_at` dan `consent_version`. **Jangan mengklaim kepatuhan hukum (mis. UU PDP)** di UI maupun dokumen; tulis bahwa ini ringkasan teknis dan bukan nasihat hukum.
10. **Akses tercatat.** Setiap unduhan/pratinjau dokumen oleh super admin, dan setiap "tampilkan data lengkap" (NIK/nomor rekening/NPWP; secara bawaan tampil tersamar: `••••1234`), menulis `audit_logs` (aksi `kyc.document.view`, `kyc.field.reveal`, dll. dengan user, pengajuan, jenis dokumen/kolom, waktu, IP). Akses oleh owner ke berkasnya sendiri juga dicatat (`kyc.document.view` oleh pemilik).
11. **Email** lewat outbox/worker (pola yang ada, dengan retry): ke owner saat pengajuan diterima, disetujui, dan ditolak (memuat alasan). Isi tidak boleh memuat data sensitif (NIK/nomor rekening), hanya nama org, status, dan alasan.
12. **`AUTO_VERIFY_ORGS=true` (development) tetap ada** dan memotong alur ini (org langsung `verified`, tab Verifikasi menampilkan "Diverifikasi otomatis (mode development)"). Untuk menguji alur sungguhan, set `false`. Data seed (org contoh) sudah `verified` dan dibiarkan.
13. **Ketersediaan di UI:** `badan_usaha` dan `komunitas`/`perorangan` ditampilkan sebagai pilihan kartu yang jelas, dengan daftar dokumen yang dibutuhkan muncul saat memilih.

## Backend

### Migrasi `0006_org_verification.up.sql` + `.down.sql` (jangan ubah migrasi lama)

- `users.is_super_admin`.
- `org_verifications`: `id`, `org_id`, `attempt`, `status` (CHECK), `org_type`, `legal_name`, `address`, `pic_name`, `pic_phone`, `pic_id_enc`, `pic_id_last4`, `npwp_enc`, `npwp_last4`, `bank_code`, `bank_name`, `account_holder`, `account_enc`, `account_last4`, `consent_at`, `consent_version`, `submitted_at`, `reviewed_at`, `reviewed_by`, `reject_reason`, `created_at`, `updated_at`; `UNIQUE (org_id, attempt)`; indeks unik parsial agar satu org hanya punya satu baris `draft|submitted`.
- `org_documents`: `id`, `verification_id`, `kind` (CHECK daftar jenis), `storage_key`, `original_name`, `content_type`, `size`, `sha256`, `created_at`; `UNIQUE (verification_id, kind)`.
- Pastikan pengiriman email ke owner bisa mengetahui penerima (join `org_members.role='owner'`).

### Paket

- `internal/secretbox/` (enkripsi + tes), `internal/kyc/` (store berkas terenkripsi, validasi dokumen, matriks dokumen, aturan status, kecocokan nama, handler mitra dan admin, audit, email), integrasi di `server/router.go`, `config` (`KYC_ENC_KEY`, `KYC_DIR`), worker (topik email verifikasi), `cmd/migrate` (`superadmin`), `Makefile`.

### Endpoint

Mitra (`/v1/org/{org}/verification…`, **owner saja**; non-anggota 404; peran lain 404/403 sesuai pola `RequireRole`):

| Method | Path | Keterangan |
| --- | --- | --- |
| GET | `/verification` | pengajuan terkini + matriks dokumen yang dibutuhkan + status + alasan penolakan terakhir + ringkasan riwayat; data sensitif tersamar |
| PUT | `/verification` | simpan isian draft (validasi per kolom, 422 dengan `fields`); hanya saat `draft` |
| POST | `/verification/documents` | multipart `kind` + `file`; hanya saat `draft`; membatasi laju |
| DELETE | `/verification/documents/{kind}` | hanya saat `draft` |
| GET | `/verification/documents/{kind}` | unduh/pratinjau berkas sendiri (didekripsi), tercatat |
| POST | `/verification/submit` | validasi kelengkapan (semua isian, dokumen wajib per jenis, rekening, persetujuan) → `submitted`, email konfirmasi, audit |
| POST | `/verification/reopen` | hanya dari `rejected` → pengajuan baru `draft` (aturan no. 2) |

Super admin (`/v1/admin/…`, `auth.Require` + pemeriksaan `is_super_admin`; selain itu 404):

| Method | Path | Keterangan |
| --- | --- | --- |
| GET | `/verifications?status=&q=&page=` | daftar (default `submitted`, terlama dulu), dengan hitung per status, nama org, jenis, waktu kirim, attempt |
| GET | `/verifications/{id}` | detail: data tersamar, daftar dokumen, penanda kecocokan nama, riwayat pengajuan org itu, daftar anggota org (untuk transparansi) |
| POST | `/verifications/{id}/reveal` | `{"field":"pic_id"|"account"|"npwp"}` → nilai lengkap; tercatat |
| GET | `/verifications/{id}/documents/{kind}` | dokumen terdekripsi; tercatat |
| POST | `/verifications/{id}/approve` | aturan no. 8 |
| POST | `/verifications/{id}/reject` | `{"reason"}` |

### Tes backend (wajib, `go test ./...` hijau, tes Postgres/Redis **tidak** di-skip)

- `secretbox`: roundtrip, nonce berbeda tiap enkripsi, ciphertext diubah 1 byte → error, kunci salah → error jelas, versi kunci tidak dikenal → error.
- Validasi dokumen: file teks berekstensi `.pdf`/`.jpg`, PDF tanpa `%%EOF`, PDF ber-`/JavaScript`, PDF terenkripsi, > 5 MB, EXE yang diganti nama → semua ditolak; JPEG ber-EXIF-GPS → tersimpan tanpa EXIF; PDF valid → tersimpan apa adanya.
- **Tidak ada plaintext di penyimpanan:** setelah mengajukan, baca kolom `*_enc` mentah dan berkas di `KYC_DIR` lalu buktikan NIK, nomor rekening, NPWP, dan isi dokumen **tidak** muncul sebagai teks biasa (mis. berkas tidak diawali `%PDF-`/`\xff\xd8`/PNG magic).
- Mesin status: submit tanpa dokumen wajib/rekening/persetujuan ditolak per kolom; tidak bisa mengubah/mengunggah setelah `submitted`; approve → `verified` dan event bisa diterbitkan; reject → tetap terkunci, alasan tersimpan; reopen → pengajuan baru terisi salinan, dokumen salinan bisa didekripsi, menghapus dokumen di pengajuan baru tidak menghapus berkas pengajuan lama; matriks dokumen per jenis benar (3 jenis).
- Otorisasi (matriks lengkap): anonim → 401; pelari biasa ke `/admin/*` → 404; owner org A ke verifikasi org B → 404; admin/finance/rpc_staff org sendiri ke `/verification` → ditolak; super admin ke endpoint mitra → ditolak; super admin yang anggota org itu → `conflict_of_interest` pada approve/reject; non-super-admin tidak bisa mengambil dokumen/`reveal` meski tahu ID; dokumen org lain tidak bisa diunduh lewat `kind` di org sendiri.
- Audit: unduhan dokumen, `reveal`, approve, reject, submit semuanya tercatat dengan data yang benar; **audit/log aplikasi tidak memuat** nilai sensitif.
- Email: terkirim ke antrean outbox untuk submit/approve/reject dan **tidak memuat NIK/nomor rekening**.
- `AUTO_VERIFY_ORGS=true` tetap memverifikasi otomatis dan melewati alur.
- Pembatas laju unggahan bekerja.

## Frontend

Gaya visual dan komponen mengikuti yang ada. Form: label selalu terlihat (pola `Field`), tanda wajib, pesan error per kolom, tombol disabled saat menyimpan, isian tidak hilang saat error, nyaman di HP 390px.

**Mitra (`/org/[org]/verifikasi`, tab baru "Verifikasi" di `org-shell`, hanya owner; peran lain melihat status saja):**
- Banner status di atas dashboard dan tab: belum diajukan (ajakan), menunggu peninjauan, **ditolak (tampilkan alasan + tombol "Ajukan ulang")**, disetujui (lencana "Terverifikasi"). Dialog Terbitkan di form event yang gagal karena belum terverifikasi menautkan ke halaman ini.
- Langkah bertahap (stepper yang bisa dipakai keyboard): **1 Jenis & identitas** (kartu pilihan jenis + penjelasan dokumen yang dibutuhkan, data PIC), **2 Dokumen** (satu baris per jenis dokumen dengan status Wajib/Opsional/Terunggah, tombol unggah/ganti/hapus, pratinjau, progres, pesan error jelas, catatan "format JPG/PNG/PDF, maks 5 MB"), **3 Rekening** (bank, nomor, nama pemilik + petunjuk aturan kecocokan nama), **4 Tinjau & kirim** (ringkasan tersamar, centang persetujuan, tombol Kirim). Simpan otomatis ke draft saat pindah langkah; peringatan bila meninggalkan halaman dengan perubahan belum disimpan.
- Pemberitahuan privasi singkat di langkah dokumen: dokumen disimpan terenkripsi, hanya dilihat tim peninjau, aksesnya tercatat. Tulis juga: gunakan salinan dokumen yang memang boleh dibagikan; **jangan** menjanjikan kepatuhan hukum.

**Super admin (`/admin` dan `/admin/verifikasi/[id]`, hanya muncul bila `is_super_admin`; menu header "Admin"):**
- `/admin`: tab Menunggu / Disetujui / Ditolak dengan jumlah, pencarian nama org, daftar dengan jenis, waktu kirim, attempt.
- Detail: data pengajuan (tersamar + tombol "Tampilkan" per bidang), **penanda kecocokan nama rekening** (hijau/kuning dengan penjelasan), daftar anggota org, riwayat pengajuan, daftar dokumen dengan **pratinjau di halaman** (gambar langsung; PDF di `<iframe sandbox>`/`<object>` dari URL endpoint dengan cookie, tanpa membuka tab baru yang tidak sandbox), daftar periksa peninjau (tampilan saja, tidak disimpan), tombol **Setujui** dan **Tolak** (dialog yang bisa dipakai keyboard; alasan wajib dengan penghitung karakter). Tampilkan pesan jelas untuk `conflict_of_interest`.
- Semua halaman admin 404 untuk non-super-admin (jangan membocorkan keberadaan).
- `lib/api.ts`: tipe + fungsi; header menampilkan "Admin" hanya bila `me.is_super_admin`.

## Skrip, produksi, dan dokumen

- `docker-compose.prod.yml`: volume `kyc` untuk `api`, env `KYC_ENC_KEY`/`KYC_DIR`; `.env.example` dan `.env.prod.example` (komentar jelas tentang kunci); `gen-secrets.sh` (aturan no. 5); `scripts/backup.sh`/`restore.sh` mencakup volume `kyc` (terenkripsi) dan **tidak** mencadangkan kunci ke folder yang sama (dokumentasikan cara menyimpan kunci terpisah); `.gitignore` menambah `backend/data/kyc/`.
- Perbarui: `docs/07-api.md` (semua endpoint + kode error), `docs/02-arsitektur.md` (alur verifikasi, model data, enkripsi, penyimpanan privat), `docs/01-prd.md` (peran super admin, KYC), `docs/08-development.md` (`make superadmin`, cara menguji alur dengan `AUTO_VERIFY_ORGS=false`), `docs/10-deploy.md` (kunci, backup terpisah, `make prod-superadmin`), `docs/09-roadmap.md` (centang verifikasi mitra; tambah: 2FA untuk super admin, kebijakan retensi/penghapusan data, rotasi kunci, pencabutan/penangguhan org, verifikasi ulang saat ganti rekening, notifikasi super admin), `README.md`, `frontend/README.md`.

## Di luar cakupan

OCR/pengecekan otomatis dokumen, liveness/selfie, integrasi layanan KYC atau cek rekening (name inquiry), pencairan dana, ganti rekening setelah terverifikasi, pencabutan/penangguhan org, beberapa tingkat peran admin, 2FA, job penghapusan data, dokumen kebijakan privasi/legal, rotasi kunci. Jangan dikerjakan sekarang.

## Kriteria selesai (cek satu per satu dan laporkan dengan bukti nyata)

1. `make test` hijau (Go vet + tes dengan Postgres/Redis, lint, typegen, tsc), `cd frontend && npm run build` sukses, tes lama tidak diubah dan hijau. Semua tes baru di atas ada dan lulus (laporkan jumlah PASS dan 0 SKIP).
2. **Bukti enkripsi:** tunjukkan (tanpa mencetak data asli) bahwa kolom `*_enc` dan berkas di `KYC_DIR` tidak mengandung plaintext, dan bahwa mengganti `KYC_ENC_KEY` membuat dokumen gagal didekripsi dengan error jelas (bukan data rusak).
3. **Uji browser sungguhan** (Playwright di folder sementara di luar repo; jangan mengubah `package.json`; OTP dari Mailpit; override env di baris perintah dengan `AUTO_VERIFY_ORGS=false`, **jangan menyentuh `.env` pribadi**; semua dokumen uji dibuat skrip dan bertuliskan "CONTOH — TIDAK ASLI", jangan memakai data pribadi nyata):
   - Akun A daftar mitra jenis `badan_usaha` → tidak bisa menerbitkan event → mengisi 4 langkah, mengunggah dokumen wajib (JPEG, PNG, dan PDF) → mencoba kirim dengan dokumen kurang (ditolak per kolom) → kirim lengkap → status "menunggu".
   - Super admin (dibuat dengan `make superadmin`) melihat antrean, membuka detail, pratinjau tiap dokumen, "Tampilkan" NIK (tercatat), menolak dengan alasan → A menerima email dengan alasan, melihat alasan di banner, "Ajukan ulang", memperbaiki, kirim lagi → super admin menyetujui → A menerbitkan event.
   - Jenis `perorangan` dan `komunitas` juga dilewati sekali dengan dokumen yang sesuai matriks.
   - Kasus negatif lewat `curl`/browser: pelari biasa buka `/admin` dan `/v1/admin/...` → 404; owner org B ambil dokumen org A → 404; super admin yang anggota org tersebut → `conflict_of_interest`; ambil berkas dengan menebak path di `/uploads/` atau `KYC_DIR` → 404; unggah EXE bernama `.pdf` dan file > 5 MB → ditolak dengan pesan jelas.
   - Screenshot tiap langkah (stepper mitra, banner status, antrean admin, detail dengan pratinjau, dialog tolak/setuju) di layar laptop **dan** HP 390px: **lihat sendiri** dan perbaiki yang rusak; tanpa geser horizontal.
   - Konsol browser dan log API/worker bersih dari error/warning baru; log tidak memuat nilai sensitif.
4. Audit: tampilkan cuplikan `audit_logs` yang membuktikan submit, view dokumen, reveal, approve, reject tercatat (tanpa nilai sensitif).
5. Produksi lokal (`make prod-up` dengan `SITE_ADDRESS=localhost`, **tanpa** tunnel ke internet): `gen-secrets.sh` membuat `KYC_ENC_KEY` dan eksekusi kedua tidak menimpanya; API menolak start bila kosong; satu putaran alur verifikasi lewat Caddy/HTTPS berfungsi; **backup → hancurkan volume → restore** mengembalikan dokumen yang tetap bisa dibuka dengan kunci yang sama. Matikan dan bersihkan stack setelahnya.
6. `AUTO_VERIFY_ORGS=true` tetap memverifikasi otomatis di development dan tab Verifikasi menampilkan keterangannya.
7. Semua dokumen di atas diperbarui; `make help` menampilkan target baru; `git status` bersih dari berkas uji, `.env`, atau data (`backend/data/` tidak masuk repo).

## Cara kerja

- Bertahap, uji tiap tahap: migrasi + config → `secretbox` + tes → penyimpanan berkas + validasi + tes → mesin status + endpoint mitra + tes → endpoint admin + otorisasi + tes → email/audit → frontend mitra → frontend admin → skrip/produksi/backup → dokumentasi → uji browser dan produksi lokal.
- Jangan mengubah perilaku checkout, pembayaran, tiket, scanner, login, antrian, atau portal mitra selain yang disebut. Bug di luar cakupan: catat di laporan akhir, jangan diperbaiki diam-diam kecuali menghalangi tugas ini.
- Jangan pernah menulis data sensitif (NIK, nomor rekening, isi dokumen, kunci) ke log, pesan error, email, audit, screenshot, atau laporan akhir.
- Bila keputusan di dokumen ini bertabrakan dengan kode atau kenyataan teknis, pilih opsi paling sederhana yang aman dan catat alasannya.
- Tanpa rahasia di repo, tanpa email sungguhan, tanpa tunnel ke internet, tanpa menyentuh `.env` pribadi.
- Di akhir: ringkasan perubahan per file, tabel hasil tiap kriteria (berdasarkan uji nyata; tandai yang tidak bisa diuji), keputusan/penyimpangan, dan usulan pesan commit. **Jangan commit dan jangan push.**
