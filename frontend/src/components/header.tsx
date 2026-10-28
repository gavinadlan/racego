"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Suspense } from "react";
import { DEV_USERS } from "@/lib/format";
import { useSession } from "./session";

/** Logo: BIB mini dengan dua lubang peniti + wordmark. */
export function Logo({ light = false }: { light?: boolean }) {
  return (
    <span className="inline-flex items-center gap-2">
      <span className="relative inline-block h-6 w-8 rounded-[3px] bg-hivis" aria-hidden>
        <span className="absolute top-1 left-1 size-1 rounded-full bg-paper" />
        <span className="absolute top-1 right-1 size-1 rounded-full bg-paper" />
      </span>
      <span className={`font-display text-2xl leading-none font-black ${light ? "text-white" : ""}`}>RaceGo</span>
    </span>
  );
}

const NAV = [
  { href: "/kalender", label: "Kalender" },
  { href: "/penyelenggara", label: "Penyelenggara" },
  { href: "/untuk-penyelenggara", label: "Untuk penyelenggara" },
];

/**
 * Tautan navigasi. usePathname dipisah ke komponen sendiri di dalam <Suspense>
 * (syarat Next 16 dengan cacheComponents) supaya header tetap ikut prerender.
 */
function NavLinks({ active }: { active: (href: string) => boolean }) {
  const { userId, me } = useSession();
  const link = (href: string) => `whitespace-nowrap hover:text-asphalt ${active(href) ? "text-asphalt font-semibold" : "text-muted"}`;
  return (
    <>
      {NAV.map((n) => (
        <Link key={n.href} href={n.href} className={link(n.href)}>
          {n.label}
        </Link>
      ))}
      {userId && (
        <Link href="/tickets" className={link("/tickets")}>
          Tiket saya
        </Link>
      )}
      {me?.orgs.map((o) => (
        <Link key={o.slug} href={`/org/${o.slug}`} className={link(`/org/${o.slug}`)}>
          Dashboard {o.name}
        </Link>
      ))}
    </>
  );
}

function ActiveNavLinks() {
  const path = usePathname();
  return <NavLinks active={(href) => path.startsWith(href)} />;
}

export function Header() {
  const { ready, userId, switchUser } = useSession();

  return (
    <header className="sticky top-0 z-30 border-b border-line bg-paper/95 backdrop-blur">
      <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-8 gap-y-2 px-4 py-3 sm:px-6">
        <Link href="/" aria-label="RaceGo, beranda">
          <Logo />
        </Link>
        <nav className="order-3 -mx-1 flex w-full gap-5 overflow-x-auto px-1 text-sm sm:order-none sm:w-auto">
          <Suspense fallback={<NavLinks active={() => false} />}>
            <ActiveNavLinks />
          </Suspense>
        </nav>
        <label className="ml-auto flex items-center gap-2 text-xs text-muted">
          <span className="rounded-sm bg-asphalt px-1.5 py-0.5 font-semibold text-white">Dev</span>
          <select
            className="max-w-44 rounded-md border border-line bg-card px-2 py-1.5 text-sm text-asphalt"
            value={ready ? (userId ?? "") : ""}
            onChange={(e) => switchUser(e.target.value || null)}
            aria-label="Masuk sebagai akun contoh"
          >
            <option value="">Belum masuk</option>
            {DEV_USERS.map((u) => (
              <option key={u.id} value={u.id}>
                {u.label}
              </option>
            ))}
          </select>
        </label>
      </div>
    </header>
  );
}

export function Footer() {
  return (
    <footer className="mt-24 bg-asphalt text-white/70">
      <div className="mx-auto grid max-w-6xl gap-10 px-4 py-14 sm:grid-cols-2 sm:px-6 lg:grid-cols-4">
        <div>
          <Logo light />
          <p className="mt-4 max-w-xs text-sm">Tiket race lari dari komunitas dan penyelenggara di seluruh Indonesia.</p>
        </div>
        <div className="text-sm">
          <p className="mb-3 font-semibold text-white">Pelari</p>
          <ul className="space-y-2">
            <li><Link href="/kalender" className="hover:text-white">Kalender race</Link></li>
            <li><Link href="/kalender?arsip=1" className="hover:text-white">Arsip race</Link></li>
            <li><Link href="/tickets" className="hover:text-white">Tiket saya</Link></li>
          </ul>
        </div>
        <div className="text-sm">
          <p className="mb-3 font-semibold text-white">Penyelenggara</p>
          <ul className="space-y-2">
            <li><Link href="/penyelenggara" className="hover:text-white">Daftar penyelenggara</Link></li>
            <li><Link href="/untuk-penyelenggara" className="hover:text-white">Jual tiket di RaceGo</Link></li>
          </ul>
        </div>
        <div className="text-sm">
          <p className="mb-3 font-semibold text-white">Pembayaran</p>
          <ul className="flex flex-wrap gap-2">
            {["Virtual account", "QRIS", "GoPay", "ShopeePay", "Kartu kredit"].map((m) => (
              <li key={m} className="rounded-sm border border-white/20 px-2 py-1 text-xs">{m}</li>
            ))}
          </ul>
          <p className="mt-4 text-xs">Diproses oleh Midtrans (mode sandbox di proyek ini).</p>
        </div>
      </div>
      <div className="border-t border-white/10 py-5 text-center text-xs">RaceGo, proyek pribadi. Bukan layanan komersial.</div>
    </footer>
  );
}
