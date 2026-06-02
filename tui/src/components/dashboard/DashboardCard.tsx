import React, { useState, useEffect } from "react";
import { Box, Text } from "ink";
import { DialogContainer } from "../dialogs/dialog-container.tsx";
import { Card } from "../design-system/Card.tsx";
import { Badge } from "../design-system/Badge.tsx";
import { useTheme } from "../../lib/theme.ts";
import { fetchGoal, fetchPlan } from "./api.ts";
import type { GoalData, PlanData, PlanItemStatus } from "./api.ts";
import type { BackendClient } from "../../backend/client.ts";

// ─── Props ─────────────────────────────────────────────────────────────────

export interface DashboardCardProps {
  readonly open: boolean;
  readonly onClose: () => void;
  readonly backend: BackendClient;
}

// ─── Refresh interval ──────────────────────────────────────────────────────

const REFRESH_MS = 5000;

// ─── Status to Badge variant mapping ──────────────────────────────────────

function goalBadgeVariant(
  status: GoalData["status"],
): "success" | "warning" | "info" | "danger" {
  switch (status) {
    case "active":
      return "success";
    case "blocked":
      return "warning";
    case "complete":
      return "info";
    case "budget_limited":
      return "danger";
  }
}

function goalBadgeLabel(status: GoalData["status"]): string {
  switch (status) {
    case "active":
      return "ACTIVE";
    case "blocked":
      return "BLOCKED";
    case "complete":
      return "DONE";
    case "budget_limited":
      return "BUDGET LIMIT";
  }
}

// ─── Plan step status icon ────────────────────────────────────────────────

const PLAN_ICONS: Record<PlanItemStatus, string> = {
  pending: "⬜",
  in_progress: "◐",
  done: "✅",
};

// ─── Helpers ───────────────────────────────────────────────────────────────

function formatTime(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}

// ─── Budget progress bar (▰ filled  ▱ empty) ──────────────────────────────

function BudgetBar({
  used,
  budget,
  theme,
}: {
  used: number;
  budget: number | null;
  theme: ReturnType<typeof useTheme>;
}): React.JSX.Element {
  const barWidth = 20;

  if (budget === null || budget === 0) {
    return (
      <Box>
        <Text color={theme.muted}>Unlimited</Text>
      </Box>
    );
  }

  const ratio = Math.min(used / budget, 1);
  const filled = Math.round(ratio * barWidth);
  const empty = barWidth - filled;

  const fillColor =
    ratio > 0.9 ? theme.error : ratio > 0.7 ? theme.warning : theme.success;

  return (
    <Box flexDirection="row">
      <Text color={fillColor}>{"▰".repeat(filled)}</Text>
      <Text color={theme.muted}>{"▱".repeat(empty)}</Text>
      <Text>
        {" "}
        {formatTokens(used)} / {formatTokens(budget)}
      </Text>
    </Box>
  );
}

// ─── DashboardCard (internal, no panel wrapper) ───────────────────────────

export function DashboardCard({
  open,
  onClose,
  backend,
}: DashboardCardProps): React.JSX.Element | null {
  const theme = useTheme();

  const [goal, setGoal] = useState<GoalData | null>(null);
  const [plan, setPlan] = useState<PlanData | null>(null);
  const [goalError, setGoalError] = useState<string | null>(null);
  const [planError, setPlanError] = useState<string | null>(null);

  // ── Fetch helpers ─────────────────────────────────────────────────────

  const load = () => {
    fetchGoal(backend)
      .then((g) => {
        setGoal(g);
        setGoalError(null);
      })
      .catch((e: unknown) => {
        setGoalError((e as Error).message);
      });

    fetchPlan(backend)
      .then((p) => {
        setPlan(p);
        setPlanError(null);
      })
      .catch((e: unknown) => {
        setPlanError((e as Error).message);
      });
  };

  // ── Auto-refresh (only when open) ──────────────────────────────────────

  useEffect(() => {
    if (!open) return;
    load();
    const iv = setInterval(load, REFRESH_MS);
    return () => clearInterval(iv);
  }, [open]);

  // ── Plan counters ─────────────────────────────────────────────────────

  const planDone = plan?.items.filter((i) => i.status === "done").length ?? 0;
  const planTotal = plan?.items.length ?? 0;
  const planRemaining = plan ? planDone < planTotal : false;

  // ── Render ────────────────────────────────────────────────────────────

  if (!open) return null;

  return (
    <DialogContainer open={open} onClose={onClose} title="Dashboard">
      <Card title="Dashboard">
        <Box flexDirection="column">
          {/* ── ACTIVE GOAL ──────────────────────────────────────────── */}
          <Box marginBottom={1}>
            <Text bold color={theme.accent}>
              Active Goal
            </Text>
          </Box>

          {goalError && !goal ? (
            <Box marginBottom={1}>
              <Text color={theme.error}>Goal: {goalError}</Text>
            </Box>
          ) : goal ? (
            <Box flexDirection="column" marginBottom={1}>
              <Box marginBottom={1}>
                <Box marginRight={1}>
                  <Badge
                    variant={goalBadgeVariant(goal.status)}
                    label={goalBadgeLabel(goal.status)}
                  />
                </Box>
                <Text>{goal.objective}</Text>
              </Box>
              <Box>
                <Text color={theme.muted}>
                  Tokens: {formatTokens(goal.tokens_used)} | Time:{" "}
                  {formatTime(goal.time_used_s)}
                </Text>
              </Box>
            </Box>
          ) : (
            <Box marginBottom={1}>
              <Text color={theme.muted}>—</Text>
            </Box>
          )}

          {/* ── DIVIDER ──────────────────────────────────────────────── */}
          <Box marginBottom={1}>
            <Text color={theme.border}>{"─".repeat(30)}</Text>
          </Box>

          {/* ── PLAN ─────────────────────────────────────────────────── */}
          <Box marginBottom={1}>
            <Text bold color={theme.accent}>
              Plan
            </Text>
            {planTotal > 0 ? (
              <Text color={theme.muted}>
                {" "}
                ({planDone}/{planTotal} done
                {planRemaining ? `, ${planTotal - planDone} remaining` : ""})
              </Text>
            ) : null}
          </Box>

          {planError && !plan ? (
            <Box marginBottom={1}>
              <Text color={theme.error}>Plan: {planError}</Text>
            </Box>
          ) : plan && plan.items.length > 0 ? (
            <Box flexDirection="column" marginBottom={1}>
              {plan.items.map((item) => (
                <Box key={item.id} flexDirection="row">
                  <Box marginRight={1}>
                    <Text>{PLAN_ICONS[item.status]}</Text>
                  </Box>
                  <Text
                    dimColor={item.status === "done"}
                    strikethrough={item.status === "done"}
                  >
                    {item.text}
                  </Text>
                </Box>
              ))}
            </Box>
          ) : (
            <Box marginBottom={1}>
              <Text color={theme.muted}>—</Text>
            </Box>
          )}

          {/* ── DIVIDER ──────────────────────────────────────────────── */}
          <Box marginBottom={1}>
            <Text color={theme.border}>{"─".repeat(30)}</Text>
          </Box>

          {/* ── BUDGET ───────────────────────────────────────────────── */}
          <Box marginBottom={1}>
            <Text bold color={theme.accent}>
              Budget
            </Text>
          </Box>

          {goalError && !goal ? (
            <Box>
              <Text color={theme.muted}>—</Text>
            </Box>
          ) : goal ? (
            <BudgetBar
              used={goal.tokens_used}
              budget={goal.token_budget}
              theme={theme}
            />
          ) : (
            <Box>
              <Text color={theme.muted}>—</Text>
            </Box>
          )}
        </Box>
      </Card>
    </DialogContainer>
  );
}
