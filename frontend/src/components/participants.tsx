"use client";

import { useParams } from "next/navigation";
import { useState } from "react";
import { canEdit, qs, roleIn, type Paged, type PartnerEvent, type EventParticipant, type ParticipantSummary } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { Download, Pager } from "./finance";
import { useSession } from "./session";
import { btnDark, ErrorBox, Field, inputCls, Loading, LoginHint, PageTitle } from "./ui";

const TICKET_STATUS: Record<string, [string, string]> = {
  issued: ["Diterbitkan", "bg-go-soft text-go"],
  collected: ["Diambil", "bg-sky-100 text-sky-800"],
  refunded: ["Refund", "bg-red-100 text-red-800"],
  cancelled: ["Dibatalkan", "bg-line text-muted"],
};

function TicketBadge({ status }: { status: string }) {
  const [label, cls] = TICKET_STATUS[status] ?? [status, "bg-line text-muted"];
  return <span className={`inline-block rounded-full px-2.5 py-0.5 text-xs font-semibold whitespace-nowrap ${cls}`}>{label}</span>;
}

/** Halaman Peserta per event: owner/admin melihat daftar + unduh CSV; staf RPC hanya ringkasan. */
export function Participants() {
  const { org, eventId } = useParams<{ org: string; eventId: string }>();
  const { ready, userId, me } = useSession();
  const base = `/v1/org/${org}/events/${eventId}`;
  const event = useApi<PartnerEvent>(userId ? base : null, [userId]);
  const summary = useApi<ParticipantSummary>(userId ? `${base}/participants/summary` : null, [userId]);
  const role = roleIn(me, org);

  if (!ready) return <Loading />;
  if (!userId) return <LoginHint />;

  return (
    <>
      <PageTitle title="Peserta" back={{ href: `/org/${org}`, label: "Dashboard mitra" }}>
        {event.data && <p className="mt-2 text-sm break-words text-muted">{event.data.name}</p>}
      </PageTitle>
      {summary.error && !summary.data ? <ErrorBox message={summary.error} /> : !summary.data ? <Loading /> : <Summary s={summary.data} />}
      {canEdit(role) ? (
        <ParticipantList base={base} categories={summary.data?.by_category ?? []} />
      ) : (
        role && <p className="mt-8 rounded-md border border-dashed border-line bg-card p-5 text-sm text-muted">Daftar nama dan kontak peserta hanya untuk owner dan admin.</p>
      )}
    </>
  );
}

function Summary({ s }: { s: ParticipantSummary }) {
  const groups: [string, { key: string; label?: string; count: number }[]][] = [
    ["Per kategori", s.by_category.map((c) => ({ ...c, label: c.key }))],
    ["Ukuran jersey (tiket berlaku)", s.by_jersey],
    ["Status tiket", s.by_status.map((c) => ({ ...c, label: TICKET_STATUS[c.key]?.[0] ?? c.key }))],
  ];
  return (
    <section aria-labelledby="ringkasan" className="mb-8">
      <h2 id="ringkasan" className="mb-3 text-lg font-semibold">
        Ringkasan · <span className="tabular-nums">{s.total}</span> tiket
      </h2>
      <div className="grid gap-3 sm:grid-cols-3">
        {groups.map(([title, rows]) => (
          <div key={title} className="rounded-md bg-card p-4 ring-1 ring-line">
            <h3 className="text-sm font-semibold">{title}</h3>
            {rows.length === 0 ? (
              <p className="mt-2 text-sm text-muted">Belum ada.</p>
            ) : (
              <dl className="mt-2 grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 text-sm">
                {rows.map((r) => (
                  <div key={r.key} className="contents">
                    <dt className="min-w-0 truncate text-muted">{r.label ?? r.key}</dt>
                    <dd className="text-right font-semibold tabular-nums">{r.count}</dd>
                  </div>
                ))}
              </dl>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}

function ParticipantList({ base, categories }: { base: string; categories: ParticipantSummary["by_category"] }) {
  const [category, setCategory] = useState("");
  const [status, setStatus] = useState("");
  const [search, setSearch] = useState("");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const list = useApi<Paged<EventParticipant> & { total: number }>(`${base}/participants${qs({ category, status, q, page })}`);
  const filters = qs({ category, status, q });

  return (
    <section aria-labelledby="daftar" className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <h2 id="daftar" className="text-lg font-semibold">
          Daftar peserta
        </h2>
        <Download path={`${base}/participants.csv${filters}`} name="peserta.csv" label="Unduh CSV" />
      </div>
      <p className="text-sm text-muted">Nomor identitas hanya tampil 4 digit terakhir. Setiap unduhan tercatat di log audit.</p>
      <form
        role="search"
        className="grid gap-3 sm:grid-cols-2 lg:grid-cols-[minmax(0,12rem)_minmax(0,12rem)_minmax(0,1fr)_auto] lg:items-end"
        onSubmit={(e) => {
          e.preventDefault();
          setPage(1);
          setQ(search.trim());
        }}
      >
        <Field label="Kategori">
          <select className={inputCls} value={category} onChange={(e) => (setCategory(e.target.value), setPage(1))}>
            <option value="">Semua kategori</option>
            {categories.map((c) => (
              <option key={c.id} value={c.id}>
                {c.key} · {c.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Status tiket">
          <select className={inputCls} value={status} onChange={(e) => (setStatus(e.target.value), setPage(1))}>
            <option value="">Semua status</option>
            {Object.entries(TICKET_STATUS).map(([k, [label]]) => (
              <option key={k} value={k}>
                {label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Cari nama, email, atau BIB">
          <input type="search" className={inputCls} value={search} maxLength={100} onChange={(e) => setSearch(e.target.value)} />
        </Field>
        <button type="submit" className={btnDark}>
          Cari
        </button>
      </form>

      {list.error && !list.data ? (
        <ErrorBox message={list.error} />
      ) : !list.data ? (
        <Loading />
      ) : list.data.items.length === 0 ? (
        <p className="rounded-md border border-dashed border-line bg-card p-5 text-sm text-muted">Tidak ada peserta{filters ? " yang cocok dengan filter" : ""}.</p>
      ) : (
        <>
          <p className="text-sm text-muted tabular-nums" aria-live="polite">
            {list.data.total} peserta
          </p>
          {/* Layar lebar: tabel. HP: kartu per peserta (tanpa geser horizontal). */}
          <div className="hidden overflow-hidden rounded-md border border-line bg-card md:block">
            <table className="w-full table-fixed text-left text-sm">
              <thead className="border-b border-line bg-paper text-xs text-muted">
                <tr>
                  <th scope="col" className="w-24 px-3 py-2 font-semibold">BIB</th>
                  <th scope="col" className="px-3 py-2 font-semibold">Peserta</th>
                  <th scope="col" className="w-20 px-3 py-2 font-semibold">Kategori</th>
                  <th scope="col" className="w-16 px-3 py-2 font-semibold">Jersey</th>
                  <th scope="col" className="w-44 px-3 py-2 font-semibold">Kontak darurat</th>
                  <th scope="col" className="w-32 px-3 py-2 font-semibold">Status</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {list.data.items.map((p) => (
                  <tr key={p.ticket_id} className="align-top">
                    <td className="px-3 py-2.5 font-mono font-semibold">{p.bib}</td>
                    <td className="px-3 py-2.5">
                      <p className="truncate font-semibold">{p.name}</p>
                      <p className="truncate text-xs text-muted">
                        {p.email} · ID {p.id_number || "–"}
                      </p>
                    </td>
                    <td className="px-3 py-2.5">{p.category}</td>
                    <td className="px-3 py-2.5">{p.jersey_size}</td>
                    <td className="px-3 py-2.5 text-xs">
                      {p.emergency_name || "–"}
                      {p.emergency_phone && <span className="block text-muted tabular-nums">{p.emergency_phone}</span>}
                    </td>
                    <td className="px-3 py-2.5">
                      <TicketBadge status={p.status} />
                      {p.collected_at && <span className="mt-0.5 block text-xs text-muted">{formatDateTime(p.collected_at)}</span>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <ul className="divide-y divide-line rounded-md border border-line bg-card md:hidden">
            {list.data.items.map((p) => (
              <li key={p.ticket_id} className="p-4 text-sm">
                <p className="flex flex-wrap items-center gap-2">
                  <b className="font-mono">{p.bib}</b>
                  <TicketBadge status={p.status} />
                </p>
                <p className="mt-1 font-semibold break-words">{p.name}</p>
                <p className="break-all text-muted">{p.email}</p>
                <p className="text-muted">
                  {p.category} · jersey {p.jersey_size} · ID {p.id_number || "–"}
                </p>
                {p.emergency_name && (
                  <p className="text-muted">
                    Darurat: {p.emergency_name} {p.emergency_phone}
                  </p>
                )}
              </li>
            ))}
          </ul>
          <Pager page={list.data.page} pageSize={list.data.page_size} total={list.data.total} onPage={setPage} />
        </>
      )}
    </section>
  );
}
