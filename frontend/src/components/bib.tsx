"use client";

import Link from "next/link";
import type { EventStatus, EventSummary } from "@/lib/api";
import { dayOfMonth, daysUntil, formatDateShort, formatRupiah, humanizeUntil, monthShort, textOn } from "@/lib/format";
import { useNow } from "./clock";

/** Empat lubang peniti di sudut — ciri khas nomor dada pelari. */
export function PinHoles() {
  return (
    <>
      <span className="pin top-2.5 left-2.5" aria-hidden />
      <span className="pin top-2.5 right-2.5" aria-hidden />
      <span className="pin bottom-2.5 left-2.5" aria-hidden />
      <span className="pin right-2.5 bottom-2.5" aria-hidden />
    </>
  );
}

const statusText: Record<EventStatus, string> = {
  open: "Pendaftaran dibuka",
  upcoming: "Segera dibuka",
  closed: "Pendaftaran ditutup",
  finished: "Selesai",
};

export function StatusPill({ status, className = "" }: { status: EventStatus; className?: string }) {
  const style: Record<EventStatus, string> = {
    open: "bg-go-soft text-go",
    upcoming: "bg-hivis-soft text-[#a83a10]",
    closed: "bg-line text-muted",
    finished: "bg-line text-muted",
  };
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-semibold whitespace-nowrap ${style[status]} ${className}`}>
      {status === "open" && <span className="size-1.5 rounded-full bg-go" aria-hidden />}
      {statusText[status]}
    </span>
  );
}

/** Keterangan waktu di sobekan BIB: H-n, atau hitung mundur ke pembukaan pendaftaran. */
function TimeNote({ e }: { e: EventSummary }) {
  const now = useNow();
  if (e.status === "finished") return <span>{formatDateShort(e.race_date)}</span>;
  if (!now) return <span>&nbsp;</span>;
  if (e.status === "upcoming") return <span>Dibuka {humanizeUntil(new Date(e.sale_open_at).getTime() - now)} lagi</span>;
  const h = daysUntil(e.race_date, now);
  return <span>{h === 0 ? "Hari ini" : `H-${h}`}</span>;
}

/**
 * Kartu event berbentuk BIB: pita warna penyelenggara, tanggal sebesar nomor dada,
 * lalu sobekan bawah berisi harga dan waktu.
 */
export function EventBib({ e, size = "md" }: { e: EventSummary; size?: "md" | "lg" }) {
  const finished = e.status === "finished";
  const band = finished ? "#9aa0a6" : e.theme_color;
  return (
    <Link
      href={`/events/${e.id}`}
      className="group relative flex h-full flex-col overflow-hidden rounded-md bg-card shadow-[0_1px_0_#e1e3dd,0_8px_24px_-12px_rgb(35_39_46/0.25)] ring-1 ring-line transition-transform duration-200 hover:-rotate-[0.6deg] motion-reduce:transition-none"
    >
      <PinHoles />
      <div className="flex items-center justify-between gap-3 px-6 pt-5 pb-3" style={{ background: band, color: textOn(band) }}>
        <span className="truncate text-sm font-semibold">{e.org_name}</span>
        <span className="text-xs font-semibold whitespace-nowrap opacity-90">{e.city}</span>
      </div>

      <div className="flex flex-1 gap-5 px-6 pt-4 pb-5">
        <div className={`font-display shrink-0 text-center leading-[0.8] font-black ${finished ? "text-muted" : ""}`}>
          <div className={size === "lg" ? "text-[96px]" : "text-[72px]"}>{dayOfMonth(e.race_date)}</div>
          <div className="mt-1 text-2xl font-bold">{monthShort(e.race_date)}</div>
        </div>
        <div className="flex min-w-0 flex-1 flex-col">
          <h3 className={`leading-snug font-semibold text-balance ${size === "lg" ? "text-2xl" : "text-lg"} group-hover:underline group-hover:decoration-2 group-hover:underline-offset-4`}>
            {e.name}
          </h3>
          <p className="mt-1 line-clamp-2 text-sm text-muted">{e.tagline || e.venue}</p>
          <div className="mt-auto flex flex-wrap gap-1.5 pt-3">
            {e.distances.map((d) => (
              <span key={d} className="font-display rounded-sm bg-asphalt px-2 py-0.5 text-sm font-bold text-white">
                {d}
              </span>
            ))}
          </div>
        </div>
      </div>

      <div className="perforation flex items-center justify-between gap-3 px-6 pt-3 pb-5 text-sm">
        <span>
          {finished ? <StatusPill status="finished" /> : <>Mulai <b className="font-semibold">{formatRupiah(e.min_price)}</b></>}
        </span>
        <span className="font-medium text-muted">
          <TimeNote e={e} />
        </span>
      </div>
    </Link>
  );
}

/** Grid kartu BIB. */
export function BibGrid({ events, empty }: { events: EventSummary[]; empty?: React.ReactNode }) {
  if (!events.length) return <>{empty ?? <p className="text-muted">Belum ada race.</p>}</>;
  return (
    <ul className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      {events.map((e) => (
        <li key={e.id}>
          <EventBib e={e} />
        </li>
      ))}
    </ul>
  );
}
