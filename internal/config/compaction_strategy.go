package config

import "fmt"

// ResolveStrategy preserves use_lcm=false as the legacy kill switch. Auto uses
// durable DAG memory when a PostgreSQL pool exists, otherwise existing LCM.
// An explicit durable selection must never silently downgrade.
func (c CompactionConfig) ResolveStrategy(durable bool) (string, error) {
	switch c.Strategy {
	case "", "auto", "seahorse", "lcm", "legacy":
	default:
		return "", fmt.Errorf("config: compaction.strategy must be auto, seahorse, lcm or legacy")
	}
	if !c.UseLCM || c.Strategy == "legacy" {
		return "legacy", nil
	}
	switch c.Strategy {
	case "seahorse":
		if !durable {
			return "", fmt.Errorf("config: seahorse compaction requires durable PostgreSQL")
		}
		return "seahorse", nil
	case "lcm":
		return "lcm", nil
	default:
		if durable {
			return "seahorse", nil
		}
		return "lcm", nil
	}
}
