import React, { useState, useMemo } from "react";
import { Box, Text, useInput } from "ink";
import { useTheme } from "../../hooks/use-theme.ts";
import type { SidebarTab } from "../../hooks/use-sidebar.ts";
import type { Theme } from "../../themes/definitions.ts";

// ─── Sidebar — OpenCode-style layout ─────────────────────────────────────

interface SidebarProps {
  /** Session title */
  title?: string;
  /** Current directory */
  directory?: string;
  /** Git branch */
  branch?: string;
  /** Token count */
  tokens?: number;
  /** Context window % used */
  contextPercent?: number;
  /** Cost in USD */
  cost?: number;
  /** Modified files with deltas */
  files?: Array<{ file: string; additions: number; deletions: number }>;
  /** App version */
  version?: string;
  /** Active tab */
  activeTab?: SidebarTab;
  /** Called when user switches tabs */
  onTabChange?: (tab: SidebarTab) => void;
  /** Panel children keyed by tab name */
  panels?: Record<string, React.ReactNode>;
  /** When true, the main chat input or a modal is focused — skip global key handlers */
  isInputFocused?: boolean;
}

const SIDEBAR_WIDTH = 42;
const MAX_VISIBLE_FILES = 8;
const TABS: Array<{ id: SidebarTab; label: string }> = [
  { id: "sessions", label: "🎙 Sessions" },
  { id: "todos", label: "✅ Todo" },
  { id: "skills", label: "🧠 Skills" },
  { id: "agents", label: "🤖 Agents" },
  { id: "self-eval", label: "💭 Eval" },
  { id: "tests", label: "🧪 Tests" },
  { id: "wizard", label: "🪄 Wizard" },
  { id: "queue", label: "📋 Queue" },
];

export function Sidebar({
  title,
  directory,
  branch,
  tokens = 0,
  contextPercent,
  cost,
  files = [],
  version = "v1",
  activeTab = "sessions",
  onTabChange,
  panels = {},
  isInputFocused = false,
}: SidebarProps): React.JSX.Element {
  const { theme } = useTheme();
  const [fileScrollOffset, setFileScrollOffset] = useState(0);

  const pathLabel = directory
    ? directory.replace(process.env.HOME || "/home/user", "~")
    : ".";
  const fullPath = branch ? `${pathLabel}:${branch}` : pathLabel;
  const dirParts = fullPath.split("/");
  const dirName = dirParts[dirParts.length - 1] ?? fullPath;
  const dirParent = dirParts.slice(0, -1).join("/") || "/";

  const money = cost != null ? `$${cost.toFixed(2)}` : null;

  const totalFiles = files.length;
  const visibleFiles = files.slice(
    fileScrollOffset,
    fileScrollOffset + MAX_VISIBLE_FILES,
  );
  const canScrollUp = fileScrollOffset > 0;
  const canScrollDown = fileScrollOffset + MAX_VISIBLE_FILES < totalFiles;

  // Keyboard handlers for file scrolling
  useInput(
    (_input, key) => {
      if (key.downArrow && canScrollDown) {
        setFileScrollOffset((prev) =>
          Math.min(prev + 1, totalFiles - MAX_VISIBLE_FILES),
        );
      }
      if (key.upArrow && canScrollUp) {
        setFileScrollOffset((prev) => Math.max(0, prev - 1));
      }
      if (key.pageDown && canScrollDown) {
        setFileScrollOffset((prev) =>
          Math.min(prev + MAX_VISIBLE_FILES, totalFiles - MAX_VISIBLE_FILES),
        );
      }
      if (key.pageUp && canScrollUp) {
        setFileScrollOffset((prev) => Math.max(0, prev - MAX_VISIBLE_FILES));
      }
    },
    { isActive: !isInputFocused },
  );

  return (
    <Box
      flexDirection="column"
      width={SIDEBAR_WIDTH}
      flexShrink={0}
      borderStyle="single"
      borderColor={theme.border}
      paddingX={1}
    >
      {/* Header — session title */}
      <Box flexDirection="column" flexShrink={0} paddingBottom={1}>
        <Text bold color={theme.accent}>
          {title ?? "Overkill"}
        </Text>
        {version && (
          <Text dimColor color={theme.muted}>
            {version}
          </Text>
        )}
      </Box>

      {/* Tabs — horizontal row */}
      <Box flexDirection="row" flexShrink={0} paddingBottom={1} flexWrap="wrap">
        {TABS.map((tab) => (
          <Box key={tab.id} paddingRight={1}>
            <Text
              color={activeTab === tab.id ? theme.accent : theme.muted}
              dimColor={activeTab !== tab.id}
            >
              {tab.label}
            </Text>
          </Box>
        ))}
      </Box>

      {/* Scrollable content area */}
      <Box flexDirection="column" flexGrow={1} overflow="hidden">
        {/* Context */}
        <Box flexDirection="column" paddingBottom={1} flexShrink={0}>
          <Text bold color={theme.text}>
            Context
          </Text>
          <Text color={theme.muted}>{tokens.toLocaleString()} tokens</Text>
          {contextPercent != null && (
            <Text color={theme.muted}>{contextPercent}% used</Text>
          )}
          {money && <Text color={theme.muted}>{money} spent</Text>}
        </Box>

        {/* Active panel — tab-selected content */}
        {panels[activeTab]}

        {/* Modified Files — with scrollbar */}
        {files.length > 0 && (
          <Box flexDirection="column" flexGrow={1} overflow="hidden">
            <CollapsibleSection
              title="Modified Files"
              count={files.length}
              defaultOpen={true}
              theme={theme}
            >
              {/* Scroll up indicator */}
              {canScrollUp && (
                <Box>
                  <Text dimColor color={theme.muted}>
                    ↑ {fileScrollOffset} more files
                  </Text>
                </Box>
              )}

              {/* Visible files */}
              {visibleFiles.map((f, i) => (
                <Box
                  key={`${f.file}-${i}`}
                  flexDirection="row"
                  justifyContent="space-between"
                >
                  <Text color={theme.muted} wrap="truncate-end">
                    {f.file.length > 24
                      ? "…" + f.file.slice(f.file.length - 21)
                      : f.file}
                  </Text>
                  <Box flexDirection="row" flexShrink={0}>
                    {f.additions > 0 && (
                      <Text color={theme.success}>+{f.additions}</Text>
                    )}
                    {f.deletions > 0 && (
                      <Text color={theme.error}>-{f.deletions}</Text>
                    )}
                  </Box>
                </Box>
              ))}

              {/* Scroll down indicator */}
              {canScrollDown && (
                <Box>
                  <Text dimColor color={theme.muted}>
                    ↓ {totalFiles - fileScrollOffset - MAX_VISIBLE_FILES} more
                  </Text>
                </Box>
              )}

              {/* Scrollbar track when files overflow */}
              {totalFiles > MAX_VISIBLE_FILES && (
                <Box marginTop={1}>
                  <Text dimColor color={theme.muted}>
                    {totalFiles} files · scroll to see all
                  </Text>
                </Box>
              )}
            </CollapsibleSection>
          </Box>
        )}
      </Box>

      {/* Footer — fixed at bottom */}
      <Box flexDirection="column" flexShrink={0} paddingTop={1}>
        <Box>
          <Text dimColor>{"─".repeat(SIDEBAR_WIDTH - 2)}</Text>
        </Box>
        <Box flexDirection="column" paddingTop={1}>
          <Text>
            <Text color={theme.muted}>{dirParent}/</Text>
            <Text bold color={theme.text}>
              {dirName}
            </Text>
          </Text>
        </Box>
      </Box>
    </Box>
  );
}

// ─── CollapsibleSection ──────────────────────────────────────────────────

interface CollapsibleSectionProps {
  title: string;
  count?: number;
  defaultOpen?: boolean;
  theme: Theme;
  children: React.ReactNode;
}

function CollapsibleSection({
  title,
  count,
  defaultOpen = false,
  theme,
  children,
}: CollapsibleSectionProps): React.JSX.Element {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <Box flexDirection="column" flexShrink={0}>
      <Box>
        <Text color={theme.muted} dimColor={!open}>
          {open ? "▼" : "▶"} {title}
          {count != null && ` (${count})`}
        </Text>
      </Box>
      {open && (
        <Box flexDirection="column" paddingLeft={1}>
          {children}
        </Box>
      )}
    </Box>
  );
}
