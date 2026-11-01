"use client";

import { useParams } from "next/navigation";
import { useState } from "react";
import {
  ApiError,
  createInvitation,
  removeMember,
  resendInvitation,
  revokeInvitation,
  ROLE_LABEL,
  updateMember,
  type Invitation,
  type InvitationList,
  type Member,
  type Role,
} from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { OrgShell } from "./org-shell";
import { useSession } from "./session";
import { btnDark, btnGhost, ErrorBox, Field, inputCls, inputErr, Loading } from "./ui";
import { ConfirmDialog } from "./ui-client";

const ROLE_HINT: Partial<Record<Role, string>> = {
  admin: "buat & ubah event, profil, undang staf",
  finance: "lihat penjualan, ajukan pencairan",
  rpc_staff: "scan race pack",
};

export function OrgTeam() {
  return (
    <OrgShell tab="team">
      {(profile) =>
        profile.role === "owner" || profile.role === "admin" ? (
          <Team owner={profile.role === "owner"} />
        ) : (
          <p className="text-sm text-muted">Hanya owner dan admin yang bisa mengelola tim.</p>
        )
      }
    </OrgShell>
  );
}

function Team({ owner }: { owner: boolean }) {
  const { org } = useParams<{ org: string }>();
  return (
    <Invites org={org}>
      {owner ? (
        <Members org={org} />
      ) : (
        <p className="rounded-md bg-card p-4 text-sm text-muted ring-1 ring-line">
          Sebagai admin, kamu bisa mengundang staf race pack. Daftar anggota dan perubahan peran dikelola owner.
        </p>
      )}
    </Invites>
  );
}

function errorText(err: unknown, fallback: string) {
  return err instanceof ApiError ? err.message : fallback;
}

function Members({ org }: { org: string }) {
  const { me } = useSession();
  const { data, error, loading } = useApi<{ members: Member[] }>(`/v1/org/${org}/members`);
  // Daftar terbaru dari respons aksi terakhir; null = pakai hasil GET.
  const [members, setMembers] = useState<Member[] | null>(null);
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
    try {
      setMembers((await action()).members);
      setMsg({ ok });
      return true;
    } catch (err) {
      setMsg({ error: errorText(err, "Gagal menyimpan") });
      return false;
    } finally {
      setBusy(false);
    }
  }

  return (
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
        {msg.error && <ErrorBox message={msg.error} />}
      </div>

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
          <b>{removing?.name}</b> ({removing?.email}) tidak bisa lagi membuka dashboard ini. Kamu bisa mengundangnya lagi nanti.
        </p>
      </ConfirmDialog>
    </section>
  );
}

/** Kolom kiri: children (anggota) + undangan tertunda; kolom kanan: formulir undangan. */
function Invites({ org, children }: { org: string; children: React.ReactNode }) {
  const { data, error, loading } = useApi<InvitationList>(`/v1/org/${org}/invitations`);
  const [latest, setLatest] = useState<InvitationList | null>(null);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("rpc_staff");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState<{ error?: string; ok?: string }>({});
  const [busy, setBusy] = useState(false);
  const [revoking, setRevoking] = useState<Invitation | null>(null);
  const [dialogError, setDialogError] = useState<string | null>(null);

  const state = latest ?? data ?? null;
  if (loading && !state) return <Loading />;
  if (error && !state) return <ErrorBox message={error} />;
  if (!state) return null;
  const roles = state.invitable_roles;
  const chosen = roles.includes(role) ? role : roles[0];

  async function run(action: () => Promise<InvitationList>, ok: string) {
    setBusy(true);
    setMsg({});
    setFields({});
    try {
      setLatest(await action());
      setMsg({ ok });
      return true;
    } catch (err) {
      if (err instanceof ApiError && err.fields) setFields(err.fields);
      setMsg({
        error: err instanceof ApiError && err.fields ? undefined : errorText(err, "Gagal menyimpan"),
      });
      return false;
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid gap-10 lg:grid-cols-[1fr_340px]">
      <div className="min-w-0 space-y-10">
        {children}
        <section aria-labelledby="undangan">
          <h2 id="undangan" className="font-display text-3xl leading-none font-extrabold">
            Undangan tertunda
          </h2>
          <div className="mt-3 min-h-6 text-sm" aria-live="polite">
            {msg.ok && <p className="text-go">{msg.ok}</p>}
            {msg.error && <ErrorBox message={msg.error} />}
          </div>
          {state.invitations.length === 0 ? (
            <p className="mt-2 rounded-md border border-dashed border-line bg-card p-5 text-sm text-muted">Belum ada undangan yang menunggu.</p>
          ) : (
            <ul className="mt-2 divide-y divide-line rounded-md border border-line bg-card">
              {state.invitations.map((i) => {
                // Jeda 5 menit ditegakkan server (429 dengan pesan); di sini cukup batas total.
                const canResend = i.can_manage && i.send_count < state.max_sends;
                return (
                  <li key={i.id} className="grid gap-3 px-4 py-3 sm:grid-cols-[1fr_auto] sm:items-center">
                    <div className="min-w-0 text-sm">
                      <p className="flex flex-wrap items-center gap-2">
                        <b className="break-all">{i.email}</b>
                        <span className="rounded-full bg-paper px-2.5 py-0.5 text-xs font-semibold ring-1 ring-line">{ROLE_LABEL[i.role]}</span>
                        {i.status === "expired" ? (
                          <span className="rounded-full bg-red-100 px-2.5 py-0.5 text-xs font-semibold text-red-800">Kedaluwarsa</span>
                        ) : (
                          <span className="rounded-full bg-amber-100 px-2.5 py-0.5 text-xs font-semibold text-amber-900">Menunggu</span>
                        )}
                      </p>
                      <p className="mt-1 text-muted">
                        Dikirim {formatDateTime(i.last_sent_at)} oleh {i.invited_by} · {i.send_count}× · {i.status === "expired" ? "kedaluwarsa" : "berlaku sampai"}{" "}
                        {formatDateTime(i.expires_at)}
                      </p>
                    </div>
                    {i.can_manage && (
                      <div className="flex flex-wrap gap-2">
                        <button
                          type="button"
                          className={`${btnGhost} min-h-11`}
                          disabled={busy || !canResend}
                          title={i.send_count >= state.max_sends ? "Batas kirim ulang tercapai" : undefined}
                          onClick={() => run(() => resendInvitation(org, i.id), `Undangan dikirim ulang ke ${i.email}. Tautan lama tidak berlaku lagi.`)}
                        >
                          Kirim ulang
                          <span className="sr-only"> ke {i.email}</span>
                        </button>
                        <button type="button" className={`${btnGhost} min-h-11 text-red-700`} disabled={busy} onClick={() => (setDialogError(null), setRevoking(i))}>
                          Cabut
                          <span className="sr-only"> undangan {i.email}</span>
                        </button>
                      </div>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
          <p className="mt-2 text-xs text-muted">
            Tautan berlaku 7 hari dan sekali pakai. Kirim ulang membuat tautan baru (paling cepat 5 menit sekali, maksimal {state.max_sends}× per undangan).
          </p>
        </section>
      </div>

      <section aria-labelledby="undang" className="self-start rounded-md bg-card p-6 ring-1 ring-line">
        <h2 id="undang" className="text-lg font-semibold">
          Undang anggota
        </h2>
        <p className="mt-1 text-sm text-muted">Kami kirim tautan ke email itu. Penerima masuk dengan email yang sama untuk bergabung, akun dibuat otomatis bila belum ada.</p>
        <form
          className="mt-4 space-y-4"
          noValidate
          onSubmit={async (e) => {
            e.preventDefault();
            const to = email.trim();
            if (await run(() => createInvitation(org, { email: to, role: chosen }), `Undangan terkirim ke ${to} sebagai ${ROLE_LABEL[chosen]}.`)) setEmail("");
          }}
        >
          <Field label="Email" required error={fields.email}>
            <input className={inputErr(fields.email)} type="email" required autoComplete="off" inputMode="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          </Field>
          <Field label="Peran" required error={fields.role}>
            <select className={inputErr(fields.role)} value={chosen} onChange={(e) => setRole(e.target.value as Role)}>
              {roles.map((r) => (
                <option key={r} value={r}>
                  {ROLE_LABEL[r]}: {ROLE_HINT[r]}
                </option>
              ))}
            </select>
          </Field>
          <button type="submit" className={`${btnDark} min-h-11 w-full`} disabled={busy || !email.trim()}>
            {busy ? "Mengirim…" : "Kirim undangan"}
          </button>
        </form>
      </section>

      <ConfirmDialog
        open={revoking !== null}
        title="Cabut undangan?"
        confirmLabel="Cabut undangan"
        danger
        busy={busy}
        onClose={() => setRevoking(null)}
        onConfirm={async () => {
          if (!revoking) return;
          setBusy(true);
          setDialogError(null);
          try {
            setLatest(await revokeInvitation(org, revoking.id));
            setMsg({ ok: `Undangan untuk ${revoking.email} dicabut.` });
            setRevoking(null);
          } catch (err) {
            setDialogError(errorText(err, "Gagal mencabut"));
          } finally {
            setBusy(false);
          }
        }}
      >
        <p>
          Tautan yang sudah dikirim ke <b className="break-all">{revoking?.email}</b> langsung tidak berlaku. Kamu bisa mengundang lagi nanti.
        </p>
        {dialogError && <ErrorBox message={dialogError} />}
      </ConfirmDialog>
    </div>
  );
}
