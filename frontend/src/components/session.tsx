"use client";

import { createContext, useCallback, useContext, useSyncExternalStore } from "react";
import { getDevUserId, setDevUserId, type Me } from "@/lib/api";
import { useApi } from "@/lib/use-api";

type Session = {
  ready: boolean; // false saat render server/hydrate, true setelah localStorage terbaca
  userId: string | null;
  me: Me | null;
  switchUser: (id: string | null) => void;
};

const SessionContext = createContext<Session | null>(null);

// User dev disimpan di localStorage; perubahan disiarkan lewat event agar semua komponen ikut.
const CHANGE_EVENT = "racego:user-change";
function subscribe(cb: () => void) {
  window.addEventListener(CHANGE_EVENT, cb);
  window.addEventListener("storage", cb);
  return () => {
    window.removeEventListener(CHANGE_EVENT, cb);
    window.removeEventListener("storage", cb);
  };
}
const noopSubscribe = () => () => {};

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const ready = useSyncExternalStore(noopSubscribe, () => true, () => false);
  const userId = useSyncExternalStore(subscribe, getDevUserId, () => null);
  // userId ikut sebagai dependensi: path sama, tapi user berbeda harus memuat ulang.
  const { data: me } = useApi<Me>(userId ? "/v1/me" : null, [userId]);

  const switchUser = useCallback((id: string | null) => {
    setDevUserId(id);
    window.dispatchEvent(new Event(CHANGE_EVENT));
  }, []);

  return <SessionContext.Provider value={{ ready, userId, me, switchUser }}>{children}</SessionContext.Provider>;
}

export function useSession() {
  const s = useContext(SessionContext);
  if (!s) throw new Error("useSession harus dipakai di dalam SessionProvider");
  return s;
}
