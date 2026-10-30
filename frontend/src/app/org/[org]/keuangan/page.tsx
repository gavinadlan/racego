import type { Metadata } from "next";
import { Suspense } from "react";
import { OrgFinance } from "@/components/finance";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Keuangan" };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <OrgFinance />
    </Suspense>
  );
}
