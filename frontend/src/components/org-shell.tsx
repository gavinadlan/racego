"use client";

import Link from "next/link";
import { useParams, useSearchParams } from "next/navigation";
import { ROLE_LABEL, type OrgProfile } from "@/lib/api";
import { useApi } from "@/lib/use-api";
import { useSession } from "./session";
import { ErrorBox, Loading, LoginHint, PageTitle } from "./ui";

type Tab = "events" | "team" | "profile";

/**
 * Kerangka dashboard mitra: judul, tab Event · Tim · Profil, dan banner status verifikasi.
 * Profil org dimuat sekali di sini lalu diteruskan ke isi tab.
 */
export function OrgShell({ tab, children }: { tab: Tab; children: (profile: OrgProfile, reload: () => void) => React.ReactNode }) {
  const { org } = useParams<{ org: string }>();
  const { ready, userId } = useSession();
  const { data, error, loading, reload } = useApi<OrgProfile>(userId ? `/v1/org/${org}/profile` : null, [userId]);

  if (!ready || (loading && !data)) return <Loading />;
  if (!userId) return <LoginHint />;
  if (error && !data) return <ErrorBox message={error} />;
  if (!data) return null;

  const tabs: { id: Tab; href: string; label: string }[] = [
    { id: "events", href: `/org/${org}`, label: "Event" },
    ...(data.role === "owner" ? [{ id: "team" as Tab, href: `/org/${org}/tim`, label: "Tim" }] : []),
    { id: "profile", href: `/org/${org}/profil`, label: "Profil" },
  ];

  return (
    <>
      <PageTitle title={data.name}>
        <p className="mt-2 text-sm text-muted">
          Dashboard penyelenggara · peranmu <b className="text-asphalt">{ROLE_LABEL[data.role]}</b>
        </p>
      </PageTitle>
      <NewOrgBanner pending={data.kyc_status !== "verified"} />
      {data.kyc_status !== "verified" && (
        <div className="mb-6 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-900" role="status">
          <b>Menunggu verifikasi.</b> Kamu sudah bisa menyiapkan event sebagai draft, tapi belum bisa menerbitkannya sampai tim RaceGo memverifikasi penyelenggara ini.
        </div>
      )}
      <nav aria-label="Bagian dashboard" className="mb-8 flex gap-1 border-b border-line">
        {tabs.map((t) => (
          <Link
            key={t.id}
            href={t.href}
            aria-current={t.id === tab ? "page" : undefined}
            className={`-mb-px border-b-2 px-4 py-2.5 text-sm font-semibold ${t.id === tab ? "border-hivis text-asphalt" : "border-transparent text-muted hover:text-asphalt"}`}
          >
            {t.label}
          </Link>
        ))}
      </nav>
      {children(data, reload)}
    </>
  );
}

/** Banner sekali setelah mendaftar (/org/{slug}?baru=1). */
function NewOrgBanner({ pending }: { pending: boolean }) {
  const params = useSearchParams();
  if (params.get("baru") !== "1") return null;
  return (
    <div className="mb-6 rounded-md border border-go/30 bg-go-soft p-4 text-sm text-go" role="status">
      <b>Penyelenggara dibuat.</b> {pending ? "Sambil menunggu verifikasi, mulai siapkan event pertamamu." : "Langsung buat event pertamamu."}
    </div>
  );
}
