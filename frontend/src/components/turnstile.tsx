"use client";

import { useEffect, useRef, useState } from "react";

/**
 * Widget anti-bot Cloudflare Turnstile. Hanya aktif bila NEXT_PUBLIC_TURNSTILE_SITE_KEY diisi;
 * kosong = tidak ada skrip yang dimuat dan komponen ini tidak merender apa pun.
 * Token sekali pakai: setelah dipakai (berhasil atau gagal), pemanggil me-reset dengan mengganti `key`.
 * Docs: https://developers.cloudflare.com/turnstile/get-started/client-side-rendering/
 */
export const TURNSTILE_SITE_KEY = process.env.NEXT_PUBLIC_TURNSTILE_SITE_KEY ?? "";
export const turnstileEnabled = TURNSTILE_SITE_KEY !== "";

const SCRIPT_URL = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";

type TurnstileApi = {
  render: (el: HTMLElement, opts: Record<string, unknown>) => string;
  remove: (id: string) => void;
};
declare global {
  interface Window {
    turnstile?: TurnstileApi;
  }
}

let loader: Promise<TurnstileApi> | null = null;

/** Muat api.js sekali (async + defer, tidak memblokir render). */
function loadScript(): Promise<TurnstileApi> {
  if (window.turnstile) return Promise.resolve(window.turnstile);
  loader ??= new Promise((resolve, reject) => {
    const s = document.createElement("script");
    s.src = SCRIPT_URL;
    s.async = true;
    s.defer = true;
    s.onload = () => (window.turnstile ? resolve(window.turnstile) : reject(new Error("turnstile")));
    s.onerror = () => {
      loader = null; // boleh dicoba lagi setelah remount
      s.remove();
      reject(new Error("turnstile"));
    };
    document.head.appendChild(s);
  });
  return loader;
}

export function Turnstile({ onToken, theme = "light" }: { onToken: (token: string | null) => void; theme?: "light" | "dark" }) {
  const box = useRef<HTMLDivElement>(null);
  const onTokenRef = useRef(onToken);
  useEffect(() => {
    onTokenRef.current = onToken;
  });
  const [status, setStatus] = useState<"checking" | "ok" | "error">("checking");

  useEffect(() => {
    if (!turnstileEnabled) return;
    let id: string | null = null;
    let cancelled = false;
    const fail = () => {
      onTokenRef.current(null);
      setStatus("error");
    };
    loadScript().then((ts) => {
      if (cancelled || !box.current) return;
      id = ts.render(box.current, {
        sitekey: TURNSTILE_SITE_KEY,
        theme,
        language: "id",
        callback: (token: string) => {
          onTokenRef.current(token);
          setStatus("ok");
        },
        "expired-callback": () => {
          onTokenRef.current(null);
          setStatus("checking");
        },
        "error-callback": () => {
          fail();
          return true; // ditangani di sini (pesan di bawah widget)
        },
      });
    }, fail);
    return () => {
      cancelled = true;
      if (id) window.turnstile?.remove(id);
      onTokenRef.current(null);
    };
  }, [theme]);

  if (!turnstileEnabled) return null;
  return (
    <div role="group" aria-label="Verifikasi keamanan" className="min-w-0">
      <div ref={box} className="min-h-[65px] max-w-full overflow-hidden" />
      <p className={`mt-1 text-xs ${status === "error" ? "font-medium text-red-700" : "opacity-70"}`} role={status === "error" ? "alert" : undefined} aria-live="polite">
        {status === "error"
          ? "Verifikasi keamanan gagal dimuat. Periksa koneksi lalu muat ulang halaman."
          : status === "checking"
            ? "Memeriksa bahwa kamu bukan bot…"
            : "Verifikasi keamanan selesai."}
      </p>
    </div>
  );
}

/** Token + reset untuk satu formulir. reset() memasang widget baru (token lama sudah terpakai). */
export function useTurnstile() {
  const [token, setToken] = useState<string | null>(null);
  const [key, setKey] = useState(0);
  return {
    token,
    /** true bila formulir boleh dikirim (fitur mati, atau token sudah ada). */
    ready: !turnstileEnabled || token !== null,
    reset: () => {
      setToken(null);
      setKey((k) => k + 1);
    },
    widget: (theme?: "light" | "dark") => <Turnstile key={key} onToken={setToken} theme={theme} />,
  };
}
