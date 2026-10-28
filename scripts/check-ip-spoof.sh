#!/usr/bin/env bash
# Membuktikan batas permintaan kode OTP per IP (20/jam) tidak bisa diakali dengan header IP palsu.
# Mengirim 25 permintaan kode; tiap permintaan memalsukan X-Forwarded-For, X-Real-IP, dan
# CF-Connecting-IP yang berbeda. Lulus bila permintaan ke-21 sampai ke-25 tetap ditolak 429.
#
#   scripts/check-ip-spoof.sh https://racego.example.com kamu@gmail.com
#   INSECURE=1 scripts/check-ip-spoof.sh https://localhost kamu@gmail.com   (sertifikat lokal)
#
# Efek samping: sampai 20 email kode terkirim ke kamu+ipcek1@…, kamu+ipcek2@…, dan jatah kode
# dari IP-mu habis selama 1 jam.
set -euo pipefail
url=${1:?pakai: scripts/check-ip-spoof.sh <url-situs> <email-kamu>}
email=${2:?pakai: scripts/check-ip-spoof.sh <url-situs> <email-kamu>}
url=${url%/}
user=${email%@*}
domain=${email#*@}

passed=0
for i in $(seq 1 25); do
  ip="203.0.113.$i"
  status=$(curl -s ${INSECURE:+-k} -o /dev/null -w '%{http_code}' -X POST "$url/v1/auth/otp/request" \
    -H 'Content-Type: application/json' -H "Origin: $url" \
    -H "X-Forwarded-For: $ip" -H "X-Real-IP: $ip" -H "CF-Connecting-IP: $ip" \
    -d "{\"email\":\"$user+ipcek$i@$domain\"}")
  echo "permintaan $i (IP palsu $ip): HTTP $status"
  [[ $status == 429 ]] || passed=$((passed + 1))
  if ((i > 20)) && [[ $status != 429 ]]; then
    echo "GAGAL: permintaan ke-$i lolos; header IP palsu dipercaya. Periksa PROXY_MODE dan TRUSTED_PROXY_HOPS." >&2
    exit 1
  fi
done
echo "LULUS: $passed permintaan lolos, sisanya 429. Header IP palsu tidak mengakali batas per IP."
