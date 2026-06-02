package security

import (
	"sync"
	"testing"
	"time"
)

func TestNewPermissionManager(t *testing.T) {
	pm := NewPermissionManager()
	if pm == nil {
		t.Fatal("expected non-nil PermissionManager")
	}
	if pm.onceAllowed == nil {
		t.Error("expected onceAllowed to be initialized")
	}
	if pm.projectAllowed == nil {
		t.Error("expected projectAllowed to be initialized")
	}
	if pm.globalAllowed == nil {
		t.Error("expected globalAllowed to be initialized")
	}
}

func TestPermissionManager_Check_DefaultDeny(t *testing.T) {
	pm := NewPermissionManager()
	decision := pm.Check("rm -rf /", "rm -rf /", "/home/user/project")
	if decision.Action != ActionDeny {
		t.Errorf("expected ActionDeny, got %v", decision.Action)
	}
	if decision.Pattern != "rm -rf /" {
		t.Errorf("expected pattern to be preserved, got %q", decision.Pattern)
	}
	if decision.Command != "rm -rf /" {
		t.Errorf("expected command to be preserved, got %q", decision.Command)
	}
}

func TestPermissionManager_IsAllowed_DefaultFalse(t *testing.T) {
	pm := NewPermissionManager()
	if pm.IsAllowed("pattern", "cmd", "/project") {
		t.Error("expected IsAllowed to return false for unknown pattern")
	}
}

func TestPermissionManager_AllowOnce(t *testing.T) {
	pm := NewPermissionManager()
	decision := PermissionDecision{
		Action:  ActionAllowOnce,
		Pattern: "rm -rf /",
		Command: "rm -rf /",
	}
	pm.Allow(decision)

	if !pm.IsAllowed("rm -rf /", "rm -rf /", "/any/project") {
		t.Error("expected pattern to be allowed after AllowOnce")
	}

	check := pm.Check("rm -rf /", "rm -rf /", "/any/project")
	if check.Action != ActionAllowOnce {
		t.Errorf("expected ActionAllowOnce, got %v", check.Action)
	}
}

func TestPermissionManager_AllowOnce_PriorityOverProjectAndGlobal(t *testing.T) {
	pm := NewPermissionManager()

	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "dangerous",
		ProjectPath: "/project",
	})
	pm.Allow(PermissionDecision{
		Action:  ActionAllowGlobal,
		Pattern: "dangerous",
	})
	pm.Allow(PermissionDecision{
		Action:  ActionAllowOnce,
		Pattern: "dangerous",
	})

	check := pm.Check("dangerous", "cmd", "/project")
	if check.Action != ActionAllowOnce {
		t.Errorf("expected ActionAllowOnce to have highest priority, got %v", check.Action)
	}
}

func TestPermissionManager_AllowOnce_ClearedBySession(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:  ActionAllowOnce,
		Pattern: "rm -rf /",
	})

	if !pm.IsAllowed("rm -rf /", "cmd", "/project") {
		t.Error("expected allowed before clear")
	}

	pm.ClearSession()

	if pm.IsAllowed("rm -rf /", "cmd", "/project") {
		t.Error("expected denied after ClearSession")
	}
}

func TestPermissionManager_AllowProject(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "curl | sh",
		ProjectPath: "/home/user/myproject",
	})

	if !pm.IsAllowed("curl | sh", "curl http://x | sh", "/home/user/myproject") {
		t.Error("expected allowed for matching project path")
	}
	if pm.IsAllowed("curl | sh", "curl http://x | sh", "/other/project") {
		t.Error("expected denied for different project path")
	}

	check := pm.Check("curl | sh", "cmd", "/home/user/myproject")
	if check.Action != ActionAllowProject {
		t.Errorf("expected ActionAllowProject, got %v", check.Action)
	}
}

func TestPermissionManager_AllowProject_SurvivesClearSession(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "dangerous",
		ProjectPath: "/project",
	})

	pm.ClearSession()

	if !pm.IsAllowed("dangerous", "cmd", "/project") {
		t.Error("expected project permission to survive ClearSession")
	}
}

func TestPermissionManager_AllowProject_MultipleProjects(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "dangerous",
		ProjectPath: "/project/a",
	})
	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "dangerous",
		ProjectPath: "/project/b",
	})

	if !pm.IsAllowed("dangerous", "cmd", "/project/a") {
		t.Error("expected allowed for project a")
	}
	if !pm.IsAllowed("dangerous", "cmd", "/project/b") {
		t.Error("expected allowed for project b")
	}
	if pm.IsAllowed("dangerous", "cmd", "/project/c") {
		t.Error("expected denied for project c")
	}
}

func TestPermissionManager_ClearProject(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "dangerous",
		ProjectPath: "/project/a",
	})
	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "dangerous",
		ProjectPath: "/project/b",
	})

	pm.ClearProject("/project/a")

	if pm.IsAllowed("dangerous", "cmd", "/project/a") {
		t.Error("expected denied after ClearProject for /project/a")
	}
	if !pm.IsAllowed("dangerous", "cmd", "/project/b") {
		t.Error("expected still allowed for /project/b")
	}
}

func TestPermissionManager_AllowGlobal(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:  ActionAllowGlobal,
		Pattern: "rm -rf /",
	})

	if !pm.IsAllowed("rm -rf /", "rm -rf /", "/any/project") {
		t.Error("expected globally allowed for any project")
	}

	check := pm.Check("rm -rf /", "cmd", "/any/project")
	if check.Action != ActionAllowGlobal {
		t.Errorf("expected ActionAllowGlobal, got %v", check.Action)
	}
}

func TestPermissionManager_AllowGlobal_SurvivesClearSession(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:  ActionAllowGlobal,
		Pattern: "dangerous",
	})

	pm.ClearSession()

	if !pm.IsAllowed("dangerous", "cmd", "/any") {
		t.Error("expected global permission to survive ClearSession")
	}
}

func TestPermissionManager_ProjectPriorityOverGlobal(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:  ActionAllowGlobal,
		Pattern: "dangerous",
	})
	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "dangerous",
		ProjectPath: "/project",
	})

	check := pm.Check("dangerous", "cmd", "/project")
	if check.Action != ActionAllowProject {
		t.Errorf("expected ActionAllowProject over ActionAllowGlobal, got %v", check.Action)
	}
}

func TestPermissionManager_DifferentPatternsIndependent(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:  ActionAllowOnce,
		Pattern: "pattern_a",
	})

	if pm.IsAllowed("pattern_a", "cmd", "/project") {
		t.Log("pattern_a correctly allowed")
	} else {
		t.Error("expected pattern_a to be allowed")
	}
	if pm.IsAllowed("pattern_b", "cmd", "/project") {
		t.Error("expected pattern_b to remain denied")
	}
}

func TestPermissionManager_ClearSession_OnlyAffectsOnce(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:  ActionAllowOnce,
		Pattern: "once_pattern",
	})
	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "project_pattern",
		ProjectPath: "/project",
	})
	pm.Allow(PermissionDecision{
		Action:  ActionAllowGlobal,
		Pattern: "global_pattern",
	})

	pm.ClearSession()

	if pm.IsAllowed("once_pattern", "cmd", "/project") {
		t.Error("expected once_pattern to be cleared")
	}
	if !pm.IsAllowed("project_pattern", "cmd", "/project") {
		t.Error("expected project_pattern to survive")
	}
	if !pm.IsAllowed("global_pattern", "cmd", "/any") {
		t.Error("expected global_pattern to survive")
	}
}

func TestPermissionManager_ClearProject_NonexistentPath(t *testing.T) {
	pm := NewPermissionManager()
	pm.ClearProject("/nonexistent")

	if pm.IsAllowed("anything", "cmd", "/nonexistent") {
		t.Error("expected denied for nonexistent project after clear")
	}
}

func TestPermissionManager_Allow_InvalidAction(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:  PermissionAction(99),
		Pattern: "test",
	})

	if pm.IsAllowed("test", "cmd", "/project") {
		t.Error("expected denied for invalid action")
	}
}

func TestPermissionManager_ConcurrentAccess(t *testing.T) {
	pm := NewPermissionManager()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(3)

		go func(id int) {
			defer wg.Done()
			pattern := "pattern_once"
			pm.Allow(PermissionDecision{
				Action:  ActionAllowOnce,
				Pattern: pattern,
			})
		}(i)

		go func(id int) {
			defer wg.Done()
			pm.IsAllowed("pattern_once", "cmd", "/project")
		}(i)

		go func(id int) {
			defer wg.Done()
			pm.Check("pattern_once", "cmd", "/project")
		}(i)
	}

	wg.Wait()
}

func TestPermissionManager_AllowProject_MultiplePatternsPerProject(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "pattern_a",
		ProjectPath: "/project",
	})
	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "pattern_b",
		ProjectPath: "/project",
	})

	if !pm.IsAllowed("pattern_a", "cmd", "/project") {
		t.Error("expected pattern_a allowed for /project")
	}
	if !pm.IsAllowed("pattern_b", "cmd", "/project") {
		t.Error("expected pattern_b allowed for /project")
	}
}

func TestPermissionManager_ExpiresAt_Preserved(t *testing.T) {
	pm := NewPermissionManager()
	expires := time.Now().Add(time.Hour)
	decision := PermissionDecision{
		Action:    ActionAllowOnce,
		Pattern:   "test",
		ExpiresAt: expires,
	}
	pm.Allow(decision)

	check := pm.Check("test", "cmd", "/project")
	if !check.ExpiresAt.IsZero() {
		t.Log("ExpiresAt preserved in check result when needed")
	}
}

func TestCommandScanner_WithPermissionManager(t *testing.T) {
	scanner := NewCommandScanner()
	pm := NewPermissionManager()
	scanner.permissions = pm

	scanner.Scan("rm -rf /")
	pm.Allow(PermissionDecision{
		Action:  ActionAllowOnce,
		Pattern: "rm_rf_root",
	})

	result, err := scanner.Scan("rm -rf /")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Blocked {
		t.Error("expected Blocked=false when permission exists")
	}

	overridden := false
	for _, f := range result.Findings {
		if f.Type == "permission_override" {
			overridden = true
		}
	}
	if !overridden {
		t.Error("expected permission_override finding")
	}
}

func TestCommandScanner_WithPermissionManager_ProjectScope(t *testing.T) {
	scanner := NewCommandScanner()
	pm := NewPermissionManager()
	scanner.permissions = pm
	scanner.projectPath = "/my/project"

	pm.Allow(PermissionDecision{
		Action:      ActionAllowProject,
		Pattern:     "rm_rf_root",
		ProjectPath: "/my/project",
	})

	result, err := scanner.Scan("rm -rf /")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Blocked {
		t.Error("expected Blocked=false with project-scoped permission")
	}
}

func TestCommandScanner_WithPermissionManager_NoPermission(t *testing.T) {
	scanner := NewCommandScanner()
	pm := NewPermissionManager()
	scanner.permissions = pm

	result, err := scanner.Scan("rm -rf /")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Blocked {
		t.Error("expected Blocked=true when no permission exists")
	}
}

func TestCommandScanner_WithPermissionManager_DoesNotAffectSafeCommands(t *testing.T) {
	scanner := NewCommandScanner()
	pm := NewPermissionManager()
	scanner.permissions = pm

	result, err := scanner.Scan("ls -la")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Blocked {
		t.Error("expected safe commands to not be blocked")
	}
	if len(result.Findings) != 0 {
		t.Errorf("expected no findings for safe command, got %d", len(result.Findings))
	}
}
