"use client";

import Link from "next/link";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { useState } from "react";
import {
  ApiError,
  approvePayout,
  createRefund,
  qs,
  refundAction,
  rejectPayout,
  revealPayoutAccount,
  type AdminPayoutDetail,
  type AdminPayoutList,
  type AdminRefundList,
  type FinanceOverview,
  type LedgerCheck,
  type Refund,
} from "@/lib/api";
import { formatDateTime, formatRupiah } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { AdminNav, useSuperAdmin } from "./admin";
import { MoneyBadge, maskedAccount } from "./finance";
import { btnDark, btnGhost, ErrorBox, Field, inputCls, inputErr, Loading, PageTitle } from "./ui";
import { ConfirmDialog } from "./ui-client";

export function MockBanner({ mock }: { mock?: boolean }) {
  if (!mock) return null;
  return (
    <div className="mb-6 rounded-md border-2 border-red-600 bg-red-50 p-4 text-sm text-red-900" role="alert">
      <b className="block text-base">MODE MOCK: tidak ada uang sungguhan yang dikirim.</b>
      Pencairan dan refund hanya disimulasikan (DISBURSE_MODE=mock). Jangan anggap status &quot;Selesai&quot; sebagai bukti transfer.
    </div>
  );
}

function errorText(err: unknown, fallback: string) {
  if (err instanceof ApiError) return err.fields ? Object.values(err.fields).join(" ") : err.message;
  return fallback;
}

function StatusTabs({ base, tabs, status, counts }: { base: string; tabs: [string, string][]; status: string; counts?: Record<string, number | undefined> }) {
  return (
    <nav aria-label="Status" className="mb-4 flex gap-1 overflow-x-auto border-b border-line">
      {tabs.map(([s, label]) => (
        <Link
          key={s}
          href={`${base}${qs({ status: s })}`}
          aria-current={s === status ? "page" : undefined}
          className={`-mb-px border-b-2 px-3 py-2.5 text-sm font-semibold whitespace-nowrap ${s === status ? "border-hivis text-asphalt" : "border-transparent text-muted hover:text-asphalt"}`}
        >
          {label}
          {counts && s !== "all" && <span className="ml-1 rounded-full bg-line px-2 py-0.5 text-xs tabular-nums">{counts[s] ?? 0}</span>}
        </Link>
      ))}
    </nav>
  );
}

function ReasonField({ value, onChange, max, id }: { value: string; onChange: (v: string) => void; max: number; id: string }) {
  const len = value.trim().length;
  return (
    <label className="block">
      <span className="mb-1 block font-semibold">
        Alasan<span className="ml-0.5 text-hivis" aria-hidden>*</span>
      </span>
      <textarea className={`${inputCls} min-h-28`} maxLength={max} value={value} onChange={(e) => onChange(e.target.value)} aria-describedby={id} />
      <span id={id} className={`mt-1 block text-xs tabular-nums ${len > 0 && len < 10 ? "text-red-700" : "text-muted"}`}>
        {len}/{max.toLocaleString("id-ID")} karakter{len < 10 ? " · minimal 10" : ""}
      </span>
    </label>
  );
}

// ===== /admin/pencairan =====

const PAYOUT_TABS: [string, string][] = [
  ["requested", "Menunggu"],
  ["processing", "Diproses"],
  ["completed", "Selesai"],
  ["failed", "Gagal"],
  ["rejected", "Ditolak"],
  ["all", "Semua"],
];

export function AdminPayouts() {
  const ready = useSuperAdmin();
  const params = useSearchParams();
  const status = params.get("status") ?? "requested";
  const page = Number(params.get("page") ?? 1);
  const { data, error, loading } = useApi<AdminPayoutList>(ready ? `/v1/admin/payouts${qs({ status, page })}` : null);
  if (!ready) return <Loading />;
  return (
    <>
      <PageTitle title="Pencairan dana">
        <p className="mt-2 text-sm text-muted">Setujui atau tolak pengajuan pencairan mitra. Terlama di atas.</p>
      </PageTitle>
      <AdminNav current="/admin/pencairan" />
      <MockBanner mock={data?.mock} />
      <StatusTabs base="/admin/pencairan" tabs={PAYOUT_TABS} status={status} counts={data?.counts} />
      {loading && !data ? (
        <Loading />
      ) : error ? (
        <ErrorBox message={error} />
      ) : data && data.items.length === 0 ? (
        <p className="rounded-md border border-dashed border-line bg-card p-6 text-sm text-muted">Tidak ada pencairan di sini.</p>
      ) : (
        data && (
          <>
            <ul className="divide-y divide-line rounded-md border border-line bg-card">
              {data.items.map((p) => (
                <li key={p.id}>
                  <Link href={`/admin/pencairan/${p.id}`} className="grid gap-1 px-4 py-4 hover:bg-paper sm:grid-cols-[1fr_auto] sm:items-center">
                    <span className="min-w-0">
                      <span className="flex flex-wrap items-center gap-2">
                        <b className="tabular-nums">{formatRupiah(p.amount)}</b> <MoneyBadge status={p.status} />
                      </span>
                      <span className="block truncate text-sm text-muted">
                        {p.org_name} · saldo tersedia sekarang {formatRupiah(p.org_available)}
                      </span>
                    </span>
                    <span className="text-sm text-muted tabular-nums">{formatDateTime(p.created_at)}</span>
                  </Link>
                </li>
              ))}
            </ul>
            <div className="mt-4 flex gap-3 text-sm">
              {page > 1 && (
                <Link className={btnGhost} href={`/admin/pencairan${qs({ status, page: page - 1 })}`}>
                  Sebelumnya
                </Link>
              )}
              {data.items.length === data.page_size && (
                <Link className={btnGhost} href={`/admin/pencairan${qs({ status, page: page + 1 })}`}>
                  Berikutnya
                </Link>
              )}
            </div>
          </>
        )
      )}
    </>
  );
}

// ===== /admin/pencairan/[id] =====

export function AdminPayoutDetail() {
  const ready = useSuperAdmin();
  const { id } = useParams<{ id: string }>();
  const { data, error, loading, reload } = useApi<AdminPayoutDetail>(ready ? `/v1/admin/payouts/${id}` : null);
  const [updated, setUpdated] = useState<AdminPayoutDetail | null>(null);
  const [revealed, setRevealed] = useState<string | null>(null);
  const [dialog, setDialog] = useState<null | "approve" | "reject">(null);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);

  if (!ready || (loading && !data)) return <Loading />;
  if (error && !data) return <ErrorBox message={error} />;
  const d = updated ?? data;
  if (!d) return null;
  const p = d.payout;
  const len = reason.trim().length;

  async function act(kind: "approve" | "reject") {
    setBusy(true);
    setDialogError(null);
    try {
      setUpdated(kind === "approve" ? await approvePayout(p.id) : await rejectPayout(p.id, reason.trim()));
      setDialog(null);
      setMsg(kind === "approve" ? "Disetujui. Sistem mengirim transfer dan memperbarui statusnya otomatis." : "Ditolak. Saldo mitra dikembalikan dan mereka menerima email berisi alasannya.");
    } catch (err) {
      setDialogError(errorText(err, "Gagal memproses"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageTitle title={formatRupiah(p.amount)} back={{ href: "/admin/pencairan", label: "Antrean pencairan" }}>
        <p className="mt-2 flex flex-wrap items-center gap-2 text-sm text-muted">
          <MoneyBadge status={p.status} />
          <span>
            {p.org_name} · diajukan {p.requested_by_name} {formatDateTime(p.created_at)}
          </span>
        </p>
      </PageTitle>
      <AdminNav current="/admin/pencairan" />
      <MockBanner mock={d.mock} />
      {d.conflict_of_interest && (
        <div className="mb-6 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-900" role="status">
          <b>Benturan kepentingan.</b> Kamu anggota penyelenggara ini, jadi tidak bisa memutuskan pencairannya. Minta super admin lain.
        </div>
      )}
      {msg && (
        <p className="mb-6 rounded-md bg-go-soft p-4 text-sm text-go" role="status">
          {msg}
        </p>
      )}
      <div className="grid gap-8 lg:grid-cols-[1fr_320px]">
        <div className="min-w-0 space-y-6">
          <section className="rounded-md bg-card p-5 ring-1 ring-line sm:p-6">
            <h2 className="mb-4 text-lg font-semibold">Rincian</h2>
            <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-[11rem_1fr]">
              <dt className="text-muted">Penyelenggara</dt>
              <dd className="font-medium break-words">
                {p.org_name} <span className="text-muted">/{p.org_slug} · {d.org_kyc_status === "verified" ? "terverifikasi" : d.org_kyc_status}</span>
              </dd>
              <dt className="text-muted">Bank</dt>
              <dd className="font-medium">{p.account.bank_name}</dd>
              <dt className="text-muted">Nomor rekening</dt>
              <dd className="flex flex-wrap items-center gap-2 font-medium">
                <span className="font-mono">{revealed ?? `••••${p.account.last4}`}</span>
                {!revealed && (
                  <button
                    type="button"
                    className="text-xs font-semibold underline underline-offset-2"
                    onClick={async () => {
                      try {
                        setRevealed((await revealPayoutAccount(p.id)).value);
                      } catch (err) {
                        setMsg(errorText(err, "Gagal menampilkan rekening"));
                      }
                    }}
                  >
                    Tampilkan<span className="sr-only"> nomor rekening lengkap</span>
                  </button>
                )}
              </dd>
              <dt className="text-muted">Pemilik rekening</dt>
              <dd className="font-medium break-words">{p.account.account_holder}</dd>
              {p.reference_no && (
                <>
                  <dt className="text-muted">Referensi gateway</dt>
                  <dd className="font-mono text-xs break-all">{p.reference_no}</dd>
                </>
              )}
              {p.approved_by_name && (
                <>
                  <dt className="text-muted">Diputuskan</dt>
                  <dd>
                    {p.approved_by_name} {p.approved_at ? formatDateTime(p.approved_at) : ""}
                  </dd>
                </>
              )}
              {p.reject_reason && (
                <>
                  <dt className="text-muted">Alasan ditolak</dt>
                  <dd className="whitespace-pre-line">{p.reject_reason}</dd>
                </>
              )}
              {p.fail_reason && (
                <>
                  <dt className="text-muted">{p.status === "failed" ? "Alasan gagal" : "Percobaan terakhir"}</dt>
                  <dd>
                    {p.fail_reason} {p.attempts > 0 && <span className="text-muted">({p.attempts} percobaan)</span>}
                  </dd>
                </>
              )}
            </dl>
            <p className="mt-4 text-xs text-muted">Menekan &quot;Tampilkan&quot; mencatat aksesmu di log audit.</p>
          </section>

          <section>
            <h2 className="mb-3 text-lg font-semibold">Riwayat pencairan penyelenggara</h2>
            {d.history.length === 0 ? (
              <p className="text-sm text-muted">Ini pencairan pertama.</p>
            ) : (
              <ul className="divide-y divide-line rounded-md border border-line bg-card text-sm">
                {d.history.map((h) => (
                  <li key={h.id} className="flex flex-wrap items-center justify-between gap-2 p-3">
                    <span className="flex items-center gap-2">
                      <b className="tabular-nums">{formatRupiah(h.amount)}</b> <MoneyBadge status={h.status} />
                    </span>
                    <span className="text-muted tabular-nums">{formatDateTime(h.created_at)}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>

        <aside className="space-y-6">
          <section className="rounded-md bg-card p-5 text-sm ring-1 ring-line">
            <h2 className="text-lg font-semibold">Saldo penyelenggara saat ini</h2>
            <dl className="mt-3 grid grid-cols-2 gap-y-1">
              {(
                [
                  ["Tertahan", d.balances.held],
                  ["Tersedia", d.balances.available],
                  ["Dalam proses", d.balances.in_process],
                  ["Sudah dicairkan", d.balances.paid_out],
                ] as [string, number][]
              ).map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="text-muted">{k}</dt>
                  <dd className={`text-right tabular-nums ${v < 0 ? "font-semibold text-red-700" : ""}`}>{formatRupiah(v)}</dd>
                </div>
              ))}
            </dl>
          </section>
          {p.status === "requested" ? (
            <section className="rounded-md bg-card p-5 ring-1 ring-line">
              <h2 className="text-lg font-semibold">Keputusan</h2>
              <p className="mt-1 text-sm text-muted">Pastikan nama pemilik rekening sesuai penyelenggara dan jumlahnya wajar.</p>
              <div className="mt-4 flex flex-wrap gap-2">
                <button type="button" className={btnDark} disabled={d.conflict_of_interest} onClick={() => (setDialogError(null), setDialog("approve"))}>
                  Setujui
                </button>
                <button type="button" className={`${btnGhost} text-red-700`} disabled={d.conflict_of_interest} onClick={() => (setDialogError(null), setDialog("reject"))}>
                  Tolak
                </button>
              </div>
            </section>
          ) : (
            (p.status === "approved" || p.status === "processing") && (
              <button type="button" className={btnGhost} onClick={() => (setUpdated(null), reload())}>
                Muat ulang status
              </button>
            )
          )}
        </aside>
      </div>

      <ConfirmDialog open={dialog === "approve"} title="Setujui pencairan?" confirmLabel="Setujui & kirim" busy={busy} onClose={() => setDialog(null)} onConfirm={() => act("approve")}>
        <p>
          <b className="tabular-nums">{formatRupiah(p.amount)}</b> akan dikirim ke <b className="break-words">{maskedAccount(p.account)}</b>.
        </p>
        {dialogError && <ErrorBox message={dialogError} />}
      </ConfirmDialog>
      <ConfirmDialog
        open={dialog === "reject"}
        title="Tolak pencairan?"
        confirmLabel="Tolak"
        danger
        busy={busy}
        disabled={len < 10 || len > 1000}
        onClose={() => setDialog(null)}
        onConfirm={() => act("reject")}
      >
        <p>Saldo kembali ke saldo tersedia mitra. Alasan dikirim lewat email; jangan menulis nomor rekening.</p>
        <ReasonField value={reason} onChange={setReason} max={1000} id="hitung-tolak-cair" />
        {dialogError && <ErrorBox message={dialogError} />}
      </ConfirmDialog>
    </>
  );
}

// ===== /admin/refund =====

const REFUND_TABS: [string, string][] = [
  ["requested", "Menunggu"],
  ["manual_required", "Perlu kirim manual"],
  ["failed", "Gagal"],
  ["processing", "Diproses"],
  ["completed", "Selesai"],
  ["rejected", "Ditolak"],
  ["all", "Semua"],
];

export function AdminRefunds() {
  const ready = useSuperAdmin();
  const params = useSearchParams();
  const router = useRouter();
  const status = params.get("status") ?? "requested";
  const { data, error, loading, reload } = useApi<AdminRefundList>(ready ? `/v1/admin/refunds${qs({ status })}` : null);
  const [target, setTarget] = useState<{ r: Refund; kind: "approve" | "reject" | "mark-done" } | null>(null);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [newOrder, setNewOrder] = useState("");
  const [newReason, setNewReason] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [createErr, setCreateErr] = useState<Record<string, string>>({});

  if (!ready) return <Loading />;
  const len = text.trim().length;

  async function act() {
    if (!target) return;
    setBusy(true);
    setDialogError(null);
    try {
      const { r, kind } = target;
      await refundAction(r.id, kind, kind === "reject" ? { reason: text.trim() } : kind === "mark-done" ? { reference: text.trim() } : undefined);
      setTarget(null);
      setMsg(
        kind === "approve"
          ? "Refund disetujui: order dan tiket tidak berlaku lagi, dana dikembalikan ke pembeli."
          : kind === "reject"
            ? "Refund ditolak. Order tetap berlaku."
            : "Refund ditandai selesai.",
      );
      reload();
    } catch (err) {
      setDialogError(errorText(err, "Gagal memproses"));
    } finally {
      setBusy(false);
    }
  }

  async function create() {
    setBusy(true);
    setCreateErr({});
    try {
      await createRefund(newOrder.trim(), newReason.trim());
      setCreateOpen(false);
      setNewOrder("");
      setNewReason("");
      setMsg("Refund dibuat dan langsung disetujui.");
      router.push(`/admin/refund${qs({ status: "all" })}`);
      reload();
    } catch (err) {
      if (err instanceof ApiError && err.fields) setCreateErr(err.fields);
      else setCreateErr({ _: errorText(err, "Gagal membuat refund") });
    } finally {
      setBusy(false);
    }
  }

  const dialogTitle = { approve: "Setujui refund?", reject: "Tolak refund?", "mark-done": "Tandai sudah dikembalikan?" };
  return (
    <>
      <PageTitle title="Refund">
        <p className="mt-2 text-sm text-muted">Refund selalu seluruh order. Disetujui = tiket langsung tidak berlaku dan saldo mitra dikoreksi.</p>
      </PageTitle>
      <AdminNav current="/admin/refund" />
      <MockBanner mock={data?.mock} />
      {msg && (
        <p className="mb-4 rounded-md bg-go-soft p-3 text-sm text-go" role="status">
          {msg}
        </p>
      )}
      <div className="mb-6">
        <button type="button" className={btnDark} onClick={() => (setCreateOpen(true), setCreateErr({}), setMsg(null))}>
          Buat refund langsung
        </button>
      </div>
      <StatusTabs base="/admin/refund" tabs={REFUND_TABS} status={status} counts={data?.counts} />
      {loading && !data ? (
        <Loading />
      ) : error ? (
        <ErrorBox message={error} />
      ) : data && data.items.length === 0 ? (
        <p className="rounded-md border border-dashed border-line bg-card p-6 text-sm text-muted">Tidak ada refund di sini.</p>
      ) : (
        data && (
          <ul className="divide-y divide-line rounded-md border border-line bg-card text-sm">
            {data.items.map((r) => (
              <li key={r.id} className="grid gap-3 p-4 lg:grid-cols-[1fr_auto] lg:items-start">
                <div className="min-w-0">
                  <p className="flex flex-wrap items-center gap-2">
                    <b className="tabular-nums">{formatRupiah(r.amount)}</b> <MoneyBadge status={r.status} />
                    <span className="font-mono text-xs text-muted">order #{r.order_id.slice(0, 8)}</span>
                  </p>
                  <p className="mt-0.5 break-words text-muted">
                    {r.org_name} · {r.event_name} · pembeli {r.buyer_email}
                  </p>
                  <p className="mt-1 break-words">
                    &ldquo;{r.reason}&rdquo; <span className="text-muted">— {r.requested_by_name}, {formatDateTime(r.created_at)}</span>
                  </p>
                  {r.collected_tickets > 0 && (
                    <p className="mt-2 rounded-sm bg-amber-50 p-2 text-amber-900">⚠ {r.collected_tickets} tiket order ini sudah mengambil race pack.</p>
                  )}
                  {r.fail_reason && r.status !== "completed" && <p className="mt-1 text-amber-900">{r.fail_reason}</p>}
                  {r.reject_reason && <p className="mt-1 text-red-800">Ditolak: {r.reject_reason}</p>}
                  {r.reference && <p className="mt-1 text-muted">Referensi: {r.reference}</p>}
                </div>
                <div className="flex flex-wrap gap-2">
                  {r.status === "requested" && (
                    <>
                      <button type="button" className={btnDark} onClick={() => (setTarget({ r, kind: "approve" }), setText(""), setDialogError(null))}>
                        Setujui
                      </button>
                      <button type="button" className={`${btnGhost} text-red-700`} onClick={() => (setTarget({ r, kind: "reject" }), setText(""), setDialogError(null))}>
                        Tolak
                      </button>
                    </>
                  )}
                  {(r.status === "manual_required" || r.status === "failed") && (
                    <button type="button" className={btnDark} onClick={() => (setTarget({ r, kind: "mark-done" }), setText(""), setDialogError(null))}>
                      Tandai sudah dikembalikan
                    </button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )
      )}

      <ConfirmDialog
        open={target !== null}
        title={target ? dialogTitle[target.kind] : ""}
        confirmLabel={target?.kind === "approve" ? "Setujui refund" : target?.kind === "reject" ? "Tolak" : "Tandai selesai"}
        danger={target?.kind === "approve"}
        busy={busy}
        disabled={target?.kind === "reject" ? len < 10 || len > 1000 : target?.kind === "mark-done" ? len < 3 : false}
        onClose={() => setTarget(null)}
        onConfirm={act}
      >
        {target?.kind === "approve" && (
          <>
            <p>
              <b>{formatRupiah(target.r.amount)}</b> dikembalikan penuh ke {target.r.buyer_email}. Semua tiket order ini langsung tidak berlaku dan saldo {target.r.org_name} dikurangi bagian
              penjualannya.
            </p>
            {target.r.collected_tickets > 0 && (
              <p className="rounded-md border border-amber-300 bg-amber-50 p-3 text-amber-900">
                {target.r.collected_tickets} tiket sudah mengambil race pack. Pastikan memang layak di-refund.
              </p>
            )}
          </>
        )}
        {target?.kind === "reject" && <ReasonField value={text} onChange={setText} max={1000} id="hitung-tolak-refund" />}
        {target?.kind === "mark-done" && (
          <>
            <p>Kirim dana ke pembeli di luar sistem dulu, lalu isi nomor referensi transfernya.</p>
            <Field label="Nomor referensi transfer" required>
              <input className={inputCls} maxLength={100} value={text} onChange={(e) => setText(e.target.value)} />
            </Field>
          </>
        )}
        {dialogError && <ErrorBox message={dialogError} />}
      </ConfirmDialog>

      <ConfirmDialog
        open={createOpen}
        title="Buat refund langsung"
        confirmLabel="Buat & setujui"
        danger
        busy={busy}
        disabled={!newOrder.trim() || newReason.trim().length < 10}
        onClose={() => setCreateOpen(false)}
        onConfirm={create}
      >
        <p>Refund langsung disetujui: order dan tiketnya tidak berlaku lagi.</p>
        <Field label="ID order" required error={createErr.order_id}>
          <input className={inputErr(createErr.order_id)} value={newOrder} onChange={(e) => setNewOrder(e.target.value)} placeholder="UUID order lengkap" />
        </Field>
        <ReasonField value={newReason} onChange={setNewReason} max={1000} id="hitung-buat-refund" />
        {createErr.reason && <p className="text-xs text-red-700">{createErr.reason}</p>}
        {createErr._ && <ErrorBox message={createErr._} />}
      </ConfirmDialog>
    </>
  );
}

// ===== /admin/keuangan =====

export function AdminFinance() {
  const ready = useSuperAdmin();
  const overview = useApi<FinanceOverview>(ready ? "/v1/admin/finance/overview" : null);
  const check = useApi<LedgerCheck>(ready ? "/v1/admin/ledger/check" : null);
  if (!ready || (overview.loading && !overview.data)) return <Loading />;
  if (overview.error && !overview.data) return <ErrorBox message={overview.error} />;
  const o = overview.data;
  if (!o) return null;
  const cards: [string, number, string][] = [
    ["Saldo mitra tertahan", o.held, `Dirilis ${o.hold_days} hari setelah hari lomba`],
    ["Saldo mitra tersedia", o.available, "Bisa dicairkan mitra"],
    ["Pencairan dalam proses", o.in_process, "Diajukan, disetujui, atau sedang ditransfer"],
    ["Pendapatan platform", o.platform_revenue, "Fee platform setelah refund"],
    ["Beban biaya PG refund", o.refund_pg_cost, "Biaya PG yang tidak kembali saat refund"],
    ["Kas di Midtrans (estimasi)", o.cash, "Uang masuk − refund − pencairan − biaya PG"],
  ];
  return (
    <>
      <PageTitle title="Keuangan platform">
        <p className="mt-2 text-sm text-muted">
          Mode pencairan: <b>{o.disburse_mode}</b> · minimum pencairan {formatRupiah(o.payout_min)}
        </p>
      </PageTitle>
      <AdminNav current="/admin/keuangan" />
      <MockBanner mock={o.mock} />
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {cards.map(([k, v, hint]) => (
          <div key={k} className="rounded-md bg-card p-4 ring-1 ring-line">
            <p className="text-sm font-semibold text-muted">{k}</p>
            <p className={`mt-1 font-display text-3xl font-extrabold tabular-nums ${v < 0 ? "text-red-700" : ""}`}>{formatRupiah(v)}</p>
            <p className="mt-1 text-xs text-muted">{hint}</p>
          </div>
        ))}
      </div>
      <section className="mt-8" aria-labelledby="integritas">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
          <h2 id="integritas" className="text-lg font-semibold">
            Pemeriksaan integritas ledger
          </h2>
          <button type="button" className={btnGhost} onClick={check.reload}>
            Periksa ulang
          </button>
        </div>
        {check.error && !check.data ? (
          <ErrorBox message={check.error} />
        ) : !check.data ? (
          <Loading />
        ) : (
          <div className={`rounded-md border-2 p-4 ${check.data.ok ? "border-go/40 bg-go-soft" : "border-red-600 bg-red-50"}`} role="status">
            <p className={`font-semibold ${check.data.ok ? "text-go" : "text-red-800"}`}>{check.data.ok ? "✓ Ledger sehat" : "✗ Ledger bermasalah: hentikan pencairan dan periksa"}</p>
            <ul className="mt-3 space-y-2 text-sm">
              {check.data.checks.map((c) => (
                <li key={c.name} className="flex gap-2">
                  <span aria-hidden className={c.ok ? "text-go" : "text-red-700"}>
                    {c.ok ? "✓" : "✗"}
                  </span>
                  <span className="min-w-0 break-words">
                    <b>{c.name}</b> <span className="text-muted">· {c.detail}</span>
                    <span className="sr-only">{c.ok ? " (lulus)" : " (gagal)"}</span>
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </section>
      <section className="mt-8 grid gap-4 sm:grid-cols-2">
        <div className="rounded-md bg-card p-4 text-sm ring-1 ring-line">
          <h2 className="font-semibold">Pencairan</h2>
          <p className="mt-2 leading-8 text-muted">
            {Object.entries(o.payouts).length === 0 && "Belum ada · "}
            {Object.entries(o.payouts).map(([k, v]) => (
              <span key={k} className="mr-2 inline-flex items-center gap-1">
                <MoneyBadge status={k} /> <span className="tabular-nums">{v}</span>
              </span>
            ))}
            <Link href="/admin/pencairan" className="font-semibold text-asphalt underline underline-offset-2">
              Buka antrean
            </Link>
          </p>
        </div>
        <div className="rounded-md bg-card p-4 text-sm ring-1 ring-line">
          <h2 className="font-semibold">Refund</h2>
          <p className="mt-2 leading-8 text-muted">
            {Object.entries(o.refunds).length === 0 && "Belum ada · "}
            {Object.entries(o.refunds).map(([k, v]) => (
              <span key={k} className="mr-2 inline-flex items-center gap-1">
                <MoneyBadge status={k} /> <span className="tabular-nums">{v}</span>
              </span>
            ))}
            <Link href="/admin/refund" className="font-semibold text-asphalt underline underline-offset-2">
              Buka antrean
            </Link>
          </p>
        </div>
      </section>
    </>
  );
}
