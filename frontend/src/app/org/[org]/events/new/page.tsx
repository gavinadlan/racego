import type { Metadata } from "next";
import { Suspense } from "react";
import { NewEventPage } from "@/components/event-form";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Buat event" };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <NewEventPage />
    </Suspense>
  );
}
