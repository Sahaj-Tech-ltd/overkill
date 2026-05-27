import React, { useState, useEffect, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import type { SessionInfo } from "../../backend/types.ts";

interface SessionPanelProps {
  backend: BackendClient;
  onSessionSelect?: (session: SessionInfo) => void;
}

export function SessionPanel({
  backend,
  onSessionSelect,
}: SessionPanelProps): React.JSX.Element {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selectedIdx, setSelectedIdx] = useState(0);

  const fetchSessions = useCallback(() => {
    setLoading(true);
    setError(null);
    backend
      .call<{ sessions: SessionInfo[] }>("session.list")
      .then((result) => {
        setSessions(result.sessions ?? []);
      })
      .catch((err: unknown) => {
        setError((err as Error).message);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [backend]);

  useEffect(() => {
    fetchSessions();
  }, [fetchSessions]);

  useInput((input, key) => {
    if (key.upArrow) {
      setSelectedIdx((prev) => Math.max(0, prev - 1));
    } else if (key.downArrow) {
      setSelectedIdx((prev) => Math.min(sessions.length - 1, prev + 1));
    } else if (key.return) {
      if (sessions[selectedIdx]) {
        onSessionSelect?.(sessions[selectedIdx]);
      }
    } else if (input === "r") {
      fetchSessions();
    }
  });

  const formatDate = (iso: string): string => {
    try {
      const d = new Date(iso);
      return d.toLocaleDateString("en-US", {
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      });
    } catch {
      return iso;
    }
  };

  const getStatusColor = (session: SessionInfo): string => {
    const updated = new Date(session.updatedAt).getTime();
    const fiveMinutesAgo = Date.now() - 5 * 60 * 1000;
    return updated > fiveMinutesAgo ? "green" : "yellow";
  };

  return (
    <Box flexDirection="column" overflow="hidden">
      {loading && (
        <Box paddingX={1}>
          <Text color="yellow">Loading...</Text>
        </Box>
      )}

      {error && (
        <Box paddingX={1}>
          <Text color="red">Error: {error}</Text>
        </Box>
      )}

      {!loading && !error && sessions.length === 0 && (
        <Box paddingX={1}>
          <Text dimColor>No sessions</Text>
        </Box>
      )}

      {!loading &&
        !error &&
        sessions.map((s, i) => (
          <Box key={s.id} paddingX={1}>
            <Text color={i === selectedIdx ? "cyan" : undefined}>
              {i === selectedIdx ? "▸ " : "  "}
            </Text>
            <Text color={getStatusColor(s)}>● </Text>
            <Box flexDirection="column" flexGrow={1}>
              <Text
                color={i === selectedIdx ? "white" : undefined}
                bold={i === selectedIdx}
              >
                {(s.name || s.folder).slice(0, 24)}
              </Text>
              <Text dimColor>{formatDate(s.updatedAt)}</Text>
            </Box>
          </Box>
        ))}

      {!loading && !error && sessions.length > 0 && (
        <Box paddingX={1} marginTop={1}>
          <Text dimColor>Enter to switch · r refresh</Text>
        </Box>
      )}
    </Box>
  );
}
