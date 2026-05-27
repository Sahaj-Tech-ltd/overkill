import { useState, useCallback } from "react";
import type { BackendClient } from "../backend/client.ts";
import type { Message, AgentSendResult } from "../backend/types.ts";

export interface UseChatResult {
  messages: Message[];
  sendMessage: (text: string) => Promise<void>;
  clearChat: () => void;
  isLoading: boolean;
  error: string | null;
  model?: string;
  provider?: string;
}

export function useChat(backend: BackendClient): UseChatResult {
  const [messages, setMessages] = useState<Message[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [model, setModel] = useState<string | undefined>();
  const [provider, setProvider] = useState<string | undefined>();

  const sendMessage = useCallback(
    async (text: string) => {
      if (isLoading) return;

      const userMsg: Message = { role: "user", content: text };
      setMessages((prev) => [...prev, userMsg]);
      setIsLoading(true);
      setError(null);

      try {
        const result = await backend.call<AgentSendResult>("agent.send", {
          message: text,
        });

        const assistantMsg: Message = {
          role: "assistant",
          content: result.response,
        };
        setMessages((prev) => [...prev, assistantMsg]);

        if (result.model) {
          const parts = result.model.split("/");
          if (parts.length >= 2) {
            setProvider(parts[0]);
            setModel(parts.slice(1).join("/"));
          } else {
            setModel(result.model);
          }
        }
      } catch (err) {
        const errorMessage = (err as Error).message;
        setError(errorMessage);
        const errorMsg: Message = {
          role: "system",
          content: `Error: ${errorMessage}`,
        };
        setMessages((prev) => [...prev, errorMsg]);
      } finally {
        setIsLoading(false);
      }
    },
    [backend, isLoading],
  );

  const clearChat = useCallback(() => {
    setMessages([]);
    setError(null);
  }, []);

  return {
    messages,
    sendMessage,
    clearChat,
    isLoading,
    error,
    model,
    provider,
  };
}
