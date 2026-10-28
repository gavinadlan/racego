import { Suspense } from "react";
import { OrgDashboard } from "@/components/org-dashboard";
import { Loading } from "@/components/ui";

export default function OrgPage() {
  return (
    <Suspense fallback={<Loading />}>
      <OrgDashboard />
    </Suspense>
  );
}
