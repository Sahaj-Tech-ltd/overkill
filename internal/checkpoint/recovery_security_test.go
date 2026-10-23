package checkpoint

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionRecoveryRejectsSymlinkSnapshotAndRetargetedRestore(t *testing.T) {
	work, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(work, "safe")
	secret := filepath.Join(outside, "secret")
	os.WriteFile(target, []byte("original"), 0o755)
	os.WriteFile(secret, []byte("outside"), 0o600)
	m, err := NewSessionManager(t.TempDir(), 5, work, "s")
	if err != nil {
		t.Fatal(err)
	}
	man, err := m.Snapshot("s", "before", []string{"safe"})
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(target)
	if err := os.Symlink(secret, target); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Snapshot("s", "bad", []string{"safe"}); err == nil {
		t.Fatal("snapshot followed symlink")
	}
	if _, err := m.Restore(man.ID); err == nil {
		t.Fatal("restore followed symlink")
	}
	raw, _ := os.ReadFile(secret)
	if string(raw) != "outside" {
		t.Fatal("outside file changed")
	}
}

func TestSessionRecoveryPreflightsAllContentBeforeWriting(t *testing.T) {
	work, store := t.TempDir(), t.TempDir()
	m, _ := NewSessionManager(store, 5, work, "s")
	for _, name := range []string{"a", "b"} {
		os.WriteFile(filepath.Join(work, name), []byte("old-"+name), 0o600)
	}
	man, err := m.Snapshot("s", "before", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(work, "a"), []byte("new-a"), 0o600)
	os.WriteFile(filepath.Join(store, man.ID, man.Entries[1].Sha256), []byte("corrupt"), 0o600)
	if _, err := m.Restore(man.ID); err == nil {
		t.Fatal("corrupt snapshot restored")
	}
	raw, _ := os.ReadFile(filepath.Join(work, "a"))
	if string(raw) != "new-a" {
		t.Fatal("first file mutated before complete verification")
	}
	for _, id := range []string{"..", "../escape", "x/y", "x\\y"} {
		if _, err := m.Restore(id); err == nil {
			t.Fatalf("unsafe ID accepted %q", id)
		}
	}
}

func TestSessionRecoveryPreservesModesAndRetainsUndoOfRollback(t *testing.T) {
	work := t.TempDir()
	m, _ := NewSessionManager(t.TempDir(), 5, work, "s")
	path := filepath.Join(work, "run.sh")
	os.WriteFile(path, []byte("old"), 0o755)
	man, err := m.Snapshot("s", "before", []string{"run.sh"})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("new"), 0o600)
	os.Chmod(path, 0o640)
	if _, err := m.Restore(man.ID); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode lost: %v", info.Mode())
	}
	list, _ := m.List("s")
	recovery := ""
	for _, item := range list {
		if item.RecoveryOf == man.ID {
			recovery = item.ID
		}
	}
	if recovery == "" {
		t.Fatal("pre-restore recovery not retained")
	}
	if _, err := m.Restore(recovery); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	info, _ = os.Stat(path)
	if string(raw) != "new" || info.Mode().Perm() != 0o640 {
		t.Fatal("rollback recovery lost present contents/mode")
	}
}

func TestWholeWorkspaceRecoveryCapturesAndRemovesCreations(t *testing.T) {
	work := t.TempDir()
	m, _ := NewSessionManager(t.TempDir(), 5, work, "s")
	os.WriteFile(filepath.Join(work, "old"), []byte("before"), 0o600)
	man, err := m.Snapshot("s", "whole", nil)
	if err != nil {
		t.Fatal(err)
	}
	if man.Scope != "workspace" || len(man.Entries) != 1 {
		t.Fatalf("scope/entries: %+v", man)
	}
	os.Remove(filepath.Join(work, "old"))
	os.WriteFile(filepath.Join(work, "created"), []byte("new"), 0o600)
	if _, err := m.Restore(man.ID); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(work, "old")); string(raw) != "before" {
		t.Fatal("deleted file not restored")
	}
	if _, err := os.Stat(filepath.Join(work, "created")); !os.IsNotExist(err) {
		t.Fatal("new file not removed")
	}
}

func TestWholeWorkspaceGitScopeDoesNotStageCommitOrDeleteIgnoredFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	work := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", work)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	os.WriteFile(filepath.Join(work, ".gitignore"), []byte("ignored\n"), 0o600)
	os.WriteFile(filepath.Join(work, "visible"), []byte("old"), 0o600)
	os.WriteFile(filepath.Join(work, "ignored"), []byte("private"), 0o600)
	m, _ := NewSessionManager(t.TempDir(), 5, work, "s")
	man, err := m.Snapshot("s", "whole", nil)
	if err != nil {
		t.Fatal(err)
	}
	if man.Scope != "git-visible" {
		t.Fatal(man.Scope)
	}
	os.WriteFile(filepath.Join(work, "created"), []byte("new"), 0o600)
	if _, err := m.Restore(man.ID); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(work, "ignored")); string(raw) != "private" {
		t.Fatal("ignored file changed")
	}
	cmd = exec.Command("git", "-C", work, "diff", "--cached", "--name-only")
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatal("index changed")
	}
	if _, err := os.Stat(filepath.Join(work, ".git", "refs", "heads", "master")); err == nil {
		t.Fatal("commit created")
	}
}

func TestForgedManifestCannotEscapeWorkspaceOrReadArbitraryBlob(t *testing.T) {
	work, store := t.TempDir(), t.TempDir()
	m, _ := NewSessionManager(store, 5, work, "s")
	os.WriteFile(filepath.Join(work, "a"), []byte("old"), 0o600)
	man, _ := m.Snapshot("s", "before", []string{"a"})
	man.Entries[0].Sha256 = "../../outside"
	raw, _ := json.Marshal(man)
	os.WriteFile(filepath.Join(store, man.ID, "manifest.json"), raw, 0o600)
	if _, err := m.Restore(man.ID); err == nil {
		t.Fatal("invalid hash accepted")
	}
	man.Entries[0].Path = filepath.Join(t.TempDir(), "outside")
	raw, _ = json.Marshal(man)
	os.WriteFile(filepath.Join(store, man.ID, "manifest.json"), raw, 0o600)
	if _, err := m.Restore(man.ID); err == nil {
		t.Fatal("outside path accepted")
	}
}
