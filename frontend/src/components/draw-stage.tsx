"use client";

import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { ApiError, drawPrize, forfeitWinner, type DrawPrize, type DrawWinner } from "@/lib/api";
import { ConfirmDialog } from "./ui-client";

const REDUCED = "(prefers-reduced-motion: reduce)";
function useReducedMotion() {
  return useSyncExternalStore(
    (cb) => {
      const m = window.matchMedia(REDUCED);
      m.addEventListener("change", cb);
      return () => m.removeEventListener("change", cb);
    },
    () => window.matchMedia(REDUCED).matches,
    () => false,
  );
}

const SPIN_MS = 5000;
const scramble = (t: string) => Array.from(t, () => String(Math.floor(Math.random() * 10))).join("");

/**
 * Reel BIB: hasil sudah ditentukan server (target). Angka hanya berputar lalu melambat dan berhenti di target.
 * prefers-reduced-motion: tanpa putaran, hasil langsung tampil (transisi opasitas saja).
 */
function Reel({ target, reduced, onDone }: { target: string; reduced: boolean; onDone: () => void }) {
  const [shown, setShown] = useState(() => scramble(target));
  const done = useRef(onDone);
  useEffect(() => {
    done.current = onDone;
  });
  useEffect(() => {
    let t: ReturnType<typeof setTimeout>;
    if (reduced) {
      t = setTimeout(() => done.current(), 0);
      return () => clearTimeout(t);
    }
    let elapsed = 0;
    let delay = 40;
    const step = () => {
      elapsed += delay;
      if (elapsed >= SPIN_MS) {
        setShown(target);
        done.current();
        return;
      }
      setShown(scramble(target));
      delay = Math.min(delay * 1.1, 420); // melambat natural
      t = setTimeout(step, delay);
    };
    t = setTimeout(step, delay);
    return () => clearTimeout(t);
  }, [target, reduced]);
  return (
    <p className={`font-display font-extrabold tabular-nums leading-none text-hivis ${reduced ? "transition-opacity duration-300" : ""}`} style={{ fontSize: "clamp(4.5rem, 19vw, 18rem)" }} aria-hidden>
      {reduced ? target : shown}
    </p>
  );
}

type Phase = "idle" | "spinning" | "revealed";

export function DrawStage({
  base,
  eventName,
  prizes,
  winners,
  onChanged,
  onClose,
}: {
  base: string;
  eventName: string;
  prizes: DrawPrize[];
  winners: DrawWinner[];
  onChanged: () => void;
  onClose: () => void;
}) {
  const reduced = useReducedMotion();
  const [prizeId, setPrizeId] = useState(() => (prizes.find((p) => p.remaining > 0) ?? prizes[0]).id);
  const [phase, setPhase] = useState<Phase>("idle");
  const [last, setLast] = useState<DrawWinner | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirm, setConfirm] = useState(false);
  const key = useRef<string | null>(null); // kunci idempoten per klik; dipakai ulang hanya bila koneksi putus
  const prize = prizes.find((p) => p.id === prizeId) ?? prizes[0];
  const mine = winners.filter((w) => w.prize_id === prize.id);
  const locked = busy || phase === "spinning";

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !document.querySelector("dialog[open]")) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  async function draw() {
    setBusy(true);
    setError(null);
    const k = (key.current ??= crypto.randomUUID());
    try {
      const r = await drawPrize(base, prize.id, k);
      key.current = null;
      setLast(r.winners[0]);
      setPhase("spinning");
    } catch (e) {
      if (e instanceof ApiError) {
        key.current = null;
        setError(e.message);
      } else {
        setError("Koneksi terputus. Tekan Undi lagi untuk mencoba ulang. Tidak akan mengundi dua kali.");
      }
    } finally {
      setBusy(false);
    }
  }

  async function replace() {
    if (!last) return;
    setBusy(true);
    setError(null);
    try {
      await forfeitWinner(base, last.id, "Tidak hadir di panggung");
      setConfirm(false);
      setLast(null);
      setPhase("idle");
      onChanged();
    } catch (e) {
      setConfirm(false);
      setError(e instanceof ApiError ? e.message : "Koneksi terputus. Coba lagi.");
      setBusy(false);
      return;
    }
    setBusy(false);
    await draw();
  }

  const btn = "rounded-lg px-6 py-4 text-xl font-bold focus-visible:outline-4 focus-visible:outline-offset-2 focus-visible:outline-white disabled:opacity-40";
  const exhausted = prize.remaining <= 0 && phase !== "spinning";

  return (
    <div className="fixed inset-0 z-40 overflow-y-auto bg-black text-white" role="region" aria-label="Layar panggung undian">
      <div className="mx-auto flex min-h-full max-w-[1800px] flex-col gap-6 p-4 sm:p-8">
        <header className="flex flex-wrap items-center justify-between gap-3">
          <p className="text-lg font-semibold break-words text-white/80">{eventName}</p>
          <div className="flex items-center gap-3">
            <label className="text-sm text-white/80">
              <span className="sr-only">Hadiah</span>
              <select className="rounded-md border border-white/40 bg-black px-3 py-2 text-base text-white" value={prize.id} disabled={locked} onChange={(e) => { setPrizeId(e.target.value); setPhase("idle"); setLast(null); setError(null); }}>
                {prizes.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name} ({p.drawn}/{p.quantity})
                  </option>
                ))}
              </select>
            </label>
            <button type="button" className="rounded-md border border-white/40 px-4 py-2 text-base font-semibold hover:bg-white/10 focus-visible:outline-4 focus-visible:outline-white" onClick={onClose} disabled={phase === "spinning"}>
              Tutup (Esc)
            </button>
          </div>
        </header>

        <main className="flex flex-1 flex-col items-center justify-center gap-4 text-center">
          <h1 className="font-display font-extrabold break-words" style={{ fontSize: "clamp(2rem, 6vw, 5rem)" }}>
            {prize.name}
          </h1>
          <p className="text-xl text-white/70 tabular-nums">
            Terundi {prize.drawn} dari {prize.quantity}
            {prize.category_code && ` · khusus ${prize.category_code}`}
          </p>
          <div className="my-4 min-h-[1em]" data-testid="reel">
            {last ? <Reel key={last.id} target={last.bib} reduced={reduced} onDone={() => { setPhase("revealed"); onChanged(); }} /> : <p className="font-display font-extrabold text-white/20" style={{ fontSize: "clamp(4.5rem, 19vw, 18rem)" }} aria-hidden>
              ······
            </p>}
          </div>
          <div className="min-h-24" aria-live="polite" data-testid="stage-result">
            {phase === "revealed" && last && (
              <>
                <p className="font-display font-extrabold" style={{ fontSize: "clamp(2rem, 7vw, 6rem)" }}>
                  {last.display_name}
                </p>
                <p className="text-2xl text-white/80">
                  BIB {last.bib} · {last.category}
                </p>
              </>
            )}
          </div>
          {error && (
            <p role="alert" className="max-w-3xl rounded-md bg-red-700 px-4 py-3 text-lg font-semibold">
              {error}
            </p>
          )}
          <div className="flex flex-wrap justify-center gap-3">
            <button type="button" autoFocus className={`${btn} bg-hivis text-asphalt`} disabled={locked || exhausted} onClick={draw}>
              {busy && phase !== "spinning" ? "Mengundi…" : phase === "revealed" ? "Undi lagi" : "Undi"}
            </button>
            {phase === "revealed" && last?.status === "winner" && (
              <button type="button" className={`${btn} border-2 border-white/60`} disabled={locked} onClick={() => setConfirm(true)}>
                Tidak hadir → undi pengganti
              </button>
            )}
          </div>
          {exhausted && <p className="text-lg text-white/80">Semua hadiah ini sudah terundi.</p>}
        </main>

        <aside aria-label="Pemenang hadiah ini" className="rounded-lg bg-white/10 p-4">
          <h2 className="mb-2 text-lg font-semibold">Pemenang {prize.name}</h2>
          {mine.length === 0 ? (
            <p className="text-white/70">Belum ada.</p>
          ) : (
            <ul className="grid gap-x-6 gap-y-1 text-xl tabular-nums sm:grid-cols-2 lg:grid-cols-3">
              {mine.map((w) => (
                <li key={w.id} className={w.status === "forfeited" ? "text-white/40 line-through" : ""}>
                  <b>{w.bib}</b> · {w.display_name} · {w.category}
                </li>
              ))}
            </ul>
          )}
        </aside>
      </div>
      <ConfirmDialog open={confirm} title="Pemenang tidak hadir?" confirmLabel="Tandai gugur dan undi pengganti" danger busy={busy} onClose={() => setConfirm(false)} onConfirm={replace}>
        <p>
          BIB <b>{last?.bib}</b> ditandai gugur dan satu pemenang pengganti diundi. Tercatat di log audit.
        </p>
      </ConfirmDialog>
    </div>
  );
}
