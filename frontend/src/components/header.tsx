"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Suspense, useEffect, useRef, useState } from "react";
import { DEV_USERS, initials } from "@/lib/format";
import { LoginLink } from "./login-link";
import { useSession } from "./session";
import { btnDark } from "./ui";

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
  const link = (href: string) => `whitespace-nowrap hover:text-asphalt ${active(href) ? "text-asphalt font-semibold" : "text-muted"}`;
  return (
    <>
      {NAV.map((n) => (
        <Link key={n.href} href={n.href} className={link(n.href)}>
          {n.label}
        </Link>
      ))}
    </>
  );
}

function ActiveNavLinks() {
  const path = usePathname();
  return <NavLinks active={(href) => path.startsWith(href)} />;
}

/** Tombol inisial + menu akun. Pola disclosure: Tab menelusuri tautan, Escape/klik di luar menutup. */
function AccountMenu() {
  const { me, logout } = useSession();
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => !root.current?.contains(e.target as Node) && setOpen(false);
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setOpen(false);
      button.current?.focus();
    };
    const onFocus = (e: FocusEvent) => !root.current?.contains(e.target as Node) && setOpen(false);
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    document.addEventListener("focusin", onFocus);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("focusin", onFocus);
    };
  }, [open]);

  if (!me) return null;
  const item = "block w-full px-4 py-2 text-left text-sm hover:bg-paper focus:bg-paper focus:outline-none";
  const close = () => setOpen(false);
  return (
    <div ref={root} className="relative">
      <button
        ref={button}
        type="button"
        aria-expanded={open}
        aria-controls="account-menu"
        aria-label={`Menu akun ${me.name}`}
        onClick={() => setOpen((o) => !o)}
        className="font-display flex size-10 items-center justify-center rounded-full bg-asphalt text-lg font-bold text-white hover:bg-road focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-hivis"
      >
        {initials(me.name) || me.email[0].toUpperCase()}
      </button>
      {open && (
        <div id="account-menu" className="absolute right-0 z-40 mt-2 w-64 overflow-hidden rounded-md border border-line bg-card py-1 shadow-lg">
          <p className="truncate border-b border-line px-4 pt-2 pb-3 text-xs text-muted">
            <span className="block truncate text-sm font-semibold text-asphalt">{me.name}</span>
            {me.email}
          </p>
          <Link href="/tickets" className={item} onClick={close}>
            Tiket saya
          </Link>
          {me.orgs.map((o) => (
            <Link key={o.slug} href={`/org/${o.slug}`} className={item} onClick={close}>
              Dashboard {o.name}
            </Link>
          ))}
          {me.orgs.length === 0 && (
            <Link href="/daftar-mitra" className={item} onClick={close}>
              Jadi penyelenggara
            </Link>
          )}
          <Link href="/akun" className={item} onClick={close}>
            Akun
          </Link>
          <button
            type="button"
            className={`${item} border-t border-line`}
            onClick={async () => {
              close();
              await logout();
            }}
          >
            Keluar
          </button>
        </div>
      )}
    </div>
  );
}

function DevPicker() {
  const { ready, me, switchUser } = useSession();
  const devIds: readonly string[] = DEV_USERS.map((u) => u.id);
  return (
    <label className="flex items-center gap-2 text-xs text-muted">
      <span className="rounded-sm bg-asphalt px-1.5 py-0.5 font-semibold whitespace-nowrap text-white">Akun contoh (dev)</span>
      <select
        className="max-w-36 rounded-md border border-line bg-card px-2 py-1.5 text-sm text-asphalt"
        value={ready && me && devIds.includes(me.id) ? me.id : ""}
        onChange={(e) => switchUser(e.target.value || null)}
        aria-label="Masuk sebagai akun contoh (dev)"
      >
        <option value="">Tidak dipakai</option>
        {DEV_USERS.map((u) => (
          <option key={u.id} value={u.id}>
            {u.label}
          </option>
        ))}
      </select>
    </label>
  );
}

export function Header() {
  const { ready, me } = useSession();

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
        <div className="ml-auto flex items-center gap-3">
          {/* Dicek langsung (bukan lewat DEV_LOGIN) agar build produksi membuang DevPicker dari bundle. */}
          {process.env.NEXT_PUBLIC_DEV_LOGIN === "true" && <DevPicker />}
          {!ready ? (
            <span className="size-10" aria-hidden />
          ) : me ? (
            <AccountMenu />
          ) : (
            <LoginLink className={btnDark}>Masuk</LoginLink>
          )}
        </div>
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
