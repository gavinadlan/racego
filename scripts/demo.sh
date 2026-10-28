#!/usr/bin/env bash
# Demo sementara dari laptop: stack produksi + Cloudflare quick tunnel (gratis, tanpa akun & domain).
#   make demo        nyalakan, cetak URL publik, tunggu Ctrl+C
#   make demo-down   hentikan tunnel & stack (dari terminal lain; data di volume tetap ada)
# Alamat https://….trycloudflare.com berubah setiap demo dinyalakan ulang.
set -euo pipefail
cd "$(dirname "$0")/.."

ENV_FILE=${ENV_FILE:-.env.prod}
DEMO_ENV=.env.demo
DEMO_PORT=${DEMO_PORT:-8088} # port lokal Caddy (hanya 127.0.0.1) yang dituju tunnel
RUN_DIR=${TMPDIR:-/tmp}
RUN_DIR=${RUN_DIR%/}/racego-demo
PID_FILE=$RUN_DIR/cloudflared.pid
LOG=$RUN_DIR/cloudflared.log

compose() { docker compose -f docker-compose.prod.yml --env-file "$ENV_FILE" --env-file "$DEMO_ENV" "$@"; }

stop_all() {
  if [[ -f $PID_FILE ]]; then
    kill "$(cat "$PID_FILE")" 2>/dev/null || true
    rm -f "$PID_FILE"
  fi
  if [[ -f $DEMO_ENV ]]; then
    compose down --remove-orphans
    rm -f "$DEMO_ENV"
  fi
}

if [[ ${1:-} == down ]]; then
  stop_all
  echo "demo dihentikan (data tetap di volume; hapus dengan: docker compose -f docker-compose.prod.yml down -v)"
  exit 0
fi

command -v docker >/dev/null || { echo "docker belum terpasang: https://docs.docker.com/get-docker/" >&2; exit 1; }
if ! command -v cloudflared >/dev/null; then
  echo "cloudflared belum terpasang." >&2
  echo "  macOS:  brew install cloudflared" >&2
  echo "  Linux:  https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/" >&2
  exit 1
fi
if [[ ! -f $ENV_FILE ]]; then
  echo "$ENV_FILE belum ada; membuat dari .env.prod.example dan mengisi rahasia…"
  ENV_FILE=$ENV_FILE scripts/gen-secrets.sh </dev/null
fi
if [[ -f $PID_FILE ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
  echo "demo sudah berjalan; hentikan dulu dengan make demo-down" >&2
  exit 1
fi

mkdir -p "$RUN_DIR"
: >"$LOG"
trap 'echo; echo "menghentikan demo…"; stop_all' EXIT
trap 'exit 130' INT TERM

echo "• membuka quick tunnel ke http://127.0.0.1:$DEMO_PORT"
cloudflared tunnel --no-autoupdate --url "http://127.0.0.1:$DEMO_PORT" >"$LOG" 2>&1 &
echo $! >"$PID_FILE"

url=""
for _ in $(seq 1 60); do
  url=$(grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' "$LOG" | head -n1 || true)
  [[ -n $url ]] && break
  kill -0 "$(cat "$PID_FILE")" 2>/dev/null || break
  sleep 1
done
if [[ -z $url ]]; then
  echo "URL tunnel tidak muncul. Log cloudflared:" >&2
  tail -n 20 "$LOG" >&2
  exit 1
fi
echo "• URL publik: $url"

# Di belakang tunnel: Caddy HTTP biasa (TLS diurus Cloudflare), hanya terjangkau dari laptop ini,
# dan CF-Connecting-IP dari Cloudflare dipercaya untuk IP klien (lihat docs/10-deploy.md).
cat >"$DEMO_ENV" <<EOF
# Dibuat scripts/demo.sh, dihapus saat demo berhenti.
SITE_ADDRESS=:80
FRONTEND_URL=$url
PROXY_MODE=tunnel
CADDY_BIND=127.0.0.1
HTTP_PORT=$DEMO_PORT
HTTPS_PORT=$((DEMO_PORT + 1))
TRUSTED_PROXY_HOPS=1
EOF

echo "• menyalakan stack produksi (image frontend dipakai ulang; tidak bergantung URL)"
compose up -d --build --wait --remove-orphans

events=$(compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d racego -Atc "SELECT count(*) FROM events"')
if [[ $events == 0 ]]; then
  echo "• database kosong: mengisi data contoh (make prod-seed)"
  compose run --rm migrate /app/migrate seed
fi

echo "• menunggu $url/healthz …"
ok=""
for _ in $(seq 1 60); do
  if curl -fsS -o /dev/null "$url/healthz" 2>/dev/null; then ok=1; break; fi
  sleep 2
done
[[ -n $ok ]] || { echo "healthz lewat URL publik tidak menjawab. Cek: $LOG dan make prod-logs" >&2; exit 1; }

cat <<EOF

  RaceGo demo berjalan:  $url

  Masuk: buka $url/masuk, isi email, lalu masukkan kode dari email.
         (Butuh SMTP_* di $ENV_FILE. Tanpa SMTP kode tidak terkirim.)
  Jadikan akunmu pemilik penyelenggara contoh (terminal lain):
         make prod-grant EMAIL=kamu@email.com ORG=jakarta-runners ROLE=owner
  Pembayaran: MIDTRANS_SERVER_KEY kosong = mode mock (tombol "Simulasikan bayar").

  Ingat: alamat ini BERUBAH setiap demo dinyalakan ulang, dan tautan di email lama ikut mati.
  Hentikan dengan Ctrl+C di sini atau make demo-down di terminal lain.

EOF

# Tetap di depan sampai Ctrl+C atau tunnel berhenti (make demo-down mematikan cloudflared).
while kill -0 "$(cat "$PID_FILE" 2>/dev/null || echo 0)" 2>/dev/null; do sleep 2; done
echo "tunnel berhenti"
