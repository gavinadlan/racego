# Uji beban RaceGo (k6, lokal, gratis)

Hasil dan cara membacanya: [docs/11-performa.md](../docs/11-performa.md).

Semua berjalan di mesin ini. Tidak ada akun, layanan berbayar, atau target di luar `localhost`:
pembayaran mock, email ke Mailpit uji, Turnstile mati. Data development dan `.env` tidak dipakai.

```bash
make loadtest-up                           # Postgres :55439, Redis :6381, Mailpit :8026 khusus uji + skema
make obs-up                                # opsional: Grafana http://127.0.0.1:3001 (sandi di .env.obs)
make loadtest SCENARIO=browse VUS=50       # baca-berat
make loadtest SCENARIO=war USERS=500       # antrean → reservasi → order → bayar → tiket, lalu cek integritas
make loadtest SCENARIO=war-noqueue USERS=500
make loadtest SCENARIO=soak USERS=3000 DURATION=12m
make loadtest SCENARIO=war USERS=2000 CHAOS=redis    # atau CHAOS=worker, CHAOS=redis-kill
make loadtest-clean                        # hentikan semua, hapus DB/Redis uji dan loadtest/results/
```

| Berkas | Isi |
| --- | --- |
| `run.sh` | Pengendali: infra, API + worker uji di `127.0.0.1:18080`, seed, k6 (image `grafana/k6`), cek integritas, chaos |
| `lib.js` | Penolak target non-lokal, pemuat pengguna sintetis, request dengan backoff |
| `browse.js` | Daftar event, detail, kalender bulan, filter |
| `war.js` | `war` dan `war-noqueue` (ditentukan event uji: ber-antrean atau tidak) |
| `soak.js` | Jelajah + pembelian stabil 10–15 menit |
| `results/` | Log k6, ringkasan JSON, hasil integritas, token sesi sintetis. Tidak masuk Git |

Parameter (env / argumen `make`): `USERS`, `QUOTA` (bawaan 500, dibagi 50/30/20 ke 3 kategori), `ARRIVAL`
(detik sebelum/sesudah jam buka, bawaan 60), `WAR_MINUTES` (batas menunggu, bawaan 6), `ABANDON` (porsi yang
menyerah, bawaan 0,15), `SETTLE` (menunggu slot yang ditinggal kembali, bawaan 4m), `VUS`, `DURATION`,
`BUY_RATE`, `CHAOS`, `CHAOS_AT`, `CHAOS_DOWN`, `DB_MAX_CONNS`, `QUEUE_ADMIT_PER_SEC`, `QUEUE_MAX_ACTIVE`,
`RESERVATION_TTL` (bawaan uji 1m supaya slot yang ditinggal cepat kembali).

Pengguna sintetis dibuat `migrate loadgen-seed` langsung di database (pengguna + sesi), tanpa pintu belakang
di server. Perintah itu menolak jalan bila `APP_ENV` bukan `development` atau nama database bukan `*_loadtest`.
Tiap pengguna membawa `X-Forwarded-For` sendiri (API uji memakai `TRUSTED_PROXY_HOPS=1`), jadi pembatas laju
per IP dan per akun tetap aktif dan ikut teruji.

Setelah tiap `war*`/`soak`, `migrate loadgen-check` memeriksa: (a) tiket ≤ kuota dan order lunas = tiket,
(b) tanpa BIB/tiket ganda, (c) batas per pembeli, (d) ledger seimbang dan satu jurnal per order lunas,
(e) stok Redis = kuota − terjual − ditahan, (f) tanpa order pending yatim dan outbox kosong. (g) jawaban
jelas bagi yang tidak kebagian diperiksa ambang k6 `unexpected == 0`. Satu saja gagal = `make loadtest` gagal.
