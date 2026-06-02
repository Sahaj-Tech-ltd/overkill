import React, { useState, useMemo, useEffect } from "react";
import { Box, Text, useInput } from "ink";
import { useTheme } from "../../hooks/use-theme.ts";
import type { BackendClient } from "../../backend/client.ts";

// ─── Types ─────────────────────────────────────────────────────────────────

interface SkillInfo {
  name: string;
  description: string;
  category: string;
  tags: string[];
  enabled: boolean;
  bundled: boolean;
}

// TODO(backend): wire to RPC once skills.* methods are added to server.go
// Planned calls:
//   mount:  backend.call<SkillInfo[]>("skills.list")
//   toggle: backend.call("skills.toggle", { name, enabled })

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

  const [skills, setSkills] = useState<SkillInfo[]>([]);
  const [search, setSearch] = useState("");
  const [focusedIdx, setFocusedIdx] = useState(-1);
  const [isSearching, setIsSearching] = useState(false);

  // Mount: load skills from backend.
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    if (!mounted) {
      setMounted(true);
      backend
        .call<SkillInfo[]>("skills.list")
        .then((list) => {
          if (list?.length) setSkills(list);
        })
        .catch(() => {});
    }
  }, [backend, mounted]);

  const toggleSkill = (name: string) => {
    backend
      .call<{ name: string; enabled: boolean }>("skills.toggle", { name })
      .then((result) => {
        setSkills((prev) =>
          prev.map((s) =>
            s.name === name ? { ...s, enabled: result.enabled } : s,
          ),
        );
      })
      .catch(() => {
        // Fallback: optimistic toggle
        setSkills((prev) =>
          prev.map((s) =>
            s.name === name ? { ...s, enabled: !s.enabled } : s,
          ),
        );
      });
  };

  const filtered = useMemo(() => {
    if (!search.trim()) return skills;
    const q = search.toLowerCase();
    return skills.filter(
      (s) =>
        s.name.toLowerCase().includes(q) ||
        s.description.toLowerCase().includes(q) ||
        s.tags.some((t) => t.toLowerCase().includes(q)) ||
        s.category.toLowerCase().includes(q),
    );
  }, [skills, search]);

  const enabledCount = skills.filter((s) => s.enabled).length;

  // Keyboard handling
  useInput((input, key) => {
    if (!active) return;

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
        toggleSkill(filtered[focusedIdx]!.name);
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
