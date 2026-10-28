const rupiah = new Intl.NumberFormat("id-ID", { style: "currency", currency: "IDR", maximumFractionDigits: 0 });

export const formatRupiah = (n: number) => rupiah.format(n);

export const formatDate = (iso: string) =>
  new Date(iso).toLocaleDateString("id-ID", { weekday: "long", day: "numeric", month: "long", year: "numeric" });

export const formatDateShort = (iso: string) =>
  new Date(iso).toLocaleDateString("id-ID", { day: "numeric", month: "short", year: "numeric" });

export const formatDateTime = (iso: string) =>
  new Date(iso).toLocaleString("id-ID", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });

export const dayOfMonth = (iso: string) => new Date(iso).getDate();
export const monthShort = (iso: string) => new Date(iso).toLocaleDateString("id-ID", { month: "short" }).replace(".", "");
export const monthLong = (d: Date) => d.toLocaleDateString("id-ID", { month: "long", year: "numeric" });

/** Kunci bulan "YYYY-MM" dalam zona waktu lokal. */
export const monthKey = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;

/** Selisih hari kalender antara hari ini dan tanggal race (H-n). */
export function daysUntil(iso: string, now = Date.now()) {
  const d = new Date(iso);
  const target = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const t = new Date(now);
  const today = new Date(t.getFullYear(), t.getMonth(), t.getDate()).getTime();
  return Math.round((target - today) / 86_400_000);
}

/** Waktu flag-off = tanggal race + jam start (waktu lokal pengguna). */
export function flagOffTime(raceDate: string, startTime: string) {
  const d = new Date(raceDate);
  const [h, m] = startTime.split(":").map(Number);
  return new Date(d.getFullYear(), d.getMonth(), d.getDate(), h || 0, m || 0).getTime();
}

export function splitDuration(ms: number) {
  const s = Math.max(0, Math.floor(ms / 1000));
  return { days: Math.floor(s / 86400), hours: Math.floor((s % 86400) / 3600), minutes: Math.floor((s % 3600) / 60), seconds: s % 60 };
}

export function formatCountdown(ms: number) {
  if (ms <= 0) return "00:00";
  const s = Math.floor(ms / 1000);
  return `${String(Math.floor(s / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
}

export function humanizeUntil(ms: number) {
  const { days, hours, minutes } = splitDuration(ms);
  if (days > 0) return `${days} hari ${hours} jam`;
  if (hours > 0) return `${hours} jam ${minutes} menit`;
  return `${Math.max(1, minutes)} menit`;
}

/** Warna teks yang terbaca di atas warna tema (putih atau aspal). */
export function textOn(hex: string) {
  const v = hex.replace("#", "");
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(v.slice(i, i + 2), 16) / 255).map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  const lum = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  return lum > 0.22 ? "#23272e" : "#ffffff";
}

export function initials(name: string) {
  return name
    .split(/\s+/)
    .filter((w) => /^[A-Za-z]/.test(w))
    .slice(0, 2)
    .map((w) => w[0].toUpperCase())
    .join("");
}

// Akun contoh dari backend/migrations/seed.sql
export const DEV_USERS = [
  { id: "11111111-1111-1111-1111-111111111111", label: "Budi (pelari)" },
  { id: "22222222-2222-2222-2222-222222222222", label: "Sari (owner Jakarta Runners)" },
  { id: "33333333-3333-3333-3333-333333333333", label: "Andi (staf RPC Jakarta)" },
  { id: "44444444-4444-4444-4444-444444444444", label: "Rina (owner Bandung Trail)" },
] as const;
