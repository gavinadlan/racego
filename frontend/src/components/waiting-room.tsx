"use client";

import { useEffect, useRef, useState } from "react";
import { ApiError, joinQueue, leaveQueue, queueStatus, type EventDetail, type QueueState } from "@/lib/api";
import { formatCountdown, formatDateTime } from "@/lib/format";
import { PinHoles } from "./bib";
import { BigCountdown, useNow } from "./clock";
import { LoginLink } from "./login-link";
import { useSession } from "./session";
import { btnGhost, btnPrimary } from "./ui";

// Pass disimpan per event di sessionStorage (hilang saat tab ditutup, tidak terbawa ke tab lain).
const passKey = (eventId: string) => `racego.queuePass.${eventId}`;

export function storedPass(eventId: string): string | null {
  try {
    return window.sessionStorage.getItem(passKey(eventId));
  } catch {
    return null;
  }
}

function storePass(eventId: string, pass: string | null) {
  try {
    if (pass) window.sessionStorage.setItem(passKey(eventId), pass);
    else window.sessionStorage.removeItem(passKey(eventId));
  } catch {
    /* sessionStorage bisa tidak tersedia (mode privat) */
  }
}

/** Kartu antrian tampil bila event memakai antrian dan penjualan belum lewat. */
export const queueVisible = (event: EventDetail) => event.waiting_room && (event.status === "upcoming" || event.status === "open");

const ACTIVE = new Set(["lobby", "waiting", "admitted"]);

type Snapshot = {
  st: QueueState;
  offset: number; // jam server − jam perangkat (ms)
  firstAhead: number | null; // posisi saat pertama menunggu, untuk bilah progres
  lost: boolean; // sebelumnya di antrian/dipanggil, sekarang tidak (pass habis, tab lama ditutup, data antrian hilang)
};

/**
 * Antrian virtual di halaman event. onPass dipanggil dengan pass saat dipanggil, dan null saat pass
 * tidak berlaku lagi. resetKey berubah = muat ulang status (mis. Reserve menolak pass).
 */
export function WaitingRoom({ event, onPass, resetKey }: { event: EventDetail; onPass: (pass: string | null) => void; resetKey: number }) {
  const { ready, userId } = useSession();
  const now = useNow();
  const [snap, setSnap] = useState<Snapshot | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [announce, setAnnounce] = useState("");
  const announced = useRef<{ state: string; position: number | null } | null>(null);
  // Jadwal polling berikutnya: dari poll_after_ms server (sudah berjitter), atau jeda coba lagi.
  const [poll, setPoll] = useState<{ ms: number; seq: number } | null>(null);
  const schedule = (ms: number) => setPoll((p) => ({ ms, seq: (p?.seq ?? 0) + 1 }));
  const onPassRef = useRef(onPass);
  useEffect(() => {
    onPassRef.current = onPass;
  });

  // Satu tempat untuk menerima status baru dari server.
  function accept(st: QueueState, receivedAt: number) {
    setError(null);
    setSnap((prev) => {
      const wasIn = !!prev && (ACTIVE.has(prev.st.state) || prev.lost);
      const lost = st.state === "not_in_queue" && wasIn;
      const firstAhead = st.state === "waiting" ? (prev?.firstAhead ?? st.ahead) : null;
      return { st, offset: Date.parse(st.server_time) - receivedAt, firstAhead, lost };
    });
    const pass = st.state === "admitted" ? (st.pass ?? null) : null;
    storePass(event.id, pass);
    onPassRef.current(pass);
    // Tetap polling saat tab tersembunyi: polling sekaligus tanda hidup.
    if (ACTIVE.has(st.state) && st.poll_after_ms > 0) schedule(st.poll_after_ms);
    // Pembaca layar: umumkan hanya saat status berubah atau posisi bergeser ≥ 10.
    const last = announced.current;
    const moved = st.position !== null && last?.position != null && Math.abs(last.position - st.position) >= 10;
    if (!last || last.state !== st.state || moved) {
      announced.current = { state: st.state, position: st.position };
      setAnnounce(announcement(st));
    }
  }

  // Gagal sementara (429, jaringan): coba lagi setelah jeda tanpa mengubah tampilan status.
  function fail(e: unknown) {
    if (e instanceof ApiError && e.status === 429) {
      schedule((e.retryAfter ?? 1) * 1000 + 300);
    } else if (e instanceof ApiError && e.status !== 0) {
      setError(e.message);
    } else {
      setError("Koneksi terputus. Mencoba lagi…");
      schedule(5000);
    }
  }

  function fetchStatus(cancelled: () => boolean) {
    queueStatus(event.id).then(
      (st) => !cancelled() && accept(st, Date.now()),
      (e) => !cancelled() && fail(e),
    );
  }

  // Status awal (dan setiap resetKey berubah).
  useEffect(() => {
    if (!userId) return;
    let cancelled = false;
    fetchStatus(() => cancelled);
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userId, event.id, resetKey]);

  useEffect(() => {
    if (!poll) return;
    let cancelled = false;
    const t = setTimeout(() => fetchStatus(() => cancelled), poll.ms);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [poll]);

  const st = snap?.st;

  async function join() {
    setBusy(true);
    setError(null);
    try {
      accept(await joinQueue(event.id), Date.now());
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Gagal masuk antrian");
    } finally {
      setBusy(false);
    }
  }

  async function leave() {
    setBusy(true);
    try {
      await leaveQueue(event.id);
      storePass(event.id, null);
      onPassRef.current(null);
      setSnap((s) => s && { ...s, st: { ...s.st, state: "not_in_queue", position: null, ahead: null, pass: undefined }, lost: false, firstAhead: null });
      announced.current = { state: "not_in_queue", position: null };
      setAnnounce("Kamu keluar dari antrian.");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Gagal keluar antrian");
    } finally {
      setBusy(false);
    }
  }

  const offset = snap?.offset ?? 0;
  const serverNow = now === null ? null : now + offset;
  const toLocal = (iso: string) => Date.parse(iso) - offset; // target jam server → jam perangkat
  const lobbyOpensAt = st?.lobby_opens_at ?? event.lobby_opens_at ?? event.sale_open_at;
  const saleOpenAt = st?.sale_open_at ?? event.sale_open_at;
  const beforeLobby = serverNow !== null && serverNow < Date.parse(lobbyOpensAt);
  const beforeSale = serverNow === null || serverNow < Date.parse(saleOpenAt);

  let body: React.ReactNode;
  if (!ready || serverNow === null || (userId && !snap && !error)) {
    body = <p className="mt-4 text-sm text-white/70">Memeriksa antrian…</p>;
  } else if (beforeLobby && (!st || st.state === "not_in_queue")) {
    body = (
      <>
        <p className="mt-4 text-sm text-white/70">Antrian dibuka dalam</p>
        <div className="mt-2">
          <BigCountdown target={toLocal(lobbyOpensAt)} />
        </div>
        <p className="mt-4 text-sm text-white/70">Antrian dibuka {formatDateTime(lobbyOpensAt)}, penjualan {formatDateTime(saleOpenAt)}.</p>
        {!userId && <LoginCta />}
      </>
    );
  } else if (!userId) {
    body = (
      <>
        <p className="mt-4">{beforeSale ? "Lobi antrian sudah dibuka." : "Penjualan berjalan lewat antrian."} Masuk dulu untuk mengambil tempat.</p>
        <LoginCta />
      </>
    );
  } else if (!st) {
    body = null;
  } else if (st.state === "sold_out") {
    body = <FinalMessage title="Kuota habis">Semua slot sudah terjual. Antrian ditutup. Terima kasih sudah menunggu.</FinalMessage>;
  } else if (st.state === "closed") {
    body = <FinalMessage title="Penjualan ditutup">Pendaftaran event ini sudah ditutup penyelenggara.</FinalMessage>;
  } else if (st.state === "not_in_queue") {
    body = (
      <>
        {snap?.lost ? (
          <p className="mt-4">
            <b>Kamu sudah tidak ada di antrian.</b> Biasanya karena giliranmu habis atau halaman ini lama tertutup. Kamu bisa bergabung lagi, di urutan belakang.
          </p>
        ) : (
          <p className="mt-4">{beforeSale ? "Lobi sudah dibuka. Masuk sekarang supaya ikut diundi saat jam buka." : "Ambil nomor antrian untuk mendaftar."}</p>
        )}
        <button className={`${btnPrimary} mt-4 w-full sm:w-auto`} disabled={busy} onClick={join}>
          {busy ? "Memproses…" : snap?.lost ? "Bergabung lagi" : beforeSale ? "Masuk lobi" : "Masuk antrian"}
        </button>
      </>
    );
  } else if (st.state === "lobby") {
    body = (
      <>
        <p className="mt-4 text-lg font-semibold">Kamu di lobi. Urutan diacak pada {formatDateTime(saleOpenAt)}.</p>
        <p className="mt-1 text-sm text-white/70">{st.queue_size.toLocaleString("id-ID")} orang di lobi. Penjualan dibuka dalam</p>
        <div className="mt-2">
          <BigCountdown target={toLocal(saleOpenAt)} />
        </div>
        <KeepOpen />
        <LeaveButton busy={busy} onLeave={leave} />
      </>
    );
  } else if (st.state === "waiting") {
    const ahead = st.ahead ?? 0;
    const first = snap?.firstAhead ?? ahead;
    const progress = first > 0 ? Math.min(1, Math.max(0, 1 - ahead / first)) : 1;
    body = (
      <>
        <p className="mt-4 text-sm text-white/70">Posisi kamu</p>
        <p className="font-display text-7xl leading-none font-black tabular-nums">{st.position?.toLocaleString("id-ID")}</p>
        <p className="mt-2 text-sm">
          {ahead === 0 ? "Kamu paling depan. Sebentar lagi giliranmu." : `${ahead.toLocaleString("id-ID")} orang di depanmu.`}
          {st.eta_seconds !== null && ahead > 0 && <> Perkiraan {etaText(st.eta_seconds)}.</>}
        </p>
        <div
          className="mt-4 h-2 overflow-hidden rounded-full bg-white/15"
          role="progressbar"
          aria-label="Kemajuan antrian"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(progress * 100)}
        >
          <div className="h-full bg-hivis transition-[width] duration-700 motion-reduce:transition-none" style={{ width: `${Math.max(2, progress * 100)}%` }} />
        </div>
        <KeepOpen />
        <LeaveButton busy={busy} onLeave={leave} />
      </>
    );
  } else {
    const left = st.pass_expires_at ? toLocal(st.pass_expires_at) - (now ?? 0) : 0;
    body = (
      <>
        <p className="font-display mt-4 text-5xl leading-none font-black text-hivis">Giliran kamu!</p>
        <p className="mt-2">Pilih kategori di bawah lalu tekan Daftar. Slot baru aman setelah kamu menekan Daftar.</p>
        <p className="mt-3 text-sm text-white/70">
          Giliranmu berlaku <span className="font-display text-2xl font-black text-white tabular-nums">{formatCountdown(left)}</span> lagi.
        </p>
      </>
    );
  }

  return (
    <div className="relative overflow-hidden rounded-md bg-asphalt p-6 text-white sm:p-8">
      <PinHoles />
      <h3 className="font-display text-3xl leading-none font-extrabold">Antrian</h3>
      {body}
      {error && <p className="mt-3 text-sm text-hivis">{error}</p>}
      {(!st || ["not_in_queue", "lobby"].includes(st.state)) && (
        <details className="mt-5 border-t border-dashed border-white/25 pt-4 text-sm text-white/75">
          <summary className="cursor-pointer font-semibold text-white">Cara kerja antrian</summary>
          <ul className="mt-2 list-disc space-y-1 pl-5">
            <li>Lobi dibuka {minutesBefore(lobbyOpensAt, saleOpenAt)} menit sebelum penjualan. Siapa pun yang masuk lobi sebelum jam buka diundi urutannya saat penjualan dibuka, jadi tidak perlu berebut detik pertama.</li>
            <li>Yang datang setelah jam buka mengantre di belakang peserta lobi, sesuai urutan datang.</li>
            <li>Kami memanggil beberapa orang tiap detik. Saat giliranmu tiba, tombol Daftar aktif selama beberapa menit.</li>
            <li>Satu akun satu tempat. Biarkan halaman ini terbuka sampai giliranmu.</li>
          </ul>
        </details>
      )}
      <p className="sr-only" aria-live="polite">
        {announce}
      </p>
    </div>
  );
}

function LoginCta() {
  return (
    <LoginLink className={`${btnPrimary} mt-4 w-full sm:w-auto`}>Masuk untuk antri</LoginLink>
  );
}

function KeepOpen() {
  return (
    <p className="mt-4 rounded-md bg-white/10 px-3 py-2 text-sm">
      <b>Biarkan halaman ini terbuka.</b> Jika ditutup lebih dari 45 detik, kamu keluar dari antrian.
    </p>
  );
}

function LeaveButton({ busy, onLeave }: { busy: boolean; onLeave: () => void }) {
  return (
    <button className={`${btnGhost} mt-4 border-white/30 bg-transparent text-white hover:border-white`} disabled={busy} onClick={onLeave}>
      Keluar antrian
    </button>
  );
}

function FinalMessage({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <>
      <p className="font-display mt-4 text-4xl leading-none font-black">{title}</p>
      <p className="mt-2 text-white/80">{children}</p>
    </>
  );
}

function etaText(secs: number) {
  if (secs < 60) return "kurang dari 1 menit";
  return `± ${Math.round(secs / 60)} menit`;
}

function minutesBefore(lobby: string, sale: string) {
  return Math.round((Date.parse(sale) - Date.parse(lobby)) / 60_000);
}

function announcement(st: QueueState) {
  switch (st.state) {
    case "lobby":
      return "Kamu di lobi antrian. Urutan diacak saat penjualan dibuka.";
    case "waiting":
      return `Posisi antrian ${st.position}. ${st.ahead} orang di depanmu.`;
    case "admitted":
      return "Giliran kamu. Tombol Daftar sudah aktif.";
    case "sold_out":
      return "Kuota habis. Antrian ditutup.";
    case "closed":
      return "Penjualan ditutup.";
    default:
      return "";
  }
}
