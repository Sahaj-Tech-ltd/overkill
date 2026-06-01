import type { Command } from "./types.ts";

/** Context passed to command factories so they can close over app state. */
export interface CommandContext {
  open: (dialog: string) => void;
  close: () => void;
  exit: () => void;
  toggleSidebar: () => void;
  undoLastExchange: () => void;
  retryLastMessage: () => void;
  /** Backend for async operations like fork(). */
  backend: {
    fork: (sessionId: string, name: string) => Promise<unknown>;
    estop?: () => void;
    call?: (method: string, params?: unknown) => Promise<unknown>;
  };
  sessionId: string;
  /** Theme control for /theme command. */
  themeName?: string;
  setTheme?: (name: string) => void;
}

type CommandFactory = (ctx: CommandContext) => Command;

const registry = new Map<string, CommandFactory>();

export function registerCommand(factory: CommandFactory): void {
  // Call factory once to get the ID.
  const cmd = factory(null as unknown as CommandContext);
  registry.set(cmd.id, factory);
}

export function registerCommands(factories: CommandFactory[]): void {
  for (const f of factories) {
    registerCommand(f);
  }
}

export function getCommands(ctx: CommandContext): Command[] {
  const result: Command[] = [];
  for (const factory of registry.values()) {
    const cmd = factory(ctx);
    if (cmd.isHidden) continue;
    if (cmd.isEnabled && !cmd.isEnabled()) continue;
    result.push(cmd);
  }
  return result;
}

export function getCommand(
  id: string,
  ctx: CommandContext,
): Command | undefined {
  const factory = registry.get(id);
  if (!factory) return undefined;
  const cmd = factory(ctx);
  if (cmd.isHidden) return undefined;
  if (cmd.isEnabled && !cmd.isEnabled()) return undefined;
  return cmd;
}

export function clearCommands(): void {
  registry.clear();
}
