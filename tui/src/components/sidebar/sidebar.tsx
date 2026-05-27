import React from "react";
import { Box, Text } from "ink";
import type { SidebarTab } from "../../hooks/use-sidebar.ts";

const TABS: { id: SidebarTab; label: string }[] = [
  { id: "sessions", label: "Sessions" },
  { id: "tools", label: "Tools" },
  { id: "files", label: "Files" },
  { id: "agents", label: "Agents" },
];

interface SidebarProps {
  visible: boolean;
  activeTab: SidebarTab;
  onTabChange: (tab: SidebarTab) => void;
  children: React.ReactNode;
}

const SIDEBAR_WIDTH = 30;

export function Sidebar({
  visible,
  activeTab,
  onTabChange,
  children,
}: SidebarProps): React.JSX.Element | null {
  if (!visible) return null;

  return (
    <Box flexDirection="column" width={SIDEBAR_WIDTH} flexShrink={0}>
      {/* Tab bar */}
      <Box>
        <Text>│ </Text>
        {TABS.map((tab, i) => {
          const isActive = tab.id === activeTab;
          return (
            <React.Fragment key={tab.id}>
              {i > 0 && <Text> </Text>}
              <Text
                inverse={isActive}
                bold={isActive}
                underline={!isActive}
                color={isActive ? "cyan" : undefined}
              >
                {tab.label}
              </Text>
            </React.Fragment>
          );
        })}
      </Box>
      {/* Separator */}
      <Box>
        <Text dimColor>{"─".repeat(SIDEBAR_WIDTH)}</Text>
      </Box>
      {/* Content area */}
      <Box flexDirection="column" flexGrow={1} overflow="hidden">
        {children}
      </Box>
      {/* Bottom separator */}
      <Box>
        <Text dimColor>{"─".repeat(SIDEBAR_WIDTH)}</Text>
      </Box>
    </Box>
  );
}
