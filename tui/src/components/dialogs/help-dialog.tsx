import React from "react";
import { Box, Text } from "ink";
import { DialogContainer } from "./dialog-container.tsx";

interface HelpDialogProps {
  open: boolean;
  onClose: () => void;
}

interface ShortcutGroup {
  title: string;
  shortcuts: Array<{ key: string; description: string }>;
}

const SHORTCUT_GROUPS: ShortcutGroup[] = [
  {
    title: "General",
    shortcuts: [
      { key: "Ctrl+K", description: "Open command palette" },
      { key: "Ctrl+B", description: "Toggle sidebar" },
      { key: "Ctrl+C", description: "Quit Overkill" },
      { key: "Esc", description: "Close dialog / Go back" },
    ],
  },
  {
    title: "Navigation",
    shortcuts: [
      { key: "↑/↓", description: "Navigate lists" },
      { key: "Enter", description: "Select item" },
      { key: "Tab", description: "Switch sidebar tab" },
    ],
  },
  {
    title: "Chat",
    shortcuts: [
      { key: "Ctrl+Enter", description: "Send message" },
      { key: "Ctrl+L", description: "Clear chat" },
    ],
  },
];

export function HelpDialog({
  open,
  onClose,
}: HelpDialogProps): React.JSX.Element | null {
  if (!open) return null;

  return (
    <DialogContainer open={open} onClose={onClose} title="Keyboard Shortcuts">
      <Box flexDirection="column" paddingX={1}>
        {SHORTCUT_GROUPS.map((group, gi) => (
          <Box key={gi} flexDirection="column" marginBottom={1}>
            <Text color="cyan" bold>
              {group.title}
            </Text>
            {group.shortcuts.map((s, si) => (
              <Box key={si} paddingLeft={1}>
                <Box width={14}>
                  <Text color="yellow">{s.key}</Text>
                </Box>
                <Text dimColor>{s.description}</Text>
              </Box>
            ))}
          </Box>
        ))}
      </Box>
    </DialogContainer>
  );
}
