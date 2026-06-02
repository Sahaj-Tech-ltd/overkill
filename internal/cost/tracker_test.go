package cost

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/stretchr/testify/suite"
)

type CostTestSuite struct {
	suite.Suite
	tracker *PostgresTracker
}

func (s *CostTestSuite) SetupTest() {
	db := openCostDB(s.T())
	cfg := config.CostConfig{
		DailyLimitUSD:    10.0,
		PerTaskLimitUSD:  5.0,
		RollingWindowHrs: 5,
		WarnAtPercent:    80,
	}
	tracker, err := NewPostgresTracker(db, cfg)
	s.Require().NoError(err)
	s.tracker = tracker
}

func (s *CostTestSuite) TearDownTest() {
	s.tracker.Close()
}

func TestCostTestSuite(t *testing.T) {
	suite.Run(t, new(CostTestSuite))
}

func (s *CostTestSuite) TestRecord_BasicEntry() {
	ctx := context.Background()
	entry := Entry{
		ID:           "entry-1",
		SessionID:    "sess-1",
		Model:        "gpt-4o",
		Provider:     "openai",
		Timestamp:    time.Now(),
		InputTokens:  100,
		OutputTokens: 50,
		CachedTokens: 20,
		CostUSD:      0.005,
	}
	err := s.tracker.Record(ctx, entry)
	s.Require().NoError(err)

	summary, err := s.tracker.SessionCost(ctx, "sess-1")
	s.Require().NoError(err)
	s.InDelta(0.005, summary.TotalUSD, 0.0001)
	s.Equal(int64(100), summary.InputTokens)
	s.Equal(int64(50), summary.OutputTokens)
	s.Equal(int64(20), summary.CachedTokens)
	s.Equal(int64(1), summary.RequestCount)
}

func (s *CostTestSuite) TestRecord_AutoCalculateCost() {
	model := providers.Model{
		ID:          "gpt-4o",
		Name:        "GPT-4o",
		CostIn:      5.0,
		CostOut:     15.0,
		CostCacheIn: 2.5,
	}
	s.tracker.RegisterModel(model)

	entry := Entry{
		ID:           "entry-auto",
		SessionID:    "sess-auto",
		Model:        "gpt-4o",
		Provider:     "openai",
		Timestamp:    time.Now(),
		InputTokens:  1000,
		OutputTokens: 500,
		CachedTokens: 200,
		CostUSD:      0,
	}
	err := s.tracker.Record(ctx(), entry)
	s.Require().NoError(err)

	report, err := s.tracker.Usage(ctx(), UsageOptions{})
	s.Require().NoError(err)
	s.Require().Len(report.Entries, 1)

	expectedCost := CalculateCost(providers.Usage{
		InputTokens:       1000,
		OutputTokens:      500,
		CachedInputTokens: 200,
	}, model)
	s.InDelta(expectedCost, report.Entries[0].CostUSD, 0.000001)
}

func (s *CostTestSuite) TestSessionCost() {
	ctx := context.Background()
	now := time.Now()

	entries := []Entry{
		{ID: "s1", SessionID: "sess-a", Model: "gpt-4o", Provider: "openai", Timestamp: now, CostUSD: 0.01},
		{ID: "s2", SessionID: "sess-a", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(1 * time.Second), CostUSD: 0.02},
		{ID: "s3", SessionID: "sess-a", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(2 * time.Second), CostUSD: 0.03},
	}
	for _, e := range entries {
		s.Require().NoError(s.tracker.Record(ctx, e))
	}

	summary, err := s.tracker.SessionCost(ctx, "sess-a")
	s.Require().NoError(err)
	s.InDelta(0.06, summary.TotalUSD, 0.0001)
	s.Equal(int64(3), summary.RequestCount)
}

func (s *CostTestSuite) TestSessionCost_Empty() {
	ctx := context.Background()

	summary, err := s.tracker.SessionCost(ctx, "nonexistent-session")
	s.Require().NoError(err)
	s.InDelta(0.0, summary.TotalUSD, 0.0001)
	s.Equal(int64(0), summary.RequestCount)
}

func (s *CostTestSuite) TestDailyCost() {
	ctx := context.Background()
	now := time.Now()

	entries := []Entry{
		{ID: "d1", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: now, CostUSD: 0.01},
		{ID: "d2", SessionID: "sess-2", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(1 * time.Second), CostUSD: 0.02},
		{ID: "d3", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(2 * time.Second), CostUSD: 0.03},
	}
	for _, e := range entries {
		s.Require().NoError(s.tracker.Record(ctx, e))
	}

	summary, err := s.tracker.DailyCost(ctx)
	s.Require().NoError(err)
	s.InDelta(0.06, summary.TotalUSD, 0.0001)
	s.Equal(int64(3), summary.RequestCount)
}

func (s *CostTestSuite) TestDailyCost_MultiDay() {
	ctx := context.Background()
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)

	entries := []Entry{
		{ID: "md1", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: now, CostUSD: 0.05},
		{ID: "md2", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: yesterday, CostUSD: 0.10},
	}
	for _, e := range entries {
		s.Require().NoError(s.tracker.Record(ctx, e))
	}

	summary, err := s.tracker.DailyCost(ctx)
	s.Require().NoError(err)
	s.InDelta(0.05, summary.TotalUSD, 0.0001)
	s.Equal(int64(1), summary.RequestCount)
}

func (s *CostTestSuite) TestRollingCost_5Hours() {
	ctx := context.Background()
	now := time.Now()

	entries := []Entry{
		{ID: "r1", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(-6 * time.Hour), CostUSD: 0.10},
		{ID: "r2", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(-4 * time.Hour), CostUSD: 0.01},
		{ID: "r3", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(-3 * time.Hour), CostUSD: 0.02},
		{ID: "r4", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(-2 * time.Hour), CostUSD: 0.03},
		{ID: "r5", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(-1 * time.Hour), CostUSD: 0.04},
	}
	for _, e := range entries {
		s.Require().NoError(s.tracker.Record(ctx, e))
	}

	summary, err := s.tracker.RollingCost(ctx, 5*time.Hour)
	s.Require().NoError(err)
	s.InDelta(0.10, summary.TotalUSD, 0.0001)
	s.Equal(int64(4), summary.RequestCount)
}

func (s *CostTestSuite) TestCheckBudget_UnderLimit() {
	ctx := context.Background()

	entries := []Entry{
		{ID: "b1", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: time.Now(), CostUSD: 1.0},
	}
	for _, e := range entries {
		s.Require().NoError(s.tracker.Record(ctx, e))
	}

	status, err := s.tracker.CheckBudget(ctx, "sess-1")
	s.Require().NoError(err)
	s.False(status.ShouldAbort)
	s.False(status.ShouldWarn)
	s.InDelta(1.0, status.DailyUsed, 0.0001)
	s.InDelta(1.0, status.TaskUsed, 0.0001)
}

func (s *CostTestSuite) TestCheckBudget_OverDailyLimit() {
	ctx := context.Background()

	entries := []Entry{
		{ID: "bd1", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: time.Now(), CostUSD: 6.0},
		{ID: "bd2", SessionID: "sess-2", Model: "gpt-4o", Provider: "openai", Timestamp: time.Now().Add(1 * time.Second), CostUSD: 5.0},
	}
	for _, e := range entries {
		s.Require().NoError(s.tracker.Record(ctx, e))
	}

	status, err := s.tracker.CheckBudget(ctx, "sess-1")
	s.Require().NoError(err)
	s.True(status.ShouldAbort)
	s.InDelta(11.0, status.DailyUsed, 0.0001)
	s.InDelta(10.0, status.DailyLimit, 0.0001)
}

func (s *CostTestSuite) TestCheckBudget_OverTaskLimit() {
	ctx := context.Background()

	entries := []Entry{
		{ID: "bt1", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: time.Now(), CostUSD: 3.0},
		{ID: "bt2", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: time.Now().Add(1 * time.Second), CostUSD: 3.0},
	}
	for _, e := range entries {
		s.Require().NoError(s.tracker.Record(ctx, e))
	}

	status, err := s.tracker.CheckBudget(ctx, "sess-1")
	s.Require().NoError(err)
	s.True(status.ShouldAbort)
	s.InDelta(6.0, status.TaskUsed, 0.0001)
	s.InDelta(5.0, status.TaskLimit, 0.0001)
}

func (s *CostTestSuite) TestCheckBudget_WarnThreshold() {
	ctx := context.Background()

	entries := []Entry{
		{ID: "bw1", SessionID: "sess-1", Model: "gpt-4o", Provider: "openai", Timestamp: time.Now(), CostUSD: 4.30},
	}
	for _, e := range entries {
		s.Require().NoError(s.tracker.Record(ctx, e))
	}

	status, err := s.tracker.CheckBudget(ctx, "sess-1")
	s.Require().NoError(err)
	s.True(status.ShouldWarn)
	s.False(status.ShouldAbort)
	s.InDelta(86.0, status.TaskPercent, 0.1)
}

func (s *CostTestSuite) TestUsage_ByModel() {
	ctx := context.Background()
	now := time.Now()

	entries := []Entry{
		{ID: "um1", SessionID: "s1", Model: "gpt-4o", Provider: "openai", Timestamp: now, InputTokens: 100, OutputTokens: 50, CostUSD: 0.01},
		{ID: "um2", SessionID: "s1", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(1 * time.Second), InputTokens: 200, OutputTokens: 100, CostUSD: 0.02},
		{ID: "um3", SessionID: "s1", Model: "claude-3-opus", Provider: "anthropic", Timestamp: now.Add(2 * time.Second), InputTokens: 300, OutputTokens: 150, CostUSD: 0.05},
	}
	for _, e := range entries {
		s.Require().NoError(s.tracker.Record(ctx, e))
	}

	report, err := s.tracker.Usage(ctx, UsageOptions{})
	s.Require().NoError(err)

	s.InDelta(0.08, report.Summary.TotalUSD, 0.0001)
	s.Equal(int64(3), report.Summary.RequestCount)

	gptSummary, ok := report.ByModel["gpt-4o"]
	s.True(ok)
	s.InDelta(0.03, gptSummary.TotalUSD, 0.0001)
	s.Equal(int64(2), gptSummary.RequestCount)
	s.Equal(int64(300), gptSummary.InputTokens)
	s.Equal(int64(150), gptSummary.OutputTokens)

	claudeSummary, ok := report.ByModel["claude-3-opus"]
	s.True(ok)
	s.InDelta(0.05, claudeSummary.TotalUSD, 0.0001)
	s.Equal(int64(1), claudeSummary.RequestCount)
}

func (s *CostTestSuite) TestUsage_ByProvider() {
	ctx := context.Background()
	now := time.Now()

	entries := []Entry{
		{ID: "up1", SessionID: "s1", Model: "gpt-4o", Provider: "openai", Timestamp: now, CostUSD: 0.01},
		{ID: "up2", SessionID: "s1", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(1 * time.Second), CostUSD: 0.02},
		{ID: "up3", SessionID: "s1", Model: "claude-3-opus", Provider: "anthropic", Timestamp: now.Add(2 * time.Second), CostUSD: 0.05},
	}
	for _, e := range entries {
		s.Require().NoError(s.tracker.Record(ctx, e))
	}

	report, err := s.tracker.Usage(ctx, UsageOptions{})
	s.Require().NoError(err)

	openaiSummary, ok := report.ByProvider["openai"]
	s.True(ok)
	s.InDelta(0.03, openaiSummary.TotalUSD, 0.0001)
	s.Equal(int64(2), openaiSummary.RequestCount)

	anthropicSummary, ok := report.ByProvider["anthropic"]
	s.True(ok)
	s.InDelta(0.05, anthropicSummary.TotalUSD, 0.0001)
	s.Equal(int64(1), anthropicSummary.RequestCount)
}

func (s *CostTestSuite) TestUsage_DateFilter() {
	ctx := context.Background()
	now := time.Now()
	twoDaysAgo := now.AddDate(0, 0, -2)
	oneDayAgo := now.AddDate(0, 0, -1)

	entries := []Entry{
		{ID: "df1", SessionID: "s1", Model: "gpt-4o", Provider: "openai", Timestamp: twoDaysAgo, CostUSD: 0.10},
		{ID: "df2", SessionID: "s1", Model: "gpt-4o", Provider: "openai", Timestamp: oneDayAgo, CostUSD: 0.20},
		{ID: "df3", SessionID: "s1", Model: "gpt-4o", Provider: "openai", Timestamp: now, CostUSD: 0.30},
	}
	for _, e := range entries {
		s.Require().NoError(s.tracker.Record(ctx, e))
	}

	report, err := s.tracker.Usage(ctx, UsageOptions{
		StartTime: oneDayAgo,
		EndTime:   now.Add(1 * time.Second),
	})
	s.Require().NoError(err)

	s.Len(report.Entries, 2)
	s.InDelta(0.50, report.Summary.TotalUSD, 0.0001)
}

func (s *CostTestSuite) TestUsage_SessionFilter() {
	ctx := context.Background()
	now := time.Now()

	entries := []Entry{
		{ID: "sf1", SessionID: "sess-a", Model: "gpt-4o", Provider: "openai", Timestamp: now, CostUSD: 0.01},
		{ID: "sf2", SessionID: "sess-b", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(1 * time.Second), CostUSD: 0.02},
		{ID: "sf3", SessionID: "sess-a", Model: "gpt-4o", Provider: "openai", Timestamp: now.Add(2 * time.Second), CostUSD: 0.03},
	}
	for _, e := range entries {
		s.Require().NoError(s.tracker.Record(ctx, e))
	}

	report, err := s.tracker.Usage(ctx, UsageOptions{SessionID: "sess-a"})
	s.Require().NoError(err)

	s.Len(report.Entries, 2)
	s.InDelta(0.04, report.Summary.TotalUSD, 0.0001)
}

func (s *CostTestSuite) TestUsage_Limit() {
	ctx := context.Background()
	now := time.Now()

	for i := range 10 {
		s.Require().NoError(s.tracker.Record(ctx, Entry{
			ID:        "lim" + string(rune('0'+i)),
			SessionID: "s1",
			Model:     "gpt-4o",
			Provider:  "openai",
			Timestamp: now.Add(time.Duration(i) * time.Second),
			CostUSD:   0.01,
		}))
	}

	report, err := s.tracker.Usage(ctx, UsageOptions{Limit: 3})
	s.Require().NoError(err)

	s.Len(report.Entries, 3)
	s.InDelta(0.10, report.Summary.TotalUSD, 0.0001)
	s.Equal(int64(10), report.Summary.RequestCount)
}

func TestCalculateCost_AllFields(t *testing.T) {
	usage := providers.Usage{
		InputTokens:       1000,
		OutputTokens:      500,
		CachedInputTokens: 200,
	}
	model := providers.Model{
		CostIn:      5.0,
		CostOut:     15.0,
		CostCacheIn: 2.5,
	}

	cost := CalculateCost(usage, model)

	expectedInput := 1000.0 * 5.0 / 1_000_000
	expectedOutput := 500.0 * 15.0 / 1_000_000
	expectedCache := 200.0 * 2.5 / 1_000_000
	expected := expectedInput + expectedOutput + expectedCache

	if math.Abs(cost-expected) > 0.000001 {
		t.Errorf("expected %f, got %f", expected, cost)
	}
}

func TestCalculateCost_Detailed(t *testing.T) {
	usage := providers.Usage{
		InputTokens:       1000,
		OutputTokens:      500,
		CachedInputTokens: 200,
	}
	model := providers.Model{
		CostIn:      5.0,
		CostOut:     15.0,
		CostCacheIn: 2.5,
	}

	bd := CalculateCostDetailed(usage, model)

	expectedInput := 1000.0 * 5.0 / 1_000_000
	expectedOutput := 500.0 * 15.0 / 1_000_000
	expectedCache := 200.0 * 2.5 / 1_000_000

	if math.Abs(bd.InputCost-expectedInput) > 0.000001 {
		t.Errorf("input cost: expected %f, got %f", expectedInput, bd.InputCost)
	}
	if math.Abs(bd.OutputCost-expectedOutput) > 0.000001 {
		t.Errorf("output cost: expected %f, got %f", expectedOutput, bd.OutputCost)
	}
	if math.Abs(bd.CacheCost-expectedCache) > 0.000001 {
		t.Errorf("cache cost: expected %f, got %f", expectedCache, bd.CacheCost)
	}
	if math.Abs(bd.Total-(expectedInput+expectedOutput+expectedCache)) > 0.000001 {
		t.Errorf("total: expected %f, got %f", expectedInput+expectedOutput+expectedCache, bd.Total)
	}
}

func TestCalculateCost_NoCache(t *testing.T) {
	usage := providers.Usage{
		InputTokens:  1000,
		OutputTokens: 500,
	}
	model := providers.Model{
		CostIn:      5.0,
		CostOut:     15.0,
		CostCacheIn: 2.5,
	}

	bd := CalculateCostDetailed(usage, model)

	if bd.CacheCost != 0 {
		t.Errorf("cache cost should be 0, got %f", bd.CacheCost)
	}
}

func TestCalculateCost_ZeroTokens(t *testing.T) {
	usage := providers.Usage{}
	model := providers.Model{
		CostIn:      5.0,
		CostOut:     15.0,
		CostCacheIn: 2.5,
	}

	cost := CalculateCost(usage, model)

	if cost != 0 {
		t.Errorf("expected 0 cost for zero tokens, got %f", cost)
	}
}

func ctx() context.Context {
	return context.Background()
}
