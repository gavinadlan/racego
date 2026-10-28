import { Suspense } from "react";
import { OrderStatusView } from "@/components/order-status";
import { Loading } from "@/components/ui";

export default function OrderPage() {
  return (
    <Suspense fallback={<Loading />}>
      <OrderStatusView />
    </Suspense>
  );
}
