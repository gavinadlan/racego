#!/usr/bin/env bash
# Backup stack produksi: database (pg_dump -Fc) + poster/logo unggahan (volume uploads).
# Hasil: backups/racego-YYYYMMDD-HHMMSS/{db.dump,uploads.tar.gz}. Menyimpan KEEP (7) backup terakhir.
# Kode keluar bukan nol bila gagal, jadi aman untuk cron (contoh baris cron: docs/10-deploy.md).
# Catatan: .env.prod (kunci QR, password) TIDAK ikut; cadangkan terpisah di tempat aman.
set -euo pipefail
umask 077 # isi backup: data pribadi peserta
cd "$(dirname "$0")/.."

ENV_FILE=${ENV_FILE:-.env.prod}
KEEP=${KEEP:-7}
compose() { docker compose -f docker-compose.prod.yml --env-file "$ENV_FILE" "$@"; }

name=racego-$(date +%Y%m%d-%H%M%S)
tmp=backups/.$name.partial
mkdir -p "$tmp"
trap 'rm -rf "$tmp"' EXIT

compose exec -T postgres sh -c 'pg_dump -Fc -U "$POSTGRES_USER" racego' >"$tmp/db.dump"
# Pastikan arsipnya terbaca, bukan sekadar file berisi pesan error.
compose exec -T postgres pg_restore --list <"$tmp/db.dump" >/dev/null

uploads=$(compose ps -q api | xargs docker inspect -f '{{range .Mounts}}{{if eq .Destination "/data/uploads"}}{{.Name}}{{end}}{{end}}')
[[ -n $uploads ]] || { echo "volume uploads tidak ditemukan (api tidak berjalan?)" >&2; exit 1; }
docker run --rm -v "$uploads":/u:ro postgres:17-alpine tar -C /u -czf - . >"$tmp/uploads.tar.gz"
tar -tzf "$tmp/uploads.tar.gz" >/dev/null

mv "$tmp" "backups/$name"
echo "backup: backups/$name ($(du -sh "backups/$name" | cut -f1))"

# Rotasi: hapus backup lama, sisakan KEEP terbaru (nama berurutan waktu).
ls -1d backups/racego-* | sort -r | tail -n +"$((KEEP + 1))" | while read -r old; do
  rm -rf "$old" && echo "dihapus: $old"
done
