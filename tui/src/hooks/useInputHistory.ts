import { useState, useRef, useCallback } from "react";

const MAX_HISTORY = 100;

/**
 * Input history hook for up/down arrow message cycling.
 *
 * Maintains a ring buffer of previously submitted messages and exposes
 * keyboard handlers for navigating through them. The hook owns the input
 * value state — callers don't need their own useState for the value.
 *
 * Cursor semantics:
 *   -1 = editing a new (or restored) input
 *    0 = latest history entry
 *    1 = second latest
 *    n = (n+1)th from latest
 */
export function useInputHistory(): {
  value: string;
  setValue: (v: string) => void;
  handleKeyDown: (key: { upArrow?: boolean; downArrow?: boolean }) => boolean;
  recordSubmit: (text: string) => void;
} {
  const [value, setValue] = useState("");
  const historyRef = useRef<string[]>([]);
  const cursorRef = useRef(-1);
  const savedDraftRef = useRef("");

  const handleKeyDown = useCallback(
    (key: { upArrow?: boolean; downArrow?: boolean }): boolean => {
      const h = historyRef.current;

      if (key.upArrow) {
        if (h.length === 0) return false;

        if (cursorRef.current === -1) {
          // First up-arrow press: save current draft and enter history
          savedDraftRef.current = value;
          cursorRef.current = 0;
          setValue(h[h.length - 1]);
          return true;
        }

        if (cursorRef.current < h.length - 1) {
          cursorRef.current++;
          setValue(h[h.length - 1 - cursorRef.current]);
          return true;
        }

        // Already at the oldest entry
        return false;
      }

      if (key.downArrow) {
        if (cursorRef.current === -1) {
          // Not navigating history — nothing to do
          return false;
        }

        cursorRef.current--;

        if (cursorRef.current === -1) {
          // Exited history — restore the original draft
          setValue(savedDraftRef.current);
          return true;
        }

        setValue(h[h.length - 1 - cursorRef.current]);
        return true;
      }

      return false;
    },
    [value],
  );

  const recordSubmit = useCallback((text: string) => {
    if (text.trim().length === 0) return;
    const h = historyRef.current;

    // Don't push consecutive identical entries
    if (h.length === 0 || h[h.length - 1] !== text) {
      h.push(text);
      if (h.length > MAX_HISTORY) h.shift();
    }

    cursorRef.current = -1;
    savedDraftRef.current = "";
    setValue("");
  }, []);

  return {
    value,
    setValue,
    handleKeyDown,
    recordSubmit,
  };
}
