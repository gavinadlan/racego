"use client";

import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { apiFetch, ApiError, type ScanResult } from "./api";
import {
  deleteManifests,
  deleteQueue,
  deviceId,
  enqueue,
  getManifest,
  listQueue,
  allTickets,
  OfflineStoreError,
  offlineSupported,
  saveManifest,
  updateQueue,
  putTicket,
  type ManifestTicket,
  type QueueEntry,
  type QueueKind,
  type StoredManifest,
} from "./offline-store";
import { formatDateTime } from "./format";
import { ed25519Supported, verifyQR } from "./qr-verify";

// ===== Status jaringan =====

function subscribeOnline(cb: () => void) {
  window.addEventListener("online", cb);
  window.addEventListener("offline", cb);
  return () => {
    window.removeEventListener("online", cb);
    window.removeEventListener("offline", cb);
  };
}

export function useOnline() {
  return useSyncExternalStore(
    subscribeOnline,
    () => navigator.onLine,
    () => true,
  );
}

// ===== Tipe =====

/** Hasil scan lokal: sama dengan online, plus "not_in_manifest" (QR sah tapi tiket belum ada di data offline). */
export type LocalResult = Omit<ScanResult, "result"> & { result: ScanResult["result"] | "not_in_manifest"; local: true; ms: number };

type ManifestResponse = {
  event: { id: string; name: string; org_slug: string };
  public_key: string;
  generated_at: string;
  expires_at: string;
  version: string;
  incremental: boolean;
  attendance_window?: { opens_at: string; closes_at: string };
  tickets: ManifestTicket[];
};

type BatchResponse = {
  server_time: string;
  results: {
    client_op_id: string;
    result: string;
    message: string;
    ticket?: { collected_at?: string; collected_by_staff?: string; picked_up_by?: string; attended_at?: string; attended_by_staff?: string };
  }[];
};

export const SKEW_WARN_MS = 5 * 60_000;
const SYNC_EVERY_MS = 30_000;
const MAX_BACKOFF_MS = 5 * 60_000;
const BATCH = 200;

export type OfflineState = {
  supported: boolean | null; // null = belum dicek
  error: string | null; // penyimpanan gagal/diblokir: mode offline dimatikan
  manifest: StoredManifest | null;
  ready: boolean; // manifest ada & belum kedaluwarsa
  expiredNotice: boolean; // manifest kedaluwarsa sudah dihapus
  switchedNotice: boolean; // data milik akun lain dihapus
  issued: number; // tiket berlaku (issued + collected)
  collected: number;
  attended: number; // sudah diabsen hari-H (tiket berlaku)
  queue: QueueEntry[]; // milik pengguna ini
  foreignQueue: number; // antrean belum terkirim milik akun lain
  syncing: boolean;
  lastSyncAt: number | null;
  syncError: string | null;
  clockSkewMs: number;
  busy: boolean;
};

const initial: OfflineState = {
  supported: null,
  error: null,
  manifest: null,
  ready: false,
  expiredNotice: false,
  switchedNotice: false,
  issued: 0,
  collected: 0,
  attended: 0,
  queue: [],
  foreignQueue: 0,
  syncing: false,
  lastSyncAt: null,
  syncError: null,
  clockSkewMs: 0,
  busy: false,
};

// Galat jaringan (status 0) memakai pesan khusus scanner, bukan pesan umum apiFetch yang ditujukan ke developer.
const errText = (e: unknown, fallback: string) => (e instanceof OfflineStoreError || (e instanceof ApiError && e.status !== 0) ? e.message : fallback);

/**
 * Scanner offline untuk satu event. user = pengguna yang login (null bila sesi tidak bisa dicek karena
 * offline: data offline yang ada dipakai atas nama pemiliknya).
 */
export function useOfflineScanner(org: string, eventId: string, user: { id: string; name: string } | null, sessionReady: boolean) {
  const [s, setS] = useState<OfflineState>(initial);
  const tickets = useRef(new Map<string, ManifestTicket>());
  const online = useOnline();
  const syncing = useRef(false);
  const backoff = useRef(0);
  const nextSyncAt = useRef(0);
  const lastRefresh = useRef(0);
  const base = `/v1/org/${org}/events/${eventId}`;

  const offlineOwner = s.manifest && !online ? s.manifest : null;
  const owner = useMemo(() => user ?? (offlineOwner ? { id: offlineOwner.userId, name: offlineOwner.staffName } : null), [user, offlineOwner]);
  const ownerId = owner?.id ?? null;

  const patch = useCallback((p: Partial<OfflineState>) => setS((cur) => ({ ...cur, ...p })), []);

  const recount = useCallback(() => {
    let issued = 0;
    let collected = 0;
    let attended = 0;
    for (const t of tickets.current.values()) {
      if (t.status === "issued" || t.status === "collected") issued++;
      if (t.status === "collected") collected++;
      if ((t.status === "issued" || t.status === "collected") && t.attended_at) attended++;
    }
    return { issued, collected, attended };
  }, []);

  /** Muat ulang semua dari IndexedDB. */
  const load = useCallback(async () => {
    if (!offlineSupported()) {
      patch({ supported: false, error: "Peramban ini tidak menyediakan penyimpanan offline (IndexedDB)." });
      return;
    }
    try {
      let m = await getManifest(eventId);
      const p: Partial<OfflineState> = { supported: true, error: null };
      if (m && user && m.userId !== user.id) {
        await deleteManifests(eventId); // berganti pengguna di perangkat yang sama
        m = null;
        p.switchedNotice = true;
      }
      if (m && Date.parse(m.expiresAt) <= Date.now()) {
        await deleteManifests(eventId); // data basi berisiko menerima tiket yang sudah di-refund
        m = null;
        p.expiredNotice = true;
      }
      if (m && !(await ed25519Supported(m.publicKey))) {
        patch({ supported: false, error: "Peramban ini belum mendukung verifikasi tanda tangan Ed25519. Perbarui peramban (Chrome/Edge 137+, Safari 17+, Firefox 129+) untuk mode offline." });
        return;
      }
      const map = new Map<string, ManifestTicket>();
      if (m) for (const t of await allTickets(eventId)) map.set(t.id, t);
      tickets.current = map;
      const q = await listQueue(eventId);
      const me = user?.id ?? m?.userId;
      setS((cur) => ({
        ...cur,
        ...p,
        manifest: m,
        ready: !!m,
        ...recount(),
        queue: q.filter((e) => e.staff === me),
        foreignQueue: q.filter((e) => e.staff !== me && e.state === "pending").length,
        clockSkewMs: m?.clockSkewMs ?? cur.clockSkewMs,
      }));
    } catch (e) {
      patch({ supported: false, error: errText(e, "Penyimpanan offline gagal dibuka.") + " Mode offline dimatikan; scan online tetap bisa." });
    }
  }, [eventId, user, patch, recount]);

  useEffect(() => {
    if (!sessionReady) return;
    let cancelled = false;
    void (async () => {
      if (!cancelled) await load();
    })();
    return () => {
      cancelled = true;
    };
  }, [sessionReady, load]);

  /** Unduh manifest (penuh, atau inkremental bila sudah ada). */
  const download = useCallback(
    async (full: boolean) => {
      if (!user) return;
      const cur = full ? null : await getManifest(eventId).catch(() => null);
      const sentAt = Date.now();
      const res = await apiFetch<ManifestResponse>(`${base}/scan-manifest${cur ? `?since=${encodeURIComponent(cur.version)}` : ""}`);
      if (!(await ed25519Supported(res.public_key))) throw new OfflineStoreError("Peramban ini belum mendukung verifikasi tanda tangan Ed25519. Perbarui peramban untuk mode offline.");
      const skew = Math.round((sentAt + Date.now()) / 2 - Date.parse(res.generated_at));
      const m: StoredManifest = {
        eventId,
        orgSlug: org,
        eventName: res.event.name,
        userId: user.id,
        staffName: user.name,
        publicKey: res.public_key,
        generatedAt: res.generated_at,
        expiresAt: res.expires_at,
        version: res.version,
        downloadedAt: new Date().toISOString(),
        clockSkewMs: skew,
        attendanceOpensAt: res.attendance_window?.opens_at,
        attendanceClosesAt: res.attendance_window?.closes_at,
      };
      await saveManifest(m, res.tickets, !cur);
      lastRefresh.current = Date.now();
      await load();
    },
    [user, eventId, base, org, load],
  );

  const prepare = useCallback(async () => {
    patch({ busy: true, error: null, expiredNotice: false, switchedNotice: false });
    try {
      await download(true);
    } catch (e) {
      patch({ error: errText(e, "Gagal mengunduh data offline. Periksa koneksi lalu coba lagi.") });
    } finally {
      patch({ busy: false });
    }
  }, [download, patch]);

  const refresh = useCallback(async () => {
    patch({ busy: true, error: null });
    try {
      await download(false);
    } catch (e) {
      patch({ error: errText(e, "Gagal memperbarui data offline.") });
    } finally {
      patch({ busy: false });
    }
  }, [download, patch]);

  /** Kirim antrean ke server. Kegagalan jaringan tidak menghapus antrean. */
  const sync = useCallback(async () => {
    if (syncing.current || !user) return;
    const pending = (await listQueue(eventId).catch(() => [])).filter((e) => e.state === "pending" && e.staff === user.id);
    if (pending.length === 0) return;
    syncing.current = true;
    patch({ syncing: true, syncError: null });
    const device = await deviceId().catch(() => null);
    try {
      for (let i = 0; i < pending.length; i += BATCH) {
        const chunk = pending.slice(i, i + BATCH);
        const byId = new Map(chunk.map((e) => [e.client_op_id, e]));
        let res: BatchResponse;
        const sentAt = Date.now();
        try {
          res = await apiFetch<BatchResponse>(`${base}/scan/batch`, {
            method: "POST",
            body: { device_id: device ?? chunk[0].device_id, entries: chunk.map((e) => ({ client_op_id: e.client_op_id, token: e.token, ticket_id: e.ticket_id, scanned_at: e.scanned_at, picked_up_by: e.delegated_to, kind: e.kind ?? "collect" })) },
          });
        } catch (e) {
          // Ditolak permanen (peran dicabut, event tidak ada, data tidak sah): berhenti dicoba, tampilkan.
          if (e instanceof ApiError && [401, 403, 404, 413, 422].includes(e.status)) {
            await updateQueue(chunk.map((q) => ({ ...q, state: "rejected", result: e.code, message: e.message })));
            continue;
          }
          throw e;
        }
        const skew = Math.round((sentAt + Date.now()) / 2 - Date.parse(res.server_time));
        const done: string[] = [];
        const changed: QueueEntry[] = [];
        for (const r of res.results) {
          const q = byId.get(r.client_op_id);
          if (!q) continue;
          const kind = q.kind ?? "collect";
          if (r.result === "ok") {
            done.push(q.client_op_id);
          } else if (kind === "attend" && r.result === "already_attended") {
            // Dua perangkat mengabsen peserta yang sama: yang diproses server lebih dulu menang; sisanya "Perlu ditinjau".
            changed.push({ ...q, state: "conflict", result: r.result, message: r.message, server_attended_at: r.ticket?.attended_at, server_attended_by: r.ticket?.attended_by_staff });
            const t = tickets.current.get(q.ticket_id);
            if (t) {
              const fixed = { ...t, attended_at: r.ticket?.attended_at ?? t.attended_at, attended_by: r.ticket?.attended_by_staff };
              tickets.current.set(t.id, fixed);
              await putTicket(eventId, fixed);
            }
          } else if (kind === "collect" && r.result === "already_collected") {
            changed.push({ ...q, state: "conflict", result: r.result, message: r.message, server_collected_at: r.ticket?.collected_at, server_collected_by: r.ticket?.collected_by_staff });
            const t = tickets.current.get(q.ticket_id);
            if (t) {
              const fixed = { ...t, status: "collected" as const, collected_at: r.ticket?.collected_at, collected_by: r.ticket?.collected_by_staff, picked_up_by: r.ticket?.picked_up_by };
              tickets.current.set(t.id, fixed);
              await putTicket(eventId, fixed);
            }
          } else {
            changed.push({ ...q, state: "rejected", result: r.result, message: r.message });
            const t = tickets.current.get(q.ticket_id);
            if (t) {
              let fixed = t;
              if (r.result === "refunded" || r.result === "cancelled") fixed = { ...fixed, status: r.result as ManifestTicket["status"] };
              // Absen ditolak server: jangan biarkan perangkat ini menampilkan peserta sebagai hadir.
              if (kind === "attend") fixed = { ...fixed, attended_at: undefined, attended_by: undefined };
              if (fixed !== t) {
                tickets.current.set(t.id, fixed);
                await putTicket(eventId, fixed);
              }
            }
          }
        }
        await deleteQueue(done);
        await updateQueue(changed);
        patch({ clockSkewMs: skew });
      }
      backoff.current = 0;
      patch({ lastSyncAt: Date.now() });
      // Ambil perubahan dari perangkat lain (maks. sekali per menit).
      if (Date.now() - lastRefresh.current > 60_000) await download(false).catch(() => {});
    } catch (e) {
      backoff.current = Math.min(backoff.current ? backoff.current * 2 : SYNC_EVERY_MS, MAX_BACKOFF_MS);
      nextSyncAt.current = Date.now() + backoff.current;
      patch({ syncError: errText(e, "Belum terkirim: koneksi bermasalah. Dicoba lagi otomatis.") });
    } finally {
      syncing.current = false;
      patch({ syncing: false });
      await load();
    }
  }, [user, eventId, base, patch, download, load]);

  // Sinkron otomatis: saat kembali online, dan tiap 30 detik selama ada antrean (dengan backoff).
  const pendingCount = s.queue.filter((e) => e.state === "pending").length;
  useEffect(() => {
    if (!online || !user || pendingCount === 0) return;
    const run = () => {
      if (Date.now() >= nextSyncAt.current) void sync();
    };
    const first = setTimeout(run, 300);
    const t = setInterval(run, SYNC_EVERY_MS);
    return () => {
      clearTimeout(first);
      clearInterval(t);
    };
  }, [online, user, pendingCount, sync]);

  /** Verifikasi lokal; ok → masuk antrean & langsung dianggap diambil di perangkat ini. null = tidak siap offline. */
  const scanLocal = useCallback(
    async (req: { token?: string; ticket_id?: string; picked_up_by?: string }, mode: QueueKind = "collect"): Promise<LocalResult | null> => {
      const m = s.manifest;
      if (!m || !owner) return null;
      const t0 = performance.now();
      const done = (r: Omit<LocalResult, "local" | "ms">): LocalResult => ({ ...r, local: true, ms: Math.round(performance.now() - t0) });
      if (Date.parse(m.expiresAt) <= Date.now()) {
        await load(); // menghapus manifest kedaluwarsa
        return done({ result: "error", message: "Data offline sudah kedaluwarsa dan dihapus. Sambungkan internet lalu unduh ulang." });
      }
      let ticketId = req.ticket_id ?? "";
      if (req.token) {
        const qr = await verifyQR(m.publicKey, req.token);
        if (!qr.valid) return done({ result: "invalid", message: "QR tidak dikenali atau palsu" });
        if (qr.eventId !== eventId.toLowerCase()) return done({ result: "wrong_event", message: "Tiket ini untuk event lain" });
        ticketId = qr.ticketId;
      }
      const t = tickets.current.get(ticketId);
      if (!t) {
        if (req.token)
          return done({ result: "not_in_manifest", message: "Tidak ada di daftar offline. Tiket mungkin baru terbit setelah data diunduh: sambungkan internet, atau cari manual nama/BIB." });
        return done({ result: "not_found", message: "Tiket tidak terdaftar di event ini" });
      }
      const ticket = {
        id: t.id,
        participant_name: t.name,
        category_code: t.category,
        bib_number: t.bib,
        jersey_size: t.jersey,
        status: t.status,
        collected_at: t.collected_at,
        collected_by_staff: t.collected_by,
        picked_up_by: t.picked_up_by,
        attended_at: t.attended_at,
        attended_by_staff: t.attended_by,
      };
      if (mode === "attend") {
        // Absen hari-H: tidak menyentuh status/collected. Jendela waktu dinilai dengan jam server perkiraan.
        const serverNow = Date.now() - m.clockSkewMs;
        const opens = m.attendanceOpensAt ? Date.parse(m.attendanceOpensAt) : null;
        const closes = m.attendanceClosesAt ? Date.parse(m.attendanceClosesAt) : null;
        if ((opens !== null && serverNow < opens) || (closes !== null && serverNow >= closes))
          return done({ result: "attend_not_open", message: `Absen hari-H ${opens !== null && serverNow < opens ? "belum dibuka" : "sudah ditutup"}. Jendela absen: ${m.attendanceOpensAt ? formatDateTime(m.attendanceOpensAt) : "-"} sampai ${m.attendanceClosesAt ? formatDateTime(m.attendanceClosesAt) : "-"}.` });
        if (t.status !== "issued" && t.status !== "collected") return done({ result: t.status, message: "Tiket berstatus " + t.status + ", tidak bisa absen", ticket });
        if (t.attended_at) return done({ result: "already_attended", message: "Peserta sudah diabsen sebelumnya", ticket });
        const at = new Date().toISOString();
        const attended: ManifestTicket = { ...t, attended_at: at, attended_by: `${owner.name} (perangkat ini)` };
        const entry: QueueEntry = {
          kind: "attend",
          client_op_id: crypto.randomUUID(),
          eventId,
          orgSlug: org,
          ticket_id: t.id,
          token: req.token,
          scanned_at: at,
          device_id: await deviceId(),
          staff: owner.id,
          state: "pending",
          name: t.name,
          bib: t.bib,
        };
        try {
          await enqueue(entry, attended);
        } catch (e) {
          return done({ result: "error", message: errText(e, "Gagal menyimpan ke antrean perangkat.") + " Absen BELUM tercatat." });
        }
        tickets.current.set(t.id, attended);
        setS((cur) => ({ ...cur, queue: [...cur.queue, entry], ...recount() }));
        if (navigator.onLine) nextSyncAt.current = 0;
        return done({ result: "ok", message: "Hadir tercatat", ticket: { ...ticket, attended_at: at, attended_by_staff: attended.attended_by } });
      }
      if (t.status === "collected") return done({ result: "already_collected", message: "Race pack sudah diambil sebelumnya", ticket });
      if (t.status !== "issued") return done({ result: t.status, message: "Tiket berstatus " + t.status, ticket });
      const now = new Date().toISOString();
      const delegated = req.picked_up_by?.trim() || undefined;
      const collected: ManifestTicket = { ...t, status: "collected", collected_at: now, collected_by: `${owner.name} (perangkat ini)`, picked_up_by: delegated };
      const entry: QueueEntry = {
        client_op_id: crypto.randomUUID(),
        eventId,
        orgSlug: org,
        ticket_id: t.id,
        token: req.token,
        scanned_at: now,
        device_id: await deviceId(),
        staff: owner.id,
        delegated_to: delegated,
        state: "pending",
        name: t.name,
        bib: t.bib,
      };
      try {
        await enqueue(entry, collected);
      } catch (e) {
        return done({ result: "error", message: errText(e, "Gagal menyimpan ke antrean perangkat.") + " Race pack JANGAN diserahkan dulu." });
      }
      tickets.current.set(t.id, collected);
      setS((cur) => ({ ...cur, queue: [...cur.queue, entry], ...recount() }));
      if (navigator.onLine) nextSyncAt.current = 0; // kirim di latar belakang
      return done({ result: "ok", message: "Valid — serahkan race pack", ticket: { ...ticket, status: "collected" } });
    },
    [s.manifest, owner, eventId, org, load, recount],
  );

  /** Cari manual offline: nama (sebagian) atau BIB. */
  const search = useCallback((q: string) => {
    const needle = q.trim().toLowerCase();
    if (!needle) return [];
    const out: ManifestTicket[] = [];
    for (const t of tickets.current.values()) {
      if (t.bib.toLowerCase() === needle || t.name.toLowerCase().includes(needle)) out.push(t);
      if (out.length >= 20) break;
    }
    return out;
  }, []);

  const clearData = useCallback(async () => {
    try {
      await deleteManifests(eventId);
      tickets.current = new Map();
    } catch (e) {
      patch({ error: errText(e, "Gagal menghapus data offline.") });
    }
    await load();
  }, [eventId, patch, load]);

  /** Buang entri antrean (setelah ditinjau, atau antrean yang memang tidak akan dikirim). */
  const discard = useCallback(
    async (ids: string[]) => {
      await deleteQueue(ids).catch(() => {});
      await load();
    },
    [load],
  );

  const discardForeign = useCallback(async () => {
    const q = await listQueue(eventId).catch(() => []);
    await discard(q.filter((e) => e.staff !== ownerId && e.state === "pending").map((e) => e.client_op_id));
  }, [eventId, ownerId, discard]);

  return { state: s, online, owner, prepare, refresh, sync, scanLocal, search, clearData, discard, discardForeign };
}
