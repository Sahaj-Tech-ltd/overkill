import React, { useState, useEffect, useCallback, useRef } from "react";
import { Box, Text } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import type { SubagentInfo } from "../../backend/types.ts";

interface SubagentPanelProps {
  backend: BackendClient;
}

const STATUS_COLORS: Record<string, string> = {
  running: "green",
  completed: "grey",
  failed: "red",
};

const STATUS_ICONS: Record<string, string> = {
  running: "●",
  completed: "✓",
  failed: "✕",
};

function formatElapsed(ms: number): string {
  const totalSeconds = Math.floor(ms / 1000);
  const mins = Math.floor(totalSeconds / 60);
  const secs = totalSeconds % 60;
  if (mins > 0) {
    return `${mins}m ${secs.toString().padStart(2, "0")}s`;
  }
  return `${secs}s`;
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toLocaleTimeString("en-US", {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  } catch {
    return iso;
  }
}

export function SubagentPanel({
  backend,
}: SubagentPanelProps): React.JSX.Element {
  const [subagents, setSubagents] = useState<SubagentInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const fetchSubagents = useCallback(() => {
    backend
      .call<{ subagents: SubagentInfo[] }>("agent.subagents")
      .then((result) => {
        setSubagents(result.subagents ?? []);
        setError(null);
      })
      .catch((err: unknown) => {
        // Don't overwrite existing data on transient errors
        if ((err as Error).message !== "AbortError") {
          setError((err as Error).message);
        }
      })
      .finally(() => {
        setLoading(false);
      });
  }, [backend]);

  useEffect(() => {
    setLoading(true);
    fetchSubagents();

    // Auto-refresh every 2 seconds
    intervalRef.current = setInterval(fetchSubagents, 2000);

    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
      }
    };
  }, [fetchSubagents]);

  const activeCount = subagents.filter(
    (s) => s.status === "running",
  ).length;

  return (
    <Box flexDirection="column" overflow="hidden">
      {/* Summary header */}
      <Box paddingX={1}>
        <Text color="cyan" bold>
          Agents
        </Text>
        {!loading && (
          <Text dimColor>
            {" "}
            ({activeCount} running)
          </Text>
        )}
      </Box>

      {/* Loading state */}
      {loading && subagents.length === 0 && (
        <Box paddingX={1}>
          <Text color="yellow">Loading...</Text>
        </Box>
      )}

      {/* Error state */}
      {error && subagents.length === 0 && (
        <Box paddingX={1}>
          <Text color="red">Error: {error}</Text>
        </Box>
      )}

      {/* Empty state */}
      {!loading && !error && subagents.length === 0 && (
        <Box paddingX={1}>
          <Text dimColor>No active subagents</Text>
        </Box>
      )}

      {/* Subagent list */}
      {subagents.map((agent) => {
        const color = STATUS_COLORS[agent.status] ?? "white";
        const icon = STATUS_ICONS[agent.status] ?? "?";

        return (
          <Box key={agent.id} paddingX={2} flexDirection="column">
            <Box>
              <Text color={color}>{icon} </Text>
              <Text bold={agent.status === "running"} dimColor={agent.status !== "running"}>
                {agent.name}
              </Text>
            </Box>
            <Box paddingLeft={2}>
              <Text dimColor>
                {formatTime(agent.startedAt)} · {formatElapsed(agent.elapsed)}
                {agent.model ? ` · ${agent.model}` : ""}
              </Text>
            </Box>
          </Box>
        );
      })}

      {/* Hint */}
      {subagents.length > 0 && (
        <Box paddingX={1} marginTop={1}>
          <Text dimColor>Auto-refreshes every 2s</Text>
        </Box>
      )}
    </Box>
  );
}
