import type { Metadata } from "next";
import { Suspense } from "react";
import { AccountView } from "@/components/account";
import { Loading } from "@/components/ui";

export const metadata: Metadata = { title: "Akun" };

export default function AccountPage() {
  return (
    <Suspense fallback={<Loading />}>
      <AccountView />
    </Suspense>
  );
}
