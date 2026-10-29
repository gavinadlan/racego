#!/usr/bin/env bash
# Mengisi rahasia kosong di .env.prod: POSTGRES_USER, POSTGRES_PASSWORD, REDIS_PASSWORD, QUEUE_SECRET, KYC_ENC_KEY, QR_PRIVATE_KEY.
# KYC_ENC_KEY yang sudah terisi TIDAK PERNAH ditimpa (kunci berubah = dokumen verifikasi tak terbaca).
# Nilai yang sudah terisi tidak ditimpa tanpa konfirmasi. Nilai rahasia tidak pernah dicetak.
#   scripts/gen-secrets.sh            (dari root repo)
set -euo pipefail
cd "$(dirname "$0")/.."

ENV_FILE=${ENV_FILE:-.env.prod}
if [[ ! -f $ENV_FILE ]]; then
  cp .env.prod.example "$ENV_FILE"
  echo "• $ENV_FILE dibuat dari .env.prod.example"
fi
chmod 600 "$ENV_FILE"

current() { grep -E "^$1=" "$ENV_FILE" | tail -n1 | cut -d= -f2- || true; }

# set_var KEY VALUE: ganti baris KEY=… atau tambahkan di akhir. Nilai lewat environment, bukan argumen awk.
set_var() {
  local tmp
  tmp=$(mktemp "$ENV_FILE.XXXXXX")
  if grep -qE "^$1=" "$ENV_FILE"; then
    K="$1" V="$2" awk 'BEGIN{k=ENVIRON["K"]; v=ENVIRON["V"]} index($0, k"=")==1 {print k"="v; next} {print}' "$ENV_FILE" >"$tmp"
  else
    cat "$ENV_FILE" >"$tmp"
    # Baris terakhir tanpa newline (disimpan editor) jangan sampai tersambung dengan baris baru.
    [[ -s $tmp && $(tail -c1 "$tmp") != "" ]] && echo >>"$tmp"
    printf '%s=%s\n' "$1" "$2" >>"$tmp"
  fi
  chmod 600 "$tmp"
  mv "$tmp" "$ENV_FILE"
}

random_hex() { od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n'; }

# confirm PROMPT: true bila pengguna mengetik "y". Tanpa terminal (cron/CI) selalu "tidak".
confirm() {
  [[ -t 0 ]] || return 1
  local ans
  read -r -p "$1 [y/N] " ans
  [[ $ans == y || $ans == Y ]]
}

fill() { # fill KEY GENERATOR [peringatan bila menimpa]
  local key=$1 gen=$2 warn=${3:-}
  if [[ -n $(current "$key") ]]; then
    [[ -n $warn ]] && echo "  ! $warn"
    if ! confirm "  $key sudah terisi. Timpa dengan nilai baru?"; then
      echo "• $key: dibiarkan"
      return
    fi
  fi
  set_var "$key" "$($gen)"
  echo "• $key: diisi"
}

pg_user() { echo "rg_$(random_hex 4)"; }
pw() { random_hex 24; }

fill POSTGRES_USER pg_user "Postgres hanya memakai user/password ini saat volume pgdata pertama dibuat; mengganti nanti membuat api tidak bisa login."
fill POSTGRES_PASSWORD pw "Postgres hanya memakai user/password ini saat volume pgdata pertama dibuat; mengganti nanti membuat api tidak bisa login."
fill REDIS_PASSWORD pw
fill QUEUE_SECRET pw "Mengganti QUEUE_SECRET membatalkan pass antrian yang sedang dipegang pembeli (aman di luar jam war tiket)."

# Kunci KYC: hanya diisi bila kosong, tanpa pilihan menimpa.
if [[ -n $(current KYC_ENC_KEY) ]]; then
  echo "• KYC_ENC_KEY: sudah terisi, TIDAK diubah (mengganti kunci = dokumen verifikasi tidak bisa dibuka)"
else
  set_var KYC_ENC_KEY "$(head -c 32 /dev/urandom | base64 | tr -d '\n')"
  cat <<'WARN'
• KYC_ENC_KEY: diisi.
  !!! Cadangkan KYC_ENC_KEY SEKARANG di tempat terpisah dari backup data (mis. pengelola password). !!!
  Tanpa kunci ini, dokumen verifikasi & NIK/rekening/NPWP di backup TIDAK BISA dipulihkan.
WARN
fi

if [[ -n $(current QR_PRIVATE_KEY) ]]; then
  cat <<'WARN'
  !!! QR_PRIVATE_KEY sudah terisi. !!!
  Mengganti kunci ini membuat SEMUA QR tiket yang sudah terbit TIDAK SAH di scanner.
  Ganti hanya bila kunci bocor, dan simpan kunci lama untuk catatan.
WARN
  ans=""
  [[ -t 0 ]] && read -r -p "  Ketik GANTI KUNCI QR untuk mengganti, atau Enter untuk membiarkan: " ans
  if [[ $ans != "GANTI KUNCI QR" ]]; then
    echo "• QR_PRIVATE_KEY: dibiarkan"
    exit 0
  fi
fi

if ! docker image inspect racego-backend:latest >/dev/null 2>&1; then
  echo "• membangun image backend untuk keygen…"
  docker build -q -t racego-backend:latest ./backend >/dev/null
fi
key=$(docker run --rm racego-backend:latest /app/keygen | sed -n 's/^QR_PRIVATE_KEY=//p')
[[ -n $key ]] || { echo "keygen gagal" >&2; exit 1; }
set_var QR_PRIVATE_KEY "$key"
echo "• QR_PRIVATE_KEY: diisi. Cadangkan .env.prod (atau minimal kunci ini) di luar server sekarang."
