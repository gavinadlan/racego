import { Suspense } from "react";
import { Scanner } from "@/components/scanner";
import { Loading } from "@/components/ui";

export default function ScanPage() {
  return (
    <Suspense fallback={<Loading />}>
      <Scanner />
    </Suspense>
  );
}
