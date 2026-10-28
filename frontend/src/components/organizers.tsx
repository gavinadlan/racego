"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import type { EventSummary, Organizer } from "@/lib/api";
import { initials, textOn } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { BibGrid, PinHoles } from "./bib";
import { Monogram } from "./home";
import { ErrorBox, Loading, PageTitle, SectionHead } from "./ui";

export function OrganizerDirectory() {
  const { data, error, loading } = useApi<{ organizers: Organizer[] }>("/v1/organizers");
  return (
    <>
      <PageTitle title="Penyelenggara">
        <p className="mt-3 max-w-xl text-muted">Komunitas dan event organizer yang menjual tiket di RaceGo. Semua sudah diverifikasi.</p>
      </PageTitle>
      {error && <ErrorBox message={error} />}
      {loading ? (
        <Loading />
      ) : (
        <ul className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
          {data?.organizers.map((o) => (
            <li key={o.slug}>
              <Link href={`/penyelenggara/${o.slug}`} className="group flex h-full flex-col rounded-md bg-card p-6 ring-1 ring-line hover:ring-asphalt">
                <div className="flex items-center gap-4">
                  <Monogram name={o.name} color={o.theme_color} />
                  <div className="min-w-0">
                    <h2 className="truncate text-lg font-semibold group-hover:underline group-hover:underline-offset-4">{o.name}</h2>
                    <p className="text-sm text-muted">{o.city}</p>
                  </div>
                </div>
                <p className="mt-4 line-clamp-3 text-sm text-muted">{o.description}</p>
                <p className="mt-auto pt-5 text-sm">
                  <b className="font-display text-xl font-extrabold">{o.upcoming_events}</b> race mendatang,{" "}
                  <b className="font-display text-xl font-extrabold">{o.past_events}</b> sudah digelar
                </p>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

type Profile = { organizer: Organizer; upcoming: EventSummary[]; past: EventSummary[] };

export function OrganizerProfile() {
  const { slug } = useParams<{ slug: string }>();
  const { data, error, loading } = useApi<Profile>(`/v1/organizers/${slug}`);
  if (loading) return <Loading />;
  if (error) return <ErrorBox message={error} />;
  if (!data) return null;
  const o = data.organizer;
  const fg = textOn(o.theme_color);

  return (
    <div className="space-y-20">
      <section className="relative overflow-hidden rounded-md p-8 sm:p-12" style={{ background: o.theme_color, color: fg }}>
        <PinHoles />
        <Link href="/penyelenggara" className="text-sm opacity-80 hover:opacity-100">
          ← Semua penyelenggara
        </Link>
        <div className="mt-6 flex flex-wrap items-end gap-6">
          <span className="font-display grid size-24 place-items-center rounded-md bg-white/15 text-5xl font-black ring-2 ring-current/30" aria-hidden>
            {initials(o.name)}
          </span>
          <div className="min-w-0 flex-1">
            <h1 className="font-display text-5xl leading-[0.95] font-black text-balance sm:text-7xl">{o.name}</h1>
            <p className="mt-2 opacity-85">{o.city}</p>
          </div>
        </div>
        <p className="mt-8 max-w-2xl text-lg opacity-95">{o.description}</p>
        <div className="mt-8 flex flex-wrap items-center gap-x-10 gap-y-4">
          <Stat value={o.upcoming_events} label="race mendatang" />
          <Stat value={o.past_events} label="sudah digelar" />
          <Stat value={o.runners} label="tiket terbit di RaceGo" />
          <div className="flex gap-4 text-sm font-semibold">
            {o.website && (
              <a href={o.website} target="_blank" rel="noreferrer" className="underline underline-offset-4">
                Situs web
              </a>
            )}
            {o.instagram && <span>Instagram {o.instagram}</span>}
          </div>
        </div>
      </section>

      <section>
        <SectionHead title="Race mendatang" />
        <BibGrid events={data.upcoming} empty={<p className="text-muted">Belum ada race baru dari {o.name}. Cek lagi nanti.</p>} />
      </section>

      <section>
        <SectionHead title="Arsip" intro={`Race yang pernah digelar ${o.name}.`} />
        <BibGrid events={data.past} empty={<p className="text-muted">Belum ada arsip.</p>} />
      </section>
    </div>
  );
}

function Stat({ value, label }: { value: number; label: string }) {
  return (
    <div>
      <div className="font-display text-5xl leading-none font-black">{value.toLocaleString("id-ID")}</div>
      <div className="mt-1 text-sm opacity-80">{label}</div>
    </div>
  );
}
