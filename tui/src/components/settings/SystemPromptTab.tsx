import React, { useState, useEffect, useCallback, useRef } from "react";
import { Box, Text, useInput, useFocus } from "ink";
import type { BackendClient } from "../../backend/client.ts";
import { useTheme } from "../../hooks/use-theme.ts";

// ─── Types ─────────────────────────────────────────────────────────────────

interface SystemPromptTabProps {
  backend: BackendClient;
}

// ─── Component ─────────────────────────────────────────────────────────────

export function SystemPromptTab({
  backend,
}: SystemPromptTabProps): React.JSX.Element {
  const { theme } = useTheme();
  const { isFocused } = useFocus({ isActive: true });

  const [systemPrompt, setSystemPrompt] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [warning, setWarning] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [editValue, setEditValue] = useState("");
  const [cursorPos, setCursorPos] = useState(0);

  const warningTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // ─── Load system prompt ──────────────────────────────────────────────────

  const loadPrompt = useCallback(() => {
    setLoading(true);
    setError(null);
    backend
      .call<{ system_prompt: string }>("config.get")
      .then((result) => {
        setSystemPrompt(result.system_prompt ?? "");
        if (!editing) {
          setEditValue(result.system_prompt ?? "");
        }
      })
      .catch((err: unknown) => {
        setError((err as Error).message);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [backend, editing]);

  useEffect(() => {
    loadPrompt();
  }, [loadPrompt]);

  // ─── Show warning auto-dismiss ───────────────────────────────────────────

  const showWarning = useCallback((message: string) => {
    setWarning(message);
    if (warningTimerRef.current) {
      clearTimeout(warningTimerRef.current);
    }
    warningTimerRef.current = setTimeout(() => {
      setWarning(null);
    }, 4000);
  }, []);

  useEffect(() => {
    return () => {
      if (warningTimerRef.current) {
        clearTimeout(warningTimerRef.current);
      }
    };
  }, []);

  // ─── Save ────────────────────────────────────────────────────────────────

  const savePrompt = useCallback(() => {
    if (editValue === systemPrompt) {
      setEditing(false);
      return;
    }

    backend
      .call("config.update", {
        patch: { system_prompt: editValue },
      })
      .then(() => {
        setSystemPrompt(editValue);
        setEditing(false);
        showWarning(
          "System prompt changed — session history cleared, starting fresh",
        );
      })
      .catch((err: unknown) => {
        showWarning(`Error saving: ${(err as Error).message}`);
      });
  }, [backend, editValue, systemPrompt, showWarning]);

  // ─── Cancel editing ──────────────────────────────────────────────────────

  const cancelEditing = useCallback(() => {
    setEditValue(systemPrompt ?? "");
    setEditing(false);
  }, [systemPrompt]);

  // ─── Keyboard handling ───────────────────────────────────────────────────

  useInput((input, key) => {
    if (!isFocused) return;

    if (editing) {
      if (key.escape) {
        cancelEditing();
        return;
      }

      if (key.return) {
        savePrompt();
        return;
      }

      if (key.backspace || key.delete) {
        setEditValue((prev) => {
          const before = prev.slice(0, Math.max(0, cursorPos - 1));
          const after = prev.slice(cursorPos);
          setCursorPos(Math.max(0, cursorPos - 1));
          return before + after;
        });
        return;
      }

      if (key.leftArrow) {
        setCursorPos((prev) => Math.max(0, prev - 1));
        return;
      }

      if (key.rightArrow) {
        setCursorPos((prev) => Math.min(editValue.length, prev + 1));
        return;
      }

      // Printable characters
      if (input.length === 1 && input >= " ") {
        setEditValue((prev) => {
          const before = prev.slice(0, cursorPos);
          const after = prev.slice(cursorPos);
          const result = before + input + after;
          setCursorPos((c) => c + 1);
          return result;
        });
        return;
      }

      return;
    }

    // Not editing
    if (input === "e" || input === "E") {
      setEditing(true);
      setCursorPos(editValue.length);
      return;
    }
  });

  // ─── Render ──────────────────────────────────────────────────────────────

  if (loading) {
    return (
      <Box flexDirection="column" paddingX={1}>
        <Box marginBottom={1}>
          <Text color={theme.warning}>Loading system prompt...</Text>
        </Box>
      </Box>
    );
  }

  if (error) {
    return (
      <Box flexDirection="column" paddingX={1}>
        <Box marginBottom={1}>
          <Text color={theme.error}>Error: {error}</Text>
        </Box>
      </Box>
    );
  }

  const displayPrompt = editing ? editValue : (systemPrompt ?? "");

  return (
    <Box flexDirection="column" paddingX={1} width="100%">
      {/* Warning banner */}
      {warning && (
        <Box marginBottom={1}>
          <Text color={theme.warning} bold>
            ⚠ {warning}
          </Text>
        </Box>
      )}

      {/* Status bar */}
      <Box marginBottom={1}>
        <Text dimColor>
          {editing
            ? "Editing — Enter to save, Esc to cancel"
            : "Press E to edit system prompt"}
        </Text>
      </Box>

      {/* Prompt display */}
      <Box
        flexDirection="column"
        borderStyle="round"
        borderColor={editing ? "yellow" : "gray"}
        paddingX={1}
        paddingY={0}
        width="100%"
      >
        {displayPrompt.length > 0 ? (
          <Box flexDirection="column">
            {/* Show text with cursor indicator when editing */}
            {editing ? (
              <Text>
                {displayPrompt.slice(0, cursorPos)}
                <Text inverse> </Text>
                {displayPrompt.slice(cursorPos)}
              </Text>
            ) : (
              <Text>{displayPrompt}</Text>
            )}
          </Box>
        ) : (
          <Text dimColor>
            {editing ? "Start typing..." : "No system prompt set"}
          </Text>
        )}
      </Box>

      {/* Prompt stats */}
      <Box marginTop={1}>
        <Text dimColor>
          {displayPrompt.length} characters
          {displayPrompt.length > 0
            ? ` · ~${Math.ceil(displayPrompt.length / 4)} tokens`
            : ""}
        </Text>
      </Box>

      {/* Warning about clearing */}
      {editing && editValue !== systemPrompt && (
        <Box marginTop={1}>
          <Text color={theme.warning} dimColor>
            Saving will clear session history and start fresh
          </Text>
        </Box>
      )}
    </Box>
  );
}
