export interface SessionInfo {
  id: string;
  folder: string;
  name: string;
  title?: string;
  createdAt: string;
  updatedAt: string;
  parentId?: string;
  children?: string[];
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

export interface FileChange {
  path: string;
  added: number;
  removed: number;
  timestamp: number;
}

export interface Message {
  id?: string;
  role: "user" | "assistant" | "system";
  content: string;
  reasoning?: string;
  reasoningDuration?: number;
  turnDuration?: number;
  startTime?: number;
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
  fileChanges?: FileChange[];
}

// --- wizard.catalog ---

export interface WizardOption {
  id: string;
  name: string;
  description: string;
  rating: number;
  stars: string; // pre-rendered "⭐⭐⭐⭐⭐"
  category: string;
  api_key_env?: string;
  default_base?: string;
  models?: string[];
  requires_key: boolean;
  tags?: string[];
}

export interface WizardCatalogResult {
  providers: WizardOption[];
  gateways: WizardOption[];
  tts: WizardOption[];
  databases: WizardOption[];
  recommended: QuickSetup;
}

export interface QuickSetup {
  provider: string;
  model: string;
  gateway: string;
  tts: string;
  database: string;
}

export interface WizardQuickSetupParams {
  provider?: string;
  model?: string;
  gateway?: string;
  tts?: string;
  database?: string;
}

export interface WizardQuickSetupResult {
  status: string;
  message: string;
}

// --- self-eval ---

export interface SelfEvalStatus {
  phase: string; // "idle" | "planning" | "executing" | "reflecting" | "red_team_check"
  confidence: number; // 0.0 - 1.0
  reflection_notes?: string;
  iteration: number;
  max_iterations: number;
  red_team_passed?: boolean;
  started_at?: string;
}

// --- test pane ---

export interface TestResult {
  id: string;
  name: string;
  passed: boolean;
  error?: string;
  duration_ms?: number;
  category?: string; // "security", "prompt_injection", "tool_safety", etc.
}

export interface TestResultsResult {
  tests: TestResult[];
  total: number;
  passed: number;
  failed: number;
  running: boolean;
}

// --- sequential queue ---

export interface QueueItem {
  index: number;
  description: string;
  status: string; // "pending" | "active" | "done" | "failed" | "skipped"
  error?: string;
  elapsed_ms: number;
}

export interface QueueStatus {
  active: boolean;
  total: number;
  done: number;
  failed: number;
  items: QueueItem[];
}

// --- session fork ---

export interface ForkParams {
  session_id: string;
  name?: string;
}

export interface ForkResult {
  session: SessionInfo;
}

export interface SessionUsageParams {
  scope?: "session" | "daily" | "all";
  session_id?: string;
}

export interface CostSummary {
  total_usd: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  request_count: number;
}

export interface UsageReport {
  summary: CostSummary;
  by_model: Record<string, CostSummary>;
  by_provider: Record<string, CostSummary>;
}

export interface SessionUsageResult {
  report: UsageReport;
  daily: UsageReport | null;
}
