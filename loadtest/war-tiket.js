// Uji beban "war tiket" untuk jalur paling panas: reservasi slot.
//
//   make db-reset            # kuota kembali penuh
//   make loadtest            # atau: k6 run loadtest/war-tiket.js
//   k6 run -e RATE=2000 -e QUOTA=200 loadtest/war-tiket.js
//
// Yang dibuktikan: berapa pun banyaknya pembeli, reservasi sukses TIDAK PERNAH melebihi kuota,
// dan p95 latensi reservasi tetap < 300 ms. Jalankan langsung ke API (bukan lewat Cloudflare)
// agar tidak terkena rate limit / melanggar ketentuan layanan.
import http from "k6/http";
import { Counter } from "k6/metrics";
import { uuidv4 } from "https://jslib.k6.io/k6-utils/1.4.0/index.js";

const API = __ENV.API_URL || "http://localhost:8080";
const CATEGORY = __ENV.CATEGORY_ID || "cccccccc-0000-0000-0000-000000000021"; // 21K Jakarta, kuota 200
const QUOTA = Number(__ENV.QUOTA || 200);
const RATE = Number(__ENV.RATE || 500); // request/detik di puncak

const reserved = new Counter("reserved_ok");
const soldOut = new Counter("sold_out");
const unexpected = new Counter("unexpected");

export const options = {
  scenarios: {
    war_tiket: {
      executor: "ramping-arrival-rate",
      startRate: 50,
      timeUnit: "1s",
      preAllocatedVUs: 500,
      maxVUs: 5000,
      stages: [
        { target: RATE, duration: "10s" }, // jam buka: lonjakan
        { target: RATE, duration: "40s" },
        { target: 0, duration: "10s" },
      ],
    },
  },
  thresholds: {
    reserved_ok: [`count<=${QUOTA}`], // NOL oversell
    unexpected: ["count==0"],
    "http_req_duration{name:reserve}": ["p(95)<300"],
  },
};

export default function () {
  // Setiap iterasi = pembeli berbeda (UUID acak; reservasi tidak butuh user terdaftar).
  const res = http.post(`${API}/v1/categories/${CATEGORY}/reservations`, JSON.stringify({ qty: 1 }), {
    headers: { "Content-Type": "application/json", "X-User-ID": uuidv4() },
    tags: { name: "reserve" },
  });
  if (res.status === 201) reserved.add(1);
  else if (res.status === 409 && res.json("error") === "sold_out") soldOut.add(1);
  else unexpected.add(1);
}
