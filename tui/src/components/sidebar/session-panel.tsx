import React, { useState, useEffect, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import type { SessionInfo, ForkResult } from "../../backend/types.ts";

interface SessionPanelProps {
  backend: BackendClient;
  currentSessionId?: string;
  onSessionSelect?: (session: SessionInfo) => void;
}

interface TreeNode {
  session: SessionInfo;
  children: TreeNode[];
  depth: number;
}

/** Build a tree from a flat session list using ParentID/Children relationships. */
function buildTree(sessions: SessionInfo[]): TreeNode[] {
  const byId = new Map<string, SessionInfo>();
  for (const s of sessions) {
    byId.set(s.id, s);
  }

  // Find roots: sessions with no parent, or whose parent isn't in the list
  const roots: SessionInfo[] = [];
  const childrenMap = new Map<string, SessionInfo[]>();

  for (const s of sessions) {
    if (s.parentId && byId.has(s.parentId)) {
      const siblings = childrenMap.get(s.parentId) ?? [];
      siblings.push(s);
      childrenMap.set(s.parentId, siblings);
    } else {
      roots.push(s);
    }
  }

  function buildNodes(list: SessionInfo[], depth: number): TreeNode[] {
    return list.map((s) => ({
      session: s,
      depth,
      children: buildNodes(childrenMap.get(s.id) ?? [], depth + 1),
    }));
  }

  return buildNodes(roots, 0);
}

/** Flatten tree into a list preserving traversal order for keyboard nav. */
function flattenTree(nodes: TreeNode[]): TreeNode[] {
  const result: TreeNode[] = [];
  function walk(nodes: TreeNode[]) {
    for (const node of nodes) {
      result.push(node);
      walk(node.children);
    }
  }
  walk(nodes);
  return result;
}

export function SessionPanel({
  backend,
  currentSessionId,
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

  const tree = buildTree(sessions);
  const flatNodes = flattenTree(tree);

  useInput((input, key) => {
    if (key.upArrow) {
      setSelectedIdx((prev) => Math.max(0, prev - 1));
    } else if (key.downArrow) {
      setSelectedIdx((prev) => Math.min(flatNodes.length - 1, prev + 1));
    } else if (key.return) {
      if (flatNodes[selectedIdx]) {
        onSessionSelect?.(flatNodes[selectedIdx].session);
      }
    } else if (input === "r") {
      fetchSessions();
    } else if (input === "f" && currentSessionId) {
      // Fork the current session
      const forkName = `forked-${Date.now()}`;
      backend
        .call<ForkResult>("session.fork", {
          session_id: currentSessionId,
          name: forkName,
        })
        .then((result) => {
          fetchSessions();
          onSessionSelect?.(result.session);
        })
        .catch((err: unknown) => {
          setError((err as Error).message);
        });
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

  const displayName = (s: SessionInfo): string => {
    return (s.title || s.name || s.folder).slice(0, 30);
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
        flatNodes.map((node, i) => {
          const s = node.session;
          const isSelected = i === selectedIdx;
          const isCurrent = s.id === currentSessionId;
          const hasChildren = node.children.length > 0;
          const prefix =
            node.depth > 0
              ? "  ".repeat(node.depth) + "└─ "
              : "";

          return (
            <Box key={s.id} paddingX={1}>
              <Text color={isSelected ? "cyan" : undefined}>
                {isSelected ? "▸ " : "  "}
              </Text>
              <Text color={isCurrent ? "green" : getStatusColor(s)}>
                {isCurrent ? "● " : "○ "}
              </Text>
              <Text dimColor>{prefix}</Text>
              <Text>
                {hasChildren ? "📁 " : "📄 "}
              </Text>
              <Box flexDirection="column" flexGrow={1}>
                <Text
                  color={isSelected ? "white" : undefined}
                  bold={isSelected}
                >
                  {displayName(s)}
                </Text>
                <Text dimColor>{formatDate(s.updatedAt)}</Text>
              </Box>
            </Box>
          );
        })}

      {!loading && !error && sessions.length > 0 && (
        <Box paddingX={1} marginTop={1} flexDirection="column">
          <Text dimColor>Enter to switch · r refresh</Text>
          {currentSessionId && (
            <Text dimColor>f fork current session</Text>
          )}
        </Box>
      )}
    </Box>
  );
}
