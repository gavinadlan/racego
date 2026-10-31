import type { Metadata } from "next";
import { Suspense } from "react";
import { Participants } from "@/components/participants";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Peserta" };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <Participants />
    </Suspense>
  );
}
