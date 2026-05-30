import React, { useState, useMemo } from "react";
import { Box, Text, useInput } from "ink";
import { useTheme } from "../../hooks/use-theme.ts";

// ─── Types ─────────────────────────────────────────────────────────────────

interface SkillInfo {
  name: string;
  description: string;
  category: string;
  tags: string[];
  enabled: boolean;
  bundled: boolean;
}

// ─── Bundled skills registry (mirrors skills/ directory) ────────────────────

const BUNDLED_SKILLS: SkillInfo[] = [
  {
    name: "code-review", description: "Disciplined code review pass focused on correctness, security, and maintainability.",
    category: "review", tags: ["code", "review", "quality", "security"], enabled: true, bundled: true,
  },
  {
    name: "debugging", description: "Systematic 4-phase root-cause debugging. Never fix before understanding.",
    category: "debugging", tags: ["debug", "diagnose", "fix"], enabled: true, bundled: true,
  },
  {
    name: "bug-hunt", description: "Find and fix bugs systematically. Use when tests fail or behavior is unexpected.",
    category: "debugging", tags: ["bug", "test", "fix"], enabled: true, bundled: true,
  },
  {
    name: "git-workflow", description: "Structured Git workflow: commit, branch, PR, rebase, and merge strategies.",
    category: "workflow", tags: ["git", "commit", "branch", "pr"], enabled: true, bundled: true,
  },
  {
    name: "testing-pipeline", description: "Design and run a multi-phase test pipeline for code changes.",
    category: "testing", tags: ["test", "quality", "pipeline"], enabled: true, bundled: true,
  },
  {
    name: "docx", description: "Create, edit, and analyze .docx files with tracked changes, comments, and formatting.",
    category: "document", tags: ["docx", "word", "document"], enabled: false, bundled: true,
  },
  {
    name: "self-modify", description: "Modify Overkill's own configuration, skills, and personality via conversation.",
    category: "meta", tags: ["self", "config", "modify"], enabled: true, bundled: true,
  },
  {
    name: "mutation-test", description: "Run mutation testing to verify test suite quality and find weak assertions.",
    category: "testing", tags: ["mutation", "test", "quality"], enabled: false, bundled: true,
  },
  {
    name: "frontend-design", description: "Design and build frontend components with best practices for accessibility and performance.",
    category: "development", tags: ["frontend", "design", "ui", "css"], enabled: true, bundled: true,
  },
  {
    name: "understand-anything", description: "Deep comprehension of unfamiliar codebases, concepts, or systems.",
    category: "analysis", tags: ["learn", "understand", "codebase"], enabled: true, bundled: true,
  },
  {
    name: "red-team", description: "Adversarial testing: find security vulnerabilities, prompt injection vectors, and safety issues.",
    category: "security", tags: ["security", "adversarial", "red-team"], enabled: false, bundled: true,
  },
  {
    name: "humanizer", description: "Make AI responses more natural, empathetic, and human-like in tone and style.",
    category: "style", tags: ["tone", "style", "human"], enabled: true, bundled: true,
  },
];

const MAX_DISPLAY = 12;

// ─── Component ──────────────────────────────────────────────────────────────

export function SkillsPanel({
  active,
}: {
  active: boolean;
}): React.JSX.Element {
  const { theme } = useTheme();

  const [skills, setSkills] = useState<SkillInfo[]>(BUNDLED_SKILLS);
  const [search, setSearch] = useState("");
  const [focusedIdx, setFocusedIdx] = useState(-1);
  const [isSearching, setIsSearching] = useState(false);

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

  const toggleSkill = (name: string) => {
    setSkills((prev) =>
      prev.map((s) => (s.name === name ? { ...s, enabled: !s.enabled } : s)),
    );
  };

  const enabledCount = skills.filter((s) => s.enabled).length;

  // Keyboard handling
  useInput((input, key) => {
    if (!active) return;

    if (isSearching) {
      if (key.escape) { setIsSearching(false); setSearch(""); return; }
      if (key.return) { setIsSearching(false); return; }
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

    if (key.escape) { setFocusedIdx(-1); return; }
    if (input === "/") { setIsSearching(true); return; }
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
          {" "}({enabledCount}/{skills.length} enabled)
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
        const desc = skill.description.length > 28
          ? skill.description.slice(0, 28) + "…"
          : skill.description;

        return (
          <Box key={skill.name} paddingX={1} flexDirection="column">
            <Box>
              <Text color={isFocused ? theme.accent : theme.muted}>
                {isFocused ? "▸" : " "}
              </Text>
              <Text color={skill.enabled ? theme.success : theme.muted}>
                {" "}[{skill.enabled ? "✓" : "✗"}]
              </Text>
              <Text
                bold={isFocused}
                color={skill.enabled ? theme.text : theme.muted}
                dimColor={!skill.enabled}
              >
                {" "}{skill.name}
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
