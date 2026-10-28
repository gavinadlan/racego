// Memuat Midtrans Snap.js sesuai kebutuhan dan membuka popup pembayaran.

type SnapCallbacks = {
  onSuccess?: () => void;
  onPending?: () => void;
  onError?: () => void;
  onClose?: () => void;
};

declare global {
  interface Window {
    snap?: { pay: (token: string, callbacks: SnapCallbacks) => void };
  }
}

const SNAP_URL =
  process.env.NEXT_PUBLIC_MIDTRANS_IS_PRODUCTION === "true"
    ? "https://app.midtrans.com/snap/snap.js"
    : "https://app.sandbox.midtrans.com/snap/snap.js";

let loading: Promise<void> | null = null;

function loadSnap(): Promise<void> {
  if (window.snap) return Promise.resolve();
  if (!loading) {
    loading = new Promise((resolve, reject) => {
      const s = document.createElement("script");
      s.src = SNAP_URL;
      s.setAttribute("data-client-key", process.env.NEXT_PUBLIC_MIDTRANS_CLIENT_KEY ?? "");
      s.onload = () => resolve();
      s.onerror = () => {
        loading = null;
        reject(new Error("Gagal memuat Midtrans Snap"));
      };
      document.body.appendChild(s);
    });
  }
  return loading;
}

export async function openSnap(token: string, callbacks: SnapCallbacks) {
  await loadSnap();
  window.snap!.pay(token, callbacks);
}
