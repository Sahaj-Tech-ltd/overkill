import { useState, useCallback, useRef } from "react";
import type { Key } from "ink";

export interface SearchInputOptions {
  isActive: boolean;
  onExit?: () => void;
  onCancel?: () => void;
  onExitUp?: () => void;
  passthroughCtrlKeys?: string[];
  initialQuery?: string;
  backspaceExitsOnEmpty?: boolean;
}

export interface SearchInputResult {
  query: string;
  setQuery: (q: string) => void;
  cursorOffset: number;
  handleKeyDown: (
    input: string,
    key: Key,
  ) => { handled: boolean; shouldExit: boolean } | void;
}

export function useSearchInput(options: SearchInputOptions): SearchInputResult {
  const {
    isActive,
    onExit,
    onCancel,
    onExitUp,
    passthroughCtrlKeys = [],
    initialQuery = "",
    backspaceExitsOnEmpty = false,
  } = options;

  const [query, setQueryState] = useState(initialQuery);
  const [cursorOffset, setCursorOffset] = useState(initialQuery.length);
  const killRingRef = useRef<string[]>([]);
  const killRingIdxRef = useRef(0);
  const lastKillRef = useRef("");
  const yankActiveRef = useRef(false);

  const setQuery = useCallback((val: string) => {
    setQueryState(val);
    setCursorOffset((prev) => Math.min(prev, val.length));
    yankActiveRef.current = false;
  }, []);

  const handleKeyDown = useCallback(
    (
      input: string,
      key: Key,
    ): { handled: boolean; shouldExit: boolean } | void => {
      if (!isActive) return;

      // Ctrl+D at empty = exit
      if (key.ctrl && input === "d") {
        if (query.length === 0) {
          onCancel?.();
          return { handled: true, shouldExit: true };
        }
        // Delete forward
        if (cursorOffset < query.length) {
          const newQuery =
            query.slice(0, cursorOffset) + query.slice(cursorOffset + 1);
          setQueryState(newQuery);
          return { handled: true, shouldExit: false };
        }
        return { handled: true, shouldExit: false };
      }

      // Ctrl+A = start of line
      if (key.ctrl && input === "a") {
        setCursorOffset(0);
        return { handled: true, shouldExit: false };
      }

      // Ctrl+E = end of line
      if (key.ctrl && input === "e") {
        setCursorOffset(query.length);
        return { handled: true, shouldExit: false };
      }

      // Ctrl+K = kill to end
      if (key.ctrl && input === "k") {
        const killed = query.slice(cursorOffset);
        if (killed.length > 0) {
          killRingRef.current.unshift(killed);
          if (killRingRef.current.length > 10) killRingRef.current.pop();
          killRingIdxRef.current = 0;
          lastKillRef.current = killed;
          yankActiveRef.current = false;
        }
        setQueryState(query.slice(0, cursorOffset));
        return { handled: true, shouldExit: false };
      }

      // Ctrl+U = kill to start
      if (key.ctrl && input === "u") {
        const killed = query.slice(0, cursorOffset);
        if (killed.length > 0) {
          killRingRef.current.unshift(killed);
          if (killRingRef.current.length > 10) killRingRef.current.pop();
          killRingIdxRef.current = 0;
          lastKillRef.current = killed;
          yankActiveRef.current = false;
        }
        const remaining = query.slice(cursorOffset);
        setQueryState(remaining);
        setCursorOffset(0);
        return { handled: true, shouldExit: false };
      }

      // Ctrl+W = kill word before cursor
      if (key.ctrl && input === "w") {
        const before = query.slice(0, cursorOffset);
        const match = before.match(/(\s*\S+)$/);
        if (match && match.index !== undefined) {
          const start = match.index;
          const killed = before.slice(start);
          if (killed.length > 0) {
            killRingRef.current.unshift(killed);
            if (killRingRef.current.length > 10) killRingRef.current.pop();
            killRingIdxRef.current = 0;
            lastKillRef.current = killed;
            yankActiveRef.current = false;
          }
          const newQuery = before.slice(0, start) + query.slice(cursorOffset);
          setQueryState(newQuery);
          setCursorOffset(start);
        }
        return { handled: true, shouldExit: false };
      }

      // Ctrl+Y = yank
      if (key.ctrl && input === "y") {
        const toYank = killRingRef.current[killRingIdxRef.current] ?? "";
        if (toYank) {
          const newQuery =
            query.slice(0, cursorOffset) + toYank + query.slice(cursorOffset);
          setQueryState(newQuery);
          setCursorOffset(cursorOffset + toYank.length);
          yankActiveRef.current = true;
        }
        return { handled: true, shouldExit: false };
      }

      // Meta+Y = yank-pop
      if (key.meta && input === "y" && yankActiveRef.current) {
        const currentYank = killRingRef.current[killRingIdxRef.current] ?? "";
        killRingIdxRef.current =
          (killRingIdxRef.current + 1) % killRingRef.current.length;
        const nextYank =
          killRingRef.current[killRingIdxRef.current] ?? currentYank;
        // Replace the last yanked text
        const beforeYank = cursorOffset - currentYank.length;
        if (beforeYank >= 0) {
          const newQuery =
            query.slice(0, beforeYank) + nextYank + query.slice(cursorOffset);
          setQueryState(newQuery);
          setCursorOffset(beforeYank + nextYank.length);
        }
        return { handled: true, shouldExit: false };
      }

      // Escape
      if (key.escape) {
        onCancel?.();
        return { handled: true, shouldExit: true };
      }

      // Return/Enter
      if (key.return) {
        return { handled: true, shouldExit: false };
      }

      // Backspace
      if (key.backspace || (key.delete && !key.ctrl)) {
        if (cursorOffset > 0) {
          const newQuery =
            query.slice(0, cursorOffset - 1) + query.slice(cursorOffset);
          setQueryState(newQuery);
          setCursorOffset(cursorOffset - 1);
        } else if (backspaceExitsOnEmpty && query.length === 0) {
          onCancel?.();
          return { handled: true, shouldExit: true };
        }
        yankActiveRef.current = false;
        return { handled: true, shouldExit: false };
      }

      // Left arrow
      if (key.leftArrow) {
        if (key.ctrl) {
          // Word left
          const before = query.slice(0, cursorOffset);
          const match = before.match(/(.*\s)?(\S+)$/);
          if (match && match.index !== undefined) {
            setCursorOffset(match.index + (match[1]?.length ?? 0));
          } else {
            setCursorOffset(0);
          }
        } else if (cursorOffset > 0) {
          setCursorOffset(cursorOffset - 1);
        }
        return { handled: true, shouldExit: false };
      }

      // Right arrow
      if (key.rightArrow) {
        if (key.ctrl) {
          // Word right
          const after = query.slice(cursorOffset);
          const match = after.match(/^(\S+\s*)?/);
          const len = match?.[0]?.length ?? 0;
          setCursorOffset(Math.min(query.length, cursorOffset + len));
        } else if (cursorOffset < query.length) {
          setCursorOffset(cursorOffset + 1);
        }
        return { handled: true, shouldExit: false };
      }

      // Up arrow - could exit to list
      if (key.upArrow) {
        onExitUp?.();
        return { handled: true, shouldExit: false };
      }

      // Passthrough Ctrl+ keys
      if (key.ctrl && passthroughCtrlKeys.includes(input)) {
        return { handled: false, shouldExit: false };
      }

      // Printable characters
      if (input.length === 1 && input >= " ") {
        const newQuery =
          query.slice(0, cursorOffset) + input + query.slice(cursorOffset);
        setQueryState(newQuery);
        setCursorOffset(cursorOffset + 1);
        return { handled: true, shouldExit: false };
      }

      return { handled: false, shouldExit: false };
    },
    [
      isActive,
      query,
      cursorOffset,
      onCancel,
      onExit,
      onExitUp,
      passthroughCtrlKeys,
      backspaceExitsOnEmpty,
    ],
  );

  return { query, setQuery, cursorOffset, handleKeyDown };
}
