"use client";

import { createContext, useCallback, useContext, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { apiFetch, DEV_USER_EVENT, getDevUserId, logout as apiLogout, setDevUserId, type Me } from "@/lib/api";

type Session = {
  ready: boolean; // true setelah GET /v1/auth/session pertama selesai
  userId: string | null;
  me: Me | null;
  refresh: () => Promise<void>; // panggil setelah login / ubah profil
  logout: () => Promise<void>;
  switchUser: (id: string | null) => void; // akun contoh, hanya bila NEXT_PUBLIC_DEV_LOGIN=true
};

const SessionContext = createContext<Session | null>(null);

// Akun contoh disimpan di localStorage; perubahan disiarkan lewat event agar semua komponen ikut.
function subscribe(cb: () => void) {
  window.addEventListener(DEV_USER_EVENT, cb);
  window.addEventListener("storage", cb);
  return () => {
    window.removeEventListener(DEV_USER_EVENT, cb);
    window.removeEventListener("storage", cb);
  };
}

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const devUser = useSyncExternalStore(subscribe, getDevUserId, () => null);
  const [state, setState] = useState<{ me: Me | null } | null>(null);
  const seq = useRef(0);

  // Sumber kebenaran: GET /v1/auth/session (cookie sesi, atau X-User-ID akun contoh); me null = belum masuk.
  // Respons lama diabaikan.
  const refresh = useCallback(async () => {
    const n = ++seq.current;
    // undefined = error jaringan: pertahankan status sebelumnya.
    const me = await apiFetch<{ me: Me | null }>("/v1/auth/session").then(
      (r) => r.me,
      () => undefined,
    );
    if (n !== seq.current) return;
    setState((s) => (me === undefined ? (s ?? { me: null }) : { me }));
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh, devUser]);

  const logout = useCallback(async () => {
    await apiLogout().catch(() => {});
    if (getDevUserId()) setDevUserId(null);
    seq.current++;
    setState({ me: null });
  }, []);

  const switchUser = useCallback(
    async (id: string | null) => {
      await logout(); // jangan sampai ada dua identitas sekaligus
      setDevUserId(id);
    },
    [logout],
  );

  const me = state?.me ?? null;
  return (
    <SessionContext.Provider value={{ ready: state !== null, userId: me?.id ?? null, me, refresh, logout, switchUser }}>
      {children}
    </SessionContext.Provider>
  );
}

export function useSession() {
  const s = useContext(SessionContext);
  if (!s) throw new Error("useSession harus dipakai di dalam SessionProvider");
  return s;
}
