"use client";

import Link from "next/link";
import { notFound, useParams, useRouter, useSearchParams } from "next/navigation";
import { useState } from "react";
import {
  adminDocUrl,
  ApiError,
  approveVerification,
  qs,
  rejectVerification,
  revealField,
  ROLE_LABEL,
  type AdminList,
  type AdminVerification,
  type DocKind,
} from "@/lib/api";
import { formatDateShort, formatDateTime } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { useSession } from "./session";
import { btnDark, btnGhost, ErrorBox, inputCls, Loading, PageTitle } from "./ui";
import { ConfirmDialog } from "./ui-client";
import { DocPreview, VerificationBadge } from "./verification";

/** Halaman admin: selain super admin melihat 404 yang sama dengan alamat yang tidak ada. */
function useSuperAdmin() {
  const { ready, me } = useSession();
  if (ready && !me?.is_super_admin) notFound();
  return ready;
}

const TABS = [
  { status: "submitted", label: "Menunggu" },
  { status: "approved", label: "Disetujui" },
  { status: "rejected", label: "Ditolak" },
] as const;

const typeLabel = (t: string | null, d: AdminVerification["requirements"] | null = null) =>
  d?.types.find((x) => x.type === t)?.label ?? ({ perorangan: "Perorangan", komunitas: "Komunitas", badan_usaha: "Badan usaha" } as Record<string, string>)[t ?? ""] ?? "–";

export function AdminQueue() {
  const ready = useSuperAdmin();
  const params = useSearchParams();
  const router = useRouter();
  const status = params.get("status") ?? "submitted";
  const q = params.get("q") ?? "";
  const page = Number(params.get("page") ?? 1);
  const [search, setSearch] = useState(q);
  const { data, error, loading } = useApi<AdminList>(ready ? `/v1/admin/verifications${qs({ status, q, page })}` : null);
  const href = (p: Record<string, string | number>) => `/admin${qs({ status, q, ...p })}`;

  if (!ready) return <Loading />;
  return (
    <>
      <PageTitle title="Verifikasi mitra">
        <p className="mt-2 text-sm text-muted">Tinjau data, dokumen, dan rekening penyelenggara sebelum mereka bisa menerbitkan event. Setiap akses dokumen dan data lengkap tercatat.</p>
      </PageTitle>
      <nav aria-label="Status pengajuan" className="mb-4 flex gap-1 overflow-x-auto border-b border-line">
        {TABS.map((t) => (
          <Link
            key={t.status}
            href={`/admin${qs({ status: t.status, q })}`}
            aria-current={t.status === status ? "page" : undefined}
            className={`-mb-px border-b-2 px-4 py-2.5 text-sm font-semibold whitespace-nowrap ${t.status === status ? "border-hivis text-asphalt" : "border-transparent text-muted hover:text-asphalt"}`}
          >
            {t.label} <span className="ml-1 rounded-full bg-line px-2 py-0.5 text-xs tabular-nums">{data?.counts[t.status] ?? "…"}</span>
          </Link>
        ))}
      </nav>
      <form
        role="search"
        className="mb-6 flex max-w-md gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          router.push(`/admin${qs({ status, q: search.trim() })}`);
        }}
      >
        <label className="sr-only" htmlFor="cari-org">
          Cari nama penyelenggara
        </label>
        <input id="cari-org" type="search" className={inputCls} placeholder="Cari nama penyelenggara" value={search} onChange={(e) => setSearch(e.target.value)} />
        <button type="submit" className={btnDark}>
          Cari
        </button>
      </form>
      {loading && !data ? (
        <Loading />
      ) : error ? (
        <ErrorBox message={error} />
      ) : data && data.items.length === 0 ? (
        <p className="rounded-md border border-dashed border-line bg-card p-6 text-sm text-muted">{q ? `Tidak ada hasil untuk "${q}".` : "Tidak ada pengajuan di sini."}</p>
      ) : (
        data && (
          <>
            <ul className="divide-y divide-line rounded-md border border-line bg-card">
              {data.items.map((it) => (
                <li key={it.id}>
                  <Link href={`/admin/verifikasi/${it.id}`} className="grid gap-1 px-4 py-4 hover:bg-paper sm:grid-cols-[1fr_auto] sm:items-center">
                    <span className="min-w-0">
                      <span className="block truncate font-semibold">{it.org_name}</span>
                      <span className="block truncate text-sm text-muted">
                        {typeLabel(it.org_type)} · {it.legal_name || "–"} · pengajuan ke-{it.attempt}
                      </span>
                    </span>
                    <span className="text-sm text-muted tabular-nums">
                      {status === "submitted" ? `Dikirim ${it.submitted_at ? formatDateTime(it.submitted_at) : "–"}` : `Ditinjau ${it.reviewed_at ? formatDateTime(it.reviewed_at) : "–"}`}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
            {data.total > data.page_size && (
              <div className="mt-4 flex items-center gap-3 text-sm">
                {page > 1 && (
                  <Link className={btnGhost} href={href({ page: page - 1 })}>
                    Sebelumnya
                  </Link>
                )}
                <span className="text-muted">
                  Halaman {page} dari {Math.ceil(data.total / data.page_size)}
                </span>
                {page * data.page_size < data.total && (
                  <Link className={btnGhost} href={href({ page: page + 1 })}>
                    Berikutnya
                  </Link>
                )}
              </div>
            )}
          </>
        )
      )}
    </>
  );
}

const CHECKLIST = [
  "Nama di KTP sama dengan nama penanggung jawab",
  "Foto/scan dokumen terbaca jelas dan tidak tampak diedit",
  "Nama badan usaha/komunitas sama di semua dokumen",
  "Nama pemilik rekening sesuai aturan jenis penyelenggara",
  "Tidak ada dokumen yang juga dipakai penyelenggara lain",
];

export function AdminDetail() {
  const ready = useSuperAdmin();
  const { id } = useParams<{ id: string }>();
  const { data, error, loading } = useApi<AdminVerification>(ready ? `/v1/admin/verifications/${id}` : null);
  const [updated, setUpdated] = useState<AdminVerification | null>(null);
  const [revealed, setRevealed] = useState<Record<string, string>>({});
  const [open, setOpen] = useState<DocKind | null>(null);
  const [dialog, setDialog] = useState<null | "approve" | "reject">(null);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);

  if (!ready || (loading && !data)) return <Loading />;
  if (error && !data) return <ErrorBox message={error} />;
  const d = updated ?? data;
  if (!d) return null;
  const v = d.verification;
  const req = d.requirements;

  async function reveal(field: "pic_id" | "account" | "npwp") {
    setMsg(null);
    try {
      const r = await revealField(v.id, field);
      setRevealed((s) => ({ ...s, [field]: r.value }));
    } catch (err) {
      setMsg(err instanceof ApiError ? err.message : "Gagal menampilkan data");
    }
  }

  async function act(kind: "approve" | "reject") {
    setBusy(true);
    setDialogError(null);
    try {
      setUpdated(kind === "approve" ? await approveVerification(v.id) : await rejectVerification(v.id, reason.trim()));
      setDialog(null);
      setMsg(kind === "approve" ? "Pengajuan disetujui. Penyelenggara sudah bisa menerbitkan event." : "Pengajuan ditolak. Owner menerima email berisi alasannya.");
    } catch (err) {
      setDialogError(err instanceof ApiError ? (err.fields ? Object.values(err.fields).join(" ") : err.message) : "Gagal memproses");
    } finally {
      setBusy(false);
    }
  }

  const sensitive = (field: "pic_id" | "account" | "npwp", masked: string) =>
    masked ? (
      <span className="flex flex-wrap items-center gap-2">
        <span className="font-mono">{revealed[field] ?? masked}</span>
        {!revealed[field] && (
          <button type="button" className="text-xs font-semibold underline underline-offset-2" onClick={() => reveal(field)}>
            Tampilkan<span className="sr-only"> {field === "pic_id" ? "NIK" : field === "account" ? "nomor rekening" : "NPWP"} lengkap</span>
          </button>
        )}
      </span>
    ) : (
      "–"
    );
  const rule = req.types.find((t) => t.type === v.org_type);
  const reasonLen = reason.trim().length;

  return (
    <>
      <PageTitle title={d.org.name} back={{ href: "/admin", label: "Antrean verifikasi" }}>
        <p className="mt-2 flex flex-wrap items-center gap-2 text-sm text-muted">
          <VerificationBadge status={v.status} />
          <span>
            {typeLabel(v.org_type, req)} · pengajuan ke-{v.attempt} · dikirim {v.submitted_at ? formatDateTime(v.submitted_at) : "–"}
          </span>
        </p>
      </PageTitle>
      {d.conflict_of_interest && (
        <div className="mb-6 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-900" role="status">
          <b>Benturan kepentingan.</b> Kamu anggota penyelenggara ini, jadi tidak bisa menyetujui atau menolak pengajuannya. Minta super admin lain untuk meninjau.
        </div>
      )}
      {msg && (
        <p className="mb-6 rounded-md bg-go-soft p-4 text-sm text-go" role="status">
          {msg}
        </p>
      )}
      <div className="grid gap-8 lg:grid-cols-[1fr_320px]">
        <div className="min-w-0 space-y-8">
          <section aria-labelledby="data" className="rounded-md bg-card p-5 ring-1 ring-line sm:p-6">
            <h2 id="data" className="mb-4 text-lg font-semibold">
              Data pengajuan
            </h2>
            <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-[11rem_1fr]">
              {(
                [
                  ["Nama sesuai dokumen", v.legal_name],
                  ["Alamat", v.address],
                  ["Penanggung jawab", v.pic_name],
                  ["No. HP", v.pic_phone],
                  ["NIK", sensitive("pic_id", v.pic_id)],
                  ["NPWP", sensitive("npwp", v.npwp)],
                  ["Bank", v.bank_name],
                  ["Nomor rekening", sensitive("account", v.account)],
                  ["Pemilik rekening", v.account_holder],
                ] as [string, React.ReactNode][]
              ).map(([k, val]) => (
                <div key={k} className="contents">
                  <dt className="text-muted">{k}</dt>
                  <dd className="min-w-0 font-medium break-words">{val || "–"}</dd>
                </div>
              ))}
            </dl>
            <p className="mt-4 text-xs text-muted">Menekan &quot;Tampilkan&quot; mencatat aksesmu (nama, waktu, IP) di log audit.</p>
            <div
              className={`mt-5 rounded-md border p-4 text-sm ${d.name_match.ok ? "border-go/30 bg-go-soft text-go" : "border-amber-300 bg-amber-50 text-amber-900"}`}
              role="note"
            >
              <p className="font-semibold">{d.name_match.ok ? "✓ Nama pemilik rekening cocok" : "! Nama pemilik rekening perlu dicek"}</p>
              <p className="mt-1 text-asphalt">
                Pemilik: <b>{d.name_match.holder || "–"}</b>. Dibandingkan dengan: {d.name_match.expected.filter(Boolean).join(" / ") || "–"}.
              </p>
              <p className="mt-1 text-xs text-asphalt/80">
                {d.name_match.rule} Petunjuk otomatis (abaikan huruf besar/kecil, tanda baca, gelar umum), bukan penentu; putuskan dari dokumen.
              </p>
            </div>
          </section>

          <section aria-labelledby="dokumen">
            <h2 id="dokumen" className="mb-3 text-lg font-semibold">
              Dokumen
            </h2>
            <ul className="divide-y divide-line rounded-md border border-line bg-card text-sm">
              {(rule ? [...rule.required, ...rule.optional] : d.documents.map((x) => x.kind)).map((k) => {
                const doc = d.documents.find((x) => x.kind === k);
                return (
                  <li key={k} className="p-4">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span className="min-w-0">
                        <b>{req.documents[k]}</b> {rule && !rule.required.includes(k) && <span className="text-muted">(opsional)</span>}
                        {doc && <span className="block truncate text-xs text-muted">{doc.name}</span>}
                      </span>
                      {doc ? (
                        <button type="button" className={`${btnGhost} py-1`} aria-expanded={open === k} onClick={() => setOpen(open === k ? null : k)}>
                          {open === k ? "Tutup" : "Pratinjau"}
                          <span className="sr-only"> {req.documents[k]}</span>
                        </button>
                      ) : (
                        <span className="text-xs text-muted">Tidak diunggah</span>
                      )}
                    </div>
                    {doc?.duplicate_orgs && doc.duplicate_orgs.length > 0 && (
                      <p className="mt-2 rounded-sm bg-amber-50 p-2 text-xs text-amber-900">Berkas identik juga diunggah oleh: {doc.duplicate_orgs.join(", ")}.</p>
                    )}
                    {open === k && doc && <DocPreview url={adminDocUrl(v.id, k)} contentType={doc.content_type} title={req.documents[k]} />}
                  </li>
                );
              })}
            </ul>
          </section>
        </div>

        <aside className="space-y-6">
          {v.status === "submitted" ? (
            <section aria-labelledby="periksa" className="rounded-md bg-card p-5 ring-1 ring-line">
              <h2 id="periksa" className="text-lg font-semibold">
                Daftar periksa
              </h2>
              <p className="mt-1 text-xs text-muted">Bantuan saja, tidak disimpan.</p>
              <ul className="mt-3 space-y-2 text-sm">
                {CHECKLIST.map((c) => (
                  <li key={c}>
                    <label className="flex gap-2">
                      <input type="checkbox" className="mt-0.5 size-4 shrink-0 accent-hivis" />
                      {c}
                    </label>
                  </li>
                ))}
              </ul>
              <div className="mt-5 flex flex-wrap gap-2">
                <button type="button" className={btnDark} disabled={d.conflict_of_interest} onClick={() => (setDialogError(null), setDialog("approve"))}>
                  Setujui
                </button>
                <button type="button" className={`${btnGhost} text-red-700`} disabled={d.conflict_of_interest} onClick={() => (setDialogError(null), setDialog("reject"))}>
                  Tolak
                </button>
              </div>
            </section>
          ) : (
            <section className="rounded-md bg-card p-5 text-sm ring-1 ring-line">
              <p>
                {v.status === "approved" ? "Disetujui" : "Ditolak"} {d.reviewer ? `oleh ${d.reviewer}` : ""} {v.reviewed_at ? formatDateTime(v.reviewed_at) : ""}.
              </p>
              {v.reject_reason && <p className="mt-2 whitespace-pre-line text-muted">Alasan: {v.reject_reason}</p>}
            </section>
          )}

          <section aria-labelledby="anggota" className="rounded-md bg-card p-5 text-sm ring-1 ring-line">
            <h2 id="anggota" className="text-lg font-semibold">
              Anggota penyelenggara
            </h2>
            <ul className="mt-3 space-y-2">
              {d.members.map((m) => (
                <li key={m.email} className="min-w-0">
                  <span className="block truncate font-medium">
                    {m.name} <span className="text-xs font-normal text-muted">· {ROLE_LABEL[m.role]}</span>
                  </span>
                  <span className="block truncate text-xs text-muted">{m.email}</span>
                </li>
              ))}
            </ul>
            <p className="mt-3 text-xs text-muted">
              {d.org.city} · terdaftar {formatDateShort(d.org.created_at)} · /{d.org.slug}
            </p>
          </section>

          <section aria-labelledby="riwayat" className="rounded-md bg-card p-5 text-sm ring-1 ring-line">
            <h2 id="riwayat" className="text-lg font-semibold">
              Riwayat pengajuan
            </h2>
            <ol className="mt-3 space-y-3">
              {d.history.map((h) => (
                <li key={h.id}>
                  <span className="flex flex-wrap items-center gap-2">
                    {h.id === v.id ? <b>Ke-{h.attempt} (ini)</b> : <Link href={`/admin/verifikasi/${h.id}`} className="underline underline-offset-2">Ke-{h.attempt}</Link>}
                    <VerificationBadge status={h.status} />
                  </span>
                  {h.reject_reason && <span className="mt-1 block whitespace-pre-line text-xs text-muted">{h.reject_reason}</span>}
                </li>
              ))}
            </ol>
          </section>
        </aside>
      </div>

      <ConfirmDialog
        open={dialog === "approve"}
        title="Setujui pengajuan?"
        confirmLabel="Setujui"
        busy={busy}
        onClose={() => setDialog(null)}
        onConfirm={() => act("approve")}
      >
        <p>
          <b>{d.org.name}</b> akan terverifikasi dan langsung bisa menerbitkan event. Owner menerima email pemberitahuan.
        </p>
        {dialogError && <ErrorBox message={dialogError} />}
      </ConfirmDialog>

      <ConfirmDialog
        open={dialog === "reject"}
        title="Tolak pengajuan?"
        confirmLabel="Tolak pengajuan"
        danger
        busy={busy}
        disabled={reasonLen < 10 || reasonLen > 1000}
        onClose={() => setDialog(null)}
        onConfirm={() => act("reject")}
      >
        <p>Alasan dikirim ke owner lewat email dan tampil di dashboard mereka. Tulis apa yang perlu diperbaiki; jangan menulis NIK atau nomor rekening.</p>
        <label className="block">
          <span className="mb-1 block font-semibold">
            Alasan penolakan<span className="ml-0.5 text-hivis" aria-hidden>*</span>
          </span>
          <textarea
            className={`${inputCls} min-h-32`}
            maxLength={1000}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            aria-describedby="hitung-alasan"
          />
          <span id="hitung-alasan" className={`mt-1 block text-xs tabular-nums ${reasonLen > 0 && reasonLen < 10 ? "text-red-700" : "text-muted"}`}>
            {reasonLen}/1.000 karakter{reasonLen < 10 ? ` · minimal 10` : ""}
          </span>
        </label>
        {dialogError && <ErrorBox message={dialogError} />}
      </ConfirmDialog>
    </>
  );
}
