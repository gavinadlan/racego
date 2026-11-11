// Skenario war / war-noqueue: N pengguna datang dalam jendela ±ARRIVAL detik di sekitar jam buka.
// Tiap pengguna = satu VU yang bertingkah seperti klien nyata: masuk antrean → polling mengikuti
// poll_after_ms → dipanggil → reservasi → isi data → order → bayar (rute simulasi mock) → tunggu tiket.
// Sebagian menyerah di tengah (tutup tab saat antre, tidak jadi order, tidak jadi bayar).
// Event tanpa antrean (war-noqueue): pengguna menunggu jam buka lalu langsung berebut reservasi.
//   make loadtest SCENARIO=war USERS=500        make loadtest SCENARIO=war-noqueue USERS=500
import { sleep } from "k6";
import exec from "k6/execution";
import { Counter, Trend } from "k6/metrics";
import { call, errCode, guard, meta, rand, unexpected, users } from "./lib.js";

const USERS = Math.min(Number(__ENV.USERS || 100), users.length);
const ARRIVAL = Number(__ENV.ARRIVAL || 60); // detik sebelum/sesudah jam buka
const MAX_MIN = Number(__ENV.WAR_MINUTES || 6); // batas satu pengguna menunggu sebelum menyerah
const ABANDON = Number(__ENV.ABANDON || 0.15); // porsi pengguna yang menyerah di tengah
const QUOTA = meta.categories.reduce((a, c) => a + c.quota, 0);
const CHAOS = !!__ENV.CHAOS; // saat gangguan disengaja, 5xx dilaporkan tapi tidak menggagalkan ambang

const got = new Counter("outcome_ticket"); // tiket terbit
const soldOut = new Counter("outcome_sold_out"); // jawaban jelas "habis"
const abandoned = new Counter("outcome_abandoned"); // menyerah sendiri (bagian dari skenario)
const stillWaiting = new Counter("outcome_still_waiting"); // masih antre saat batas waktu uji
const expired = new Counter("outcome_expired"); // reservasi/pass kedaluwarsa sebelum selesai
const failed = new Counter("outcome_failed"); // berakhir karena error tak terduga
const reservedOK = new Counter("reserved_ok");
const queueWait = new Trend("queue_wait_ms", true); // masuk antrean → dipanggil
const ticketWait = new Trend("ticket_wait_ms", true); // bayar → tiket terbit
const buyTime = new Trend("buy_total_ms", true); // tiba → tiket terbit

const loose = CHAOS ? [] : ["count==0"];
export const options = {
  scenarios: {
    war: { executor: "per-vu-iterations", vus: USERS, iterations: 1, maxDuration: `${MAX_MIN + 3}m`, gracefulStop: "10s" },
  },
  thresholds: {
    outcome_ticket: [`count<=${QUOTA}`], // tidak pernah lebih dari kuota
    reserved_ok: ["count>=0"],
    unexpected: loose,
    outcome_failed: loose,
    http_req_failed: CHAOS ? [] : ["rate<0.01"],
    "http_req_duration{name:reserve}": ["p(95)<300"],
    "http_req_duration{name:queue_status}": ["p(95)<300"],
    "http_req_duration{name:queue_join}": ["p(95)<500"],
    "http_req_duration{name:order_create}": ["p(95)<800"],
    "http_req_duration{name:pay_simulate}": ["p(95)<800"],
    "http_req_duration{name:order_get}": ["p(95)<300"],
    queue_wait_ms: ["p(95)>=0"],
    ticket_wait_ms: ["p(95)<10000"],
    buy_total_ms: ["p(95)>=0"],
  },
};

export function setup() {
  guard();
  return { start: Date.now() };
}

// Antrean: mengembalikan pass, atau null bila berakhir (habis/menyerah/batas waktu; sudah dicatat).
function waitInQueue(u, deadline, quitAt) {
  const ev = `/v1/events/${meta.event_id}/queue`;
  let rejoin = 0;
  let r = call("POST", ev, u, "queue_join");
  const joined = Date.now();
  for (;;) {
    if (!r || r.status >= 500 || r.status === 0) return failed.add(1), null;
    if (Date.now() > deadline) return stillWaiting.add(1), null;
    if (quitAt && Date.now() > quitAt) {
      // Separuh keluar baik-baik, separuh menutup tab (berhenti polling; worker membuangnya dari antrean).
      if (Math.random() < 0.5) call("DELETE", ev + "/me", u, "queue_leave");
      return abandoned.add(1), null;
    }
    const st = r.status === 200 || r.status === 201 ? r.json() : {};
    switch (st.state) {
      case "admitted":
        queueWait.add(Date.now() - joined);
        return st.pass;
      case "sold_out":
      case "closed":
        return soldOut.add(1), null;
      case "lobby":
      case "waiting":
        break;
      default:
        // not_in_queue (dibuang karena telat polling) atau 409 queue_not_open: masuk lagi, maksimal 5 kali.
        if (++rejoin > 5) return failed.add(1), unexpected.add(1, { name: "queue_state" }), null;
        sleep(rand(1, 3));
        r = call("POST", ev, u, "queue_join");
        continue;
    }
    sleep(Math.max(st.poll_after_ms || 2000, 1000) / 1000 + rand(0, 0.3));
    r = call("GET", ev + "/me", u, "queue_status");
  }
}

// Tanpa antrean: tunggu jam buka (menyegarkan halaman event tiap ±1 detik), lalu langsung reservasi.
function waitForOpen(u) {
  while (Date.now() < meta.sale_open_at) {
    call("GET", `/v1/events/${meta.event_id}`, u, "event_detail");
    sleep(Math.min(rand(0.8, 1.2), Math.max((meta.sale_open_at - Date.now()) / 1000, 0.05)));
  }
}

// Reservasi: coba kategori pilihan dulu, lalu kategori lain bila habis. Mengembalikan {cat, rid},
// "sold_out", "retry" (pass kedaluwarsa → antre lagi), atau null (gagal).
function reserve(u, pass) {
  const cats = meta.categories.slice().sort(() => Math.random() - 0.5);
  for (const c of cats) {
    for (let i = 0; i < 20; i++) {
      const r = call("POST", `/v1/categories/${c.id}/reservations`, u, "reserve", { qty: 1 }, pass ? { "X-Queue-Pass": pass } : null);
      if (!r) return null;
      if (r.status === 201) return reservedOK.add(1), { cat: c.id, rid: r.json("reservation_id") };
      const e = errCode(r);
      if (e === "sold_out") break;
      if (e === "sale_not_open") {
        sleep(rand(0.2, 0.6)); // jam buka belum sampai di cache server (≤3 dtk)
        continue;
      }
      if (e === "queue_pass_expired" || e === "queue_required") return "retry";
      unexpected.add(1, { name: "reserve_" + (e || r.status) });
      return null;
    }
  }
  return "sold_out";
}

export default function (data) {
  const u = users[exec.vu.idInTest - 1];
  const arrive = meta.sale_open_at + rand(-ARRIVAL, ARRIVAL) * 1000;
  sleep(Math.max(arrive - Date.now(), 0) / 1000);
  const t0 = Date.now();
  const deadline = data.start + MAX_MIN * 60e3;
  const quits = Math.random() < ABANDON;
  const quitStage = quits ? Math.floor(rand(0, 3)) : -1; // 0 saat antre, 1 setelah reservasi, 2 setelah order

  let res;
  for (let round = 0; ; round++) {
    let pass = null;
    if (meta.waiting_room) {
      pass = waitInQueue(u, deadline, quitStage === 0 ? t0 + rand(5, 40) * 1000 : 0);
      if (!pass) return;
    } else {
      waitForOpen(u);
    }
    res = reserve(u, pass);
    if (res !== "retry") break;
    if (round >= 2) return expired.add(1), undefined;
  }
  if (res === "sold_out") return soldOut.add(1), undefined;
  if (!res) return failed.add(1), undefined;
  if (quitStage === 1) return abandoned.add(1), undefined;

  sleep(rand(2, 8)); // mengisi data peserta
  const n = exec.vu.idInTest;
  const idem = `lt-${meta.event_id.slice(0, 8)}-${n}`;
  const order = call("POST", "/v1/orders", u, "order_create", {
    category_id: res.cat,
    reservation_id: res.rid,
    participants: [{ full_name: `Pelari Uji ${n}`, email: `lt-${n}@loadtest.invalid`, id_number: String(3170000000000000 + n), jersey_size: "M" }],
  }, { "Idempotency-Key": idem });
  if (!order) return failed.add(1), undefined;
  if (order.status === 410) return expired.add(1), undefined;
  if (order.status !== 201 && order.status !== 200) return unexpected.add(1, { name: "order_" + (errCode(order) || order.status) }), failed.add(1), undefined;
  const orderID = order.json("order_id");
  if (quitStage === 2) return abandoned.add(1), undefined;

  sleep(rand(1, 4)); // memilih metode bayar
  const pay = call("POST", `/v1/dev/orders/${orderID}/simulate`, u, "pay_simulate", { status: "settlement" });
  if (!pay || pay.status !== 200) return unexpected.add(1, { name: "pay" }), failed.add(1), undefined;
  const paidAt = Date.now();
  for (let i = 0; i < 180; i++) {
    const o = call("GET", `/v1/orders/${orderID}`, u, "order_get");
    if (o && o.status === 200 && o.json("tickets_issued") >= 1) {
      ticketWait.add(Date.now() - paidAt);
      buyTime.add(Date.now() - t0);
      return got.add(1), undefined;
    }
    if (o && o.status === 200 && o.json("status") === "needs_refund") return unexpected.add(1, { name: "needs_refund" }), failed.add(1), undefined;
    sleep(1);
  }
  unexpected.add(1, { name: "ticket_timeout" });
  failed.add(1);
}
