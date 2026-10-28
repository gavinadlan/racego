"use client";

import Link from "next/link";
import { usePathname, useSearchParams } from "next/navigation";
import { Suspense } from "react";

/** /masuk?next=<halaman sekarang>. Butuh <Suspense> karena membaca URL. */
export function useLoginHref() {
  const path = usePathname();
  const search = useSearchParams().toString();
  return `/masuk?next=${encodeURIComponent(path + (search ? `?${search}` : ""))}`;
}

function CurrentLoginLink(props: { className?: string; children: React.ReactNode }) {
  return <Link href={useLoginHref()} {...props} />;
}

/** Tautan ke halaman masuk yang kembali ke halaman ini setelah login. */
export function LoginLink(props: { className?: string; children: React.ReactNode }) {
  return (
    <Suspense fallback={<Link href="/masuk" {...props} />}>
      <CurrentLoginLink {...props} />
    </Suspense>
  );
}
