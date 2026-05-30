// ─── Dashboard API helpers ────────────────────────────────────────────────
// Uses fetch() directly against REST endpoints. Defaults to localhost:8420
// (Overkill web UI port) or OVERKILL_API_PORT from environment.

const DEFAULT_PORT = 8420; // Overkill web UI port (matches DefaultWebUIAddr)

function getBaseUrl(): string {
  const port =
    process.env["OVERKILL_API_PORT"] ??
    process.env["API_PORT"] ??
    String(DEFAULT_PORT);
  return `http://localhost:${port}`;
}

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

export async function fetchGoal(): Promise<GoalData> {
  const res = await fetch(`${getBaseUrl()}/api/goal`);
  if (!res.ok) {
    throw new Error(`Goal API returned ${res.status}`);
  }
  return res.json() as Promise<GoalData>;
}

export async function fetchPlan(): Promise<PlanData> {
  const res = await fetch(`${getBaseUrl()}/api/plan`);
  if (!res.ok) {
    throw new Error(`Plan API returned ${res.status}`);
  }
  return res.json() as Promise<PlanData>;
}
