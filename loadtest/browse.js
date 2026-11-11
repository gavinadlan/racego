// Skenario browse: baca-berat (daftar event, detail, kalender bulan, filter, penyelenggara) = baseline.
//   make loadtest SCENARIO=browse VUS=50 DURATION=60s
import http from "k6/http";
import { sleep } from "k6";
import exec from "k6/execution";
import { BASE, guard, headers, rand, users, unexpected } from "./lib.js";

const VUS = Number(__ENV.VUS || 50);
const DURATION = __ENV.DURATION || "60s";

export const options = {
  scenarios: {
    browse: {
      executor: "ramping-vus",
      startVUs: 1,
      stages: [
        { target: VUS, duration: "10s" },
        { target: VUS, duration: DURATION },
        { target: 0, duration: "5s" },
      ],
    },
  },
  thresholds: {
    http_req_failed: ["rate<0.01"],
    unexpected: ["count==0"],
    "http_req_duration{name:event_list}": ["p(95)<300"],
    "http_req_duration{name:event_detail}": ["p(95)<300"],
    "http_req_duration{name:event_month}": ["p(95)<300"],
  },
};

export function setup() {
  guard();
}

function get(path, name, u) {
  const r = http.get(BASE + path, { headers: headers(u), tags: { name } });
  if (r.status !== 200) unexpected.add(1, { name });
  return r;
}

export default function () {
  // 30% pengunjung sudah login (cookie sesi → pencarian sesi per request), sisanya anonim.
  const real = users[(exec.vu.idInTest - 1) % users.length];
  const u = Math.random() < 0.3 ? real : { ip: real.ip };
  const list = get("/v1/events?scope=upcoming", "event_list", u);
  sleep(rand(0.5, 2));
  const events = list.status === 200 ? list.json("events") || [] : [];
  if (events.length && Math.random() < 0.7) {
    get(`/v1/events/${events[Math.floor(Math.random() * events.length)].id}`, "event_detail", u);
    sleep(rand(1, 3));
  }
  if (Math.random() < 0.35) {
    const d = new Date(Date.now() + Math.floor(rand(0, 4)) * 30 * 86400e3);
    get(`/v1/events?month=${d.toISOString().slice(0, 7)}`, "event_month", u);
    sleep(rand(0.5, 2));
  }
  if (Math.random() < 0.2) get("/v1/events/filters", "event_filters", u);
  if (Math.random() < 0.1) get("/v1/organizers", "organizers", u);
  sleep(rand(0.5, 1.5));
}
