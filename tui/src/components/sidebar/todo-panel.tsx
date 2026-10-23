import React, { useState, useMemo, useEffect, useSyncExternalStore } from "react";
import { Box, Text, useInput } from "ink";
import { useTheme } from "../../hooks/use-theme.ts";
import type { BackendClient } from "../../backend/client.ts";

import { TodoPanelModel, type TodoStatus } from "./panel-models.ts";

// ─── Helpers ────────────────────────────────────────────────────────────────

const STATUS_ICON: Record<TodoStatus, string> = {
  open: "○",
  in_progress: "●",
  shipped: "✓",
  abandoned: "✕",
};

const MAX_DISPLAY = 15;

// ─── Component ──────────────────────────────────────────────────────────────

export function TodoPanel({
  active,
  backend,
  sessionId,
}: {
  active: boolean;
  backend: BackendClient;
  sessionId: string;
}): React.JSX.Element {
  const { theme } = useTheme();

  const model = useMemo(() => new TodoPanelModel(backend), [backend]);
  const state = useSyncExternalStore(model.subscribe, model.getSnapshot);
  const todos = state.sessionId === sessionId ? state.todos : [];
  const [focusedIdx, setFocusedIdx] = useState(-1);
  const [filter, setFilter] = useState<TodoStatus | "all">("all");
  const [editor, setEditor] = useState<{ id?: string; text: string } | null>(null);
  useEffect(() => {
    setEditor(null); setFocusedIdx(-1);
    void model.setSession(sessionId);
    return () => model.dispose();
  }, [model, sessionId]);

  const filtered =
    filter === "all" ? todos : todos.filter((t) => t.status === filter);
  const counts = {
    all: todos.length,
    open: todos.filter((t) => t.status === "open").length,
    in_progress: todos.filter((t) => t.status === "in_progress").length,
    shipped: todos.filter((t) => t.status === "shipped").length,
    abandoned: todos.filter((t) => t.status === "abandoned").length,
  };

  // Keyboard handling when active
  useInput((input, key) => {
    if (!active || state.busy) return;
    if (key.tab) return;
    if (editor) {
      if (key.escape) { setEditor(null); return; }
      if (key.return) {
        void model.save(editor.text, editor.id).then((saved) => { if (saved) setEditor(null); });
        return;
      }
      if (key.ctrl && input === "u") { setEditor({ ...editor, text: "" }); return; }
      if (key.backspace || key.delete) { setEditor({ ...editor, text: editor.text.slice(0, -1) }); return; }
      if (!key.ctrl && !key.meta && input) setEditor({ ...editor, text: editor.text + input.replace(/[\r\n]/g, " ") });
      return;
    }
    if (input === "r") { void model.reload(); return; }

    if (key.escape) {
      setFocusedIdx(-1);
      return;
    }
    if (input === "a") {
      if (sessionId) setEditor({ text: "" });
      return;
    }
    if (input === "e") {
      const task = filtered[focusedIdx];
      if (task) setEditor({ id: task.id, text: task.intent });
      return;
    }
    if (input === "j" || key.downArrow) {
      setFocusedIdx((p) => {
        if (filtered.length === 0) return -1;
        return p < filtered.length - 1 ? p + 1 : p;
      });
      return;
    }
    if (input === "k" || key.upArrow) {
      setFocusedIdx((p) => (p > 0 ? p - 1 : p));
      return;
    }
    if (input === " ") {
      if (focusedIdx >= 0 && focusedIdx < filtered.length) {
        void model.toggle(filtered[focusedIdx]!.id);
      }
      return;
    }
    if (input === "d") {
      if (focusedIdx >= 0 && focusedIdx < filtered.length) {
        void model.remove(filtered[focusedIdx]!.id);
        setFocusedIdx(-1);
      }
      return;
    }
    if (input === "f") {
      const cycle: Array<TodoStatus | "all"> = [
        "all",
        "open",
        "in_progress",
        "shipped",
      ];
      const idx = cycle.indexOf(filter);
      setFilter(cycle[(idx + 1) % cycle.length]!);
      setFocusedIdx(-1);
      return;
    }
    if (input === "c") {
      void model.clear();
      setFocusedIdx(-1);
      return;
    }
  });

  const FILTERS: Array<{ id: typeof filter; label: string }> = [
    { id: "all", label: "All" },
    { id: "open", label: "Open" },
    { id: "in_progress", label: "Active" },
    { id: "shipped", label: "Shipped" },
  ];

  return (
    <Box flexDirection="column" overflow="hidden">
      {/* Header */}
      <Box paddingX={1}>
        <Text color={theme.accent} bold>
          Todo
        </Text>
        <Text dimColor> ({counts.all})</Text>
      </Box>

      {!sessionId && <Text dimColor>Start or select a session to add tasks.</Text>}
      {state.error && <Text color={theme.error}>{state.error} · {editor ? "Enter:retry save" : "r:reload"}</Text>}
      {state.busy && <Text dimColor>Saving/loading tasks…</Text>}
      {editor && <Box flexDirection="column" paddingX={1}>
        <Text>{editor.id ? "Edit task" : "New task"}: {editor.text}<Text inverse> </Text></Text>
        <Text dimColor>Enter:save · Esc:cancel · Ctrl+U:clear</Text>
      </Box>}
      {/* Filter pills */}
      <Box paddingX={1} marginTop={1}>
        {FILTERS.map((f, i) => (
          <React.Fragment key={f.id}>
            {i > 0 && <Text dimColor> </Text>}
            <Text
              bold={filter === f.id}
              underline={filter === f.id}
              color={filter === f.id ? theme.accent : theme.muted}
            >
              {f.label}:{counts[f.id]}
            </Text>
          </React.Fragment>
        ))}
      </Box>

      <Box>
        <Text dimColor>{"─".repeat(28)}</Text>
      </Box>

      {/* List */}
      {filtered.length === 0 && (
        <Box paddingX={1} marginTop={1}>
          <Text dimColor>
            {filter === "all" ? "No todos yet." : `No ${filter}.`}
          </Text>
        </Box>
      )}

      {filtered.slice(0, MAX_DISPLAY).map((todo, i) => {
        const isFocused = i === focusedIdx;
        const done = todo.status === "shipped";
        const color = done
          ? theme.muted
          : todo.status === "in_progress"
            ? theme.warning
            : theme.text;

        return (
          <Box key={todo.id} paddingX={1}>
            <Text color={isFocused ? theme.accent : theme.muted}>
              {isFocused ? "▸" : " "}
            </Text>
            <Text color={color}> [{STATUS_ICON[todo.status]}]</Text>
            <Text color={color} strikethrough={done} dimColor={done}>
              {" "}
              {todo.intent.length > 21
                ? todo.intent.slice(0, 21) + "…"
                : todo.intent}
            </Text>
          </Box>
        );
      })}

      {filtered.length > MAX_DISPLAY && (
        <Box paddingX={2}>
          <Text dimColor>... {filtered.length - MAX_DISPLAY} more</Text>
        </Box>
      )}

      {/* Help */}
      {active && (
        <Box paddingX={1} marginTop={1} flexDirection="column">
          <Text dimColor>a:add · e:edit · j/k:nav · space:toggle</Text>
          <Text dimColor>d:delete · f:filter · c:clear</Text>
        </Box>
      )}
    </Box>
  );
}
