// ─── Dashboard API helpers ────────────────────────────────────────────────
// Uses BackendClient for JSON-RPC calls. The client already knows the
// correct API port (from OVERKILL_API_PORT env var or constructor).

import type { BackendClient } from "../../backend/client.ts";

// ─── Types ─────────────────────────────────────────────────────────────────

export interface GoalData {
  objective: string;
  status: "active" | "blocked" | "complete" | "budget_limited";
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

export async function fetchGoal(backend: BackendClient): Promise<GoalData> {
  const result = await backend.call<GoalData>("goal.get", {});
  return (
    result ?? {
      objective: "",
      status: "active",
      token_budget: null,
      tokens_used: 0,
      time_used_s: 0,
    }
  );
}

export async function fetchPlan(backend: BackendClient): Promise<PlanData> {
  const result = await backend.call<PlanData>("plan.get", {});
  return result ?? { title: "", items: [] };
}
