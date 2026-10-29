import type { Metadata } from "next";
import { Suspense } from "react";
import { AdminDetail } from "@/components/admin";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Tinjau verifikasi", robots: { index: false } };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <AdminDetail />
    </Suspense>
  );
}
