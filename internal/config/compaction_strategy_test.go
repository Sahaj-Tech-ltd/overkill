package config

import "testing"

func TestCompactionStrategyCompatibility(t *testing.T) {
	for _, tt := range []struct {
		strategy    string
		enabled, db bool
		want        string
		fail        bool
	}{{"", true, true, "seahorse", false}, {"auto", true, true, "seahorse", false}, {"auto", true, false, "lcm", false}, {"seahorse", true, false, "", true}, {"lcm", true, true, "lcm", false}, {"legacy", true, true, "legacy", false}, {"seahorse", false, false, "legacy", false}, {"auto", false, true, "legacy", false}, {"typo", true, true, "", true}} {
		got, err := (CompactionConfig{Strategy: tt.strategy, UseLCM: tt.enabled}).ResolveStrategy(tt.db)
		if got != tt.want || (err != nil) != tt.fail {
			t.Errorf("%#v: got=%s err=%v", tt, got, err)
		}
	}
}
