#!/usr/bin/env bash
# Backup stack produksi: database (pg_dump -Fc) + poster/logo unggahan (volume uploads) + dokumen
# verifikasi mitra (volume kyc, isinya sudah terenkripsi).
# Hasil: backups/racego-YYYYMMDD-HHMMSS/{db.dump,uploads.tar.gz,kyc.tar.gz}. Menyimpan KEEP (7) backup terakhir.
# Kode keluar bukan nol bila gagal, jadi aman untuk cron (contoh baris cron: docs/10-deploy.md).
# Catatan: .env.prod (kunci QR, KYC_ENC_KEY, password) SENGAJA TIDAK ikut. Backup berisi data terenkripsi;
# kuncinya harus disimpan terpisah (pengelola password), agar backup yang bocor tidak bisa dibuka.
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

volume() { compose ps -q api | xargs docker inspect -f "{{range .Mounts}}{{if eq .Destination \"$1\"}}{{.Name}}{{end}}{{end}}"; }
for v in uploads kyc; do
  name_v=$(volume "/data/$v")
  [[ -n $name_v ]] || { echo "volume $v tidak ditemukan (api tidak berjalan?)" >&2; exit 1; }
  docker run --rm -v "$name_v":/u:ro postgres:17-alpine tar -C /u -czf - . >"$tmp/$v.tar.gz"
  tar -tzf "$tmp/$v.tar.gz" >/dev/null
done

mv "$tmp" "backups/$name"
echo "backup: backups/$name ($(du -sh "backups/$name" | cut -f1))"

# Rotasi: hapus backup lama, sisakan KEEP terbaru (nama berurutan waktu).
ls -1d backups/racego-* | sort -r | tail -n +"$((KEEP + 1))" | while read -r old; do
  rm -rf "$old" && echo "dihapus: $old"
done
