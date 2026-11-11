import type { Metadata } from "next";
import { Suspense } from "react";
import { Draw } from "@/components/draw";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Undian doorprize" };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <Draw />
    </Suspense>
  );
}
