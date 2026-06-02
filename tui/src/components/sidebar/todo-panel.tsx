import React, { useState, useCallback, useEffect } from "react";
import { Box, Text, useInput } from "ink";
import { useTheme } from "../../hooks/use-theme.ts";
import type { BackendClient } from "../../backend/client.ts";

// ─── Types ─────────────────────────────────────────────────────────────────

type TodoStatus = "open" | "in_progress" | "shipped" | "abandoned";

interface Todo {
  id: string;
  intent: string;
  status: TodoStatus;
}

// ─── Helpers ────────────────────────────────────────────────────────────────

const STATUS_ICON: Record<TodoStatus, string> = {
  open: "○",
  in_progress: "●",
  shipped: "✓",
  abandoned: "✕",
};

const MAX_DISPLAY = 15;

// ─── Component ──────────────────────────────────────────────────────────────

// TODO(backend): wire to RPC once todo.* methods are added to server.go
// Planned calls:
//   mount:  backend.call<Todo[]>("todo.list")
//   add:    backend.call("todo.add", { description })
//   toggle: backend.call("todo.toggle", { id })
//   delete: backend.call("todo.delete", { id })

export function TodoPanel({
  active,
  backend,
}: {
  active: boolean;
  backend: BackendClient;
}): React.JSX.Element {
  const { theme } = useTheme();

  const [todos, setTodos] = useState<Todo[]>([]);
  const [focusedIdx, setFocusedIdx] = useState(-1);
  const [filter, setFilter] = useState<TodoStatus | "all">("all");

  // Mount: load todos from backend.
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    if (!mounted) {
      setMounted(true);
      backend
        .call<Todo[]>("todo.list", { session_id: "" })
        .then((list) => {
          if (list?.length) setTodos(list);
        })
        .catch(() => {});
    }
  }, [backend, mounted]);

  const addTodo = useCallback(
    (desc: string) => {
      backend
        .call<Todo>("todo.add", { description: desc, session_id: "" })
        .then((t) => {
          if (t) setTodos((prev) => [...prev, t]);
        })
        .catch(() => {});
    },
    [backend],
  );

  const toggleTodo = useCallback(
    (id: string) => {
      backend
        .call<Todo>("todo.toggle", { id })
        .then((t) => {
          if (t)
            setTodos((prev) => prev.map((item) => (item.id === id ? t : item)));
        })
        .catch(() => {});
    },
    [backend],
  );

  const removeTodo = useCallback(
    (id: string) => {
      backend
        .call("todo.delete", { id })
        .then(() => {
          setTodos((prev) => prev.filter((t) => t.id !== id));
        })
        .catch(() => {});
    },
    [backend],
  );

  const clearAll = useCallback(() => {
    todos.forEach((t) =>
      backend.call("todo.delete", { id: t.id }).catch(() => {}),
    );
    setTodos([]);
  }, [todos, backend]);

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
    if (!active) return;

    if (key.escape) {
      setFocusedIdx(-1);
      return;
    }
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
      clearAll();
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
          <Text dimColor>a:add · j/k:nav · space:toggle</Text>
          <Text dimColor>d:delete · f:filter · c:clear</Text>
        </Box>
      )}
    </Box>
  );
}
