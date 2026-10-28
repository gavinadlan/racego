"use client";

import { useSyncExternalStore } from "react";
import { splitDuration } from "@/lib/format";

/**
 * Jam bersama: satu interval untuk semua countdown di halaman.
 * Saat render server/hydrate nilainya null, supaya teks waktu tidak berbeda antara server & klien.
 */
let listeners = new Set<() => void>();
let timer: ReturnType<typeof setInterval> | null = null;
let current = 0;

function subscribe(cb: () => void) {
  listeners.add(cb);
  if (!timer) {
    current = Date.now();
    timer = setInterval(() => {
      current = Date.now();
      listeners.forEach((l) => l());
    }, 1000);
  }
  return () => {
    listeners.delete(cb);
    if (listeners.size === 0 && timer) {
      clearInterval(timer);
      timer = null;
      listeners = new Set();
    }
  };
}

export function useNow(): number | null {
  return useSyncExternalStore(
    subscribe,
    () => {
      if (!current) current = Date.now(); // snapshot harus stabil di antara render
      return current;
    },
    () => null,
  );
}

/** Countdown besar bergaya papan waktu race (hari · jam · menit · detik). */
export function BigCountdown({ target, tone = "dark" }: { target: number; tone?: "dark" | "light" }) {
  const now = useNow();
  const d = splitDuration(now ? target - now : 0);
  const units: [number, string][] = [
    [d.days, "hari"],
    [d.hours, "jam"],
    [d.minutes, "menit"],
    [d.seconds, "detik"],
  ];
  const sub = tone === "dark" ? "text-white/60" : "text-muted";
  return (
    <div className="flex items-end gap-3 sm:gap-5" aria-label={`${d.days} hari ${d.hours} jam ${d.minutes} menit lagi`}>
      {units.map(([v, label]) => (
        <div key={label} className="text-center">
          <div className="font-display text-5xl leading-none font-extrabold tabular-nums sm:text-6xl" suppressHydrationWarning>
            {now ? String(v).padStart(2, "0") : "--"}
          </div>
          <div className={`mt-1 text-xs ${sub}`}>{label}</div>
        </div>
      ))}
    </div>
  );
}
