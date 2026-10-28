"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { qs, type EventFilters } from "@/lib/api";
import { monthKey, monthLong } from "@/lib/format";
import { useNow } from "./clock";

/** Pencarian race: kota, bulan, jarak → hasil di halaman kalender (tampilan daftar). */
export function SearchBar({ filters, initial }: { filters: EventFilters | null; initial?: { city?: string; month?: string; distance?: string } }) {
  const router = useRouter();
  const [city, setCity] = useState(initial?.city ?? "");
  const [month, setMonth] = useState(initial?.month ?? "");
  const [distance, setDistance] = useState(initial?.distance ?? "");
  // Daftar bulan dihitung di browser (bukan saat build) agar selalu mulai dari bulan ini.
  const now = useNow();
  const months = Array.from({ length: now ? 9 : 0 }, (_, i) => {
    const d = new Date(now!);
    d.setDate(1);
    d.setMonth(d.getMonth() + i);
    return { key: monthKey(d), label: monthLong(d) };
  });
  const field = "w-full appearance-none bg-transparent py-1 text-base font-semibold focus:outline-none";

  return (
    <form
      className="grid overflow-hidden rounded-md border border-asphalt bg-card sm:grid-cols-[1fr_1fr_1fr_auto]"
      onSubmit={(e) => {
        e.preventDefault();
        router.push(`/kalender${qs({ view: "daftar", city, month, distance })}`);
      }}
    >
      <label className="border-b border-line px-4 py-2.5 sm:border-r sm:border-b-0">
        <span className="block text-xs text-muted">Kota</span>
        <select className={field} value={city} onChange={(e) => setCity(e.target.value)}>
          <option value="">Semua kota</option>
          {filters?.cities.map((c) => <option key={c}>{c}</option>)}
        </select>
      </label>
      <label className="border-b border-line px-4 py-2.5 sm:border-r sm:border-b-0">
        <span className="block text-xs text-muted">Bulan</span>
        <select className={field} value={month} onChange={(e) => setMonth(e.target.value)}>
          <option value="">Kapan saja</option>
          {months.map((m) => (
            <option key={m.key} value={m.key}>
              {m.label}
            </option>
          ))}
        </select>
      </label>
      <label className="border-b border-line px-4 py-2.5 sm:border-b-0">
        <span className="block text-xs text-muted">Jarak</span>
        <select className={field} value={distance} onChange={(e) => setDistance(e.target.value)}>
          <option value="">Semua jarak</option>
          {filters?.distances.map((d) => <option key={d}>{d}</option>)}
        </select>
      </label>
      <button className="bg-hivis px-7 py-4 font-semibold text-asphalt transition-colors hover:bg-[#ff7445]">Cari race</button>
    </form>
  );
}
