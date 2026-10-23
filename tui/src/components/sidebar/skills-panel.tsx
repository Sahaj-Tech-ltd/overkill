import React, { useState, useMemo, useEffect, useSyncExternalStore } from "react";
import { Box, Text, useInput } from "ink";
import { useTheme } from "../../hooks/use-theme.ts";
import type { BackendClient } from "../../backend/client.ts";

import { SkillsPanelModel } from "./panel-models.ts";

const MAX_DISPLAY = 12;

// ─── Component ──────────────────────────────────────────────────────────────

export function SkillsPanel({
  active,
  backend,
}: {
  active: boolean;
  backend: BackendClient;
}): React.JSX.Element {
  const { theme } = useTheme();

  const model = useMemo(() => new SkillsPanelModel(backend), [backend]);
  const { skills, busy, error } = useSyncExternalStore(model.subscribe, model.getSnapshot);
  const [search, setSearch] = useState("");
  const [focusedIdx, setFocusedIdx] = useState(-1);
  const [isSearching, setIsSearching] = useState(false);

  useEffect(() => { void model.reload(); return () => model.dispose(); }, [model]);

  const filtered = useMemo(() => {
    if (!search.trim()) return skills;
    const q = search.toLowerCase();
    return skills.filter(
      (s) =>
        s.name.toLowerCase().includes(q) ||
        s.description.toLowerCase().includes(q) ||
        (s.tags ?? []).some((t) => t.toLowerCase().includes(q)) ||
        s.category.toLowerCase().includes(q),
    );
  }, [skills, search]);

  const enabledCount = skills.filter((s) => s.enabled).length;

  // Keyboard handling
  useInput((input, key) => {
    if (!active || busy) return;

    if (isSearching) {
      if (key.escape) {
        setIsSearching(false);
        setSearch("");
        return;
      }
      if (key.return) {
        setIsSearching(false);
        return;
      }
      if (key.delete || key.backspace) {
        setSearch((p) => p.slice(0, -1));
        return;
      }
      if (input.length === 1 && input >= " ") {
        setSearch((p) => p + input);
        return;
      }
      return;
    }

    if (key.escape) {
      setFocusedIdx(-1);
      return;
    }
    if (input === "r") { void model.reload(); return; }
    if (input === "/") {
      setIsSearching(true);
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
    if (input === " " || key.return) {
      if (focusedIdx >= 0 && focusedIdx < filtered.length) {
        const skill = filtered[focusedIdx]!;
        void model.setEnabled(skill.name, !skill.enabled);
      }
      return;
    }
  });

  return (
    <Box flexDirection="column" overflow="hidden">
      {/* Header */}
      <Box paddingX={1}>
        <Text color={theme.accent} bold>
          Skills
        </Text>
        <Text dimColor>
          {" "}
          ({enabledCount}/{skills.length} enabled)
        </Text>
      </Box>

      {error && <Text color={theme.error}>{error} · r:retry</Text>}
      {busy && <Text dimColor>Updating skills…</Text>}
      {/* Search bar */}
      <Box paddingX={1} marginTop={1}>
        {isSearching ? (
          <Box>
            <Text color={theme.accent}>/</Text>
            <Text>{search || " "}</Text>
            <Text inverse> </Text>
          </Box>
        ) : (
          <Text dimColor>/ search · space toggle · j/k nav</Text>
        )}
      </Box>

      <Box>
        <Text dimColor>{"─".repeat(28)}</Text>
      </Box>

      {/* Skill list */}
      {filtered.length === 0 && (
        <Box paddingX={1} marginTop={1}>
          <Text dimColor>
            {search ? `No skills matching "${search}"` : "No skills loaded."}
          </Text>
        </Box>
      )}

      {filtered.slice(0, MAX_DISPLAY).map((skill, i) => {
        const isFocused = i === focusedIdx;
        const desc =
          skill.description.length > 28
            ? skill.description.slice(0, 28) + "…"
            : skill.description;

        return (
          <Box key={skill.name} paddingX={1} flexDirection="column">
            <Box>
              <Text color={isFocused ? theme.accent : theme.muted}>
                {isFocused ? "▸" : " "}
              </Text>
              <Text color={skill.enabled ? theme.success : theme.muted}>
                {" "}
                [{skill.enabled ? "✓" : "✗"}]
              </Text>
              <Text
                bold={isFocused}
                color={skill.enabled ? theme.text : theme.muted}
                dimColor={!skill.enabled}
              >
                {" "}
                {skill.name}
              </Text>
            </Box>
            <Box paddingLeft={4}>
              <Text dimColor>{desc}</Text>
            </Box>
            <Box paddingLeft={4}>
              <Text dimColor>
                {skill.category}
                {skill.bundled ? " · bundled" : " · user"}
              </Text>
            </Box>
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
        <Box paddingX={1} marginTop={1}>
          <Text dimColor>/:search · j/k:nav · space:toggle</Text>
        </Box>
      )}
    </Box>
  );
}
