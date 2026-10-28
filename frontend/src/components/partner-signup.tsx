"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { ApiError, checkOrgSlug, createOrg, type OrgInput } from "@/lib/api";
import { slugify } from "@/lib/format";
import { useSession } from "./session";
import { btnPrimary, ErrorBox, Field, inputErr, Loading, PageTitle } from "./ui";

type SlugState = { slug: string; available: boolean; message?: string } | null;

export function PartnerSignup() {
  const router = useRouter();
  const { ready, me, refresh } = useSession();
  const [form, setForm] = useState<Required<OrgInput>>({ name: "", slug: "", city: "", description: "", instagram: "", website: "" });
  const [slugEdited, setSlugEdited] = useState(false);
  const [slugState, setSlugState] = useState<SlugState>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (ready && !me) router.replace(`/masuk?next=${encodeURIComponent("/daftar-mitra")}`);
  }, [ready, me, router]);

  const slug = slugEdited ? form.slug : slugify(form.name);

  // Cek ketersediaan alamat 400 ms setelah berhenti mengetik.
  useEffect(() => {
    if (slug.length < 3) return;
    let cancelled = false;
    const t = setTimeout(() => {
      checkOrgSlug(slug)
        .then((r) => !cancelled && setSlugState({ slug, ...r }))
        .catch(() => !cancelled && setSlugState(null));
    }, 400);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [slug]);

  if (!ready || !me) return <Loading />;

  const set = (k: keyof OrgInput) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => setForm((f) => ({ ...f, [k]: e.target.value }));
  const current = slugState?.slug === slug ? slugState : null;
  const slugMsg = fields.slug ?? (slug.length > 0 && slug.length < 3 ? "Alamat minimal 3 karakter" : current && !current.available ? current.message : undefined);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setFields({});
    try {
      // Alamat yang tidak diubah manual boleh diberi akhiran -2 dst. oleh server bila bentrok.
      const res = await createOrg({ ...form, slug: slugEdited ? form.slug : "" });
      await refresh(); // menu header langsung menampilkan dashboard baru
      router.push(`/org/${res.slug}?baru=1`);
    } catch (err) {
      if (err instanceof ApiError) {
        setFields(err.fields ?? {});
        setError(err.fields ? "Periksa kembali isian yang ditandai." : err.message);
      } else setError("Gagal mendaftarkan penyelenggara");
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto max-w-2xl">
      <PageTitle title="Daftarkan penyelenggara" back={{ href: "/untuk-penyelenggara", label: "Untuk penyelenggara" }}>
        <p className="mt-3 text-muted">
          Komunitas lari, EO, kampus, atau perusahaan. Setelah terdaftar kamu jadi <b className="text-asphalt">owner</b> dan bisa langsung menyiapkan event.
        </p>
      </PageTitle>
      <form onSubmit={submit} className="space-y-5 rounded-md bg-card p-6 ring-1 ring-line" noValidate>
        <Field label="Nama penyelenggara" required hint="Nama yang dilihat pelari, mis. Lari Pagi Bogor" error={fields.name}>
          <input className={inputErr(fields.name)} required minLength={3} maxLength={80} value={form.name} onChange={set("name")} autoComplete="organization" />
        </Field>
        <Field
          label="Alamat halaman"
          required
          hint={current?.available ? "Tersedia. Alamat tidak bisa diubah setelah terdaftar." : "Huruf kecil, angka, dan strip. Tidak bisa diubah setelah terdaftar."}
          error={slugMsg}
        >
          <div className={`flex items-center overflow-hidden rounded-md border bg-card text-sm focus-within:border-asphalt ${slugMsg ? "border-red-500" : "border-line"}`}>
            <span className="shrink-0 pl-3 text-muted">/penyelenggara/</span>
            <input
              className="min-w-0 flex-1 bg-transparent py-2.5 pr-3 focus:outline-none"
              value={slug}
              maxLength={40}
              onChange={(e) => {
                setSlugEdited(true);
                setForm((f) => ({ ...f, slug: e.target.value.toLowerCase() }));
              }}
              aria-invalid={!!slugMsg}
            />
            {current?.available && (
              <span className="shrink-0 pr-3 text-xs font-semibold text-go" aria-hidden>
                ✓ tersedia
              </span>
            )}
          </div>
        </Field>
        <Field label="Kota" required hint="Kota basis penyelenggara" error={fields.city}>
          <input className={inputErr(fields.city)} required maxLength={60} value={form.city} onChange={set("city")} autoComplete="address-level2" />
        </Field>
        <Field label="Deskripsi singkat" hint="Ceritakan komunitasmu dalam 1–3 kalimat. Tampil di halaman penyelenggara." error={fields.description}>
          <textarea className={`${inputErr(fields.description)} min-h-24`} maxLength={1000} value={form.description} onChange={set("description")} />
        </Field>
        <div className="grid gap-5 sm:grid-cols-2">
          <Field label="Instagram" hint="Nama akun saja, tanpa @" error={fields.instagram}>
            <input className={inputErr(fields.instagram)} maxLength={30} placeholder="larikita.id" value={form.instagram} onChange={set("instagram")} />
          </Field>
          <Field label="Situs web" hint="Diawali https://" error={fields.website}>
            <input className={inputErr(fields.website)} type="url" inputMode="url" placeholder="https://" value={form.website} onChange={set("website")} />
          </Field>
        </div>
        {error && <ErrorBox message={error} />}
        <button type="submit" className={`${btnPrimary} w-full sm:w-auto`} disabled={busy || !form.name || !form.city}>
          {busy ? "Mendaftarkan…" : "Daftarkan penyelenggara"}
        </button>
      </form>
    </div>
  );
}
