"use client";

import Link from "next/link";
import { useState } from "react";
import { ApiError, canEdit, updateOrgProfile, type OrgProfile } from "@/lib/api";
import { ImageUpload } from "./image-upload";
import { OrgShell } from "./org-shell";
import { useSession } from "./session";
import { btnPrimary, ErrorBox, Field, inputCls, inputErr } from "./ui";

export function OrgProfileTab() {
  return <OrgShell tab="profile">{(profile, reload) => <ProfileForm key={profile.slug} profile={profile} onSaved={reload} />}</OrgShell>;
}

function ProfileForm({ profile, onSaved }: { profile: OrgProfile; onSaved: () => void }) {
  const { refresh } = useSession();
  const editable = canEdit(profile.role);
  const [form, setForm] = useState({
    name: profile.name,
    city: profile.city,
    description: profile.description,
    instagram: profile.instagram ?? "",
    website: profile.website ?? "",
    theme_color: profile.theme_color,
    logo_url: profile.logo_url ?? "",
  });
  const [fields, setFields] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState<{ error?: string; ok?: string }>({});
  const [busy, setBusy] = useState(false);
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => setForm((f) => ({ ...f, [k]: e.target.value }));

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFields({});
    setMsg({});
    try {
      await updateOrgProfile(profile.slug, form);
      setMsg({ ok: "Profil disimpan." });
      onSaved();
      void refresh(); // nama di menu header
    } catch (err) {
      if (err instanceof ApiError && err.fields) setFields(err.fields);
      setMsg({ error: err instanceof ApiError ? (err.fields ? "Periksa kembali isian yang ditandai." : err.message) : "Gagal menyimpan" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="max-w-2xl space-y-5 rounded-md bg-card p-6 ring-1 ring-line" noValidate>
      {!editable && <p className="rounded-md bg-paper p-3 text-sm text-muted">Hanya owner atau admin yang bisa mengubah profil.</p>}
      <fieldset disabled={!editable || busy} className="space-y-5">
        <Field label="Alamat halaman" required hint="Tidak bisa diubah setelah terdaftar.">
          <input className={`${inputCls} text-muted`} value={`/penyelenggara/${profile.slug}`} readOnly />
        </Field>
        <Field label="Nama penyelenggara" required error={fields.name}>
          <input className={inputErr(fields.name)} maxLength={80} value={form.name} onChange={set("name")} />
        </Field>
        <Field label="Kota" required error={fields.city}>
          <input className={inputErr(fields.city)} maxLength={60} value={form.city} onChange={set("city")} />
        </Field>
        <Field label="Deskripsi" hint="Tampil di halaman penyelenggara." error={fields.description}>
          <textarea className={`${inputErr(fields.description)} min-h-28`} maxLength={1000} value={form.description} onChange={set("description")} />
        </Field>
        <div className="grid gap-5 sm:grid-cols-2">
          <Field label="Instagram" hint="Nama akun saja, tanpa @" error={fields.instagram}>
            <input className={inputErr(fields.instagram)} maxLength={30} value={form.instagram} onChange={set("instagram")} />
          </Field>
          <Field label="Situs web" hint="Diawali https://" error={fields.website}>
            <input className={inputErr(fields.website)} type="url" value={form.website} onChange={set("website")} />
          </Field>
        </div>
        <Field label="Warna tema" required hint="Warna pita kartu event dan halaman penyelenggara." error={fields.theme_color}>
          <span className="flex items-center gap-3">
            <input type="color" className="h-10 w-14 cursor-pointer rounded-md border border-line bg-card p-1" value={form.theme_color} onChange={set("theme_color")} aria-label="Pilih warna" />
            <input className={`${inputErr(fields.theme_color)} w-32 font-mono uppercase`} maxLength={7} value={form.theme_color} onChange={set("theme_color")} />
          </span>
        </Field>
      </fieldset>
      <ImageUpload
        org={profile.slug}
        kind="logo"
        label="Logo"
        hint="JPEG atau PNG, maks 3 MB. Paling bagus persegi dengan latar putih/transparan."
        value={form.logo_url}
        onChange={(url) => setForm((f) => ({ ...f, logo_url: url }))}
        disabled={!editable || busy}
        error={fields.logo_url}
        boxClass="size-24"
      />
      {msg.error && <ErrorBox message={msg.error} />}
      <div className="flex flex-wrap items-center gap-4">
        {editable && (
          <button type="submit" className={btnPrimary} disabled={busy}>
            {busy ? "Menyimpan…" : "Simpan profil"}
          </button>
        )}
        <span className="text-sm text-go" aria-live="polite">
          {msg.ok}
        </span>
        {profile.kyc_status === "verified" && (
          <Link href={`/penyelenggara/${profile.slug}`} className="text-sm font-semibold underline decoration-hivis decoration-2 underline-offset-4">
            Lihat halaman publik
          </Link>
        )}
      </div>
    </form>
  );
}
