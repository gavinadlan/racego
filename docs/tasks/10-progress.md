# Tugas 10 · progres (berkas sementara, dihapus di akhir)

Sesi terputus karena batas pemakaian. Lanjutkan dari "Berikutnya".

## Infra uji (masih menyala; matikan di akhir: `docker rm -f t10-pg t10-redis t10-mailpit`)
- `t10-pg` Postgres :55440 (DB `racego_t10` untuk app/browser, `racego_test` untuk tes), `t10-redis` :6390, `t10-mailpit` SMTP :1035 / UI :8035.
- Tes Go: `RACEGO_TEST_DATABASE_URL="postgres://racego:racego@localhost:55440/racego_test?sslmode=disable" go test ./...` (dari `backend/`).

## Tahap
- [x] 0. Infra uji + baca kode
- [x] 1. Migrasi `0009_attendance_draw` (tickets.attended_*, scan_logs.kind, events.attendance_*/draw_*, draw_prizes, draw_winners append-only + trigger). Sudah diterapkan ke kedua DB.
- [x] 2. Bagian A backend + tes: `internal/ticket/attend.go`, `offline.go`, `service.go`, `internal/participant/participant.go`, route di `server/router.go`; tes `internal/server/attend_test.go` (7 tes lulus). Seluruh suite Go hijau saat itu.
- [x] 3. Bagian A frontend: `lib/api.ts`, `lib/offline-store.ts`, `lib/offline-scanner.ts`, `components/scanner.tsx` (mode ambil pack | absen, penanda besar, offline attend, Perlu ditinjau), `components/participants.tsx` (kolom hadir, filter, ringkasan, batal absen). `tsc` dan `eslint` bersih. BELUM ada UI pengaturan jendela absen (rencana: kartu di halaman Undian).
- [~] 4. Bagian B backend: `internal/draw/draw.go` DITULIS dan terpasang di router (`Deps.DrawSource` untuk tes), `go build` hijau. Tes unit `internal/draw/draw_test.go` lulus (keseragaman 20.000 undian, batas, hash). **Belum ada tes integrasi** `internal/server/draw_test.go`.
- [ ] 5. Bagian B frontend (halaman `/org/[org]/events/[eventId]/undian`, layar panggung + reel + reduced-motion + kunci idempoten per klik, hasil publik `/events/[id]/hasil-undian` noindex, tautan "Undian" di `org-dashboard.tsx`, pengaturan jendela absen)
- [ ] 6. Dokumen: 05, 07, 02, 01, **12-undian.md baru**, 08, 09, README, frontend/README
- [ ] 7. Uji browser Playwright (folder di luar repo) + screenshot (kriteria 2–5)
- [ ] 8. `make test` + `npm run build` + bersih-bersih (hapus container t10-*, file ini, pastikan `git status` bersih)

## Berikutnya
1. Tulis `backend/internal/server/draw_test.go` (pakai `newAttendEnv` di `attend_test.go`; cleanup draw sudah ada di sana). Cakup: kelayakan (hanya hadir, mode collected, filter kategori, refund/batal, pemenang keluar dari kolam), puluhan undian tanpa non-hadir, konkurensi (hadiah qty 1 × 8 permintaan; tiket sama dua hadiah), `count`>sisa, `pool_empty`, `draw_not_open`, idempotensi `Idempotency-Key`, `all`, claim/forfeit + pengganti, append-only (DELETE/UPDATE ditolak trigger), `settings_locked`, hapus/kurangi hadiah, matriks peran (owner/admin; rpc_staff baca; finance 403; asing 404; anonim 401), publik (404 bila mati, field aman, `X-Robots-Tag`), audit (`draw.*`, tanpa nama), `pool_hash` direproduksi lewat `draw.PoolHash`, CSV (audit + peran), batal absen pemenang aktif → `attendance_in_use`.
2. Frontend Bagian B, dokumen, uji browser, bersih-bersih.

## Temuan / keputusan
- Tes lama diubah SATU asersi: `participants_test.go` CSV 13 → 14 kolom (spesifikasi menambah kolom).
- Tes lama `TestScanOnlineUnchanged` flaky (merusak 2 karakter terakhir token base64; kadang token tetap sah). Bukan regresi; tes baru memakai kerusakan di tengah token.
- Hasil publik memakai event ID (uuid), bukan slug (slug hanya unik per org): `/v1/events/{eventID}/draw-results`.
- Jendela absen: endpoint `GET/PUT .../attendance/settings`; batch offline menilai jendela dengan `scanned_at` perangkat.
- Draw: kunci advisory per event (`ticket.LockEventDraw`) dipakai undi, ubah pengaturan/hadiah/status, dan batal absen. `pool_hash` = SHA-256 hex ticket_id kolam terurut (snapshot sebelum permintaan).
- Kode error tambahan: `not_attended`, `prize_has_winners`, `invalid_status`, `too_many_prizes`, `results_not_found`.
