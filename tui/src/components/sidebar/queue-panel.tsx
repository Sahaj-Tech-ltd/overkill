import React, { useState, useEffect, useRef } from "react";
import { Box, Text } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import type { QueueStatus } from "../../backend/types.ts";
import { useTheme } from "../../hooks/use-theme.ts";
import type { Theme } from "../../themes/definitions.ts";

interface QueuePanelProps {
  backend: BackendClient;
  sessionId: string | null;
}

function getStatusColor(status: string, theme: Theme): string {
  switch (status) {
    case "pending":
      return theme.muted;
    case "active":
      return theme.warning;
    case "done":
      return theme.success;
    case "failed":
      return theme.error;
    case "skipped":
      return theme.muted;
    default:
      return theme.text;
  }
}

function getStatusIcon(status: string): string {
  switch (status) {
    case "pending":
      return "⬜";
    case "active":
      return "🔄";
    case "done":
      return "✅";
    case "failed":
      return "❌";
    case "skipped":
      return "⏭️";
    default:
      return "?";
  }
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`;
}

function truncate(s: string, maxLen: number): string {
  if (s.length <= maxLen) return s;
  return s.slice(0, maxLen - 3) + "...";
}

export function QueuePanel({ backend, sessionId }: QueuePanelProps): React.JSX.Element {
  const { theme } = useTheme();
  const [status, setStatus] = useState<QueueStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // State carries its owner so a render for a new selection cannot expose
  // the previous queue even before effect cleanup runs.
  const ownerRef = useRef<string | null>(null);
  useEffect(() => {
    let disposed = false;
    ownerRef.current = sessionId;
    setStatus(null);
    setError(null);
    setLoading(Boolean(sessionId));
    if (!sessionId) return;
    const fetchStatus = () => {
      backend.call<QueueStatus>("sequential.queue", { session_id: sessionId })
        .then((result) => {
          if (disposed) return;
          setStatus(result);
          setError(null);
        })
        .catch((err: unknown) => {
          if (!disposed && (err as Error).message !== "AbortError") setError((err as Error).message);
        })
        .finally(() => { if (!disposed) setLoading(false); });
    };
    fetchStatus();
    const interval = setInterval(fetchStatus, 2000);
    return () => { disposed = true; clearInterval(interval); };
  }, [backend, sessionId]);
  const currentStatus = ownerRef.current === sessionId ? status : null;
  const currentError = ownerRef.current === sessionId ? error : null;

  const items = currentStatus?.items ?? [];
  const active = currentStatus?.active ?? false;
  const total = currentStatus?.total ?? 0;
  const done = currentStatus?.done ?? 0;
  const failed = currentStatus?.failed ?? 0;

  return (
    <Box flexDirection="column" overflow="hidden">
      {/* Header */}
      <Box paddingX={1}>
        <Text color={theme.accent} bold>
          Think Queue
        </Text>
        {active && <Text color={theme.warning}> (processing)</Text>}
      </Box>

      {/* Loading */}
      {loading && !currentStatus && (
        <Box paddingX={1}>
          <Text color={theme.warning}>Loading...</Text>
        </Box>
      )}

      {/* Error */}
      {currentError && !currentStatus && (
        <Box paddingX={1} flexDirection="column">
          <Text color={theme.error}>Error: {currentError}</Text>
        </Box>
      )}

      {/* Summary bar */}
      {currentStatus && (
        <Box paddingX={1} marginTop={1}>
          <Text dimColor>
            {total > 0 ? `${done}/${total} done` : "No items queued"}
          </Text>
          {failed > 0 && <Text color={theme.error}> · {failed} failed</Text>}
        </Box>
      )}

      {/* Item list */}
      {items.length > 0 && (
        <Box flexDirection="column" paddingX={1} marginTop={1}>
          {items.map((item, i) => {
            const color = getStatusColor(item.status, theme);
            const icon = getStatusIcon(item.status);
            return (
              <Box key={i} flexDirection="column" marginBottom={1}>
                <Box>
                  <Text>{icon} </Text>
                  <Text bold color={color}>
                    Item {item.index}
                  </Text>
                  <Text dimColor> — </Text>
                  <Text color={color}>{item.status}</Text>
                </Box>
                <Box paddingLeft={3}>
                  <Text dimColor>{truncate(item.description, 40)}</Text>
                </Box>
                {item.elapsed_ms > 0 && (
                  <Box paddingLeft={3}>
                    <Text dimColor>{formatDuration(item.elapsed_ms)}</Text>
                  </Box>
                )}
                {item.error && (
                  <Box paddingLeft={3}>
                    <Text color={theme.error}>{truncate(item.error, 40)}</Text>
                  </Box>
                )}
              </Box>
            );
          })}
        </Box>
      )}

      {/* Empty state */}
      {!loading && !currentStatus && !currentError && (
        <Box paddingX={1} marginTop={1}>
          <Text dimColor>No items in queue.</Text>
        </Box>
      )}

      {/* Idle state */}
      {!loading && currentStatus && !active && items.length === 0 && (
        <Box paddingX={1} marginTop={1}>
          <Text dimColor>
            Use /think to enable multi-item processing. Dump a list and I'll
            work through it one at a time.
          </Text>
        </Box>
      )}

      {/* Footer hint */}
      <Box paddingX={1} marginTop={1}>
        <Text dimColor>Auto-refreshes every 2s</Text>
      </Box>
    </Box>
  );
}
