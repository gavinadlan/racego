#!/usr/bin/env bash
# Pulihkan backup ke stack produksi yang sedang berjalan. MENIMPA database dan unggahan saat ini.
#   scripts/restore.sh backups/racego-YYYYMMDD-HHMMSS
# Kunci QR (QR_PRIVATE_KEY) dan KYC_ENC_KEY di .env.prod harus sama dengan saat backup dibuat: QR tiket tetap
# sah dan dokumen verifikasi mitra tetap bisa dibuka. Backup lama tanpa kyc.tar.gz tetap bisa dipulihkan.
set -euo pipefail
cd "$(dirname "$0")/.."

ENV_FILE=${ENV_FILE:-.env.prod}
compose() { docker compose -f docker-compose.prod.yml --env-file "$ENV_FILE" "$@"; }

src=${1:-}
src=${src%/}
if [[ -z $src || ! -f $src/db.dump || ! -f $src/uploads.tar.gz ]]; then
  echo "pakai: scripts/restore.sh backups/racego-YYYYMMDD-HHMMSS  (folder berisi db.dump & uploads.tar.gz)" >&2
  exit 1
fi

echo "Database dan unggahan di stack ini akan DIGANTI dengan isi $src."
echo "Semua data (termasuk sesi login) yang dibuat setelah backup itu akan hilang."
read -r -p "Ketik PULIHKAN untuk melanjutkan: " ans
[[ $ans == PULIHKAN ]] || { echo "dibatalkan"; exit 1; }

volume() { compose ps -q api | xargs docker inspect -f "{{range .Mounts}}{{if eq .Destination \"$1\"}}{{.Name}}{{end}}{{end}}"; }
uploads=$(volume /data/uploads)
kyc=$(volume /data/kyc)
[[ -n $uploads && -n $kyc ]] || { echo "volume uploads/kyc tidak ditemukan; jalankan make prod-up dulu" >&2; exit 1; }

echo "• menghentikan api & worker"
compose stop api worker

echo "• memulihkan database"
compose exec -T postgres sh -c 'dropdb -U "$POSTGRES_USER" --force --if-exists racego && createdb -U "$POSTGRES_USER" racego'
compose exec -T postgres sh -c 'pg_restore -U "$POSTGRES_USER" -d racego --no-owner --no-privileges --exit-on-error' <"$src/db.dump"

echo "• memulihkan unggahan"
docker run --rm -i -v "$uploads":/u postgres:17-alpine sh -c \
  'find /u -mindepth 1 -delete && tar -C /u -xzf - && chown -R 65532:65532 /u' <"$src/uploads.tar.gz"

if [[ -f $src/kyc.tar.gz ]]; then
  echo "• memulihkan dokumen verifikasi (terenkripsi; butuh KYC_ENC_KEY yang sama)"
  docker run --rm -i -v "$kyc":/u postgres:17-alpine sh -c \
    'find /u -mindepth 1 -delete && tar -C /u -xzf - && chown -R 65532:65532 /u && chmod 700 /u' <"$src/kyc.tar.gz"
else
  echo "• backup ini belum berisi kyc.tar.gz: dokumen verifikasi dibiarkan"
fi

# Stok & hold di Redis milik keadaan sebelum restore; kosongkan agar dimuat ulang dari database.
echo "• mengosongkan Redis (stok dimuat ulang dari database)"
compose exec -T redis sh -c 'redis-cli --no-auth-warning FLUSHALL' >/dev/null

echo "• menyalakan api & worker"
compose up -d --wait api worker
echo "selesai: $src dipulihkan"
