package automation

import (
	"context"
	"errors"
	"testing"
)

func TestAutoCommitter_DisabledStageNoop(t *testing.T) {
	called := false
	c := NewAutoCommitter("/tmp", func(ctx context.Context, dir, msg string) (bool, error) {
		called = true
		return true, nil
	}, nil)
	committed, err := c.Commit(context.Background(), StageTestPass, "x")
	if err != nil || committed {
		t.Fatalf("disabled should noop, got committed=%v err=%v", committed, err)
	}
	if called {
		t.Fatal("runner should not be called when stage disabled")
	}
}

func TestAutoCommitter_EnabledStageFires(t *testing.T) {
	var seenMsg string
	c := NewAutoCommitter("/tmp", func(ctx context.Context, dir, msg string) (bool, error) {
		seenMsg = msg
		return true, nil
	}, nil)
	c.SetEnabled(StageBuildGreen, true)
	committed, err := c.Commit(context.Background(), StageBuildGreen, "build green for foo")
	if err != nil || !committed {
		t.Fatalf("expected commit, got committed=%v err=%v", committed, err)
	}
	if seenMsg == "" || !contains(seenMsg, "build-green") || !contains(seenMsg, "build green for foo") {
		t.Fatalf("commit msg malformed: %q", seenMsg)
	}
}

func TestAutoCommitter_RunnerErrorPropagates(t *testing.T) {
	c := NewAutoCommitter("/tmp", func(ctx context.Context, dir, msg string) (bool, error) {
		return false, errors.New("git not found")
	}, nil)
	c.SetEnabled(StageLintClean, true)
	if _, err := c.Commit(context.Background(), StageLintClean, "x"); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestAutoCommitter_NothingToCommitOK(t *testing.T) {
	c := NewAutoCommitter("/tmp", func(ctx context.Context, dir, msg string) (bool, error) {
		return false, nil
	}, nil)
	c.SetEnabled(StageTestPass, true)
	committed, err := c.Commit(context.Background(), StageTestPass, "noop")
	if err != nil || committed {
		t.Fatalf("nothing-to-commit should be (false, nil): committed=%v err=%v", committed, err)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
