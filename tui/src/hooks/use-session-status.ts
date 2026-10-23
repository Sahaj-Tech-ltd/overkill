import { useState, useEffect } from "react";
import type { BackendClient } from "../backend/client.ts";

// Selection-scoped polling has one request at a time; cleanup cancels pending
// transport and ignores any late completion from the previous selection.
export function useSessionStatus<T>(backend: BackendClient, sessionId: string | null, method: string, delay = 2000) {
  const [state, setState] = useState<{ owner: string | null; value: T | null; error: string | null; loading: boolean }>({owner: null, value: null, error: null, loading: false});
  useEffect(() => {
    let disposed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const controller = new AbortController();
    setState({owner: sessionId, value: null, error: null, loading: Boolean(sessionId)});
    if (!sessionId) return;
    const poll = async () => {
      try {
        const value = await backend.call<T>(method, {session_id: sessionId}, {signal: controller.signal});
        if (!disposed) setState({owner: sessionId, value, error: null, loading: false});
      } catch (error) {
        if (!disposed) setState({owner: sessionId, value: null, error: (error as Error).message, loading: false});
      } finally { if (!disposed) timer = setTimeout(() => { void poll(); }, delay); }
    };
    void poll();
    return () => { disposed = true; controller.abort(); if (timer) clearTimeout(timer); };
  }, [backend, sessionId, method, delay]);
  return state.owner === sessionId ? state : {owner: sessionId, value: null, error: null, loading: Boolean(sessionId)};
}
