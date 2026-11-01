// Verifikasi QR tiket di perangkat (scanner offline). Format: v1.<ticket_id>.<event_id>.<signature Ed25519 base64url>.
// Kripto memakai WebCrypto bawaan peramban (Ed25519: Chrome/Edge 137+, Safari 17+, Firefox 129+).
// Tidak ada fallback pustaka: peramban tanpa Ed25519 tidak bisa memakai mode offline (pesan jelas di UI).

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function b64ToBytes(s: string, url: boolean): Uint8Array<ArrayBuffer> | null {
  try {
    let b = url ? s.replace(/-/g, "+").replace(/_/g, "/") : s;
    while (b.length % 4) b += "=";
    const bin = atob(b);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  } catch {
    return null;
  }
}

const keys = new Map<string, Promise<CryptoKey>>();

function importKey(publicKeyB64: string): Promise<CryptoKey> {
  let k = keys.get(publicKeyB64);
  if (!k) {
    const raw = b64ToBytes(publicKeyB64, false);
    k = raw ? crypto.subtle.importKey("raw", raw, { name: "Ed25519" }, false, ["verify"]) : Promise.reject(new Error("kunci publik rusak"));
    keys.set(publicKeyB64, k);
  }
  return k;
}

/** true bila peramban bisa memverifikasi Ed25519 (dicek dengan mengimpor kunci). */
export async function ed25519Supported(publicKeyB64: string): Promise<boolean> {
  try {
    if (typeof crypto === "undefined" || !crypto.subtle) return false;
    await importKey(publicKeyB64);
    return true;
  } catch {
    keys.delete(publicKeyB64);
    return false;
  }
}

export type QRCheck = { valid: false } | { valid: true; ticketId: string; eventId: string };

/** Tanda tangan sah → ID tiket & event. Tidak sah/rusak → valid:false (tanpa pengecualian). */
export async function verifyQR(publicKeyB64: string, token: string): Promise<QRCheck> {
  const parts = token.trim().split(".");
  if (parts.length !== 4 || parts[0] !== "v1" || !UUID.test(parts[1]) || !UUID.test(parts[2])) return { valid: false };
  const sig = b64ToBytes(parts[3], true);
  if (!sig || sig.length !== 64) return { valid: false };
  try {
    const key = await importKey(publicKeyB64);
    const msg = new TextEncoder().encode(parts.slice(0, 3).join("."));
    const ok = await crypto.subtle.verify({ name: "Ed25519" }, key, sig, msg);
    return ok ? { valid: true, ticketId: parts[1].toLowerCase(), eventId: parts[2].toLowerCase() } : { valid: false };
  } catch {
    return { valid: false };
  }
}
