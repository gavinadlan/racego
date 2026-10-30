"use client";

import Link from "next/link";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { useState } from "react";
import { ApiError, reopenVerification, ROLE_LABEL, type OrgProfile } from "@/lib/api";
import { useApi } from "@/lib/use-api";
import { useSession } from "./session";
import { btnDark, ErrorBox, Loading, LoginHint, PageTitle } from "./ui";

type Tab = "events" | "team" | "profile" | "verification" | "finance";

/**
 * Kerangka dashboard mitra: judul, tab Event · Keuangan · Tim · Verifikasi · Profil, dan banner status verifikasi.
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
    ...(data.role === "owner" || data.role === "finance" ? [{ id: "finance" as Tab, href: `/org/${org}/keuangan`, label: "Keuangan" }] : []),
    ...(data.role === "owner" ? [{ id: "team" as Tab, href: `/org/${org}/tim`, label: "Tim" }] : []),
    { id: "verification", href: `/org/${org}/verifikasi`, label: "Verifikasi" },
    { id: "profile", href: `/org/${org}/profil`, label: "Profil" },
  ];

  return (
    <>
      <PageTitle title={data.name}>
        <p className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted">
          <span>
            Dashboard penyelenggara · peranmu <b className="text-asphalt">{ROLE_LABEL[data.role]}</b>
          </span>
          {data.kyc_status === "verified" && <span className="rounded-full bg-go-soft px-2.5 py-0.5 text-xs font-semibold text-go">✓ Terverifikasi</span>}
        </p>
      </PageTitle>
      <NewOrgBanner pending={data.kyc_status !== "verified"} />
      <VerificationBanner profile={data} onTab={tab === "verification"} reload={reload} />
      <nav aria-label="Bagian dashboard" className="mb-8 flex gap-1 overflow-x-auto border-b border-line">
        {tabs.map((t) => (
          <Link
            key={t.id}
            href={t.href}
            aria-current={t.id === tab ? "page" : undefined}
            className={`-mb-px border-b-2 px-4 py-2.5 text-sm font-semibold whitespace-nowrap ${t.id === tab ? "border-hivis text-asphalt" : "border-transparent text-muted hover:text-asphalt"}`}
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

/**
 * Status verifikasi di atas dashboard & tab: belum diajukan, menunggu, ditolak (alasan + Ajukan ulang).
 * Disetujui cukup ditandai lencana "Terverifikasi" di judul.
 */
function VerificationBanner({ profile, onTab, reload }: { profile: OrgProfile; onTab: boolean; reload: () => void }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  if (profile.kyc_status === "verified") return null;
  const owner = profile.role === "owner";
  const href = `/org/${profile.slug}/verifikasi`;
  const status = profile.verification_status;
  const link = (label: string) =>
    owner && !onTab ? (
      <Link href={href} className="font-semibold underline decoration-2 underline-offset-4">
        {label}
      </Link>
    ) : null;

  if (status === "rejected") {
    return (
      <div className="mb-6 rounded-md border border-red-200 bg-red-50 p-4 text-sm text-red-900" role="status">
        <p>
          <b>Pengajuan verifikasi ditolak.</b> Event belum bisa diterbitkan. Alasan dari tim RaceGo:
        </p>
        <p className="mt-2 rounded-sm bg-white/70 p-3 whitespace-pre-line text-asphalt">{profile.reject_reason}</p>
        {owner ? (
          <div className="mt-3 flex flex-wrap items-center gap-3">
            <button
              type="button"
              className={btnDark}
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                setError(null);
                try {
                  await reopenVerification(profile.slug);
                  // Profil dimuat ulang → banner hilang, tab Verifikasi ikut memuat draft baru.
                  reload();
                  if (!onTab) router.push(href);
                } catch (err) {
                  setError(err instanceof ApiError ? err.message : "Gagal mengajukan ulang");
                } finally {
                  setBusy(false);
                }
              }}
            >
              {busy ? "Memproses…" : "Ajukan ulang"}
            </button>
            <span className="text-xs">Isian dan dokumen sebelumnya disalin, tinggal diperbaiki.</span>
          </div>
        ) : (
          <p className="mt-2 text-xs">Owner penyelenggara bisa memperbaiki lalu mengajukan ulang.</p>
        )}
        {error && <p className="mt-2 font-medium" role="alert">{error}</p>}
      </div>
    );
  }
  if (status === "submitted") {
    return (
      <div className="mb-6 rounded-md border border-sky-200 bg-sky-50 p-4 text-sm text-sky-900" role="status">
        <b>Menunggu peninjauan.</b> Pengajuan verifikasi sudah dikirim dan sedang ditinjau tim RaceGo. Sambil menunggu, event bisa disiapkan sebagai draft. {link("Lihat pengajuan")}
      </div>
    );
  }
  return (
    <div className="mb-6 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-900" role="status">
      <b>{status === "draft" ? "Verifikasi belum dikirim." : "Belum terverifikasi."}</b> Event bisa disiapkan sebagai draft, tapi baru bisa diterbitkan setelah penyelenggara diverifikasi.{" "}
      {owner ? link(status === "draft" ? "Lanjutkan verifikasi" : "Mulai verifikasi") : "Owner penyelenggara perlu mengirim data verifikasi."}
    </div>
  );
}
