import type { JSONRPCRequest, JSONRPCResponse } from "./types.ts";

const DEFAULT_PORT = 3000;

function getBaseUrl(): string {
  const port = process.env["OVERKILL_API_PORT"] ?? String(DEFAULT_PORT);
  return `http://localhost:${port}`;
}

export class BackendClient {
  private baseUrl: string;
  private requestId = 0;

  constructor(baseUrl?: string) {
    this.baseUrl = baseUrl ?? getBaseUrl();
  }

  private nextId(): number {
    return ++this.requestId;
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
}

export function createClient(baseUrl?: string): BackendClient {
  return new BackendClient(baseUrl);
}
