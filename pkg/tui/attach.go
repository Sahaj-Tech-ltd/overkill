// Package tui — /attach handler (layer 2 of the image-paste feature).
//
// Terminals don't reliably forward clipboard image bytes through key
// events, so we sidestep the whole bracketed-paste mess and accept an
// explicit file path. The user runs `/attach <path>` (or pastes a path
// after the command), we read the file, detect MIME from extension+magic
// bytes, and stage it for the next send. On send the editor's text plus
// the staged attachments go into a single user message.
package tui

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	tuitypes "github.com/Sahaj-Tech-ltd/overkill/pkg/tui/types"
)

const (
	// maxAttachments caps how many files can be staged at once. Models
	// have different ceilings — Anthropic accepts up to 20 images per
	// request, OpenAI is 10, Gemini is loose. We pick the tightest so
	// the send doesn't surprise-fail on a provider switch.
	maxAttachments = 10

	// maxAttachmentBytes is the per-image ceiling. 20 MB is well under
	// every provider's documented limit (Anthropic 5 MB per image after
	// base64, OpenAI 20 MB), but we accept the raw size and let the
	// provider reject if it can't fit — better than rejecting locally
	// on a stale rule.
	maxAttachmentBytes = 20 * 1024 * 1024
)

// pendingAttachment is the TUI-side staged attachment. It carries the
// path so we can re-render the chip with a useful label and the bytes
// + MIME for the eventual providers.Attachment conversion.
type pendingAttachment struct {
	Path      string
	MediaType string
	Data      []byte
}

// imageMIMEFromPath returns the IANA media type for common image
// extensions. We fall back to http.DetectContentType which sniffs magic
// bytes — handles renamed files and works for the screenshot-saved-as
// "screenshot" no-extension case.
func imageMIMEFromPath(path string, data []byte) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	sniffed := http.DetectContentType(data)
	// http.DetectContentType returns "application/octet-stream" when it
	// can't decide. Treat that as "not an image" so we don't lie to the
	// provider about MIME.
	if strings.HasPrefix(sniffed, "image/") {
		return sniffed
	}
	return ""
}

// runAttach is the /attach handler. The full slash payload arrives as
// arg (everything after "/attach "). Empty arg prints usage; multiple
// space-separated paths attach all of them.
func (m *appModel) runAttach(arg string) tea.Cmd {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return m.toastCmd("usage: /attach <path> [more paths...]", "info")
	}

	// Split on whitespace but respect simple quoted paths so spaces in
	// filenames don't shred the input. We don't do full POSIX quoting —
	// just the common "path with spaces" case.
	paths := splitAttachPaths(arg)
	if len(paths) == 0 {
		return m.toastCmd("attach: no paths given", "warning")
	}

	var added int
	for _, p := range paths {
		if len(m.pendingAttachments) >= maxAttachments {
			return m.toastCmd(fmt.Sprintf("attach: hit %d-image limit, send first", maxAttachments), "warning")
		}
		// Expand ~ and resolve to absolute so chips show a useful label
		// instead of "../../../foo.png".
		resolved := expandTilde(p)
		info, err := os.Stat(resolved)
		if err != nil {
			return m.toastCmd("attach: "+err.Error(), "error")
		}
		if info.IsDir() {
			return m.toastCmd("attach: "+resolved+" is a directory", "warning")
		}
		if info.Size() > maxAttachmentBytes {
			return m.toastCmd(fmt.Sprintf("attach: %s exceeds %d MB cap", filepath.Base(resolved), maxAttachmentBytes/1024/1024), "warning")
		}

		data, err := os.ReadFile(resolved)
		if err != nil {
			return m.toastCmd("attach: read: "+err.Error(), "error")
		}
		mime := imageMIMEFromPath(resolved, data)
		if mime == "" {
			return m.toastCmd("attach: "+filepath.Base(resolved)+" is not a recognized image", "warning")
		}
		m.pendingAttachments = append(m.pendingAttachments, pendingAttachment{
			Path:      resolved,
			MediaType: mime,
			Data:      data,
		})
		added++
	}

	if added == 1 {
		return m.toastCmd(fmt.Sprintf("attached: %s (%d staged)", filepath.Base(paths[0]), len(m.pendingAttachments)), "success")
	}
	return func() tea.Msg {
		return tuitypes.ToastMsg{
			Text: fmt.Sprintf("attached %d files (%d staged total)", added, len(m.pendingAttachments)),
			Kind: "success",
		}
	}
}

// runAttachClear drops staged attachments without sending. Useful when
// the user changes their mind or staged the wrong file.
func (m *appModel) runAttachClear() tea.Cmd {
	if len(m.pendingAttachments) == 0 {
		return m.toastCmd("attach: nothing staged", "info")
	}
	n := len(m.pendingAttachments)
	m.pendingAttachments = nil
	return m.toastCmd(fmt.Sprintf("attach: cleared %d", n), "info")
}

// drainAttachments returns the staged attachments converted to the
// providers shape and clears the pending slice. Called from the send
// path so a successful send always consumes the staged attachments.
func (m *appModel) drainAttachments() []providers.Attachment {
	if len(m.pendingAttachments) == 0 {
		return nil
	}
	out := make([]providers.Attachment, 0, len(m.pendingAttachments))
	for _, p := range m.pendingAttachments {
		out = append(out, providers.Attachment{
			Kind:      providers.AttachmentImage,
			MediaType: p.MediaType,
			Data:      p.Data,
		})
	}
	m.pendingAttachments = nil
	return out
}

// renderAttachmentChips produces a one-line summary of staged
// attachments for display under the editor. Returns "" when nothing is
// staged so the caller can skip the row entirely.
func (m *appModel) renderAttachmentChips() string {
	if len(m.pendingAttachments) == 0 {
		return ""
	}
	chips := make([]string, 0, len(m.pendingAttachments))
	for _, p := range m.pendingAttachments {
		chips = append(chips, fmt.Sprintf("📎 %s", filepath.Base(p.Path)))
	}
	return strings.Join(chips, "  ")
}

// splitAttachPaths splits on whitespace while keeping quoted segments
// intact. We only honor balanced single or double quotes; an
// unterminated quote is a usage error and falls back to plain split.
func splitAttachPaths(s string) []string {
	var (
		out     []string
		current strings.Builder
		quote   rune
	)
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t':
			if current.Len() > 0 {
				out = append(out, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	// Unterminated quote — fall back to a plain split so the user still
	// gets something useful, not silent corruption.
	if quote != 0 {
		return strings.Fields(s)
	}
	return out
}

// expandTilde turns a leading ~ into $HOME. Only the leading character
// is expanded — ~user style is not supported (we have no need for it
// and it would require user-lookup).
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
