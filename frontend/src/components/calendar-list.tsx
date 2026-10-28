"use client";

import Link from "next/link";
import type { EventSummary } from "@/lib/api";
import { dayOfMonth, formatRupiah, monthKey, monthLong } from "@/lib/format";
import { StatusPill } from "./bib";
import { UploadedImage } from "./ui-client";

/** Kelompokkan event per bulan (urutan mengikuti input). */
export function groupByMonth(events: EventSummary[]) {
  const groups: { key: string; label: string; events: EventSummary[] }[] = [];
  for (const e of events) {
    const d = new Date(e.race_date);
    const key = monthKey(d);
    let g = groups.find((x) => x.key === key);
    if (!g) {
      g = { key, label: monthLong(d), events: [] };
      groups.push(g);
    }
    g.events.push(e);
  }
  return groups;
}

/** Daftar start list per bulan: nama bulan besar di kiri, baris race di kanan. */
export function CalendarList({ events, empty }: { events: EventSummary[]; empty?: React.ReactNode }) {
  const groups = groupByMonth(events);
  if (!groups.length) return <>{empty ?? <p className="text-muted">Tidak ada race yang cocok.</p>}</>;
  return (
    <div className="space-y-12">
      {groups.map((g) => (
        <section key={g.key} className="grid gap-4 md:grid-cols-[200px_1fr] md:gap-10">
          <h3 className="font-display text-3xl leading-none font-extrabold capitalize md:sticky md:top-24 md:self-start">{g.label}</h3>
          <ul className="divide-y divide-line border-y border-line">
            {g.events.map((e) => (
              <li key={e.id}>
                <Link href={`/events/${e.id}`} className="group grid grid-cols-[56px_1fr] items-center gap-x-4 gap-y-2 py-4 sm:grid-cols-[56px_1fr_auto]">
                  <div className="text-center">
                    <div className="font-display text-4xl leading-none font-black">{dayOfMonth(e.race_date)}</div>
                    <div className="text-xs text-muted">{new Date(e.race_date).toLocaleDateString("id-ID", { weekday: "short" })}</div>
                  </div>
                  <div className="flex min-w-0 items-center gap-3">
                    {e.cover_url && (
                      <div className="h-12 w-10 shrink-0 overflow-hidden rounded-sm" style={{ background: e.theme_color }}>
                        <UploadedImage url={e.cover_url} alt={`Poster ${e.name}`} className="size-full" />
                      </div>
                    )}
                    <div className="min-w-0">
                      <p className="flex items-center gap-2 font-semibold group-hover:underline group-hover:underline-offset-4">
                        <span className="size-2.5 shrink-0 rounded-full" style={{ background: e.theme_color }} aria-hidden />
                        <span className="truncate">{e.name}</span>
                      </p>
                      <p className="mt-0.5 truncate text-sm text-muted">
                        {e.city}, oleh {e.org_name}
                      </p>
                    </div>
                  </div>
                  <div className="col-start-2 flex flex-wrap items-center gap-2 sm:col-start-auto sm:justify-end">
                    <span className="font-display text-base font-bold">{e.distances.join(" / ")}</span>
                    <span className="text-sm text-muted">{e.status === "finished" ? "" : formatRupiah(e.min_price)}</span>
                    <StatusPill status={e.status} />
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
