"use client";

import Link from "next/link";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { useEffect, useMemo, useState } from "react";
import {
  ApiError,
  canEdit,
  createCategory,
  createEvent,
  deleteCategory,
  deleteEvent,
  eventAction,
  roleIn,
  updateCategory,
  updateEvent,
  type CategoryInput,
  type EventInput,
  type EventSummary,
  type OrgProfile,
  type PartnerEvent,
  type QueueSummary,
} from "@/lib/api";
import { formatRupiah, formatWIB, fromWIBInput, todayWIB, toWIBInput } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { EventBib } from "./bib";
import { ImageUpload } from "./image-upload";
import { EventStatusBadge } from "./org-dashboard";
import { useSession } from "./session";
import { btnDark, btnGhost, btnPrimary, ErrorBox, Field, inputErr, Loading, LoginHint, PageTitle } from "./ui";
import { ConfirmDialog } from "./ui-client";

// ===== Halaman =====

export function NewEventPage() {
  const { org } = useParams<{ org: string }>();
  const { ready, me } = useSession();
  const editor = canEdit(roleIn(me, org));
  const { data: profile, error } = useApi<OrgProfile>(me && editor ? `/v1/org/${org}/profile` : null, [me?.id]);
  if (!ready) return <Loading />;
  if (!me) return <LoginHint />;
  if (!editor) return <ErrorBox message="Hanya owner atau admin penyelenggara ini yang bisa membuat event." />;
  if (error) return <ErrorBox message={error} />;
  if (!profile) return <Loading />;
  return <EventForm org={org} orgInfo={{ name: profile.name, kyc_status: profile.kyc_status, theme_color: profile.theme_color }} event={null} isOwner={profile.role === "owner"} />;
}

export function EditEventPage() {
  const { org, eventId } = useParams<{ org: string; eventId: string }>();
  const justCreated = useSearchParams().get("baru") === "1";
  const { ready, me } = useSession();
  const role = roleIn(me, org);
  const { data, error } = useApi<PartnerEvent>(me && canEdit(role) ? `/v1/org/${org}/events/${eventId}` : null, [me?.id]);
  // Versi terbaru dari server setelah simpan/terbit; form dipasang ulang dengan isian baru.
  const [saved, setSaved] = useState<PartnerEvent | null>(null);
  const event = saved ?? data;
  if (!ready) return <Loading />;
  if (!me) return <LoginHint />;
  if (!canEdit(role)) return <ErrorBox message="Hanya owner atau admin penyelenggara ini yang bisa mengubah event." />;
  if (error && !event) return <ErrorBox message={error} />;
  if (!event) return <Loading />;
  // Dipasang ulang hanya saat status berubah (terbit/tutup); simpan biasa memperbarui isian di tempat.
  return <EventForm key={`${event.id}:${event.status}`} org={org} orgInfo={event.org} event={event} isOwner={role === "owner"} onSaved={setSaved} justCreated={justCreated} />;
}

// ===== State form =====

type Row = {
  key: string;
  id?: string;
  code: string;
  name: string;
  distance_km: string;
  price: string;
  quota: string;
  max_per_user: string;
  bib_prefix: string;
  locked: boolean;
  has_orders: boolean;
  sold: number;
  pending: number;
};

type Info = Omit<EventInput, "sale_open_at" | "sale_close_at"> & { sale_open: string; sale_close: string }; // datetime-local WIB

let rowSeq = 0;
const newKey = () => `r${++rowSeq}`;

function emptyRow(): Row {
  return { key: newKey(), code: "", name: "", distance_km: "", price: "", quota: "", max_per_user: "1", bib_prefix: "", locked: false, has_orders: false, sold: 0, pending: 0 };
}

function rowsFrom(e: PartnerEvent): Row[] {
  return e.categories.map((c) => ({
    key: newKey(),
    id: c.id,
    code: c.code,
    name: c.name,
    distance_km: String(c.distance_km),
    price: String(c.price),
    quota: String(c.quota),
    max_per_user: String(c.max_per_user),
    bib_prefix: c.bib_prefix,
    locked: c.locked,
    has_orders: c.has_orders,
    sold: c.sold,
    pending: c.pending,
  }));
}

function infoFrom(e: PartnerEvent | null): Info {
  if (!e) {
    return {
      name: "",
      tagline: "",
      description: "",
      venue: "",
      city: "",
      race_date: "",
      start_time: "05:00",
      sale_open: "",
      sale_close: "",
      racepack_info: "",
      theme_color: "",
      cover_url: "",
      waiting_room: false,
    };
  }
  return {
    name: e.name,
    tagline: e.tagline,
    description: e.description,
    venue: e.venue,
    city: e.city,
    race_date: e.race_date,
    start_time: e.start_time,
    sale_open: toWIBInput(e.sale_open_at),
    sale_close: toWIBInput(e.sale_close_at),
    racepack_info: e.racepack_info,
    theme_color: e.theme_color ?? "",
    cover_url: e.cover_url ?? "",
    waiting_room: e.waiting_room,
  };
}

const num = (s: string) => (s.trim() === "" ? NaN : Number(s.replace(",", ".")));

function toCategory(r: Row): CategoryInput {
  return {
    code: r.code.trim().toUpperCase(),
    name: r.name.trim(),
    distance_km: num(r.distance_km) || 0,
    price: Math.round(num(r.price.replace(/\./g, ""))) || 0,
    quota: Math.round(num(r.quota)) || 0,
    max_per_user: Math.round(num(r.max_per_user)) || 0,
    bib_prefix: r.bib_prefix.trim(),
  };
}

function toEventInput(i: Info): EventInput {
  const { sale_open, sale_close, ...rest } = i;
  return { ...rest, sale_open_at: fromWIBInput(sale_open), sale_close_at: fromWIBInput(sale_close) };
}

/** Cek sebelum terbit (server tetap memeriksa ulang). */
function publishChecks(info: Info, rows: Row[], kyc: OrgProfile["kyc_status"], now: number) {
  const open = Date.parse(fromWIBInput(info.sale_open));
  const close = Date.parse(fromWIBInput(info.sale_close));
  const raceEnd = info.race_date ? Date.parse(`${info.race_date}T00:00:00+07:00`) + 86_400_000 : NaN;
  const datesOk = !!info.race_date && info.race_date > todayWIB(now) && open < close && close <= raceEnd && close > now;
  return [
    { ok: kyc === "verified", label: "Penyelenggara terverifikasi", fail: "Menunggu verifikasi tim RaceGo. Draft tetap tersimpan dan bisa diterbitkan setelah terverifikasi." },
    { ok: rows.length > 0, label: "Minimal satu kategori tiket", fail: "Tambahkan kategori tiket dulu." },
    { ok: datesOk, label: "Tanggal valid", fail: "Tanggal race setelah hari ini, penjualan dibuka sebelum ditutup, dan ditutup paling lambat akhir hari race (belum lewat)." },
  ];
}

// ===== Form =====

function EventForm({
  org,
  orgInfo,
  event,
  isOwner,
  onSaved,
  justCreated = false,
}: {
  org: string;
  orgInfo: PartnerEvent["org"];
  event: PartnerEvent | null;
  isOwner: boolean;
  onSaved?: (e: PartnerEvent) => void;
  justCreated?: boolean;
}) {
  const router = useRouter();
  const { refresh } = useSession();
  const initial = useMemo(() => ({ info: infoFrom(event), rows: event ? rowsFrom(event) : [emptyRow()] }), [event]);
  const [info, setInfo] = useState<Info>(initial.info);
  const [rows, setRows] = useState<Row[]>(initial.rows);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState<{ error?: string; ok?: string }>(() => (justCreated ? { ok: "Draft dibuat. Lengkapi lalu terbitkan." } : {}));
  const [busy, setBusy] = useState(false);
  const [dialog, setDialog] = useState<null | "publish" | "close" | "delete">(null);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [now] = useState(() => Date.now());

  const status = event?.status ?? "draft";
  const dirty = JSON.stringify({ info, rows: rows.map(toCategory) }) !== JSON.stringify({ info: initial.info, rows: initial.rows.map(toCategory) });
  const saleStarted = status !== "draft" && event !== null && Date.parse(event.sale_open_at) <= now;
  const canAddCategory = status === "draft" || (status === "published" && !saleStarted);
  const checks = publishChecks(info, rows, orgInfo.kyc_status, now);

  const openDialog = (d: "publish" | "close" | "delete") => {
    setDialogError(null);
    setDialog(d);
  };
  const set = (k: keyof Info) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => setInfo((s) => ({ ...s, [k]: e.target.value }));
  const setRow = (key: string, k: keyof Row, v: string) => setRows((rs) => rs.map((r) => (r.key === key ? { ...r, [k]: v } : r)));

  /** Simpan semua perubahan. true bila semuanya tersimpan. */
  async function save(): Promise<boolean> {
    setBusy(true);
    setMsg({});
    setErrors({});
    try {
      if (!event) {
        const created = await createEvent(org, { ...toEventInput(info), categories: rows.map(toCategory) });
        router.replace(`/org/${org}/events/${created.id}/edit?baru=1`);
        return true;
      }
      const errs: Record<string, string> = {};
      let latest = await updateEvent(org, event.id, toEventInput(info));
      // Kategori disimpan satu per satu; error dicatat per baris dan baris lain tetap diproses.
      const ids = new Set(rows.map((r) => r.id).filter(Boolean));
      for (const c of event.categories.filter((c) => !ids.has(c.id))) {
        try {
          latest = await deleteCategory(org, event.id, c.id);
        } catch (err) {
          errs.categories = `${c.code}: ${err instanceof ApiError ? err.message : "gagal dihapus"}`;
        }
      }
      const next = [...rows];
      for (const [i, r] of rows.entries()) {
        const before = event.categories.find((c) => c.id === r.id);
        const body = toCategory(r);
        if (before && JSON.stringify(toCategory(rowsFrom({ ...event, categories: [before] })[0])) === JSON.stringify(body)) continue;
        try {
          latest = r.id ? await updateCategory(org, event.id, r.id, body) : await createCategory(org, event.id, body);
          if (!r.id) next[i] = { ...r, id: latest.categories.find((c) => c.code === body.code)?.id };
        } catch (err) {
          if (err instanceof ApiError && err.fields) for (const [f, m] of Object.entries(err.fields)) errs[`${r.key}.${f}`] = m;
          else errs[`${r.key}.row`] = err instanceof ApiError ? err.message : "Gagal menyimpan kategori";
        }
      }
      if (Object.keys(errs).length > 0) {
        setRows(next); // baris baru yang berhasil dapat id, isian lain tetap
        setErrors(errs);
        setMsg({ error: "Sebagian kategori belum tersimpan. Periksa baris yang ditandai." });
        return false;
      }
      setInfo(infoFrom(latest));
      setRows(rowsFrom(latest)); // id, status kunci, dan angka terjual terbaru dari server
      onSaved?.(latest);
      return true;
    } catch (err) {
      if (err instanceof ApiError && err.fields) {
        const errs: Record<string, string> = {};
        for (const [f, m] of Object.entries(err.fields)) {
          const cat = /^categories\.(\d+)\.(.+)$/.exec(f);
          errs[cat ? `${rows[Number(cat[1])]?.key}.${cat[2]}` : f === "sale_open_at" ? "sale_open" : f === "sale_close_at" ? "sale_close" : f] = m;
        }
        setErrors(errs);
        setMsg({ error: "Periksa kembali isian yang ditandai." });
      } else {
        setMsg({ error: err instanceof ApiError ? err.message : "Gagal menyimpan" });
      }
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function act(action: "publish" | "unpublish" | "close" | "delete") {
    if (!event) return;
    setBusy(true);
    setDialogError(null);
    try {
      if (action === "delete") {
        await deleteEvent(org, event.id);
        router.replace(`/org/${org}`);
        return;
      }
      const updated = await eventAction(org, event.id, action);
      setDialog(null);
      onSaved?.(updated);
      void refresh();
    } catch (err) {
      const fields = err instanceof ApiError && err.fields ? Object.values(err.fields).join(" ") : "";
      setDialogError(err instanceof ApiError ? `${err.message} ${fields}`.trim() : "Gagal memproses");
      if (!dialog) setMsg({ error: err instanceof ApiError ? err.message : "Gagal memproses" });
    } finally {
      setBusy(false);
    }
  }

  const preview: EventSummary = {
    id: event?.id ?? "preview",
    slug: event?.slug ?? "",
    name: info.name || "Nama event",
    tagline: info.tagline,
    city: info.city || "Kota",
    venue: info.venue,
    race_date: info.race_date ? `${info.race_date}T00:00:00+07:00` : new Date(now + 30 * 86_400_000).toISOString(),
    start_time: info.start_time,
    sale_open_at: fromWIBInput(info.sale_open) || new Date(now).toISOString(),
    sale_close_at: fromWIBInput(info.sale_close) || new Date(now + 86_400_000).toISOString(),
    status: info.sale_open && Date.parse(fromWIBInput(info.sale_open)) > now ? "upcoming" : "open",
    theme_color: info.theme_color || orgInfo.theme_color,
    cover_url: info.cover_url || null,
    org_name: orgInfo.name,
    org_slug: org,
    min_price: rows.length ? Math.min(...rows.map((r) => toCategory(r).price)) : 0,
    distances: rows.map((r) => r.code.toUpperCase()).filter(Boolean),
  };

  return (
    <div>
      <PageTitle title={event ? event.name : "Buat event"} back={{ href: `/org/${org}`, label: `Dashboard ${orgInfo.name}` }}>
        <div className="mt-3 flex flex-wrap items-center gap-3 text-sm text-muted">
          <EventStatusBadge status={status} />
          {status === "draft" && <span>Belum tampil untuk publik.</span>}
          {status === "published" && <span>Tampil di kalender dan bisa dibeli sesuai jadwal penjualan.</span>}
          {status === "closed" && <span>Penjualan ditutup. Event tetap tampil di kalender dan arsip.</span>}
          {status !== "draft" && event && (
            <Link href={`/events/${event.id}`} className="font-semibold text-asphalt underline decoration-hivis decoration-2 underline-offset-4">
              Lihat halaman publik
            </Link>
          )}
        </div>
      </PageTitle>

      <form
        noValidate
        onSubmit={async (e) => {
          e.preventDefault();
          if (await save()) setMsg({ ok: "Tersimpan." });
        }}
        className="space-y-10 pb-28"
      >
        <Section title="Informasi">
          <div className="grid gap-8 lg:grid-cols-[1fr_320px]">
            <div className="grid gap-5 sm:grid-cols-2">
              <div className="sm:col-span-2">
                <Field label="Nama event" required error={errors.name}>
                  <input className={inputErr(errors.name)} maxLength={100} value={info.name} onChange={set("name")} />
                </Field>
              </div>
              <div className="sm:col-span-2">
                <Field label="Tagline" hint="Satu kalimat pendek di kartu event, maks 120 karakter" error={errors.tagline}>
                  <input className={inputErr(errors.tagline)} maxLength={120} value={info.tagline} onChange={set("tagline")} />
                </Field>
              </div>
              <div className="sm:col-span-2">
                <Field label="Deskripsi" hint="Rute, fasilitas, medali, dan hal penting lain. Maks 5.000 karakter." error={errors.description}>
                  <textarea className={`${inputErr(errors.description)} min-h-32`} maxLength={5000} value={info.description} onChange={set("description")} />
                </Field>
              </div>
              <Field label="Kota" required error={errors.city}>
                <input className={inputErr(errors.city)} maxLength={60} value={info.city} onChange={set("city")} />
              </Field>
              <Field label="Lokasi start" hint="mis. Bundaran HI" error={errors.venue}>
                <input className={inputErr(errors.venue)} maxLength={100} value={info.venue} onChange={set("venue")} />
              </Field>
              <Field label="Tanggal race" required error={errors.race_date}>
                <input className={inputErr(errors.race_date)} type="date" value={info.race_date} onChange={set("race_date")} />
              </Field>
              <Field label="Jam flag-off (WIB)" required error={errors.start_time}>
                <input className={inputErr(errors.start_time)} type="time" value={info.start_time} onChange={set("start_time")} />
              </Field>
              <div className="sm:col-span-2">
                <Field label="Info race pack" hint="Tempat & jam pengambilan, syarat diwakilkan. Maks 2.000 karakter." error={errors.racepack_info}>
                  <textarea className={`${inputErr(errors.racepack_info)} min-h-24`} maxLength={2000} value={info.racepack_info} onChange={set("racepack_info")} />
                </Field>
              </div>
              <Field label="Warna tema" hint={info.theme_color ? "Warna khusus event ini" : "Mengikuti warna penyelenggara"} error={errors.theme_color}>
                <span className="flex flex-wrap items-center gap-3">
                  <input
                    type="color"
                    className="h-10 w-14 cursor-pointer rounded-md border border-line bg-card p-1"
                    value={info.theme_color || orgInfo.theme_color}
                    onChange={set("theme_color")}
                    aria-label="Pilih warna tema"
                  />
                  {info.theme_color && (
                    <button type="button" className={btnGhost} onClick={() => setInfo((s) => ({ ...s, theme_color: "" }))}>
                      Pakai warna penyelenggara
                    </button>
                  )}
                </span>
              </Field>
            </div>
            <div className="lg:sticky lg:top-24 lg:self-start">
              <p className="mb-2 text-sm font-semibold">Pratinjau kartu</p>
              <div className="max-w-sm">
                <EventBib e={preview} preview />
              </div>
            </div>
          </div>
        </Section>

        <Section title="Poster">
          <ImageUpload
            org={org}
            kind="cover"
            label="Poster event"
            hint="JPEG atau PNG, maks 3 MB, sisi terpanjang maks 4.000 px. Tampil di halaman event dan kartu kalender."
            value={info.cover_url}
            onChange={(url) => setInfo((s) => ({ ...s, cover_url: url }))}
            disabled={busy}
            error={errors.cover_url}
            boxClass="aspect-[4/5] w-36"
          />
        </Section>

        <Section title="Penjualan" intro="Semua waktu dalam WIB (Asia/Jakarta).">
          <div className="grid gap-5 sm:grid-cols-2">
            <Field label="Penjualan dibuka" required hint={formatWIB(fromWIBInput(info.sale_open)) ? `Dibuka ${formatWIB(fromWIBInput(info.sale_open))}` : undefined} error={errors.sale_open}>
              <input className={inputErr(errors.sale_open)} type="datetime-local" value={info.sale_open} onChange={set("sale_open")} />
            </Field>
            <Field
              label="Penjualan ditutup"
              required
              hint={formatWIB(fromWIBInput(info.sale_close)) ? `Ditutup ${formatWIB(fromWIBInput(info.sale_close))}` : "Paling lambat akhir hari race"}
              error={errors.sale_close}
            >
              <input className={inputErr(errors.sale_close)} type="datetime-local" value={info.sale_close} onChange={set("sale_close")} />
            </Field>
          </div>
          <div className="mt-6 rounded-md bg-card p-4 ring-1 ring-line">
            <label className={`flex items-start gap-3 ${saleStarted ? "cursor-not-allowed" : "cursor-pointer"}`}>
              <input
                type="checkbox"
                className="mt-1 size-4 accent-hivis"
                checked={info.waiting_room}
                disabled={saleStarted}
                onChange={(e) => setInfo((s) => ({ ...s, waiting_room: e.target.checked }))}
                aria-describedby="waiting-room-hint"
              />
              <span>
                <span className="block text-sm font-semibold">Aktifkan antrian virtual</span>
                <span id="waiting-room-hint" className="mt-1 block text-xs text-muted">
                  Disarankan untuk event yang peminatnya jauh melebihi kuota. Pembeli masuk lobi sebelum penjualan dibuka, urutan lobi diundi saat jam buka,
                  lalu dipanggil bertahap sehingga server tidak kewalahan. {saleStarted ? <b className="text-asphalt">Terkunci karena penjualan sudah dibuka.</b> : "Tidak bisa diubah setelah penjualan dibuka."}
                </span>
                {errors.waiting_room && (
                  <span className="mt-1 block text-xs font-medium text-red-700" role="alert">
                    {errors.waiting_room}
                  </span>
                )}
              </span>
            </label>
          </div>
          {event && event.waiting_room && status !== "draft" && <QueuePanel org={org} eventId={event.id} />}
        </Section>

        <Section
          title="Kategori tiket"
          intro="Nomor BIB = prefix + 4 digit urut (mis. prefix 21 → 210001). Kode, harga, jarak, dan prefix terkunci setelah ada penjualan; kuota tetap bisa diubah."
        >
          {errors.categories && <ErrorBox message={errors.categories} />}
          <div className="mt-3 hidden gap-3 px-4 text-xs font-semibold text-muted lg:grid lg:grid-cols-[5.5rem_1fr_5.5rem_8rem_6rem_5.5rem_5.5rem_2.5rem]" aria-hidden>
            <span>Kode *</span>
            <span>Nama *</span>
            <span>Jarak (km) *</span>
            <span>Harga (Rp) *</span>
            <span>Kuota *</span>
            <span>Maks/akun *</span>
            <span>Prefix BIB *</span>
            <span />
          </div>
          <ul className="mt-2 space-y-3">
            {rows.map((r, i) => {
              const e = (f: string) => errors[`${r.key}.${f}`];
              return (
                <li key={r.key} className="rounded-md bg-card p-4 ring-1 ring-line">
                  <fieldset className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-[5.5rem_1fr_5.5rem_8rem_6rem_5.5rem_5.5rem_2.5rem] lg:items-start">
                    <legend className="sr-only">Kategori {i + 1}</legend>
                    <Field label="Kode" required labelClass="lg:sr-only" error={e("code")}>
                      <input className={`${inputErr(e("code"))} uppercase`} maxLength={8} placeholder="10K" value={r.code} disabled={r.locked} onChange={(ev) => setRow(r.key, "code", ev.target.value)} />
                    </Field>
                    <div className="col-span-1 sm:col-span-3 lg:col-span-1">
                      <Field label="Nama" required labelClass="lg:sr-only" error={e("name")}>
                        <input className={inputErr(e("name"))} maxLength={60} placeholder="Half Marathon" value={r.name} onChange={(ev) => setRow(r.key, "name", ev.target.value)} />
                      </Field>
                    </div>
                    <Field label="Jarak (km)" required labelClass="lg:sr-only" error={e("distance_km")}>
                      <input className={inputErr(e("distance_km"))} inputMode="decimal" placeholder="21.1" value={r.distance_km} disabled={r.locked} onChange={(ev) => setRow(r.key, "distance_km", ev.target.value)} />
                    </Field>
                    <Field label="Harga (Rp)" required labelClass="lg:sr-only" error={e("price")}>
                      <input className={inputErr(e("price"))} inputMode="numeric" placeholder="250000" value={r.price} disabled={r.locked} onChange={(ev) => setRow(r.key, "price", ev.target.value)} />
                    </Field>
                    <Field label="Kuota" required labelClass="lg:sr-only" error={e("quota")}>
                      <input className={inputErr(e("quota"))} inputMode="numeric" placeholder="500" value={r.quota} onChange={(ev) => setRow(r.key, "quota", ev.target.value)} />
                    </Field>
                    <Field label="Maks/akun" required labelClass="lg:sr-only" error={e("max_per_user")}>
                      <input className={inputErr(e("max_per_user"))} inputMode="numeric" value={r.max_per_user} onChange={(ev) => setRow(r.key, "max_per_user", ev.target.value)} />
                    </Field>
                    <Field label="Prefix BIB" required labelClass="lg:sr-only" error={e("bib_prefix")}>
                      <input className={inputErr(e("bib_prefix"))} inputMode="numeric" maxLength={3} placeholder="21" value={r.bib_prefix} disabled={r.locked} onChange={(ev) => setRow(r.key, "bib_prefix", ev.target.value)} />
                    </Field>
                    <div className="col-span-2 flex items-end justify-end sm:col-span-1 lg:col-span-1 lg:items-start">
                      <button
                        type="button"
                        className="inline-flex h-10 min-w-10 items-center justify-center rounded-md border border-line px-3 text-sm whitespace-nowrap hover:border-red-600 hover:text-red-700 disabled:cursor-not-allowed disabled:opacity-30 lg:px-0"
                        disabled={r.has_orders}
                        onClick={() => setRows((rs) => rs.filter((x) => x.key !== r.key))}
                        aria-label={`Hapus kategori ${r.code || i + 1}`}
                        title={r.has_orders ? "Kategori dengan order tidak bisa dihapus" : "Hapus kategori"}
                      >
                        <span aria-hidden>✕</span>
                        <span className="ml-1 lg:hidden">Hapus</span>
                      </button>
                    </div>
                  </fieldset>
                  {(r.locked || r.sold > 0 || r.pending > 0) && (
                    <p className="mt-2 text-xs text-muted">
                      {r.locked && <span className="font-semibold text-asphalt">🔒 Terkunci karena sudah ada penjualan. </span>}
                      Terjual {r.sold}
                      {r.pending > 0 && `, menunggu bayar ${r.pending}`}. Harga {formatRupiah(toCategory(r).price)}.
                    </p>
                  )}
                  {e("row") && <p className="mt-2 text-xs font-medium text-red-700">{e("row")}</p>}
                </li>
              );
            })}
          </ul>
          {canAddCategory ? (
            <button type="button" className={`${btnGhost} mt-3`} onClick={() => setRows((rs) => [...rs, emptyRow()])}>
              + Tambah kategori
            </button>
          ) : (
            <p className="mt-3 text-xs text-muted">Kategori baru hanya bisa ditambah sebelum penjualan dibuka.</p>
          )}
        </Section>

        {/* Bilah aksi menempel di bawah layar supaya selalu terjangkau saat form panjang. */}
        <div className="fixed inset-x-0 bottom-0 z-30 border-t border-line bg-paper/95 backdrop-blur">
          <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-2 px-4 py-3 sm:px-6">
            <button type="submit" className={btnDark} disabled={busy || (!!event && !dirty)}>
              {busy ? "Menyimpan…" : status === "draft" ? "Simpan draft" : "Simpan perubahan"}
            </button>
            {status === "draft" && (
              <button
                type="button"
                className={btnPrimary.replace("px-5 py-2.5", "px-4 py-2 text-sm")}
                disabled={busy}
                onClick={async () => {
                  if (!event || dirty) {
                    if (!(await save())) return;
                    if (!event) return; // event baru: diarahkan ke halaman edit, terbitkan dari sana
                  }
                  openDialog("publish");
                }}
              >
                Terbitkan
              </button>
            )}
            {status === "published" && !event?.has_orders && (
              <button type="button" className={btnGhost} disabled={busy} onClick={() => act("unpublish")}>
                Kembalikan ke draft
              </button>
            )}
            {status === "published" && (
              <button type="button" className={btnGhost} disabled={busy} onClick={() => openDialog("close")}>
                Tutup penjualan
              </button>
            )}
            {status === "draft" && event && isOwner && !event.has_orders && (
              <button type="button" className={`${btnGhost} text-red-700`} disabled={busy} onClick={() => openDialog("delete")}>
                Hapus draft
              </button>
            )}
            <span className="min-w-0 flex-1 text-sm" aria-live="polite">
              {msg.error ? <span className="font-medium text-red-700">{msg.error}</span> : msg.ok && !dirty ? <span className="text-go">{msg.ok}</span> : dirty && event ? <span className="text-muted">Ada perubahan yang belum disimpan.</span> : null}
            </span>
          </div>
        </div>
      </form>

      <ConfirmDialog
        open={dialog === "publish"}
        title="Terbitkan event?"
        confirmLabel="Terbitkan sekarang"
        busy={busy}
        disabled={checks.some((c) => !c.ok)}
        onClose={() => setDialog(null)}
        onConfirm={() => act("publish")}
      >
        <ul className="space-y-2">
          {checks.map((c) => (
            <li key={c.label} className="flex gap-2">
              <span aria-hidden className={c.ok ? "text-go" : "text-red-700"}>
                {c.ok ? "✓" : "✕"}
              </span>
              <span>
                <b>{c.label}</b>
                {!c.ok && <span className="block text-muted">{c.fail}</span>}
              </span>
            </li>
          ))}
        </ul>
        <p className="text-muted">Setelah terbit, event tampil di beranda, kalender, dan halaman penyelenggara. Penjualan dibuka {formatWIB(fromWIBInput(info.sale_open))}.</p>
        {dialogError && <ErrorBox message={dialogError} />}
      </ConfirmDialog>

      <ConfirmDialog open={dialog === "close"} title="Tutup penjualan?" confirmLabel="Tutup penjualan" danger busy={busy} onClose={() => setDialog(null)} onConfirm={() => act("close")}>
        <p>Pembelian baru langsung berhenti dan tidak bisa dibuka lagi. Tiket yang sudah terjual tetap berlaku dan race pack tetap bisa di-scan.</p>
        <p className="text-muted">Event yang sudah punya penjualan tidak bisa dihapus atau dibatalkan dari sini karena fitur refund belum tersedia.</p>
        {dialogError && <ErrorBox message={dialogError} />}
      </ConfirmDialog>

      <ConfirmDialog open={dialog === "delete"} title="Hapus draft?" confirmLabel="Hapus permanen" danger busy={busy} onClose={() => setDialog(null)} onConfirm={() => act("delete")}>
        <p>
          Draft <b>{event?.name}</b> beserta kategorinya dihapus permanen.
        </p>
        {dialogError && <ErrorBox message={dialogError} />}
      </ConfirmDialog>
    </div>
  );
}

/** Ringkasan antrian langsung (owner/admin/keuangan), diperbarui tiap 5 detik. */
function QueuePanel({ org, eventId }: { org: string; eventId: string }) {
  const { data, error, reload } = useApi<QueueSummary>(`/v1/org/${org}/events/${eventId}/queue`);
  useEffect(() => {
    const t = setInterval(reload, 5000);
    return () => clearInterval(t);
  }, [reload]);
  const phase: Record<QueueSummary["phase"], string> = {
    before_lobby: "Lobi belum dibuka",
    lobby: "Lobi dibuka, menunggu jam buka",
    selling: "Penjualan berjalan, pembeli dipanggil bertahap",
    closed: "Penjualan selesai",
  };
  return (
    <div className="mt-4 rounded-md bg-asphalt p-4 text-white">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <p className="font-semibold">Antrian langsung</p>
        <p className="text-xs text-white/70">
          {data ? (data.final === "sold_out" ? "Kuota habis, antrian ditutup" : phase[data.phase]) : error ? error : "Memuat…"}
        </p>
      </div>
      {data && (
        <dl className="mt-3 grid grid-cols-3 gap-3 text-center">
          {(
            [
              ["Di antrian", data.queue_size],
              [`Pass aktif (maks ${data.max_active})`, data.active],
              ["Terpanggil total", data.admitted_total],
            ] as const
          ).map(([label, value]) => (
            <div key={label}>
              <dd className="font-display text-4xl leading-none font-black tabular-nums">{value.toLocaleString("id-ID")}</dd>
              <dt className="mt-1 text-xs text-white/70">{label}</dt>
            </div>
          ))}
        </dl>
      )}
      {data && <p className="mt-3 text-xs text-white/60">Lobi dibuka {formatWIB(data.lobby_opens_at)}. Dipanggil {data.admit_per_sec} orang per detik.</p>}
    </div>
  );
}

function Section({ title, intro, children }: { title: string; intro?: string; children: React.ReactNode }) {
  return (
    <section className="border-t border-line pt-6">
      <h2 className="font-display text-3xl leading-none font-extrabold">{title}</h2>
      {intro && <p className="mt-2 max-w-2xl text-sm text-muted">{intro}</p>}
      <div className="mt-5">{children}</div>
    </section>
  );
}
