import { useEffect, useRef, useCallback } from "react";

type Handler = () => void;

interface KeyBinding {
  key: string;
  handler: Handler;
  context: string;
}

export interface UseKeybindingsResult {
  register: (key: string, handler: Handler, context?: string) => void;
  unregister: (key: string) => void;
  activeContext: string;
  setContext: (ctx: string) => void;
}

function normalizeKey(input: string, ctrl: boolean): string {
  if (ctrl) {
    return `ctrl+${input.toLowerCase()}`;
  }
  return input.toLowerCase();
}

export function useKeybindings(
  onInput?: (input: string, key: { ctrl: boolean; meta: boolean }) => void,
): UseKeybindingsResult {
  const bindingsRef = useRef<Map<string, KeyBinding>>(new Map());
  const contextRef = useRef<string>("default");
  const setContext = useCallback((ctx: string) => {
    contextRef.current = ctx;
  }, []);

  const register = useCallback(
    (key: string, handler: Handler, context: string = "default") => {
      bindingsRef.current.set(key, { key, handler, context });
    },
    [],
  );

  const unregister = useCallback((key: string) => {
    bindingsRef.current.delete(key);
  }, []);

  useEffect(() => {
    // Keybindings are consumed via the onInput callback passed from the parent.
    // The parent should call handleKey when it receives useInput events.
  }, []);

  return {
    register,
    unregister,
    activeContext: contextRef.current,
    setContext,
  };
}

export function dispatchKey(
  bindings: Map<string, KeyBinding>,
  activeContext: string,
  input: string,
  key: { ctrl: boolean; meta: boolean },
): boolean {
  const normalized = normalizeKey(input, key.ctrl);
  const binding = bindings.get(normalized);
  if (binding && binding.context === activeContext) {
    binding.handler();
    return true;
  }
  return false;
}
