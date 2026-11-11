"use client";

import { useParams } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { apiFetch, ApiError, type AttendanceSettings, type ScanResult, type ScanTicket } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { SKEW_WARN_MS, useOfflineScanner, type LocalResult } from "@/lib/offline-scanner";
import type { ManifestTicket, QueueEntry, QueueKind } from "@/lib/offline-store";
import { useApi } from "@/lib/use-api";
import { useNow } from "./clock";
import { useSession } from "./session";
import { btnGhost, ErrorBox, Loading, LoginHint, PageTitle } from "./ui";
import { ConfirmDialog } from "./ui-client";

type Stats = { stats: { issued: number; collected: number; attended: number }; results: ScanTicket[] | null };
type Shown = (ScanResult | LocalResult) & { result: string };
type Mode = QueueKind;

// Mode terakhir diingat per event di perangkat ini (bukan data peserta).
const modeKey = (eventId: string) => `racego-scan-mode:${eventId}`;
const readMode = (eventId: string): Mode => {
  try {
    return window.localStorage.getItem(modeKey(eventId)) === "attend" ? "attend" : "collect";
  } catch {
    return "collect";
  }
};

// Hijau / kuning / merah besar: dibaca sekilas di bawah matahari, satu tangan.
const TONE: Record<string, { cls: string; icon: string }> = {
  ok: { cls: "border-emerald-600 bg-emerald-50 text-emerald-950", icon: "✓" },
  already_collected: { cls: "border-amber-500 bg-amber-50 text-amber-950", icon: "!" },
  already_attended: { cls: "border-amber-500 bg-amber-50 text-amber-950", icon: "!" },
  attend_not_open: { cls: "border-amber-500 bg-amber-50 text-amber-950", icon: "⏱" },
  not_in_manifest: { cls: "border-amber-500 bg-amber-50 text-amber-950", icon: "?" },
};
const RED = { cls: "border-red-600 bg-red-50 text-red-950", icon: "✕" };

/** "Judul. Penjelasan…" → judul besar + penjelasan lebih kecil. */
const headline = (msg: string): [string, string] => {
  const i = msg.indexOf(". ");
  return i < 0 ? [msg, ""] : [msg.slice(0, i), msg.slice(i + 2)];
};

const ago = (ms: number) => (ms < 60_000 ? "baru saja" : ms < 3_600_000 ? `${Math.round(ms / 60_000)} menit lalu` : `${Math.round(ms / 3_600_000)} jam lalu`);

export function Scanner() {
  const { org, eventId } = useParams<{ org: string; eventId: string }>();
  const { ready, userId, me } = useSession();
  const user = useMemo(() => (me ? { id: me.id, name: me.name } : null), [me]);
  const off = useOfflineScanner(org, eventId, user, ready);
  const st = off.state;
  const base = `/v1/org/${org}/events/${eventId}`;
  const [mode, setModeState] = useState<Mode>(() => (typeof window === "undefined" ? "collect" : readMode(eventId)));
  const [query, setQuery] = useState("");
  const [search, setSearch] = useState("");
  // Mode offline siap: progres & pencarian dari data lokal, tidak memanggil API.
  const { data, error, reload } = useApi<Stats>(userId && !st.ready ? `${base}/tickets?q=${encodeURIComponent(search)}` : null, [userId, st.ready]);

  const attendance = useApi<AttendanceSettings>(userId && mode === "attend" ? `${base}/attendance/settings` : null, [userId, mode]);
  const [result, setResult] = useState<Shown | null>(null);
  const setMode = (m: Mode) => {
    setModeState(m);
    setResult(null); // hasil dari mode lain tidak boleh terbaca sebagai hasil mode ini
    try {
      window.localStorage.setItem(modeKey(eventId), m);
    } catch {
      /* penyimpanan diblokir: mode tidak diingat */
    }
  };
  const [pickedUpBy, setPickedUpBy] = useState("");
  const [manualToken, setManualToken] = useState("");
  const [cameraOn, setCameraOn] = useState(false);
  const [cameraError, setCameraError] = useState<string | null>(null);
  const [dialog, setDialog] = useState<"prepare" | "clear" | "foreign" | null>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const resultRef = useRef<HTMLDivElement>(null);
  const lastToken = useRef<{ token: string; at: number } | null>(null);
  const busy = useRef(false);
  const now = useNow();

  // Halaman scanner tidak bisa dibuka ulang tanpa internet (lihat docs/05): saat offline, peramban
  // diminta mengonfirmasi sebelum memuat ulang/menutup tab. Antrean tetap aman di IndexedDB.
  const pendingCount = st.queue.filter((q) => q.state === "pending").length;
  useEffect(() => {
    if (!st.ready || (off.online && pendingCount === 0)) return;
    const warn = (e: BeforeUnloadEvent) => {
      if (navigator.onLine) return;
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [st.ready, off.online, pendingCount]);

  // Pustaka kamera dimuat saat masih online supaya kamera tetap bisa dinyalakan setelah sinyal hilang.
  useEffect(() => {
    if (st.ready) void import("@zxing/browser").catch(() => {});
  }, [st.ready]);

  const submit = useCallback(
    async (body: { token?: string; ticket_id?: string }) => {
      if (busy.current) return;
      busy.current = true;
      try {
        // Offline siap: selalu verifikasi lokal dulu; antrean dikirim di latar belakang.
        const local = await off.scanLocal({ ...body, picked_up_by: mode === "collect" ? pickedUpBy : undefined }, mode);
        if (local) {
          setResult(local);
          if (local.result === "ok") setPickedUpBy("");
          return;
        }
        const res = await apiFetch<ScanResult>(`${base}/${mode === "attend" ? "attend" : "scan"}`, {
          method: "POST",
          body: mode === "attend" ? body : { ...body, picked_up_by: pickedUpBy || undefined },
        });
        setResult(res);
        if (res.result === "ok") setPickedUpBy("");
        reload();
      } catch (e) {
        // Di luar jendela waktu server membalas 409 attend_not_open: tampil kuning, bukan galat umum.
        if (e instanceof ApiError && e.code === "attend_not_open") setResult({ result: "attend_not_open", message: e.message });
        else setResult({ result: "error", message: e instanceof ApiError ? e.message : "Gagal memproses" });
      } finally {
        busy.current = false;
      }
    },
    [base, pickedUpBy, reload, off, mode],
  );

  // Hasil baru selalu terlihat (HP satu tangan: tanpa menggulir mencari hasil).
  useEffect(() => {
    if (result) resultRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [result]);

  // Kamera: @zxing/browser bekerja di Chrome, Safari iOS, dan Firefox.
  // Butuh HTTPS atau localhost agar browser mengizinkan akses kamera.
  useEffect(() => {
    if (!cameraOn || !videoRef.current) return;
    let stop: (() => void) | undefined;
    let cancelled = false;
    (async () => {
      try {
        const { BrowserQRCodeReader } = await import("@zxing/browser");
        const reader = new BrowserQRCodeReader();
        const controls = await reader.decodeFromVideoDevice(undefined, videoRef.current!, (res) => {
          if (!res) return;
          const token = res.getText();
          const t = Date.now();
          // Abaikan QR yang sama selama 3 detik (kamera membaca berkali-kali per detik).
          if (lastToken.current?.token === token && t - lastToken.current.at < 3000) return;
          lastToken.current = { token, at: t };
          submit({ token });
        });
        if (cancelled) controls.stop();
        else stop = () => controls.stop();
      } catch {
        setCameraError("Kamera tidak bisa dibuka. Izinkan akses kamera, atau pakai input manual di bawah.");
        setCameraOn(false);
      }
    })();
    return () => {
      cancelled = true;
      stop?.();
    };
  }, [cameraOn, submit]);

  if (!ready || st.supported === null) return <Loading />;
  if (!userId && !off.owner) return <LoginHint />;

  const t = result?.ticket;
  const tone = result ? (TONE[result.result] ?? RED) : null;
  const progress = st.ready ? { issued: st.issued, collected: st.collected, attended: st.attended } : data?.stats;
  const attend = mode === "attend";
  const localResults: ManifestTicket[] = st.ready ? off.search(search) : [];
  const review = st.queue.filter((q) => q.state === "conflict" || q.state === "rejected");

  return (
    <>
      <PageTitle title={attend ? "Absen hari-H" : "Scan race pack"} back={{ href: `/org/${org}`, label: "Dashboard mitra" }}>
        {progress && (
          <p className="mt-2 text-sm text-muted" data-testid="progres">
            {attend ? "Hadir" : "Diambil"} <b className="text-asphalt tabular-nums">{attend ? progress.attended : progress.collected}</b> dari {progress.issued} tiket
            {st.ready && " (data perangkat ini)"}
          </p>
        )}
      </PageTitle>
      <ModeBar mode={mode} onMode={setMode} />
      {attend && attendance.data && !attendance.data.open && (
        <p className="mt-3 rounded-md bg-amber-100 p-3 text-sm font-semibold text-amber-950" data-testid="jendela-absen">
          Absen hari-H belum/tidak sedang dibuka. Jendela absen: {formatDateTime(attendance.data.effective_opens_at)} sampai {formatDateTime(attendance.data.effective_closes_at)}.
        </p>
      )}
      {error && !st.ready && <ErrorBox message={error} />}

      <OfflinePanel off={off} now={now} onDialog={setDialog} />

      <div className="mt-6 grid gap-6 lg:grid-cols-2">
        <section className="space-y-4">
          {/* Hasil di atas kamera: di HP langsung terlihat tanpa menggulir. */}
          <div ref={resultRef} className={`min-h-32 scroll-mt-24 rounded-md border-4 p-5 ${tone ? tone.cls : "border-dashed border-line bg-card text-muted"}`} role="status" aria-live="assertive" data-result={result?.result}>
            {!result ? (
              <p>{attend ? "Arahkan kamera ke QR tiket peserta yang hadir di lokasi lomba." : "Arahkan kamera ke QR tiket peserta."}</p>
            ) : (
              <>
                <p className="flex items-start gap-3 text-2xl leading-tight font-bold">
                  <span aria-hidden className="font-display text-4xl leading-none">
                    {tone?.icon}
                  </span>
                  <span>{headline(result.message)[0]}</span>
                </p>
                {headline(result.message)[1] && <p className="mt-2 text-base font-semibold">{headline(result.message)[1]}</p>}
                {t && (
                  <dl className="mt-4 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-base">
                    <dt>Nama</dt>
                    <dd className="font-semibold break-words">{t.participant_name}</dd>
                    <dt>Kategori · BIB</dt>
                    <dd className="font-display text-lg font-semibold">
                      {t.category_code} · {t.bib_number}
                    </dd>
                    {!attend && (
                      <>
                        <dt>Jersey</dt>
                        <dd className="text-2xl font-bold">{t.jersey_size}</dd>
                      </>
                    )}
                    {attend && t.attended_at && result.result !== "ok" && (
                      <>
                        <dt>Hadir</dt>
                        <dd>
                          {formatDateTime(t.attended_at)}
                          {t.attended_by_staff && ` · oleh ${t.attended_by_staff}`}
                        </dd>
                      </>
                    )}
                    {!attend && t.collected_at && result.result !== "ok" && (
                      <>
                        <dt>Diambil</dt>
                        <dd>
                          {formatDateTime(t.collected_at)}
                          {t.collected_by_staff && ` · oleh ${t.collected_by_staff}`}
                          {t.picked_up_by && ` (diwakilkan ${t.picked_up_by})`}
                        </dd>
                      </>
                    )}
                  </dl>
                )}
                {result.result === "ok" && !attend && <p className="mt-4 text-sm">Cocokkan nama dengan KTP sebelum menyerahkan race pack.</p>}
                {"local" in result && <p className="mt-3 text-xs opacity-70">Diverifikasi di perangkat ({result.ms} ms){result.result === "ok" ? ", menunggu terkirim ke server" : ""}.</p>}
              </>
            )}
          </div>

          <div className="overflow-hidden rounded-md border border-line bg-asphalt">
            {cameraOn ? (
              <video ref={videoRef} className="aspect-square w-full object-cover" muted playsInline />
            ) : (
              <button
                className="grid aspect-square w-full place-items-center text-hivis"
                onClick={() => {
                  setCameraError(null);
                  setCameraOn(true);
                }}
              >
                <span className="rounded-md border border-hivis/40 px-4 py-2 font-semibold">Nyalakan kamera</span>
              </button>
            )}
          </div>
          {cameraOn && (
            <button className="min-h-11 text-sm text-muted underline" onClick={() => setCameraOn(false)}>
              Matikan kamera
            </button>
          )}
          {cameraError && <ErrorBox message={cameraError} />}
          {!attend && (
            <label className="block text-sm">
              <span className="text-muted">Diwakilkan? Nama pengambil (opsional, berlaku untuk scan berikutnya)</span>
              <input className="mt-1 min-h-11 w-full rounded-md border border-line bg-card px-3 py-2" value={pickedUpBy} onChange={(e) => setPickedUpBy(e.target.value)} />
            </label>
          )}
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (manualToken.trim()) submit({ token: manualToken.trim() });
              setManualToken("");
            }}
          >
            <input
              className="min-h-11 min-w-0 flex-1 rounded-md border border-line bg-card px-3 py-2 font-display text-xs"
              placeholder="Tempel isi QR (v1.…) untuk uji tanpa kamera"
              aria-label="Isi QR"
              value={manualToken}
              onChange={(e) => setManualToken(e.target.value)}
            />
            <button className="min-h-11 rounded-md bg-hivis px-4 text-sm font-semibold text-asphalt">Cek</button>
          </form>
        </section>

        <section className="space-y-4">
          {review.length > 0 && <Review entries={review} onDismiss={(id) => void off.discard([id])} />}

          <div className="rounded-md border border-line bg-card p-5">
            <p className="text-sm font-semibold">HP pelari mati? Cari manual</p>
            {st.ready && <p className="mt-1 text-xs text-muted">Mode offline: cari nama atau nomor BIB. Pencarian 4 digit akhir NIK hanya tersedia online.</p>}
            <form
              className="mt-2 flex gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                setSearch(query);
                reload();
              }}
            >
              <input
                className="min-h-11 min-w-0 flex-1 rounded-md border border-line px-3 py-2 text-sm"
                placeholder={st.ready ? "Nama atau nomor BIB" : "Nama, nomor BIB, atau 4 digit akhir NIK"}
                aria-label="Cari peserta"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
              <button className="min-h-11 rounded-md border border-line px-4 text-sm">Cari</button>
            </form>
            <ul className="mt-3 divide-y divide-line text-sm">
              {(st.ready
                ? localResults.map((r) => ({ id: r.id, name: r.name, cat: r.category, bib: r.bib, status: r.status, attended: r.attended_at }))
                : (data?.results ?? []).map((r) => ({ id: r.id, name: r.participant_name, cat: r.category_code, bib: r.bib_number, status: r.status, attended: r.attended_at }))
              ).map((r) => (
                <li key={r.id} className="flex items-center justify-between gap-2 py-2">
                  <span className="min-w-0">
                    {r.name}{" "}
                    <span className="font-display text-muted">
                      {r.cat} · {r.bib}
                    </span>
                  </span>
                  {attend ? (
                    (r.status === "issued" || r.status === "collected") && !r.attended ? (
                      <button className="min-h-11 shrink-0 rounded-md bg-sky-700 px-3 text-xs font-semibold text-white" onClick={() => submit({ ticket_id: r.id })}>
                        Tandai hadir
                      </button>
                    ) : (
                      <span className="shrink-0 text-xs text-muted">{r.attended ? "sudah hadir" : r.status}</span>
                    )
                  ) : r.status === "issued" ? (
                    <button className="min-h-11 shrink-0 rounded-md bg-hivis px-3 text-xs font-semibold text-asphalt" onClick={() => submit({ ticket_id: r.id })}>
                      Tandai diambil
                    </button>
                  ) : (
                    <span className="shrink-0 text-xs text-muted">{r.status === "collected" ? "sudah diambil" : r.status}</span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        </section>
      </div>

      <ConfirmDialog open={dialog === "prepare"} title="Siapkan mode offline?" confirmLabel="Unduh data" busy={st.busy} onClose={() => setDialog(null)} onConfirm={async () => (await off.prepare(), setDialog(null))}>
        <p>
          Nama, BIB, kategori, dan ukuran jersey semua peserta event ini akan <b>tersimpan di perangkat ini</b> sampai masa berlakunya habis (bawaan 24 jam), kamu keluar, atau kamu menghapusnya.
        </p>
        <p>
          <b>Gunakan hanya di perangkat panitia.</b> Data tidak dienkripsi; perangkat yang hilang berisiko membocorkan daftar nama peserta.
        </p>
      </ConfirmDialog>
      <ConfirmDialog open={dialog === "clear"} title="Hapus data offline?" confirmLabel="Hapus data" danger onClose={() => setDialog(null)} onConfirm={async () => (await off.clearData(), setDialog(null))}>
        <p>Daftar peserta di perangkat ini dihapus. Scan yang belum terkirim tetap disimpan dan dikirim saat online.</p>
      </ConfirmDialog>
      <ConfirmDialog
        open={dialog === "foreign"}
        title="Buang antrean akun lain?"
        confirmLabel="Buang permanen"
        danger
        onClose={() => setDialog(null)}
        onConfirm={async () => (await off.discardForeign(), setDialog(null))}
      >
        <p>Scan ini dibuat akun lain di perangkat ini dan belum terkirim. Bila dibuang, tiketnya tetap tercatat belum diambil di server.</p>
      </ConfirmDialog>
    </>
  );
}

function OfflinePanel({
  off,
  now,
  onDialog,
}: {
  off: ReturnType<typeof useOfflineScanner>;
  now: number | null;
  onDialog: (d: "prepare" | "clear" | "foreign") => void;
}) {
  const st = off.state;
  const pending = st.queue.filter((q) => q.state === "pending").length;
  const m = st.manifest;
  const left = m && now ? Date.parse(m.expiresAt) - now : null;
  const status = st.syncing ? "Menyinkronkan…" : off.online ? "Online" : "Offline";
  const dot = st.syncing ? "bg-sky-500" : off.online ? "bg-emerald-600" : "bg-red-600";
  return (
    <section aria-labelledby="mode-offline" className="rounded-md border-2 border-line bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="mode-offline" className="flex items-center gap-2 text-base font-bold">
          <span aria-hidden className={`inline-block size-3 rounded-full ${dot}`} />
          <span aria-live="polite">{status}</span>
          <span className="font-normal text-muted">· {st.ready ? "mode offline siap" : "mode offline belum disiapkan"}</span>
        </h2>
        <p className="text-sm" aria-live="polite">
          Belum terkirim: <b className="tabular-nums">{pending}</b>
        </p>
      </div>
      {st.error && (
        <div className="mt-3">
          <ErrorBox message={st.error} />
        </div>
      )}
      {st.expiredNotice && <p className="mt-3 rounded-md bg-amber-100 p-3 text-sm text-amber-950">Data offline kedaluwarsa dan sudah dihapus. Sambungkan internet lalu unduh ulang sebelum scan offline.</p>}
      {st.switchedNotice && <p className="mt-3 rounded-md bg-amber-100 p-3 text-sm text-amber-950">Data offline milik akun lain di perangkat ini sudah dihapus.</p>}
      {m && now && (
        <p className="mt-2 text-sm text-muted">
          Data diunduh {ago(now - Date.parse(m.downloadedAt))} · berlaku sampai {formatDateTime(m.expiresAt)}
          {st.lastSyncAt && ` · sinkron terakhir ${ago(now - st.lastSyncAt)}`}
        </p>
      )}
      {left !== null && left < 2 * 3_600_000 && <p className="mt-2 rounded-md bg-amber-100 p-3 text-sm font-semibold text-amber-950">Data offline kedaluwarsa dalam {ago(-left).replace(" lalu", "")}. Perbarui saat online.</p>}
      {Math.abs(st.clockSkewMs) > SKEW_WARN_MS && (
        <p className="mt-2 rounded-md bg-amber-100 p-3 text-sm text-amber-950">
          Jam perangkat berbeda {Math.round(Math.abs(st.clockSkewMs) / 60_000)} menit dari server. Scan tetap aman (server memakai jamnya sendiri), tapi perbaiki pengaturan jam perangkat.
        </p>
      )}
      {st.syncError && <p className="mt-2 text-sm text-red-800">{st.syncError}</p>}
      {st.ready && (
        <p className="mt-2 rounded-md bg-red-50 p-3 text-sm font-semibold text-red-900" data-testid="peringatan-muat-ulang">
          Jangan muat ulang atau menutup halaman ini saat offline: halaman scanner hanya bisa dibuka ulang dengan internet. Scan yang belum terkirim tetap tersimpan di perangkat dan dikirim saat online.
        </p>
      )}
      {st.foreignQueue > 0 && (
        <p className="mt-2 text-sm">
          {st.foreignQueue} scan milik akun lain belum terkirim (masuk dengan akun itu untuk mengirim).{" "}
          <button type="button" className="underline" onClick={() => onDialog("foreign")}>
            Buang
          </button>
        </p>
      )}
      <div className="mt-3 flex flex-wrap gap-2">
        {!st.ready ? (
          <button type="button" className={`${btnGhost} min-h-11`} disabled={!off.online || st.busy || st.supported === false} onClick={() => onDialog("prepare")}>
            Siapkan mode offline
          </button>
        ) : (
          <>
            <button type="button" className={`${btnGhost} min-h-11`} disabled={!off.online || st.busy} onClick={() => void off.refresh()}>
              {st.busy ? "Memperbarui…" : "Perbarui data"}
            </button>
            <button type="button" className={`${btnGhost} min-h-11`} disabled={!off.online || st.syncing || pending === 0} onClick={() => void off.sync()}>
              Sinkronkan sekarang
            </button>
            <button type="button" className={`${btnGhost} min-h-11 text-red-700`} onClick={() => onDialog("clear")}>
              Hapus data offline
            </button>
          </>
        )}
      </div>
    </section>
  );
}

function Review({ entries, onDismiss }: { entries: QueueEntry[]; onDismiss: (id: string) => void }) {
  return (
    <section aria-labelledby="ditinjau" className="rounded-md border-2 border-amber-500 bg-amber-50 p-4 text-amber-950">
      <h2 id="ditinjau" className="font-bold">
        Perlu ditinjau ({entries.length})
      </h2>
      <p className="mt-1 text-sm">Scan offline ini tidak diterima server. Tidak ada yang ditimpa otomatis; periksa lalu tutup.</p>
      <ul className="mt-3 divide-y divide-amber-300 text-sm">
        {entries.map((e) => (
          <li key={e.client_op_id} className="flex flex-wrap items-center justify-between gap-2 py-2">
            <span className="min-w-0">
              <b>{e.name}</b> <span className="font-display">{e.bib}</span>
              <span className="block">
                {e.state === "conflict"
                  ? e.kind === "attend"
                    ? `Sudah diabsen lebih dulu${e.server_attended_by ? ` oleh ${e.server_attended_by}` : ""}${e.server_attended_at ? ` pada ${formatDateTime(e.server_attended_at)}` : ""}. Absen di perangkat ini ${formatDateTime(e.scanned_at)}.`
                    : `Sudah diambil lebih dulu${e.server_collected_by ? ` oleh ${e.server_collected_by}` : ""}${e.server_collected_at ? ` pada ${formatDateTime(e.server_collected_at)}` : ""}. Scan di perangkat ini ${formatDateTime(e.scanned_at)}.`
                  : `Ditolak${e.kind === "attend" ? " (absen)" : ""}: ${e.message ?? e.result}`}
              </span>
            </span>
            <button type="button" className="min-h-11 rounded-md border border-amber-600 px-3 text-sm font-semibold" onClick={() => onDismiss(e.client_op_id)}>
              Tutup<span className="sr-only"> {e.name}</span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Pemilih mode + penanda besar mode aktif: staf tidak boleh salah mode saat antrean panjang. */
function ModeBar({ mode, onMode }: { mode: Mode; onMode: (m: Mode) => void }) {
  const attend = mode === "attend";
  const btn = (m: Mode, label: string) => (
    <button
      type="button"
      aria-pressed={mode === m}
      onClick={() => onMode(m)}
      className={`min-h-12 flex-1 rounded-md border-2 px-4 text-base font-bold ${mode === m ? "border-asphalt bg-asphalt text-white" : "border-line bg-card text-asphalt hover:border-asphalt"}`}
    >
      {label}
    </button>
  );
  return (
    <section aria-label="Mode scanner" className="space-y-3">
      <div role="group" aria-label="Pilih mode" className="flex gap-2">
        {btn("collect", "Ambil race pack")}
        {btn("attend", "Absen hari-H")}
      </div>
      <p
        data-testid="mode-aktif"
        data-mode={mode}
        className={`rounded-md px-4 py-3 text-center font-display text-2xl leading-none font-extrabold tracking-wide uppercase ${attend ? "bg-sky-700 text-white" : "bg-hivis text-asphalt"}`}
      >
        Mode: {attend ? "Absen hari-H" : "Ambil race pack"}
      </p>
    </section>
  );
}
