import { useState, useEffect, useCallback, useRef } from "react";
import { BackendClient, createClient } from "../backend/client.ts";
import type { ConnectionState } from "../backend/types.ts";

export interface UseBackendResult {
  backend: BackendClient;
  connected: ConnectionState;
  error: string | null;
}

export function useBackend(): UseBackendResult {
  const backendRef = useRef(createClient());
  const [connected, setConnected] = useState<ConnectionState>("connecting");
  const [error, setError] = useState<string | null>(null);
  // B018: Use ref for connected state so the interval doesn't recreate
  // on every state change — the interval closure reads the ref, not the
  // reactive state binding.
  const connectedRef = useRef(connected);
  const setConnectedSync = (v: ConnectionState) => {
    connectedRef.current = v;
    setConnected(v);
  };

  const tryConnect = useCallback(async () => {
    const client = backendRef.current;
    setConnectedSync("connecting");
    setError(null);
    try {
      const ok = await client.health();
      setConnectedSync(ok ? "connected" : "disconnected");
      if (!ok) {
        setError("Health check failed");
      }
    } catch (err) {
      setConnectedSync("disconnected");
      setError((err as Error).message);
    }
  }, []);

  useEffect(() => {
    void tryConnect();

    const interval = setInterval(() => {
      if (connectedRef.current !== "connected") {
        void tryConnect();
      }
    }, 3000);

    return () => clearInterval(interval);
  }, [tryConnect]); // B018: connected removed — interval reads connectedRef instead.

  return {
    backend: backendRef.current,
    connected,
    error,
  };
}
