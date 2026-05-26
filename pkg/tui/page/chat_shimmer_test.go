package page

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/chat"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/logo"
)

func TestChatPage_ShimmerStopsOnceMessagesArrive(t *testing.T) {
	p := NewChatPage(nil)
	// Window size primes the shimmer.
	p, _ = p.Update(tea.WindowSizeMsg{Width: 200, Height: 50})

	// Append a message to leave welcome state.
	p.messages.Append(chat.NewMessage("user", "hi"))
	if !p.HasMessages() {
		t.Fatal("expected HasMessages to be true after append")
	}

	// Tick — shimmer must stop, returning nil cmd.
	p, cmd := p.Update(logo.ShimmerTickMsg{})
	if cmd != nil {
		t.Fatal("expected shimmer tick to return nil cmd once chat has messages")
	}
	if p.shimmerLogo.IsActive() {
		t.Fatal("expected shimmer to be inactive once chat is non-empty")
	}
}

func TestChatPage_ShimmerArmedOnWelcomeWindowSize(t *testing.T) {
	p := NewChatPage(nil)
	// Big enough that animation.Enabled() is true.
	updated, _ := p.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	// We can't observe the cmd timer directly, but the logo's width should
	// be set so subsequent ticks know whether to animate.
	if updated.shimmerLogo.View() == "" {
		t.Fatal("expected shimmer logo to render")
	}
}
