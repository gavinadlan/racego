#!/usr/bin/env node
// Simulasi antrian virtual (waiting room) terhadap API DEVELOPMENT di localhost.
// Identitas pengguna palsu memakai header X-User-ID, yang hanya diterima saat APP_ENV=development,
// jadi skrip ini menolak berjalan terhadap API lain. Tanpa dependensi npm (fetch bawaan Node ≥ 20).
//
//   node scripts/queue-sim.mjs load --users 10000 --quota 200 [--spawn]
//       Buat event uji ber-antrian (lewat endpoint mitra, akun contoh Sari / Jakarta Runners),
//       masukkan N pengguna ke lobi, tunggu jam buka, lalu tiap pengguna polling mengikuti
//       poll_after_ms dan langsung reservasi saat dipanggil. Event uji dihapus di akhir.
//       --spawn: jalankan API + worker khusus (backend/bin, port 18080, TRUSTED_PROXY_HOPS=1)
//       supaya tiap pengguna palsu bisa membawa IP sendiri (X-Forwarded-For); tanpa itu batas
//       join per IP menahan ribuan pengguna dari satu laptop. Dipakai `make queue-load`.
//   node scripts/queue-sim.mjs demo
//       Event demo ber-antrian: kuota 50, lobi sudah dibuka, penjualan 3 menit lagi. Dipakai `make queue-demo`.
//   node scripts/queue-sim.mjs bots --users 500 --event <id|slug> [--reserve-rate 0.7]
//       Isi antrian event dengan pengguna palsu yang bertingkah seperti manusia: sebagian keluar,
//       sebagian menutup tab, sebagian dipanggil tapi tidak membeli, sisanya reservasi.
//
// Opsi umum: --api http://localhost:8080
import { execFile, execFileSync, spawn } from "node:child_process";
import { createWriteStream, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const OWNER = "22222222-2222-2222-2222-222222222222"; // Sari, owner Jakarta Runners (seed)
const ORG = "jakarta-runners";
const WEB = process.env.FRONTEND_URL || "http://localhost:3000";

// ===== Argumen =====
const [mode, ...rest] = process.argv.slice(2);
const opt = {};
for (let i = 0; i < rest.length; i++) {
  if (!rest[i].startsWith("--")) continue;
  const k = rest[i].slice(2);
  opt[k] = rest[i + 1] && !rest[i + 1].startsWith("--") ? rest[++i] : true;
}
if (!["load", "bots", "demo"].includes(mode)) {
  console.error("pemakaian: node scripts/queue-sim.mjs load|bots|demo [opsi], lihat komentar di atas berkas ini");
  process.exit(2);
}
let API = String(opt.api || (opt.spawn ? "http://localhost:18080" : "http://localhost:8080")).replace(/\/$/, "");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);

// ===== HTTP =====
const lat = { status: [], reserve: [], join: [] };
const counts = { unexpected: 0, limited: 0 };
const unexpectedSamples = [];

function unexpected(what, detail) {
  counts.unexpected++;
  if (unexpectedSamples.length < 10) unexpectedSamples.push(`${what}: ${detail}`);
}

async function call(method, path, { user, ip, body, headers = {}, metric } = {}) {
  const h = { ...headers };
  if (user) h["X-User-ID"] = user;
  if (ip) h["X-Forwarded-For"] = ip;
  if (body !== undefined) h["Content-Type"] = "application/json";
  const t0 = performance.now();
  let res;
  try {
    res = await fetch(API + path, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch (e) {
    return { status: 0, body: { error: "network", message: String(e.cause?.code || e.message) } };
  }
  const text = await res.text();
  if (metric) lat[metric].push(performance.now() - t0);
  let data = {};
  try {
    data = text ? JSON.parse(text) : {};
  } catch {
    data = { raw: text };
  }
  return { status: res.status, body: data };
}

async function must(method, path, opts, want) {
  const r = await call(method, path, opts);
  if (r.status !== want) throw new Error(`${method} ${path}: ${r.status} ${JSON.stringify(r.body)}`);
  return r.body;
}

// ===== Pengaman: hanya API development di localhost =====
async function guard() {
  const u = new URL(API);
  if (!["localhost", "127.0.0.1", "[::1]"].includes(u.hostname)) {
    console.error(`Menolak: ${API} bukan localhost. Skrip ini hanya untuk API development.`);
    process.exit(1);
  }
  const h = await call("GET", "/healthz");
  if (h.status !== 200 || h.body.env !== "development") {
    console.error(`Menolak: ${API}/healthz tidak menunjukkan APP_ENV=development (${h.status} ${JSON.stringify(h.body)}).`);
    process.exit(1);
  }
  return h.body;
}

// ===== API + worker khusus (--spawn) =====
const children = [];
async function spawnBackend() {
  const dir = mkdtempSync(join(tmpdir(), "racego-queue-load-"));
  // Mode mock Midtrans: uji beban tidak boleh memakai kunci Midtrans sungguhan dari .env.
  const env = { ...process.env, APP_ENV: "development", HTTP_ADDR: ":18080", TRUSTED_PROXY_HOPS: "1", MIDTRANS_SERVER_KEY: "" };
  for (const name of ["api", "worker"]) {
    const out = createWriteStream(join(dir, `${name}.log`));
    const p = spawn(join(ROOT, "backend/bin", name), [], { cwd: join(ROOT, "backend"), env, stdio: ["ignore", "pipe", "pipe"] });
    p.stdout.pipe(out);
    p.stderr.pipe(out);
    p.on("exit", (code) => code && code !== 143 && !stopping && console.error(`${name} berhenti (kode ${code}), log: ${dir}/${name}.log`));
    children.push(p);
  }
  for (let i = 0; i < 50; i++) {
    if ((await call("GET", "/healthz")).status === 200) return dir;
    await sleep(200);
  }
  throw new Error(`API khusus tidak menyala, log: ${dir}`);
}
let stopping = false;
function stopChildren() {
  stopping = true;
  for (const p of children) p.kill("SIGTERM");
}
process.on("SIGINT", () => {
  stopChildren();
  process.exit(130);
});

// ===== Redis (opsional, untuk memori & konsistensi) lewat container dev =====
function redis(...args) {
  try {
    return execFileSync("docker", ["compose", "exec", "-T", "redis", "redis-cli", ...args], { cwd: ROOT, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim();
  } catch {
    return null;
  }
}
// Versi async untuk dipakai selama uji berjalan (versi sinkron menahan event loop & mengacaukan latensi).
const redisAsync = (...args) =>
  new Promise((resolve) =>
    execFile("docker", ["compose", "exec", "-T", "redis", "redis-cli", ...args], { cwd: ROOT, encoding: "utf8", maxBuffer: 64 << 20 }, (err, out) =>
      resolve(err ? null : out.trim()),
    ),
  );
const memOf = (info) => {
  const m = info && /used_memory:(\d+)/.exec(info);
  return m ? Number(m[1]) : null;
};
const redisMemory = () => memOf(redis("INFO", "memory"));
const mb = (b) => (b === null ? "tidak terukur" : `${(b / 1048576).toFixed(1)} MB`);

// ===== Event uji =====
const wibISO = (ms) => new Date(ms + 7 * 3600_000).toISOString().replace(/\.\d+Z$/, "+07:00");

async function createEvent({ name, openAt, quota, maxPerUser = 1 }) {
  const raceDay = new Date(Date.now() + 60 * 86400_000 + 7 * 3600_000).toISOString().slice(0, 10);
  const body = {
    name,
    tagline: "Event uji antrian virtual",
    description: "Dibuat otomatis oleh scripts/queue-sim.mjs.",
    venue: "Monas",
    city: "Jakarta",
    race_date: raceDay,
    start_time: "05:30",
    sale_open_at: wibISO(openAt),
    sale_close_at: wibISO(Date.now() + 2 * 86400_000),
    racepack_info: "",
    theme_color: "",
    cover_url: "",
    waiting_room: true,
    categories: [{ code: "Q5K", name: "Uji antrian 5K", distance_km: 5, price: 100000, quota, max_per_user: maxPerUser, bib_prefix: String(100 + Math.floor(Math.random() * 900)) }],
  };
  const ev = await must("POST", `/v1/org/${ORG}/events`, { user: OWNER, body }, 201);
  const pub = await must("POST", `/v1/org/${ORG}/events/${ev.id}/publish`, { user: OWNER }, 200);
  return { id: pub.id, categoryId: pub.categories[0].id };
}

async function deleteEvent(ev) {
  await call("POST", `/v1/org/${ORG}/events/${ev.id}/unpublish`, { user: OWNER });
  const r = await call("DELETE", `/v1/org/${ORG}/events/${ev.id}`, { user: OWNER });
  if (r.status !== 204) log(`event uji tidak terhapus (${r.status} ${r.body.message ?? ""}); hapus manual dari dashboard Jakarta Runners`);
  // Sisa key Redis antrian & kategori (stok sudah dihapus saat event dihapus).
  for (const pattern of [`{ev:${ev.id}}:*`, `{cat:${ev.categoryId}}:*`]) {
    const keys = (redis("--scan", "--pattern", pattern) || "").split("\n").filter(Boolean);
    for (let i = 0; i < keys.length; i += 500) redis("DEL", ...keys.slice(i, i + 500));
  }
}

const summary = (ev) => call("GET", `/v1/org/${ORG}/events/${ev.id}/queue`, { user: OWNER }).then((r) => r.body);

// ===== Statistik =====
function pct(arr, p) {
  if (!arr.length) return NaN;
  const s = [...arr].sort((a, b) => a - b);
  return s[Math.min(s.length - 1, Math.floor((p / 100) * s.length))];
}
const ms = (v) => (Number.isNaN(v) ? "-" : `${v.toFixed(0)} ms`);
const sec = (v) => (Number.isNaN(v) ? "-" : `${(v / 1000).toFixed(1)} s`);

/** Korelasi peringkat Spearman antara dua urutan (pasangan [a, b], tanpa peringkat kembar). */
function spearman(pairs) {
  const n = pairs.length;
  if (n < 3) return NaN;
  const rank = (i) => {
    const idx = pairs.map((p, j) => [p[i], j]).sort((x, y) => x[0] - y[0]);
    const r = new Array(n);
    idx.forEach(([, j], k) => (r[j] = k));
    return r;
  };
  const ra = rank(0);
  const rb = rank(1);
  let d2 = 0;
  for (let j = 0; j < n; j++) d2 += (ra[j] - rb[j]) ** 2;
  return 1 - (6 * d2) / (n * (n * n - 1));
}

async function pool(items, size, fn) {
  let i = 0;
  await Promise.all(
    Array.from({ length: Math.min(size, items.length) }, async () => {
      while (i < items.length) await fn(items[i++]);
    }),
  );
}

// ===== Mode load =====
async function load() {
  const users = Number(opt.users || 10000);
  const quota = Number(opt.quota || 200);
  const xff = !!opt.spawn;
  let logDir = null;
  if (opt.spawn) {
    log("menyalakan API + worker khusus di :18080 …");
    logDir = await spawnBackend();
  }
  const health = await guard();
  if (!health.midtrans_mock) log("catatan: Midtrans bukan mode mock (skrip ini tidak membayar, jadi tidak ada transaksi)");

  const memBefore = redisMemory();
  const openIn = Number(opt["open-in"] || Math.ceil(15 + users / 400)) * 1000;
  const openAt = Date.now() + openIn;
  const ev = await createEvent({ name: `Uji Antrian ${new Date().toISOString().slice(11, 19)}`, openAt, quota });
  log(`event uji ${ev.id}, kuota ${quota}, jam buka dalam ${openIn / 1000} detik, ${users} pengguna`);

  const U = Array.from({ length: users }, (_, i) => ({
    i,
    id: crypto.randomUUID(),
    ip: `10.${(i >> 13) & 255}.${(i >> 5) & 255}.${(i & 31) + 1}`, // ±32 pengguna per IP (mis. NAT kampus)
    joinedSeq: -1,
    admittedAt: null,
    admittedSeq: -1,
    admits: 0,
    done: false,
    outcome: null,
  }));
  const opts = (u) => ({ user: u.id, ip: xff ? u.ip : undefined });

  // 1. Semua masuk lobi.
  let joinSeq = 0;
  let lateJoins = 0;
  const t0 = Date.now();
  await pool(U, 200, async (u) => {
    const r = await call("POST", `/v1/events/${ev.id}/queue`, { ...opts(u), metric: "join" });
    if (r.status !== 201) return unexpected("join", `${r.status} ${JSON.stringify(r.body)}`);
    u.joinedSeq = joinSeq++;
    u.state = r.body;
    u.lobby = r.body.state === "lobby";
    if (r.body.state !== "lobby") lateJoins++;
  });
  log(`${joinSeq} pengguna bergabung dalam ${sec(Date.now() - t0)} (${lateJoins} setelah jam buka)`);

  // Urutan antrian yang dibekukan (langsung dari Redis), untuk uji keadilan atas semua peserta.
  let frozen = null;
  // 2. Pantau ringkasan (pass aktif maksimum, laju panggil).
  let maxActive = 0;
  const admitSamples = [];
  let monitoring = true;
  const monitor = (async () => {
    while (monitoring) {
      const s = await summary(ev);
      if (s.active > maxActive) maxActive = s.active;
      admitSamples.push([Date.now(), s.admitted_total ?? 0]);
      await sleep(500);
    }
  })();
  // Snapshot sesaat sebelum jam buka: urutan lobi = urutan skor acak = urutan pemanggilan.
  const snapshot = (async () => {
    if (Date.now() >= openAt) return;
    await sleep(Math.max(0, openAt - Date.now() - 1000));
    const z = await redisAsync("ZRANGE", `{ev:${ev.id}}:queue`, "0", "-1");
    if (z) frozen = z.split("\n");
  })();

  // 3. Klien: polling mengikuti poll_after_ms, reservasi saat dipanggil.
  let admittedSeq = 0;
  let reserved = 0;
  let soldOutAtReserve = 0;
  const memPeak = { v: memBefore ?? 0 };
  const client = async (u) => {
    let st = u.state;
    while (!u.done && !stopAll) {
      await sleep(st.poll_after_ms || 2000);
      if (stopAll) return;
      const r = await call("GET", `/v1/events/${ev.id}/queue/me`, { ...opts(u), metric: "status" });
      if (r.status === 429) {
        counts.limited++;
        await sleep((r.body.retry_after ?? 1) * 1000);
        continue;
      }
      if (r.status !== 200) {
        unexpected("status", `${r.status} ${JSON.stringify(r.body)}`);
        continue;
      }
      st = r.body;
      if (st.state === "admitted") {
        if (u.admittedAt === null) {
          u.admittedAt = Date.now();
          u.admittedSeq = admittedSeq++;
        }
        u.admits++;
        const res = await call("POST", `/v1/categories/${ev.categoryId}/reservations`, {
          ...opts(u),
          body: { qty: 1 },
          headers: { "X-Queue-Pass": st.pass },
          metric: "reserve",
        });
        if (res.status === 201) {
          reserved++;
          u.outcome = "reserved";
        } else if (res.status === 409 && res.body.error === "sold_out") {
          soldOutAtReserve++;
          u.outcome = "sold_out";
          await call("DELETE", `/v1/events/${ev.id}/queue/me`, opts(u));
        } else {
          unexpected("reserve", `${res.status} ${JSON.stringify(res.body)}`);
          u.outcome = "error";
        }
        u.done = true;
      } else if (st.state === "sold_out" || st.state === "closed") {
        u.outcome = st.state;
        u.done = true;
      } else if (st.state === "not_in_queue") {
        unexpected("status", "keluar antrian tanpa sebab (tanda hidup?)");
        u.outcome = "dropped";
        u.done = true;
      }
    }
  };
  let stopAll = false;
  const joined = U.filter((u) => u.joinedSeq >= 0);
  const running = Promise.all(joined.map(client));

  // 4. Selesai saat semua klien selesai, atau stok habis (sisa antrian menunggu hold kedaluwarsa).
  const deadline = Date.now() + Number(opt.timeout || 600) * 1000;
  let soldOutSince = null;
  while (Date.now() < deadline) {
    await sleep(1000);
    const m = memOf(await redisAsync("INFO", "memory"));
    if (m !== null && m > memPeak.v) memPeak.v = m;
    if (joined.every((u) => u.done)) break;
    const stock = await redisAsync("GET", `{cat:${ev.categoryId}}:stock`);
    if (stock === "0" || reserved >= quota) {
      soldOutSince ??= Date.now();
      // Beri waktu peserta yang sudah dipanggil (pass maks 10 menit) untuk melihat statusnya.
      const s = await summary(ev);
      if (s.active === 0 && Date.now() - soldOutSince > 8000) break;
    }
  }
  stopAll = true;
  await running;
  monitoring = false;
  await monitor;
  await snapshot;
  const end = Date.now();

  // 5. Laporan.
  const s = await summary(ev);
  const evDetail = await must("GET", `/v1/org/${ORG}/events/${ev.id}`, { user: OWNER }, 200);
  const cat = evDetail.categories[0];
  const stock = Number(redis("GET", `{cat:${ev.categoryId}}:stock`) ?? cat.remaining ?? NaN);
  const holds = Number(redis("ZCARD", `{cat:${ev.categoryId}}:holds`) ?? NaN);
  const admitted = joined.filter((u) => u.admittedAt !== null);
  const twice = joined.filter((u) => u.admits > 1).length;
  const waits = admitted.map((u) => u.admittedAt - openAt);
  // Laju panggil aktual: dari admitted_total, selama penjualan berjalan sampai pemanggilan berhenti.
  const sel = admitSamples.filter(([t]) => t >= openAt);
  const lastGrow = sel.findLastIndex(([, n], k) => k > 0 && n > sel[k - 1][1]);
  const rate = lastGrow > 0 ? (sel[lastGrow][1] - sel[0][1]) / ((sel[lastGrow][0] - sel[0][0]) / 1000) : NaN;
  const rhoAdmitted = spearman(admitted.map((u) => [u.joinedSeq, u.admittedSeq]));
  const pos = frozen ? new Map(frozen.map((id, k) => [id, k])) : null;
  const rhoAll = pos ? spearman(joined.filter((u) => u.lobby && pos.has(u.id)).map((u) => [u.joinedSeq, pos.get(u.id)])) : NaN;
  const waitingLeft = joined.filter((u) => !u.done).length;
  const memAfter = redisMemory();

  const ok = {
    "reservasi sukses = kuota": reserved === Math.min(quota, joined.length),
    "tanpa oversell": reserved <= quota && stock >= 0,
    "tidak ada user dipanggil dua kali": twice === 0 && s.admitted_total === admitted.length,
    "pass aktif ≤ MAX_ACTIVE": maxActive <= (s.max_active ?? Infinity),
    "konsistensi stok + hold + terjual = kuota": stock + holds + cat.sold === quota,
    "galat tak terduga = 0": counts.unexpected === 0,
    "keadilan |rho| < 0,1": Math.abs(Number.isNaN(rhoAll) ? rhoAdmitted : rhoAll) < 0.1,
  };

  console.log(`
===== Hasil queue-sim load =====
Pengguna                 ${users} (bergabung ${joined.length}, ${lateJoins} setelah jam buka)
Kuota                    ${quota}
Reservasi sukses         ${reserved}   (dipanggil saat stok sudah habis: ${soldOutAtReserve})
Dipanggil (pass)         ${admitted.length}   admitted_total server: ${s.admitted_total}   dipanggil >1 kali: ${twice}
Masih menunggu di akhir  ${waitingLeft}   (stok habis; slot hanya kembali bila hold tidak dibayar)
Pass aktif maks (sampel) ${maxActive} / MAX_ACTIVE ${s.max_active}
Laju panggil aktual      ${Number.isNaN(rate) ? "-" : rate.toFixed(1)} /detik (QUEUE_ADMIT_PER_SEC ${s.admit_per_sec})
Waktu tunggu (buka→dipanggil)  p50 ${sec(pct(waits, 50))}  p95 ${sec(pct(waits, 95))}  maks ${sec(pct(waits, 100))}
Latensi join             p50 ${ms(pct(lat.join, 50))}  p95 ${ms(pct(lat.join, 95))}  p99 ${ms(pct(lat.join, 99))}  (${lat.join.length} req)
Latensi queue/me         p50 ${ms(pct(lat.status, 50))}  p95 ${ms(pct(lat.status, 95))}  p99 ${ms(pct(lat.status, 99))}  (${lat.status.length} req)
Latensi reserve          p50 ${ms(pct(lat.reserve, 50))}  p95 ${ms(pct(lat.reserve, 95))}  p99 ${ms(pct(lat.reserve, 99))}  (${lat.reserve.length} req)
Keadilan (Spearman)      urutan masuk lobi vs urutan antrian saat buka: ${Number.isNaN(rhoAll) ? "tidak terukur" : rhoAll.toFixed(4)}
                         urutan masuk lobi vs urutan dipanggil (${admitted.length} orang): ${Number.isNaN(rhoAdmitted) ? "-" : rhoAdmitted.toFixed(4)}
Konsistensi akhir        stok Redis ${stock} + hold ${holds} + terjual ${cat.sold} = ${stock + holds + cat.sold} (kuota ${quota})
Memori Redis             sebelum ${mb(memBefore)}, puncak ${mb(memPeak.v || null)}, sesudah ${mb(memAfter)}
Galat tak terduga        ${counts.unexpected}${unexpectedSamples.length ? "\n  " + unexpectedSamples.join("\n  ") : ""}
Dibatasi (429)           ${counts.limited}
Durasi total             ${sec(end - t0)} (sejak join pertama)
${Object.entries(ok)
  .map(([k, v]) => `${v ? "LULUS" : "GAGAL"}  ${k}`)
  .join("\n")}
`);
  // Worker khusus dihentikan dulu, supaya tidak membuat ulang key antrian event yang dihapus.
  const worker = children.find((p) => p.spawnfile.endsWith("worker"));
  if (worker) {
    worker.kill("SIGTERM");
    await new Promise((r) => worker.once("exit", r));
  }
  await deleteEvent(ev);
  log("event uji dihapus");
  if (logDir) {
    stopChildren();
    await sleep(500);
    const errs = execFileSync("sh", ["-c", `grep -hE 'level=(ERROR|WARN)' ${logDir}/*.log | grep -v 'mode MOCK' | head -5 || true`], { encoding: "utf8" });
    console.log(errs ? `Log API/worker berisi error/warning:\n${errs}` : `Log API/worker bersih dari error/warning (${logDir})`);
  }
  process.exit(Object.values(ok).every(Boolean) ? 0 : 1);
}

// ===== Mode demo =====
async function demo() {
  await guard();
  const openAt = Date.now() + 3 * 60_000;
  const ev = await createEvent({ name: `Demo Antrian ${new Date().toISOString().slice(11, 16)}`, openAt, quota: Number(opt.quota || 50), maxPerUser: 2 });
  console.log(`
Event demo ber-antrian siap: kuota ${opt.quota || 50}, lobi sudah dibuka, penjualan dibuka ${new Date(openAt).toLocaleTimeString("id-ID")}.

  Buka:   ${WEB}/events/${ev.id}
  Mitra:  ${WEB}/org/${ORG}/events/${ev.id}/edit   (akun contoh Sari)

Isi antrian dengan pengguna palsu (terminal lain):
  node scripts/queue-sim.mjs bots --users 500 --event ${ev.id}
`);
}

// ===== Mode bots =====
async function bots() {
  await guard();
  const n = Number(opt.users || 500);
  const reserveRate = Number(opt["reserve-rate"] ?? 0.7);
  let eventId = String(opt.event || "");
  if (!/^[0-9a-f-]{36}$/.test(eventId)) {
    const list = await must("GET", "/v1/events?scope=all&limit=200", {}, 200);
    eventId = list.events.find((e) => e.slug === eventId)?.id;
    if (!eventId) throw new Error(`event "${opt.event}" tidak ditemukan`);
  }
  const ev = await must("GET", `/v1/events/${eventId}`, {}, 200);
  if (!ev.waiting_room) throw new Error("event ini tidak memakai antrian");
  const saleOpen = Date.parse(ev.sale_open_at);
  // Batas join per IP (semua bot dari laptop ini): sebar kedatangan, sisakan ruang untuk akun sungguhan.
  const spread = Number(opt.spread || Math.max(30, Math.min(150, (saleOpen - Date.now()) / 1000 - 10)));
  log(`${n} bot ke "${ev.name}", datang tersebar ${spread.toFixed(0)} detik, ${Math.round(reserveRate * 100)}% membeli bila dipanggil`);

  const tally = { joined: 0, left: 0, closedTab: 0, reserved: 0, noBuy: 0, soldOut: 0, other: 0 };
  const bot = async (k) => {
    const id = crypto.randomUUID();
    await sleep(Math.random() * spread * 1000);
    const pick = Math.random();
    const plan = pick < 0.08 ? "leave" : pick < 0.13 ? "closeTab" : "stay";
    let st;
    for (;;) {
      const r = await call("POST", `/v1/events/${eventId}/queue`, { user: id });
      if (r.status === 429) {
        await sleep((r.body.retry_after ?? 5) * 1000);
        continue;
      }
      if (r.status !== 201 && r.status !== 200) return void tally.other++;
      st = r.body;
      break;
    }
    tally.joined++;
    const giveUpAt = Date.now() + (plan === "stay" ? Infinity : 20_000 + Math.random() * 90_000);
    for (;;) {
      await sleep(st.poll_after_ms || 3000);
      if (Date.now() > giveUpAt) {
        if (plan === "leave") {
          await call("DELETE", `/v1/events/${eventId}/queue/me`, { user: id });
          tally.left++;
        } else tally.closedTab++; // berhenti polling: dibuang saat sampai depan antrian
        return;
      }
      const r = await call("GET", `/v1/events/${eventId}/queue/me`, { user: id });
      if (r.status === 429) continue;
      if (r.status !== 200) return void tally.other++;
      st = r.body;
      if (st.state === "sold_out" || st.state === "closed" || st.state === "not_in_queue") return;
      if (st.state !== "admitted") continue;
      await sleep(1000 + Math.random() * 4000); // memilih kategori
      if (Math.random() >= reserveRate) {
        tally.noBuy++;
        if (Math.random() < 0.5) await call("DELETE", `/v1/events/${eventId}/queue/me`, { user: id });
        return;
      }
      const cat = ev.categories[k % ev.categories.length];
      const res = await call("POST", `/v1/categories/${cat.id}/reservations`, { user: id, body: { qty: 1 }, headers: { "X-Queue-Pass": st.pass } });
      if (res.status === 201) tally.reserved++;
      else if (res.body.error === "sold_out") {
        tally.soldOut++;
        await call("DELETE", `/v1/events/${eventId}/queue/me`, { user: id });
      } else tally.other++;
      return;
    }
  };
  const ticker = setInterval(() => log(JSON.stringify(tally)), 5000);
  await Promise.all(Array.from({ length: n }, (_, k) => bot(k)));
  clearInterval(ticker);
  log("selesai", JSON.stringify(tally));
}

try {
  await { load, demo, bots }[mode]();
} catch (e) {
  console.error(e.message);
  stopChildren();
  process.exit(1);
}
