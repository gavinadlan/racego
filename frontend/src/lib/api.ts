// Klien API backend RaceGo. Semua panggilan lewat apiFetch agar header & error seragam.

export const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

const USER_KEY = "racego.devUser";

// SEMENTARA (development): identitas dikirim lewat header X-User-ID dari pemilih user dev.
// Ganti dengan token login sungguhan sebelum aplikasi dibuka ke publik.
export function getDevUserId(): string | null {
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
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
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
    });
  } catch {
    throw new ApiError(0, "network", "Tidak bisa terhubung ke server. Pastikan backend berjalan (make api).");
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new ApiError(res.status, data.error ?? "unknown", data.message ?? `Error ${res.status}`);
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
