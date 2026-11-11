import type { Metadata } from "next";
import { Suspense } from "react";
import { DrawResults } from "@/components/draw-results";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Hasil undian", robots: { index: false, follow: false } };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <DrawResults />
    </Suspense>
  );
}
