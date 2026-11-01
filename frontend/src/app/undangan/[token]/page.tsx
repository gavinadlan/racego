import type { Metadata } from "next";
import { Suspense } from "react";
import { InvitationPage } from "@/components/invitation";
import { Loading } from "@/components/ui";

// Tautan berisi token rahasia: jangan diindeks, jangan kirim URL lengkap sebagai referrer.
export const metadata: Metadata = { title: "Undangan tim", robots: { index: false, follow: false }, referrer: "no-referrer" };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <InvitationPage />
    </Suspense>
  );
}
