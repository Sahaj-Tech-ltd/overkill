import { useState, useEffect } from "react";
import { BackendClient, createClient } from "../backend/client.ts";
import { setMemoClient } from "../components/memo-phrases.ts";
import type { ConnectionState } from "../backend/types.ts";

export interface UseBackendResult {
  backend: BackendClient;
  connected: ConnectionState;
  error: string | null;
}

export function useBackend(options?: {
  client?: BackendClient; healthyIntervalMs?: number; retryInitialMs?: number; maxRetryMs?: number;
}): UseBackendResult {
  const [backend] = useState(() => options?.client ?? createClient());
  const [connected, setConnected] = useState<ConnectionState>("connecting");
  const [error, setError] = useState<string | null>(null);
  const healthyInterval = options?.healthyIntervalMs ?? 5000;
  const retryInitial = options?.retryInitialMs ?? 3000;
  const retryMax = options?.maxRetryMs ?? 60_000;
  useEffect(() => {
    setMemoClient(backend);
    return () => setMemoClient(null);
  }, [backend]);
  useEffect(() => {
    let disposed = false;
    let delay = retryInitial;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const controller = new AbortController();
    const probe = async () => {
      const ok = await backend.health(controller.signal);
      if (disposed) return;
      setConnected(ok ? "connected" : "disconnected");
      setError(ok ? null : "Health check failed");
      // Schedule after completion: at most one bounded probe can be in flight.
      timer = setTimeout(() => { void probe(); }, ok ? healthyInterval : delay);
      delay = ok ? retryInitial : Math.min(delay * 2, retryMax);
    };
    void probe();
    return () => { disposed = true; controller.abort(); if (timer) clearTimeout(timer); };
  }, [backend, healthyInterval, retryInitial, retryMax]);
  return { backend, connected, error };
}
