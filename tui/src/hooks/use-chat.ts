import { useState, useCallback, useRef, useEffect } from "react";
import type { BackendClient } from "../backend/client.ts";
import type { Message, StreamEvent, FileChange } from "../backend/types.ts";

export interface UseChatResult {
  messages: Message[];
  sendMessage: (text: string) => Promise<void>;
  clearChat: () => void;
  undoLastExchange: () => void;
  retryLastMessage: () => void;
  isLoading: boolean;
  error: string | null;
  model?: string;
  provider?: string;
  streamingText?: string;
  reasoning?: string;
  queuedMessages: number;
  statusPhase?: string;
  sessionId: string;
  /** The user's last message (for Memo banner context) */
  lastUserMessage?: string;
  /** Seconds elapsed since isLoading became true */
  thinkingElapsed: number;
  /** Duration of the last completed turn in ms */
  turnDuration?: number;
  /** Cumulative time of all completed turns in ms */
  totalSessionTime: number;
  /** Files changed during the current/last turn */
  fileChanges: FileChange[];
  /** Virtual scroll offset (0 = bottom, >0 = scrolled up) */
  scrollOffset: number;
  /** Set scroll offset for virtual scrolling */
  setScrollOffset: (offset: number) => void;
}

let _nextSessionId = 0;
let _nextMsgId = 0;

function newMsgId(): string {
  return `msg-${Date.now()}-${++_nextMsgId}`;
}

/** Known file-modifying tool names to track for the file change bar */
const FILE_TOOL_NAMES = new Set([
  "write_file", "write", "create_file", "file_write",
  "patch", "edit_file", "replace", "sed",
  "file_edit", "update_file",
]);

/** Try to extract a file path from tool input (JSON or plain string) */
function tryExtractPath(input: unknown): string | undefined {
  if (typeof input === "string") {
    try {
      const parsed = JSON.parse(input);
      return tryExtractPath(parsed);
    } catch {
      // Not JSON, check if it looks like a path
      if (input.startsWith("/") || input.startsWith("./") || input.startsWith("~/")) {
        return input;
      }
      return undefined;
    }
  }
  if (input && typeof input === "object" && !Array.isArray(input)) {
    const obj = input as Record<string, unknown>;
    if (typeof obj.path === "string") return obj.path;
    if (typeof obj.file === "string") return obj.file;
    if (typeof obj.filePath === "string") return obj.filePath;
    if (typeof obj.file_path === "string") return obj.file_path;
    if (typeof obj.filename === "string") return obj.filename;
  }
  return undefined;
}

/** Estimate line delta from tool output */
function estimateDelta(
  output: string | undefined,
  input: unknown,
): { added: number; removed: number } {
  let added = 0;
  let removed = 0;

  if (output) {
    try {
      const parsed = JSON.parse(output);
      if (parsed && typeof parsed === "object") {
        if (typeof parsed.lines_added === "number") added = parsed.lines_added;
        if (typeof parsed.lines_removed === "number") removed = parsed.lines_removed;
        if (typeof parsed.added === "number") added = parsed.added;
        if (typeof parsed.removed === "number") removed = parsed.removed;
        if (typeof parsed.additions === "number") added = parsed.additions;
        if (typeof parsed.deletions === "number") removed = parsed.deletions;
      }
    } catch {
      // Not JSON — count output lines as rough estimate
      added = output.split("\n").length;
    }
  }

  // If we couldn't get deltas from output, try the input content
  if (added === 0 && removed === 0 && input && typeof input === "object" && !Array.isArray(input)) {
    const obj = input as Record<string, unknown>;
    if (typeof obj.content === "string") {
      added = obj.content.split("\n").length;
    }
    if (typeof obj.new_string === "string" && typeof obj.old_string === "string") {
      added = obj.new_string.split("\n").length;
      removed = obj.old_string.split("\n").length;
    }
  }

  return { added, removed };
}

export function useChat(backend: BackendClient): UseChatResult {
  const [messages, setMessages] = useState<Message[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [model, setModel] = useState<string | undefined>();
  const [provider, setProvider] = useState<string | undefined>();
  const [streamingText, setStreamingText] = useState<string | undefined>();
  const [reasoning, setReasoning] = useState<string | undefined>();
  const [queuedMessages, setQueuedMessages] = useState(0);
  const [statusPhase, setStatusPhase] = useState<string | undefined>();
  const [lastUserMessage, setLastUserMessage] = useState<string | undefined>();
  const [thinkingElapsed, setThinkingElapsed] = useState(0);
  const [turnDuration, setTurnDuration] = useState<number | undefined>();
  const [totalSessionTime, setTotalSessionTime] = useState(0);
  const [fileChanges, setFileChanges] = useState<FileChange[]>([]);
  const [scrollOffset, setScrollOffset] = useState(0);

  const thinkingStartRef = useRef<number>(0);
  const thinkingTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const turnStartRef = useRef<number>(0);
  const reasoningStartRef = useRef<number>(0);
  const reasoningDurationRef = useRef<number>(0);
  const hasReasoningRef = useRef(false);

  // B122: Use refs for state values that retryLastMessage reads inside
  // setTimeout to avoid stale closures.
  const isLoadingRef = useRef(false);
  // Keep the ref in sync with the state.
  const syncIsLoading = (v: boolean) => {
    isLoadingRef.current = v;
    setIsLoading(v);
  };

  // B017: Clean up thinking timer on unmount so the interval doesn't fire
  // after the component is gone.
  useEffect(() => {
    return () => {
      if (thinkingTimerRef.current) {
        clearInterval(thinkingTimerRef.current);
        thinkingTimerRef.current = null;
      }
    };
  }, []);

  // Generate a local session ID so undo/retry can target the right agent.
  const sessionIdRef = useRef<string>(`tui-${Date.now()}-${++_nextSessionId}`);

  const sendMessage = useCallback(
    async (text: string) => {
      if (isLoading) return;

      const userMsg: Message = {
        id: newMsgId(),
        role: "user",
        content: text,
        startTime: Date.now(),
      };
      setMessages((prev) => [...prev, userMsg]);
      syncIsLoading(true);
      setError(null);
      setStreamingText("");
      setReasoning(undefined);
      setQueuedMessages(1);
      setLastUserMessage(text);

      // Start thinking timer
      const now = Date.now();
      thinkingStartRef.current = now;
      turnStartRef.current = now;
      setThinkingElapsed(0);
      setTurnDuration(undefined);
      setFileChanges([]);
      reasoningDurationRef.current = 0;
      reasoningStartRef.current = 0;
      hasReasoningRef.current = false;
      // Auto-scroll to bottom on new message
      setScrollOffset(0);

      thinkingTimerRef.current = setInterval(() => {
        setThinkingElapsed(
          Math.floor((Date.now() - thinkingStartRef.current) / 1000),
        );
      }, 250);

      // Create placeholder assistant message
      const assistantMsg: Message = {
        id: newMsgId(),
        role: "assistant",
        content: "",
        startTime: now,
      };
      setMessages((prev) => [...prev, assistantMsg]);

      try {
        let accumulatedText = "";
        let reasoningText = "";

        for await (const event of backend.streamCall("agent.send", {
          message: text,
          session_id: sessionIdRef.current,
        })) {
          switch (event.type) {
            case "status":
              if (event.phase) {
                setStatusPhase(event.phase);
              }
              break;

            case "text":
              // If we were in reasoning phase, compute duration
              if (reasoningStartRef.current > 0) {
                reasoningDurationRef.current = Date.now() - reasoningStartRef.current;
                reasoningStartRef.current = 0;
              }
              if (event.content) {
                accumulatedText += event.content;
                setStreamingText(accumulatedText);
                // Update the last message progressively
                setMessages((prev) => {
                  const updated = [...prev];
                  updated[updated.length - 1] = {
                    ...updated[updated.length - 1],
                    content: accumulatedText,
                  };
                  return updated;
                });
              }
              break;

            case "reasoning":
              if (event.content) {
                // Track reasoning start time
                if (!hasReasoningRef.current) {
                  reasoningStartRef.current = Date.now();
                  hasReasoningRef.current = true;
                }
                reasoningText += event.content;
                setReasoning(reasoningText);
              }
              break;

            case "tool_call":
              // If we were in reasoning phase, compute duration
              if (reasoningStartRef.current > 0) {
                reasoningDurationRef.current = Date.now() - reasoningStartRef.current;
                reasoningStartRef.current = 0;
              }
              // Extract file changes
              if (event.name && FILE_TOOL_NAMES.has(event.name)) {
                const path = tryExtractPath(event.input);
                if (path) {
                  const { added, removed } = estimateDelta(event.output, event.input);
                  setFileChanges((prev) => {
                    // Avoid duplicate entries for same path within a turn
                    const existing = prev.findIndex((fc) => fc.path === path);
                    if (existing >= 0) {
                      const updated = [...prev];
                      updated[existing] = {
                        ...updated[existing],
                        added: updated[existing].added + added,
                        removed: updated[existing].removed + removed,
                        timestamp: Date.now(),
                      };
                      return updated;
                    }
                    return [...prev, { path, added, removed, timestamp: Date.now() }];
                  });
                }
              }
              // Append tool call info to the streaming text
              if (event.name) {
                const toolCallText = `\n\n🔧 Calling tool: **${event.name}**`;
                accumulatedText += toolCallText;
                setStreamingText(accumulatedText);
                setMessages((prev) => {
                  const updated = [...prev];
                  updated[updated.length - 1] = {
                    ...updated[updated.length - 1],
                    content: accumulatedText,
                  };
                  return updated;
                });
              }
              break;

            case "done": {
              // Compute final reasoning duration
              if (reasoningStartRef.current > 0) {
                reasoningDurationRef.current = Date.now() - reasoningStartRef.current;
                reasoningStartRef.current = 0;
              }
              // Compute turn duration
              const turnDurationMs = Date.now() - turnStartRef.current;
              setTurnDuration(turnDurationMs);
              // Accumulate session total time
              setTotalSessionTime((prev) => prev + turnDurationMs);

              if (event.model) {
                const parts = event.model.split("/");
                if (parts.length >= 2) {
                  setProvider(parts[0]);
                  setModel(parts.slice(1).join("/"));
                } else {
                  setModel(event.model);
                }
              }
              // Finalize the message with all metadata
              setMessages((prev) => {
                const updated = [...prev];
                if (updated.length > 0) {
                  updated[updated.length - 1] = {
                    ...updated[updated.length - 1],
                    content: accumulatedText,
                    reasoning: reasoningText || undefined,
                    reasoningDuration: reasoningDurationRef.current || undefined,
                    turnDuration: turnDurationMs,
                    startTime: turnStartRef.current,
                  };
                }
                return updated;
              });
              break;
            }

            case "error":
              throw new Error(event.message ?? "Unknown stream error");
          }
        }
      } catch (err) {
        const errorMessage = (err as Error).message;
        setError(errorMessage);
        // Replace the placeholder with an error message
        setMessages((prev) => {
          const updated = [...prev];
          if (updated.length > 0 && updated[updated.length - 1].role === "assistant") {
            if (updated[updated.length - 1].content === "") {
              // Remove empty placeholder
              updated.pop();
            }
          }
          updated.push({
            id: newMsgId(),
            role: "system",
            content: `Error: ${errorMessage}`,
          });
          return updated;
        });
        setStreamingText(undefined);
        setReasoning(undefined);
      } finally {
        syncIsLoading(false);
        setStreamingText(undefined);
        setQueuedMessages(0);
        // Stop thinking timer
        if (thinkingTimerRef.current) {
          clearInterval(thinkingTimerRef.current);
          thinkingTimerRef.current = null;
        }
      }
    },
    [backend, isLoading],
  );

  const clearChat = useCallback(() => {
    setMessages([]);
    setError(null);
    setStreamingText(undefined);
    setReasoning(undefined);
    setQueuedMessages(0);
    setFileChanges([]);
    setTurnDuration(undefined);
    setTotalSessionTime(0);
    setScrollOffset(0);
    // Rotate session ID on clear so old agent history doesn't leak.
    sessionIdRef.current = `tui-${Date.now()}-${++_nextSessionId}`;
  }, []);

  const undoLastExchange = useCallback(() => {
    // Remove the last user→assistant exchange from local messages.
    setMessages((prev) => {
      const updated = [...prev];
      // Walk backwards: remove assistant messages and then the last user message.
      while (updated.length > 0 && updated[updated.length - 1].role === "assistant") {
        updated.pop();
      }
      if (updated.length > 0 && updated[updated.length - 1].role === "user") {
        updated.pop();
      }
      return updated;
    });

    // Tell the server to pop the last exchange from agent history.
    backend.undo(sessionIdRef.current).catch((err: unknown) => {
      console.error("undo failed:", err);
    });
  }, [backend]);

  const retryLastMessage = useCallback(() => {
    // B122: Guard with ref instead of stale closure isLoading.
    if (isLoadingRef.current) return;

    // Find the last user message, then undo everything after it and resend.
    setMessages((prev) => {
      // Walk backwards to find the last user message.
      let lastUserIdx = -1;
      for (let i = prev.length - 1; i >= 0; i--) {
        if (prev[i].role === "user") {
          lastUserIdx = i;
          break;
        }
      }

      if (lastUserIdx < 0) return prev;

      const lastUserMsg = prev[lastUserIdx];
      const text = lastUserMsg.content;

      // Truncate to just before the last user message.
      const truncated = prev.slice(0, lastUserIdx);

      // Resend the message (async, outside setState).
      setTimeout(() => {
        sendMessage(text);
      }, 0);

      return truncated;
    });

    // Also tell the server to undo, since retry will resend.
    backend.undo(sessionIdRef.current).catch((err: unknown) => { console.error("undo (retry) failed:", err); });
  }, [backend, sendMessage]);

  return {
    messages,
    sendMessage,
    clearChat,
    undoLastExchange,
    retryLastMessage,
    isLoading,
    error,
    model,
    provider,
    streamingText,
    reasoning,
    queuedMessages,
    statusPhase,
    sessionId: sessionIdRef.current,
    lastUserMessage,
    thinkingElapsed,
    turnDuration,
    totalSessionTime,
    fileChanges,
    scrollOffset,
    setScrollOffset,
  };
}
