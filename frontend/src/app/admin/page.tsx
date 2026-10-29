import type { Metadata } from "next";
import { Suspense } from "react";
import { AdminQueue } from "@/components/admin";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Admin", robots: { index: false } };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <AdminQueue />
    </Suspense>
  );
}
