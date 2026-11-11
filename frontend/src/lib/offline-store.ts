// Penyimpanan scanner offline di IndexedDB (tanpa dependensi). Semua fungsi aman dipanggil di mana
// pun: IndexedDB yang tidak ada/diblokir/penuh menghasilkan OfflineStoreError, tidak pernah crash.
// Data peserta (nama + BIB) di sini TIDAK dienkripsi aplikasi; lihat docs/05-tiket-qr-dan-scan.md.

export type ManifestTicket = {
  id: string;
  bib: string;
  name: string;
  category: string;
  jersey: string;
  status: "issued" | "collected" | "refunded" | "cancelled";
  collected_at?: string;
  collected_by?: string;
  picked_up_by?: string;
  attended_at?: string; // absen hari-H
  attended_by?: string;
};

export type StoredManifest = {
  eventId: string;
  orgSlug: string;
  eventName: string;
  userId: string; // pemilik data di perangkat ini
  staffName: string;
  publicKey: string;
  generatedAt: string;
  expiresAt: string;
  version: string;
  downloadedAt: string;
  clockSkewMs: number; // jam perangkat − jam server saat diunduh
  // Jendela absen hari-H dari server (perangkat offline menolak absen di luar waktu). Manifest lama: kosong = tidak dicek lokal.
  attendanceOpensAt?: string;
  attendanceClosesAt?: string;
};

export type QueueState = "pending" | "synced" | "conflict" | "rejected";

export type QueueKind = "collect" | "attend";

export type QueueEntry = {
  /** collect (ambil race pack) atau attend (absen hari-H). Entri lama tanpa kind = collect. */
  kind?: QueueKind;
  client_op_id: string;
  eventId: string;
  orgSlug: string;
  ticket_id: string;
  token?: string;
  scanned_at: string; // jam perangkat
  device_id: string;
  staff: string; // user id
  delegated_to?: string;
  state: QueueState;
  // Hasil dari server (conflict/rejected): siapa/kapan lebih dulu, alasan.
  result?: string;
  message?: string;
  server_collected_at?: string;
  server_collected_by?: string;
  server_attended_at?: string;
  server_attended_by?: string;
  name: string;
  bib: string;
};

export class OfflineStoreError extends Error {}

const DB_NAME = "racego-scanner";
const DB_VERSION = 1;
let dbPromise: Promise<IDBDatabase> | null = null;

export function offlineSupported(): boolean {
  try {
    return typeof window !== "undefined" && typeof window.indexedDB !== "undefined" && window.indexedDB !== null;
  } catch {
    return false;
  }
}

function open(): Promise<IDBDatabase> {
  if (!offlineSupported()) return Promise.reject(new OfflineStoreError("Penyimpanan offline (IndexedDB) tidak tersedia di peramban ini."));
  dbPromise ??= new Promise<IDBDatabase>((resolve, reject) => {
    let req: IDBOpenDBRequest;
    try {
      req = window.indexedDB.open(DB_NAME, DB_VERSION);
    } catch {
      reject(new OfflineStoreError("Penyimpanan offline diblokir peramban (mode privat atau izin situs)."));
      return;
    }
    req.onupgradeneeded = () => {
      const db = req.result;
      db.createObjectStore("manifests", { keyPath: "eventId" });
      db.createObjectStore("tickets", { keyPath: ["eventId", "id"] }).createIndex("event", "eventId");
      db.createObjectStore("queue", { keyPath: "client_op_id" }).createIndex("event", "eventId");
      db.createObjectStore("meta");
    };
    req.onsuccess = () => {
      req.result.onversionchange = () => req.result.close();
      resolve(req.result);
    };
    req.onerror = () => reject(new OfflineStoreError("Penyimpanan offline gagal dibuka: " + (req.error?.message ?? "tidak diketahui")));
    req.onblocked = () => reject(new OfflineStoreError("Penyimpanan offline sedang dipakai tab lain. Tutup tab RaceGo lain lalu coba lagi."));
  }).catch((e) => {
    dbPromise = null;
    throw e instanceof OfflineStoreError ? e : new OfflineStoreError(String(e));
  });
  return dbPromise;
}

/** Jalankan fn dalam satu transaksi; selesai saat transaksi commit. */
async function tx<T>(stores: string[], mode: IDBTransactionMode, fn: (t: IDBTransaction) => T | Promise<T>): Promise<T> {
  const db = await open();
  return new Promise<T>((resolve, reject) => {
    let t: IDBTransaction;
    try {
      t = db.transaction(stores, mode);
    } catch (e) {
      reject(new OfflineStoreError("Penyimpanan offline gagal: " + String(e)));
      return;
    }
    let out: T;
    Promise.resolve(fn(t)).then(
      (v) => (out = v),
      (e) => {
        try {
          t.abort();
        } catch {}
        reject(e);
      },
    );
    t.oncomplete = () => resolve(out);
    t.onerror = () => reject(new OfflineStoreError(t.error?.name === "QuotaExceededError" ? "Penyimpanan perangkat penuh. Hapus data situs lain lalu coba lagi." : "Penyimpanan offline gagal: " + (t.error?.message ?? "")));
    t.onabort = () => reject(new OfflineStoreError("Penyimpanan offline dibatalkan: " + (t.error?.message ?? "")));
  });
}

function req<T>(r: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => reject(r.error);
  });
}

// ===== Perangkat =====

export async function deviceId(): Promise<string> {
  return tx(["meta"], "readwrite", async (t) => {
    const s = t.objectStore("meta");
    const cur = (await req(s.get("device_id"))) as string | undefined;
    if (cur) return cur;
    const id = crypto.randomUUID();
    s.put(id, "device_id");
    return id;
  });
}

// ===== Manifest =====

export async function getManifest(eventId: string): Promise<StoredManifest | null> {
  return tx(["manifests"], "readonly", async (t) => ((await req(t.objectStore("manifests").get(eventId))) as StoredManifest | undefined) ?? null);
}

/** Simpan manifest + tiket. full=true menghapus tiket lama event ini dulu (unduhan penuh). */
export async function saveManifest(m: StoredManifest, tickets: ManifestTicket[], full: boolean): Promise<void> {
  // Tiket yang sudah ditandai lokal tapi belum terkirim tetap "collected" walau server belum tahu.
  // Absen hari-H yang belum terkirim juga dipertahankan (digabung ke tiket dari server).
  const pending = (await listQueue(m.eventId)).filter((q) => q.state === "pending");
  const queuedCollect = new Set(pending.filter((q) => (q.kind ?? "collect") === "collect").map((q) => q.ticket_id));
  const queuedAttend = new Map(pending.filter((q) => q.kind === "attend").map((q) => [q.ticket_id, q]));
  await tx(["manifests", "tickets"], "readwrite", async (t) => {
    const ts = t.objectStore("tickets");
    if (full) ts.delete(IDBKeyRange.bound([m.eventId, ""], [m.eventId, "\uffff"]));
    for (const tk of tickets) {
      if (queuedCollect.has(tk.id) && tk.status === "issued") continue;
      const a = queuedAttend.get(tk.id);
      const merged = a && !tk.attended_at && (tk.status === "issued" || tk.status === "collected") ? { ...tk, attended_at: a.scanned_at, attended_by: "perangkat ini" } : tk;
      ts.put({ ...merged, eventId: m.eventId });
    }
    t.objectStore("manifests").put(m);
  });
}

export async function getTicket(eventId: string, id: string): Promise<ManifestTicket | null> {
  return tx(["tickets"], "readonly", async (t) => ((await req(t.objectStore("tickets").get([eventId, id]))) as ManifestTicket | undefined) ?? null);
}

export async function allTickets(eventId: string): Promise<ManifestTicket[]> {
  return tx(["tickets"], "readonly", async (t) => (await req(t.objectStore("tickets").index("event").getAll(eventId))) as ManifestTicket[]);
}

export async function putTicket(eventId: string, tk: ManifestTicket): Promise<void> {
  await tx(["tickets"], "readwrite", (t) => void t.objectStore("tickets").put({ ...tk, eventId }));
}

/** Hapus manifest + tiket (satu event, atau semua). Antrean TIDAK ikut terhapus. */
export async function deleteManifests(eventId?: string): Promise<void> {
  await tx(["manifests", "tickets"], "readwrite", (t) => {
    if (eventId) {
      t.objectStore("manifests").delete(eventId);
      t.objectStore("tickets").delete(IDBKeyRange.bound([eventId, ""], [eventId, "\uffff"]));
    } else {
      t.objectStore("manifests").clear();
      t.objectStore("tickets").clear();
    }
  });
}

// ===== Antrean =====

export async function enqueue(e: QueueEntry, ticket: ManifestTicket): Promise<void> {
  await tx(["queue", "tickets"], "readwrite", (t) => {
    t.objectStore("queue").put(e);
    t.objectStore("tickets").put({ ...ticket, eventId: e.eventId });
  });
}

export async function listQueue(eventId: string): Promise<QueueEntry[]> {
  return tx(["queue"], "readonly", async (t) => {
    const list = (await req(t.objectStore("queue").index("event").getAll(eventId))) as QueueEntry[];
    return list.sort((a, b) => a.scanned_at.localeCompare(b.scanned_at));
  });
}

export async function updateQueue(entries: QueueEntry[]): Promise<void> {
  await tx(["queue"], "readwrite", (t) => {
    for (const e of entries) t.objectStore("queue").put(e);
  });
}

export async function deleteQueue(ids: string[]): Promise<void> {
  await tx(["queue"], "readwrite", (t) => {
    for (const id of ids) t.objectStore("queue").delete(id);
  });
}

/** Logout: data peserta dihapus. Antrean belum terkirim dipertahankan (bisa dibuang manual di scanner). */
export async function clearOnLogout(): Promise<void> {
  if (!offlineSupported()) return;
  try {
    await deleteManifests();
  } catch {
    /* penyimpanan tidak bisa dibuka: tidak ada yang perlu dihapus */
  }
}
