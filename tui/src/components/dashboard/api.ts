// ─── Dashboard API helpers ────────────────────────────────────────────────
// Uses BackendClient for JSON-RPC calls. The client already knows the
// correct API port (from OVERKILL_API_PORT env var or constructor).

import type { BackendClient } from "../../backend/client.ts";

// ─── Types ─────────────────────────────────────────────────────────────────

export interface GoalData {
  objective: string;
  status: "active" | "paused" | "blocked" | "complete" | "budget_limited";
  token_budget: number | null;
  tokens_used: number;
  time_used_s: number;
}

export type PlanItemStatus = "pending" | "in_progress" | "done";

export interface PlanItem {
  id: string;
  text: string;
  status: PlanItemStatus;
}

export interface PlanData {
  title: string;
  items: PlanItem[];
}

// ─── Fetchers ──────────────────────────────────────────────────────────────

export async function fetchGoal(backend: BackendClient, sessionId: string, signal?: AbortSignal): Promise<GoalData | null> {
  const result = await backend.get<GoalData | {goal: null}>("/api/goal", {session_id: sessionId}, {signal});
  return "goal" in result ? null : result;
}
export async function fetchPlan(backend: BackendClient, sessionId: string, signal?: AbortSignal): Promise<PlanData | null> {
  const result = await backend.get<PlanData | {plan: null}>("/api/plan", {session_id: sessionId}, {signal});
  return "plan" in result ? null : result;
}
