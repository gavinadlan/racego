import type { Metadata } from "next";
import { Suspense } from "react";
import { Loading } from "@/components/ui";
import { OrgVerification } from "@/components/verification";

export const metadata: Metadata = { title: "Verifikasi penyelenggara" };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <OrgVerification />
    </Suspense>
  );
}
