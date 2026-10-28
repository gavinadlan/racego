"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useState } from "react";
import { ApiError, safeNext, updateMe, type Me } from "@/lib/api";
import { useSession } from "./session";
import { btnGhost, btnPrimary, ErrorBox, inputCls, Loading, LoginHint, PageTitle } from "./ui";

const ROLE_LABEL: Record<string, string> = { owner: "Owner", admin: "Admin", finance: "Keuangan", rpc_staff: "Staf race pack" };

export function AccountView() {
  const params = useSearchParams();
  const isNew = params.get("baru") === "1";
  const next = safeNext(params.get("next"));
  const { ready, me } = useSession();

  if (!ready) return <Loading />;
  if (!me) return <LoginHint />;
  return (
    <div className="mx-auto max-w-xl">
      <PageTitle title={isNew ? "Lengkapi profil" : "Akun"}>
        {isNew && <p className="mt-3 text-muted">Akun kamu sudah dibuat. Isi nama sesuai KTP supaya data peserta terisi otomatis saat mendaftar race.</p>}
      </PageTitle>
      <ProfileForm key={me.id} me={me} isNew={isNew} next={next} />
      {!isNew && <AccountExtras me={me} />}
    </div>
  );
}

function ProfileForm({ me, isNew, next }: { me: Me; isNew: boolean; next: string }) {
  const router = useRouter();
  const { refresh } = useSession();
  const [name, setName] = useState(me.name);
  const [phone, setPhone] = useState(me.phone ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const updated = await updateMe({ name, phone });
      await refresh();
      if (isNew) {
        router.push(next);
        return;
      }
      setPhone(updated.phone ?? "");
      setSaved(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Gagal menyimpan profil");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-4 rounded-md bg-card p-6 ring-1 ring-line">
      <label className="block">
        <span className="mb-1 block text-sm font-semibold">Email</span>
        <input className={`${inputCls} text-muted`} value={me.email} readOnly aria-describedby="email-hint" />
        <span id="email-hint" className="mt-1 block text-xs text-muted">
          Email dipakai untuk masuk dan tidak bisa diubah.
        </span>
      </label>
      <label className="block">
        <span className="mb-1 block text-sm font-semibold">Nama lengkap</span>
        <input className={inputCls} required minLength={2} maxLength={100} autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} />
        <span className="mt-1 block text-xs text-muted">Sesuai KTP / paspor; dipakai mengisi data peserta.</span>
      </label>
      <label className="block">
        <span className="mb-1 block text-sm font-semibold">
          No. HP <span className="font-normal text-muted">(opsional)</span>
        </span>
        <input className={inputCls} type="tel" inputMode="tel" autoComplete="tel" placeholder="08xxxxxxxxxx" value={phone} onChange={(e) => setPhone(e.target.value)} />
      </label>
      {error && <ErrorBox message={error} />}
      <div className="flex flex-wrap items-center gap-4">
        <button type="submit" className={btnPrimary} disabled={busy}>
          {busy ? "Menyimpan…" : isNew ? "Simpan & lanjut" : "Simpan"}
        </button>
        {saved && (
          <p className="text-sm text-go" role="status">
            Profil tersimpan.
          </p>
        )}
      </div>
    </form>
  );
}

function AccountExtras({ me }: { me: Me }) {
  const router = useRouter();
  const { logout } = useSession();
  return (
    <>
      {me.orgs.length > 0 && (
        <section className="mt-10">
          <h2 className="font-display text-3xl leading-none font-extrabold">Peran mitra</h2>
          <ul className="mt-4 divide-y divide-line rounded-md bg-card ring-1 ring-line">
            {me.orgs.map((o) => (
              <li key={o.slug}>
                <Link href={`/org/${o.slug}`} className="flex items-center justify-between gap-4 px-5 py-3 hover:bg-paper">
                  <span className="font-semibold">{o.name}</span>
                  <span className="text-sm text-muted">{ROLE_LABEL[o.role] ?? o.role}</span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
      <button
        type="button"
        className={`${btnGhost} mt-10`}
        onClick={async () => {
          await logout();
          router.push("/");
        }}
      >
        Keluar
      </button>
    </>
  );
}
