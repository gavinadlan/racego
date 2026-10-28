"use client";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useMemo, useState } from "react";
import { apiFetch, ApiError, type Category, type EventDetail, type Order, type Participant, type Reservation } from "@/lib/api";
import { dayOfMonth, flagOffTime, formatCountdown, formatDate, formatDateTime, formatRupiah, textOn } from "@/lib/format";
import { openSnap } from "@/lib/snap";
import { useApi } from "@/lib/use-api";
import { PinHoles, StatusPill } from "./bib";
import { BigCountdown } from "./clock";
import { Monogram } from "./home";
import { useSession } from "./session";
import { btnDark, btnPrimary, ErrorBox, inputCls, Loading, LoginHint } from "./ui";

export function EventDetailView() {
  const { eventId } = useParams<{ eventId: string }>();
  const { data: event, error, loading, reload } = useApi<EventDetail>(`/v1/events/${eventId}`);
  const [reservation, setReservation] = useState<{ res: Reservation; category: Category } | null>(null);

  if (loading && !event) return <Loading />;
  if (error) return <ErrorBox message={error} />;
  if (!event) return null;

  const band = event.status === "finished" ? "#9aa0a6" : event.theme_color;
  const fg = textOn(band);
  const raceDate = new Date(event.race_date);

  return (
    <article>
      <header className="relative overflow-hidden rounded-md p-8 sm:p-12" style={{ background: band, color: fg }}>
        <PinHoles />
        <Link href="/kalender" className="text-sm opacity-80 hover:opacity-100">
          ← Kalender race
        </Link>
        <div className="mt-6 flex flex-wrap items-end justify-between gap-8">
          <div className="max-w-3xl min-w-0">
            <Link href={`/penyelenggara/${event.org.slug}`} className="text-sm font-semibold underline-offset-4 hover:underline">
              {event.org.name}
            </Link>
            <h1 className="font-display mt-2 text-[clamp(48px,7vw,88px)] leading-[0.88] font-black text-balance">{event.name}</h1>
            {event.tagline && <p className="mt-4 text-lg opacity-90">{event.tagline}</p>}
          </div>
          <div className="font-display text-right leading-[0.8] font-black" aria-label={formatDate(event.race_date)}>
            <div className="text-[120px]">{dayOfMonth(event.race_date)}</div>
            <div className="text-3xl font-bold capitalize">{raceDate.toLocaleDateString("id-ID", { month: "long", year: "numeric" })}</div>
          </div>
        </div>
        <dl className="mt-10 flex flex-wrap gap-x-10 gap-y-4 border-t border-dashed border-current/30 pt-6 text-sm">
          <div>
            <dt className="opacity-75">Flag-off</dt>
            <dd className="font-semibold">{formatDate(event.race_date)}, {event.start_time}</dd>
          </div>
          <div>
            <dt className="opacity-75">Lokasi start</dt>
            <dd className="font-semibold">{event.venue}, {event.city}</dd>
          </div>
          <div>
            <dt className="opacity-75">Kategori</dt>
            <dd className="font-display text-lg leading-tight font-bold">{event.distances.join(" / ")}</dd>
          </div>
        </dl>
      </header>

      <div className="mt-12 grid gap-12 lg:grid-cols-[1fr_340px]">
        <div className="space-y-16">
          <section id="kategori" className="scroll-mt-24">
            {reservation ? (
              <CheckoutForm
                event={event}
                category={reservation.category}
                reservation={reservation.res}
                onExpired={() => {
                  setReservation(null);
                  reload();
                }}
              />
            ) : (
              <CategoryPicker event={event} onReserved={(res, category) => setReservation({ res, category })} />
            )}
          </section>

          <section>
            <h2 className="font-display text-4xl leading-none font-extrabold">Tentang race</h2>
            <p className="mt-4 max-w-2xl leading-relaxed">{event.description}</p>
          </section>

          {event.racepack_info && (
            <section>
              <h2 className="font-display text-4xl leading-none font-extrabold">Pengambilan race pack</h2>
              <p className="mt-4 max-w-2xl leading-relaxed">{event.racepack_info}</p>
            </section>
          )}
        </div>

        <aside className="space-y-4 lg:sticky lg:top-24 lg:self-start">
          <StatusCard event={event} />
          <Link href={`/penyelenggara/${event.org.slug}`} className="group block rounded-md bg-card p-6 ring-1 ring-line hover:ring-asphalt">
            <p className="text-sm text-muted">Diselenggarakan oleh</p>
            <div className="mt-3 flex items-center gap-3">
              <Monogram name={event.org.name} color={event.org.theme_color} />
              <div className="min-w-0">
                <p className="truncate font-semibold group-hover:underline group-hover:underline-offset-4">{event.org.name}</p>
                <p className="text-sm text-muted">{event.org.city}</p>
              </div>
            </div>
            <p className="mt-3 line-clamp-3 text-sm text-muted">{event.org.description}</p>
            <p className="mt-4 text-sm font-semibold underline decoration-hivis decoration-2 underline-offset-4">Lihat race lain dari penyelenggara ini</p>
          </Link>
        </aside>
      </div>
    </article>
  );
}

/** Kartu samping: hitung mundur sesuai status (ke pembukaan pendaftaran atau ke flag-off). */
function StatusCard({ event }: { event: EventDetail }) {
  return (
    <div className="rounded-md bg-asphalt p-6 text-white">
      <StatusPill status={event.status} />
      {event.status === "upcoming" && (
        <>
          <p className="mt-5 text-sm text-white/70">Pendaftaran dibuka dalam</p>
          <div className="mt-2">
            <BigCountdown target={new Date(event.sale_open_at).getTime()} />
          </div>
          <p className="mt-4 text-sm text-white/70">Dibuka {formatDateTime(event.sale_open_at)}. Masuk lebih awal, kuota biasanya habis di menit pertama.</p>
        </>
      )}
      {event.status === "open" && (
        <>
          <p className="mt-5 text-sm text-white/70">Flag-off dalam</p>
          <div className="mt-2">
            <BigCountdown target={flagOffTime(event.race_date, event.start_time)} />
          </div>
          <p className="mt-5 text-sm text-white/70">Mulai</p>
          <p className="font-display text-4xl leading-none font-black">{formatRupiah(event.min_price)}</p>
          <a href="#kategori" className={`${btnPrimary} mt-5 w-full`}>
            Pilih kategori
          </a>
          <p className="mt-3 text-xs text-white/60">Pendaftaran ditutup {formatDateTime(event.sale_close_at)}.</p>
        </>
      )}
      {event.status === "closed" && <p className="mt-5 text-white/80">Pendaftaran sudah ditutup. Sampai jumpa di garis start.</p>}
      {event.status === "finished" && <p className="mt-5 text-white/80">Race ini sudah digelar. Cek race berikutnya dari penyelenggara yang sama.</p>}
    </div>
  );
}

function CategoryPicker({ event, onReserved }: { event: EventDetail; onReserved: (r: Reservation, c: Category) => void }) {
  const router = useRouter();
  const { ready, userId } = useSession();
  const [qty, setQty] = useState<Record<string, number>>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const canBuy = event.status === "open";

  async function reserve(c: Category) {
    // Belum masuk: login dulu, slot belum diamankan.
    if (!userId) {
      router.push(`/masuk?next=${encodeURIComponent(`/events/${event.id}`)}`);
      return;
    }
    setBusy(c.id);
    setError(null);
    try {
      const res = await apiFetch<Reservation>(`/v1/categories/${c.id}/reservations`, {
        method: "POST",
        body: { qty: qty[c.id] ?? 1 },
      });
      onReserved(res, c);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Gagal mengamankan slot");
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <h2 className="font-display text-4xl leading-none font-extrabold">{canBuy ? "Pilih kategori" : "Kategori"}</h2>
      {canBuy && !userId && (
        <div className="mt-4">
          <LoginHint />
        </div>
      )}
      {error && (
        <div className="mt-4">
          <ErrorBox message={error} />
        </div>
      )}
      <ul className="mt-6 space-y-4">
        {event.categories.map((c) => {
          const soldOut = c.remaining === 0;
          const left = c.remaining ?? c.quota;
          return (
            <li key={c.id} className="relative overflow-hidden rounded-md bg-card ring-1 ring-line">
              <div className="flex flex-wrap items-center gap-x-6 gap-y-4 p-5 sm:p-6">
                <div className="font-display w-24 text-5xl leading-none font-black">{c.code}</div>
                <div className="min-w-44 flex-1">
                  <p className="font-semibold">{c.name}</p>
                  <p className="text-sm text-muted">
                    {c.distance_km.toLocaleString("id-ID")} km, maks. {c.max_per_user} slot per akun
                  </p>
                  {event.status !== "finished" && (
                    <div className="mt-2 flex items-center gap-2 text-xs whitespace-nowrap text-muted">
                      <span className="h-1.5 w-28 overflow-hidden rounded-full bg-line" aria-hidden>
                        <span className="block h-full bg-go" style={{ width: `${Math.max(2, (left / c.quota) * 100)}%` }} />
                      </span>
                      {soldOut ? "Kuota habis" : c.remaining === null ? `Kuota ${c.quota}` : `Sisa ${c.remaining} dari ${c.quota}`}
                    </div>
                  )}
                </div>
                <p className="font-display text-3xl leading-none font-extrabold">{formatRupiah(c.price)}</p>
                {canBuy && (
                  <div className="flex items-center gap-2">
                    {c.max_per_user > 1 && (
                      <select
                        className="rounded-md border border-line bg-card px-2 py-2 text-sm"
                        value={qty[c.id] ?? 1}
                        onChange={(e) => setQty({ ...qty, [c.id]: Number(e.target.value) })}
                        aria-label={`Jumlah peserta ${c.code}`}
                      >
                        {Array.from({ length: c.max_per_user }, (_, i) => i + 1).map((n) => (
                          <option key={n} value={n}>
                            {n} orang
                          </option>
                        ))}
                      </select>
                    )}
                    <button className={btnPrimary} disabled={!ready || soldOut || busy !== null} onClick={() => reserve(c)}>
                      {busy === c.id ? "Mengamankan slot…" : soldOut ? "Habis" : "Daftar"}
                    </button>
                  </div>
                )}
              </div>
            </li>
          );
        })}
      </ul>
    </>
  );
}

const JERSEY = ["XS", "S", "M", "L", "XL", "XXL"];

/** YYYY-MM-DD menurut zona waktu browser (untuk batas input tanggal). */
function localDate(ms: number): string {
  const d = new Date(ms);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

/** Label yang selalu terlihat di atas kolom, plus keterangan singkat di bawahnya. */
function Field({ label, required, hint, children }: { label: string; required?: boolean; hint?: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-sm font-semibold">
        {label}
        {required ? (
          <span className="ml-0.5 text-hivis" aria-hidden>
            *
          </span>
        ) : (
          <span className="ml-1 font-normal text-muted">(opsional)</span>
        )}
      </span>
      {children}
      {hint && <span className="mt-1 block text-xs text-muted">{hint}</span>}
    </label>
  );
}

function emptyParticipant(): Participant {
  return { full_name: "", email: "", id_number: "", birth_date: "", gender: "", jersey_size: "", emergency_name: "", emergency_phone: "" };
}

function CheckoutForm({
  event,
  category,
  reservation,
  onExpired,
}: {
  event: EventDetail;
  category: Category;
  reservation: Reservation;
  onExpired: () => void;
}) {
  const router = useRouter();
  const { me } = useSession();
  // Peserta pertama diisi dari akun yang login; sisanya kosong.
  const [participants, setParticipants] = useState<Participant[]>(() =>
    Array.from({ length: reservation.qty }, (_, i) =>
      i === 0 && me ? { ...emptyParticipant(), full_name: me.name.replace(/\s*\(.*\)$/, ""), email: me.email } : emptyParticipant(),
    ),
  );
  const [now, setNow] = useState(() => Date.now());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Satu kunci per reservasi: klik ganda / retry tidak membuat order dobel.
  const idempotencyKey = useMemo(() => crypto.randomUUID(), []);
  const remaining = new Date(reservation.expires_at).getTime() - now;
  const today = localDate(now);

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  function update(i: number, field: keyof Participant, value: string) {
    setParticipants((ps) => ps.map((p, idx) => (idx === i ? { ...p, [field]: value } : p)));
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const order = await apiFetch<Order>("/v1/orders", {
        method: "POST",
        headers: { "Idempotency-Key": idempotencyKey },
        body: { category_id: category.id, reservation_id: reservation.reservation_id, participants },
      });
      const goToOrder = () => router.push(`/orders/${order.order_id}`);
      if (order.mock || !order.snap_token) {
        goToOrder();
      } else {
        await openSnap(order.snap_token, { onSuccess: goToOrder, onPending: goToOrder, onClose: goToOrder, onError: goToOrder });
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Gagal membuat order");
      setBusy(false);
    }
  }

  if (remaining <= 0) {
    return (
      <div className="rounded-md bg-card p-6 ring-1 ring-line">
        <p className="font-semibold">Waktu 15 menit habis dan slot dikembalikan ke kuota.</p>
        <button className={`${btnDark} mt-4`} onClick={onExpired}>
          Pilih kategori lagi
        </button>
      </div>
    );
  }

  const urgent = remaining < 3 * 60_000;
  return (
    <form onSubmit={submit} className="space-y-6">
      <div className="relative flex flex-wrap items-center justify-between gap-4 overflow-hidden rounded-md bg-asphalt p-6 text-white">
        <PinHoles />
        <div>
          <p className="text-sm text-white/70">Slot {category.code} sudah diamankan untuk kamu</p>
          <p className="mt-1 text-lg font-semibold">
            {reservation.qty} peserta × {formatRupiah(category.price)} = {formatRupiah(reservation.qty * category.price)}
          </p>
        </div>
        <div className="text-right">
          <p className="text-xs text-white/70">Selesaikan dalam</p>
          <p className={`font-display text-5xl leading-none font-black tabular-nums ${urgent ? "text-hivis" : ""}`}>{formatCountdown(remaining)}</p>
        </div>
      </div>

      <p className="text-sm text-muted">
        Data peserta dipakai untuk nomor BIB, asuransi, dan pencocokan KTP saat pengambilan race pack. Kolom bertanda{" "}
        <span className="font-semibold text-hivis">*</span> wajib diisi.
      </p>

      {participants.map((p, i) => (
        <fieldset key={i} className="rounded-md bg-card p-6 ring-1 ring-line">
          <legend className="font-display px-1 text-2xl font-extrabold">Peserta {i + 1}</legend>
          {i === 0 && me && <p className="text-sm text-muted">Nama dan email diisi dari akun kamu. Ubah bila slot ini untuk orang lain.</p>}

          <h3 className="mt-4 text-xs font-semibold tracking-wide text-muted uppercase">Data diri</h3>
          <div className="mt-2 grid gap-4 sm:grid-cols-2">
            <Field label="Nama lengkap" required hint="Sesuai KTP / paspor">
              <input className={inputCls} required autoComplete="name" placeholder="mis. Budi Santoso" value={p.full_name} onChange={(e) => update(i, "full_name", e.target.value)} />
            </Field>
            <Field label="Email" required hint="Tiket & info race dikirim ke sini">
              <input className={inputCls} type="email" required autoComplete="email" placeholder="nama@email.com" value={p.email} onChange={(e) => update(i, "email", e.target.value)} />
            </Field>
            <Field label="NIK / nomor paspor" required hint="16 digit NIK di KTP, atau nomor paspor untuk WNA">
              <input className={inputCls} required minLength={6} maxLength={32} placeholder="3171xxxxxxxxxxxx" value={p.id_number} onChange={(e) => update(i, "id_number", e.target.value)} />
            </Field>
            <Field label="Tanggal lahir" hint="Untuk kategori usia & asuransi">
              <input className={inputCls} type="date" min="1920-01-01" max={today} autoComplete="bday" value={p.birth_date} onChange={(e) => update(i, "birth_date", e.target.value)} />
            </Field>
            <Field label="Jenis kelamin">
              <select className={inputCls} value={p.gender} onChange={(e) => update(i, "gender", e.target.value)}>
                <option value="">Pilih…</option>
                <option value="M">Laki-laki</option>
                <option value="F">Perempuan</option>
              </select>
            </Field>
            <Field label="Ukuran jersey" required hint="Ukuran tidak bisa diganti setelah bayar">
              <select className={inputCls} required value={p.jersey_size} onChange={(e) => update(i, "jersey_size", e.target.value)}>
                <option value="">Pilih ukuran…</option>
                {JERSEY.map((s) => (
                  <option key={s}>{s}</option>
                ))}
              </select>
            </Field>
          </div>

          <h3 className="mt-6 text-xs font-semibold tracking-wide text-muted uppercase">Kontak darurat</h3>
          <p className="mt-1 text-sm text-muted">Orang yang dihubungi panitia bila terjadi sesuatu saat race. Jangan isi dengan nomor peserta sendiri.</p>
          <div className="mt-2 grid gap-4 sm:grid-cols-2">
            <Field label="Nama kontak darurat" hint="mis. orang tua, pasangan, teman">
              <input className={inputCls} placeholder="mis. Ibu Siti" value={p.emergency_name} onChange={(e) => update(i, "emergency_name", e.target.value)} />
            </Field>
            <Field label="No. HP kontak darurat">
              <input className={inputCls} type="tel" inputMode="tel" autoComplete="off" placeholder="08xxxxxxxxxx" value={p.emergency_phone} onChange={(e) => update(i, "emergency_phone", e.target.value)} />
            </Field>
          </div>
        </fieldset>
      ))}

      {error && <ErrorBox message={error} />}
      <div className="flex flex-wrap items-center gap-4">
        <button type="submit" disabled={busy} className={btnPrimary}>
          {busy ? "Memproses…" : "Lanjut ke pembayaran"}
        </button>
        <p className="text-sm text-muted">
          {event.name}, {category.code}
        </p>
      </div>
    </form>
  );
}
