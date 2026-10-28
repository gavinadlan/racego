// Klien API backend RaceGo. Semua panggilan lewat apiFetch agar header & error seragam.

export const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

const USER_KEY = "racego.devUser";
export const DEV_USER_EVENT = "racego:user-change";

/** Pemilih akun contoh hanya aktif bila NEXT_PUBLIC_DEV_LOGIN=true (development). */
export const DEV_LOGIN = process.env.NEXT_PUBLIC_DEV_LOGIN === "true";

// Akun contoh (development): identitas dikirim lewat header X-User-ID, hanya diterima backend
// saat APP_ENV=development. Login sungguhan memakai cookie sesi HttpOnly dari /v1/auth/otp/verify.
export function getDevUserId(): string | null {
  if (!DEV_LOGIN) return null;
  try {
    return window.localStorage.getItem(USER_KEY);
  } catch {
    return null;
  }
}

export function setDevUserId(id: string | null) {
  try {
    if (id) window.localStorage.setItem(USER_KEY, id);
    else window.localStorage.removeItem(USER_KEY);
  } catch {
    /* localStorage bisa tidak tersedia (mode privat) */
  }
  window.dispatchEvent(new Event(DEV_USER_EVENT));
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public retryAfter?: number,
  ) {
    super(message);
  }
}

export async function apiFetch<T>(
  path: string,
  init: { method?: string; body?: unknown; headers?: Record<string, string> } = {},
): Promise<T> {
  const headers: Record<string, string> = { ...init.headers };
  const userId = typeof window !== "undefined" ? getDevUserId() : null;
  if (userId) headers["X-User-ID"] = userId;
  if (init.body !== undefined) headers["Content-Type"] = "application/json";

  let res: Response;
  try {
    res = await fetch(`${API_URL}${path}`, {
      method: init.method ?? "GET",
      headers,
      body: init.body !== undefined ? JSON.stringify(init.body) : undefined,
      cache: "no-store",
      credentials: "include",
    });
  } catch {
    throw new ApiError(0, "network", "Tidak bisa terhubung ke server. Pastikan backend berjalan (make api).");
  }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new ApiError(res.status, data.error ?? "unknown", data.message ?? `Error ${res.status}`, data.retry_after);
  }
  return data as T;
}

// ===== Tipe respons =====

export type EventStatus = "open" | "upcoming" | "closed" | "finished";

export type EventSummary = {
  id: string;
  slug: string;
  name: string;
  tagline: string;
  city: string;
  venue: string;
  race_date: string;
  start_time: string;
  sale_open_at: string;
  sale_close_at: string;
  status: EventStatus;
  theme_color: string;
  cover_url: string | null;
  org_name: string;
  org_slug: string;
  min_price: number;
  distances: string[];
};

export type Category = {
  id: string;
  code: string;
  name: string;
  distance_km: number;
  price: number;
  quota: number;
  remaining: number | null;
  max_per_user: number;
};

export type OrgBrief = { slug: string; name: string; city: string; description: string; theme_color: string };

export type EventDetail = EventSummary & {
  description: string;
  racepack_info: string;
  org: OrgBrief;
  categories: Category[];
};

export type Organizer = OrgBrief & {
  logo_url: string | null;
  instagram: string | null;
  website: string | null;
  upcoming_events: number;
  past_events: number;
  runners: number;
};

export type EventFilters = { cities: string[]; distances: string[] };

/** Bangun query string dari objek, abaikan nilai kosong. */
export function qs(params: Record<string, string | number | undefined | null>) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== "") p.set(k, String(v));
  const s = p.toString();
  return s ? `?${s}` : "";
}

export type Reservation = {
  reservation_id: string;
  category_id: string;
  qty: number;
  expires_at: string;
  remaining: number;
};

export type Participant = {
  full_name: string;
  email: string;
  id_number: string;
  birth_date?: string;
  gender?: "M" | "F" | "";
  jersey_size: string;
  emergency_name?: string;
  emergency_phone?: string;
};

export type Order = {
  order_id: string;
  status: "pending" | "paid" | "expired" | "cancelled" | "refunded" | "needs_refund";
  total: number;
  expires_at: string;
  snap_token?: string;
  redirect_url?: string;
  mock: boolean;
};

export type MyTicket = {
  id: string;
  status: "issued" | "collected" | "cancelled" | "refunded";
  bib_number: string;
  qr_token: string;
  participant_name: string;
  jersey_size: string;
  event_id: string;
  event_name: string;
  event_venue: string;
  race_date: string;
  category_code: string;
  category_name: string;
  org_name: string;
};

export type Me = {
  id: string;
  name: string;
  email: string;
  phone: string | null;
  orgs: { slug: string; name: string; role: string }[];
};

export type OrgEvent = {
  id: string;
  name: string;
  status: string;
  race_date: string;
  quota: number;
  sold: number;
  collected: number;
  revenue: number;
};

export type ScanTicket = {
  id: string;
  participant_name: string;
  category_code: string;
  bib_number: string;
  jersey_size: string;
  status: string;
  collected_at?: string;
  collected_by_staff?: string;
  picked_up_by?: string;
};

export type ScanResult = {
  result: "ok" | "already_collected" | "invalid" | "wrong_event" | "not_found" | "refunded" | "cancelled" | "error";
  message: string;
  ticket?: ScanTicket;
};

// ===== Login (email + kode OTP) =====

export const requestOtp = (email: string) =>
  apiFetch<{ ok: true; resend_after: number }>("/v1/auth/otp/request", { method: "POST", body: { email } });

export const verifyOtp = (email: string, code: string) =>
  apiFetch<Me & { is_new: boolean }>("/v1/auth/otp/verify", { method: "POST", body: { email, code } });

export const logout = () => apiFetch<void>("/v1/auth/logout", { method: "POST" });

export const updateMe = (body: { name: string; phone: string }) => apiFetch<Me>("/v1/me", { method: "PATCH", body });

/** Tujuan setelah login: hanya path relatif di situs ini (cegah open redirect). */
export function safeNext(next: string | null | undefined): string {
  const ok = next && next.startsWith("/") && !next.startsWith("//") && !next.startsWith("/\\") && !next.startsWith("/masuk");
  return ok ? next : "/";
}
