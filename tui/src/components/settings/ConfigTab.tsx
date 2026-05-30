import React, { useState, useEffect, useMemo, useCallback, useRef } from "react";
import { Box, Text, useInput, useFocus } from "ink";
import { SearchBox } from "./SearchBox.tsx";
import { useSearchInput } from "../../hooks/useSearchInput.ts";
import type { BackendClient } from "../../backend/client.ts";

// ─── Types ─────────────────────────────────────────────────────────────────

type Setting =
  | {
      id: string;
      label: string;
      type: "boolean";
      value: boolean;
      onChange: (v: boolean) => void;
    }
  | {
      id: string;
      label: string;
      type: "enum";
      value: string;
      options: string[];
      onChange: (v: string) => void;
    }
  | {
      id: string;
      label: string;
      type: "managedEnum";
      value: string;
      onChange: (v: string) => void;
    }
  | {
      id: string;
      label: string;
      type: "text";
      value: string;
    };

interface ConfigTabProps {
  backend: BackendClient;
  contentHeight: number;
}

interface ConfigResponse {
  version: number;
  agent: {
    default_provider: string;
    default_model: string;
    max_output_tokens: number;
    spec_driven: boolean;
  };
  ui: {
    animations: boolean;
  };
  thinking: {
    level: string;
    budget_tokens: number;
  };
  system_prompt: string;
  security?: {
    autonomy_level?: string;
    sandbox_enabled?: boolean;
  };
  session?: {
    auto_title?: boolean;
  };
  cost?: {
    daily_limit?: number;
  };
  compaction?: {
    threshold?: number;
  };
}

// ─── Fuzzy matching ────────────────────────────────────────────────────────

function fuzzyMatch(
  query: string,
  text: string,
): { score: number; indices: number[] } {
  const lowerQuery = query.toLowerCase();
  const lowerText = text.toLowerCase();

  if (lowerQuery.length === 0) return { score: 1, indices: [] };

  let qi = 0;
  const indices: number[] = [];
  let score = 0;
  let lastMatchIdx = -1;

  for (let ti = 0; ti < lowerText.length && qi < lowerQuery.length; ti++) {
    if (lowerText[ti] === lowerQuery[qi]) {
      indices.push(ti);
      if (lastMatchIdx === ti - 1) {
        score += 2;
      } else {
        score += 1;
      }
      if (ti === 0 || lowerText[ti - 1] === " " || lowerText[ti - 1] === "/") {
        score += 2;
      }
      lastMatchIdx = ti;
      qi++;
    }
  }

  if (qi < lowerQuery.length) return { score: -1, indices: [] };
  return { score, indices };
}

function highlightText(
  text: string,
  indices: number[],
): React.JSX.Element[] {
  if (indices.length === 0) return [<Text key={0}>{text}</Text>];

  const parts: React.JSX.Element[] = [];
  let lastIdx = 0;
  const indexSet = new Set(indices);

  for (let i = 0; i < text.length; i++) {
    if (indexSet.has(i)) {
      if (i > lastIdx) {
        parts.push(<Text key={`t-${lastIdx}`}>{text.slice(lastIdx, i)}</Text>);
      }
      parts.push(
        <Text key={`h-${i}`} color="cyan" bold>
          {text[i]}
        </Text>,
      );
      lastIdx = i + 1;
    }
  }

  if (lastIdx < text.length) {
    parts.push(<Text key={`t-${lastIdx}`}>{text.slice(lastIdx)}</Text>);
  }

  return parts;
}

// ─── Helpers ───────────────────────────────────────────────────────────────

function buildPatch(path: string[], value: unknown): Record<string, unknown> {
  const patch: Record<string, unknown> = {};
  let current = patch;
  for (let i = 0; i < path.length - 1; i++) {
    const level: Record<string, unknown> = {};
    current[path[i]!] = level;
    current = level;
  }
  current[path[path.length - 1]!] = value;
  return patch;
}

function setNested(obj: object, path: string[], value: unknown): void {
  let current: Record<string, unknown> = obj as Record<string, unknown>;
  for (let i = 0; i < path.length - 1; i++) {
    if (!current[path[i]!] || typeof current[path[i]!] !== "object") {
      current[path[i]!] = {} as Record<string, unknown>;
    }
    current = current[path[i]!] as Record<string, unknown>;
  }
  current[path[path.length - 1]!] = value;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

// ─── Component ─────────────────────────────────────────────────────────────

export function ConfigTab({
  backend,
  contentHeight,
}: ConfigTabProps): React.JSX.Element {
  const { isFocused } = useFocus({ isActive: true });

  // ─── State ─────────────────────────────────────────────────────────────
  const [config, setConfig] = useState<ConfigResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isSearchMode, setIsSearchMode] = useState(true);
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [scrollOffset, setScrollOffset] = useState(0);

  const changeSummaryRef = useRef<string[]>([]);
  const isDirtyRef = useRef(false);

  // Load config
  useEffect(() => {
    setLoading(true);
    setError(null);
    backend
      .call<ConfigResponse>("config.get")
      .then((result) => {
        setConfig(result);
        isDirtyRef.current = false;
        changeSummaryRef.current = [];
      })
      .catch((err: unknown) => {
        setError((err as Error).message);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [backend]);

  // ─── Search input hook ───────────────────────────────────────────────────

  const searchInput = useSearchInput({
    isActive: isSearchMode,
    onExitUp: () => setIsSearchMode(false),
    onCancel: () => {},
    backspaceExitsOnEmpty: false,
  });

  // ─── Persist helpers ─────────────────────────────────────────────────────

  const persistBoolean = useCallback(
    (path: string[], label: string, currentVal: boolean) => {
      return (v: boolean) => {
        isDirtyRef.current = true;
        changeSummaryRef.current.push(`${label}: ${currentVal} → ${v}`);
        setConfig((prev) => {
          if (!prev) return prev;
          const next = structuredClone(prev);
          if (isRecord(next)) setNested(next, path, v);
          return next;
        });
        backend
          .call("config.update", { patch: buildPatch(path, v) })
          .catch((err: unknown) => {
            console.error("config.update (persistBool) failed:", err);
            // Rollback on failure
            setConfig((prev) => {
              if (!prev) return prev;
              const next = structuredClone(prev);
              if (isRecord(next)) setNested(next, path, currentVal);
              return next;
            });
          });
      };
    },
    [backend],
  );

  const persistString = useCallback(
    (path: string[], label: string, currentVal: string) => {
      return (v: string) => {
        isDirtyRef.current = true;
        changeSummaryRef.current.push(`${label}: ${currentVal} → ${v}`);
        setConfig((prev) => {
          if (!prev) return prev;
          const next = structuredClone(prev);
          if (isRecord(next)) setNested(next, path, v);
          return next;
        });
        backend
          .call("config.update", { patch: buildPatch(path, v) })
          .catch((err: unknown) => {
            console.error("config.update (persistString) failed:", err);
            setConfig((prev) => {
              if (!prev) return prev;
              const next = structuredClone(prev);
              if (isRecord(next)) setNested(next, path, currentVal);
              return next;
            });
          });
      };
    },
    [backend],
  );

  // ─── Build settings list from config ─────────────────────────────────────

  const allSettings = useMemo<Setting[]>(() => {
    if (!config) return [];
    const agent = config.agent;
    const ui = config.ui;
    const thinking = config.thinking ?? { level: "off", budget_tokens: 0 };
    const security = config.security ?? {};
    const session = config.session ?? {};
    const cost = config.cost ?? {};
    const compaction = config.compaction ?? {};

    const settings: Setting[] = [
      {
        id: "agent.default_provider",
        label: "Default provider",
        type: "managedEnum" as const,
        value: agent.default_provider ?? "",
        onChange: persistString(
          ["agent", "default_provider"],
          "Default provider",
          agent.default_provider ?? "",
        ),
      },
      {
        id: "agent.default_model",
        label: "Default model",
        type: "managedEnum" as const,
        value: agent.default_model ?? "",
        onChange: persistString(
          ["agent", "default_model"],
          "Default model",
          agent.default_model ?? "",
        ),
      },
      {
        id: "agent.max_output_tokens",
        label: "Max output tokens",
        type: "managedEnum" as const,
        value: agent.max_output_tokens != null ? String(agent.max_output_tokens) : "unlimited",
        onChange: (v: string) => {
          const numVal: number | null = v === "unlimited" ? null : parseInt(v, 10);
          isDirtyRef.current = true;
          changeSummaryRef.current.push(`Max output tokens: ${agent.max_output_tokens ?? "unlimited"} → ${v}`);
          setConfig((prev) => {
            if (!prev) return prev;
            const next = structuredClone(prev);
            if (isRecord(next)) setNested(next, ["agent", "max_output_tokens"], numVal);
            return next;
          });
          backend
            .call("config.update", { patch: buildPatch(["agent", "max_output_tokens"], numVal) })
            .catch((err: unknown) => {
              console.error("config.update (max_output_tokens) failed:", err);
              setConfig((prev) => {
                if (!prev) return prev;
                const next = structuredClone(prev);
                if (isRecord(next)) setNested(next, ["agent", "max_output_tokens"], agent.max_output_tokens);
                return next;
              });
            });
        },
      },
      {
        id: "agent.spec_driven",
        label: "Spec driven",
        type: "boolean" as const,
        value: agent.spec_driven ?? false,
        onChange: persistBoolean(
          ["agent", "spec_driven"],
          "Spec driven",
          agent.spec_driven ?? false,
        ),
      },
      {
        id: "thinking.level",
        label: "Thinking level",
        type: "enum" as const,
        value: thinking.level ?? "off",
        options: ["off", "minimal", "low", "medium", "high", "x-high"],
        onChange: persistString(
          ["thinking", "level"],
          "Thinking level",
          thinking.level ?? "off",
        ),
      },
      {
        id: "thinking.budget_tokens",
        label: "Thinking budget",
        type: "text" as const,
        value: thinking.budget_tokens != null ? `${thinking.budget_tokens} tokens` : "not set",
      },
      {
        id: "ui.animations",
        label: "Animations",
        type: "boolean" as const,
        value: ui.animations ?? true,
        onChange: persistBoolean(
          ["ui", "animations"],
          "Animations",
          ui.animations ?? true,
        ),
      },
      {
        id: "security.autonomy_level",
        label: "Autonomy level",
        type: "enum" as const,
        value: security.autonomy_level ?? "default",
        options: ["plan", "default", "acceptEdits"],
        onChange: persistString(
          ["security", "autonomy_level"],
          "Autonomy level",
          security.autonomy_level ?? "default",
        ),
      },
      {
        id: "security.sandbox_enabled",
        label: "Sandbox enabled",
        type: "boolean" as const,
        value: security.sandbox_enabled ?? false,
        onChange: persistBoolean(
          ["security", "sandbox_enabled"],
          "Sandbox enabled",
          security.sandbox_enabled ?? false,
        ),
      },
      {
        id: "session.auto_title",
        label: "Auto title sessions",
        type: "boolean" as const,
        value: session.auto_title ?? true,
        onChange: persistBoolean(
          ["session", "auto_title"],
          "Auto title sessions",
          session.auto_title ?? true,
        ),
      },
      {
        id: "cost.daily_limit",
        label: "Daily cost limit",
        type: "managedEnum" as const,
        value: cost.daily_limit != null ? `$${cost.daily_limit}` : "none",
        onChange: (v: string) => {
          const numVal: number | null = v === "none" ? null : parseInt(v.replace(/^\$/, ""), 10);
          isDirtyRef.current = true;
          changeSummaryRef.current.push(`Daily cost limit: ${cost.daily_limit != null ? `$${cost.daily_limit}` : "none"} → ${v}`);
          setConfig((prev) => {
            if (!prev) return prev;
            const next = structuredClone(prev);
            if (isRecord(next)) setNested(next, ["cost", "daily_limit"], numVal);
            return next;
          });
          backend
            .call("config.update", { patch: buildPatch(["cost", "daily_limit"], numVal) })
            .catch((err: unknown) => {
              console.error("config.update (daily_limit) failed:", err);
              setConfig((prev) => {
                if (!prev) return prev;
                const next = structuredClone(prev);
                if (isRecord(next)) setNested(next, ["cost", "daily_limit"], cost.daily_limit);
                return next;
              });
            });
        },
      },
      {
        id: "compaction.threshold",
        label: "Compaction threshold",
        type: "enum" as const,
        value: String(compaction.threshold ?? 0),
        options: ["0", "50", "75", "100", "125"],
        onChange: persistString(
          ["compaction", "threshold"],
          "Compaction threshold",
          String(compaction.threshold ?? 0),
        ),
      },
    ];

    return settings;
  }, [config, backend, persistBoolean, persistString]);

  // ─── Filter settings ────────────────────────────────────────────────────

  const filtered = useMemo(() => {
    const q = isSearchMode ? searchInput.query : "";
    if (q.length === 0) {
      return allSettings.map((s) => ({
        setting: s,
        score: 1,
        indices: [] as number[],
      }));
    }

    return allSettings
      .map((s) => {
        const idMatch = fuzzyMatch(q, s.id);
        const labelMatch = fuzzyMatch(q, s.label);
        const best =
          idMatch.score >= labelMatch.score ? idMatch : labelMatch;
        return { setting: s, score: best.score, indices: best.indices };
      })
      .filter((r) => r.score >= 0)
      .sort((a, b) => b.score - a.score);
  }, [allSettings, searchInput.query, isSearchMode]);

  // ─── Scroll/pagination ──────────────────────────────────────────────────

  const maxVisible = Math.max(3, contentHeight - 4);
  const visibleStart = scrollOffset;
  const visibleEnd = scrollOffset + maxVisible;
  const visibleItems = filtered.slice(visibleStart, visibleEnd);

  useEffect(() => {
    if (selectedIdx < scrollOffset) {
      setScrollOffset(selectedIdx);
    } else if (selectedIdx >= scrollOffset + maxVisible) {
      setScrollOffset(Math.max(0, selectedIdx - maxVisible + 1));
    }
  }, [selectedIdx, scrollOffset, maxVisible]);

  useEffect(() => {
    setSelectedIdx(0);
    setScrollOffset(0);
  }, [searchInput.query]);

  // ─── Keyboard handling ──────────────────────────────────────────────────

  useInput((input, key) => {
    if (!isFocused) return;

    // Printable chars in list mode → enter search mode
    if (!isSearchMode && input.length === 1 && input >= " " && input !== "/") {
      setIsSearchMode(true);
      searchInput.setQuery(input);
      return;
    }

    // "/" in list mode → enter search mode
    if (!isSearchMode && input === "/") {
      setIsSearchMode(true);
      return;
    }

    if (isSearchMode) {
      const result = searchInput.handleKeyDown(input, key);
      if (result?.handled) {
        return;
      }
      if (key.downArrow) {
        setIsSearchMode(false);
        return;
      }
      if (key.return) {
        setIsSearchMode(false);
        return;
      }
      return;
    }

    // ─── List mode ──────────────────────────────────────────────────────

    if (key.upArrow) {
      if (selectedIdx === 0) {
        setIsSearchMode(true);
      } else {
        setSelectedIdx((prev) => Math.max(0, prev - 1));
      }
      return;
    }

    if (key.downArrow) {
      setSelectedIdx((prev) => Math.min(filtered.length - 1, prev + 1));
      return;
    }

    if (key.return) {
      const selected = filtered[selectedIdx];
      if (!selected) return;
      const s = selected.setting;

      if (s.type === "managedEnum") {
        // Phase 2 will open picker overlay
      } else if (s.type === "boolean") {
        s.onChange(!s.value);
      } else if (s.type === "enum") {
        const idx = s.options.indexOf(s.value);
        const next = s.options[(idx + 1) % s.options.length];
        if (next) s.onChange(next);
      }
      return;
    }

    // Space toggles booleans
    if (input === " ") {
      const selected = filtered[selectedIdx];
      if (!selected || selected.setting.type !== "boolean") return;
      selected.setting.onChange(!selected.setting.value);
      return;
    }

    // ←/→ cycles enum values
    if (key.leftArrow || key.rightArrow) {
      const selected = filtered[selectedIdx];
      if (!selected || selected.setting.type !== "enum") return;
      const s = selected.setting;
      const idx = s.options.indexOf(s.value);
      const next = key.rightArrow
        ? s.options[(idx + 1) % s.options.length]
        : s.options[(idx - 1 + s.options.length) % s.options.length];
      if (next) s.onChange(next);
      return;
    }
  });

  // ─── Render helper for setting rows ─────────────────────────────────────

  const renderSetting = (
    setting: Setting,
    isSelected: boolean,
    matchIndices: number[],
  ): React.JSX.Element => {
    const indent = "  ";

    // Special: thinking.budget_tokens is display-only
    if (setting.id === "thinking.budget_tokens" && config) {
      return (
        <Box key={setting.id} paddingX={1}>
          <Text color={isSelected ? "cyan" : undefined}>
            {isSelected ? "▸ " : indent}
          </Text>
          <Text color={isSelected ? "white" : undefined} bold={isSelected}>
            {matchIndices.length > 0
              ? highlightText(setting.label, matchIndices)
              : setting.label}
          </Text>
          <Text dimColor>: </Text>
          <Text>{config.thinking?.budget_tokens != null ? `${config.thinking.budget_tokens} tokens` : "not set"}</Text>
        </Box>
      );
    }

    const renderValue = (): React.JSX.Element => {
      if (setting.type === "boolean") {
        return (
          <Text color={setting.value ? "green" : "red"}>
            {setting.value ? "✓ on" : "✗ off"}
          </Text>
        );
      }
      if (setting.type === "enum") {
        return (
          <Box>
            <Text dimColor>{"◂ "}</Text>
            <Text>{setting.value}</Text>
            <Text dimColor>{" ▸"}</Text>
          </Box>
        );
      }
      // managedEnum
      return (
        <Box>
          <Text>{setting.value}</Text>
          <Text dimColor>{" ▸"}</Text>
        </Box>
      );
    };

    return (
      <Box key={setting.id} paddingX={1}>
        <Text color={isSelected ? "cyan" : undefined}>
          {isSelected ? "▸ " : indent}
        </Text>
        <Box flexGrow={1}>
          <Text color={isSelected ? "white" : undefined} bold={isSelected}>
            {matchIndices.length > 0
              ? highlightText(setting.label, matchIndices)
              : setting.label}
          </Text>
        </Box>
        <Text dimColor>  </Text>
        {renderValue()}
      </Box>
    );
  };

  // ─── Footer ─────────────────────────────────────────────────────────────

  const footer = isSearchMode ? (
    <Box paddingX={1} marginTop={0}>
      <Text dimColor>
        type to filter · ↓ to list · Esc to close
      </Text>
    </Box>
  ) : (
    <Box paddingX={1} marginTop={0}>
      <Text dimColor>
        ↑↓ navigate · Space toggle · ←→ cycle · / search · Enter select · Esc close
      </Text>
    </Box>
  );

  // ─── Render ─────────────────────────────────────────────────────────────

  if (loading) {
    return (
      <Box flexDirection="column" paddingX={1}>
        <Box marginBottom={1}>
          <Text color="yellow">Loading configuration...</Text>
        </Box>
      </Box>
    );
  }

  if (error) {
    return (
      <Box flexDirection="column" paddingX={1}>
        <Box marginBottom={1}>
          <Text color="red">Error: {error}</Text>
        </Box>
      </Box>
    );
  }

  return (
    <Box flexDirection="column" width="100%">
      {/* Search box */}
      <Box marginBottom={1} paddingX={1}>
        <SearchBox
          query={searchInput.query}
          placeholder={
            isSearchMode ? "Search settings..." : "Press / to search..."
          }
          isFocused={isSearchMode}
          isTerminalFocused={isFocused}
          cursorOffset={searchInput.cursorOffset}
          borderless
        />
      </Box>

      {/* Scroll indicator above */}
      {!isSearchMode && visibleStart > 0 && (
        <Box paddingX={1}>
          <Text dimColor>↑ {visibleStart} more above</Text>
        </Box>
      )}

      {/* Settings list */}
      <Box flexDirection="column">
        {!isSearchMode && filtered.length === 0 && (
          <Box paddingX={1}>
            <Text dimColor>No matching settings</Text>
          </Box>
        )}
        {visibleItems.map((item, i) =>
          renderSetting(
            item.setting,
            !isSearchMode && visibleStart + i === selectedIdx,
            isSearchMode ? item.indices : [],
          ),
        )}
      </Box>

      {/* Scroll indicator below */}
      {!isSearchMode && visibleEnd < filtered.length && (
        <Box paddingX={1}>
          <Text dimColor>↓ {filtered.length - visibleEnd} more below</Text>
        </Box>
      )}

      {/* Footer */}
      {footer}
    </Box>
  );
}
