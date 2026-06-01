// Package config — profile presets for v2.0 Control.
//
// Five named one-click profiles flip a coordinated set of fields:
//
//	yolo       — vibe coder defaults. Scanners off, auto-approve all,
//	             no confirmations. The bed-coder profile.
//	default    — sane developer defaults. CommandScanner on, write
//	             confirms on destructive ops, no auto-approve.
//	paranoid   — production-repo posture. Every scanner on, MCP
//	             default-deny via mcpshield, cost cap enabled.
//	enterprise — paranoid + receipt-chain verify on boot, flight
//	             recorder always-on, telemetry forwarded to operator.
//	remote     — bridge / API-originated jobs. CommandScanner on,
//	             no auto-approve, pty_shell denied, shell/patch/git-push
//	             require explicit approval, web fetches restricted to
//	             operator-configured allowlist (empty by default).
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
	case "remote":
		applyRemote(u)
	default:
		return fmt.Errorf("config: unknown profile %q (must be yolo|default|paranoid|enterprise|remote)", profile)
	}
	u.Profile = strings.ToLower(strings.TrimSpace(profile))
	if u.Profile == "" {
		u.Profile = "yolo"
	}
	return nil
}

// AvailableProfiles is the canonical ordered list — order matches the
// Settings UI presentation (least-restrictive first).
var AvailableProfiles = []string{"yolo", "default", "paranoid", "enterprise", "remote"}

// boolPtr is the YAML-friendly way to express "explicitly set to X"
// vs "unset, use parent". Used by profiles so a partial override file
// can leave most fields nil and let the profile fill them in.
func boolPtr(b bool) *bool { return &b }

func applyYOLO(u *UserOverrides) {
	u.Basic.ConfirmWrites = boolPtr(false)
	u.Basic.AutoCompactPercent = 0.5
	u.Basic.CostCapMonthly = 0 // no cap

	u.Advanced.Scanners.Command = ScannerOnOff{Enabled: boolPtr(false)}
	u.Advanced.Scanners.Injection = ScannerOnOff{Enabled: boolPtr(false)}
	u.Advanced.Scanners.PromptInjectBrowser = ScannerOnOff{Enabled: boolPtr(false)}
	u.Advanced.Permissions.AutoApproveAll = boolPtr(true)
	u.Advanced.Permissions.SkipDestructiveConfirm = boolPtr(true)
	// Hooks default-on but the user can wire their own. We don't
	// install any sample hooks at YOLO level.
	u.Advanced.Hooks = HooksUserConfig{}
	u.Advanced.Telemetry.EventLog = boolPtr(false)
	u.Advanced.Telemetry.FlightRecorder = boolPtr(true) // crash audit only
	u.Advanced.Telemetry.VerifyOnBoot = boolPtr(false)
}

func applyDefault(u *UserOverrides) {
	u.Basic.ConfirmWrites = boolPtr(true)
	u.Basic.AutoCompactPercent = 0.5
	u.Basic.CostCapMonthly = 0

	u.Advanced.Scanners.Command = ScannerOnOff{Enabled: boolPtr(true)}
	u.Advanced.Scanners.Injection = ScannerOnOff{Enabled: boolPtr(false)}
	u.Advanced.Scanners.PromptInjectBrowser = ScannerOnOff{Enabled: boolPtr(false)}
	u.Advanced.Permissions.AutoApproveAll = boolPtr(false)
	u.Advanced.Permissions.SkipDestructiveConfirm = boolPtr(false)
	u.Advanced.Telemetry.EventLog = boolPtr(true)
	u.Advanced.Telemetry.FlightRecorder = boolPtr(true)
	u.Advanced.Telemetry.VerifyOnBoot = boolPtr(false)
}

func applyParanoid(u *UserOverrides) {
	u.Basic.ConfirmWrites = boolPtr(true)
	u.Basic.AutoCompactPercent = 0.4
	if u.Basic.CostCapMonthly == 0 {
		u.Basic.CostCapMonthly = 50 // sensible default cap; user can raise
	}

	u.Advanced.Scanners.Command = ScannerOnOff{Enabled: boolPtr(true)}
	u.Advanced.Scanners.Injection = ScannerOnOff{Enabled: boolPtr(true)}
	u.Advanced.Scanners.PromptInjectBrowser = ScannerOnOff{Enabled: boolPtr(true)}
	u.Advanced.Permissions.AutoApproveAll = boolPtr(false)
	u.Advanced.Permissions.SkipDestructiveConfirm = boolPtr(false)
	u.Advanced.Telemetry.EventLog = boolPtr(true)
	u.Advanced.Telemetry.FlightRecorder = boolPtr(true)
	u.Advanced.Telemetry.RetentionDays = 90
	u.Advanced.Telemetry.VerifyOnBoot = boolPtr(true)
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

// applyRemote configures the posture appropriate for bridge- or
// API-originated jobs that run without an interactive operator.
//
//   - CommandScanner on (same as default) to catch risky commands.
//   - AutoApprove off — remote jobs must not silently self-approve.
//   - pty_shell denied outright (no interactive terminal available).
//   - shell, patch, and git-push variants require explicit approval.
//   - Web fetches restricted to the operator-configured allowlist;
//     the list is intentionally empty by default so operators must
//     consciously open access.
func applyRemote(u *UserOverrides) {
	u.Basic.ConfirmWrites = boolPtr(true)
	u.Basic.AutoCompactPercent = 0.5
	u.Basic.CostCapMonthly = 0

	u.Advanced.Scanners.Command = ScannerOnOff{Enabled: boolPtr(true)}
	u.Advanced.Scanners.Injection = ScannerOnOff{Enabled: boolPtr(false)}
	u.Advanced.Scanners.PromptInjectBrowser = ScannerOnOff{Enabled: boolPtr(false)}
	u.Advanced.Permissions.AutoApproveAll = boolPtr(false)
	u.Advanced.Permissions.SkipDestructiveConfirm = boolPtr(false)
	u.Advanced.Permissions.DeniedTools = []string{
		"pty_shell",
	}
	u.Advanced.Permissions.RequireApprovalTools = []string{
		"shell",
		"patch",
		"git_push",
		"git-push",
	}
	// Empty by default; operators add domains they trust.
	u.Advanced.Permissions.AllowedWebDomains = []string{}
	u.Advanced.Telemetry.EventLog = boolPtr(true)
	u.Advanced.Telemetry.FlightRecorder = boolPtr(true)
	u.Advanced.Telemetry.VerifyOnBoot = boolPtr(false)
}
