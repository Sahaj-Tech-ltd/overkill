package sidebar

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
)

type CostPanel struct {
	summary cost.CostSummary
	budget  cost.BudgetStatus
	width   int
	height  int
	hasData bool
}

func NewCostPanel() CostPanel {
	return CostPanel{}
}

func (p CostPanel) Name() string {
	return "Cost"
}

func (p *CostPanel) UpdateSummary(s cost.CostSummary) {
	p.summary = s
	p.hasData = true
}

func (p *CostPanel) UpdateBudget(b cost.BudgetStatus) {
	p.budget = b
}

func (p CostPanel) View(width, height int) string {
	if width <= 0 {
		width = 30
	}
	if height <= 0 {
		height = 15
	}

	if !p.hasData {
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))
		return dim.Render("No usage yet")
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#89b4fa"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4"))

	var lines []string
	lines = append(lines, headerStyle.Render("Token Usage"))
	lines = append(lines, labelStyle.Render("Input:  ")+valueStyle.Render(formatCostTokens(p.summary.InputTokens)))
	lines = append(lines, labelStyle.Render("Output: ")+valueStyle.Render(formatCostTokens(p.summary.OutputTokens)))

	cacheStr := formatCostTokens(p.summary.CachedTokens)
	lines = append(lines, labelStyle.Render("Cache:  ")+valueStyle.Render(cacheStr))

	costStr := fmt.Sprintf("$%.2f total", p.summary.TotalUSD)
	lines = append(lines, labelStyle.Render("Cost:   ")+valueStyle.Render(costStr))

	lines = append(lines, "")

	lines = append(lines, headerStyle.Render("Budget"))

	barColor := "#a6e3a1"
	if p.budget.DailyPercent > 0.95 {
		barColor = "#f38ba8"
	} else if p.budget.DailyPercent > 0.80 {
		barColor = "#f9e2af"
	}

	barWidth := width - 20
	if barWidth < 5 {
		barWidth = 5
	}

	bar := formatBudgetBar(p.budget.DailyPercent, barWidth)
	pctStr := fmt.Sprintf("%.0f%%", p.budget.DailyPercent*100)
	barLine := labelStyle.Render("Daily ") + lipgloss.NewStyle().Foreground(lipgloss.Color(barColor)).Render("["+bar+"] ") + valueStyle.Render(pctStr)
	lines = append(lines, barLine)

	if p.budget.TaskLimit > 0 {
		taskStr := fmt.Sprintf("$%.2f / $%.2f", p.budget.TaskUsed, p.budget.TaskLimit)
		lines = append(lines, labelStyle.Render("Per-task: ")+valueStyle.Render(taskStr))
	}

	if p.budget.RollingUsed > 0 {
		windowHours := int(p.budget.Window.Hours())
		rollingStr := fmt.Sprintf("Rolling (%dh): $%.2f", windowHours, p.budget.RollingUsed)
		if windowHours == 0 {
			rollingStr = fmt.Sprintf("Rolling: $%.2f", p.budget.RollingUsed)
		}
		lines = append(lines, labelStyle.Render(rollingStr))
	}

	result := ""
	for _, l := range lines {
		result += l + "\n"
	}

	if len(result) > 0 && result[len(result)-1] == '\n' {
		result = result[:len(result)-1]
	}

	return result
}

func formatCostTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000.0)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1000000.0)
}

func formatBudgetBar(percent float64, width int) string {
	if width <= 0 {
		width = 10
	}
	filled := int(percent * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	bar := ""
	for i := 0; i < filled; i++ {
		bar += "█"
	}
	for i := filled; i < width; i++ {
		bar += "░"
	}
	return bar
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	ellipsis := "…"
	avail := max - len(ellipsis)
	if avail <= 0 {
		if max >= 3 {
			return ellipsis
		}
		return s[:max]
	}
	for avail > 0 && !isValidCutoff(s, avail) {
		avail--
	}
	if avail <= 0 {
		return ellipsis
	}
	return s[:avail] + ellipsis
}

func isValidCutoff(s string, i int) bool {
	if i <= 0 || i >= len(s) {
		return true
	}
	return s[i-1] < 0x80 || s[i]&0xC0 != 0xC0
}
