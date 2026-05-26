package dialog

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// stubOAuth replaces the package-level oauthStart/oauthPoll for the duration
// of a test. Returns a restore func.
func stubOAuth(t *testing.T, code, url, token string, pollErr error) func() {
	t.Helper()
	origStart := oauthStart
	origPoll := oauthPoll
	oauthStart = func(ctx context.Context, provider string) (*oauthFlow, error) {
		return &oauthFlow{userCode: code, verURL: url}, nil
	}
	oauthPoll = func(ctx context.Context, of *oauthFlow) (string, error) {
		if pollErr != nil {
			return "", pollErr
		}
		return token, nil
	}
	return func() {
		oauthStart = origStart
		oauthPoll = origPoll
	}
}

func TestSetupDialog_OAuthBranchAdvancesToModelStep(t *testing.T) {
	defer stubOAuth(t, "ABCD-1234", "https://example.test/device", "tok-success", nil)()

	d := NewSetupDialog()
	d.Show = true
	// Pick anthropic (idx 1 in the providers list).
	d.providerIdx = 1
	d.step = 1

	// Press 'b' for browser sign-in.
	updated, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if !updated.oauthActive {
		t.Fatalf("expected oauthActive=true after 'b'")
	}
	if cmd == nil {
		t.Fatalf("expected non-nil cmd from startOAuth")
	}
	// First message is the internal envelope.
	envMsg := cmd()
	env, ok := envMsg.(setupOAuthStartedInternal)
	if !ok {
		t.Fatalf("expected setupOAuthStartedInternal, got %T", envMsg)
	}
	if env.started.UserCode != "ABCD-1234" {
		t.Fatalf("user code mismatch: %q", env.started.UserCode)
	}
	// Apply the envelope; the dialog should now expose the user code.
	updated, batchCmd := updated.Update(env)
	if updated.oauthUserCode != "ABCD-1234" {
		t.Fatalf("expected user code stored on dialog, got %q", updated.oauthUserCode)
	}
	if batchCmd == nil {
		t.Fatalf("expected batch cmd that includes the poll")
	}
	// Drive the poll cmd directly.
	completeMsg := env.poll()
	complete, ok := completeMsg.(SetupOAuthCompleteMsg)
	if !ok {
		t.Fatalf("expected SetupOAuthCompleteMsg, got %T", completeMsg)
	}
	if complete.Token != "tok-success" {
		t.Fatalf("expected token, got %q", complete.Token)
	}
	updated, _ = updated.Update(complete)
	if updated.step != 2 {
		t.Fatalf("expected step to advance to 2 (model), got %d", updated.step)
	}
	if updated.oauthActive {
		t.Fatalf("expected oauthActive cleared after success")
	}
}

func TestSetupDialog_OAuthCancelReturnsToKeyEntry(t *testing.T) {
	defer stubOAuth(t, "X-CODE", "https://example.test/device", "tok", nil)()

	d := NewSetupDialog()
	d.Show = true
	d.providerIdx = 1 // anthropic
	d.step = 1

	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if !d.oauthActive {
		t.Fatalf("oauth should be active")
	}
	// Esc cancels.
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if d.oauthActive {
		t.Fatalf("expected oauth to be canceled by esc")
	}
	if d.step != 1 {
		t.Fatalf("expected to remain on key step, got %d", d.step)
	}
}

func TestSetupDialog_OAuthNotOfferedForUnsupportedProvider(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true
	// openai is index 0; oauth not supported for openai per oauthSupportedProviders.
	d.providerIdx = 0
	d.step = 1
	updated, _ := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if updated.oauthActive {
		t.Fatalf("expected 'b' to be ignored for unsupported provider")
	}
}

func TestSetupDialog_OAuthErrorSurfacesNote(t *testing.T) {
	defer stubOAuth(t, "X-CODE", "https://example.test/device", "", errors.New("denied"))()
	d := NewSetupDialog()
	d.Show = true
	d.providerIdx = 1
	d.step = 1
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	env := cmd().(setupOAuthStartedInternal)
	d, _ = d.Update(env)
	complete := env.poll().(SetupOAuthCompleteMsg)
	d, _ = d.Update(complete)
	if d.oauthErr == "" {
		t.Fatalf("expected oauthErr set on poll failure")
	}
}
