import React, {
  createContext,
  useContext,
  useCallback,
  useMemo,
  useState,
  useEffect,
  useRef,
  type ReactNode,
} from "react";
import { useInput, type Key } from "ink";

// ─── Types ─────────────────────────────────────────────────────────────────

export type KeyCombo =
  | { key: string; ctrl?: boolean; shift?: boolean; meta?: boolean }
  | { special: SpecialKey; ctrl?: boolean; shift?: boolean; meta?: boolean };

export type SpecialKey =
  | "enter"
  | "escape"
  | "tab"
  | "backspace"
  | "delete"
  | "up"
  | "down"
  | "left"
  | "right"
  | "pageup"
  | "pagedown"
  | "home"
  | "end"
  | "space";

interface ActiveBinding {
  context: string;
  isActiveRef: React.MutableRefObject<boolean>;
  getBindings: () => Record<string, () => void>;
}

interface KeybindingContextValue {
  getShortcut: (action: string) => KeyCombo | undefined;
  shortcuts: Record<string, KeyCombo>;
}

// ─── Matchers ──────────────────────────────────────────────────────────────

function matchKey(input: string, key: Key, combo: KeyCombo): boolean {
  const ctrl = combo.ctrl ?? false;
  const shift = combo.shift ?? false;
  const meta = combo.meta ?? false;
  if (key.ctrl !== ctrl || key.shift !== shift || key.meta !== meta)
    return false;

  if ("special" in combo) {
    switch (combo.special) {
      case "enter":
        return key.return;
      case "escape":
        return key.escape;
      case "tab":
        return key.tab;
      case "backspace":
        return key.backspace;
      case "delete":
        return key.delete;
      case "up":
        return key.upArrow;
      case "down":
        return key.downArrow;
      case "left":
        return key.leftArrow;
      case "right":
        return key.rightArrow;
      case "pageup":
        return key.pageUp;
      case "pagedown":
        return key.pageDown;
      case "home":
        return key.home;
      case "end":
        return key.end;
      case "space":
        return input === " ";
    }
    return false;
  }
  return input === combo.key;
}

export function comboToString(combo: KeyCombo): string {
  const parts: string[] = [];
  if (combo.ctrl) parts.push("Ctrl");
  if (combo.shift) parts.push("Shift");
  if (combo.meta) parts.push("Meta");
  if ("special" in combo) {
    parts.push(combo.special.charAt(0).toUpperCase() + combo.special.slice(1));
  } else {
    parts.push(combo.key === " " ? "Space" : combo.key);
  }
  return parts.join("+");
}

// ─── Default bindings ──────────────────────────────────────────────────────

export const DefaultBindings: Record<string, KeyCombo> = {
  "global:quit": { key: "c", ctrl: true },
  "global:commandPalette": { key: "k", ctrl: true },
  "global:toggleSidebar": { key: "b", ctrl: true },
  "global:dashboard": { key: "g", ctrl: true },
  "global:settings": { key: ",", ctrl: true },

  "confirm:no": { special: "escape" },
  "confirm:yes": { special: "enter" },

  "select:accept": { key: " " },
  "select:next": { special: "down" },
  "select:previous": { special: "up" },
  "select:toggle": { special: "enter" },

  "tabs:next": { special: "right" },
  "tabs:previous": { special: "left" },
  "tabs:focusContent": { special: "down" },

  "search:open": { key: "/" },
  "search:exit": { special: "escape" },

  "global:cycleThinking": { special: "tab" },
  "global:toggleMode": { special: "tab", shift: true },

  "chat:send": { special: "enter" },
  "chat:scrollUp": { special: "pageup" },
  "chat:scrollDown": { special: "pagedown" },
};

// ─── Module-level registry ─────────────────────────────────────────────────

const registry: ActiveBinding[] = [];

// ─── Context ───────────────────────────────────────────────────────────────

const KeybindingContext = createContext<KeybindingContextValue>({
  getShortcut: () => undefined,
  shortcuts: {},
});

export function useKeybindingContext(): KeybindingContextValue {
  return useContext(KeybindingContext);
}

// ─── Provider ──────────────────────────────────────────────────────────────

interface KeybindingProviderProps {
  children: ReactNode;
  shortcuts?: Record<string, KeyCombo>;
}

export function KeybindingProvider({
  children,
  shortcuts: userShortcuts = {},
}: KeybindingProviderProps): React.JSX.Element {
  const [shortcuts] = useState(userShortcuts);

  const getShortcut = useCallback(
    (action: string): KeyCombo | undefined => {
      return shortcuts[action] ?? DefaultBindings[action];
    },
    [shortcuts],
  );

  const ctxValue = useMemo<KeybindingContextValue>(
    () => ({ getShortcut, shortcuts }),
    [getShortcut, shortcuts],
  );

  // Global input handler: active contexts (last registered = highest priority).
  useInput((input, key) => {
    for (let i = registry.length - 1; i >= 0; i--) {
      const ctx = registry[i]!;
      if (!ctx.isActiveRef.current) continue;
      for (const [action, handler] of Object.entries(ctx.getBindings())) {
        const combo = shortcuts[action] ?? DefaultBindings[action];
        if (!combo) continue;
        if (matchKey(input, key, combo)) {
          handler();
          return; // first active match wins, no propagation
        }
      }
    }
  });

  return React.createElement(
    KeybindingContext.Provider,
    { value: ctxValue },
    children,
  );
}

// ─── Hook ──────────────────────────────────────────────────────────────────

interface UseKeybindingsOptions {
  context: string;
  isActive: boolean;
}

/**
 * Register keybindings that are active when `isActive` is true.
 * Bindings are automatically unregistered on unmount or when
 * dependencies change. Last-registered contexts have highest priority.
 *
 * Each key in `bindings` is an action name (e.g. "confirm:no").
 * The corresponding value is a handler function.
 */
export function useKeybindings(
  bindings: Record<string, () => void>,
  options: UseKeybindingsOptions,
): void {
  const { context, isActive } = options;
  const isActiveRef = useRef(isActive);
  isActiveRef.current = isActive;

  // Stable ref so registry doesn't re-register on every render.
  // The input handler reads bindings via getBindings() which always
  // returns the latest bindings from the ref.
  const bindingsRef = useRef(bindings);
  bindingsRef.current = bindings;

  useEffect(() => {
    const entry: ActiveBinding = {
      context,
      isActiveRef,
      getBindings: () => bindingsRef.current,
    };
    registry.push(entry);
    return () => {
      const idx = registry.indexOf(entry);
      if (idx >= 0) registry.splice(idx, 1);
    };
  }, [context]);
}

// ─── Shortcut display ──────────────────────────────────────────────────────

/**
 * Returns a human-readable string for an action's current keybinding.
 * Uses user overrides from the provider, falling back to defaults.
 */
export function useShortcutDisplay(action: string, fallback?: string): string {
  const { getShortcut } = useKeybindingContext();
  const combo = getShortcut(action);
  if (!combo) return fallback ?? action;
  return comboToString(combo);
}
