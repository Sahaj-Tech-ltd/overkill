import React, { useMemo, useState, useEffect } from "react";
import { Box, Text, useApp } from "ink";
import { execSync } from "node:child_process";
import { useBackend } from "./hooks/use-backend.ts";
import { useChat } from "./hooks/use-chat.ts";
import { useDialogs } from "./hooks/use-dialogs.ts";
import { useClarifyPoll } from "./hooks/use-clarify-poll.ts";
import { useSidebar } from "./hooks/use-sidebar.ts";
import { useTheme } from "./hooks/use-theme.ts";
import { useToast } from "./hooks/use-toast.ts";
import { StatusBar } from "./components/status-bar.tsx";
import { ChatView } from "./components/chat/chat-view.tsx";
import { CommandPalette } from "./components/dialogs/command-palette.tsx";
import { ModelSwitcher } from "./components/dialogs/model-switcher.tsx";
import { SessionManager } from "./components/dialogs/session-manager.tsx";
import { HelpDialog } from "./components/dialogs/help-dialog.tsx";
import { ClarifyDialog } from "./components/dialogs/clarify-dialog.tsx";
import { ToastContainer } from "./components/toast.tsx";
import { Sidebar } from "./components/sidebar/sidebar.tsx";
import { SteerDialog } from "./components/dialogs/steer-dialog.tsx";
import { SettingsPanel } from "./components/settings/SettingsPanel.tsx";
import { DashboardCard } from "./components/dashboard/DashboardCard.tsx";
import { SessionPanel } from "./components/sidebar/session-panel.tsx";
import { SubagentPanel } from "./components/sidebar/subagent-panel.tsx";
import { SelfEvalPanel } from "./components/sidebar/self-eval-panel.tsx";
import { TestPanel } from "./components/sidebar/test-panel.tsx";
import { WizardPanel } from "./components/sidebar/wizard-panel.tsx";
import { QueuePanel } from "./components/sidebar/queue-panel.tsx";
import { TodoPanel } from "./components/sidebar/todo-panel.tsx";
import { SkillsPanel } from "./components/sidebar/skills-panel.tsx";
import { Wizard } from "./components/onboarding/wizard.tsx";
import {
  KeybindingProvider,
  useKeybindings,
} from "./context/KeybindingContext.tsx";
import { allCommands } from "./commands/builtin.ts";
import type { CommandContext } from "./commands/registry.ts";
import type { ModelInfo, SessionInfo, FileChange } from "./backend/types.ts";

function useGitBranch(): string | undefined {
  const [branch, setBranch] = useState<string | undefined>();

  useEffect(() => {
    try {
      const result = execSync("git rev-parse --abbrev-ref HEAD", {
        encoding: "utf-8",
        stdio: ["pipe", "pipe", "pipe"],
      });
      setBranch(result.trim() || undefined);
    } catch {
      // Silently fail — not in a git repo
    }
  }, []);

  return branch;
}

function useConfigExists(backend: ReturnType<typeof useBackend>["backend"]): {
  exists: boolean | null;
  loading: boolean;
} {
  const [exists, setExists] = useState<boolean | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    backend
      .call<{ exists: boolean }>("config.exists")
      .then((result) => {
        if (!cancelled) {
          setExists(result.exists);
        }
      })
      .catch((err) => {
        console.error("config.exists check failed:", err);
        if (!cancelled) {
          setExists(true);
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [backend]);

  return { exists, loading };
}

export function App(): React.JSX.Element {
  const { backend, connected } = useBackend();
  const { exists: configExists, loading: configLoading } =
    useConfigExists(backend);
  const [onboardingComplete, setOnboardingComplete] = useState(false);
  const [thinkingLevel, setThinkingLevel] = useState<string>("off");
  const [agentMode, setAgentMode] = useState<"plan" | "build">("build");
  const { show: showToast } = useToast();
  const {
    messages,
    sendMessage,
    clearChat,
    undoLastExchange,
    retryLastMessage,
    isLoading,
    model,
    provider,
    streamingText,
    queuedMessages,
    statusPhase,
    sessionId,
    lastUserMessage,
    thinkingElapsed,
    turnDuration,
    totalSessionTime,
    fileChanges,
    scrollOffset,
    setScrollOffset,
  } = useChat(backend);
  const { openDialog, open, close, clarifyRequest, clarifyOpen, clarifyCallback, dismissClarify, showClarify } = useDialogs();
  useClarifyPoll(backend, showClarify, clarifyOpen);
  const { visible: sidebarVisible, activeTab, toggle, setTab } = useSidebar();
  const { exit } = useApp();
  const { theme, themeName, setTheme } = useTheme();
  const { toasts } = useToast();
  const gitBranch = useGitBranch();

  const handleModelSelect = (provider: string, model: ModelInfo) => {
    backend
      .call("models.select", { provider, model: model.id })
      .then(() => {
        showToast(`Model: ${model.name}`, "success");
      })
      .catch((err: unknown) => {
        showToast(`Failed: ${(err as Error).message}`, "error");
      });
  };

  const handleSessionSelect = (session: SessionInfo) => {
    backend
      .call("session.load", { id: session.id })
      .then(() => {
        showToast(`Session: ${session.name || session.folder}`, "success");
      })
      .catch((err: unknown) => {
        showToast(`Failed: ${(err as Error).message}`, "error");
      });
  };

  const handleOnboardingComplete = () => {
    setOnboardingComplete(true);
  };

  // Build the command list from the registry, capturing current app state.
  const commandCtx: CommandContext = {
    open,
    close,
    exit,
    toggleSidebar: toggle,
    undoLastExchange,
    retryLastMessage,
    backend,
    sessionId,
    themeName,
    setTheme,
  };
  const commands = useMemo(() => allCommands(commandCtx), [commandCtx]);

  const isDialogOpen = openDialog !== null;

  useKeybindings(
    {
      "global:commandPalette": () => {
        if (!isDialogOpen) open("command-palette");
      },
      "global:settings": () => {
        if (isDialogOpen) close();
        else open("settings");
      },
      "global:toggleSidebar": () => toggle(),
      "global:dashboard": () => {
        if (isDialogOpen && openDialog !== "dashboard") close();
        else if (openDialog === "dashboard") close();
        else open("dashboard");
      },
      "global:quit": () => exit(),
      "global:cycleThinking": () => {
        const levels = ["off", "minimal", "low", "medium", "high", "x-high"];
        const idx = levels.indexOf(thinkingLevel);
        const next = levels[(idx + 1) % levels.length]!;
        setThinkingLevel(next);
        backend.call("thinking.set_level", { level: next }).catch((err: unknown) => { console.error("thinking.set_level failed:", err); });
        showToast(`Thinking: ${next}`, "info");
      },
      "global:toggleMode": () => {
        const next = agentMode === "plan" ? "build" : "plan";
        setAgentMode(next);
        backend.call("mode.set", { mode: next }).catch((err: unknown) => { console.error("mode.set failed:", err); });
        showToast(`Mode: ${next}`, next === "plan" ? "warning" : "success");
      },
    },
    { context: "App", isActive: true },
  );

  // Show loading while checking config
  if (configLoading) {
    return (
      <Box
        flexDirection="column"
        width="100%"
        height="100%"
        alignItems="center"
        justifyContent="center"
      >
        <Text color="yellow">Checking configuration...</Text>
      </Box>
    );
  }

  // Show onboarding wizard if no config exists
  if (!configExists && !onboardingComplete) {
    return (
      <Box flexDirection="column" width="100%" height="100%">
        <Wizard backend={backend} onComplete={handleOnboardingComplete} />
      </Box>
    );
  }

  return (
    <KeybindingProvider>
    <Box flexDirection="column" width="100%" height="100%">
      {/* Main area: Chat + Sidebar */}
      <Box flexDirection="row" flexGrow={1} width="100%" overflow="hidden">
        <ChatView
          messages={messages}
          sendMessage={sendMessage}
          clearChat={clearChat}
          isLoading={isLoading}
          streamingText={streamingText}
          model={model}
          provider={provider}
          onOpenPalette={() => open("command-palette")}
          isDialogOpen={isDialogOpen}
          userMessage={lastUserMessage}
          statusPhase={statusPhase}
          thinkingElapsed={thinkingElapsed}
          theme={theme}
          scrollOffset={scrollOffset}
          onScrollChange={setScrollOffset}
          fileChanges={fileChanges}
        />
        <Sidebar
          title={sessionId || "Overkill"}
          directory={process.cwd()}
          branch={gitBranch}
          files={fileChanges.map((fc: {path: string; added: number; removed: number}) => ({
            file: fc.path.split("/").pop() ?? fc.path,
            additions: fc.added,
            deletions: fc.removed,
          }))}
          version="v3"
        >
          <TodoPanel active={sidebarVisible} />
          <SkillsPanel active={sidebarVisible} />
        </Sidebar>
      </Box>

      {/* Bottom status bar — matches OpenCode footer */}
      <StatusBar
        directory={process.cwd()}
        branch={gitBranch}
        connected={connected}
        theme={theme}
      />
    </Box>

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
      <SteerDialog
        open={openDialog === "steer"}
        onClose={close}
        onSubmit={(msg) => {
          backend.steer(sessionId, msg).catch((err: unknown) => { console.error("steer failed:", err); });
        }}
      />
      <SettingsPanel
        open={openDialog === "settings"}
        onClose={close}
        backend={backend}
      />
      <DashboardCard open={openDialog === "dashboard"} onClose={close} />
      <ClarifyDialog
        open={clarifyOpen}
        request={clarifyRequest}
        onAnswer={(answer, index) => {
          if (clarifyCallback) {
            clarifyCallback(answer, index);
          }
          dismissClarify();
        }}
        onCancel={() => {
          // Send a cancel signal to unblock the agent.
          if (clarifyCallback) {
            clarifyCallback("", -1);
          }
          dismissClarify();
          // Also send the cancel via RPC so the agent doesn't wait for timeout.
          backend.call("agent.answer", {
            session_id: "",
            text: "",
            index: -1,
          }).catch((err: unknown) => { console.error("agent.answer cancel failed:", err); });
        }}
      />
    </KeybindingProvider>
  );
}

// test
