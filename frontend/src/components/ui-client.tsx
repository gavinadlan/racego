"use client";

import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { assetUrl } from "@/lib/api";
import { btnDark, btnGhost } from "./ui";

/**
 * Dialog konfirmasi memakai <dialog> bawaan browser: fokus terkunci di dalam, Escape menutup,
 * fokus kembali ke tombol pemicu, latar tidak bisa di-scroll (globals.css). Dipakai untuk aksi yang
 * tidak bisa dibatalkan. Satu-satunya modal di aplikasi: semua dialog lewat komponen ini.
 */
export function ConfirmDialog({
  open,
  title,
  children,
  confirmLabel,
  danger,
  busy,
  disabled,
  onConfirm,
  onClose,
}: {
  open: boolean;
  title: string;
  children: React.ReactNode;
  confirmLabel: string;
  danger?: boolean;
  busy?: boolean;
  disabled?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const onCloseRef = useRef(onClose);
  useLayoutEffect(() => {
    onCloseRef.current = onClose;
  });
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);
  // Navigasi: Next.js (cacheComponents) tidak membongkar halaman lama, hanya menyembunyikannya dengan
  // <Activity>. Dialog modal yang masih terbuka di halaman tersembunyi membuat halaman baru inert
  // (tidak bisa diklik). Cleanup ini jalan sinkron saat halaman disembunyikan/dibongkar: tutup dialog
  // dan reset state pemanggil, jadi tautan/router.push dari dalam dialog aman dan dialog tidak
  // terbuka lagi saat kembali (back).
  useLayoutEffect(() => {
    const d = ref.current;
    return () => {
      if (d?.open) {
        d.close();
        onCloseRef.current();
      }
    };
  }, []);
  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onClose={onClose}
      className="m-auto w-[min(32rem,calc(100vw-2rem))] rounded-md bg-card p-0 text-asphalt shadow-xl ring-1 ring-line backdrop:bg-asphalt/50"
    >
      <div className="p-6">
        <h2 id={titleId} className="font-display text-3xl leading-none font-extrabold">
          {title}
        </h2>
        <div className="mt-4 space-y-3 text-sm">{children}</div>
        <div className="mt-6 flex flex-wrap justify-end gap-3">
          <button type="button" className={btnGhost} onClick={onClose} autoFocus>
            Batal
          </button>
          <button
            type="button"
            className={danger ? "inline-flex items-center justify-center rounded-md bg-red-700 px-4 py-2 text-sm font-semibold text-white hover:bg-red-800 disabled:opacity-40" : btnDark}
            disabled={busy || disabled}
            onClick={onConfirm}
          >
            {busy ? "Memproses…" : confirmLabel}
          </button>
        </div>
      </div>
    </dialog>
  );
}

/**
 * Gambar unggahan (poster/logo). Ukuran kotak ditentukan pemanggil lewat className, jadi bila
 * gambar gagal dimuat isinya diganti fallback tanpa membuat tata letak bergeser.
 */
export function UploadedImage({ url, alt, className = "", fit = "cover", fallback = null }: { url: string; alt: string; className?: string; fit?: "cover" | "contain"; fallback?: React.ReactNode }) {
  const [failed, setFailed] = useState<string | null>(null);
  if (failed === url) return <>{fallback}</>;
  return (
    // eslint-disable-next-line @next/next/no-img-element -- gambar dari API (bukan aset Next), sudah di-cache immutable
    <img src={assetUrl(url)} alt={alt} className={`${fit === "cover" ? "object-cover" : "object-contain"} ${className}`} loading="lazy" onError={() => setFailed(url)} />
  );
}
