package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
)

func freshClock() *automation.AlarmClock {
	return automation.NewAlarmClock(func(*automation.Alarm) error { return nil })
}

func TestAlarmSet_RelativeDuration(t *testing.T) {
	clock := freshClock()
	tool := NewAlarmSetTool(clock, func() string { return "sess-1" })

	in := []byte(`{"name":"build","when":"in 5m","prompt":"check the build log"}`)
	out, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	var res alarmSetOutput
	_ = json.Unmarshal(out, &res)
	if res.ID == "" {
		t.Error("ID not returned")
	}
	if d := time.Until(res.FireAt); d < 4*time.Minute || d > 6*time.Minute {
		t.Errorf("fire-at not ~5m away: %s", d)
	}
}

func TestAlarmSet_AbsoluteRFC3339(t *testing.T) {
	clock := freshClock()
	tool := NewAlarmSetTool(clock, nil)

	want := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	in := []byte(`{"when":"` + want + `","prompt":"thing"}`)
	out, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	var res alarmSetOutput
	_ = json.Unmarshal(out, &res)
	if got := res.FireAt.UTC().Format(time.RFC3339); got != want {
		t.Errorf("FireAt: got %s want %s", got, want)
	}
}

func TestAlarmSet_BareDuration(t *testing.T) {
	clock := freshClock()
	tool := NewAlarmSetTool(clock, nil)

	in := []byte(`{"when":"30s","prompt":"poll"}`)
	if _, err := tool.Execute(context.Background(), in); err != nil {
		t.Errorf("bare duration should parse: %v", err)
	}
}

func TestAlarmSet_PlusPrefix(t *testing.T) {
	clock := freshClock()
	tool := NewAlarmSetTool(clock, nil)

	in := []byte(`{"when":"+10m","prompt":"thing"}`)
	if _, err := tool.Execute(context.Background(), in); err != nil {
		t.Errorf("+ prefix should parse: %v", err)
	}
}

func TestAlarmSet_RequiresPromptAndWhen(t *testing.T) {
	clock := freshClock()
	tool := NewAlarmSetTool(clock, nil)

	if _, err := tool.Execute(context.Background(), []byte(`{"when":"5m"}`)); err == nil {
		t.Error("missing prompt should error")
	}
	if _, err := tool.Execute(context.Background(), []byte(`{"prompt":"x"}`)); err == nil {
		t.Error("missing when should error")
	}
}

func TestAlarmSet_NegativeDurationRejected(t *testing.T) {
	clock := freshClock()
	tool := NewAlarmSetTool(clock, nil)

	if _, err := tool.Execute(context.Background(), []byte(`{"when":"-5m","prompt":"p"}`)); err == nil {
		t.Error("past time should error")
	}
}

func TestAlarmSet_InvalidFormatRejected(t *testing.T) {
	clock := freshClock()
	tool := NewAlarmSetTool(clock, nil)

	if _, err := tool.Execute(context.Background(), []byte(`{"when":"nonsense","prompt":"p"}`)); err == nil {
		t.Error("garbage when-string should error")
	}
}

func TestAlarmSet_AutoNamesFromPrompt(t *testing.T) {
	clock := freshClock()
	tool := NewAlarmSetTool(clock, nil)

	in := []byte(`{"when":"5m","prompt":"check whether the long-running build at /tmp/build.log finished"}`)
	_, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	all := clock.List()
	if len(all) != 1 {
		t.Fatalf("want 1 alarm, got %d", len(all))
	}
	if all[0].Name == "" {
		t.Error("name should auto-populate from prompt")
	}
	if !strings.Contains(all[0].Name, "check") {
		t.Errorf("name lost prompt content: %q", all[0].Name)
	}
}

func TestAlarmList_ReturnsAllStates(t *testing.T) {
	clock := freshClock()
	setTool := NewAlarmSetTool(clock, nil)
	cancelTool := NewAlarmCancelTool(clock)
	listTool := NewAlarmListTool(clock)

	// Set two alarms.
	out1, _ := setTool.Execute(context.Background(), []byte(`{"when":"1h","prompt":"first"}`))
	var first alarmSetOutput
	_ = json.Unmarshal(out1, &first)
	_, _ = setTool.Execute(context.Background(), []byte(`{"when":"2h","prompt":"second"}`))

	// Cancel one.
	cancelIn, _ := json.Marshal(alarmCancelInput{ID: first.ID})
	if _, err := cancelTool.Execute(context.Background(), cancelIn); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	out, err := listTool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var entries []alarmListEntry
	_ = json.Unmarshal(out, &entries)
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	states := map[string]int{}
	for _, e := range entries {
		states[e.State]++
	}
	if states["cancelled"] != 1 || states["pending"] != 1 {
		t.Errorf("state distribution wrong: %+v", states)
	}
}

func TestAlarmCancel_UnknownID(t *testing.T) {
	clock := freshClock()
	tool := NewAlarmCancelTool(clock)
	in, _ := json.Marshal(alarmCancelInput{ID: "does-not-exist"})
	if _, err := tool.Execute(context.Background(), in); err == nil {
		t.Error("unknown id should error")
	}
}

func TestAlarmCancel_RequiresID(t *testing.T) {
	clock := freshClock()
	tool := NewAlarmCancelTool(clock)
	if _, err := tool.Execute(context.Background(), []byte(`{}`)); err == nil {
		t.Error("missing id should error")
	}
}

func TestParseAlarmWhen_TimeFormats(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration // approximate
	}{
		{"5m", 5 * time.Minute},
		{"in 30s", 30 * time.Second},
		{"+1h", time.Hour},
		{"1h30m", 90 * time.Minute},
		{"in1m", time.Minute},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseAlarmWhen(c.in)
			if err != nil {
				t.Fatalf("parse %q: %v", c.in, err)
			}
			d := time.Until(got)
			if d < c.want-2*time.Second || d > c.want+2*time.Second {
				t.Errorf("parse %q: duration %s want ≈%s", c.in, d, c.want)
			}
		})
	}
}

func TestFirstWords_TruncatesAtWordBoundary(t *testing.T) {
	long := strings.Repeat("hello ", 20)
	got := firstWords(long, 30)
	if len(got) > 35 {
		t.Errorf("truncation overshot: %q (%d chars)", got, len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis on truncation, got %q", got)
	}
}
