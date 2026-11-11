// Skenario soak: beban sedang 10–15 menit (jelajah + pembelian stabil, event tanpa antrean berkuota
// besar) untuk melihat kebocoran memori/goroutine/koneksi, backlog outbox, dan pertumbuhan kunci Redis.
//   make loadtest SCENARIO=soak USERS=3000 DURATION=12m BUY_RATE=3 VUS=30
import http from "k6/http";
import { sleep } from "k6";
import exec from "k6/execution";
import { Counter } from "k6/metrics";
import { BASE, call, errCode, guard, headers, meta, rand, unexpected, users } from "./lib.js";

const DURATION = __ENV.DURATION || "12m";
const BUY_RATE = Number(__ENV.BUY_RATE || 3); // pembelian baru per detik
const VUS = Number(__ENV.VUS || 30); // pengunjung yang hanya menjelajah
const bought = new Counter("outcome_ticket");
const soldOut = new Counter("outcome_sold_out");

export const options = {
  scenarios: {
    browse: { executor: "constant-vus", vus: VUS, duration: DURATION, exec: "browse" },
    buy: { executor: "constant-arrival-rate", rate: BUY_RATE, timeUnit: "1s", duration: DURATION, preAllocatedVUs: BUY_RATE * 20, maxVUs: BUY_RATE * 60, exec: "buy" },
  },
  thresholds: {
    http_req_failed: ["rate<0.01"],
    unexpected: ["count==0"],
    "http_req_duration{name:reserve}": ["p(95)<300"],
    "http_req_duration{name:order_create}": ["p(95)<800"],
    "http_req_duration{name:event_list}": ["p(95)<300"],
  },
};

export function setup() {
  guard();
}

export function browse() {
  const u = { ip: users[(exec.vu.idInTest - 1) % users.length].ip };
  const r = http.get(`${BASE}/v1/events?scope=upcoming`, { headers: headers(u), tags: { name: "event_list" } });
  if (r.status !== 200) unexpected.add(1, { name: "event_list" });
  sleep(rand(1, 3));
  http.get(`${BASE}/v1/events/${meta.event_id}`, { headers: headers(u), tags: { name: "event_detail" } });
  sleep(rand(1, 3));
}

// Tiap iterasi = pembeli berikutnya (bergilir), satu kategori acak. Pembeli yang sudah punya slot di
// kategori itu mendapat limit_exceeded: jawaban sah, dihitung sebagai "habis untuk dia".
export function buy() {
  const n = exec.scenario.iterationInTest;
  const u = users[n % users.length];
  const c = meta.categories[Math.floor(Math.random() * meta.categories.length)];
  const r = call("POST", `/v1/categories/${c.id}/reservations`, u, "reserve", { qty: 1 });
  if (!r || r.status !== 201) {
    const e = r ? errCode(r) : "";
    if (e === "sold_out" || e === "limit_exceeded") soldOut.add(1);
    else unexpected.add(1, { name: "reserve_" + (e || (r && r.status)) });
    return;
  }
  sleep(rand(1, 4));
  const order = call("POST", "/v1/orders", u, "order_create", {
    category_id: c.id,
    reservation_id: r.json("reservation_id"),
    participants: [{ full_name: `Pelari Soak ${n}`, email: `lt-${n}@loadtest.invalid`, id_number: String(3170000000000000 + n), jersey_size: "L" }],
  }, { "Idempotency-Key": `soak-${meta.event_id.slice(0, 8)}-${n}` });
  if (!order || order.status !== 201) return unexpected.add(1, { name: "order" }), undefined;
  const id = order.json("order_id");
  sleep(rand(1, 3));
  const pay = call("POST", `/v1/dev/orders/${id}/simulate`, u, "pay_simulate", { status: "settlement" });
  if (!pay || pay.status !== 200) return unexpected.add(1, { name: "pay" }), undefined;
  for (let i = 0; i < 60; i++) {
    const o = call("GET", `/v1/orders/${id}`, u, "order_get");
    if (o && o.status === 200 && o.json("tickets_issued") >= 1) return bought.add(1), undefined;
    sleep(1);
  }
  unexpected.add(1, { name: "ticket_timeout" });
}
