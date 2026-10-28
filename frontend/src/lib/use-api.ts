"use client";

import { useCallback, useEffect, useState } from "react";
import { apiFetch } from "./api";

type State<T> = { key: string | null; data: T | null; error: string | null };

// Hook kecil untuk GET data dari backend. path null = jangan fetch (mis. belum login).
// Data lama tetap ditampilkan saat reload (tidak berkedip saat polling).
// Untuk kebutuhan lebih (cache, revalidasi saat fokus) pertimbangkan SWR / TanStack Query.
export function useApi<T>(path: string | null, deps: unknown[] = []) {
  const [tick, setTick] = useState(0);
  const key = path ? `${path}#${tick}#${JSON.stringify(deps)}` : null;
  const [state, setState] = useState<State<T>>({ key: null, data: null, error: null });

  useEffect(() => {
    if (!path || !key) return;
    let cancelled = false;
    apiFetch<T>(path)
      .then((data) => !cancelled && setState({ key, data, error: null }))
      .catch((e: Error) => !cancelled && setState((s) => ({ key, data: s.data, error: e.message })));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return {
    data: key ? state.data : null,
    error: key ? state.error : null,
    loading: key !== null && state.key !== key,
    reload,
  };
}
