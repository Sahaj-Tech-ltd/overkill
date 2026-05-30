import React, { useState, useCallback, useRef } from "react";
import { Box, Text, useInput } from "ink";
import { useTheme } from "../../hooks/use-theme.ts";

// ─── Types ─────────────────────────────────────────────────────────────────

type TodoStatus = "pending" | "active" | "done";

interface Todo {
  id: number;
  description: string;
  status: TodoStatus;
}

// ─── Helpers ────────────────────────────────────────────────────────────────

const STATUS_ICON: Record<TodoStatus, string> = {
  pending: "○",
  active: "●",
  done: "✓",
};

const MAX_DISPLAY = 15;

// ─── Component ──────────────────────────────────────────────────────────────

export function TodoPanel({
  active,
}: {
  active: boolean;
}): React.JSX.Element {
  const { theme } = useTheme();

  const [todos, setTodos] = useState<Todo[]>([
    { id: 1, description: "Review PR #142", status: "pending" },
    { id: 2, description: "Fix session leak in agent loop", status: "active" },
    { id: 3, description: "Add mouse support to TUI", status: "done" },
  ]);
  const [focusedIdx, setFocusedIdx] = useState(-1);
  const nextId = useRef(4);
  const [filter, setFilter] = useState<TodoStatus | "all">("all");

  const addTodo = useCallback((desc: string) => {
    setTodos((prev) => [
      ...prev,
      { id: nextId.current++, description: desc, status: "pending" as TodoStatus },
    ]);
  }, []);

  const toggleTodo = useCallback((id: number) => {
    setTodos((prev) =>
      prev.map((t) => {
        if (t.id !== id) return t;
        const next: TodoStatus =
          t.status === "pending" ? "active" : t.status === "active" ? "done" : "pending";
        return { ...t, status: next };
      }),
    );
  }, []);

  const removeTodo = useCallback((id: number) => {
    setTodos((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const clearAll = useCallback(() => setTodos([]), []);

  const filtered = filter === "all" ? todos : todos.filter((t) => t.status === filter);
  const counts = {
    all: todos.length,
    pending: todos.filter((t) => t.status === "pending").length,
    active: todos.filter((t) => t.status === "active").length,
    done: todos.filter((t) => t.status === "done").length,
  };

  // Keyboard handling when active
  useInput((input, key) => {
    if (!active) return;

    if (key.escape) { setFocusedIdx(-1); return; }
    if (input === "a") {
      // Simple: just add a placeholder todo for now
      // The user types in the main chat to describe it
      addTodo("New task (edit me)");
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
        toggleTodo(filtered[focusedIdx]!.id);
      }
      return;
    }
    if (input === "d") {
      if (focusedIdx >= 0 && focusedIdx < filtered.length) {
        removeTodo(filtered[focusedIdx]!.id);
        setFocusedIdx(-1);
      }
      return;
    }
    if (input === "f") {
      const cycle: Array<TodoStatus | "all"> = ["all", "pending", "active", "done"];
      const idx = cycle.indexOf(filter);
      setFilter(cycle[(idx + 1) % cycle.length]!);
      setFocusedIdx(-1);
      return;
    }
    if (input === "c") {
      clearAll();
      setFocusedIdx(-1);
      return;
    }
  });

  const FILTERS: Array<{ id: typeof filter; label: string }> = [
    { id: "all", label: "All" },
    { id: "pending", label: "Pending" },
    { id: "active", label: "Active" },
    { id: "done", label: "Done" },
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
          <Text dimColor>{filter === "all" ? "No todos yet." : `No ${filter}.`}</Text>
        </Box>
      )}

      {filtered.slice(0, MAX_DISPLAY).map((todo, i) => {
        const isFocused = i === focusedIdx;
        const done = todo.status === "done";
        const color = done ? theme.muted : todo.status === "active" ? theme.warning : theme.text;

        return (
          <Box key={todo.id} paddingX={1}>
            <Text color={isFocused ? theme.accent : theme.muted}>
              {isFocused ? "▸" : " "}
            </Text>
            <Text color={color}> [{STATUS_ICON[todo.status]}]</Text>
            <Text
              color={color}
              strikethrough={done}
              dimColor={done}
            >
              {" "}{todo.description.length > 21
                ? todo.description.slice(0, 21) + "…"
                : todo.description}
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
          <Text dimColor>
            a:add · j/k:nav · space:toggle
          </Text>
          <Text dimColor>
            d:delete · f:filter · c:clear
          </Text>
        </Box>
      )}
    </Box>
  );
}
