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
    public fields?: Record<string, string>, // 422 validation: pesan per kolom form
  ) {
    super(message);
  }
}

export async function apiFetch<T>(
  path: string,
  init: { method?: string; body?: unknown; headers?: Record<string, string>; form?: FormData } = {},
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
      body: init.form ?? (init.body !== undefined ? JSON.stringify(init.body) : undefined),
      cache: "no-store",
      credentials: "include",
    });
  } catch {
    throw new ApiError(0, "network", "Tidak bisa terhubung ke server. Pastikan backend berjalan (make api).");
  }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new ApiError(res.status, data.error ?? "unknown", data.message ?? `Error ${res.status}`, data.retry_after, data.fields);
  }
  return data as T;
}

/** URL gambar unggahan (path relatif "/uploads/…" dari backend). */
export const assetUrl = (url: string) => `${API_URL}${url}`;

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

export type OrgBrief = { slug: string; name: string; city: string; description: string; theme_color: string; logo_url: string | null };

export type EventDetail = EventSummary & {
  description: string;
  racepack_info: string;
  org: OrgBrief;
  categories: Category[];
  waiting_room: boolean;
  lobby_opens_at?: string; // hanya bila waiting_room
};

export type Organizer = OrgBrief & {
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
  qty: number;
  tickets_issued: number; // tiket terbit beberapa detik setelah lunas (worker)
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
  order_id: string;
};

export type Me = {
  id: string;
  name: string;
  email: string;
  phone: string | null;
  orgs: { slug: string; name: string; role: string }[];
  is_super_admin: boolean; // super admin platform: menu Admin & halaman /admin
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
  waiting_room: boolean;
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

// ===== Portal mitra =====

export type Role = "owner" | "admin" | "finance" | "rpc_staff";

export const ROLE_LABEL: Record<Role, string> = {
  owner: "Owner",
  admin: "Admin",
  finance: "Keuangan",
  rpc_staff: "Staf race pack",
};

/** Peran user di org tertentu (dari me.orgs), atau null bila bukan anggota. */
export const roleIn = (me: Me | null, slug: string) => (me?.orgs.find((o) => o.slug === slug)?.role as Role | undefined) ?? null;
export const canEdit = (role: Role | null) => role === "owner" || role === "admin";

export type OrgInput = { name: string; slug?: string; city: string; description: string; instagram: string; website: string };

export type OrgProfile = {
  slug: string;
  name: string;
  city: string;
  description: string;
  instagram: string | null;
  website: string | null;
  theme_color: string;
  logo_url: string | null;
  kyc_status: "pending" | "verified" | "rejected";
  role: Role;
  verification_status: VerificationStatus | null; // pengajuan terbaru (null = belum pernah)
  reject_reason: string | null;
};

export type CategoryInput = {
  code: string;
  name: string;
  distance_km: number;
  price: number;
  quota: number;
  max_per_user: number;
  bib_prefix: string;
};

export type PartnerCategory = CategoryInput & {
  id: string;
  sold: number;
  pending: number;
  locked: boolean;
  has_orders: boolean;
  remaining: number | null;
};

export type EventInput = {
  name: string;
  tagline: string;
  description: string;
  venue: string;
  city: string;
  race_date: string; // YYYY-MM-DD
  start_time: string; // HH:MM
  sale_open_at: string; // ISO dengan offset
  sale_close_at: string;
  racepack_info: string;
  theme_color: string;
  cover_url: string;
  waiting_room: boolean;
};

export type PartnerEvent = Omit<EventInput, "theme_color" | "cover_url" | "waiting_room"> & {
  id: string;
  slug: string;
  status: "draft" | "published" | "closed";
  theme_color: string | null;
  cover_url: string | null;
  has_orders: boolean;
  waiting_room: boolean;
  categories: PartnerCategory[];
  org: { name: string; kyc_status: OrgProfile["kyc_status"]; theme_color: string };
};

export type Member = { user_id: string; name: string; email: string; role: Role; joined_at: string };

export const createOrg = (body: OrgInput) =>
  apiFetch<{ slug: string; name: string; kyc_status: OrgProfile["kyc_status"] }>("/v1/orgs", { method: "POST", body });

export const checkOrgSlug = (slug: string) =>
  apiFetch<{ available: boolean; message?: string }>(`/v1/orgs/slug-check${qs({ slug })}`);

export const updateOrgProfile = (org: string, body: Omit<OrgInput, "slug"> & { theme_color: string; logo_url: string }) =>
  apiFetch<OrgProfile>(`/v1/org/${org}/profile`, { method: "PATCH", body });

export function uploadImage(org: string, kind: "cover" | "logo", file: File) {
  const form = new FormData();
  form.append("file", file);
  return apiFetch<{ url: string }>(`/v1/org/${org}/uploads${qs({ kind })}`, { method: "POST", form });
}

const ev = (org: string, id?: string, rest = "") => `/v1/org/${org}/events${id ? `/${id}` : ""}${rest}`;

export const createEvent = (org: string, body: EventInput & { categories: CategoryInput[] }) =>
  apiFetch<PartnerEvent>(ev(org), { method: "POST", body });
export const updateEvent = (org: string, id: string, body: EventInput) => apiFetch<PartnerEvent>(ev(org, id), { method: "PATCH", body });
export const eventAction = (org: string, id: string, action: "publish" | "unpublish" | "close") =>
  apiFetch<PartnerEvent>(ev(org, id, `/${action}`), { method: "POST" });
export const deleteEvent = (org: string, id: string) => apiFetch<void>(ev(org, id), { method: "DELETE" });
export const createCategory = (org: string, id: string, body: CategoryInput) =>
  apiFetch<PartnerEvent>(ev(org, id, "/categories"), { method: "POST", body });
export const updateCategory = (org: string, id: string, catId: string, body: CategoryInput) =>
  apiFetch<PartnerEvent>(ev(org, id, `/categories/${catId}`), { method: "PATCH", body });
export const deleteCategory = (org: string, id: string, catId: string) =>
  apiFetch<PartnerEvent>(ev(org, id, `/categories/${catId}`), { method: "DELETE" });

export const addMember = (org: string, body: { email: string; role: Role }) =>
  apiFetch<{ members: Member[] }>(`/v1/org/${org}/members`, { method: "POST", body });
export const updateMember = (org: string, userId: string, role: Role) =>
  apiFetch<{ members: Member[] }>(`/v1/org/${org}/members/${userId}`, { method: "PATCH", body: { role } });
export const removeMember = (org: string, userId: string) =>
  apiFetch<{ members: Member[] }>(`/v1/org/${org}/members/${userId}`, { method: "DELETE" });

// ===== Antrian virtual (waiting room) =====

export type QueueState = {
  state: "lobby" | "waiting" | "admitted" | "sold_out" | "closed" | "not_in_queue";
  position: number | null; // null di lobi: urutan baru dibekukan saat jam buka
  ahead: number | null;
  queue_size: number;
  eta_seconds: number | null; // null = laju panggil belum terukur
  pass?: string;
  pass_expires_at?: string;
  poll_after_ms: number; // 0 = tidak perlu polling lagi
  server_time: string;
  lobby_opens_at: string;
  sale_open_at: string;
};

const queuePath = (eventId: string) => `/v1/events/${eventId}/queue`;
export const joinQueue = (eventId: string) => apiFetch<QueueState>(queuePath(eventId), { method: "POST" });
export const queueStatus = (eventId: string) => apiFetch<QueueState>(`${queuePath(eventId)}/me`);
export const leaveQueue = (eventId: string) => apiFetch<void>(`${queuePath(eventId)}/me`, { method: "DELETE" });

export type QueueSummary = {
  waiting_room: boolean;
  phase: "before_lobby" | "lobby" | "selling" | "closed";
  lobby_opens_at: string;
  sale_open_at: string;
  queue_size: number;
  active: number;
  admitted_total: number;
  final?: "sold_out" | "closed" | "";
  max_active: number;
  admit_per_sec: number;
  server_time: string;
};

// ===== Verifikasi mitra (KYC) =====

export type VerificationStatus = "draft" | "submitted" | "approved" | "rejected";
export type OrgType = "perorangan" | "komunitas" | "badan_usaha";
export type DocKind = "ktp_pic" | "npwp" | "surat_keterangan" | "akta_pendirian" | "sk_kemenkumham" | "nib" | "surat_kuasa";

/** Matriks jenis & dokumen dari backend (satu sumber, tidak diduplikasi di frontend). */
export type KycRequirements = {
  types: { type: OrgType; label: string; description: string; required: DocKind[]; optional: DocKind[]; holder_rule: string }[];
  documents: Record<DocKind, string>;
  banks: { code: string; name: string }[];
  consent: { version: string; text: string };
  max_bytes: number;
};

export type KycDoc = { kind: DocKind; label: string; name: string; content_type: string; size: number; created_at: string };

/** Pengajuan; pic_id, npwp, account selalu tersamar ("••••1234") atau "". */
export type Verification = {
  id: string;
  attempt: number;
  status: VerificationStatus;
  org_type: OrgType | "";
  legal_name: string;
  address: string;
  pic_name: string;
  pic_phone: string;
  pic_id: string;
  npwp: string;
  bank_code: string;
  bank_name: string;
  account: string;
  account_holder: string;
  consent_at: string | null;
  submitted_at: string | null;
  reviewed_at: string | null;
  reject_reason: string | null;
  created_at: string;
  documents: KycDoc[] | null;
};

export type VerificationHistory = { id: string; attempt: number; status: VerificationStatus; submitted_at: string | null; reviewed_at: string | null; reject_reason: string | null };

export type VerificationState = {
  kyc_status: OrgProfile["kyc_status"];
  auto_verify: boolean;
  verification: Verification | null;
  history: VerificationHistory[];
  requirements: KycRequirements;
};

/** pic_id/npwp/account_number: undefined = tidak diubah (nilai lama tidak pernah dikirim ke browser). */
export type VerificationInput = {
  org_type: OrgType | "";
  legal_name: string;
  address: string;
  pic_name: string;
  pic_phone: string;
  pic_id?: string;
  npwp?: string;
  bank_code: string;
  bank_name: string;
  account_number?: string;
  account_holder: string;
};

const verif = (org: string, rest = "") => `/v1/org/${org}/verification${rest}`;
export const saveVerification = (org: string, body: VerificationInput) => apiFetch<VerificationState>(verif(org), { method: "PUT", body });
export const deleteKycDocument = (org: string, kind: DocKind) => apiFetch<VerificationState>(verif(org, `/documents/${kind}`), { method: "DELETE" });
export const submitVerification = (org: string, consent: boolean) =>
  apiFetch<VerificationState>(verif(org, "/submit"), { method: "POST", body: { consent } });
export const reopenVerification = (org: string) => apiFetch<VerificationState>(verif(org, "/reopen"), { method: "POST" });
/** URL berkas (cookie sesi); v = cache-buster saat berkas diganti. */
export const kycDocUrl = (org: string, d: KycDoc) => `${API_URL}${verif(org, `/documents/${d.kind}`)}?v=${encodeURIComponent(d.created_at)}`;

/** Unggah dokumen dengan progres (fetch belum punya progres unggah, jadi XHR). */
export function uploadKycDocument(org: string, kind: DocKind, file: File, onProgress: (pct: number) => void) {
  return new Promise<VerificationState>((resolve, reject) => {
    const form = new FormData();
    form.append("kind", kind);
    form.append("file", file);
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${API_URL}${verif(org, "/documents")}`);
    xhr.withCredentials = true;
    const userId = getDevUserId();
    if (userId) xhr.setRequestHeader("X-User-ID", userId);
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress(Math.round((e.loaded / e.total) * 100));
    xhr.onerror = () => reject(new ApiError(0, "network", "Tidak bisa terhubung ke server. Periksa koneksi lalu coba lagi."));
    xhr.onload = () => {
      let data: Record<string, unknown> = {};
      try {
        data = JSON.parse(xhr.responseText);
      } catch {
        /* respons bukan JSON, mis. dari proxy */
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data as VerificationState);
      else if (xhr.status === 413) reject(new ApiError(413, "file_too_large", "Ukuran berkas maksimal 5 MB."));
      else {
        const fields = data.fields as Record<string, string> | undefined;
        const msg = fields ? Object.values(fields).join(" ") : (data.message as string | undefined);
        reject(new ApiError(xhr.status, (data.error as string) ?? "unknown", msg ?? `Error ${xhr.status}`, data.retry_after as number | undefined, fields));
      }
    };
    xhr.send(form);
  });
}

// ===== Super admin =====

export type AdminListItem = {
  id: string;
  org_name: string;
  org_slug: string;
  org_type: OrgType | null;
  legal_name: string;
  attempt: number;
  status: VerificationStatus;
  submitted_at: string | null;
  reviewed_at: string | null;
};

export type AdminList = {
  items: AdminListItem[];
  counts: Record<"submitted" | "approved" | "rejected", number>;
  total: number;
  page: number;
  page_size: number;
};

export type AdminVerification = {
  verification: Verification;
  documents: (KycDoc & { duplicate_orgs: string[] | null })[];
  org: { slug: string; name: string; city: string; kyc_status: OrgProfile["kyc_status"]; created_at: string };
  members: { name: string; email: string; role: Role }[];
  history: VerificationHistory[];
  name_match: { ok: boolean; holder: string; expected: string[]; rule: string };
  reviewer: string | null;
  conflict_of_interest: boolean;
  requirements: KycRequirements;
};

const adminV = (id: string, rest = "") => `/v1/admin/verifications/${id}${rest}`;
export const revealField = (id: string, field: "pic_id" | "account" | "npwp") =>
  apiFetch<{ field: string; value: string }>(adminV(id, "/reveal"), { method: "POST", body: { field } });
export const approveVerification = (id: string) => apiFetch<AdminVerification>(adminV(id, "/approve"), { method: "POST" });
export const rejectVerification = (id: string, reason: string) =>
  apiFetch<AdminVerification>(adminV(id, "/reject"), { method: "POST", body: { reason } });
export const adminDocUrl = (id: string, kind: DocKind) => `${API_URL}${adminV(id, `/documents/${kind}`)}`;
