import React, { useMemo } from "react";
import { Box, Text, useApp, useInput } from "ink";
import { useBackend } from "./hooks/use-backend.ts";
import { useDialogs } from "./hooks/use-dialogs.ts";
import { useSidebar } from "./hooks/use-sidebar.ts";
import { useTheme } from "./hooks/use-theme.ts";
import { useToast } from "./hooks/use-toast.ts";
import { StatusBar } from "./components/status-bar.tsx";
import { ChatView } from "./components/chat/chat-view.tsx";
import { CommandPalette } from "./components/dialogs/command-palette.tsx";
import { ModelSwitcher } from "./components/dialogs/model-switcher.tsx";
import { SessionManager } from "./components/dialogs/session-manager.tsx";
import { HelpDialog } from "./components/dialogs/help-dialog.tsx";
import { ToastContainer } from "./components/toast.tsx";
import { Sidebar } from "./components/sidebar/sidebar.tsx";
import { SessionPanel } from "./components/sidebar/session-panel.tsx";
import type { ModelInfo, SessionInfo } from "./backend/types.ts";

export function App(): React.JSX.Element {
  const { backend, connected } = useBackend();
  const { openDialog, open, close } = useDialogs();
  const { visible: sidebarVisible, activeTab, toggle, setTab } = useSidebar();
  const { exit } = useApp();
  const { theme } = useTheme();
  const { toasts } = useToast();

  const handleModelSelect = (provider: string, model: ModelInfo) => {
    void provider;
    void model;
  };

  const handleSessionSelect = (session: SessionInfo) => {
    void session;
  };

  const handleSidebarSessionSelect = (session: SessionInfo) => {
    handleSessionSelect(session);
  };

  const commands = useMemo(
    () => [
      {
        id: "switch-model",
        title: "Switch Model",
        description: "Change the active AI model",
        keybind: "",
        action: () => open("model-switcher"),
      },
      {
        id: "new-session",
        title: "New Session",
        description: "Create a new chat session",
        keybind: "",
        action: () => open("session-manager"),
      },
      {
        id: "switch-session",
        title: "Switch Session",
        description: "Switch to a different session",
        keybind: "",
        action: () => open("session-manager"),
      },
      {
        id: "settings",
        title: "Settings",
        description: "Open settings",
        keybind: "",
        action: () => {},
      },
      {
        id: "help",
        title: "Keyboard Shortcuts",
        description: "Show all keyboard shortcuts",
        keybind: "Ctrl+?",
        action: () => open("help"),
      },
      {
        id: "quit",
        title: "Quit",
        description: "Exit Overkill",
        keybind: "Ctrl+C",
        action: () => exit(),
      },
    ],
    [open, exit],
  );

  const isDialogOpen = openDialog !== null;

  useInput((input, key) => {
    if (key.ctrl && input === "k" && !isDialogOpen) {
      open("command-palette");
    }
    if (key.ctrl && input === "b") {
      toggle();
    }
    if (key.ctrl && input === "c") {
      exit();
    }
  });

  return (
    <Box flexDirection="column" width="100%" height="100%">
      <Box flexDirection="row" flexGrow={1} width="100%">
        <ChatView
          backend={backend}
          onOpenPalette={() => open("command-palette")}
          isDialogOpen={isDialogOpen}
        />
        <Sidebar
          visible={sidebarVisible}
          activeTab={activeTab}
          onTabChange={setTab}
        >
          {activeTab === "sessions" && (
            <SessionPanel
              backend={backend}
              onSessionSelect={handleSidebarSessionSelect}
            />
          )}
          {activeTab === "tools" && (
            <Box paddingX={1}>
              <Text dimColor>No tool calls yet</Text>
            </Box>
          )}
          {activeTab === "files" && (
            <Box paddingX={1}>
              <Text dimColor>No files modified</Text>
            </Box>
          )}
        </Sidebar>
      </Box>
      <StatusBar connectionState={connected} theme={theme} />

      {/* Toast notifications */}
      <ToastContainer toasts={toasts} />

      {/* Dialog overlays */}
      <CommandPalette
        open={openDialog === "command-palette"}
        onClose={close}
        commands={commands}
      />
      <ModelSwitcher
        open={openDialog === "model-switcher"}
        onClose={close}
        backend={backend}
        onSelect={handleModelSelect}
      />
      <SessionManager
        open={openDialog === "session-manager"}
        onClose={close}
        backend={backend}
        onSessionSelect={handleSessionSelect}
      />
      <HelpDialog open={openDialog === "help"} onClose={close} />
    </Box>
  );
}
