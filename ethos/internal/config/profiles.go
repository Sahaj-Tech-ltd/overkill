// Package config — profile presets for v2.0 Control.
//
// Four named one-click profiles flip a coordinated set of fields:
//
//   yolo       — vibe coder defaults. Scanners off, auto-approve all,
//                no confirmations. The bed-coder profile.
//   default    — sane developer defaults. CommandScanner on, write
//                confirms on destructive ops, no auto-approve.
//   paranoid   — production-repo posture. Every scanner on, MCP
//                default-deny via mcpshield, cost cap enabled.
//   enterprise — paranoid + receipt-chain verify on boot, flight
//                recorder always-on, telemetry forwarded to operator.
//
// Switching profiles rewrites the affected fields in-memory. Users in
// the Settings → Advanced view can still flip individual switches
// after a profile is applied; the picker UI shows "modified" so it's
// clear the active state has drifted from the named profile.
package config

import (
	"fmt"
	"strings"
)

// ApplyProfile mutates u to match the named profile. Returns an error
// on an unknown profile name; an empty profile is treated as "yolo"
// (the documented default).
func ApplyProfile(u *UserOverrides, profile string) error {
	if u == nil {
		return fmt.Errorf("config: nil user overrides")
	}
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", "yolo":
		applyYOLO(u)
	case "default":
		applyDefault(u)
	case "paranoid":
		applyParanoid(u)
	case "enterprise":
		applyEnterprise(u)
	default:
		return fmt.Errorf("config: unknown profile %q (must be yolo|default|paranoid|enterprise)", profile)
	}
	u.Profile = strings.ToLower(strings.TrimSpace(profile))
	if u.Profile == "" {
		u.Profile = "yolo"
	}
	return nil
}

// AvailableProfiles is the canonical ordered list — order matches the
// Settings UI presentation (least-restrictive first).
var AvailableProfiles = []string{"yolo", "default", "paranoid", "enterprise"}

// boolPtr is the YAML-friendly way to express "explicitly set to X"
// vs "unset, use parent". Used by profiles so a partial override file
// can leave most fields nil and let the profile fill them in.
func boolPtr(b bool) *bool { return &b }

func applyYOLO(u *UserOverrides) {
	u.Basic.ConfirmWrites = false
	u.Basic.AutoCompactPercent = 0.5
	u.Basic.CostCapMonthly = 0 // no cap

	u.Advanced.Scanners = ScannerToggles{
		Command:             ScannerOnOff{Enabled: false},
		Injection:           ScannerOnOff{Enabled: false},
		PromptInjectBrowser: ScannerOnOff{Enabled: false},
	}
	u.Advanced.Permissions = PermissionsUserConfig{
		AutoApproveAll:         boolPtr(true),
		SkipDestructiveConfirm: boolPtr(true),
	}
	// Hooks default-on but the user can wire their own. We don't
	// install any sample hooks at YOLO level.
	u.Advanced.Hooks = HooksUserConfig{}
	u.Advanced.Telemetry = TelemetryUserConfig{
		EventLog:       boolPtr(false),
		FlightRecorder: boolPtr(true), // crash audit only
		VerifyOnBoot:   boolPtr(false),
	}
}

func applyDefault(u *UserOverrides) {
	u.Basic.ConfirmWrites = true
	u.Basic.AutoCompactPercent = 0.5
	u.Basic.CostCapMonthly = 0

	u.Advanced.Scanners = ScannerToggles{
		Command:             ScannerOnOff{Enabled: true},
		Injection:           ScannerOnOff{Enabled: false},
		PromptInjectBrowser: ScannerOnOff{Enabled: false},
	}
	u.Advanced.Permissions = PermissionsUserConfig{
		AutoApproveAll:         boolPtr(false),
		SkipDestructiveConfirm: boolPtr(false),
	}
	u.Advanced.Telemetry = TelemetryUserConfig{
		EventLog:       boolPtr(true),
		FlightRecorder: boolPtr(true),
		VerifyOnBoot:   boolPtr(false),
	}
}

func applyParanoid(u *UserOverrides) {
	u.Basic.ConfirmWrites = true
	u.Basic.AutoCompactPercent = 0.4
	if u.Basic.CostCapMonthly == 0 {
		u.Basic.CostCapMonthly = 50 // sensible default cap; user can raise
	}

	u.Advanced.Scanners = ScannerToggles{
		Command:             ScannerOnOff{Enabled: true},
		Injection:           ScannerOnOff{Enabled: true},
		PromptInjectBrowser: ScannerOnOff{Enabled: true},
	}
	u.Advanced.Permissions = PermissionsUserConfig{
		AutoApproveAll:         boolPtr(false),
		SkipDestructiveConfirm: boolPtr(false),
	}
	u.Advanced.Telemetry = TelemetryUserConfig{
		EventLog:       boolPtr(true),
		FlightRecorder: boolPtr(true),
		RetentionDays:  90,
		VerifyOnBoot:   boolPtr(true),
	}
	// MCP servers default-deny (no Trusted flag → mcpshield enforces).
	// Per-server allowlists must be added explicitly.
	for i := range u.Advanced.MCPServers {
		if u.Advanced.MCPServers[i].Trusted == nil {
			u.Advanced.MCPServers[i].Trusted = boolPtr(false)
		}
	}
}

func applyEnterprise(u *UserOverrides) {
	applyParanoid(u)
	// Enterprise layers on always-on auditability + lower thresholds.
	u.Advanced.Telemetry.VerifyOnBoot = boolPtr(true)
	u.Advanced.Telemetry.RetentionDays = 180
	u.Advanced.Memory.Enabled = boolPtr(true)
	// Slightly tighter compact thresholds so long sessions don't
	// linger near the context cap.
	if u.Advanced.Compaction.SoftThreshold == 0 {
		u.Advanced.Compaction.SoftThreshold = 0.35
	}
	if u.Advanced.Compaction.HardThreshold == 0 {
		u.Advanced.Compaction.HardThreshold = 0.85
	}
}
