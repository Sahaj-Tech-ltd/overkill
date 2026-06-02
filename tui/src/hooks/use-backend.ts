import { useState, useEffect, useCallback, useRef } from "react";
import { BackendClient, createClient } from "../backend/client.ts";
import { setMemoClient } from "../components/memo-phrases.ts";
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

  // B146: Exponential backoff for reconnection to prevent storms when
  // health checks hang or timeout. Starts at 3s, doubles each failure,
  // capped at 60s. Resets on successful connection.
  const backoffRef = useRef(3000);
  const backoffTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const tryConnect = useCallback(async () => {
    const client = backendRef.current;
    setConnectedSync("connecting");
    setError(null);
    try {
      const ok = await client.health();
      setConnectedSync(ok ? "connected" : "disconnected");
      if (!ok) {
        setError("Health check failed");
      } else {
        // Reset backoff on successful connection
        backoffRef.current = 3000;
      }
    } catch (err) {
      setConnectedSync("disconnected");
      setError((err as Error).message);
    }
  }, []);

  // Wire memo phrases to the backend client so memo.phrase/memo.learn RPCs
  // reach the server instead of using only hardcoded local phrases (M15).
  useEffect(() => {
    setMemoClient(backendRef.current);
    return () => setMemoClient(null);
  }, []);

  useEffect(() => {
    void tryConnect();

    // B146: Use exponential backoff scheduling instead of a fixed-interval
    // setInterval. Each attempt schedules the next with increasing delay
    // until connected, preventing reconnection storms.
    const scheduleNext = () => {
      backoffTimerRef.current = setTimeout(() => {
        if (connectedRef.current !== "connected") {
          void tryConnect().finally(() => {
            // After the attempt (pass or fail), schedule next with backoff
            if (connectedRef.current !== "connected") {
              backoffRef.current = Math.min(backoffRef.current * 2, 60_000);
            }
            scheduleNext();
          });
        }
      }, backoffRef.current);
    };
    scheduleNext();

    return () => {
      if (backoffTimerRef.current) {
        clearTimeout(backoffTimerRef.current);
        backoffTimerRef.current = null;
      }
    };
  }, [tryConnect]); // B018: connected removed — interval reads connectedRef instead.

  return {
    backend: backendRef.current,
    connected,
    error,
  };
}
