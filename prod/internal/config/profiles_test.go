package config

import (
	"slices"
	"testing"
)

func TestApplyProfile_remote_fields(t *testing.T) {
	u := &UserOverrides{}
	if err := ApplyProfile(u, "remote"); err != nil {
		t.Fatalf("ApplyProfile(remote) unexpected error: %v", err)
	}

	t.Run("profile name stored", func(t *testing.T) {
		if u.Profile != "remote" {
			t.Errorf("Profile = %q, want %q", u.Profile, "remote")
		}
	})

	t.Run("confirm writes enabled", func(t *testing.T) {
		if u.Basic.ConfirmWrites == nil || !*u.Basic.ConfirmWrites {
			t.Error("ConfirmWrites = false, want true")
		}
	})

	t.Run("command scanner on", func(t *testing.T) {
		if u.Advanced.Scanners.Command.Enabled == nil || !*u.Advanced.Scanners.Command.Enabled {
			t.Error("Scanners.Command.Enabled = false, want true")
		}
	})

	t.Run("injection scanner off", func(t *testing.T) {
		if u.Advanced.Scanners.Injection.Enabled != nil && *u.Advanced.Scanners.Injection.Enabled {
			t.Error("Scanners.Injection.Enabled = true, want false")
		}
	})

	t.Run("auto approve false", func(t *testing.T) {
		if u.Advanced.Permissions.AutoApproveAll == nil {
			t.Fatal("AutoApproveAll is nil")
		}
		if *u.Advanced.Permissions.AutoApproveAll {
			t.Error("AutoApproveAll = true, want false")
		}
	})

	t.Run("skip destructive confirm false", func(t *testing.T) {
		if u.Advanced.Permissions.SkipDestructiveConfirm == nil {
			t.Fatal("SkipDestructiveConfirm is nil")
		}
		if *u.Advanced.Permissions.SkipDestructiveConfirm {
			t.Error("SkipDestructiveConfirm = true, want false")
		}
	})

	t.Run("pty_shell denied", func(t *testing.T) {
		if !slices.Contains(u.Advanced.Permissions.DeniedTools, "pty_shell") {
			t.Errorf("DeniedTools %v does not contain pty_shell", u.Advanced.Permissions.DeniedTools)
		}
	})

	t.Run("shell requires approval", func(t *testing.T) {
		if !slices.Contains(u.Advanced.Permissions.RequireApprovalTools, "shell") {
			t.Errorf("RequireApprovalTools %v does not contain shell", u.Advanced.Permissions.RequireApprovalTools)
		}
	})

	t.Run("patch requires approval", func(t *testing.T) {
		if !slices.Contains(u.Advanced.Permissions.RequireApprovalTools, "patch") {
			t.Errorf("RequireApprovalTools %v does not contain patch", u.Advanced.Permissions.RequireApprovalTools)
		}
	})

	t.Run("git-push variant requires approval", func(t *testing.T) {
		tools := u.Advanced.Permissions.RequireApprovalTools
		hasGitPush := slices.Contains(tools, "git_push") || slices.Contains(tools, "git-push")
		if !hasGitPush {
			t.Errorf("RequireApprovalTools %v contains no git-push variant", tools)
		}
	})

	t.Run("web allowlist exists and empty by default", func(t *testing.T) {
		if u.Advanced.Permissions.AllowedWebDomains == nil {
			t.Error("AllowedWebDomains is nil, want empty slice")
		}
		if len(u.Advanced.Permissions.AllowedWebDomains) != 0 {
			t.Errorf("AllowedWebDomains = %v, want empty", u.Advanced.Permissions.AllowedWebDomains)
		}
	})

	t.Run("event log on", func(t *testing.T) {
		if u.Advanced.Telemetry.EventLog == nil || !*u.Advanced.Telemetry.EventLog {
			t.Error("EventLog should be true")
		}
	})

	t.Run("flight recorder on", func(t *testing.T) {
		if u.Advanced.Telemetry.FlightRecorder == nil || !*u.Advanced.Telemetry.FlightRecorder {
			t.Error("FlightRecorder should be true")
		}
	})
}

func TestAvailableProfiles_contains_remote(t *testing.T) {
	if !slices.Contains(AvailableProfiles, "remote") {
		t.Errorf("AvailableProfiles %v does not contain remote", AvailableProfiles)
	}
}

func TestApplyProfile_unknown_errors(t *testing.T) {
	u := &UserOverrides{}
	err := ApplyProfile(u, "nonexistent")
	if err == nil {
		t.Error("expected error for unknown profile, got nil")
	}
}

func TestApplyProfile_nil_errors(t *testing.T) {
	err := ApplyProfile(nil, "remote")
	if err == nil {
		t.Error("expected error for nil UserOverrides, got nil")
	}
}

func TestApplyProfile_all_known_profiles_succeed(t *testing.T) {
	for _, name := range AvailableProfiles {
		t.Run(name, func(t *testing.T) {
			u := &UserOverrides{}
			if err := ApplyProfile(u, name); err != nil {
				t.Errorf("ApplyProfile(%q) = %v, want nil", name, err)
			}
			if u.Profile != name && !(name == "yolo" && u.Profile == "yolo") {
				t.Errorf("Profile = %q, want %q", u.Profile, name)
			}
		})
	}
}
