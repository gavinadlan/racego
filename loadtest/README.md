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
| `stats.py` | `sample`: cuplikan sumber daya (dipanggil `run.sh`); `report <dir> <awalan>...`: median (min–maks) dari `<awalan>-r1`, `-r2`, … |
| `nginx.conf`, `../docker-compose.loadtest.scale.yml` | Mode N replika: load balancer uji dan container berbatas CPU |
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

## N replika API (docs/13-skala-lanjutan.md)

`SCALE=N` menjalankan N replika API (0,5 core masing-masing) dan `WORKERS` worker di container, di belakang nginx
`127.0.0.1:18080`. Tanpa `SCALE`, satu API dan satu worker native seperti biasa.

```bash
PG_MAX_CONNECTIONS=200 loadtest/run.sh up
NAME=c-n2-r1 SCALE=2 USERS=5000 loadtest/run.sh run war-noqueue
SCALE=2 WORKERS=2 USERS=10000 OPEN_IN=110s loadtest/run.sh run war
SCALE=2 CHAOS=replica USERS=5000 loadtest/run.sh run war        # SIGTERM satu replika; replica-kill = SIGKILL
SCALE=4 PGBOUNCER=1 DB_MAX_CONNS=50 USERS=5000 loadtest/run.sh run war-noqueue
SCALE=2 loadtest/run.sh stop
python3 loadtest/stats.py report loadtest/results c-n2          # dari NAME=c-n2-r1, -r2, -r3
```

Parameter tambahan: `SCALE` (1–8), `WORKERS` (bawaan 1), `API_CPUS` (0.5), `PG_CPUS` (2), `REDIS_CPUS` (1), `K6_CPUS` (3),
`K6_MEM` (5500m), `PGBOUNCER` (1 = API lewat PgBouncer mode transaction), `PGBOUNCER_POOL` (20), `PG_MAX_CONNECTIONS` (100),
`NAME` (awalan berkas hasil), `BACKEND_DIR` (bangun biner dari pohon kode lain). Jaga `SCALE × DB_MAX_CONNS + 10 × WORKERS` di bawah `PG_MAX_CONNECTIONS`.
`TRUSTED_PROXY_HOPS=2` di mode ini: k6 mengisi IP pengguna, nginx menambah satu entri di kanan. `.env` tidak dibaca (`--env-file /dev/null`).
