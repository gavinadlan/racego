"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useState } from "react";
import { acceptInvitation, ApiError, ROLE_LABEL, type InvitationView, type Role } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { LoginLink } from "./login-link";
import { useSession } from "./session";
import { btnDark, btnGhost, btnPrimary, ErrorBox, Loading, PageTitle } from "./ui";

/** /undangan/[token]: lihat undangan, masuk bila perlu, lalu terima. */
export function InvitationPage() {
  const { token } = useParams<{ token: string }>();
  const { ready, me, refresh, logout } = useSession();
  const { data, error, loading } = useApi<InvitationView>(ready ? `/v1/invitations/${encodeURIComponent(token)}` : null, [me?.id ?? null]);
  const [busy, setBusy] = useState(false);
  const [acceptError, setAcceptError] = useState<string | null>(null);
  const [joined, setJoined] = useState<{ org_slug: string; org_name: string; role: Role; already_member: boolean } | null>(null);

  if (joined) {
    return (
      <Card title="Selamat bergabung">
        <p role="status">
          {joined.already_member ? "Kamu sudah menjadi anggota" : "Kamu sekarang anggota"} tim <b>{joined.org_name}</b>
          {joined.already_member ? " (peranmu tidak berubah)." : <> sebagai <b>{ROLE_LABEL[joined.role]}</b>.</>}
        </p>
        <div className="mt-6 flex flex-wrap gap-3">
          <Link href={`/org/${joined.org_slug}`} className={btnPrimary}>
            Buka dashboard
          </Link>
        </div>
        {joined.role === "rpc_staff" && <p className="mt-4 text-sm text-muted">Scanner race pack ada di tiap event pada dashboard (tombol Scan race pack).</p>}
      </Card>
    );
  }
  if (!ready || (loading && !data && !error)) return <Loading />;
  if (error && !data) {
    return (
      <Card title="Undangan tidak berlaku">
        <ErrorBox message={error} />
        <p className="mt-4 text-sm text-muted">Hubungi owner atau admin penyelenggara untuk meminta undangan baru.</p>
      </Card>
    );
  }
  if (!data) return null;

  async function accept() {
    setBusy(true);
    setAcceptError(null);
    try {
      const res = await acceptInvitation(token);
      await refresh(); // peran baru ikut di menu & dashboard
      setJoined(res);
    } catch (err) {
      setAcceptError(err instanceof ApiError ? err.message : "Gagal menerima undangan");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Undangan tim">
      <p>
        <b>{data.invited_by}</b> mengundangmu bergabung ke tim <b>{data.org_name}</b> sebagai <b>{ROLE_LABEL[data.role]}</b>.
      </p>
      <dl className="mt-4 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <dt className="text-muted">Untuk email</dt>
        <dd className="font-semibold break-all">{data.email_masked}</dd>
        <dt className="text-muted">Berlaku sampai</dt>
        <dd>{formatDateTime(data.expires_at)}</dd>
      </dl>
      <div className="mt-6 space-y-3">
        {!me ? (
          <>
            <p className="text-sm">Masuk dulu dengan email <b>{data.email_masked}</b>. Belum punya akun? Akun dibuat otomatis saat masuk.</p>
            <LoginLink className={`${btnPrimary} min-h-11 w-full sm:w-auto`}>Masuk untuk menerima</LoginLink>
          </>
        ) : data.email_matches === false ? (
          <>
            <ErrorBox message={`Undangan ini untuk ${data.email_masked}, sedangkan kamu masuk sebagai ${me.email}. Keluar lalu masuk dengan email undangan.`} />
            <button type="button" className={`${btnGhost} min-h-11`} onClick={() => void logout()}>
              Keluar
            </button>
          </>
        ) : (
          <>
            {acceptError && <ErrorBox message={acceptError} />}
            <button type="button" className={`${btnDark} min-h-11 w-full sm:w-auto`} disabled={busy} onClick={accept}>
              {busy ? "Memproses…" : "Terima undangan"}
            </button>
          </>
        )}
      </div>
    </Card>
  );
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mx-auto max-w-lg">
      <PageTitle title={title} />
      <div className="rounded-md bg-card p-6 ring-1 ring-line">{children}</div>
    </div>
  );
}
