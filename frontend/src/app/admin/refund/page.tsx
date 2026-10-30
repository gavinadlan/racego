import type { Metadata } from "next";
import { Suspense } from "react";
import { AdminRefunds } from "@/components/admin-finance";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Refund", robots: { index: false } };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <AdminRefunds />
    </Suspense>
  );
}
