package dialog

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	tuitypes "github.com/Sahaj-Tech-ltd/overkill/pkg/tui/types"
)

// PermissionDecisionMsg is emitted after the user picks an option.
type PermissionDecisionMsg struct {
	Allow   bool
	Persist bool
}

// PermissionDialog renders a 3-option approval prompt for risky tool calls.
type PermissionDialog struct {
	Dialog
	ToolName string
	Args     string
	Risk     string
	Cursor   int
	Reply    chan<- tuitypes.PermissionReply
}

// NewPermissionDialog returns a fresh, hidden dialog.
func NewPermissionDialog() PermissionDialog {
	return PermissionDialog{Dialog: Dialog{Title: "permission"}}
}

// SetRequest configures the dialog for an incoming approval request and shows it.
func (p *PermissionDialog) SetRequest(req tuitypes.PermissionRequestMsg) {
	p.ToolName = req.ToolName
	p.Args = req.Args
	p.Risk = req.Risk
	p.Reply = req.Reply
	p.Cursor = 0
	p.Show = true
}

func (p PermissionDialog) options() []string {
	return []string{"allow once", "allow for session", "deny"}
}

// Update handles arrow keys and selection. The selected option is forwarded to
// the waiting goroutine via the Reply channel and the dialog hides itself.
func (p PermissionDialog) Update(msg tea.Msg) (PermissionDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	opts := p.options()
	switch k.String() {
	case "up", "k":
		if p.Cursor > 0 {
			p.Cursor--
		}
	case "down", "j":
		if p.Cursor < len(opts)-1 {
			p.Cursor++
		}
	case "enter":
		var reply tuitypes.PermissionReply
		switch p.Cursor {
		case 0:
			reply = tuitypes.PermissionReply{Allow: true, Persist: false}
		case 1:
			reply = tuitypes.PermissionReply{Allow: true, Persist: true}
		case 2:
			reply = tuitypes.PermissionReply{Allow: false, Persist: false}
		}
		if p.Reply != nil {
			// Non-blocking send — the caller buffers the channel.
			select {
			case p.Reply <- reply:
			default:
			}
			p.Reply = nil
		}
		p.Show = false
		return p, func() tea.Msg {
			return PermissionDecisionMsg{Allow: reply.Allow, Persist: reply.Persist}
		}
	case "esc":
		// Esc denies by default so the agent doesn't hang.
		if p.Reply != nil {
			select {
			case p.Reply <- tuitypes.PermissionReply{Allow: false}:
			default:
			}
			p.Reply = nil
		}
		p.Show = false
		return p, func() tea.Msg { return PermissionDecisionMsg{Allow: false} }
	}
	return p, nil
}

// View renders the dialog using the shared overlay primitive.
func (p PermissionDialog) View(totalWidth, totalHeight int) string {
	if !p.Show {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "tool:  %s\n", p.ToolName)
	fmt.Fprintf(&b, "risk:  %s\n", p.Risk)

	// Special-case `patch`: render the unified diff inline (colored) instead
	// of dumping the raw JSON args.
	if p.ToolName == "patch" {
		var pa struct {
			Path  string `json:"path"`
			Patch string `json:"patch"`
		}
		if err := json.Unmarshal([]byte(p.Args), &pa); err == nil && pa.Patch != "" {
			b.WriteString("\n")
			b.WriteString(RenderDiffBody(pa.Path, pa.Patch))
			b.WriteString("\n\n")
		} else {
			args := p.Args
			if len(args) > 200 {
				args = args[:197] + "..."
			}
			if args != "" {
				fmt.Fprintf(&b, "args:  %s\n", args)
			}
			b.WriteString("\n")
		}
	} else {
		args := p.Args
		if len(args) > 200 {
			args = args[:197] + "..."
		}
		if args != "" {
			fmt.Fprintf(&b, "args:  %s\n", args)
		}
		b.WriteString("\n")
	}
	for i, opt := range p.options() {
		prefix := "  "
		if i == p.Cursor {
			prefix = "> "
		}
		fmt.Fprintf(&b, "%s%s\n", prefix, opt)
	}
	return p.BaseView(strings.TrimRight(b.String(), "\n"), totalWidth, totalHeight)
}
