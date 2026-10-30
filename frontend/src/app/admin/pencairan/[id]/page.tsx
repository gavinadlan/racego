import type { Metadata } from "next";
import { Suspense } from "react";
import { AdminPayoutDetail } from "@/components/admin-finance";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Tinjau pencairan", robots: { index: false } };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <AdminPayoutDetail />
    </Suspense>
  );
}
