"use client";

import { useId, useRef, useState } from "react";
import { ApiError, uploadImage } from "@/lib/api";
import { btnGhost } from "./ui";
import { UploadedImage } from "./ui-client";

/**
 * Unggah poster/logo: pilih file → langsung diunggah → pratinjau. value = path "/uploads/…" atau "".
 * Validasi sesungguhnya di server (isi file, bukan ekstensi); cek di sini hanya agar pesan cepat muncul.
 */
export function ImageUpload({
  org,
  kind,
  label,
  hint,
  value,
  onChange,
  disabled,
  error,
  boxClass,
}: {
  org: string;
  kind: "cover" | "logo";
  label: string;
  hint: string;
  value: string;
  onChange: (url: string) => void;
  disabled?: boolean;
  error?: string;
  boxClass: string; // ukuran kotak pratinjau, mis. "aspect-[4/5] w-40"
}) {
  const input = useRef<HTMLInputElement>(null);
  const id = useId();
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  async function pick(file: File | undefined) {
    if (!file) return;
    setMsg(null);
    if (file.size > 3 * 1024 * 1024) {
      setMsg("Ukuran file maksimal 3 MB");
      return;
    }
    setBusy(true);
    try {
      onChange((await uploadImage(org, kind, file)).url);
    } catch (err) {
      setMsg(err instanceof ApiError ? err.message : "Gagal mengunggah gambar");
    } finally {
      setBusy(false);
      if (input.current) input.current.value = "";
    }
  }

  const shown = msg ?? error;
  return (
    <div>
      <p id={`${id}-label`} className="mb-1 text-sm font-semibold">
        {label} <span className="font-normal text-muted">(opsional)</span>
      </p>
      <div className="flex flex-wrap items-start gap-4">
        <div className={`grid shrink-0 place-items-center overflow-hidden rounded-md border border-dashed border-line bg-paper text-center text-xs text-muted ${boxClass}`}>
          {value ? <UploadedImage url={value} alt={`Pratinjau ${label.toLowerCase()}`} className="size-full" fit="contain" fallback="Gagal memuat" /> : "Belum ada"}
        </div>
        <div className="min-w-0 flex-1 space-y-2">
          <input
            ref={input}
            id={id}
            type="file"
            accept="image/jpeg,image/png"
            className="sr-only"
            aria-labelledby={`${id}-label`}
            aria-describedby={`${id}-hint`}
            disabled={disabled || busy}
            onChange={(e) => pick(e.target.files?.[0])}
          />
          <div className="flex flex-wrap gap-2">
            <label htmlFor={id} className={`${btnGhost} cursor-pointer ${disabled || busy ? "pointer-events-none opacity-40" : ""}`}>
              {busy ? "Mengunggah…" : value ? "Ganti gambar" : "Pilih gambar"}
            </label>
            {value && !disabled && (
              <button type="button" className={btnGhost} onClick={() => onChange("")}>
                Hapus
              </button>
            )}
          </div>
          <p id={`${id}-hint`} className="text-xs text-muted">
            {hint}
          </p>
          {shown && (
            <p className="text-xs font-medium text-red-700" role="alert">
              {shown}
            </p>
          )}
        </div>
      </div>
    </div>
  );
}
