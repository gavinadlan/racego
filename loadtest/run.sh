#!/usr/bin/env bash
# Pengendali uji beban lokal RaceGo (dipanggil Makefile; lihat loadtest/README.md, docs/11-performa.md).
#   run.sh up | seed | start | stop | run <skenario> | check | clean | env
# Semua target ada di mesin ini: Postgres/Redis/Mailpit khusus (docker-compose.loadtest.yml) dan
# API + worker khusus di 127.0.0.1:18080. Variabel dari .env development TIDAK dipakai (env -i),
# jadi kunci Midtrans/SMTP sungguhan di .env tidak pernah ikut: pembayaran mock, email ke Mailpit uji.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RESULTS="$ROOT/loadtest/results"
COMPOSE=(docker compose -f "$ROOT/docker-compose.loadtest.yml")
K6_IMAGE="grafana/k6:2.3.0"

DB_URL="${LOADTEST_DATABASE_URL:-postgres://racego:racego@localhost:55439/racego_loadtest?sslmode=disable}"
REDIS="${LOADTEST_REDIS_ADDR:-localhost:6381}"
API_ADDR="127.0.0.1:18080"
# BACKEND_DIR: bangun dan jalankan biner dari pohon kode lain (mis. git worktree versi lama untuk perbandingan sebelum/sesudah).
BACKEND_DIR="${BACKEND_DIR:-$ROOT/backend}"

# Pengaman: jangan pernah menyentuh data development.
dbname="${DB_URL##*/}"; dbname="${dbname%%\?*}"
case "$dbname" in loadtest_*|*_loadtest) ;; *)
  echo "Menolak: database \"$dbname\" bukan *_loadtest." >&2; exit 1;; esac
dev_redis="${REDIS_ADDR:-localhost:6380}"
if [[ "$REDIS" == "$dev_redis" || "$REDIS" == *:6380 || "$DB_URL" == "${DATABASE_URL:-}" ]] && [[ "${CONFIRM_DEV_TARGET:-}" != "ya-timpa-data-dev" ]]; then
  echo "Menolak: target ($DB_URL, redis $REDIS) sama dengan data development." >&2
  echo "Bila memang sengaja: CONFIRM_DEV_TARGET=ya-timpa-data-dev" >&2; exit 1
fi

# Lingkungan bersih untuk api/worker/migrate uji.
backend_env=(env -i "HOME=$HOME" "PATH=$PATH" "GOFLAGS=${GOFLAGS:-}"
  APP_ENV=development "HTTP_ADDR=$API_ADDR" FRONTEND_URL=http://localhost:3000 TRUSTED_PROXY_HOPS=1
  "DATABASE_URL=$DB_URL" "REDIS_ADDR=$REDIS" "DB_MAX_CONNS=${DB_MAX_CONNS:-20}"
  SMTP_ADDR=localhost:1026 "RESERVATION_TTL=${RESERVATION_TTL:-1m}"
  "QUEUE_ADMIT_PER_SEC=${QUEUE_ADMIT_PER_SEC:-20}" "QUEUE_MAX_ACTIVE=${QUEUE_MAX_ACTIVE:-300}"
  METRICS_ADDR=127.0.0.1:9464 WORKER_METRICS_ADDR=127.0.0.1:9465
  "UPLOAD_DIR=$RESULTS/data/uploads" "KYC_DIR=$RESULTS/data/kyc")

target() {
  echo "── target uji beban ─────────────────────────────────────────"
  echo "  database : ${DB_URL/racego:racego@/racego:***@}"
  echo "  redis    : $REDIS        email: Mailpit uji localhost:1026"
  echo "  api      : http://$API_ADDR (development, pembayaran mock)"
  echo "─────────────────────────────────────────────────────────────"
}

running() { [[ -f "$RESULTS/$1.pid" ]] && kill -0 "$(cat "$RESULTS/$1.pid")" 2>/dev/null; }

start_one() { # api | worker
  running "$1" && return 0
  ulimit -n 65536 2>/dev/null || ulimit -n 10240 # ribuan koneksi bersamaan (bawaan macOS hanya 256)
  # exec: PID yang dicatat adalah biner itu sendiri, bukan shell pembungkus.
  (cd "$BACKEND_DIR" && exec nohup "${backend_env[@]}" "./bin/$1" >>"$RESULTS/$1.log" 2>&1) &
  echo $! >"$RESULTS/$1.pid"
}

stop_one() {
  running "$1" && kill "$(cat "$RESULTS/$1.pid")" 2>/dev/null || true
  for _ in $(seq 50); do running "$1" || break; sleep 0.1; done
  rm -f "$RESULTS/$1.pid"
}

wait_api() {
  for _ in $(seq 100); do curl -fs "http://$API_ADDR/healthz" >/dev/null 2>&1 && return 0; sleep 0.2; done
  echo "API uji tidak menyala, lihat $RESULTS/api.log" >&2; exit 1
}

cmd="${1:-}"; shift || true
mkdir -p "$RESULTS"
case "$cmd" in
  up) # infra + skema + data contoh (untuk skenario browse) + biner
    target
    "${COMPOSE[@]}" up -d --wait
    (cd "$BACKEND_DIR" && go build -o bin/ ./cmd/api ./cmd/worker ./cmd/migrate)
    (cd "$BACKEND_DIR" && "${backend_env[@]}" ./bin/migrate up && "${backend_env[@]}" ./bin/migrate seed)
    ;;
  start)
    (cd "$BACKEND_DIR" && go build -o bin/ ./cmd/api ./cmd/worker ./cmd/migrate)
    start_one api; start_one worker; wait_api
    echo "✓ API + worker uji berjalan (log: loadtest/results/api.log, worker.log)"
    ;;
  stop) stop_one api; stop_one worker; echo "✓ API + worker uji berhenti" ;;
  stop-worker) stop_one worker ;;
  start-worker) start_one worker ;;
  seed) # USERS, QUOTA, QUEUE=true|false, OPEN_IN
    target
    (cd "$BACKEND_DIR" && "${backend_env[@]}" ./bin/migrate loadgen-seed --users "${USERS:-100}" --quota "${QUOTA:-500}" \
      --queue="${QUEUE:-true}" --open-in "${OPEN_IN:-75s}" --out "$RESULTS/seed.json")
    ;;
  check) # SETTLE=4m menunggu hold/order yang ditinggal habis
    (cd "$BACKEND_DIR" && "${backend_env[@]}" ./bin/migrate loadgen-check --seed "$RESULTS/seed.json" \
      --settle "${SETTLE:-0}" --converge "${CONVERGE:-60s}" ${CHAOS:+--allow-needs-refund} --json "$RESULTS/${NAME:-check}-integrity.json")
    ;;
  run) # run <browse|war|war-noqueue|soak>; CHAOS=redis|redis-kill|redis-flush|worker untuk gangguan saat war
    scenario="${1:?pemakaian: run.sh run <browse|war|war-noqueue|soak>}"
    users="${USERS:-100}"
    name="${NAME:-$scenario-$users${CHAOS:+-chaos-$CHAOS}}"
    script="$scenario"; queue=true; quota="${QUOTA:-500}"
    case "$scenario" in
      browse) ;;
      war) ;;
      war-noqueue) script=war; queue=false ;;
      soak) queue=false; quota="${QUOTA:-$users}" ;;
      *) echo "skenario tidak dikenal: $scenario" >&2; exit 2 ;;
    esac
    target
    "$0" start
    if [[ "$scenario" == browse ]]; then
      [[ -f "$RESULTS/seed.json" ]] || USERS="$users" QUEUE=false "$0" seed
    else
      USERS="$users" QUOTA="$quota" QUEUE="$queue" OPEN_IN="${OPEN_IN:-$([[ $scenario == soak ]] && echo 5s || echo 75s)}" "$0" seed
    fi
    if [[ -n "${CHAOS:-}" ]]; then
      ( sleep "${CHAOS_AT:-95}"
        echo "$(date +%T) CHAOS $CHAOS mulai" | tee -a "$RESULTS/$name-chaos.log"
        case "$CHAOS" in
          redis) "${COMPOSE[@]}" stop redis; sleep "${CHAOS_DOWN:-8}"; "${COMPOSE[@]}" start redis ;;
          redis-flush) "${COMPOSE[@]}" exec -T redis redis-cli FLUSHALL ;; # Redis kehilangan semua kunci, tanpa mati
          redis-kill) "${COMPOSE[@]}" kill -s SIGKILL redis; sleep "${CHAOS_DOWN:-8}"; "${COMPOSE[@]}" start redis ;;
          worker) "$0" stop-worker; sleep "${CHAOS_DOWN:-25}"; "$0" start-worker ;;
        esac >>"$RESULTS/$name-chaos.log" 2>&1
        echo "$(date +%T) CHAOS $CHAOS selesai" | tee -a "$RESULTS/$name-chaos.log" ) &
    fi
    rc=0
    docker run --rm -i --user "$(id -u):$(id -g)" --ulimit nofile=65536:65536 \
      --add-host host.docker.internal:host-gateway \
      -v "$ROOT/loadtest:/loadtest:ro" -v "$RESULTS:/results" \
      -e BASE_URL="${BASE_URL:-http://host.docker.internal:18080}" -e SEED_FILE=/results/seed.json \
      -e USERS="$users" -e VUS="${VUS:-}" -e DURATION="${DURATION:-}" -e BUY_RATE="${BUY_RATE:-}" \
      -e ARRIVAL="${ARRIVAL:-}" -e WAR_MINUTES="${WAR_MINUTES:-}" -e ABANDON="${ABANDON:-}" -e CHAOS="${CHAOS:-}" \
      "$K6_IMAGE" run --quiet --summary-trend-stats "avg,min,med,p(90),p(95),p(99),max" \
      --summary-export "/results/$name-summary.json" "/loadtest/$script.js" 2>&1 | tee "$RESULTS/$name-k6.log" || rc=$?
    wait || true
    if [[ "$scenario" != browse ]]; then
      NAME="$name" SETTLE="${SETTLE:-4m}" "$0" check 2>&1 | tee "$RESULTS/$name-integrity.log" || rc=1
    fi
    [[ $rc -eq 0 ]] && echo "✓ $name: ambang k6 dan integritas lulus" || echo "✗ $name: ada ambang/pemeriksaan yang gagal (rc=$rc)"
    exit $rc
    ;;
  clean) # hentikan proses, hapus DB/Redis uji (volume) dan semua hasil + token sintetis
    "$0" stop
    "${COMPOSE[@]}" down -v --remove-orphans
    rm -rf "$RESULTS"
    echo "✓ infra uji beban, data sintetis, dan loadtest/results/ dihapus"
    ;;
  env) printf '%s\n' "${backend_env[@]:5}" ;;
  *) echo "pemakaian: run.sh up|seed|start|stop|run <skenario>|check|clean" >&2; exit 2 ;;
esac
