package agent

import (
	"strings"
	"testing"
)

func TestLevelFromUtilization(t *testing.T) {
	cases := []struct {
		util float64
		want CavemanLevel
	}{
		{0.0, CavemanOff},
		{0.49, CavemanOff},
		{0.50, CavemanCurt},
		{0.64, CavemanCurt},
		{0.65, CavemanBlunt},
		{0.79, CavemanBlunt},
		{0.80, CavemanFull},
		{0.95, CavemanFull},
	}
	for _, tc := range cases {
		if got := LevelFromUtilization(tc.util); got != tc.want {
			t.Errorf("util=%.2f got=%v want=%v", tc.util, got, tc.want)
		}
	}
}

func TestCavemanLevel_Directive(t *testing.T) {
	if CavemanOff.Directive() != "" {
		t.Fatal("Off should be empty")
	}
	for _, l := range []CavemanLevel{CavemanCurt, CavemanBlunt, CavemanFull} {
		if l.Directive() == "" {
			t.Errorf("%v missing directive", l)
		}
	}
	if !strings.Contains(CavemanFull.Directive(), "Caveman speak") {
		t.Fatal("Full directive should mention caveman")
	}
}

func TestApplyCaveman_NilSafe(t *testing.T) {
	var a *Agent
	got := a.applyCaveman("base")
	if got != "base" {
		t.Fatalf("nil agent should return prompt unchanged, got %q", got)
	}
	a2 := &Agent{}
	if got := a2.applyCaveman("base"); got != "base" {
		t.Fatalf("agent without estimator should not mutate, got %q", got)
	}
}

func TestCavemanLevel_String(t *testing.T) {
	cases := map[CavemanLevel]string{
		CavemanOff:   "off",
		CavemanCurt:  "curt",
		CavemanBlunt: "blunt",
		CavemanFull:  "caveman",
	}
	for l, want := range cases {
		if l.String() != want {
			t.Errorf("%v string=%q want %q", l, l.String(), want)
		}
	}
}
