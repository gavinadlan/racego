import type { Metadata } from "next";
import { Suspense } from "react";
import { EditEventPage } from "@/components/event-form";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Edit event" };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <EditEventPage />
    </Suspense>
  );
}
