import type { Metadata } from "next";
import { Suspense } from "react";
import { AdminFinance } from "@/components/admin-finance";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Keuangan platform", robots: { index: false } };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <AdminFinance />
    </Suspense>
  );
}
