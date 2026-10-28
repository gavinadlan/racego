import type { Metadata } from "next";
import { MyTickets } from "@/components/my-tickets";

export const metadata: Metadata = { title: "Tiket saya" };

export default function TicketsPage() {
  return <MyTickets />;
}
