# 12 · Undian doorprize (spin BIB)

Penyelenggara mengundi hadiah di panggung dengan layar "spin" nomor BIB, **hanya dari peserta yang sudah diabsen hari-H**. Peserta yang membeli tiket tetapi tidak datang tidak ikut. Kode: `backend/internal/draw`, `frontend/src/components/draw*.tsx`.

## Absen hari-H (prasyarat kolam)

Scanner punya dua mode: **Ambil race pack** (tidak berubah) dan **Absen hari-H** (`POST …/attend`). Absen menulis `tickets.attended_at/attended_by` dan tidak mengubah `status` atau `collected_*`. Hanya tiket `issued`/`collected` yang sah; `refunded`/`cancelled` ditolak. Detail ada di [05](05-tiket-qr-dan-scan.md#absen-hari-h).

Jendela waktu: bawaan dari tanggal lomba (`race_date`, WIB) sampai akhir hari berikutnya. Penyelenggara bisa menimpanya (event multi-hari) di halaman Undian, kartu **Jendela absen hari-H**, atau lewat `PUT …/attendance/settings`. Di luar jendela: 409 `attend_not_open`. Undian memakai jendela yang sama (`draw_not_open` sebelum dibuka).

## Kelayakan

| Pengaturan | Nilai | Arti |
| --- | --- | --- |
| `draw_eligibility` | `attended` (bawaan) | tiket `issued`/`collected` dengan `attended_at` terisi |
| | `collected` | tiket `collected` (race pack diambil). **Tidak membuktikan peserta lari**; UI memberi peringatan |
| `allow_multiple_wins` | `false` (bawaan) | pemenang aktif (`winner`/`claimed`) keluar dari kolam semua hadiah |
| `draw_public` | `false` (bawaan) | halaman hasil publik mati (404) |

Hadiah boleh dibatasi kategori. Hadiah yang sudah dimenangkan satu tiket tidak pernah memilih tiket yang sama lagi, juga bila `allow_multiple_wins=true`. Eligibilitas dan `allow_multiple_wins` terkunci (409 `settings_locked`) setelah pemenang pertama. Definisi kolam ada di satu fungsi (`poolIDs`), dipakai undian dan ringkasan kolam.

## Pengacakan

- Pemenang dipilih **di server** dengan `crypto/rand` (`rand.Int` dengan batas, seragam tanpa bias modulo), lewat Fisher-Yates parsial untuk `count` > 1, dari **snapshot kolam saat itu** dalam satu transaksi.
- Hasil ditentukan sebelum animasi. Layar hanya memutar angka acak lalu berhenti di hasil server (4–6 detik, melambat). Dengan `prefers-reduced-motion`, tanpa putaran.
- **Tidak ada jalur memilih pemenang manual**: tidak ada parameter seed/BIB/urutan di API. Satu-satunya input pengundian: `count` atau `all`. Sumber acak hanya bisa diganti di tes (`Deps.DrawSource`), tidak lewat konfigurasi.
- Satu kunci advisory per event (`ticket.LockEventDraw`) membuat undi, ubah hadiah/pengaturan, dan batal absen berjalan bergantian: dua admin menekan Undi bersamaan tidak melebihi kuota dan satu tiket tidak menang dua hadiah sekaligus.
- Satu permintaan "Undi" = satu pemenang. "Undi semua sisa" (`all`) untuk hadiah banyak. Header `Idempotency-Key` (dibuat klien per klik) membuat kiriman ulang mengembalikan hasil yang sama (`replayed: true`), jadi koneksi putus atau klik ganda tidak mengundi dua kali.

## Audit

- `draw_winners` **append-only** (trigger di migrasi `0009`): tidak bisa dihapus; kolom inti tidak bisa diubah. Hanya `status` (`winner` → `claimed`/`forfeited`) dan `note` yang boleh berubah. Pengganti = baris baru lewat undian ulang.
- Tiap pengundian menyimpan `pool_size` dan `pool_hash` = SHA-256 (hex) dari `ticket_id` kolam (huruf kecil, terurut, dipisah `\n`). Untuk memeriksa: ambil tiket `attended_at IS NOT NULL` (status berlaku) pada waktu itu, kecuali yang sudah menang, urutkan, hash, bandingkan; pastikan tiket pemenang ada di daftar itu. Fungsi `draw.PoolHash` dan tes `TestDrawPoolHashAndCSVAndPublic` menunjukkan caranya. Keterbatasan: kolam dihitung dari data saat ini, jadi absen yang dibatalkan atau refund setelah undian mengubah hasil rekonstruksi; hash membuktikan konsistensi dengan data, bukan bahwa kolam "tidak dimanipulasi sebelumnya".
- `audit_logs`: `draw.prize.created|updated|deleted`, `draw.drawn` (hadiah, BIB, `pool_size`, `pool_hash`; tanpa nama), `draw.claimed`, `draw.forfeited`, `draw.settings.changed`, `draw.results.downloaded`, serta `attendance.*`.

## Privasi

Layar panggung dan hasil publik hanya memuat **BIB + nama depan (maks. 20 karakter) + kategori**. Panel panitia (owner/admin) melihat nama lengkap. Email, telepon, NIK, tanggal lahir, kontak darurat tidak pernah ada di respons undian. Hasil publik (`GET /v1/events/{eventID}/draw-results`, halaman `/events/{id}/hasil-undian`) hanya hidup bila `draw_public`, event terbit/ditutup dan org terverifikasi; `X-Robots-Tag: noindex`, `<meta robots noindex>`, cache 10 detik. Pemenang gugur tidak ditampilkan. Path memakai ID event (UUID), bukan slug, karena slug hanya unik per organisasi.

## Peran

| | owner/admin | rpc_staff | finance | non-anggota |
| --- | --- | --- | --- | --- |
| Pengaturan, hadiah, undi, status, kolam, CSV | ya | baca pengaturan/hadiah/pemenang | 403 | 404 |

## Batasan jujur

- Sumber acak adalah server penyelenggara (`crypto/rand`). Ini **bukan** ritual publik yang dapat diverifikasi pihak luar secara kriptografis (tanpa komit-ungkap, drand, atau blockchain); orang yang mengendalikan server dan database tetap bisa, secara teori, mengubah hasil. `pool_hash` dan baris append-only mempersulit dan memperlihatkan perubahan, tidak mencegahnya mutlak.
- Tidak ada klaim legal. **Aturan hukum undian/doorprize bisa berlaku (izin, pajak hadiah, syarat pengumuman); penyelenggara perlu memastikan sendiri. Dokumen ini bukan nasihat hukum.**
- Pemenang tidak dinotifikasi (email/WhatsApp); panitia memanggil BIB di panggung.
- Belum ada: undian tiket/ballot untuk pembelian, ritual komit-ungkap acak publik, syarat finis/waktu tempuh, notifikasi pemenang.
