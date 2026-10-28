import type { Metadata } from "next";
import { Suspense } from "react";
import { OrgProfileTab } from "@/components/org-profile";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Profil penyelenggara" };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <OrgProfileTab />
    </Suspense>
  );
}
