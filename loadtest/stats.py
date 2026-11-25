#!/usr/bin/env python3
"""Sumber daya selama uji beban + ringkasan median (min–maks) antar-eksekusi. Hanya pustaka standar.

  stats.py sample <dir> <nama>       cuplik tiap ±2 dtk ke <dir>/<nama>-res.jsonl sampai dihentikan (dipanggil run.sh)
  stats.py report <dir> <awalan>...  satu baris per awalan dari <awalan>-r1, -r2, ... (Markdown)
"""
import glob, json, os, re, signal, statistics as st, subprocess, sys, threading, time, urllib.request

TOKEN = "loadtest-metrics-token-lokal-saja-bukan-rahasia"
SERIES = re.compile(r'^(go_goroutines|go_sched_latencies_seconds_bucket\S*|process_resident_memory_bytes|racego_db_pool_\w+'
                    r'|racego_http_requests_total\S*|racego_redis_command_duration_seconds_(?:bucket|count|sum)\{cmd="evalsha"\S*'
                    r'|racego_redis_pool_\w+|racego_queue_admitted_total|racego_public_cache\S*) (\S+)$')
sh = lambda *a: subprocess.run(a, capture_output=True, text=True).stdout


def scrape(port):
    try:
        rq = urllib.request.Request(f"http://127.0.0.1:{port}/metrics", headers={"Authorization": "Bearer " + TOKEN})
        with urllib.request.urlopen(rq, timeout=2) as r:
            return {m[1]: float(m[2]) for m in (SERIES.match(l) for l in r.read().decode().splitlines()) if m}
    except (OSError, ValueError):  # replika mati / metrik belum siap
        return None


def mib(s):
    n, u = re.match(r"([\d.]+)(\w+)", s).groups()
    return float(n) * {"B": 1 / 2**20, "KiB": 1 / 1024, "MiB": 1, "GiB": 1024}.get(u, 1)


def cputime(pid):
    p = sh("ps", "-o", "cputime=,rss=", "-p", str(pid)).split()
    if len(p) != 2:
        return None
    sec = 0.0
    for part in re.split("[:]", p[0].replace("-", ":")):
        sec = sec * 60 + float(part)
    return sec, int(p[1]) / 1024


def sample(out, name):
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
    idle = [None]

    def iostat():  # CPU seluruh mesin
        p = subprocess.Popen(["iostat", "-n", "0", "-w", "2"], stdout=subprocess.PIPE, text=True)
        for i, line in enumerate(p.stdout):
            f = line.split()
            if i > 2 and len(f) == 6 and f[2].isdigit():
                idle[0] = int(f[2])

    threading.Thread(target=iostat, daemon=True).start()
    prev = {}
    with open(f"{out}/{name}-res.jsonl", "w", buffering=1) as f:
        while True:
            row = {"t": time.time(), "c": {}, "m": {}, "idle": idle[0]}
            for line in sh("docker", "stats", "--no-stream", "--format", "{{json .}}").splitlines():
                d = json.loads(line)
                if d["Name"].startswith("racego-loadtest"):
                    row["c"][d["Name"].replace("racego-loadtest-", "")] = [float(d["CPUPerc"].rstrip("%") or 0), mib(d["MemUsage"])]
            for proc in ("api", "worker"):
                try:
                    pid = int(open(f"{out}/{proc}.pid").read())
                except (OSError, ValueError):
                    continue
                v, now = cputime(pid), time.time()
                if v and proc in prev:
                    row["c"][proc + "-native"] = [100 * (v[0] - prev[proc][0]) / (now - prev[proc][1]), v[1]]
                if v:
                    prev[proc] = (v[0], now)
            for port in [9464, 9465, *range(19470, 19480), *range(19480, 19484)]:
                if m := scrape(port):
                    row["m"][port] = m
            info = sh("docker", "exec", "racego-loadtest-redis-1", "redis-cli", "INFO", "all")
            row["redis"] = {k: float(v) for k, v in re.findall(r"(instantaneous_ops_per_sec|used_cpu_user|used_cpu_sys):([\d.]+)", info)}
            if m := re.search(r"cmdstat_evalsha:calls=(\d+),usec=(\d+)", info):
                row["redis"]["evalsha"] = [int(m[1]), int(m[2])]
            pg = sh("docker", "exec", "racego-loadtest-postgres-1", "psql", "-U", "racego", "-d", "racego_loadtest", "-Atc",
                    "SELECT count(*) FILTER (WHERE state='active'), count(*) FROM pg_stat_activity WHERE backend_type='client backend'").strip()
            row["pg"] = [int(x) for x in pg.split("|")] if pg else None
            if int(row["t"]) % 30 < 3:
                row["therm"] = [l for l in sh("pmset", "-g", "therm").splitlines() if "CPU_" in l]
                row["power"] = sh("pmset", "-g", "batt").splitlines()[:1]
            f.write(json.dumps(row) + "\n")
            time.sleep(0.5)


def quantile(b0, b1, q):  # batas atas bucket Prometheus yang memuat kuantil q
    d = sorted((float(le.replace("+Inf", "inf")), b1[le] - b0.get(le, 0)) for le in b1)
    if not d or d[-1][1] <= 0:
        return None
    return next(le for le, n in d if n >= q * d[-1][1])


def resources(path):
    rows = [json.loads(l) for l in open(path)]
    r = {}
    peak = lambda vals: max(vals, default=None)
    names = {n for x in rows for n in x["c"]}
    total = lambda x, p, i: sum(v[i] for n, v in x["c"].items() if n.startswith(p))
    for p in ("api", "worker", "postgres", "redis", "k6", "lb", "pgbouncer"):
        if any(n.startswith(p) for n in names):
            r[p + "_cpu"] = peak(total(x, p, 0) for x in rows)
            r[p + "_mib"] = peak(total(x, p, 1) for x in rows)
            if p == "api":
                r["api_one_cpu"] = peak(v[0] for x in rows for n, v in x["c"].items() if n.startswith("api"))
    r["idle_min"] = min((x["idle"] for x in rows if x["idle"] is not None), default=None)
    api = lambda x: [m for port, m in x["m"].items() if port == "9464" or port.startswith("1947")]
    s = lambda ms, k: sum(m.get(k, 0) for m in ms)
    reqs = lambda ms: sum(v for m in ms for k, v in m.items() if k.startswith("racego_http_requests_total"))
    r["goroutines"] = peak(s(api(x), "go_goroutines") for x in rows)
    r["pool_acquired"] = peak(s(api(x), "racego_db_pool_acquired_conns") for x in rows)
    r["pool_total"] = peak(s(api(x), "racego_db_pool_total_conns") for x in rows)
    r["pg_active"] = peak(x["pg"][0] for x in rows if x["pg"])
    r["pg_conns"] = peak(x["pg"][1] for x in rows if x["pg"])
    r["redis_ops"] = peak(x["redis"].get("instantaneous_ops_per_sec", 0) for x in rows)
    r["therm"] = any("CPU_Speed_Limit" in l and "100" not in l for x in rows for l in x.get("therm", []))
    r["power"] = sorted({p for x in rows for p in x.get("power", [])})
    if rows:
        # Penghitung dijumlah per replika (maks, karena replika yang di-restart kembali ke 0) dikurangi nilai awal.
        acc = {}
        for x in rows:
            for port, m in x["m"].items():
                if port == "9464" or port.startswith("1947"):
                    first = port not in acc
                    a = acc.setdefault(port, {})
                    for k, v in m.items():
                        # Deret yang baru muncul di tengah jalan (mis. evalsha pertama) mulai dari 0, bukan dari nilai cuplikan pertamanya.
                        lo, hi = a.get(k, (v, v) if first else (0, v))
                        a[k] = (lo, max(hi, v))
        delta = lambda k: sum(a[k][1] - a[k][0] for a in acc.values() if k in a)
        r["pool_wait"] = delta("racego_db_pool_wait_total")
        r["pool_wait_s"] = delta("racego_db_pool_acquire_duration_seconds_total")
        r["redis_pool_wait"], r["redis_pool_wait_s"] = delta("racego_redis_pool_wait_total"), delta("racego_redis_pool_wait_seconds_total")
        for key, pre in (("evalsha", 'racego_redis_command_duration_seconds_bucket{cmd="evalsha",le="'), ("sched", 'go_sched_latencies_seconds_bucket{le="')):
            b0, b1 = {}, {}
            for a in acc.values():
                for k, (lo, hi) in a.items():
                    if k.startswith(pre):
                        le = k[len(pre):].split('"')[0]
                        b0[le], b1[le] = b0.get(le, 0) + lo, b1.get(le, 0) + hi
            for q in (50, 95, 99):
                v = quantile(b0, b1, q / 100) if b1 else None
                r[f"{key}_p{q}_le_ms"] = None if v is None else v * 1000
        e = [x["redis"]["evalsha"] for x in rows if "evalsha" in x["redis"]]
        if e and e[-1][0] > e[0][0]:
            r["evalsha_server_us"] = (e[-1][1] - e[0][1]) / (e[-1][0] - e[0][0])
        rps = []
        for a, b in zip(rows, rows[1:]):
            if set(a["m"]) == set(b["m"]):
                rps.append((reqs(api(b)) - reqs(api(a))) / (b["t"] - a["t"]))
        r["peak_rps"] = peak(rps)
    return r


def one(out, name):
    d = json.load(open(f"{out}/{name}-summary.json"))["metrics"]
    g = lambda m, k: d.get(m, {}).get(k)
    r = {"rps": g("http_reqs", "rate"), "reqs": g("http_reqs", "count")}
    for k in ("reserve", "queue_status", "queue_join", "order_create", "event_detail", "event_list", "event_month", "pay_simulate", "order_get"):
        for q, s in (("med", "p50"), ("p(95)", "p95"), ("p(99)", "p99")):
            r[f"{k}_{s}"] = g(f"http_req_duration{{name:{k}}}", q)
    r["all_p95"] = g("http_req_duration", "p(95)")
    r["ticket_wait_p95"] = g("ticket_wait_ms", "p(95)")
    r["queue_wait_p95"] = g("queue_wait_ms", "p(95)")
    for k in ("unexpected", "rate_limited", "outcome_ticket", "outcome_failed", "outcome_still_waiting", "outcome_sold_out", "outcome_abandoned", "outcome_expired"):
        r[k] = g(k, "count") or 0
    hf = d.get("http_req_failed", {})
    r["failed_reqs"] = hf.get("passes", 0)
    r["thresholds_failed"] = [f"{m}: {t}" for m, v in d.items() for t, bad in v.get("thresholds", {}).items() if bad]
    try:
        r["integrity"] = json.load(open(f"{out}/{name}-integrity.json"))["ok"]
    except (OSError, KeyError):
        r["integrity"] = None
    try:
        r.update(resources(f"{out}/{name}-res.jsonl"))
    except OSError:
        pass
    return r


def fmt(v):
    return "-" if v is None else f"{v:.0f}" if abs(v) >= 100 else f"{v:.1f}"


def report(out, prefixes):
    data = {}
    for p in prefixes:
        names = sorted(os.path.basename(f)[:-len("-summary.json")] for f in glob.glob(f"{out}/{p}-r[0-9]*-summary.json"))
        runs = [one(out, n) for n in names]
        data[p] = runs
        if not runs:
            continue
        print(f"\n## {p} (n={len(runs)})")
        for k in runs[0]:
            vals = [x.get(k) for x in runs]
            if k in ("thresholds_failed", "integrity", "therm", "power"):
                print(f"  {k}: {vals}")
                continue
            vals = [v for v in vals if v is not None]
            if vals:
                print(f"  {k}: {fmt(st.median(vals))} ({fmt(min(vals))}–{fmt(max(vals))})")
    return data


if __name__ == "__main__":
    if len(sys.argv) >= 4 and sys.argv[1] == "sample":
        sample(sys.argv[2], sys.argv[3])
    elif len(sys.argv) >= 4 and sys.argv[1] == "report":
        report(sys.argv[2], sys.argv[3:])
    else:
        sys.exit(__doc__)
