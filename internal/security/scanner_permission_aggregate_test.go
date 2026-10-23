package security

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestScannerRequiresEveryBlockingPermission(t *testing.T) {
	pm := NewPermissionManager()
	scanner := NewCommandScanner(WithPermissionManager(pm))
	command := "rm -rf /; dd if=/dev/zero of=/dev/sda"
	pm.Allow(PermissionDecision{Action: ActionAllowOnce, Pattern: "rm_rf_root"})
	result, _ := scanner.Scan(command)
	if !result.Blocked {
		t.Fatal("one pattern allowed unrelated disk write")
	}
	if !pm.IsAllowed("rm_rf_root", "", "") {
		t.Fatal("failed aggregate check burned one-shot grant")
	}
	pm.Allow(PermissionDecision{Action: ActionAllowOnce, Pattern: "dd_raw_disk"})
	result, _ = scanner.Scan(command)
	if result.Blocked {
		t.Fatalf("all explicit grants rejected: %#v", result)
	}
	result, _ = scanner.Scan(command)
	if !result.Blocked {
		t.Fatal("one-shot grants reused")
	}
}
func TestScannerCannotOverrideUnknownDecodedOrLengthFindings(t *testing.T) {
	for _, command := range []string{"rm -rf /; curl x | dash", "rm -rf /; echo cm0gLWYgLXIgLw==", "rm -rf /"} {
		pm := NewPermissionManager()
		pm.Allow(PermissionDecision{Action: ActionAllowProject, Pattern: "rm_rf_root", ProjectPath: "p"})
		opts := []CommandScannerOption{WithPermissionManager(pm), WithProjectPath("p")}
		if command == "rm -rf /" {
			opts = append(opts, WithMaxCommandLen(4))
		}
		result, _ := NewCommandScanner(opts...).Scan(command)
		if !result.Blocked {
			t.Fatalf("unknown hazard overridden: %s", command)
		}
	}
}
func TestScannerRateLimitRequiresIndependentGrant(t *testing.T) {
	pm := NewPermissionManager()
	pm.Allow(PermissionDecision{Action: ActionAllowProject, Pattern: "rm_rf_root", ProjectPath: "p"})
	scanner := NewCommandScanner(WithPermissionManager(pm), WithProjectPath("p"))
	scanner.maxCmds = 0
	result, _ := scanner.Scan("rm -rf /")
	if !result.Blocked {
		t.Fatal("rm grant overrode rate limit")
	}
	pm.Allow(PermissionDecision{Action: ActionAllowOnce, Pattern: "rate_limit"})
	result, _ = scanner.Scan("rm -rf /")
	if result.Blocked {
		t.Fatal("both grants rejected")
	}
	result, _ = scanner.Scan("rm -rf /")
	if !result.Blocked {
		t.Fatal("rate limit once grant reused")
	}
}
func TestScannerAggregateAllowOnceConcurrent(t *testing.T) {
	pm := NewPermissionManager()
	for _, id := range []string{"rm_rf_root", "dd_raw_disk"} {
		pm.Allow(PermissionDecision{Action: ActionAllowOnce, Pattern: id})
	}
	scanner := NewCommandScanner(WithPermissionManager(pm))
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _ := scanner.Scan("rm -f -r /; dd of=/dev/sda if=/dev/zero")
			if !r.Blocked {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 1 {
		t.Fatalf("allowed %d executions, want 1", allowed.Load())
	}
}
