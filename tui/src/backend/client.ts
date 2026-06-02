import type {
  JSONRPCRequest,
  JSONRPCResponse,
  StreamEvent,
  ForkResult,
  MemoPhraseResult,
  MemoLearnResult,
} from "./types.ts";

const DEFAULT_PORT = 3000;

function getBaseUrl(): string {
  const port = process.env["OVERKILL_API_PORT"] ?? String(DEFAULT_PORT);
  return `http://localhost:${port}`;
}

export class BackendClient {
  private baseUrl: string;
  private requestId = 0;
  // B019: Active stream cancellation. Set by streamCall, cleared when the
  // stream ends. Call cancel() to abort the in-flight fetch.
  private streamAbort: AbortController | null = null;

  constructor(baseUrl?: string) {
    this.baseUrl = baseUrl ?? getBaseUrl();
  }

  private nextId(): number {
    return ++this.requestId;
  }

  /** B019: Cancel the currently active streamCall. No-op when idle. */
  cancel(): void {
    if (this.streamAbort) {
      this.streamAbort.abort();
      this.streamAbort = null;
    }
  }

  async call<T = unknown>(method: string, params?: unknown): Promise<T> {
    const body: JSONRPCRequest = {
      jsonrpc: "2.0",
      method,
      params,
      id: this.nextId(),
    };

    const res = await fetch(`${this.baseUrl}/rpc`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });

    if (!res.ok) {
      throw new Error(`HTTP ${res.status}: ${res.statusText}`);
    }

    const json: JSONRPCResponse = (await res.json()) as JSONRPCResponse;

    if (json.error) {
      throw new Error(`RPC ${json.error.code}: ${json.error.message}`);
    }

    return json.result as T;
  }

  stream(
    method: string,
    params?: unknown,
    onData?: (chunk: string) => void,
    onError?: (err: Error) => void,
  ): () => void {
    const url = new URL(`${this.baseUrl}/sse`);
    url.searchParams.set("method", method);
    if (params !== undefined) {
      url.searchParams.set("params", JSON.stringify(params));
    }

    const controller = new AbortController();

    const run = async () => {
      try {
        const res = await fetch(url.toString(), {
          signal: controller.signal,
        });

        if (!res.ok) {
          throw new Error(`SSE ${res.status}: ${res.statusText}`);
        }

        const reader = res.body?.getReader();
        if (!reader) {
          throw new Error("No response body");
        }

        const decoder = new TextDecoder();
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          const text = decoder.decode(value, { stream: true });
          onData?.(text);
        }
      } catch (err) {
        if ((err as Error).name !== "AbortError") {
          onError?.(err as Error);
        }
      }
    };

    void run();

    return () => controller.abort();
  }

  async *streamCall(
    method: string,
    params?: Record<string, unknown>,
  ): AsyncGenerator<StreamEvent> {
    const url = new URL(`${this.baseUrl}/stream`);
    url.searchParams.set("method", method);
    if (params !== undefined) {
      url.searchParams.set("params", JSON.stringify(params));
    }

    // B019: Wire AbortController so callers can cancel the stream.
    const controller = new AbortController();
    this.streamAbort = controller;

    try {
      const res = await fetch(url.toString(), { signal: controller.signal });

      if (!res.ok) {
        throw new Error(`SSE ${res.status}: ${res.statusText}`);
      }

      const reader = res.body?.getReader();
      if (!reader) {
        throw new Error("No response body");
      }

      const decoder = new TextDecoder();
      let buffer = "";
      let currentEvent = "";
      let currentData = "";

      const yieldEvent = (): StreamEvent | null => {
        if (!currentData) return null;
        try {
          const parsed = JSON.parse(currentData) as StreamEvent;
          if (currentEvent) {
            parsed.type = currentEvent as StreamEvent["type"];
          }
          return parsed;
        } catch {
          return null;
        }
      };

      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split("\n");
          // Keep the last partial line in the buffer
          buffer = lines.pop() ?? "";

          for (const line of lines) {
            if (line.startsWith("event: ")) {
              currentEvent = line.slice(7).trim();
            } else if (line.startsWith("data: ")) {
              currentData += line.slice(6);
            } else if (line === "") {
              // Empty line = end of event
              const evt = yieldEvent();
              if (evt) yield evt;
              currentEvent = "";
              currentData = "";
            }
          }
        }

        // Flush remaining data
        if (currentData) {
          const evt = yieldEvent();
          if (evt) yield evt;
        }
      } finally {
        reader.releaseLock();
      }
    } finally {
      // B019: Clear the abort controller so cancel() is a no-op after
      // the stream ends naturally or on fetch/read failure.
      this.streamAbort = null;
    }
  }

  async health(): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/health`, {
        method: "GET",
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  async estop(): Promise<string> {
    return this.call<string>("estop");
  }

  async undo(
    sessionId: string,
  ): Promise<{ status: string; removed_text: string }> {
    return this.call<{ status: string; removed_text: string }>("agent.undo", {
      session_id: sessionId,
    });
  }

  async steer(sessionId: string, message: string): Promise<{ status: string }> {
    return this.call<{ status: string }>("agent.steer", {
      session_id: sessionId,
      message,
    });
  }

  async fork(sessionId: string, name?: string): Promise<ForkResult> {
    return this.call<ForkResult>("agent.fork", {
      session_id: sessionId,
      name,
    });
  }
  // --- memo ---

  async memoPhrase(input?: string, action?: string): Promise<MemoPhraseResult> {
    return this.call<MemoPhraseResult>("memo.phrase", {
      input: input ?? "",
      action: action ?? "",
    });
  }

  async memoLearn(
    patterns: string[],
    phrases: string[],
    category: string,
  ): Promise<MemoLearnResult> {
    return this.call<MemoLearnResult>("memo.learn", {
      patterns,
      phrases,
      category,
    });
  }
}

export function createClient(baseUrl?: string): BackendClient {
  return new BackendClient(baseUrl);
}
