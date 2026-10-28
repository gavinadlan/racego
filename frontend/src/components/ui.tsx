import Link from "next/link";
import { LoginLink } from "./login-link";

export function ErrorBox({ message }: { message: string }) {
  return <div className="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800">{message}</div>;
}

export function Loading({ label = "Memuat…" }: { label?: string }) {
  return <p className="animate-pulse py-6 text-sm text-muted">{label}</p>;
}

export function LoginHint() {
  return (
    <div className="flex flex-wrap items-center justify-between gap-4 rounded-md border border-dashed border-line bg-card p-6 text-sm">
      <p className="text-muted">
        <b className="text-asphalt">Masuk dulu untuk melanjutkan.</b> Cukup pakai email, kami kirim kode 6 digit. Belum punya akun? Akun dibuat otomatis.
      </p>
      <LoginLink className={btnDark}>Masuk</LoginLink>
    </div>
  );
}

export function PageTitle({ title, back, children }: { title: string; back?: { href: string; label: string }; children?: React.ReactNode }) {
  return (
    <div className="mb-8">
      {back && (
        <Link href={back.href} className="text-sm text-muted hover:text-asphalt">
          ← {back.label}
        </Link>
      )}
      <h1 className="font-display mt-1 text-5xl leading-[0.95] font-extrabold sm:text-6xl">{title}</h1>
      {children}
    </div>
  );
}

/** Judul section: judul display + tautan opsional di kanan. */
export function SectionHead({ title, intro, link }: { title: string; intro?: string; link?: { href: string; label: string } }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-x-6 gap-y-2">
      <div>
        <h2 className="font-display text-4xl leading-none font-extrabold sm:text-5xl">{title}</h2>
        {intro && <p className="mt-2 max-w-xl text-muted">{intro}</p>}
      </div>
      {link && (
        <Link href={link.href} className="text-sm font-semibold underline decoration-hivis decoration-2 underline-offset-4 hover:text-hivis">
          {link.label}
        </Link>
      )}
    </div>
  );
}

export const btnPrimary =
  "inline-flex items-center justify-center rounded-md bg-hivis px-5 py-2.5 font-semibold text-asphalt transition-colors hover:bg-[#ff7445] disabled:cursor-not-allowed disabled:opacity-40";
export const btnDark =
  "inline-flex items-center justify-center rounded-md bg-asphalt px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-road disabled:cursor-not-allowed disabled:opacity-40";
export const btnGhost =
  "inline-flex items-center justify-center rounded-md border border-line bg-card px-4 py-2 text-sm font-medium hover:border-asphalt";
export const inputCls =
  "w-full rounded-md border border-line bg-card px-3 py-2.5 text-sm focus:border-asphalt focus:outline-none";

const statusStyle: Record<string, string> = {
  issued: "bg-go-soft text-go",
  collected: "bg-sky-100 text-sky-800",
  paid: "bg-go-soft text-go",
  pending: "bg-hivis-soft text-[#a83a10]",
  published: "bg-go-soft text-go",
  open: "bg-go-soft text-go",
  upcoming: "bg-hivis-soft text-[#a83a10]",
};

const statusLabel: Record<string, string> = {
  issued: "Aktif",
  collected: "Race pack diambil",
  paid: "Lunas",
  pending: "Menunggu bayar",
  expired: "Kedaluwarsa",
  cancelled: "Dibatalkan",
  refunded: "Di-refund",
  needs_refund: "Perlu refund",
  open: "Pendaftaran dibuka",
  upcoming: "Segera dibuka",
  closed: "Pendaftaran ditutup",
  finished: "Selesai",
  published: "Terbit",
  draft: "Draft",
};

export function StatusBadge({ status }: { status: string }) {
  return (
    <span className={`inline-block rounded-full px-2.5 py-0.5 text-xs font-semibold whitespace-nowrap ${statusStyle[status] ?? "bg-line text-muted"}`}>
      {statusLabel[status] ?? status}
    </span>
  );
}

/**
 * Label yang selalu terlihat di atas kolom, keterangan singkat, dan pesan error dari server.
 * labelClass mis. "lg:sr-only" untuk tabel yang judul kolomnya sudah ada di baris kepala.
 */
export function Field({
  label,
  required,
  hint,
  error,
  labelClass = "",
  children,
}: {
  label: string;
  required?: boolean;
  hint?: string;
  error?: string;
  labelClass?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block min-w-0">
      <span className={`mb-1 block text-sm font-semibold ${labelClass}`}>
        {label}
        {required ? (
          <span className="ml-0.5 text-hivis" aria-hidden>
            *
          </span>
        ) : (
          <span className="ml-1 font-normal text-muted">(opsional)</span>
        )}
      </span>
      {children}
      {hint && !error && <span className="mt-1 block text-xs text-muted">{hint}</span>}
      {error && (
        <span className="mt-1 block text-xs font-medium text-red-700" role="alert">
          {error}
        </span>
      )}
    </label>
  );
}

/** Kelas input dengan tanda merah bila ada error. */
export const inputErr = (error?: string) => `${inputCls} ${error ? "border-red-500 focus:border-red-600" : ""}`;

