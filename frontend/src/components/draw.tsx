"use client";

import { useParams } from "next/navigation";
import { useState } from "react";
import {
  apiFetch,
  ApiError,
  canEdit,
  claimWinner,
  drawPath,
  roleIn,
  type AttendanceSettings,
  type DrawPool,
  type DrawPrize,
  type DrawSettings,
  type DrawWinner,
  type PartnerEvent,
} from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { DrawStage } from "./draw-stage";
import { Download } from "./finance";
import { useSession } from "./session";
import { btnDark, btnGhost, ErrorBox, Field, inputCls, inputErr, Loading, LoginHint, PageTitle } from "./ui";
import { ConfirmDialog } from "./ui-client";

const errText = (e: unknown) => (e instanceof ApiError ? e.message : "Terjadi kesalahan. Coba lagi.");
const card = "rounded-md bg-card p-4 ring-1 ring-line sm:p-5";
// <input type="datetime-local"> memakai waktu lokal tanpa zona; kirim ke server sebagai RFC 3339.
const toLocalInput = (iso: string | null) => {
  if (!iso) return "";
  const d = new Date(iso);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
};

export const STATUS_LABEL: Record<string, [string, string]> = {
  winner: ["Pemenang", "bg-amber-100 text-amber-900"],
  claimed: ["Hadiah diterima", "bg-emerald-100 text-emerald-900"],
  forfeited: ["Gugur", "bg-line text-muted"],
};

/** Halaman Undian: pengaturan, hadiah, kolam, pemenang, layar panggung. Owner/admin kelola; staf RPC hanya baca. */
export function Draw() {
  const { org, eventId } = useParams<{ org: string; eventId: string }>();
  const { ready, userId, me } = useSession();
  const base = `/v1/org/${org}/events/${eventId}`;
  const role = roleIn(me, org);
  const editor = canEdit(role);
  const event = useApi<PartnerEvent>(userId ? base : null, [userId]);
  const settings = useApi<DrawSettings>(userId ? drawPath(base, "/settings") : null, [userId]);
  const prizes = useApi<{ prizes: DrawPrize[] }>(userId ? drawPath(base, "/prizes") : null, [userId]);
  const winners = useApi<{ winners: DrawWinner[] }>(userId ? drawPath(base, "/winners") : null, [userId]);
  const pool = useApi<DrawPool>(userId && editor ? drawPath(base, "/pool") : null, [userId]);
  const [stage, setStage] = useState(false);

  if (!ready) return <Loading />;
  if (!userId) return <LoginHint />;
  const refresh = () => {
    prizes.reload();
    winners.reload();
    pool.reload();
    settings.reload();
  };

  return (
    <>
      <PageTitle title="Undian doorprize" back={{ href: `/org/${org}`, label: "Dashboard mitra" }}>
        {event.data && <p className="mt-2 text-sm break-words text-muted">{event.data.name}</p>}
      </PageTitle>
      {!role && <ErrorBox message="Anda bukan anggota organisasi ini." />}
      {role && !editor && <p className="mb-6 rounded-md border border-dashed border-line bg-card p-4 text-sm text-muted">Staf RPC hanya bisa melihat hadiah dan hasil. Pengaturan dan pengundian untuk owner dan admin.</p>}
      {editor && (
        <>
          <Warnings pool={pool.data} settings={settings.data} />
          <div className="mb-6 grid gap-4 lg:grid-cols-2">
            <SettingsCard base={base} data={settings.data} error={settings.error} onSaved={refresh} />
            <PoolCard pool={pool.data} error={pool.error} />
          </div>
          <AttendanceCard base={base} />
        </>
      )}
      <PrizeSection base={base} editor={editor} data={prizes.data?.prizes} error={prizes.error} pool={pool.data} onChanged={refresh} onStage={() => setStage(true)} />
      <WinnerSection base={base} editor={editor} data={winners.data?.winners} error={winners.error} onChanged={refresh} />
      {stage && prizes.data && <DrawStage base={base} eventName={event.data?.name ?? ""} prizes={prizes.data.prizes} winners={winners.data?.winners ?? []} onChanged={refresh} onClose={() => setStage(false)} />}
    </>
  );
}

function Warnings({ pool, settings }: { pool: DrawPool | null; settings: DrawSettings | null }) {
  if (!pool) return null;
  const msgs: string[] = [];
  if (!pool.draw_open) msgs.push(`Undian baru bisa dijalankan mulai ${formatDateTime(pool.draw_opens_at)} (saat absen hari-H dibuka).`);
  if (pool.eligibility === "attended" && pool.attended === 0) msgs.push("Belum ada peserta yang diabsen hari-H, jadi kolam undian masih kosong.");
  if (settings?.draw_eligibility === "collected") msgs.push("Kelayakan memakai pengambilan race pack. Ini tidak membuktikan peserta benar-benar ikut lari.");
  if (msgs.length === 0) return null;
  return (
    <ul className="mb-6 space-y-1 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-900" role="status">
      {msgs.map((m) => (
        <li key={m}>{m}</li>
      ))}
    </ul>
  );
}

function SettingsCard({ base, data, error, onSaved }: { base: string; data: DrawSettings | null; error: string | null; onSaved: () => void }) {
  const [form, setForm] = useState<DrawSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const f = form ?? data;
  async function save() {
    if (!f) return;
    setBusy(true);
    setMsg(null);
    try {
      await apiFetch(drawPath(base, "/settings"), { method: "PUT", body: { draw_eligibility: f.draw_eligibility, allow_multiple_wins: f.allow_multiple_wins, draw_public: f.draw_public } });
      setForm(null);
      setMsg({ ok: true, text: "Pengaturan tersimpan." });
      onSaved();
    } catch (e) {
      setMsg({ ok: false, text: errText(e) });
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className={card} aria-labelledby="pengaturan-undian">
      <h2 id="pengaturan-undian" className="mb-3 text-lg font-semibold">
        Pengaturan
      </h2>
      {error && !data ? (
        <ErrorBox message={error} />
      ) : !f ? (
        <Loading />
      ) : (
        <div className="space-y-4">
          <Field label="Siapa yang ikut diundi" required hint="Hadir hari-H = hanya peserta yang discan di lokasi lomba. Race pack diambil = tidak membuktikan peserta lari.">
            <select className={inputCls} disabled={f.locked || busy} value={f.draw_eligibility} onChange={(e) => setForm({ ...f, draw_eligibility: e.target.value as DrawSettings["draw_eligibility"] })}>
              <option value="attended">Hadir hari-H (disarankan)</option>
              <option value="collected">Race pack sudah diambil</option>
            </select>
          </Field>
          <Check label="Satu tiket hanya boleh menang sekali" hint="Bila dimatikan, peserta yang sudah menang tetap bisa menang hadiah lain." checked={!f.allow_multiple_wins} disabled={f.locked || busy} onChange={(v) => setForm({ ...f, allow_multiple_wins: !v })} />
          <Check label="Tampilkan hasil di halaman publik" hint="Hanya BIB, nama depan, dan kategori. Mati secara bawaan." checked={f.draw_public} disabled={busy} onChange={(v) => setForm({ ...f, draw_public: v })} />
          {f.locked && <p className="text-xs text-muted">Kelayakan dan aturan satu-menang terkunci karena sudah ada pemenang.</p>}
          {f.draw_public && (
            <p className="text-xs break-all text-muted">
              Tautan hasil: <code>/events/{base.split("/").pop()}/hasil-undian</code>
            </p>
          )}
          <div className="flex items-center gap-3">
            <button type="button" className={btnDark} disabled={busy || form === null} onClick={save}>
              {busy ? "Menyimpan…" : "Simpan pengaturan"}
            </button>
            {msg && (
              <span role={msg.ok ? "status" : "alert"} className={`text-sm ${msg.ok ? "text-go" : "text-red-700"}`}>
                {msg.text}
              </span>
            )}
          </div>
        </div>
      )}
    </section>
  );
}

function Check({ label, hint, checked, disabled, onChange }: { label: string; hint: string; checked: boolean; disabled?: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-start gap-3">
      <input type="checkbox" className="mt-1 size-4" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span>
        <span className="block text-sm font-semibold">{label}</span>
        <span className="block text-xs text-muted">{hint}</span>
      </span>
    </label>
  );
}

function PoolCard({ pool, error }: { pool: DrawPool | null; error: string | null }) {
  return (
    <section className={card} aria-labelledby="kolam-undian">
      <h2 id="kolam-undian" className="mb-3 text-lg font-semibold">
        Kolam undian
      </h2>
      {error && !pool ? (
        <ErrorBox message={error} />
      ) : !pool ? (
        <Loading />
      ) : (
        <>
          <p className="text-sm">
            <b className="font-display text-3xl tabular-nums" data-testid="kolam-total">
              {pool.total}
            </b>{" "}
            peserta {pool.eligibility === "attended" ? "hadir" : "dengan race pack"} layak diundi
          </p>
          <dl className="mt-3 grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 text-sm">
            {pool.by_category.map((c) => (
              <div key={c.id} className="contents">
                <dt>{c.code}</dt>
                <dd className="font-semibold tabular-nums">{c.count}</dd>
              </div>
            ))}
          </dl>
        </>
      )}
    </section>
  );
}

/** Jendela absen hari-H (bawaan: tanggal lomba sampai akhir hari berikutnya, WIB). Undian ikut aturan ini. */
function AttendanceCard({ base }: { base: string }) {
  const s = useApi<AttendanceSettings>(`${base}/attendance/settings`);
  const [form, setForm] = useState<{ o: string; c: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [err, setErr] = useState<Record<string, string>>({});
  const d = s.data;
  const o = form?.o ?? toLocalInput(d?.opens_at ?? null);
  const c = form?.c ?? toLocalInput(d?.closes_at ?? null);
  async function save(clear: boolean) {
    setBusy(true);
    setMsg(null);
    setErr({});
    try {
      await apiFetch(`${base}/attendance/settings`, {
        method: "PUT",
        body: { opens_at: clear || !o ? null : new Date(o).toISOString(), closes_at: clear || !c ? null : new Date(c).toISOString() },
      });
      setForm(null);
      setMsg({ ok: true, text: clear ? "Kembali ke jendela bawaan." : "Jendela absen tersimpan." });
      s.reload();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setErr(e.fields);
      else setMsg({ ok: false, text: errText(e) });
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className={`${card} mb-6`} aria-labelledby="jendela-absen">
      <h2 id="jendela-absen" className="text-lg font-semibold">
        Jendela absen hari-H
      </h2>
      <p className="mt-1 mb-3 text-sm text-muted">
        Absen dan undian baru bisa dijalankan dalam jendela ini. Kosongkan untuk aturan bawaan (tanggal lomba sampai akhir hari berikutnya, WIB). Berguna untuk event multi-hari.
        {d && (
          <>
            {" "}
            Berlaku sekarang: <b>{formatDateTime(d.effective_opens_at)}</b> – <b>{formatDateTime(d.effective_closes_at)}</b> ({d.open ? "terbuka" : "tertutup"}).
          </>
        )}
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Buka absen" error={err.opens_at}>
          <input type="datetime-local" className={inputErr(err.opens_at)} value={o} disabled={busy} onChange={(e) => setForm({ o: e.target.value, c })} />
        </Field>
        <Field label="Tutup absen" error={err.closes_at}>
          <input type="datetime-local" className={inputErr(err.closes_at)} value={c} disabled={busy} onChange={(e) => setForm({ o, c: e.target.value })} />
        </Field>
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <button type="button" className={btnDark} disabled={busy} onClick={() => save(false)}>
          {busy ? "Menyimpan…" : "Simpan jendela"}
        </button>
        <button type="button" className={btnGhost} disabled={busy || !d?.opens_at && !d?.closes_at} onClick={() => save(true)}>
          Pakai bawaan
        </button>
        {msg && (
          <span role={msg.ok ? "status" : "alert"} className={`text-sm ${msg.ok ? "text-go" : "text-red-700"}`}>
            {msg.text}
          </span>
        )}
      </div>
    </section>
  );
}

function PrizeSection({
  base,
  editor,
  data,
  error,
  pool,
  onChanged,
  onStage,
}: {
  base: string;
  editor: boolean;
  data?: DrawPrize[];
  error: string | null;
  pool: DrawPool | null;
  onChanged: () => void;
  onStage: () => void;
}) {
  const [editing, setEditing] = useState<DrawPrize | "new" | null>(null);
  const [name, setName] = useState("");
  const [qty, setQty] = useState("1");
  const [cat, setCat] = useState("");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [del, setDel] = useState<DrawPrize | null>(null);

  function open(p: DrawPrize | "new") {
    setEditing(p);
    setName(p === "new" ? "" : p.name);
    setQty(p === "new" ? "1" : String(p.quantity));
    setCat(p === "new" ? "" : (p.category_id ?? ""));
    setFields({});
    setMsg(null);
  }
  async function save() {
    if (!editing) return;
    setBusy(true);
    setFields({});
    setMsg(null);
    try {
      const body = { name, quantity: Number(qty), category_id: cat || null };
      if (editing === "new") await apiFetch(drawPath(base, "/prizes"), { method: "POST", body });
      else await apiFetch(drawPath(base, `/prizes/${editing.id}`), { method: "PUT", body });
      setEditing(null);
      onChanged();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      else setMsg(errText(e));
    } finally {
      setBusy(false);
    }
  }
  async function remove() {
    if (!del) return;
    setBusy(true);
    try {
      await apiFetch(drawPath(base, `/prizes/${del.id}`), { method: "DELETE" });
      setDel(null);
      onChanged();
    } catch (e) {
      setMsg(errText(e));
      setDel(null);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="mb-8" aria-labelledby="hadiah">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h2 id="hadiah" className="text-lg font-semibold">
          Hadiah
        </h2>
        <div className="flex flex-wrap gap-2">
          {editor && (
            <button type="button" className={btnGhost} onClick={() => open("new")} disabled={editing !== null}>
              Tambah hadiah
            </button>
          )}
          {editor && (
            <button type="button" className={btnDark} onClick={onStage} disabled={!data?.length}>
              Layar panggung
            </button>
          )}
        </div>
      </div>
      {msg && !editing && <ErrorBox message={msg} />}
      {editing && (
        <div className={`${card} mb-4`}>
          <h3 className="mb-3 font-semibold">{editing === "new" ? "Hadiah baru" : "Ubah hadiah"}</h3>
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label="Nama hadiah" required error={fields.name}>
              <input className={inputErr(fields.name)} value={name} maxLength={100} disabled={busy} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field label="Jumlah" required error={fields.quantity}>
              <input className={inputErr(fields.quantity)} type="number" min={1} max={1000} value={qty} disabled={busy} onChange={(e) => setQty(e.target.value)} />
            </Field>
            <Field label="Khusus kategori" error={fields.category_id} hint="Kosong = semua kategori.">
              <select className={inputErr(fields.category_id)} value={cat} disabled={busy} onChange={(e) => setCat(e.target.value)}>
                <option value="">Semua kategori</option>
                {pool?.by_category.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.code}
                  </option>
                ))}
              </select>
            </Field>
          </div>
          {msg && <p className="mt-2 text-sm text-red-700" role="alert">{msg}</p>}
          <div className="mt-3 flex gap-2">
            <button type="button" className={btnDark} disabled={busy} onClick={save}>
              {busy ? "Menyimpan…" : "Simpan hadiah"}
            </button>
            <button type="button" className={btnGhost} disabled={busy} onClick={() => setEditing(null)}>
              Batal
            </button>
          </div>
        </div>
      )}
      {error && !data ? (
        <ErrorBox message={error} />
      ) : !data ? (
        <Loading />
      ) : data.length === 0 ? (
        <p className="rounded-md border border-dashed border-line bg-card p-5 text-sm text-muted">Belum ada hadiah.{editor && " Klik Tambah hadiah."}</p>
      ) : (
        <ul className="divide-y divide-line rounded-md border border-line bg-card">
          {data.map((p) => (
            <li key={p.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
              <div className="min-w-0">
                <p className="font-semibold break-words">{p.name}</p>
                <p className="text-xs text-muted tabular-nums">
                  Terundi {p.drawn} / {p.quantity}
                  {p.category_code && ` · khusus ${p.category_code}`}
                  {p.winners > p.drawn && ` · ${p.winners - p.drawn} gugur`}
                </p>
              </div>
              {editor && (
                <div className="flex gap-2">
                  <button type="button" className={btnGhost} onClick={() => open(p)} aria-label={`Ubah ${p.name}`}>
                    Ubah
                  </button>
                  <button type="button" className={btnGhost} disabled={p.winners > 0} title={p.winners > 0 ? "Sudah punya pemenang" : undefined} onClick={() => setDel(p)} aria-label={`Hapus ${p.name}`}>
                    Hapus
                  </button>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      <ConfirmDialog open={del !== null} title="Hapus hadiah?" confirmLabel="Hapus" danger busy={busy} onClose={() => setDel(null)} onConfirm={remove}>
        <p>
          Hadiah <b>{del?.name}</b> dihapus. Tindakan ini tercatat di log audit.
        </p>
      </ConfirmDialog>
    </section>
  );
}

function WinnerSection({ base, editor, data, error, onChanged }: { base: string; editor: boolean; data?: DrawWinner[]; error: string | null; onChanged: () => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [fo, setFo] = useState<DrawWinner | null>(null);
  const [reason, setReason] = useState("");
  async function act(id: string, fn: () => Promise<unknown>) {
    setBusy(id);
    setMsg(null);
    try {
      await fn();
      setFo(null);
      onChanged();
    } catch (e) {
      setMsg(errText(e));
      setFo(null);
    } finally {
      setBusy(null);
    }
  }
  return (
    <section aria-labelledby="pemenang">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h2 id="pemenang" className="text-lg font-semibold">
          Pemenang
        </h2>
        {editor && <Download path={drawPath(base, "/results.csv")} name="hasil-undian.csv" label="Unduh CSV" />}
      </div>
      {msg && <ErrorBox message={msg} />}
      {error && !data ? (
        <ErrorBox message={error} />
      ) : !data ? (
        <Loading />
      ) : data.length === 0 ? (
        <p className="rounded-md border border-dashed border-line bg-card p-5 text-sm text-muted">Belum ada pemenang.</p>
      ) : (
        <ul className="divide-y divide-line rounded-md border border-line bg-card" data-testid="daftar-pemenang">
          {data.map((w) => {
            const [label, cls] = STATUS_LABEL[w.status];
            return (
              <li key={w.id} className="grid gap-2 px-4 py-3 sm:grid-cols-[1fr_auto] sm:items-center">
                <div className="min-w-0">
                  <p className="text-xs text-muted">
                    {w.prize_name} · #{w.seq}
                  </p>
                  <p className="font-semibold break-words">
                    <span className="font-display text-xl tabular-nums">{w.bib}</span> · {w.name} · {w.category}
                  </p>
                  <p className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted">
                    <span className={`inline-block rounded-full px-2.5 py-0.5 font-semibold whitespace-nowrap ${cls}`}>{label}</span>
                    <span>{formatDateTime(w.drawn_at)}</span>
                    {w.note && <span>Alasan: {w.note}</span>}
                  </p>
                </div>
                {editor && w.status === "winner" && (
                  <div className="flex gap-2">
                    <button type="button" className={btnGhost} disabled={busy === w.id} onClick={() => act(w.id, () => claimWinner(base, w.id))}>
                      Diterima
                    </button>
                    <button type="button" className={btnGhost} disabled={busy === w.id} onClick={() => { setReason(""); setFo(w); }}>
                      Tidak hadir
                    </button>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <ConfirmDialog
        open={fo !== null}
        title="Pemenang tidak hadir?"
        confirmLabel="Tandai gugur"
        danger
        busy={busy !== null}
        disabled={reason.trim().length < 5}
        onClose={() => setFo(null)}
        onConfirm={() => fo && act(fo.id, () => apiFetch(drawPath(base, `/winners/${fo.id}/forfeit`), { method: "POST", body: { reason: reason.trim() } }))}
      >
        <p>
          BIB <b>{fo?.bib}</b> ditandai gugur. Pengganti hanya lewat undian ulang. Baris pemenang tidak dihapus.
        </p>
        <Field label="Alasan (5–300 karakter)" required>
          <textarea className={inputCls} rows={2} maxLength={300} value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
      </ConfirmDialog>
    </section>
  );
}
