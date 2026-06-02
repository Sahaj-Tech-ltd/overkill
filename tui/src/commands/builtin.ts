import type { Command } from "./types.ts";
import type { CommandContext } from "./registry.ts";
import { log } from "../logger.ts";

/**
 * All built-in Overkill commands.
 * Each function receives the app context so closures capture current state.
 */
export function allCommands(ctx: CommandContext): Command[] {
  return [
    {
      id: "dashboard",
      title: "Dashboard",
      description: "Show live dashboard (goal, plan, budget)",
      keybind: "Ctrl+G",
      action: () => ctx.open("dashboard"),
    },
    {
      id: "switch-model",
      title: "Switch Model",
      description: "Change the active AI model",
      action: () => ctx.open("model-switcher"),
    },
    {
      id: "new-session",
      title: "New Session",
      description: "Create a new chat session",
      action: () => ctx.open("session-manager"),
    },
    {
      id: "switch-session",
      title: "Switch Session",
      description: "Switch to a different session",
      action: () => ctx.open("session-manager"),
    },
    {
      id: "settings",
      title: "Settings",
      description: "Open settings panel",
      keybind: "Ctrl+,",
      action: () => ctx.open("settings"),
    },
    {
      id: "help",
      title: "Keyboard Shortcuts",
      description: "Show all keyboard shortcuts",
      keybind: "Ctrl+?",
      action: () => ctx.open("help"),
    },
    {
      id: "quit",
      title: "Quit",
      description: "Exit Overkill",
      keybind: "Ctrl+C",
      action: () => ctx.exit(),
    },
    {
      id: "estop",
      title: "Emergency Stop",
      description: "Halt all running agent loops immediately",
      action: () => {
        ctx.backend.estop?.();
        ctx.exit();
      },
    },
    {
      id: "steer",
      title: "Steer Agent...",
      description: "Inject guidance into a running agent mid-task",
      action: () => ctx.open("steer"),
    },
    {
      id: "fork",
      title: "Fork Session",
      description: "Create a branch from this session to explore alternatives",
      action: () => {
        const forkName = `forked-${Date.now()}`;
        ctx.backend.fork(ctx.sessionId, forkName).catch((err: unknown) => {
          log.error("Fork failed:", err);
        });
      },
    },
    {
      id: "undo",
      title: "Undo Last Exchange",
      description: "Remove the last agent response and your message",
      action: () => ctx.undoLastExchange(),
    },
    {
      id: "retry",
      title: "Retry Last Message",
      description: "Resend your last message to the agent",
      action: () => ctx.retryLastMessage(),
    },
    {
      id: "theme-dark",
      title: "Theme: Dark",
      description: "Switch to dark theme (Catppuccin-inspired)",
      action: () => {
        ctx.setTheme?.("dark");
        ctx.backend
          .call?.("config.theme", { theme: "dark" })
          .catch((err: unknown) => {
            log.error("config.theme (dark) failed:", err);
          });
      },
    },
    {
      id: "theme-light",
      title: "Theme: Light",
      description: "Switch to light theme",
      action: () => {
        ctx.setTheme?.("light");
        ctx.backend
          .call?.("config.theme", { theme: "light" })
          .catch((err: unknown) => {
            log.error("config.theme (light) failed:", err);
          });
      },
    },
    {
      id: "theme-cyberpunk",
      title: "Theme: Cyberpunk",
      description: "Switch to cyberpunk theme (green/purple)",
      action: () => {
        ctx.setTheme?.("cyberpunk");
        ctx.backend
          .call?.("config.theme", { theme: "cyberpunk" })
          .catch((err: unknown) => {
            log.error("config.theme (cyberpunk) failed:", err);
          });
      },
    },
    {
      id: "theme-ocean",
      title: "Theme: Ocean",
      description: "Switch to ocean theme (blue/cyan)",
      action: () => {
        ctx.setTheme?.("ocean");
        ctx.backend
          .call?.("config.theme", { theme: "ocean" })
          .catch((err: unknown) => {
            log.error("config.theme (ocean) failed:", err);
          });
      },
    },
  ];
}
