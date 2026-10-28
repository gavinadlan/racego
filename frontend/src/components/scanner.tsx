"use client";

import { useParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { apiFetch, ApiError, type ScanResult, type ScanTicket } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { useApi } from "@/lib/use-api";
import { useSession } from "./session";
import { ErrorBox, Loading, LoginHint, PageTitle } from "./ui";

type Stats = { stats: { issued: number; collected: number }; results: ScanTicket[] | null };

const tone: Record<string, string> = {
  ok: "border-emerald-400 bg-emerald-50 text-emerald-900",
  already_collected: "border-amber-400 bg-amber-50 text-amber-900",
};

export function Scanner() {
  const { org, eventId } = useParams<{ org: string; eventId: string }>();
  const { ready, userId } = useSession();
  const base = `/v1/org/${org}/events/${eventId}`;
  const [query, setQuery] = useState("");
  const { data, error, reload } = useApi<Stats>(userId ? `${base}/tickets?q=${encodeURIComponent(query)}` : null, [userId]);

  const [result, setResult] = useState<ScanResult | null>(null);
  const [pickedUpBy, setPickedUpBy] = useState("");
  const [manualToken, setManualToken] = useState("");
  const [cameraOn, setCameraOn] = useState(false);
  const [cameraError, setCameraError] = useState<string | null>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const lastToken = useRef<{ token: string; at: number } | null>(null);
  const busy = useRef(false);

  const submit = useCallback(
    async (body: { token?: string; ticket_id?: string }) => {
      if (busy.current) return;
      busy.current = true;
      try {
        const res = await apiFetch<ScanResult>(`${base}/scan`, {
          method: "POST",
          body: { ...body, picked_up_by: pickedUpBy || undefined },
        });
        setResult(res);
        if (res.result === "ok") setPickedUpBy("");
        reload();
      } catch (e) {
        setResult({ result: "error", message: e instanceof ApiError ? e.message : "Gagal memproses" });
      } finally {
        busy.current = false;
      }
    },
    [base, pickedUpBy, reload],
  );

  // Kamera: @zxing/browser bekerja di Chrome, Safari iOS, dan Firefox.
  // Butuh HTTPS atau localhost agar browser mengizinkan akses kamera.
  useEffect(() => {
    if (!cameraOn || !videoRef.current) return;
    let stop: (() => void) | undefined;
    let cancelled = false;
    (async () => {
      try {
        const { BrowserQRCodeReader } = await import("@zxing/browser");
        const reader = new BrowserQRCodeReader();
        const controls = await reader.decodeFromVideoDevice(undefined, videoRef.current!, (res) => {
          if (!res) return;
          const token = res.getText();
          const now = Date.now();
          // Abaikan QR yang sama selama 3 detik (kamera membaca berkali-kali per detik).
          if (lastToken.current?.token === token && now - lastToken.current.at < 3000) return;
          lastToken.current = { token, at: now };
          submit({ token });
        });
        if (cancelled) controls.stop();
        else stop = () => controls.stop();
      } catch {
        setCameraError("Kamera tidak bisa dibuka. Izinkan akses kamera, atau pakai input manual di bawah.");
        setCameraOn(false);
      }
    })();
    return () => {
      cancelled = true;
      stop?.();
    };
  }, [cameraOn, submit]);

  if (!ready) return <Loading />;
  if (!userId) return <LoginHint />;

  const t = result?.ticket;
  return (
    <>
      <PageTitle title="Scan race pack" back={{ href: `/org/${org}`, label: "Dashboard mitra" }}>
        {data && (
          <p className="mt-2 text-sm text-muted">
            Diambil <b className="text-asphalt tabular-nums">{data.stats.collected}</b> dari {data.stats.issued} tiket
          </p>
        )}
      </PageTitle>
      {error && <ErrorBox message={error} />}

      <div className="grid gap-6 lg:grid-cols-2">
        <section className="space-y-4">
          <div className="overflow-hidden rounded-md border border-line bg-asphalt">
            {cameraOn ? (
              <video ref={videoRef} className="aspect-square w-full object-cover" muted playsInline />
            ) : (
              <button className="grid aspect-square w-full place-items-center text-hivis" onClick={() => { setCameraError(null); setCameraOn(true); }}>
                <span className="rounded-md border border-hivis/40 px-4 py-2 font-semibold">Nyalakan kamera</span>
              </button>
            )}
          </div>
          {cameraOn && (
            <button className="text-sm text-muted underline" onClick={() => setCameraOn(false)}>
              Matikan kamera
            </button>
          )}
          {cameraError && <ErrorBox message={cameraError} />}
          <label className="block text-sm">
            <span className="text-muted">Diwakilkan? Nama pengambil (opsional, berlaku untuk scan berikutnya)</span>
            <input className="mt-1 w-full rounded-md border border-line bg-card px-3 py-2" value={pickedUpBy} onChange={(e) => setPickedUpBy(e.target.value)} />
          </label>
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (manualToken.trim()) submit({ token: manualToken.trim() });
              setManualToken("");
            }}
          >
            <input
              className="flex-1 rounded-md border border-line bg-card px-3 py-2 font-display text-xs"
              placeholder="Tempel isi QR (v1.…) untuk uji tanpa kamera"
              value={manualToken}
              onChange={(e) => setManualToken(e.target.value)}
            />
            <button className="rounded-md bg-hivis px-3 text-sm font-semibold text-asphalt">Cek</button>
          </form>
        </section>

        <section className="space-y-4">
          <div className={`min-h-48 rounded-md border-2 p-6 ${result ? (tone[result.result] ?? "border-red-400 bg-red-50 text-red-900") : "border-dashed border-line bg-card text-muted"}`}>
            {!result ? (
              <p>Arahkan kamera ke QR tiket peserta.</p>
            ) : (
              <>
                <p className="text-xl font-semibold">{result.message}</p>
                {t && (
                  <dl className="mt-4 grid grid-cols-2 gap-y-1 text-sm">
                    <dt>Nama</dt>
                    <dd className="font-semibold">{t.participant_name}</dd>
                    <dt>Kategori · BIB</dt>
                    <dd className="font-display font-semibold">
                      {t.category_code} · {t.bib_number}
                    </dd>
                    <dt>Jersey</dt>
                    <dd className="text-lg font-bold">{t.jersey_size}</dd>
                    {t.collected_at && (
                      <>
                        <dt>Diambil</dt>
                        <dd>
                          {formatDateTime(t.collected_at)} · oleh {t.collected_by_staff}
                          {t.picked_up_by && ` (diwakilkan ${t.picked_up_by})`}
                        </dd>
                      </>
                    )}
                  </dl>
                )}
                {result.result === "ok" && <p className="mt-4 text-sm">Cocokkan nama dengan KTP sebelum menyerahkan race pack.</p>}
              </>
            )}
          </div>

          <div className="rounded-md border border-line bg-card p-5">
            <p className="text-sm font-semibold">HP pelari mati? Cari manual</p>
            <form
              className="mt-2 flex gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                reload();
              }}
            >
              <input
                className="flex-1 rounded-md border border-line px-3 py-2 text-sm"
                placeholder="Nama, nomor BIB, atau 4 digit akhir NIK"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
              <button className="rounded-md border border-line px-3 text-sm">Cari</button>
            </form>
            <ul className="mt-3 divide-y divide-line text-sm">
              {data?.results?.map((r) => (
                <li key={r.id} className="flex items-center justify-between gap-2 py-2">
                  <span>
                    {r.participant_name} <span className="font-display text-muted">{r.category_code} · {r.bib_number}</span>
                  </span>
                  {r.status === "issued" ? (
                    <button className="rounded-md bg-hivis px-2.5 py-1 text-xs font-semibold text-asphalt" onClick={() => submit({ ticket_id: r.id })}>
                      Tandai diambil
                    </button>
                  ) : (
                    <span className="text-xs text-muted">{r.status === "collected" ? "sudah diambil" : r.status}</span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        </section>
      </div>
    </>
  );
}
