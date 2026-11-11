// Pembantu bersama skrip k6 RaceGo (docs/11-performa.md).
import http from "k6/http";
import { sleep } from "k6";
import { SharedArray } from "k6/data";
import { Counter } from "k6/metrics";

export const BASE = (__ENV.BASE_URL || "http://host.docker.internal:18080").replace(/\/$/, "");

// Beban hanya boleh menyasar mesin ini: localhost atau nama host jaringan Docker lokal (tanpa titik).
// ALLOW_REMOTE_TARGET ada supaya penolakan ini eksplisit; jangan dipakai tanpa izin pemilik target.
const host = (/^https?:\/\/(\[[^\]]+\]|[^/:]+)/.exec(BASE) || [])[1] || "";
const local = ["localhost", "127.0.0.1", "[::1]", "host.docker.internal"].includes(host) || /^[a-z0-9-]+$/.test(host);
if (!local && __ENV.ALLOW_REMOTE_TARGET !== "saya-pemilik-target-ini") {
  throw new Error(`Menolak: BASE_URL=${BASE} bukan localhost/jaringan Docker lokal. Uji beban ini hanya untuk mesin sendiri.`);
}

// Hanya 5xx dan kegagalan jaringan yang dihitung http_req_failed; 4xx (habis, batas laju) adalah jawaban sah.
http.setResponseCallback(http.expectedStatuses({ min: 200, max: 499 }));

// Berkas keluaran `migrate loadgen-seed`. SharedArray: satu salinan di memori untuk semua VU.
const SEED = __ENV.SEED_FILE || "/results/seed.json";
export const users = new SharedArray("users", () => JSON.parse(open(SEED)).users);
const metaArr = new SharedArray("meta", () => {
  const s = JSON.parse(open(SEED));
  return [{ event_id: s.event_id, waiting_room: s.waiting_room, sale_open_at: Date.parse(s.sale_open_at), categories: s.categories }];
});
export const meta = metaArr[0];

export const unexpected = new Counter("unexpected"); // 5xx, jaringan, atau jawaban yang tidak dikenali
export const rateLimited = new Counter("rate_limited"); // 429

// setup(): pastikan targetnya API development ber-mode mock (tidak ada Midtrans sungguhan).
export function guard() {
  const r = http.get(`${BASE}/healthz`, { tags: { name: "healthz" } });
  if (r.status !== 200 || r.json("env") !== "development" || r.json("midtrans_mock") !== true) {
    throw new Error(`Menolak: ${BASE}/healthz bukan API development mode mock (${r.status} ${r.body})`);
  }
}

// Header satu pengguna sintetis: cookie sesi + IP sendiri (API uji memakai TRUSTED_PROXY_HOPS=1).
export function headers(u, extra) {
  const h = { "Content-Type": "application/json", "X-Forwarded-For": u.ip };
  if (u.t) h.Cookie = `racego_session=${u.t}`;
  return Object.assign(h, extra || {});
}

export const rand = (a, b) => a + Math.random() * (b - a);
export const errCode = (r) => {
  try {
    return r.json("error") || "";
  } catch (_) {
    return "";
  }
};

// call: satu request; 5xx/jaringan diulang dengan backoff eksponensial (1, 2, 4 … maks 8 dtk), seperti
// klien sungguhan. 429 menunggu retry_after. Mengembalikan respons terakhir.
export function call(method, path, u, name, body, extra, tries) {
  let res;
  for (let i = 0; i < (tries || 6); i++) {
    res = http.request(method, BASE + path, body === undefined ? null : JSON.stringify(body), { headers: headers(u, extra), tags: { name } });
    if (res.status === 429) {
      rateLimited.add(1);
      sleep(Math.min(Number(res.headers["Retry-After"]) || 1, 10) + rand(0, 0.5));
      continue;
    }
    if (res.status > 0 && res.status < 500) return res;
    unexpected.add(1, { name });
    sleep(Math.min(2 ** i, 8) * rand(0.8, 1.2));
  }
  return res;
}
