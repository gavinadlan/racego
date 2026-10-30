import type { Metadata } from "next";
import { Suspense } from "react";
import { AdminPayouts } from "@/components/admin-finance";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Pencairan", robots: { index: false } };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <AdminPayouts />
    </Suspense>
  );
}
