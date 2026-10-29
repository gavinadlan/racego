"use client";

import { useEffect, useId, useRef, useState } from "react";
import {
  ApiError,
  deleteKycDocument,
  kycDocUrl,
  saveVerification,
  submitVerification,
  uploadKycDocument,
  type DocKind,
  type KycDoc,
  type KycRequirements,
  type OrgProfile,
  type OrgType,
  type Verification,
  type VerificationHistory,
  type VerificationState,
} from "@/lib/api";
import { formatDateShort, formatDateTime } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { OrgShell } from "./org-shell";
import { btnDark, btnGhost, btnPrimary, ErrorBox, Field, inputErr, Loading } from "./ui";

export function OrgVerification() {
  return (
    <OrgShell tab="verification">
      {(profile, reload) => (profile.role === "owner" ? <OwnerVerification profile={profile} reloadProfile={reload} /> : <StatusOnly profile={profile} />)}
    </OrgShell>
  );
}

export const STATUS_LABEL: Record<string, string> = {
  draft: "Draft",
  submitted: "Menunggu peninjauan",
  approved: "Disetujui",
  rejected: "Ditolak",
};

export function VerificationBadge({ status }: { status: string }) {
  const cls: Record<string, string> = {
    draft: "bg-line text-asphalt",
    submitted: "bg-sky-100 text-sky-900",
    approved: "bg-go-soft text-go",
    rejected: "bg-red-100 text-red-800",
  };
  return <span className={`inline-block rounded-full px-2.5 py-0.5 text-xs font-semibold whitespace-nowrap ${cls[status] ?? "bg-line"}`}>{STATUS_LABEL[status] ?? status}</span>;
}

/** Peran selain owner hanya melihat status; isian dan dokumen hanya untuk owner. */
function StatusOnly({ profile }: { profile: OrgProfile }) {
  const text =
    profile.kyc_status === "verified"
      ? "Penyelenggara ini sudah terverifikasi dan bisa menerbitkan event."
      : profile.verification_status === "submitted"
        ? "Pengajuan verifikasi sedang ditinjau tim RaceGo."
        : profile.verification_status === "rejected"
          ? "Pengajuan verifikasi ditolak. Owner bisa memperbaiki lalu mengajukan ulang."
          : "Owner penyelenggara belum mengirim data verifikasi.";
  return (
    <div className="max-w-2xl rounded-md bg-card p-6 text-sm ring-1 ring-line">
      <p>{text}</p>
      <p className="mt-2 text-muted">Data identitas, dokumen, dan rekening hanya bisa dilihat dan diubah oleh owner.</p>
    </div>
  );
}

function OwnerVerification({ profile, reloadProfile }: { profile: OrgProfile; reloadProfile: () => void }) {
  // Dimuat ulang saat status di profil berubah (mis. setelah "Ajukan ulang" di banner).
  const { data, error, loading } = useApi<VerificationState>(`/v1/org/${profile.slug}/verification`, [profile.verification_status, profile.kyc_status]);
  // Respons aksi terakhir (simpan/unggah) menggantikan hasil GET sampai GET berikutnya.
  const [local, setLocal] = useState<{ base: VerificationState | null; s: VerificationState } | null>(null);
  const s = local && local.base === data ? local.s : data;

  if (loading && !s) return <Loading />;
  if (error && !s) return <ErrorBox message={error} />;
  if (!s) return null;

  const v = s.verification;
  const onState = (n: VerificationState) => {
    setLocal({ base: data, s: n });
    if (n.verification?.status !== v?.status || n.kyc_status !== s.kyc_status) reloadProfile();
  };

  if (s.kyc_status === "verified") return <VerifiedCard state={s} />;
  if (v && v.status !== "draft") {
    return (
      <div className="max-w-3xl space-y-6">
        <section className="rounded-md bg-card p-6 ring-1 ring-line">
          <div className="mb-4 flex flex-wrap items-center gap-2">
            <h2 className="font-display text-3xl leading-none font-extrabold">Pengajuan ke-{v.attempt}</h2>
            <VerificationBadge status={v.status} />
          </div>
          {v.status === "submitted" && <p className="mb-4 text-sm text-muted">Dikirim {v.submitted_at && formatDateTime(v.submitted_at)}. Selama ditinjau, isian dan dokumen tidak bisa diubah.</p>}
          <Summary org={profile.slug} v={v} req={s.requirements} />
        </section>
        <History items={s.history} />
      </div>
    );
  }
  return <Wizard key={v?.attempt ?? 1} org={profile.slug} state={s} onState={onState} />;
}

function VerifiedCard({ state }: { state: VerificationState }) {
  const v = state.verification;
  return (
    <div className="max-w-3xl space-y-6">
      <section className="rounded-md border border-go/30 bg-go-soft p-6 text-sm text-go">
        <p className="font-display text-3xl leading-none font-extrabold">✓ Terverifikasi</p>
        <p className="mt-3 text-asphalt">
          {v?.status === "approved"
            ? `Disetujui tim RaceGo ${v.reviewed_at ? formatDateShort(v.reviewed_at) : ""}. Penyelenggara ini bisa menerbitkan event.`
            : state.auto_verify
              ? "Diverifikasi otomatis (mode development): AUTO_VERIFY_ORGS=true memotong alur pengajuan. Set false untuk mencoba alur verifikasi sungguhan."
              : "Penyelenggara ini sudah terverifikasi oleh tim RaceGo dan bisa menerbitkan event."}
        </p>
      </section>
      <History items={state.history} />
    </div>
  );
}

function History({ items }: { items: VerificationHistory[] }) {
  if (items.length < 2) return null;
  return (
    <section aria-labelledby="riwayat">
      <h2 id="riwayat" className="mb-3 text-lg font-semibold">
        Riwayat pengajuan
      </h2>
      <ol className="divide-y divide-line rounded-md border border-line bg-card text-sm">
        {items.map((h) => (
          <li key={h.id} className="px-4 py-3">
            <div className="flex flex-wrap items-center gap-2">
              <b>Pengajuan ke-{h.attempt}</b>
              <VerificationBadge status={h.status} />
              {h.submitted_at && <span className="text-muted">dikirim {formatDateTime(h.submitted_at)}</span>}
            </div>
            {h.reject_reason && <p className="mt-1 whitespace-pre-line text-muted">Alasan: {h.reject_reason}</p>}
          </li>
        ))}
      </ol>
    </section>
  );
}

// ===== Formulir bertahap =====

type Form = {
  org_type: OrgType | "";
  legal_name: string;
  address: string;
  pic_name: string;
  pic_phone: string;
  pic_id: string; // kosong = tidak diubah (nilai tersimpan tidak pernah dikirim ke browser)
  npwp: string;
  bank_code: string;
  bank_name: string;
  account_number: string;
  account_holder: string;
};

const fromV = (v: Verification | null): Form => ({
  org_type: v?.org_type ?? "",
  legal_name: v?.legal_name ?? "",
  address: v?.address ?? "",
  pic_name: v?.pic_name ?? "",
  pic_phone: v?.pic_phone ?? "",
  pic_id: "",
  npwp: "",
  bank_code: v?.bank_code ?? "",
  bank_name: v?.bank_code === "lainnya" ? (v?.bank_name ?? "") : "",
  account_number: "",
  account_holder: v?.account_holder ?? "",
});

const STEPS = ["Jenis & identitas", "Dokumen", "Rekening", "Tinjau & kirim"];

/** Langkah tempat kolom berada (untuk tombol "Perbaiki" di ringkasan error). */
const stepOf = (key: string) =>
  key.startsWith("documents.") ? 1 : ["bank_code", "bank_name", "account_number", "account_holder"].includes(key) ? 2 : key === "consent" ? 3 : 0;

const FIELD_LABEL: Record<string, string> = {
  org_type: "Jenis penyelenggara",
  legal_name: "Nama sesuai dokumen",
  address: "Alamat",
  pic_name: "Nama penanggung jawab",
  pic_phone: "No. HP penanggung jawab",
  pic_id: "NIK penanggung jawab",
  npwp: "Nomor NPWP",
  bank_code: "Bank",
  bank_name: "Nama bank",
  account_number: "Nomor rekening",
  account_holder: "Nama pemilik rekening",
  consent: "Persetujuan",
};

const legalNameLabel = (t: OrgType | "") =>
  t === "badan_usaha" ? "Nama badan usaha (sesuai akta)" : t === "komunitas" ? "Nama komunitas" : "Nama lengkap sesuai KTP";

function Wizard({ org, state, onState }: { org: string; state: VerificationState; onState: (s: VerificationState) => void }) {
  const v = state.verification;
  const req = state.requirements;
  const [form, setForm] = useState<Form>(() => fromV(v));
  const [saved, setSaved] = useState<Form>(form);
  const [step, setStep] = useState(0);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState<{ error?: string; ok?: string }>({});
  const [busy, setBusy] = useState(false);
  const [consent, setConsent] = useState(false);
  const heading = useRef<HTMLHeadingElement>(null);
  const dirty = JSON.stringify(form) !== JSON.stringify(saved);
  const rule = req.types.find((t) => t.type === (v?.org_type || form.org_type));

  // Peringatan bila menutup/memuat ulang halaman dengan perubahan yang belum disimpan.
  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const set = (k: keyof Form) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => setForm((f) => ({ ...f, [k]: e.target.value }));

  async function save(): Promise<boolean> {
    if (!dirty) return true;
    setBusy(true);
    setMsg({});
    try {
      const n = await saveVerification(org, {
        ...form,
        pic_id: form.pic_id || undefined,
        npwp: form.npwp || undefined,
        account_number: form.account_number || undefined,
      });
      const f = fromV(n.verification);
      setForm(f);
      setSaved(f);
      setFields({});
      setMsg({ ok: "Draft tersimpan." });
      onState(n);
      return true;
    } catch (err) {
      if (err instanceof ApiError && err.fields) setFields(err.fields);
      setMsg({ error: err instanceof ApiError ? (err.fields ? "Periksa kembali isian yang ditandai." : err.message) : "Gagal menyimpan" });
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function go(i: number) {
    if (i === step || !(await save())) return;
    setStep(i);
    setMsg((m) => (m.error ? {} : m));
    requestAnimationFrame(() => heading.current?.focus());
  }

  async function submit() {
    if (!(await save())) return;
    setBusy(true);
    setMsg({});
    try {
      onState(await submitVerification(org, consent));
    } catch (err) {
      if (err instanceof ApiError && err.fields) setFields(err.fields);
      setMsg({ error: err instanceof ApiError ? (err.fields ? "Pengajuan belum lengkap. Perbaiki bagian berikut lalu kirim lagi." : err.message) : "Gagal mengirim" });
    } finally {
      setBusy(false);
    }
  }

  const stored = (masked: string, empty: string) => (masked ? `Tersimpan: ${masked}. Isi hanya bila ingin mengganti.` : empty);
  const errorKeys = Object.keys(fields);

  return (
    <div className="max-w-3xl">
      {v && v.attempt > 1 && (
        <p className="mb-4 rounded-md bg-paper p-3 text-sm text-muted ring-1 ring-line">
          Pengajuan ke-{v.attempt}. Isian dan dokumen dari pengajuan sebelumnya sudah disalin; perbaiki sesuai alasan penolakan lalu kirim lagi.
        </p>
      )}
      <nav aria-label="Langkah verifikasi">
        <ol className="mb-6 grid grid-cols-4 gap-1.5">
          {STEPS.map((t, i) => (
            <li key={t}>
              <button
                type="button"
                onClick={() => go(i)}
                disabled={busy}
                aria-current={i === step ? "step" : undefined}
                className={`h-full w-full rounded-md border-b-4 px-2 py-2 text-left text-xs transition-colors focus-visible:outline-2 focus-visible:outline-hivis disabled:cursor-wait ${
                  i === step ? "border-hivis bg-card font-semibold text-asphalt ring-1 ring-line" : i < step ? "border-go bg-card/60 text-asphalt" : "border-line text-muted hover:text-asphalt"
                }`}
              >
                <span className="block font-semibold">{i + 1}</span>
                <span className="hidden sm:block">{t}</span>
              </button>
            </li>
          ))}
        </ol>
      </nav>
      <h2 ref={heading} tabIndex={-1} className="font-display mb-5 text-3xl leading-none font-extrabold focus:outline-none">
        {step + 1}. {STEPS[step]}
      </h2>

      <form
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          if (step < 3) void go(step + 1);
          else void submit();
        }}
        className="space-y-6"
      >
        <fieldset disabled={busy} className="space-y-5 rounded-md bg-card p-5 ring-1 ring-line sm:p-6">
          {step === 0 && (
            <>
              <fieldset>
                <legend className="mb-2 text-sm font-semibold">
                  Jenis penyelenggara<span className="ml-0.5 text-hivis" aria-hidden>*</span>
                </legend>
                <div className="grid gap-3 sm:grid-cols-3">
                  {req.types.map((t) => (
                    <label
                      key={t.type}
                      className="block cursor-pointer rounded-md border-2 border-line p-4 text-sm has-checked:border-hivis has-checked:bg-hivis-soft/40 has-focus-visible:outline-2 has-focus-visible:outline-hivis"
                    >
                      <span className="flex items-center gap-2 font-semibold">
                        <input type="radio" name="org_type" value={t.type} checked={form.org_type === t.type} onChange={set("org_type")} className="accent-hivis" />
                        {t.label}
                      </span>
                      <span className="mt-1 block text-muted">{t.description}</span>
                      <span className="mt-2 block text-xs">
                        <b>Wajib:</b> {t.required.map((k) => req.documents[k]).join(", ")}
                      </span>
                      <span className="mt-1 block text-xs text-muted">
                        <b>Opsional:</b> {t.optional.map((k) => req.documents[k]).join(", ")}
                      </span>
                    </label>
                  ))}
                </div>
                {fields.org_type && (
                  <p className="mt-1 text-xs font-medium text-red-700" role="alert">
                    {fields.org_type}
                  </p>
                )}
                {v?.org_type && form.org_type !== v.org_type && v.documents && v.documents.length > 0 && (
                  <p className="mt-2 text-xs text-amber-800">Dokumen yang tidak dipakai jenis baru akan dihapus saat disimpan.</p>
                )}
              </fieldset>
              <Field label={legalNameLabel(form.org_type)} required error={fields.legal_name}>
                <input className={inputErr(fields.legal_name)} maxLength={150} value={form.legal_name} onChange={set("legal_name")} autoComplete="organization" />
              </Field>
              <Field label="Alamat" required hint="Alamat lengkap sesuai dokumen (jalan, kota, provinsi)." error={fields.address}>
                <textarea className={`${inputErr(fields.address)} min-h-20`} maxLength={500} value={form.address} onChange={set("address")} autoComplete="street-address" />
              </Field>
              <div className="grid gap-5 sm:grid-cols-2">
                <Field label="Nama penanggung jawab (PIC)" required hint="Sesuai KTP." error={fields.pic_name}>
                  <input className={inputErr(fields.pic_name)} maxLength={100} value={form.pic_name} onChange={set("pic_name")} autoComplete="name" />
                </Field>
                <Field label="No. HP penanggung jawab" required hint="Mis. 081234567890" error={fields.pic_phone}>
                  <input className={inputErr(fields.pic_phone)} type="tel" inputMode="tel" maxLength={20} value={form.pic_phone} onChange={set("pic_phone")} autoComplete="tel" />
                </Field>
                <Field label="NIK penanggung jawab" required hint={stored(v?.pic_id ?? "", "16 digit sesuai KTP. Disimpan terenkripsi.")} error={fields.pic_id}>
                  <input className={`${inputErr(fields.pic_id)} font-mono`} inputMode="numeric" maxLength={20} value={form.pic_id} onChange={set("pic_id")} autoComplete="off" placeholder={v?.pic_id || undefined} />
                </Field>
                <Field
                  label="Nomor NPWP"
                  required={form.org_type === "badan_usaha"}
                  hint={stored(v?.npwp ?? "", form.org_type === "badan_usaha" ? "15 atau 16 digit NPWP badan usaha." : "15 atau 16 digit, bila punya.")}
                  error={fields.npwp}
                >
                  <input className={`${inputErr(fields.npwp)} font-mono`} inputMode="numeric" maxLength={24} value={form.npwp} onChange={set("npwp")} autoComplete="off" placeholder={v?.npwp || undefined} />
                </Field>
              </div>
            </>
          )}

          {step === 1 &&
            (!v?.org_type || !rule ? (
              <p className="text-sm text-muted">Pilih jenis penyelenggara di langkah 1 dulu, lalu daftar dokumen yang dibutuhkan muncul di sini.</p>
            ) : (
              <>
                <div className="rounded-md bg-paper p-4 text-sm ring-1 ring-line">
                  <p className="font-semibold">Privasi dokumen</p>
                  <p className="mt-1 text-muted">
                    Dokumen disimpan terenkripsi, hanya dilihat tim peninjau RaceGo, dan setiap aksesnya tercatat. Gunakan salinan dokumen yang memang boleh kamu bagikan; tutupi bagian yang
                    tidak diperlukan bila perlu.
                  </p>
                </div>
                <p className="text-sm text-muted">Format JPG, PNG, atau PDF, maks {Math.round(req.max_bytes / 1024 / 1024)} MB per berkas. Satu jenis dokumen satu berkas; mengunggah lagi menggantikan.</p>
                <ul className="divide-y divide-line rounded-md border border-line">
                  {[...rule.required, ...rule.optional].map((k) => (
                    <DocRow
                      key={k}
                      org={org}
                      kind={k}
                      label={req.documents[k]}
                      required={rule.required.includes(k)}
                      doc={v.documents?.find((d) => d.kind === k) ?? null}
                      maxBytes={req.max_bytes}
                      error={fields[`documents.${k}`]}
                      onState={(n) => {
                        setFields((f) => Object.fromEntries(Object.entries(f).filter(([key]) => key !== `documents.${k}`)));
                        onState(n);
                      }}
                    />
                  ))}
                </ul>
              </>
            ))}

          {step === 2 && (
            <>
              <div className="grid gap-5 sm:grid-cols-2">
                <Field label="Bank" required error={fields.bank_code}>
                  <select className={inputErr(fields.bank_code)} value={form.bank_code} onChange={set("bank_code")}>
                    <option value="">Pilih bank</option>
                    {req.banks.map((b) => (
                      <option key={b.code} value={b.code}>
                        {b.name}
                      </option>
                    ))}
                  </select>
                </Field>
                {form.bank_code === "lainnya" && (
                  <Field label="Nama bank" required error={fields.bank_name}>
                    <input className={inputErr(fields.bank_name)} maxLength={60} value={form.bank_name} onChange={set("bank_name")} />
                  </Field>
                )}
                <Field label="Nomor rekening" required hint={stored(v?.account ?? "", "6–20 digit angka. Disimpan terenkripsi.")} error={fields.account_number}>
                  <input
                    className={`${inputErr(fields.account_number)} font-mono`}
                    inputMode="numeric"
                    maxLength={26}
                    value={form.account_number}
                    onChange={set("account_number")}
                    autoComplete="off"
                    placeholder={v?.account || undefined}
                  />
                </Field>
                <Field label="Nama pemilik rekening" required hint="Persis seperti tertulis di buku tabungan." error={fields.account_holder}>
                  <input className={inputErr(fields.account_holder)} maxLength={100} value={form.account_holder} onChange={set("account_holder")} autoComplete="off" />
                </Field>
              </div>
              {rule && (
                <p className="rounded-md bg-paper p-3 text-sm ring-1 ring-line">
                  <b>Aturan nama pemilik ({rule.label.toLowerCase()}):</b> {rule.holder_rule} Tim peninjau mencocokkannya dengan data di langkah 1; rekening atas nama lain akan ditolak.
                </p>
              )}
            </>
          )}

          {step === 3 && (
            <>
              {v ? <Summary org={org} v={v} req={req} /> : <p className="text-sm text-muted">Belum ada isian. Mulai dari langkah 1.</p>}
              <label className="flex gap-3 rounded-md bg-paper p-4 text-sm ring-1 ring-line">
                <input type="checkbox" className="mt-0.5 size-4 shrink-0 accent-hivis" checked={consent} onChange={(e) => setConsent(e.target.checked)} />
                <span>
                  {req.consent.text}
                  {fields.consent && (
                    <span className="mt-1 block text-xs font-medium text-red-700" role="alert">
                      {fields.consent}
                    </span>
                  )}
                </span>
              </label>
            </>
          )}
        </fieldset>

        {errorKeys.length > 0 && step === 3 && (
          <div className="rounded-md border border-red-200 bg-red-50 p-4 text-sm text-red-900" role="alert">
            <p className="font-semibold">Belum bisa dikirim:</p>
            <ul className="mt-2 space-y-1">
              {errorKeys.map((k) => (
                <li key={k} className="flex flex-wrap items-baseline gap-x-2">
                  <span>
                    {FIELD_LABEL[k] ?? req.documents[k.replace("documents.", "") as DocKind] ?? k}: {fields[k]}
                  </span>
                  {stepOf(k) !== 3 && (
                    <button type="button" className="font-semibold underline underline-offset-2" onClick={() => go(stepOf(k))}>
                      Perbaiki di langkah {stepOf(k) + 1}
                    </button>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}

        <div className="flex flex-wrap items-center gap-3">
          {step > 0 && (
            <button type="button" className={btnGhost} disabled={busy} onClick={() => go(step - 1)}>
              Kembali
            </button>
          )}
          {step < 3 ? (
            <button type="submit" className={btnDark} disabled={busy}>
              {busy ? "Menyimpan…" : "Simpan & lanjut"}
            </button>
          ) : (
            <button type="submit" className={btnPrimary} disabled={busy || !consent}>
              {busy ? "Mengirim…" : "Kirim untuk ditinjau"}
            </button>
          )}
          {step < 3 && dirty && (
            <button type="button" className={btnGhost} disabled={busy} onClick={() => void save()}>
              Simpan draft
            </button>
          )}
          <span className="min-w-0 basis-full text-sm sm:flex-1 sm:basis-auto" aria-live="polite">
            {msg.error ? (
              <span className="font-medium text-red-700">{msg.error}</span>
            ) : dirty ? (
              <span className="text-muted">Ada perubahan yang belum disimpan.</span>
            ) : (
              msg.ok && <span className="text-go">{msg.ok}</span>
            )}
          </span>
        </div>
      </form>
    </div>
  );
}

const sizeText = (n: number) => (n >= 1024 * 1024 ? `${(n / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`);

function DocRow({
  org,
  kind,
  label,
  required,
  doc,
  maxBytes,
  error,
  onState,
}: {
  org: string;
  kind: DocKind;
  label: string;
  required: boolean;
  doc: KycDoc | null;
  maxBytes: number;
  error?: string;
  onState: (s: VerificationState) => void;
}) {
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [preview, setPreview] = useState(false);

  async function pick(file: File | undefined) {
    if (!file) return;
    setMsg(null);
    if (file.size > maxBytes) {
      setMsg(`Ukuran berkas maksimal ${Math.round(maxBytes / 1024 / 1024)} MB (berkas ini ${sizeText(file.size)}).`);
      return;
    }
    setBusy(true);
    setProgress(0);
    try {
      onState(await uploadKycDocument(org, kind, file, setProgress));
      setPreview(false);
    } catch (err) {
      setMsg(err instanceof ApiError ? err.message : "Gagal mengunggah");
    } finally {
      setBusy(false);
      setProgress(null);
      if (input.current) input.current.value = "";
    }
  }

  async function remove() {
    setBusy(true);
    setMsg(null);
    try {
      onState(await deleteKycDocument(org, kind));
      setPreview(false);
    } catch (err) {
      setMsg(err instanceof ApiError ? err.message : "Gagal menghapus");
    } finally {
      setBusy(false);
    }
  }

  const shown = msg ?? error;
  return (
    <li className="p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-56">
          <p className="flex flex-wrap items-center gap-2 font-semibold" id={`${id}-label`}>
            {label}
            {doc ? (
              <span className="rounded-full bg-go-soft px-2 py-0.5 text-xs text-go">Terunggah</span>
            ) : required ? (
              <span className="rounded-full bg-hivis-soft px-2 py-0.5 text-xs text-[#a83a10]">Wajib</span>
            ) : (
              <span className="rounded-full bg-line px-2 py-0.5 text-xs text-muted">Opsional</span>
            )}
          </p>
          {doc && (
            <p className="mt-0.5 truncate text-xs text-muted">
              {doc.name} · {sizeText(doc.size)}
            </p>
          )}
        </div>
        <div className="flex flex-wrap gap-2">
          <input
            ref={input}
            id={id}
            type="file"
            accept="image/jpeg,image/png,application/pdf"
            className="sr-only"
            aria-labelledby={`${id}-label`}
            disabled={busy}
            onChange={(e) => pick(e.target.files?.[0])}
          />
          <label htmlFor={id} className={`${btnGhost} cursor-pointer py-1.5 ${busy ? "pointer-events-none opacity-40" : ""}`}>
            {doc ? "Ganti" : "Unggah"}
            <span className="sr-only"> {label}</span>
          </label>
          {doc && (
            <>
              <button type="button" className={`${btnGhost} py-1.5`} aria-expanded={preview} onClick={() => setPreview((p) => !p)} disabled={busy}>
                {preview ? "Tutup" : "Pratinjau"}
                <span className="sr-only"> {label}</span>
              </button>
              <button type="button" className={`${btnGhost} py-1.5 text-red-700`} onClick={remove} disabled={busy}>
                Hapus
                <span className="sr-only"> {label}</span>
              </button>
            </>
          )}
        </div>
      </div>
      {progress !== null && (
        <div className="mt-3" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress} aria-label={`Mengunggah ${label}`}>
          <div className="h-2 overflow-hidden rounded-full bg-line">
            <div className="h-full bg-hivis transition-[width]" style={{ width: `${progress}%` }} />
          </div>
          <p className="mt-1 text-xs text-muted">{progress < 100 ? `Mengunggah… ${progress}%` : "Memeriksa berkas…"}</p>
        </div>
      )}
      {shown && (
        <p className="mt-2 text-xs font-medium text-red-700" role="alert">
          {shown}
        </p>
      )}
      {preview && doc && <DocPreview url={kycDocUrl(org, doc)} contentType={doc.content_type} title={label} />}
    </li>
  );
}

/**
 * Pratinjau dokumen di halaman. Gambar langsung; PDF di <iframe>. Atribut sandbox sengaja TIDAK dipakai:
 * Chrome menolak menampilkan PDF di iframe ber-atribut sandbox ("blocked"). Penguncinya header respons
 * server `Content-Security-Policy: sandbox; default-src 'none'`, yang tetap membuat dokumen ber-origin
 * unik tanpa skrip, di bingkai ini maupun bila dibuka langsung.
 */
export function DocPreview({ url, contentType, title }: { url: string; contentType: string; title: string }) {
  if (contentType === "application/pdf") {
    return (
      <div className="mt-3">
        <iframe src={url} title={`Pratinjau ${title}`} referrerPolicy="no-referrer" className="h-[60vh] max-h-[640px] w-full rounded-md border border-line bg-paper" />
        <p className="mt-1 text-xs text-muted">PDF tidak tampil? Sebagian browser HP tidak menampilkan PDF di dalam halaman; gunakan tombol unduh di bingkai bila ada.</p>
      </div>
    );
  }
  return (
    // eslint-disable-next-line @next/next/no-img-element -- dokumen privat dari API (cookie sesi), bukan aset Next
    <img src={url} alt={`Pratinjau ${title}`} className="mt-3 max-h-[60vh] w-auto max-w-full rounded-md border border-line bg-paper object-contain" />
  );
}

/** Ringkasan pengajuan (data sensitif tersamar) + daftar dokumen. */
function Summary({ org, v, req }: { org: string; v: Verification; req: KycRequirements }) {
  const rule = req.types.find((t) => t.type === v.org_type);
  const [open, setOpen] = useState<DocKind | null>(null);
  const rows: [string, string][] = [
    ["Jenis", rule?.label ?? "Belum dipilih"],
    [legalNameLabel(v.org_type), v.legal_name],
    ["Alamat", v.address],
    ["Penanggung jawab", v.pic_name],
    ["No. HP", v.pic_phone],
    ["NIK", v.pic_id],
    ["NPWP", v.npwp],
    ["Bank", v.bank_name],
    ["Nomor rekening", v.account],
    ["Pemilik rekening", v.account_holder],
  ];
  const kinds = rule ? [...rule.required, ...rule.optional] : [];
  return (
    <div className="space-y-5 text-sm">
      <dl className="grid gap-x-6 gap-y-3 sm:grid-cols-[12rem_1fr]">
        {rows.map(([k, val]) => (
          <div key={k} className="contents">
            <dt className="text-muted">{k}</dt>
            <dd className={`min-w-0 font-medium break-words ${val ? "" : "text-muted"}`}>{val || "–"}</dd>
          </div>
        ))}
      </dl>
      {kinds.length > 0 && (
        <div>
          <p className="mb-2 font-semibold">Dokumen</p>
          <ul className="divide-y divide-line rounded-md border border-line">
            {kinds.map((k) => {
              const d = v.documents?.find((x) => x.kind === k);
              return (
                <li key={k} className="px-4 py-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span>
                      {req.documents[k]} {!rule?.required.includes(k) && <span className="text-muted">(opsional)</span>}
                      {d && <span className="block truncate text-xs text-muted">{d.name}</span>}
                    </span>
                    {d ? (
                      <button type="button" className={`${btnGhost} py-1`} aria-expanded={open === k} onClick={() => setOpen(open === k ? null : k)}>
                        {open === k ? "Tutup" : "Lihat"}
                        <span className="sr-only"> {req.documents[k]}</span>
                      </button>
                    ) : (
                      <span className={rule?.required.includes(k) ? "text-xs font-semibold text-red-700" : "text-xs text-muted"}>Belum diunggah</span>
                    )}
                  </div>
                  {open === k && d && <DocPreview url={kycDocUrl(org, d)} contentType={d.content_type} title={req.documents[k]} />}
                </li>
              );
            })}
          </ul>
        </div>
      )}
    </div>
  );
}
