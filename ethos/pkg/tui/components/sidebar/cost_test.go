package sidebar

import (
	"testing"

	"github.com/Sahaj-Tech-ltd/ethos/internal/cost"
)

func TestCostPanel_Render(t *testing.T) {
	p := NewCostPanel()
	p.UpdateSummary(cost.CostSummary{InputTokens: 1200, OutputTokens: 3500, TotalUSD: 0.05})
	v := p.View(30, 15)
	if !containsStr(v, "Token Usage") || !containsStr(v, "1.2K") {
		t.Error("missing token info")
	}
}

func TestCostPanel_DailyBudget(t *testing.T) {
	p := NewCostPanel()
	p.UpdateSummary(cost.CostSummary{TotalUSD: 0.05})
	p.UpdateBudget(cost.BudgetStatus{DailyPercent: 0.7, DailyUsed: 0.05, DailyLimit: 0.10})
	v := p.View(30, 15)
	if !containsStr(v, "█") {
		t.Error("should have progress bar")
	}
}

func TestCostPanel_BudgetWarning(t *testing.T) {
	p := NewCostPanel()
	p.UpdateSummary(cost.CostSummary{})
	p.UpdateBudget(cost.BudgetStatus{DailyPercent: 0.85})
	v := p.View(30, 15)
	if !containsStr(v, "█") {
		t.Error("should have bar")
	}
}

func TestCostPanel_PerTaskBudget(t *testing.T) {
	p := NewCostPanel()
	p.UpdateSummary(cost.CostSummary{})
	p.UpdateBudget(cost.BudgetStatus{TaskUsed: 0.02, TaskLimit: 1.00})
	v := p.View(30, 15)
	if !containsStr(v, "0.02") {
		t.Error("should show task budget")
	}
}

func TestCostPanel_RollingWindow(t *testing.T) {
	p := NewCostPanel()
	p.UpdateSummary(cost.CostSummary{})
	p.UpdateBudget(cost.BudgetStatus{RollingUsed: 0.03, Window: 5 * 1e9 * 3600})
	v := p.View(30, 15)
	if !containsStr(v, "0.03") {
		t.Error("should show rolling")
	}
}

func TestCostPanel_Empty(t *testing.T) {
	p := NewCostPanel()
	v := p.View(30, 15)
	if !containsStr(v, "No usage") {
		t.Error("should show empty state")
	}
}
