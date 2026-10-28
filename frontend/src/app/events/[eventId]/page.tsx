import { Suspense } from "react";
import { EventDetailView } from "@/components/event-detail";
import { Loading } from "@/components/ui";

export default function EventPage() {
  return (
    <Suspense fallback={<Loading />}>
      <EventDetailView />
    </Suspense>
  );
}
