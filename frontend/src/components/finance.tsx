"use client";

import { useEffect, useState } from "react";
import {
  ApiError,
  downloadFile,
  qs,
  requestPayout,
  requestRefund,
  type EventSales,
  type FinanceSummary,
  type Mutation,
  type OrgOrder,
  type OrgProfile,
  type Paged,
  type Payout,
  type Refund,
} from "@/lib/api";
import { formatDateShort, formatDateTime, formatRupiah } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { OrgShell } from "./org-shell";
import { btnDark, btnGhost, ErrorBox, Field, inputCls, inputErr, Loading, StatusBadge } from "./ui";
import { ConfirmDialog } from "./ui-client";

// ===== Lencana status keuangan (dipakai juga halaman admin) =====

const MONEY_STATUS: Record<string, [string, string]> = {
  requested: ["Menunggu persetujuan", "bg-amber-100 text-amber-900"],
  approved: ["Disetujui", "bg-sky-100 text-sky-800"],
  processing: ["Diproses", "bg-sky-100 text-sky-800"],
  completed: ["Selesai", "bg-go-soft text-go"],
  failed: ["Gagal", "bg-red-100 text-red-800"],
  rejected: ["Ditolak", "bg-red-100 text-red-800"],
  manual_required: ["Perlu kirim manual", "bg-amber-100 text-amber-900"],
};

export function MoneyBadge({ status }: { status: string }) {
  const [label, cls] = MONEY_STATUS[status] ?? [status, "bg-line text-muted"];
  return <span className={`inline-block rounded-full px-2.5 py-0.5 text-xs font-semibold whitespace-nowrap ${cls}`}>{label}</span>;
}

export const maskedAccount = (a: { bank_name: string; last4: string; account_holder: string }) => `${a.bank_name} ••••${a.last4} a.n. ${a.account_holder}`;

/** "1.250.000" ↔ 1250000: input jumlah rupiah yang tetap terbaca saat diketik. */
const digits = (s: string) => s.replace(/\D/g, "").replace(/^0+(?=\d)/, "").slice(0, 13);
const withDots = (s: string) => (s ? Number(s).toLocaleString("id-ID") : "");

function errorText(err: unknown, fallback: string) {
  if (err instanceof ApiError) return err.fields ? Object.values(err.fields).join(" ") : err.message;
  return fallback;
}

function Pager({ page, pageSize, total, onPage }: { page: number; pageSize: number; total: number; onPage: (p: number) => void }) {
  if (total <= pageSize) return null;
  const pages = Math.ceil(total / pageSize);
  return (
    <div className="mt-3 flex items-center gap-3 text-sm">
      <button type="button" className={btnGhost} disabled={page <= 1} onClick={() => onPage(page - 1)}>
        Sebelumnya
      </button>
      <span className="text-muted tabular-nums">
        Halaman {page} dari {pages}
      </span>
      <button type="button" className={btnGhost} disabled={page >= pages} onClick={() => onPage(page + 1)}>
        Berikutnya
      </button>
    </div>
  );
}

function Download({ path, name, label }: { path: string; name: string; label: string }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <span className="inline-flex flex-col items-start">
      <button
        type="button"
        className={btnGhost}
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setError(null);
          try {
            await downloadFile(path, name);
          } catch (err) {
            setError(errorText(err, "Gagal mengunduh"));
          } finally {
            setBusy(false);
          }
        }}
      >
        {busy ? "Mengunduh…" : label}
      </button>
      {error && (
        <span className="mt-1 text-xs text-red-700" role="alert">
          {error}
        </span>
      )}
    </span>
  );
}

// ===== Halaman /org/[org]/keuangan =====

export function OrgFinance() {
  return (
    <OrgShell tab="finance">
      {(profile) =>
        profile.role === "owner" || profile.role === "finance" ? (
          <FinanceContent profile={profile} />
        ) : (
          <p className="rounded-md border border-dashed border-line bg-card p-6 text-sm text-muted">Keuangan hanya bisa dibuka owner dan finance penyelenggara.</p>
        )
      }
    </OrgShell>
  );
}

function FinanceContent({ profile }: { profile: OrgProfile }) {
  const org = profile.slug;
  const summary = useApi<FinanceSummary>(`/v1/org/${org}/finance/summary`);
  const payouts = useApi<Paged<Payout>>(`/v1/org/${org}/payouts`);
  const [ledgerPage, setLedgerPage] = useState(1);
  const ledger = useApi<Paged<Mutation> & { total: number }>(`/v1/org/${org}/finance/ledger${qs({ page: ledgerPage })}`);
  const { reload: reloadSummary } = summary;
  const { reload: reloadPayouts } = payouts;
  const { reload: reloadLedger } = ledger;
  const reloadAll = () => {
    reloadSummary();
    reloadPayouts();
    reloadLedger();
  };

  // Selama ada pencairan berjalan, perbarui status tiap 5 detik tanpa refresh.
  const active = payouts.data?.items.some((p) => p.status === "requested" || p.status === "approved" || p.status === "processing");
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => {
      reloadSummary();
      reloadPayouts();
      reloadLedger();
    }, 5000);
    return () => clearInterval(t);
  }, [active, reloadSummary, reloadPayouts, reloadLedger]);

  if (summary.loading && !summary.data) return <Loading />;
  if (summary.error && !summary.data) return <ErrorBox message={summary.error} />;
  const s = summary.data;
  if (!s) return null;

  return (
    <div className="space-y-10">
      {s.mock && (
        <p className="rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900" role="note">
          Mode simulasi: pencairan tidak mengirim uang sungguhan.
        </p>
      )}
      <BalanceCards s={s} />
      <PayoutPanel org={org} s={s} onDone={reloadAll} />

      <section aria-labelledby="riwayat-cair">
        <h2 id="riwayat-cair" className="mb-3 text-lg font-semibold">
          Riwayat pencairan
        </h2>
        {payouts.error && !payouts.data ? (
          <ErrorBox message={payouts.error} />
        ) : !payouts.data ? (
          <Loading />
        ) : payouts.data.items.length === 0 ? (
          <p className="rounded-md border border-dashed border-line bg-card p-5 text-sm text-muted">Belum ada pencairan.</p>
        ) : (
          <ul className="divide-y divide-line rounded-md border border-line bg-card">
            {payouts.data.items.map((p) => (
              <li key={p.id} className="grid gap-1 p-4 sm:grid-cols-[1fr_auto] sm:items-start">
                <div className="min-w-0">
                  <p className="flex flex-wrap items-center gap-2">
                    <b className="tabular-nums">{formatRupiah(p.amount)}</b> <MoneyBadge status={p.status} />
                  </p>
                  <p className="mt-0.5 truncate text-sm text-muted">
                    ke {maskedAccount(p.account)} · diajukan {p.requested_by_name}
                  </p>
                  {p.reject_reason && <p className="mt-1 text-sm whitespace-pre-line text-red-800">Alasan ditolak: {p.reject_reason}</p>}
                  {p.status === "failed" && p.fail_reason && <p className="mt-1 text-sm text-red-800">Gagal: {p.fail_reason}. Saldo sudah dikembalikan.</p>}
                </div>
                <p className="text-sm text-muted tabular-nums sm:text-right">
                  {formatDateTime(p.created_at)}
                  {p.completed_at && <span className="block">selesai {formatDateTime(p.completed_at)}</span>}
                </p>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section aria-labelledby="mutasi">
        <div className="mb-3 flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 id="mutasi" className="text-lg font-semibold">
              Mutasi saldo
            </h2>
            <p className="text-sm text-muted">Setiap perubahan saldo dicatat di buku besar dan tidak pernah diubah.</p>
          </div>
          <Download path={`/v1/org/${org}/reports/ledger.csv`} name="mutasi-saldo.csv" label="Unduh CSV mutasi" />
        </div>
        {ledger.error && !ledger.data ? <ErrorBox message={ledger.error} /> : !ledger.data ? <Loading /> : <Mutations data={ledger.data} onPage={setLedgerPage} />}
      </section>

      <SalesSection org={org} />
      <OrdersSection org={org} onChange={reloadAll} />
    </div>
  );
}

function BalanceCards({ s }: { s: FinanceSummary }) {
  const b = s.balances;
  const cards: [string, number, string][] = [
    ["Tertahan", b.held, `Dirilis ${s.rules.hold_days} hari setelah hari lomba.`],
    ["Tersedia", b.available, "Bisa dicairkan ke rekening terverifikasi."],
    ["Dalam proses", b.in_process, "Pencairan yang sedang ditinjau atau ditransfer."],
    ["Sudah dicairkan", b.paid_out, "Total yang sudah sampai ke rekeningmu."],
  ];
  return (
    <section aria-label="Saldo">
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {cards.map(([label, value, hint]) => (
          <div key={label} className={`rounded-md bg-card p-4 ring-1 ${value < 0 ? "ring-red-300" : "ring-line"}`}>
            <p className="text-sm font-semibold text-muted">{label}</p>
            <p className={`mt-1 font-display text-3xl font-extrabold tabular-nums ${value < 0 ? "text-red-700" : ""}`}>{formatRupiah(value)}</p>
            <p className="mt-1 text-xs text-muted">{hint}</p>
          </div>
        ))}
      </div>
      {(b.available < 0 || b.held < 0) && (
        <p className="mt-3 rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-900" role="note">
          <b>Saldo negatif.</b> Refund order yang dananya sudah dirilis atau dicairkan membuat saldo minus. Kekurangan ini tertutup otomatis oleh penjualan berikutnya; selama minus, pencairan belum bisa diajukan.
        </p>
      )}
    </section>
  );
}

function PayoutPanel({ org, s, onDone }: { org: string; s: FinanceSummary; onDone: () => void }) {
  const [amount, setAmount] = useState("");
  const [fieldError, setFieldError] = useState<string | undefined>();
  const [dialog, setDialog] = useState(false);
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const available = s.balances.available;
  const min = s.rules.payout_min;
  const value = Number(amount || 0);

  function open() {
    setMsg(null);
    if (!value) return setFieldError("Isi jumlah pencairan");
    if (value < min) return setFieldError(`Minimal ${formatRupiah(min)}`);
    if (value > available) return setFieldError(`Melebihi saldo tersedia ${formatRupiah(available)}`);
    setFieldError(undefined);
    setDialogError(null);
    // Satu kunci per niat: klik ganda atau coba lagi di dialog ini tidak membuat pencairan kedua.
    setKey(crypto.randomUUID());
    setDialog(true);
  }

  async function submit() {
    setBusy(true);
    setDialogError(null);
    try {
      await requestPayout(org, value, key);
      setDialog(false);
      setAmount("");
      setMsg("Pencairan diajukan. Tim RaceGo akan meninjau, lalu dana ditransfer otomatis.");
      onDone();
    } catch (err) {
      setDialogError(errorText(err, "Gagal mengajukan pencairan"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section aria-labelledby="cairkan" className="rounded-md bg-card p-5 ring-1 ring-line sm:p-6">
      <h2 id="cairkan" className="text-lg font-semibold">
        Cairkan dana
      </h2>
      {s.account ? (
        <p className="mt-1 text-sm">
          Ke rekening <b className="break-words">{maskedAccount(s.account)}</b>
        </p>
      ) : (
        <p className="mt-1 text-sm text-muted">Belum ada rekening pencairan terverifikasi.</p>
      )}
      {msg && (
        <p className="mt-3 rounded-md bg-go-soft p-3 text-sm text-go" role="status">
          {msg}
        </p>
      )}
      {!s.can_payout && s.blocker && s.blocker.code !== "below_minimum" ? (
        <p className="mt-3 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900" role="status">
          {s.blocker.message}
        </p>
      ) : (
        <form
          className="mt-4 flex flex-wrap items-start gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            open();
          }}
        >
          <div className="w-full max-w-xs">
            <Field label="Jumlah (Rp)" required hint={`Minimal ${formatRupiah(min)} · tersedia ${formatRupiah(available)}`} error={fieldError}>
              <input
                className={inputErr(fieldError)}
                inputMode="numeric"
                autoComplete="off"
                value={withDots(amount)}
                onChange={(e) => setAmount(digits(e.target.value))}
                placeholder="0"
              />
            </Field>
          </div>
          <div className="flex gap-2 sm:mt-6">
            <button type="button" className={btnGhost} disabled={available <= 0} onClick={() => (setAmount(String(Math.max(available, 0))), setFieldError(undefined))}>
              Cairkan semua
            </button>
            <button type="submit" className={btnDark}>
              Ajukan pencairan
            </button>
          </div>
          {s.blocker?.code === "below_minimum" && <p className="w-full text-sm text-muted">{s.blocker.message}</p>}
        </form>
      )}
      <ConfirmDialog open={dialog} title="Cairkan dana?" confirmLabel="Ya, ajukan" busy={busy} onClose={() => setDialog(false)} onConfirm={submit}>
        <p>
          <b className="tabular-nums">{formatRupiah(value)}</b> akan ditransfer ke <b className="break-words">{s.account ? maskedAccount(s.account) : "–"}</b> setelah disetujui tim RaceGo.
        </p>
        <p className="text-muted">Saldo langsung dipindah ke &quot;Dalam proses&quot; dan kembali bila pencairan ditolak atau gagal.</p>
        {dialogError && <ErrorBox message={dialogError} />}
      </ConfirmDialog>
    </section>
  );
}

const ACCOUNT_LABEL: Record<Mutation["account"], string> = { tertahan: "Tertahan", tersedia: "Tersedia", dalam_proses: "Dalam proses" };

function Mutations({ data, onPage }: { data: Paged<Mutation> & { total: number }; onPage: (p: number) => void }) {
  if (data.items.length === 0) return <p className="rounded-md border border-dashed border-line bg-card p-5 text-sm text-muted">Belum ada mutasi.</p>;
  const amount = (m: Mutation) => (
    <span className={`shrink-0 font-semibold whitespace-nowrap tabular-nums ${m.amount < 0 ? "text-red-700" : "text-go"}`}>
      {m.amount > 0 ? "+" : ""}
      {formatRupiah(m.amount)}
    </span>
  );
  return (
    <>
      <table className="hidden w-full rounded-md bg-card text-sm ring-1 ring-line md:table">
        <thead className="text-left text-muted">
          <tr className="border-b border-line">
            <th className="p-3 font-semibold">Waktu</th>
            <th className="p-3 font-semibold">Keterangan</th>
            <th className="p-3 font-semibold">Saldo</th>
            <th className="p-3 text-right font-semibold">Jumlah</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {data.items.map((m) => (
            <tr key={m.id}>
              <td className="p-3 whitespace-nowrap text-muted tabular-nums">{formatDateTime(m.at)}</td>
              <td className="p-3 break-words">{m.description}</td>
              <td className="p-3">{ACCOUNT_LABEL[m.account] ?? m.account}</td>
              <td className="p-3 text-right">{amount(m)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <ul className="divide-y divide-line rounded-md border border-line bg-card md:hidden">
        {data.items.map((m) => (
          <li key={m.id} className="p-3 text-sm">
            <div className="flex items-start justify-between gap-3">
              <span className="min-w-0 break-words">{m.description}</span>
              {amount(m)}
            </div>
            <p className="mt-1 text-xs text-muted">
              {formatDateTime(m.at)} · {ACCOUNT_LABEL[m.account] ?? m.account}
            </p>
          </li>
        ))}
      </ul>
      <Pager page={data.page} pageSize={data.page_size} total={data.total} onPage={onPage} />
    </>
  );
}

function SalesSection({ org }: { org: string }) {
  const { data, error } = useApi<{ items: EventSales[] }>(`/v1/org/${org}/reports/sales`);
  return (
    <section aria-labelledby="penjualan">
      <div className="mb-3 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 id="penjualan" className="text-lg font-semibold">
            Ringkasan penjualan per event
          </h2>
          <p className="text-sm text-muted">Biaya payment gateway masih estimasi sampai dicocokkan dengan laporan settlement.</p>
        </div>
        <Download path={`/v1/org/${org}/reports/sales.csv`} name="penjualan.csv" label="Unduh CSV penjualan" />
      </div>
      {error && !data ? (
        <ErrorBox message={error} />
      ) : !data ? (
        <Loading />
      ) : data.items.length === 0 ? (
        <p className="rounded-md border border-dashed border-line bg-card p-5 text-sm text-muted">Belum ada penjualan.</p>
      ) : (
        <ul className="grid gap-3 lg:grid-cols-2">
          {data.items.map((e) => (
            <li key={e.event_id} className="rounded-md bg-card p-4 ring-1 ring-line">
              <p className="font-semibold break-words">{e.event_name}</p>
              <p className="text-xs text-muted">
                Lomba {formatDateShort(e.race_date)} · {e.orders_paid} order lunas{e.orders_refunded ? ` · ${e.orders_refunded} di-refund` : ""}
              </p>
              <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                {(
                  [
                    ["Bruto", e.gross],
                    ["Fee platform", -e.platform_fee],
                    ["Biaya PG (estimasi)", -e.pg_fee],
                    ["Refund", -e.refunds],
                  ] as [string, number][]
                ).map(([k, v]) => (
                  <div key={k} className="contents">
                    <dt className="text-muted">{k}</dt>
                    <dd className="text-right tabular-nums">{formatRupiah(v)}</dd>
                  </div>
                ))}
                <dt className="border-t border-line pt-1 font-semibold">Bersih</dt>
                <dd className="border-t border-line pt-1 text-right font-semibold tabular-nums">{formatRupiah(e.net)}</dd>
              </dl>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function OrdersSection({ org, onChange }: { org: string; onChange: () => void }) {
  const [event, setEvent] = useState("");
  const [q, setQ] = useState("");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const events = useApi<{ items: EventSales[] }>(`/v1/org/${org}/reports/sales`);
  const orders = useApi<Paged<OrgOrder> & { total: number }>(`/v1/org/${org}/orders${qs({ event, q, page })}`);
  const refunds = useApi<Paged<Refund>>(`/v1/org/${org}/refunds`);
  const [target, setTarget] = useState<OrgOrder | null>(null);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const len = reason.trim().length;

  async function submit() {
    if (!target) return;
    setBusy(true);
    setDialogError(null);
    try {
      await requestRefund(org, target.id, reason.trim());
      setTarget(null);
      setReason("");
      setMsg("Refund diajukan. Tim RaceGo akan meninjau.");
      orders.reload();
      refunds.reload();
      onChange();
    } catch (err) {
      setDialogError(errorText(err, "Gagal mengajukan refund"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section aria-labelledby="pesanan" className="space-y-4">
      <div>
        <h2 id="pesanan" className="text-lg font-semibold">
          Pesanan &amp; refund
        </h2>
        <p className="text-sm text-muted">Refund selalu seluruh order dan diputuskan tim RaceGo. Data peserta tidak ditampilkan di sini.</p>
      </div>
      {msg && (
        <p className="rounded-md bg-go-soft p-3 text-sm text-go" role="status">
          {msg}
        </p>
      )}
      <form
        role="search"
        className="grid gap-3 sm:grid-cols-[minmax(0,14rem)_minmax(0,1fr)_auto] sm:items-end"
        onSubmit={(e) => {
          e.preventDefault();
          setPage(1);
          setQ(search.trim());
        }}
      >
        <Field label="Event">
          <select className={inputCls} value={event} onChange={(e) => (setEvent(e.target.value), setPage(1))}>
            <option value="">Semua event</option>
            {events.data?.items.map((e) => (
              <option key={e.event_id} value={e.event_id}>
                {e.event_name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Cari email pembeli atau ID order">
          <input type="search" className={inputCls} value={search} onChange={(e) => setSearch(e.target.value)} />
        </Field>
        <button type="submit" className={btnDark}>
          Cari
        </button>
      </form>
      {orders.error && !orders.data ? (
        <ErrorBox message={orders.error} />
      ) : !orders.data ? (
        <Loading />
      ) : orders.data.items.length === 0 ? (
        <p className="rounded-md border border-dashed border-line bg-card p-5 text-sm text-muted">Tidak ada order.</p>
      ) : (
        <>
          <ul className="divide-y divide-line rounded-md border border-line bg-card">
            {orders.data.items.map((o) => {
              const refundable = (o.status === "paid" || o.status === "needs_refund") && (!o.refund_status || o.refund_status === "rejected");
              return (
                <li key={o.id} className="grid gap-2 p-4 sm:grid-cols-[1fr_auto] sm:items-center">
                  <div className="min-w-0 text-sm">
                    <p className="flex flex-wrap items-center gap-2">
                      <b className="font-mono">#{o.id.slice(0, 8)}</b> <StatusBadge status={o.status} />
                      {o.refund_status && o.refund_status !== "completed" && <MoneyBadge status={o.refund_status} />}
                    </p>
                    <p className="mt-0.5 truncate text-muted">
                      {o.event_name} · {o.category} · {o.qty} tiket · {formatRupiah(o.total)}
                    </p>
                    <p className="truncate text-muted">
                      {o.buyer_email}
                      {o.paid_at ? ` · lunas ${formatDateTime(o.paid_at)}` : ""}
                    </p>
                  </div>
                  {refundable && (
                    <button type="button" className={btnGhost} onClick={() => (setTarget(o), setReason(""), setDialogError(null), setMsg(null))}>
                      Ajukan refund<span className="sr-only"> order {o.id.slice(0, 8)}</span>
                    </button>
                  )}
                </li>
              );
            })}
          </ul>
          <Pager page={orders.data.page} pageSize={orders.data.page_size} total={orders.data.total} onPage={setPage} />
        </>
      )}

      <h3 className="pt-2 font-semibold">Status refund</h3>
      {refunds.data && refunds.data.items.length > 0 ? (
        <ul className="divide-y divide-line rounded-md border border-line bg-card text-sm">
          {refunds.data.items.map((r) => (
            <li key={r.id} className="p-4">
              <p className="flex flex-wrap items-center gap-2">
                <b className="font-mono">#{r.order_id.slice(0, 8)}</b> <MoneyBadge status={r.status} /> <span className="tabular-nums">{formatRupiah(r.amount)}</span>
              </p>
              <p className="mt-0.5 break-words text-muted">
                {r.event_name} · {formatDateTime(r.created_at)} · {r.reason}
              </p>
              {r.reject_reason && <p className="mt-1 text-red-800">Ditolak: {r.reject_reason}</p>}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-muted">Belum ada refund.</p>
      )}

      <ConfirmDialog
        open={target !== null}
        title="Ajukan refund?"
        confirmLabel="Ajukan refund"
        danger
        busy={busy}
        disabled={len < 10 || len > 500}
        onClose={() => setTarget(null)}
        onConfirm={submit}
      >
        {target && (
          <p>
            Order <b className="font-mono">#{target.id.slice(0, 8)}</b> · {target.qty} tiket · <b>{formatRupiah(target.total)}</b> dikembalikan penuh ke pembeli.
          </p>
        )}
        <p className="rounded-md border border-amber-300 bg-amber-50 p-3 text-amber-900">
          Setelah disetujui, <b>semua tiket order ini langsung tidak berlaku</b> dan bagian penjualanmu dari order ini ditarik dari saldo.
        </p>
        <label className="block">
          <span className="mb-1 block font-semibold">
            Alasan<span className="ml-0.5 text-hivis" aria-hidden>*</span>
          </span>
          <textarea className={`${inputCls} min-h-28`} maxLength={500} value={reason} onChange={(e) => setReason(e.target.value)} aria-describedby="hitung-refund" />
          <span id="hitung-refund" className={`mt-1 block text-xs tabular-nums ${len > 0 && len < 10 ? "text-red-700" : "text-muted"}`}>
            {len}/500 karakter{len < 10 ? " · minimal 10" : ""}
          </span>
        </label>
        {dialogError && <ErrorBox message={dialogError} />}
      </ConfirmDialog>
    </section>
  );
}
