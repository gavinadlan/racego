# Tugas 08 · Lanjutan (sesi sebelumnya terputus karena batas pemakaian)

> Jalankan dari root repo:
> `claude "Kerjakan docs/tasks/08-lanjutan.md: lanjutkan Tugas 08 dari yang belum selesai sampai semua kriteria terpenuhi"`

## Situasi

Sesi CLI yang mengerjakan `docs/tasks/08-undangan-staf-scanner-offline.md` terputus di tengah jalan. **Spesifikasi lengkap dan kriteria selesai ada di berkas itu; baca ulang seluruhnya dulu**, lalu `AGENTS.md`/`frontend/AGENTS.md`, `docs/05-tiket-qr-dan-scan.md`, dan `git status`/`git diff`. Tugas 01–07 sudah ter-commit (commit terakhir: `Perapian & hardening … Go 1.27.3`). Semua pekerjaan Tugas 08 masih belum di-commit di working tree. **Jangan mengulang dari nol dan jangan membuang pekerjaan yang ada.**

## Yang terlihat sudah ada (hasil pemeriksaan dari luar; belum dibuktikan berjalan)

- Migrasi `0008_invitations_offline_scan.{up,down}.sql` (tabel `org_invitations`, kolom baru `scan_logs`, `tickets.updated_at` + trigger).
- Backend undangan: `internal/partner/invitations.go`, rute `/v1/invitations/{token}` (+`/accept`) dan `/v1/org/{org}/invitations` (+`resend`, `DELETE`), `POST /members` dihapus, template email, worker.
- Backend scanner offline: `internal/ticket/offline.go` (manifest + batch), rute `scan-manifest` dan `scan/batch`, config `SCAN_MANIFEST_TTL_HOURS`.
- Tes: `server/invitations_test.go` (3 tes), `server/offline_scan_test.go` (4 tes), perubahan di `portal_test.go`, `kyc_test.go`, `config_test.go`.
- Frontend: `components/invitation.tsx`, `app/undangan/[token]`, `org-team.tsx` (undangan), `scanner.tsx` (mode offline), `lib/offline-store.ts`, `lib/offline-scanner.ts`, `lib/qr-verify.ts`, `api.ts`, `session.tsx`.
- Dokumen sudah banyak diperbarui (05, 07, 08, 09, 10, README, frontend/README, `.env*`, `docker-compose.prod.yml`), termasuk **keputusan 7**: service worker sudah dicoba dan **tidak dipakai** (Next 16 `cacheComponents` + hidrasi gagal), diganti peringatan "jangan muat ulang" + `beforeunload`. Pertahankan keputusan ini kecuali Anda menemukan bukti kuat sebaliknya.

## Yang harus Anda lakukan sekarang

1. **Verifikasi dasar dulu**, jangan percaya ringkasan di atas: `make up` (bila perlu), `make test`, `cd frontend && npm run lint && npm run build`. Perbaiki kegagalan/berkas setengah jadi (cari `TODO`, kode yang tidak terpakai, impor rusak, tes yang belum lengkap, tipe yang belum disinkronkan antara `api.ts` dan backend).
2. **Audit tiap kriteria 1–7 di berkas Tugas 08** terhadap kode dan tes nyata. Buat daftar singkat: *sudah terbukti / ada kode tapi belum terbukti / belum ada*. Kerjakan hanya dua kelompok terakhir. Hal-hal yang paling mungkin belum selesai karena sesi terputus (periksa, jangan berasumsi):
   - tes backend yang diminta spesifikasi tetapi belum ada: undangan (kedaluwarsa, dicabut, token salah → respons identik, email beda tidak mengubah apa pun, tidak ada token mentah di DB, isolasi tenant, audit), manifest (tanpa email/NIK/telepon/kontak darurat/tanggal lahir; `?since`; 10.000 tiket), batch (idempoten, paralel dua perangkat, QR palsu, event salah, refund, batas 200 entri/ukuran body, peran `finance`);
   - sisa teks/dokumen: `docs/01-prd.md`, `docs/02-arsitektur.md`, `.env.prod.example`, `frontend/README.md` konsisten; `make help` bila ada target baru;
   - **uji browser Playwright** (kriteria 2, 3, 4, 5) beserta bukti, screenshot laptop + HP 390px yang Anda lihat sendiri, dan angka beban 10.000 tiket (ukuran unduhan gzip, waktu, memori API, waktu muat IndexedDB, waktu verifikasi lokal < 100 ms);
   - log API/worker/konsol bersih; log tidak memuat token undangan, token QR, atau isi manifest.
3. Jalankan skenario browser yang belum pernah dijalankan dari awal di folder sementara di luar repo (jangan mengubah `package.json`; OTP dan tautan undangan dari Mailpit; **jangan menyentuh `.env` pribadi**; data fiktif):
   - Undangan: owner mengundang email baru → email Mailpit → buka tautan belum login → OTP → bergabung → muncul di Tim dan bisa membuka scanner; negatif: email akun berbeda, tautan dipakai ulang, kedaluwarsa (majukan `expires_at` via SQL), dicabut, admin mengundang admin, email yang sudah anggota, batas laju kirim ulang.
   - Offline: siapkan manifest → `setOffline(true)` → QR asli (hijau), ulang (kuning), diubah (merah), event lain (merah), refund (merah), tiket terbit setelah manifest (kuning khusus, tidak ditandai) → antrean bertahan → online → sinkron otomatis → hitungan server benar; dua perangkat (dua context) memindai tiket yang sama → satu `ok`, satu "Perlu ditinjau"; manifest kedaluwarsa menghentikan scan; logout menghapus data; IndexedDB gagal tidak membuat halaman crash; jalur kamera dengan video palsu Chrome bila bisa (atau tandai tidak bisa diuji).
   - Gunakan QR dari tiket yang dihasilkan checkout mock, bukan data palsu.
4. Bila menemukan bug saat menguji, perbaiki dan tambahkan tes. Bug di luar cakupan Tugas 08: catat saja.

## Aturan

- **Jangan commit dan jangan push.** Tanpa menyentuh `.env`/`.env.prod` pribadi, tanpa email sungguhan, tanpa tunnel ke internet, tanpa rahasia di repo, tanpa menulis token/kunci/isi manifest ke log atau laporan.
- Jangan mengubah perilaku checkout, pembayaran, ledger, pencairan, KYC, login, atau antrean. Tes lama tidak diubah kecuali perubahan perilaku yang memang diminta Tugas 08 (jelaskan).
- Bersihkan data uji (akun/event/tiket sintetis, 10.000 tiket) dan matikan proses/stack yang Anda nyalakan setelah selesai. Jangan menjalankan `make db-reset` kecuali benar-sungguh perlu; bila perlu, laporkan.
- Bila sesi terputus lagi, tulis progres di akhir setiap tahap ke `docs/tasks/08-progress.md` (daftar centang kriteria + temuan) supaya bisa dilanjutkan; hapus berkas itu di akhir bila semua selesai (atau biarkan dan sebutkan di laporan).

## Laporan akhir

Tabel hasil tiap kriteria 1–7 (bukti nyata; tandai yang tidak bisa diuji), daftar apa yang sudah ada vs yang Anda kerjakan di sesi ini, keputusan/penyimpangan (terutama keputusan 7), asumsi yang belum terverifikasi, angka beban, dan usulan pesan commit. Jangan commit dan jangan push.
