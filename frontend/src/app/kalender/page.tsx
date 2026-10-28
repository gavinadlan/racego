import type { Metadata } from "next";
import { Suspense } from "react";
import { Calendar } from "@/components/calendar";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Kalender race" };

export default function CalendarPage() {
  return (
    <Suspense fallback={<Loading />}>
      <Calendar />
    </Suspense>
  );
}
