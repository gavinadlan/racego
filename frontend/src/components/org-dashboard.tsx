"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { canEdit, type OrgEvent, type Role } from "@/lib/api";
import { formatDate, formatRupiah } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { OrgShell } from "./org-shell";
import { btnPrimary, ErrorBox, Loading } from "./ui";

type Resp = { org: { slug: string; name: string; role: string }; events: OrgEvent[] };

const EVENT_STATUS: Record<string, { label: string; cls: string }> = {
  draft: { label: "Draft", cls: "bg-line text-asphalt" },
  published: { label: "Terbit", cls: "bg-go-soft text-go" },
  closed: { label: "Ditutup", cls: "bg-asphalt text-white" },
  review: { label: "Ditinjau", cls: "bg-hivis-soft text-[#a83a10]" },
};

export function EventStatusBadge({ status }: { status: string }) {
  const s = EVENT_STATUS[status] ?? { label: status, cls: "bg-line text-muted" };
  return <span className={`inline-block rounded-full px-2.5 py-0.5 text-xs font-semibold whitespace-nowrap ${s.cls}`}>{s.label}</span>;
}

export function OrgDashboard() {
  return <OrgShell tab="events">{(profile) => <EventsTab role={profile.role} />}</OrgShell>;
}

function EventsTab({ role }: { role: Role }) {
  const { org } = useParams<{ org: string }>();
  const { data, error, loading } = useApi<Resp>(`/v1/org/${org}/events`);
  const editor = canEdit(role);
  const scanner = editor || role === "rpc_staff";

  if (loading && !data) return <Loading />;
  if (error) return <ErrorBox message={error} />;
  if (!data) return null;

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-4">
        <h2 className="font-display text-3xl leading-none font-extrabold">Event</h2>
        {editor && (
          <Link href={`/org/${org}/events/new`} className={btnPrimary}>
            Buat event
          </Link>
        )}
      </div>
      {data.events.length === 0 ? (
        <p className="rounded-md border border-dashed border-line bg-card p-6 text-sm text-muted">
          Belum ada event. {editor ? "Klik Buat event untuk menyiapkan draft pertama." : "Owner atau admin bisa membuat event."}
        </p>
      ) : (
        <ul className="divide-y divide-line rounded-md border border-line bg-card">
          {data.events.map((e) => (
            <li key={e.id} className="grid gap-3 px-4 py-4 sm:grid-cols-[1fr_auto] sm:items-center">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <p className="font-semibold">{e.name}</p>
                  <EventStatusBadge status={e.status} />
                </div>
                <p className="text-xs text-muted">{formatDate(e.race_date)}</p>
                <dl className="mt-2 flex flex-wrap gap-x-6 gap-y-1 text-sm tabular-nums">
                  <div>
                    <dt className="inline text-muted">Terjual </dt>
                    <dd className="inline font-semibold">
                      {e.sold} / {e.quota}
                    </dd>
                  </div>
                  <div>
                    <dt className="inline text-muted">Race pack diambil </dt>
                    <dd className="inline font-semibold">
                      {e.collected} / {e.sold}
                    </dd>
                  </div>
                  <div>
                    <dt className="inline text-muted">Penjualan </dt>
                    <dd className="inline font-semibold">{formatRupiah(e.revenue)}</dd>
                  </div>
                </dl>
              </div>
              <div className="flex flex-wrap gap-2">
                {editor && (
                  <Link href={`/org/${org}/events/${e.id}/edit`} className="rounded-md border border-line px-3 py-1.5 text-xs font-semibold hover:border-asphalt" aria-label={`Edit ${e.name}`}>
                    Edit
                  </Link>
                )}
                {scanner && e.status !== "draft" && (
                  <Link href={`/org/${org}/events/${e.id}/scan`} className="rounded-md bg-hivis px-3 py-1.5 text-xs font-semibold text-asphalt">
                    Scan race pack
                  </Link>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      <p className="mt-4 text-xs text-muted">Penjualan = total order lunas, sebelum fee platform & biaya payment gateway.</p>
    </>
  );
}
