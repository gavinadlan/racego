"use client";

import { useParams } from "next/navigation";
import type { PublicDrawResults } from "@/lib/api";
import { useApi } from "@/lib/use-api";
import { ErrorBox, Loading } from "./ui";

/** Hasil undian publik: BIB + nama depan + kategori. Hanya tampil bila penyelenggara mengaktifkannya. */
export function DrawResults() {
  const { eventId } = useParams<{ eventId: string }>();
  const { data, error } = useApi<PublicDrawResults>(`/v1/events/${eventId}/draw-results`);
  if (error && !data) return <ErrorBox message="Hasil undian tidak tersedia." />;
  if (!data) return <Loading />;
  return (
    <>
      <h1 className="font-display text-3xl leading-none font-extrabold break-words">Hasil undian</h1>
      <p className="mt-2 mb-6 text-sm break-words text-muted">{data.event}</p>
      {data.prizes.length === 0 ? (
        <p className="rounded-md border border-dashed border-line bg-card p-5 text-sm text-muted">Belum ada pemenang.</p>
      ) : (
        <div className="space-y-4">
          {data.prizes.map((p) => (
            <section key={p.name} className="rounded-md bg-card p-4 ring-1 ring-line">
              <h2 className="mb-2 font-semibold break-words">{p.name}</h2>
              <ul className="space-y-1 tabular-nums">
                {p.winners.map((w) => (
                  <li key={w.seq}>
                    <b className="font-display text-xl">{w.bib}</b> · {w.first_name} · {w.category}
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}
    </>
  );
}
