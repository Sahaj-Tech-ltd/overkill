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

  const tryConnect = useCallback(async () => {
    const client = backendRef.current;
    setConnected("connecting");
    setError(null);
    try {
      const ok = await client.health();
      setConnected(ok ? "connected" : "disconnected");
      if (!ok) {
        setError("Health check failed");
      }
    } catch (err) {
      setConnected("disconnected");
      setError((err as Error).message);
    }
  }, []);

  useEffect(() => {
    void tryConnect();

    const interval = setInterval(() => {
      if (connected !== "connected") {
        void tryConnect();
      }
    }, 3000);

    return () => clearInterval(interval);
  }, [tryConnect, connected]);

  return {
    backend: backendRef.current,
    connected,
    error,
  };
}
