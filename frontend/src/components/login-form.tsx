"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { ApiError, getDevUserId, requestOtp, safeNext, setDevUserId, verifyOtp } from "@/lib/api";
import { useSession } from "./session";
import { btnGhost, btnPrimary, ErrorBox, inputCls, Loading, PageTitle } from "./ui";

/** Masuk = daftar: email → kode 6 digit dari email → masuk. */
export function LoginForm() {
  const router = useRouter();
  const next = safeNext(useSearchParams().get("next"));
  const { ready, me, refresh } = useSession();
  const [step, setStep] = useState<"email" | "code">("email");
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [resendAt, setResendAt] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  const loggingIn = useRef(false);
  const codeInput = useRef<HTMLInputElement>(null);

  // Sudah masuk (mis. membuka /masuk lagi): langsung ke tujuan.
  useEffect(() => {
    if (ready && me && !loggingIn.current) router.replace(next);
  }, [ready, me, next, router]);

  const waiting = Math.max(0, Math.ceil((resendAt - now) / 1000));
  useEffect(() => {
    if (resendAt <= Date.now()) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [resendAt]);

  function holdResend(seconds: number) {
    setNow(Date.now());
    setResendAt(Date.now() + seconds * 1000);
  }

  async function send(e?: React.FormEvent) {
    e?.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await requestOtp(email);
      holdResend(res.resend_after);
      setCode("");
      setStep("code");
      requestAnimationFrame(() => codeInput.current?.focus());
    } catch (err) {
      if (err instanceof ApiError && err.retryAfter) holdResend(err.retryAfter);
      setError(err instanceof ApiError ? err.message : "Gagal mengirim kode");
    } finally {
      setBusy(false);
    }
  }

  async function verify(value: string) {
    if (busy) return;
    setBusy(true);
    setError(null);
    loggingIn.current = true;
    try {
      const res = await verifyOtp(email, value);
      if (getDevUserId()) setDevUserId(null); // login sungguhan menggantikan akun contoh
      await refresh();
      router.replace(res.is_new ? `/akun?baru=1&next=${encodeURIComponent(next)}` : next);
    } catch (err) {
      loggingIn.current = false;
      setCode("");
      setError(err instanceof ApiError ? err.message : "Gagal memeriksa kode");
      setBusy(false);
      requestAnimationFrame(() => codeInput.current?.focus());
    }
  }

  if (!ready || me) return <Loading label={me ? "Mengalihkan…" : undefined} />;

  return (
    <div className="mx-auto max-w-md">
      <PageTitle title="Masuk">
        <p className="mt-3 text-muted">Pakai email saja, tanpa kata sandi. Belum punya akun? Akun dibuat otomatis saat kamu pertama kali masuk.</p>
      </PageTitle>

      <div className="rounded-md bg-card p-6 ring-1 ring-line">
        {step === "email" ? (
          <form onSubmit={send} className="space-y-4">
            <label className="block">
              <span className="mb-1 block text-sm font-semibold">Email</span>
              <input
                className={inputCls}
                type="email"
                autoComplete="email"
                required
                autoFocus
                placeholder="nama@email.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </label>
            {error && <ErrorBox message={error} />}
            <button type="submit" className={`${btnPrimary} w-full`} disabled={busy || !email}>
              {busy ? "Mengirim…" : "Kirim kode"}
            </button>
          </form>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              verify(code);
            }}
            className="space-y-4"
          >
            <p className="text-sm">
              Kode dikirim ke <b className="wrap-anywhere">{email}</b>. Cek juga folder spam. Kode berlaku 10 menit.
            </p>
            <label className="block">
              <span className="mb-1 block text-sm font-semibold">Kode 6 digit</span>
              <input
                ref={codeInput}
                className={`${inputCls} font-display text-center text-3xl tracking-[0.5em]`}
                inputMode="numeric"
                autoComplete="one-time-code"
                pattern="[0-9]{6}"
                maxLength={6}
                required
                placeholder="••••••"
                value={code}
                disabled={busy}
                onChange={(e) => {
                  const v = e.target.value.replace(/\D/g, "").slice(0, 6);
                  setCode(v);
                  if (v.length === 6) verify(v);
                }}
              />
            </label>
            {error && <ErrorBox message={error} />}
            <button type="submit" className={`${btnPrimary} w-full`} disabled={busy || code.length !== 6}>
              {busy ? "Memeriksa…" : "Masuk"}
            </button>
            <div className="flex flex-wrap items-center justify-between gap-3 text-sm">
              <button
                type="button"
                className="underline underline-offset-4 hover:text-hivis"
                onClick={() => {
                  setStep("email");
                  setError(null);
                }}
              >
                Ganti email
              </button>
              <button type="button" className={btnGhost} disabled={busy || waiting > 0} onClick={() => send()}>
                {waiting > 0 ? `Kirim ulang (${waiting} dtk)` : "Kirim ulang"}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
