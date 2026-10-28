import type { Metadata } from "next";
import { OrganizerDirectory } from "@/components/organizers";

export const metadata: Metadata = { title: "Penyelenggara" };

export default function OrganizersPage() {
  return <OrganizerDirectory />;
}
