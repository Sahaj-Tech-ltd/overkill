import React, { useState, useMemo } from "react";
import { Box, Text } from "ink";
import { useTheme } from "../../hooks/use-theme.ts";
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
  /** Children (todo, skills, etc.) */
  children?: React.ReactNode;
}

const SIDEBAR_WIDTH = 42;
const MAX_VISIBLE_FILES = 8;

export function Sidebar({
  title,
  directory,
  branch,
  tokens = 0,
  contextPercent,
  cost,
  files = [],
  version = "v3",
  children,
}: SidebarProps): React.JSX.Element {
  const { theme } = useTheme();
  const [fileScrollOffset, setFileScrollOffset] = useState(0);
  
  const pathLabel = directory
    ? directory.replace(process.env.HOME || "/home/user", "~")
    : ".";
  const fullPath = branch ? `${pathLabel}:${branch}` : pathLabel;
  const dirParts = fullPath.split("/");
  const dirParent = dirParts.slice(0, -1).join("/");
  const dirName = dirParts.at(-1) ?? "";

  const money = cost != null ? `$${cost.toFixed(2)}` : null;

  // File scrolling logic
  const totalFiles = files.length;
  const canScrollUp = fileScrollOffset > 0;
  const canScrollDown = totalFiles > MAX_VISIBLE_FILES && fileScrollOffset < totalFiles - MAX_VISIBLE_FILES;
  const visibleFiles = files.slice(fileScrollOffset, fileScrollOffset + MAX_VISIBLE_FILES);

  return (
    <Box
      flexDirection="column"
      width={SIDEBAR_WIDTH}
      flexShrink={0}
      paddingLeft={1}
      paddingRight={1}
      overflow="hidden"
    >
      {/* Title */}
      {title && (
        <Box paddingBottom={1} flexShrink={0}>
          <Text bold color={theme.text}>
            {title.length > 36 ? title.slice(0, 33) + "…" : title}
          </Text>
        </Box>
      )}

      {/* Scrollable content area */}
      <Box flexDirection="column" flexGrow={1} overflow="hidden">
        {/* Context */}
        <Box flexDirection="column" paddingBottom={1} flexShrink={0}>
          <Text bold color={theme.text}>Context</Text>
          <Text color={theme.muted}>{tokens.toLocaleString()} tokens</Text>
          {contextPercent != null && (
            <Text color={theme.muted}>{contextPercent}% used</Text>
          )}
          {money && <Text color={theme.muted}>{money} spent</Text>}
        </Box>

        {/* Todo + Skills (delegated to children) */}
        {children}

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
                <Box key={`${f.file}-${i}`} flexDirection="row" justifyContent="space-between">
                  <Text color={theme.muted} wrap="truncate-end">
                    {f.file.length > 24 ? "…" + f.file.slice(f.file.length - 21) : f.file}
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
            <Text color={theme.text}>{dirName}</Text>
          </Text>
          <Text>
            <Text color={theme.success}>• </Text>
            <Text bold color={theme.text}>Over</Text>
            <Text bold color={theme.accent}>kill</Text>
            <Text color={theme.muted}> {version}</Text>
          </Text>
        </Box>
      </Box>
    </Box>
  );
}

// ─── Collapsible Section ─────────────────────────────────────────────────

function CollapsibleSection({
  title,
  count,
  defaultOpen,
  theme,
  children,
}: {
  title: string;
  count: number;
  defaultOpen: boolean;
  theme: Theme;
  children: React.ReactNode;
}): React.JSX.Element {
  const [open, setOpen] = useState(defaultOpen);
  const collapsible = count > 2;

  return (
    <Box flexDirection="column" paddingBottom={1}>
      <Box flexDirection="row">
        {collapsible && (
          <Text color={theme.text}>{open ? "▼" : "▶"} </Text>
        )}
        <Text bold color={theme.text}>{title}</Text>
      </Box>
      {(!collapsible || open) && children}
    </Box>
  );
}
