import {
  useEffect,
  useRef,
  useCallback,
  useState,
  createContext,
  useContext,
  type ReactNode,
} from "react";

// ─── Types ─────────────────────────────────────────────────────────────────

export interface MouseEvent {
  /** Button number: 0=left, 1=middle, 2=right */
  button: number;
  /** 1-based column */
  x: number;
  /** 1-based row */
  y: number;
  /** "press" | "release" | "move" */
  action: "press" | "release" | "move";
  /** Scroll direction if mouse wheel */
  scroll?: "up" | "down";
}

export type MouseHandler = (evt: MouseEvent) => void;

export interface ClickZone {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
  onClick?: () => void;
}

interface MouseContextValue {
  zones: React.MutableRefObject<ClickZone[]>;
  lastMouse: React.MutableRefObject<MouseEvent | null>;
}

const MouseContext = createContext<MouseContextValue>({
  zones: { current: [] },
  lastMouse: { current: null },
});

// ─── SGR Mouse Protocol Parser ─────────────────────────────────────────────

// SGR format: \x1b[<{button};{x};{y}{M|m}
// M = press, m = release
// Button 64 = scroll up, 65 = scroll down
function parseSGR(data: string): MouseEvent | null {
  // Match: ESC [ < button ; x ; y M/m
  const match = data.match(/\x1b\[<(\d+);(\d+);(\d+)([Mm])/);
  if (!match) return null;

  let button = parseInt(match[1]!, 10);
  const x = parseInt(match[2]!, 10);
  const y = parseInt(match[3]!, 10);
  const isRelease = match[4] === "m";

  // Scroll events
  if (button === 64) {
    return { button: 0, x, y, action: "press", scroll: "up" };
  }
  if (button === 65) {
    return { button: 0, x, y, action: "press", scroll: "down" };
  }

  // Button with motion tracking
  let action: "press" | "release" | "move" = isRelease ? "release" : "press";
  if (button >= 32) {
    // Motion tracking: button - 32 = actual button
    button -= 32;
    action = "move";
  }

  return { button, x, y, action };
}

// ─── ANSI Escape Sequences ──────────────────────────────────────────────────

const ENABLE_MOUSE = "\x1b[?1000h\x1b[?1002h\x1b[?1006h"; // track, button-events, SGR
const DISABLE_MOUSE = "\x1b[?1006l\x1b[?1002l\x1b[?1000l";

// ─── Provider ───────────────────────────────────────────────────────────────

interface MouseProviderProps {
  children: ReactNode;
  enabled?: boolean;
}

export function MouseProvider({
  children,
  enabled = true,
}: MouseProviderProps): React.JSX.Element {
  const zones = useRef<ClickZone[]>([]);
  const lastMouse = useRef<MouseEvent | null>(null);

  useEffect(() => {
    if (!enabled) return;

    // Enable mouse tracking
    process.stdout.write(ENABLE_MOUSE);

    // Raw stdin reading for mouse events
    const { stdin } = process;
    if (!stdin.isTTY) return;

    // Check if already raw (Ink may set it)
    try {
      stdin.setRawMode(true);
      stdin.resume();
    } catch {
      // Already raw or not supported
      return;
    }

    let buffer = "";

    const onData = (chunk: Buffer) => {
      buffer += chunk.toString();

      // Process complete SGR sequences
      while (buffer.length > 0) {
        // Look for SGR mouse sequence start
        const sgrStart = buffer.indexOf("\x1b[<");
        if (sgrStart === -1) {
          // No SGR start found, look for other ESC sequences to strip
          const escIdx = buffer.indexOf("\x1b");
          if (escIdx === -1 || escIdx === buffer.length - 1) {
            // Pass through regular input from stdin
            // Note: This is read-only for mouse events; keyboard input
            // is handled by Ink's useInput separately
            buffer = "";
            break;
          }
          // Skip this escape sequence
          if (buffer[escIdx + 1] === "[") {
            // CSI sequence, skip until we find a letter or we consumed it all
            const csiEnd = buffer.slice(escIdx).search(/[A-Za-z]/);
            if (csiEnd === -1) break; // incomplete
            buffer =
              buffer.slice(0, escIdx) + buffer.slice(escIdx + csiEnd + 1);
            continue;
          }
          buffer = buffer.slice(escIdx + 1);
          continue;
        }

        // We found a potential SGR sequence
        const endIdx = buffer.indexOf("m", sgrStart);
        const endIdxM = buffer.indexOf("M", sgrStart);
        const effectiveEnd =
          endIdx === -1
            ? endIdxM
            : endIdxM === -1
              ? endIdx
              : Math.min(endIdx, endIdxM);

        if (effectiveEnd === -1) break; // incomplete sequence

        const seq = buffer.slice(sgrStart, effectiveEnd + 1);
        const evt = parseSGR(seq);

        if (evt) {
          lastMouse.current = evt;

          // Hit-test click zones on press
          if (evt.action === "press" && evt.button === 0) {
            for (const zone of zones.current) {
              if (
                evt.x >= zone.x &&
                evt.x < zone.x + zone.width &&
                evt.y >= zone.y &&
                evt.y < zone.y + zone.height
              ) {
                zone.onClick?.();
                break; // first match wins
              }
            }
          }
        }

        buffer = buffer.slice(effectiveEnd + 1);
      }
    };

    stdin.on("data", onData);

    return () => {
      stdin.off("data", onData);
      process.stdout.write(DISABLE_MOUSE);
    };
  }, [enabled]);

  const ctxValue: MouseContextValue = { zones, lastMouse };

  return (
    <MouseContext.Provider value={ctxValue}>{children}</MouseContext.Provider>
  );
}

// ─── Hook: useClickZone ──────────────────────────────────────────────────────

export function useClickZone(
  id: string,
  x: number,
  y: number,
  width: number,
  height: number,
  onClick: () => void,
): void {
  const { zones } = useContext(MouseContext);

  useEffect(() => {
    const zone: ClickZone = { id, x, y, width, height, onClick };
    zones.current.push(zone);
    return () => {
      const idx = zones.current.findIndex((z) => z.id === id);
      if (idx >= 0) zones.current.splice(idx, 1);
    };
  }, [id, x, y, width, height, onClick]);
}

// ─── Hook: useMousePosition ──────────────────────────────────────────────────

export function useMousePosition(): MouseEvent | null {
  const { lastMouse } = useContext(MouseContext);
  const [pos, setPos] = useState<MouseEvent | null>(null);

  useEffect(() => {
    const interval = setInterval(() => {
      const current = lastMouse.current;
      if (
        current &&
        (pos === null || current.x !== pos.x || current.y !== pos.y)
      ) {
        setPos(current);
      }
    }, 50);
    return () => clearInterval(interval);
  }, [pos]);

  return pos;
}

// ─── Scroll Provider ─────────────────────────────────────────────────────────

export type ScrollHandler = (direction: "up" | "down") => void;

interface ScrollContextValue {
  registerScrollHandler: (handler: ScrollHandler) => () => void;
}

const ScrollContext = createContext<ScrollContextValue>({
  registerScrollHandler: () => () => {},
});

export function ScrollHandlerProvider({
  children,
}: {
  children: ReactNode;
}): React.JSX.Element {
  const handlers = useRef<ScrollHandler[]>([]);
  const { lastMouse } = useContext(MouseContext);

  const registerScrollHandler = useCallback((handler: ScrollHandler) => {
    handlers.current.push(handler);
    return () => {
      const idx = handlers.current.indexOf(handler);
      if (idx >= 0) handlers.current.splice(idx, 1);
    };
  }, []);

  // Poll for scroll events
  useEffect(() => {
    let prevEvt: MouseEvent | null = null;
    const interval = setInterval(() => {
      const evt = lastMouse.current;
      if (evt && evt.scroll && evt !== prevEvt) {
        prevEvt = evt;
        for (const h of handlers.current) {
          h(evt.scroll);
        }
      }
      if (!evt) prevEvt = null;
    }, 50);
    return () => clearInterval(interval);
  }, []);

  return (
    <ScrollContext.Provider value={{ registerScrollHandler }}>
      {children}
    </ScrollContext.Provider>
  );
}

export function useScrollHandler(handler: ScrollHandler): void {
  const { registerScrollHandler } = useContext(ScrollContext);
  useEffect(() => {
    return registerScrollHandler(handler);
  }, [handler, registerScrollHandler]);
}
