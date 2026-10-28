import { Suspense } from "react";
import { OrganizerProfile } from "@/components/organizers";
import { Loading } from "@/components/ui";

export default function OrganizerPage() {
  return (
    <Suspense fallback={<Loading />}>
      <OrganizerProfile />
    </Suspense>
  );
}
