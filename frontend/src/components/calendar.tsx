"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { qs, type EventFilters, type EventSummary } from "@/lib/api";
import { monthKey, monthLong } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { CalendarList } from "./calendar-list";
import { ErrorBox, Loading, PageTitle } from "./ui";

const WEEKDAYS = ["Sen", "Sel", "Rab", "Kam", "Jum", "Sab", "Min"];

const pad = (n: number) => String(n).padStart(2, "0");
const isoDay = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;

export function Calendar() {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const view = params.get("view") === "daftar" ? "daftar" : "bulan";
  const city = params.get("city") ?? "";
  const distance = params.get("distance") ?? "";
  const archive = params.get("arsip") === "1";
  const monthParam = params.get("month") ?? "";
  const month = /^\d{4}-\d{2}$/.test(monthParam) ? monthParam : monthKey(new Date());
  const [y, m] = month.split("-").map(Number);
  const first = new Date(y, m - 1, 1);
  const last = new Date(y, m, 0);

  const set = (patch: Record<string, string>) => {
    const next = { view, city, distance, month: monthParam, arsip: archive ? "1" : "", ...patch };
    router.replace(`${pathname}${qs(next)}`, { scroll: false });
  };
  const shift = (delta: number) => set({ month: monthKey(new Date(y, m - 1 + delta, 1)) });

  const filters = useApi<EventFilters>("/v1/events/filters");
  const path =
    view === "bulan"
      ? `/v1/events${qs({ scope: "all", from: isoDay(first), to: isoDay(last), city, distance })}`
      : `/v1/events${qs({ scope: archive ? "past" : "upcoming", city, distance, month: monthParam, limit: 200 })}`;
  const { data, error, loading } = useApi<{ events: EventSummary[] }>(path);
  const events = data?.events ?? [];
  // Bulan kosong: cari race berikutnya setelah bulan ini agar pengguna bisa langsung lompat.
  const afterMonth = isoDay(new Date(y, m, 1));
  const nextRace = useApi<{ events: EventSummary[] }>(
    view === "bulan" && data && events.length === 0 ? `/v1/events${qs({ scope: "upcoming", from: afterMonth, city, distance, limit: 1 })}` : null,
  );
  const upcomingNext = nextRace.data?.events[0];

  const select = "rounded-md border border-line bg-card px-3 py-2 text-sm";
  return (
    <>
      <PageTitle title={archive && view === "daftar" ? "Arsip race" : "Kalender race"}>
        <p className="mt-3 max-w-xl text-muted">
          {archive && view === "daftar" ? "Race yang sudah digelar, dari yang terbaru." : "Semua race dari semua penyelenggara. Filter berdasarkan kota dan jarak."}
        </p>
      </PageTitle>

      <div className="mb-8 flex flex-wrap items-center gap-3">
        <div className="inline-flex rounded-md bg-line p-1" role="tablist" aria-label="Tampilan">
          {(["bulan", "daftar"] as const).map((v) => (
            <button
              key={v}
              role="tab"
              aria-selected={view === v}
              onClick={() => set({ view: v })}
              className={`rounded px-4 py-1.5 text-sm font-semibold ${view === v ? "bg-card shadow-sm" : "text-muted"}`}
            >
              {v === "bulan" ? "Bulan" : "Daftar"}
            </button>
          ))}
        </div>
        <select className={select} value={city} onChange={(e) => set({ city: e.target.value })} aria-label="Kota">
          <option value="">Semua kota</option>
          {filters.data?.cities.map((c) => <option key={c}>{c}</option>)}
        </select>
        <select className={select} value={distance} onChange={(e) => set({ distance: e.target.value })} aria-label="Jarak">
          <option value="">Semua jarak</option>
          {filters.data?.distances.map((d) => <option key={d}>{d}</option>)}
        </select>
        {view === "daftar" && (
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" className="size-4 accent-hivis" checked={archive} onChange={(e) => set({ arsip: e.target.checked ? "1" : "", month: "" })} />
            Tampilkan arsip
          </label>
        )}
        {view === "daftar" && monthParam && (
          <button className="text-sm underline underline-offset-4" onClick={() => set({ month: "" })}>
            Hapus filter bulan ({monthLong(first)})
          </button>
        )}
      </div>

      {error && <ErrorBox message={error} />}

      {view === "bulan" ? (
        <section>
          <div className="mb-4 flex items-center justify-between gap-4">
            <h2 className="font-display text-4xl font-extrabold capitalize">{monthLong(first)}</h2>
            <div className="flex gap-2">
              <button className="rounded-md border border-line bg-card px-3 py-2 text-sm hover:border-asphalt" onClick={() => shift(-1)} aria-label="Bulan sebelumnya">
                ‹ Sebelumnya
              </button>
              <button className="rounded-md border border-line bg-card px-3 py-2 text-sm hover:border-asphalt" onClick={() => shift(1)} aria-label="Bulan berikutnya">
                Berikutnya ›
              </button>
            </div>
          </div>
          {data && events.length === 0 && (
            <div className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-md bg-card px-5 py-4 ring-1 ring-line">
              <p className="text-sm">
                Tidak ada race di {monthLong(first)}.
                {upcomingNext && (
                  <>
                    {" "}Race berikutnya <b>{upcomingNext.name}</b>, {new Date(upcomingNext.race_date).toLocaleDateString("id-ID", { day: "numeric", month: "long" })}.
                  </>
                )}
              </p>
              {upcomingNext && (
                <button className="text-sm font-semibold underline decoration-hivis decoration-2 underline-offset-4" onClick={() => set({ month: monthKey(new Date(upcomingNext.race_date)) })}>
                  Lompat ke {monthLong(new Date(upcomingNext.race_date))}
                </button>
              )}
            </div>
          )}
          {loading && !data ? <Loading /> : <MonthGrid first={first} last={last} events={events} />}
          <div className="mt-8 sm:hidden">
            <CalendarList events={events} empty={<p className="text-muted">Tidak ada race di bulan ini.</p>} />
          </div>
        </section>
      ) : loading && !data ? (
        <Loading />
      ) : (
        <CalendarList events={events} empty={<p className="text-muted">Tidak ada race yang cocok dengan filter ini.</p>} />
      )}
    </>
  );
}

function MonthGrid({ first, last, events }: { first: Date; last: Date; events: EventSummary[] }) {
  const lead = (first.getDay() + 6) % 7; // Senin = kolom pertama
  const cells: (Date | null)[] = [...Array(lead).fill(null), ...Array.from({ length: last.getDate() }, (_, i) => new Date(first.getFullYear(), first.getMonth(), i + 1))];
  while (cells.length % 7) cells.push(null);
  const today = isoDay(new Date());
  const byDay = new Map<string, EventSummary[]>();
  for (const e of events) {
    const k = isoDay(new Date(e.race_date));
    byDay.set(k, [...(byDay.get(k) ?? []), e]);
  }
  return (
    <div className="hidden overflow-hidden rounded-md ring-1 ring-line sm:block">
      <div className="grid grid-cols-7 bg-asphalt text-center text-xs font-semibold text-white">
        {WEEKDAYS.map((d) => (
          <div key={d} className="py-2">
            {d}
          </div>
        ))}
      </div>
      <div className="grid grid-cols-7 gap-px bg-line">
        {cells.map((d, i) => {
          if (!d) return <div key={i} className="min-h-28 bg-paper" />;
          const k = isoDay(d);
          const list = byDay.get(k) ?? [];
          const isToday = k === today;
          return (
            <div key={i} className={`min-h-28 bg-card p-2 ${isToday ? "ring-2 ring-hivis ring-inset" : ""}`}>
              <div className={`font-display text-xl leading-none font-bold ${list.length ? "" : "text-muted/60"}`}>{d.getDate()}</div>
              <ul className="mt-2 space-y-1">
                {list.map((e) => (
                  <li key={e.id}>
                    <Link
                      href={`/events/${e.id}`}
                      className={`block truncate rounded-sm border-l-4 bg-paper px-1.5 py-1 text-xs font-medium hover:bg-line ${e.status === "finished" ? "text-muted" : ""}`}
                      style={{ borderColor: e.status === "finished" ? "#9aa0a6" : e.theme_color }}
                      title={`${e.name}, ${e.city}`}
                    >
                      {e.name}
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          );
        })}
      </div>
    </div>
  );
}
