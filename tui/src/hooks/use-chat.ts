import { useState, useCallback } from "react";
import type { BackendClient } from "../backend/client.ts";
import type { Message, StreamEvent } from "../backend/types.ts";

export interface UseChatResult {
  messages: Message[];
  sendMessage: (text: string) => Promise<void>;
  clearChat: () => void;
  isLoading: boolean;
  error: string | null;
  model?: string;
  provider?: string;
  streamingText?: string;
  reasoning?: string;
  queuedMessages: number;
  statusPhase?: string;
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

  const sendMessage = useCallback(
    async (text: string) => {
      if (isLoading) return;

      const userMsg: Message = { role: "user", content: text };
      setMessages((prev) => [...prev, userMsg]);
      setIsLoading(true);
      setError(null);
      setStreamingText("");
      setReasoning(undefined);
      setQueuedMessages(1);

      // Create placeholder assistant message
      const assistantMsg: Message = {
        role: "assistant",
        content: "",
      };
      setMessages((prev) => [...prev, assistantMsg]);

      try {
        let accumulatedText = "";
        let accumulatedReasoning = "";

        for await (const event of backend.streamCall("agent.send", {
          message: text,
        })) {
          switch (event.type) {
            case "status":
              if (event.phase) {
                setStatusPhase(event.phase);
              }
              break;

            case "text":
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
                accumulatedReasoning += event.content;
                setReasoning(accumulatedReasoning);
              }
              break;

            case "tool_call":
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

            case "done":
              if (event.model) {
                const parts = event.model.split("/");
                if (parts.length >= 2) {
                  setProvider(parts[0]);
                  setModel(parts.slice(1).join("/"));
                } else {
                  setModel(event.model);
                }
              }
              // Finalize the message
              setMessages((prev) => {
                const updated = [...prev];
                if (updated.length > 0) {
                  updated[updated.length - 1] = {
                    ...updated[updated.length - 1],
                    content: accumulatedText,
                  };
                }
                return updated;
              });
              break;

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
            role: "system",
            content: `Error: ${errorMessage}`,
          });
          return updated;
        });
        setStreamingText(undefined);
        setReasoning(undefined);
      } finally {
        setIsLoading(false);
        setStreamingText(undefined);
        setQueuedMessages(0);
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
  }, []);

  return {
    messages,
    sendMessage,
    clearChat,
    isLoading,
    error,
    model,
    provider,
    streamingText,
    reasoning,
    queuedMessages,
  };
}
