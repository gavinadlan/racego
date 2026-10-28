import type { Metadata } from "next";
import { Suspense } from "react";
import { PartnerSignup } from "@/components/partner-signup";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Daftarkan penyelenggara" };

export default function PartnerSignupPage() {
  return (
    <Suspense fallback={<Loading />}>
      <PartnerSignup />
    </Suspense>
  );
}
