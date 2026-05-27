export interface SessionInfo {
  id: string;
  folder: string;
  name: string;
  createdAt: string;
  updatedAt: string;
}

export interface ProviderInfo {
  name: string;
  models: ModelInfo[];
}

export interface ModelInfo {
  id: string;
  name: string;
  maxTokens?: number;
  supports_vision?: boolean;
}

export type SubagentStatus = "running" | "completed" | "failed";

export interface SubagentInfo {
  id: string;
  name: string;
  status: SubagentStatus;
  startedAt: string;
  elapsed: number;
  model?: string;
}

export interface OnboardingProviderConfig {
  name: string;
  apiKey: string;
  baseUrl?: string;
}

export interface OnboardingTTSConfig {
  provider: string;
  apiKey?: string;
}

export interface OnboardingGatewayConfig {
  discordToken?: string;
  telegramToken?: string;
}

export interface OnboardingConfig {
  providers: OnboardingProviderConfig[];
  defaultModel: string;
  visionProvider?: string;
  tts?: OnboardingTTSConfig;
  gateway?: OnboardingGatewayConfig;
}

export interface GatewayTestParams {
  gateway: string;
  token: string;
}

export interface GatewayTestResult {
  ok: boolean;
  error?: string;
}

export interface HealthResult {
  ok: boolean;
  version: string;
  uptime: string;
  sessions: number;
}

export interface SendMessageParams {
  sessionId: string;
  message: string;
}

export interface SendMessageResult {
  reply: string;
  tokensUsed: number;
}

export interface JSONRPCRequest {
  jsonrpc: "2.0";
  method: string;
  params?: unknown;
  id: number;
}

export interface JSONRPCResponse {
  jsonrpc: "2.0";
  result?: unknown;
  error?: JSONRPCError;
  id: number;
}

export interface JSONRPCError {
  code: number;
  message: string;
  data?: unknown;
}

export interface Message {
  role: "user" | "assistant" | "system";
  content: string;
}

export interface AgentSendParams {
  message: string;
  sessionId?: string;
}

export interface AgentSendResult {
  response: string;
  toolCalls?: unknown[];
  totalTokens?: number;
  model?: string;
}

export type ConnectionState = "connecting" | "connected" | "disconnected";

export interface StreamEvent {
  type: "status" | "reasoning" | "text" | "tool_call" | "done" | "error";
  phase?: string;
  content?: string;
  name?: string;
  input?: unknown;
  output?: string;
  model?: string;
  tokens?: number;
  tool_calls?: number;
  steps?: number;
  message?: string;
}
