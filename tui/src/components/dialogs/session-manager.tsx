import React, { useState, useEffect, useCallback } from "react";
import { Box, Text, useInput } from "ink";
import { TextInput } from "../text-input.tsx";
import { DialogContainer } from "./dialog-container.tsx";
import type { BackendClient } from "../../backend/client.ts";
import type { SessionInfo } from "../../backend/types.ts";

interface SessionManagerProps {
  open: boolean;
  onClose: () => void;
  backend: BackendClient;
  currentSessionId?: string;
  onSessionSelect: (session: SessionInfo) => void;
}

type Mode = "list" | "create" | "delete-confirm";

/** Input handler that only mounts when the dialog is open. */
function SessionInputHandler({
  mode,
  sessions,
  selectedIdx,
  setSelectedIdx,
  setMode,
  setNewFolder,
  setDeleteTarget,
  onSessionSelect,
  onClose,
  handleCreate,
  handleDelete,
}: {
  mode: Mode;
  sessions: SessionInfo[];
  selectedIdx: number;
  setSelectedIdx: (n: number) => void;
  setMode: (m: Mode) => void;
  setNewFolder: (s: string) => void;
  setDeleteTarget: (s: SessionInfo | null) => void;
  onSessionSelect: (session: SessionInfo) => void;
  onClose: () => void;
  handleCreate: () => void;
  handleDelete: () => void;
}) {
  useInput((input, key) => {
    if (mode === "list") {
      if (key.upArrow) {
        setSelectedIdx(Math.max(0, selectedIdx - 1));
      } else if (key.downArrow) {
        setSelectedIdx(Math.min(Math.max(0, sessions.length - 1), selectedIdx + 1));
      } else if (key.return) {
        if (sessions[selectedIdx]) {
          onSessionSelect(sessions[selectedIdx]);
          onClose();
        }
      } else if (input === "n") {
        setMode("create");
        setNewFolder("");
      } else if (input === "d" && sessions[selectedIdx]) {
        setDeleteTarget(sessions[selectedIdx]);
        setMode("delete-confirm");
      }
    } else if (mode === "delete-confirm") {
      if (input === "y" || key.return) {
        handleDelete();
      } else if (input === "n" || key.escape) {
        setMode("list");
        setDeleteTarget(null);
      }
    }
  });
  return null;
}

export function SessionManager({
  open,
  onClose,
  backend,
  currentSessionId,
  onSessionSelect,
}: SessionManagerProps): React.JSX.Element | null {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [mode, setMode] = useState<Mode>("list");
  const [newFolder, setNewFolder] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<SessionInfo | null>(null);

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
    if (!open) return;
    setMode("list");
    setSelectedIdx(0);
    setNewFolder("");
    setDeleteTarget(null);
    fetchSessions();
  }, [open, fetchSessions]);

  const handleCreate = useCallback(() => {
    if (newFolder.trim().length === 0) return;

    backend
      .call<{ session: SessionInfo }>("session.create", {
        folder: newFolder.trim(),
      })
      .then((result) => {
        setSessions((prev) => [...prev, result.session]);
        setMode("list");
        setNewFolder("");
      })
      .catch((err: unknown) => {
        setError((err as Error).message);
      });
  }, [backend, newFolder]);

  const handleDelete = useCallback(() => {
    if (!deleteTarget) return;

    backend
      .call<void>("session.delete", { id: deleteTarget.id })
      .then(() => {
        setSessions((prev) => prev.filter((s) => s.id !== deleteTarget.id));
        setMode("list");
        setDeleteTarget(null);
        setSelectedIdx((prev) => Math.max(0, prev - 1));
      })
      .catch((err: unknown) => {
        setError((err as Error).message);
      });
  }, [backend, deleteTarget]);

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

  if (!open) return null;

  return (
    <DialogContainer open={open} onClose={onClose} title="Sessions">
      <SessionInputHandler
        mode={mode}
        sessions={sessions}
        selectedIdx={selectedIdx}
        setSelectedIdx={setSelectedIdx}
        setMode={setMode}
        setNewFolder={setNewFolder}
        setDeleteTarget={setDeleteTarget}
        onSessionSelect={onSessionSelect}
        onClose={onClose}
        handleCreate={handleCreate}
        handleDelete={handleDelete}
      />
      {loading && (
        <Box paddingX={1} paddingY={1}>
          <Text color="yellow">Loading sessions...</Text>
        </Box>
      )}

      {error && (
        <Box paddingX={1} paddingY={1}>
          <Text color="red">Error: {error}</Text>
        </Box>
      )}

      {mode === "create" && (
        <Box flexDirection="column" paddingX={1}>
          <Box marginBottom={1}>
            <Text bold color="cyan">
              New Session
            </Text>
          </Box>
          <Box>
            <Text>Folder: </Text>
            <TextInput
              value={newFolder}
              onChange={setNewFolder}
              placeholder="/path/to/project"
            />
          </Box>
          <Box marginTop={1}>
            <Text dimColor>Enter to create · Esc to cancel</Text>
          </Box>
        </Box>
      )}

      {mode === "delete-confirm" && deleteTarget && (
        <Box flexDirection="column" paddingX={1} paddingY={1}>
          <Text color="red" bold>
            Delete session?
          </Text>
          <Box marginTop={1}>
            <Text>{deleteTarget.name || deleteTarget.folder}</Text>
          </Box>
          <Box marginTop={1}>
            <Text dimColor>y/Enter to confirm · n/Esc to cancel</Text>
          </Box>
        </Box>
      )}

      {!loading && !error && mode === "list" && (
        <Box flexDirection="column">
          {sessions.length === 0 && (
            <Box paddingX={1} paddingY={1}>
              <Text dimColor>No sessions found. Press 'n' to create one.</Text>
            </Box>
          )}
          {sessions.map((s, i) => (
            <Box key={s.id} paddingX={1}>
              <Text color={i === selectedIdx ? "cyan" : undefined}>
                {i === selectedIdx ? "▸ " : "  "}
                {s.id === currentSessionId ? "● " : "  "}
              </Text>
              <Box flexDirection="column" flexGrow={1}>
                <Text
                  color={i === selectedIdx ? "white" : undefined}
                  bold={i === selectedIdx}
                >
                  {s.name || s.folder}
                </Text>
                <Box>
                  <Text dimColor>{s.folder}</Text>
                  <Text dimColor> · {formatDate(s.updatedAt)}</Text>
                </Box>
              </Box>
            </Box>
          ))}
        </Box>
      )}

      {mode === "list" && (
        <Box marginTop={1} paddingX={1}>
          <Text dimColor>
            Enter to select · n new · d delete · Esc to close
          </Text>
        </Box>
      )}
    </DialogContainer>
  );
}
