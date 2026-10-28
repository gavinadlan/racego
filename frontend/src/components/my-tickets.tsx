"use client";

import { QRCodeSVG } from "qrcode.react";
import type { MyTicket } from "@/lib/api";
import { formatDate } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { PinHoles } from "./bib";
import { useSession } from "./session";
import { ErrorBox, Loading, LoginHint, PageTitle, StatusBadge } from "./ui";

export function MyTickets() {
  const { ready, userId } = useSession();
  const { data, error, loading } = useApi<{ tickets: MyTicket[] }>(userId ? "/v1/me/tickets" : null, [userId]);

  return (
    <>
      <PageTitle title="Tiket saya">
        <p className="mt-3 text-muted">Tunjukkan QR beserta KTP saat mengambil race pack.</p>
      </PageTitle>
      {!ready || loading ? (
        <Loading />
      ) : !userId ? (
        <LoginHint />
      ) : error ? (
        <ErrorBox message={error} />
      ) : !data?.tickets.length ? (
        <p className="text-muted">Belum ada tiket. Tiket terbit otomatis beberapa detik setelah pembayaran lunas.</p>
      ) : (
        <ul className="grid gap-8 md:grid-cols-2">
          {data.tickets.map((t) => (
            <li key={t.id}>
              <BibTicket t={t} />
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

/** Tiket digital berbentuk nomor dada: nomor BIB besar, QR di sobekan bawah. */
function BibTicket({ t }: { t: MyTicket }) {
  const used = t.status !== "issued";
  return (
    <article className="relative overflow-hidden rounded-md bg-card shadow-[0_10px_30px_-14px_rgb(35_39_46/0.35)] ring-1 ring-line">
      <PinHoles />
      <div className="flex items-center justify-between gap-3 bg-hivis px-7 pt-5 pb-3 text-asphalt">
        <span className="truncate text-sm font-semibold">{t.event_name}</span>
        <span className="font-display text-lg font-black">{t.category_code}</span>
      </div>
      <div className="px-7 pt-4 pb-6 text-center">
        <div className={`font-display text-[clamp(88px,22vw,132px)] leading-[0.85] font-black tabular-nums ${used ? "text-muted" : ""}`}>{t.bib_number}</div>
        <p className="mt-2 text-lg font-semibold">{t.participant_name}</p>
        <p className="text-sm text-muted">
          {formatDate(t.race_date)}, {t.event_venue}
        </p>
      </div>
      <div className="perforation flex flex-wrap items-center gap-6 px-7 pt-5 pb-7">
        <div className={used ? "opacity-30" : ""}>
          <QRCodeSVG value={t.qr_token} size={136} level="M" marginSize={1} />
        </div>
        <dl className="grid min-w-40 flex-1 grid-cols-[auto_1fr] gap-x-5 gap-y-2 text-sm">
          <dt className="text-muted">Jersey</dt>
          <dd className="font-display text-xl leading-none font-bold">{t.jersey_size}</dd>
          <dt className="text-muted">Penyelenggara</dt>
          <dd className="font-medium">{t.org_name}</dd>
          <dt className="text-muted">Status</dt>
          <dd>
            <StatusBadge status={t.status} />
          </dd>
        </dl>
      </div>
    </article>
  );
}
