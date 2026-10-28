"use client";

import Link from "next/link";
import { UploadedImage } from "./ui-client";
import { qs, type EventFilters, type EventSummary, type Organizer } from "@/lib/api";
import { flagOffTime, formatDate, humanizeUntil, initials, textOn } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { BibGrid, PinHoles } from "./bib";
import { CalendarList } from "./calendar-list";
import { BigCountdown, useNow } from "./clock";
import { SearchBar } from "./search-bar";
import { btnPrimary, ErrorBox, Loading, SectionHead } from "./ui";

export function Home() {
  const upcoming = useApi<{ events: EventSummary[] }>(`/v1/events${qs({ scope: "upcoming", limit: 100 })}`);
  const past = useApi<{ events: EventSummary[] }>(`/v1/events${qs({ scope: "past", limit: 6 })}`);
  const filters = useApi<EventFilters>("/v1/events/filters");
  const orgs = useApi<{ organizers: Organizer[] }>("/v1/organizers");

  const events = upcoming.data?.events ?? [];
  const open = events.filter((e) => e.status === "open");
  const soon = events.filter((e) => e.status === "upcoming").sort((a, b) => a.sale_open_at.localeCompare(b.sale_open_at));
  const next = open[0] ?? events[0];

  return (
    <div className="space-y-24">
      <section className="grid items-center gap-10 lg:grid-cols-[1.35fr_1fr]">
        <div>
          <h1 className="font-display text-[clamp(56px,7.4vw,100px)] leading-[0.86] font-black text-balance">
            Semua race lari Indonesia, satu kalender.
          </h1>
          <p className="mt-6 max-w-lg text-lg text-muted">
            Fun run 5K sampai full marathon dari {orgs.data?.organizers.length ?? "banyak"} penyelenggara. Pilih race, amankan slot, dan tiket QR
            langsung terbit setelah bayar.
          </p>
          <div className="mt-8">
            <SearchBar filters={filters.data} />
          </div>
        </div>
        {next ? <NextFlagOff e={next} /> : upcoming.loading ? <Loading /> : null}
      </section>

      {upcoming.error && <ErrorBox message={upcoming.error} />}

      <section>
        <SectionHead title="Pendaftaran dibuka" intro="Slot masih ada. Kuota tiap kategori terbatas." link={{ href: "/kalender?view=daftar", label: "Lihat semua race" }} />
        {upcoming.loading ? <Loading /> : <BibGrid events={open.slice(0, 6)} empty={<p className="text-muted">Belum ada pendaftaran yang dibuka.</p>} />}
      </section>

      {soon.length > 0 && (
        <section>
          <SectionHead title="Segera dibuka" intro="Catat jamnya. Race favorit biasanya habis di menit pertama." />
          <ul className="grid gap-3 md:grid-cols-2">
            {soon.map((e) => (
              <SoonRow key={e.id} e={e} />
            ))}
          </ul>
        </section>
      )}

      <section>
        <SectionHead title="Kalender race" intro="Start list beberapa bulan ke depan." link={{ href: "/kalender", label: "Buka kalender" }} />
        {upcoming.loading ? <Loading /> : <CalendarList events={events.slice(0, 10)} />}
      </section>

      <HowItWorks />

      <OrganizerPitch />

      <section>
        <SectionHead title="Sudah finish" intro="Arsip race yang sudah digelar di RaceGo." link={{ href: "/kalender?view=daftar&arsip=1", label: "Lihat arsip lengkap" }} />
        {past.loading ? <Loading /> : <BibGrid events={past.data?.events ?? []} />}
      </section>

      {orgs.data && orgs.data.organizers.length > 0 && (
        <section>
          <SectionHead title="Penyelenggara" link={{ href: "/penyelenggara", label: "Semua penyelenggara" }} />
          <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
            {orgs.data.organizers.map((o) => (
              <li key={o.slug}>
                <Link href={`/penyelenggara/${o.slug}`} className="flex items-center gap-3 rounded-md bg-card p-3 ring-1 ring-line hover:ring-asphalt">
                  <Monogram name={o.name} color={o.theme_color} logoUrl={o.logo_url} />
                  <span className="min-w-0">
                    <span className="block truncate text-sm font-semibold">{o.name}</span>
                    <span className="block text-xs text-muted">{o.city}</span>
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

/** Logo penyelenggara; tanpa logo (atau gagal dimuat) tampil inisial di atas warna tema. */
export function Monogram({ name, color, logoUrl, size = "sm" }: { name: string; color: string; logoUrl?: string | null; size?: "sm" | "lg" }) {
  const dim = size === "lg" ? "size-24 text-5xl" : "size-11 text-xl";
  const initialsBox = (
    <span className={`font-display grid shrink-0 place-items-center rounded-md font-black ${dim}`} style={{ background: color, color: textOn(color) }} aria-hidden>
      {initials(name)}
    </span>
  );
  if (!logoUrl) return initialsBox;
  return <UploadedImage url={logoUrl} alt={`Logo ${name}`} className={`shrink-0 rounded-md bg-white ${dim}`} fit="contain" fallback={initialsBox} />;
}

/** Hero kanan: BIB gelap besar dengan hitung mundur ke flag-off race terdekat. */
function NextFlagOff({ e }: { e: EventSummary }) {
  return (
    <Link href={`/events/${e.id}`} className="group relative block overflow-hidden rounded-md bg-asphalt p-8 text-white sm:p-10">
      <PinHoles />
      <div className="h-2 w-16 rounded-full" style={{ background: e.theme_color }} aria-hidden />
      <p className="mt-6 text-sm text-white/60">Flag-off terdekat yang masih bisa kamu ikuti</p>
      <h2 className="mt-2 text-3xl leading-tight font-semibold text-balance group-hover:underline group-hover:underline-offset-4">{e.name}</h2>
      <p className="mt-2 text-white/70">
        {formatDate(e.race_date)}, {e.start_time} di {e.venue}, {e.city}
      </p>
      <div className="mt-8">
        <BigCountdown target={flagOffTime(e.race_date, e.start_time)} />
      </div>
      <div className="mt-8 flex flex-wrap items-center justify-between gap-4 border-t border-dashed border-white/20 pt-6">
        <div className="flex flex-wrap gap-1.5">
          {e.distances.map((d) => (
            <span key={d} className="font-display rounded-sm bg-white px-2 py-0.5 text-sm font-bold text-asphalt">
              {d}
            </span>
          ))}
        </div>
        <span className={btnPrimary}>Lihat race</span>
      </div>
    </Link>
  );
}

function SoonRow({ e }: { e: EventSummary }) {
  const now = useNow();
  return (
    <li>
      <Link href={`/events/${e.id}`} className="flex items-center gap-4 overflow-hidden rounded-md bg-card ring-1 ring-line hover:ring-asphalt">
        <span className="w-2 self-stretch" style={{ background: e.theme_color }} aria-hidden />
        <div className="min-w-0 flex-1 py-4">
          <p className="truncate font-semibold">{e.name}</p>
          <p className="truncate text-sm text-muted">
            {e.city}, {e.distances.join(" / ")}
          </p>
        </div>
        <div className="pr-5 text-right">
          <p className="text-xs text-muted">Pendaftaran dibuka dalam</p>
          <p className="font-display text-2xl leading-tight font-extrabold" suppressHydrationWarning>
            {now ? humanizeUntil(new Date(e.sale_open_at).getTime() - now) : "…"}
          </p>
        </div>
      </Link>
    </li>
  );
}

const STEPS = [
  { title: "Pilih race", text: "Cari berdasarkan kota, bulan, atau jarak. Cek rute, harga, dan sisa kuota." },
  { title: "Amankan slot", text: "Klik daftar dan slotmu ditahan 15 menit, cukup untuk mengisi data peserta tanpa terburu-buru." },
  { title: "Bayar", text: "Virtual account, QRIS, e-wallet, atau kartu. Konfirmasi otomatis, tanpa kirim bukti transfer." },
  { title: "Tunjukkan QR", text: "Tiket QR dan nomor BIB langsung terbit. Tunjukkan saat ambil race pack, lalu tinggal lari." },
];

function HowItWorks() {
  return (
    <section>
      <SectionHead title="Daftar dalam empat langkah" />
      <ol className="grid gap-8 sm:grid-cols-2 lg:grid-cols-4">
        {STEPS.map((s, i) => (
          <li key={s.title} className="border-t-4 border-asphalt pt-4">
            <span className="font-display text-6xl leading-none font-black text-hivis">{i + 1}</span>
            <h3 className="mt-3 text-lg font-semibold">{s.title}</h3>
            <p className="mt-1 text-sm text-muted">{s.text}</p>
          </li>
        ))}
      </ol>
    </section>
  );
}

function OrganizerPitch() {
  return (
    <section className="grid gap-10 rounded-md bg-road p-8 text-white sm:p-12 lg:grid-cols-[1fr_1.2fr]">
      <div>
        <h2 className="font-display text-5xl leading-[0.92] font-extrabold text-balance">Kamu urus rute dan race day. Pendaftaran biar kami.</h2>
        <p className="mt-4 max-w-md text-white/70">Untuk komunitas, EO, kampus, dan perusahaan yang menggelar race lari.</p>
        <Link href="/untuk-penyelenggara" className={`${btnPrimary} mt-8`}>
          Jual tiket di RaceGo
        </Link>
      </div>
      <dl className="grid gap-x-8 gap-y-6 sm:grid-cols-2">
        {[
          ["Tahan war tiket", "Ribuan orang di detik yang sama tanpa tiket terjual dobel."],
          ["Pembayaran lokal", "VA, QRIS, e-wallet, kartu. Lunas terkonfirmasi otomatis."],
          ["Scan race pack", "Staf cukup buka web dari HP, scan QR, selesai."],
          ["Dana dicairkan", "Saldo per event tercatat rapi dan bisa dicairkan ke rekening."],
        ].map(([t, d]) => (
          <div key={t}>
            <dt className="font-semibold">{t}</dt>
            <dd className="mt-1 text-sm text-white/70">{d}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
