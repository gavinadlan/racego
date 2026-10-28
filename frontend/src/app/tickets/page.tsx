import type { Metadata } from "next";
import { Suspense } from "react";
import { MyTickets } from "@/components/my-tickets";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Tiket saya" };

export default function TicketsPage() {
  return (
    <Suspense fallback={<Loading />}>
      <MyTickets />
    </Suspense>
  );
}
