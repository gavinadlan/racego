"use client";

import { useParams } from "next/navigation";
import { useState } from "react";
import { addMember, ApiError, removeMember, ROLE_LABEL, updateMember, type Member, type Role } from "@/lib/api";
import { useApi } from "@/lib/use-api";
import { OrgShell } from "./org-shell";
import { useSession } from "./session";
import { btnDark, btnGhost, ErrorBox, Field, inputCls, inputErr, Loading } from "./ui";
import { ConfirmDialog } from "./ui-client";

const INVITE_ROLES: { role: Role; hint: string }[] = [
  { role: "admin", hint: "buat & ubah event, profil" },
  { role: "finance", hint: "lihat penjualan" },
  { role: "rpc_staff", hint: "scan race pack" },
];

export function OrgTeam() {
  return (
    <OrgShell tab="team">
      {(profile) => (profile.role === "owner" ? <Team /> : <p className="text-sm text-muted">Hanya owner yang bisa mengelola anggota tim.</p>)}
    </OrgShell>
  );
}

function Team() {
  const { org } = useParams<{ org: string }>();
  const { me } = useSession();
  const { data, error, loading } = useApi<{ members: Member[] }>(`/v1/org/${org}/members`);
  // Daftar terbaru dari respons aksi terakhir; null = pakai hasil GET.
  const [members, setMembers] = useState<Member[] | null>(null);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("rpc_staff");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState<{ error?: string; ok?: string }>({});
  const [busy, setBusy] = useState(false);
  const [removing, setRemoving] = useState<Member | null>(null);

  const list = members ?? data?.members ?? null;
  if (loading && !list) return <Loading />;
  if (error && !list) return <ErrorBox message={error} />;
  if (!list) return null;

  async function run(action: () => Promise<{ members: Member[] }>, ok: string) {
    setBusy(true);
    setMsg({});
    setFields({});
    try {
      setMembers((await action()).members);
      setMsg({ ok });
      return true;
    } catch (err) {
      if (err instanceof ApiError && err.fields) setFields(err.fields);
      setMsg({ error: err instanceof ApiError ? err.message : "Gagal menyimpan" });
      return false;
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid gap-10 lg:grid-cols-[1fr_340px]">
      <section aria-labelledby="anggota">
        <h2 id="anggota" className="font-display text-3xl leading-none font-extrabold">
          Anggota
        </h2>
        <ul className="mt-4 divide-y divide-line rounded-md border border-line bg-card">
          {list.map((m) => {
            const self = m.user_id === me?.id;
            return (
              <li key={m.user_id} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
                <div className="min-w-0 flex-1 basis-48">
                  <p className="truncate font-semibold">
                    {m.name} {self && <span className="text-xs font-normal text-muted">(kamu)</span>}
                  </p>
                  <p className="truncate text-sm text-muted">{m.email}</p>
                </div>
                {self ? (
                  <span className="text-sm font-semibold">{ROLE_LABEL[m.role]}</span>
                ) : (
                  <div className="flex items-center gap-2">
                    <select
                      className={`${inputCls} w-auto py-1.5`}
                      value={m.role}
                      disabled={busy}
                      aria-label={`Peran ${m.name}`}
                      onChange={(e) => run(() => updateMember(org, m.user_id, e.target.value as Role), `Peran ${m.name} diperbarui.`)}
                    >
                      {(["owner", "admin", "finance", "rpc_staff"] as Role[]).map((r) => (
                        <option key={r} value={r}>
                          {ROLE_LABEL[r]}
                        </option>
                      ))}
                    </select>
                    <button type="button" className={`${btnGhost} py-1.5`} disabled={busy} onClick={() => setRemoving(m)}>
                      Hapus
                    </button>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
        <div className="mt-3 min-h-6 text-sm" aria-live="polite">
          {msg.ok && <p className="text-go">{msg.ok}</p>}
        </div>
      </section>

      <section aria-labelledby="tambah" className="self-start rounded-md bg-card p-6 ring-1 ring-line">
        <h2 id="tambah" className="text-lg font-semibold">
          Tambah anggota
        </h2>
        <p className="mt-1 text-sm text-muted">Orangnya harus sudah pernah masuk ke RaceGo dengan email itu.</p>
        <form
          className="mt-4 space-y-4"
          onSubmit={async (e) => {
            e.preventDefault();
            if (await run(() => addMember(org, { email, role }), `${email} ditambahkan sebagai ${ROLE_LABEL[role]}.`)) setEmail("");
          }}
        >
          <Field label="Email" required error={fields.email}>
            <input className={inputErr(fields.email)} type="email" required autoComplete="off" value={email} onChange={(e) => setEmail(e.target.value)} />
          </Field>
          <Field label="Peran" required error={fields.role}>
            <select className={inputErr(fields.role)} value={role} onChange={(e) => setRole(e.target.value as Role)}>
              {INVITE_ROLES.map((r) => (
                <option key={r.role} value={r.role}>
                  {ROLE_LABEL[r.role]}: {r.hint}
                </option>
              ))}
            </select>
          </Field>
          {msg.error && <ErrorBox message={msg.error} />}
          <button type="submit" className={`${btnDark} w-full`} disabled={busy || !email}>
            {busy ? "Menyimpan…" : "Tambah ke tim"}
          </button>
        </form>
      </section>

      <ConfirmDialog
        open={removing !== null}
        title="Hapus anggota?"
        confirmLabel="Hapus dari tim"
        danger
        busy={busy}
        onClose={() => setRemoving(null)}
        onConfirm={async () => {
          if (removing && (await run(() => removeMember(org, removing.user_id), `${removing.name} dihapus dari tim.`))) setRemoving(null);
        }}
      >
        <p>
          <b>{removing?.name}</b> ({removing?.email}) tidak bisa lagi membuka dashboard ini. Kamu bisa menambahkannya lagi nanti.
        </p>
      </ConfirmDialog>
    </div>
  );
}
