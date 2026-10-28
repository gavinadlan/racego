import type { Metadata } from "next";
import { Suspense } from "react";
import { OrgTeam } from "@/components/org-team";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Tim" };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <OrgTeam />
    </Suspense>
  );
}
