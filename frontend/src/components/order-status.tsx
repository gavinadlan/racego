"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useState } from "react";
import { apiFetch, ApiError, type Order } from "@/lib/api";
import { formatCountdown, formatRupiah } from "@/lib/format";
import { openSnap } from "@/lib/snap";
import { useApi } from "@/lib/use-api";
import { useSession } from "./session";
import { ErrorBox, Loading, LoginHint, PageTitle, StatusBadge } from "./ui";

export function OrderStatusView() {
  const { orderId } = useParams<{ orderId: string }>();
  const { ready, userId } = useSession();
  const { data: order, error, reload } = useApi<Order>(userId ? `/v1/orders/${orderId}` : null, [userId]);
  const [now, setNow] = useState(() => Date.now());
  const [simError, setSimError] = useState<string | null>(null);

  // Status lunas datang dari webhook (asinkron) — polling tiap 2 detik selama masih pending.
  useEffect(() => {
    if (order?.status !== "pending") return;
    const t = setInterval(() => {
      setNow(Date.now());
      reload();
    }, 2000);
    return () => clearInterval(t);
  }, [order?.status, reload]);

  if (!ready) return <Loading />;
  if (!userId) return <LoginHint />;
  if (error) return <ErrorBox message={error} />;
  if (!order) return <Loading />;

  async function simulate(status: "settlement" | "expire") {
    setSimError(null);
    try {
      await apiFetch(`/v1/dev/orders/${orderId}/simulate`, { method: "POST", body: { status } });
      reload();
    } catch (e) {
      setSimError(e instanceof ApiError ? e.message : "Gagal simulasi");
    }
  }

  const remaining = new Date(order.expires_at).getTime() - now;
  return (
    <>
      <PageTitle title="Status order" back={{ href: "/", label: "Event" }} />
      <div className="max-w-xl rounded-md border border-line bg-card p-6">
        <div className="flex items-center justify-between">
          <StatusBadge status={order.status} />
          <span className="font-display text-xs text-muted">{order.order_id.slice(0, 8)}</span>
        </div>
        <p className="mt-4 text-3xl font-semibold">{formatRupiah(order.total)}</p>

        {order.status === "pending" && (
          <>
            <p className="mt-2 text-sm text-muted">
              Selesaikan pembayaran dalam <span className="font-display font-semibold text-asphalt">{formatCountdown(remaining)}</span>. Halaman ini
              akan berubah otomatis begitu pembayaran diterima.
            </p>
            {order.mock ? (
              <div className="mt-5 rounded-md border border-dashed border-amber-300 bg-amber-50 p-4">
                <p className="text-sm font-medium text-amber-900">Mode mock — Midtrans belum dihubungkan</p>
                <p className="mt-1 text-xs text-amber-800">Tombol ini mengirim notifikasi bertanda tangan valid, sama seperti webhook Midtrans.</p>
                <div className="mt-3 flex gap-2">
                  <button className="rounded-md bg-hivis px-4 py-2 text-sm font-semibold text-asphalt" onClick={() => simulate("settlement")}>
                    Simulasikan bayar berhasil
                  </button>
                  <button className="rounded-md border border-line bg-card px-4 py-2 text-sm" onClick={() => simulate("expire")}>
                    Simulasikan kedaluwarsa
                  </button>
                </div>
                {simError && <p className="mt-2 text-sm text-red-700">{simError}</p>}
              </div>
            ) : (
              order.snap_token && (
                <button
                  className="mt-5 rounded-md bg-hivis px-4 py-2 text-sm font-semibold text-asphalt"
                  onClick={() => openSnap(order.snap_token!, { onClose: reload, onSuccess: reload, onPending: reload })}
                >
                  Buka pembayaran
                </button>
              )
            )}
          </>
        )}

        {order.status === "paid" && (
          <div className="mt-4">
            <p className="text-sm text-muted">Pembayaran diterima. Tiket QR sedang diterbitkan dan dikirim ke email.</p>
            <Link href="/tickets" className="mt-4 inline-block rounded-md bg-hivis px-4 py-2 text-sm font-semibold text-asphalt">
              Lihat tiket saya →
            </Link>
          </div>
        )}
        {(order.status === "expired" || order.status === "cancelled") && (
          <p className="mt-4 text-sm text-muted">Order tidak dibayar sehingga slot dikembalikan. Silakan daftar ulang bila kuota masih ada.</p>
        )}
        {order.status === "needs_refund" && (
          <p className="mt-4 text-sm text-muted">Pembayaran masuk setelah kuota habis. Dana akan dikembalikan oleh penyelenggara.</p>
        )}
      </div>
    </>
  );
}
