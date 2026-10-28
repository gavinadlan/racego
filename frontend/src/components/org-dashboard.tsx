"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import type { OrgEvent } from "@/lib/api";
import { formatDate, formatRupiah } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { useSession } from "./session";
import { ErrorBox, Loading, LoginHint, PageTitle, StatusBadge } from "./ui";

type Resp = { org: { slug: string; name: string; role: string }; events: OrgEvent[] };

export function OrgDashboard() {
  const { org } = useParams<{ org: string }>();
  const { ready, userId } = useSession();
  const { data, error, loading } = useApi<Resp>(userId ? `/v1/org/${org}/events` : null, [userId]);

  if (!ready || loading) return <Loading />;
  if (!userId) return <LoginHint />;
  if (error) return <ErrorBox message={error} />;
  if (!data) return null;

  return (
    <>
      <PageTitle title={data.org.name}>
        <p className="mt-2 text-sm text-muted">
          Dashboard mitra. Peran kamu: <b className="text-asphalt">{data.org.role}</b>
        </p>
      </PageTitle>
      <div className="overflow-x-auto rounded-md border border-line bg-card">
        <table className="w-full min-w-[640px] text-sm">
          <thead className="border-b border-line text-left text-muted">
            <tr>
              <th className="px-4 py-3 font-medium">Event</th>
              <th className="px-4 py-3 font-medium">Status</th>
              <th className="px-4 py-3 text-right font-medium">Terjual / kuota</th>
              <th className="px-4 py-3 text-right font-medium">Race pack diambil</th>
              <th className="px-4 py-3 text-right font-medium">Penjualan</th>
              <th className="px-4 py-3" />
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {data.events.map((e) => (
              <tr key={e.id}>
                <td className="px-4 py-3">
                  <p className="font-medium">{e.name}</p>
                  <p className="text-xs text-muted">{formatDate(e.race_date)}</p>
                </td>
                <td className="px-4 py-3">
                  <StatusBadge status={e.status} />
                </td>
                <td className="px-4 py-3 text-right tabular-nums">
                  {e.sold} / {e.quota}
                </td>
                <td className="px-4 py-3 text-right tabular-nums">
                  {e.collected} / {e.sold}
                </td>
                <td className="px-4 py-3 text-right tabular-nums">{formatRupiah(e.revenue)}</td>
                <td className="px-4 py-3 text-right">
                  <Link href={`/org/${org}/events/${e.id}/scan`} className="rounded-md bg-hivis px-3 py-1.5 text-xs font-semibold text-asphalt">
                    Scan race pack
                  </Link>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="mt-4 text-xs text-muted">Penjualan = total order lunas, sebelum fee platform & biaya payment gateway.</p>
    </>
  );
}
