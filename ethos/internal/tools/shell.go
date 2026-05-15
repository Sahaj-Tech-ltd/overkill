package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	// markerFullRe captures the rich form. exit captures the final shell
	// exit code (even for compound commands), cwd captures $PWD at the
	// moment the marker fired (so `cd foo && something` reports foo).
	// The bare prefix (no fields) is kept as a fallback so legacy commands
	// still parse.
	markerFullRe = regexp.MustCompile(
		`__OVERKILL_DONE__(?::exit=(-?\d+))?(?::cwd=([^\n\r]*))?\s*`,
	)
	trailingBlankRe = regexp.MustCompile(`\n+$`)
)

const overkillDoneMarker = "__OVERKILL_DONE__"

type ShellTool struct {
	maxTimeout        time.Duration
	defaultWorkingDir string
	// scanner is a defense-in-depth command scanner. The agent loop
	// also scans tool inputs before dispatch (§4.3), but a scan here
	// covers non-agent callers (direct invocation, future plugin API,
	// tests) and double-scanning is cheap.
	scanner *security.CommandScanner
}

// WithoutCommandScan disables the in-tool security scan. Used by tests
// that need to exercise commands the scanner would block. Production
// callers should leave the scanner on.
func WithoutCommandScan() func(*ShellTool) {
	return func(s *ShellTool) { s.scanner = nil }
}

type ShellInput struct {
	Command        string            `json:"command"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	WorkingDir     string            `json:"working_dir"`
	Env            map[string]string `json:"env"`
}

type ShellOutput struct {
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	TimedOut  bool   `json:"timed_out"`
	Completed bool   `json:"completed"`
	// Cwd is $PWD at the moment the marker fired — i.e. the working
	// directory the shell ended in (after any `cd` in the command).
	// Empty when the marker didn't carry a cwd (legacy / parse failure).
	Cwd string `json:"cwd,omitempty"`
	// ElapsedMs is wall-clock time from exec start to marker. Measured
	// Go-side. Includes shell startup + the command itself.
	ElapsedMs int64 `json:"elapsed_ms,omitempty"`
}

func NewShellTool(opts ...func(*ShellTool)) *ShellTool {
	t := &ShellTool{
		maxTimeout:        120 * time.Second,
		defaultWorkingDir: "",
		scanner:           security.NewCommandScanner(),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (s *ShellTool) Name() string {
	return "shell"
}

func appendMarker(cmd string) string {
	trimmed := strings.TrimSpace(cmd)
	// ALWAYS append. The previous "skip if marker already present"
	// branch let an LLM `echo '__OVERKILL_DONE__:exit=0:cwd=/' && rm -rf /`
	// fake an exit code while the destructive command still ran. The
	// parser now takes the LAST marker occurrence as truth; any
	// embedded fake earlier in the command's output is just noise.
	//
	// `;` not `&&` so the marker fires even when the user's command
	// fails. The shell captures $? before we evaluate $PWD so a failing
	// cd doesn't poison the cwd report. printf is used instead of echo
	// because echo's newline behaviour varies across shells.
	return trimmed + `; { __ovrk_e=$?; printf '` + overkillDoneMarker +
		`:exit=%d:cwd=%s\n' "$__ovrk_e" "$PWD"; }`
}

// markerInfo is the parsed payload of the structured marker. All fields
// may be zero/empty when the marker is missing or used the legacy form.
type markerInfo struct {
	Found bool
	Exit  int
	Cwd   string
}

// stripMarker pulls the structured marker out of combined output and
// returns the cleaned text plus the parsed metadata. When the marker is
// absent the cleaned output is the original input verbatim and Found is
// false — callers must NOT trust Exit/Cwd in that case.
func stripMarker(output string) (string, markerInfo) {
	// Take the LAST marker occurrence: appendMarker pins one to the end
	// of the command, so any earlier marker in the output is by
	// definition fake (an LLM-printed string that happens to match the
	// pattern). Using the last match closes the spoofing window.
	all := markerFullRe.FindAllStringSubmatch(output, -1)
	if len(all) == 0 {
		return output, markerInfo{}
	}
	m := all[len(all)-1]
	info := markerInfo{Found: true}
	if len(m) > 1 && m[1] != "" {
		// strconv-free parse: tiny positive/negative ints only.
		neg := false
		s := m[1]
		if s[0] == '-' {
			neg = true
			s = s[1:]
		}
		n := 0
		for _, c := range s {
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + int(c-'0')
		}
		if neg {
			n = -n
		}
		info.Exit = n
	}
	if len(m) > 2 {
		info.Cwd = m[2]
	}
	// Strip only the LAST marker occurrence. Earlier matches stay in
	// the output as ordinary text — they were not produced by our
	// trailing printf, so they're part of what the command actually
	// printed (or what an LLM tried to spoof) and the user/agent
	// should see them rather than have them silently elided.
	lastIdx := markerFullRe.FindAllStringIndex(output, -1)
	var cleaned string
	if len(lastIdx) > 0 {
		last := lastIdx[len(lastIdx)-1]
		cleaned = output[:last[0]] + output[last[1]:]
	} else {
		cleaned = output
	}
	cleaned = trailingBlankRe.ReplaceAllString(cleaned, "")
	if cleaned != "" {
		cleaned += "\n"
	}
	return cleaned, info
}

func (s *ShellTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in ShellInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("shell: %w", err)
	}

	if strings.TrimSpace(in.Command) == "" {
		return nil, fmt.Errorf("shell: command is required")
	}

	// §4.3 defense-in-depth. The agent loop also scans before dispatch,
	// but a scan here catches non-agent callers (direct invocation,
	// plugins). Scan the raw user command, not the marker-appended form.
	if s.scanner != nil {
		res, err := s.scanner.Scan(in.Command)
		if err == nil && res != nil && res.Blocked {
			reason := "blocked by command scanner"
			if len(res.Findings) > 0 {
				reason = res.Findings[0].Description
			}
			return nil, fmt.Errorf("shell: %s", reason)
		}
	}

	timeout := 30 * time.Second
	if in.TimeoutSeconds > 0 {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}
	if timeout > s.maxTimeout {
		timeout = s.maxTimeout
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	command := appendMarker(in.Command)

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", command)

	if in.WorkingDir != "" {
		// Constrain WorkingDir to a child of the default workspace
		// when one is configured. Without this guard an LLM could
		// `WorkingDir: "/etc"` and run commands outside the project
		// root. When no defaultWorkingDir is configured we trust the
		// caller's cwd (matches legacy permissive behaviour for the
		// no-workspace agent).
		if s.defaultWorkingDir != "" {
			base, baseErr := filepath.Abs(s.defaultWorkingDir)
			dir, dirErr := filepath.Abs(in.WorkingDir)
			if baseErr != nil || dirErr != nil {
				return nil, fmt.Errorf("shell: resolve working_dir: base=%v dir=%v", baseErr, dirErr)
			}
			rel, rerr := filepath.Rel(filepath.Clean(base), filepath.Clean(dir))
			if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, fmt.Errorf("shell: working_dir %q is outside workspace %q", in.WorkingDir, s.defaultWorkingDir)
			}
		}
		cmd.Dir = in.WorkingDir
	} else if s.defaultWorkingDir != "" {
		cmd.Dir = s.defaultWorkingDir
	}

	if len(in.Env) > 0 {
		cmd.Env = append(cmd.Environ(), envSlice(in.Env)...)
	}

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	startedAt := time.Now()
	err := cmd.Run()
	elapsed := time.Since(startedAt)

	raw := ansiRe.ReplaceAllString(combined.String(), "")
	stdout, info := stripMarker(raw)

	output := ShellOutput{
		ExitCode:  0,
		Stdout:    stdout,
		Stderr:    "",
		TimedOut:  false,
		Completed: info.Found,
		Cwd:       info.Cwd,
		ElapsedMs: elapsed.Milliseconds(),
	}
	// When the marker carries an exit code, trust it — it captures the
	// final shell exit even when the wrapper script itself succeeded.
	// Fall through to Go's exec.ExitError only when the marker is
	// missing (legacy commands, marker stripped by user output).
	if info.Found {
		output.ExitCode = info.Exit
	}

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			output.TimedOut = true
			output.ExitCode = -1
			output.Stderr = fmt.Sprintf("command timed out after %s", timeout)
			output.Completed = false
		} else if !info.Found {
			// No marker: fall back to Go's view of the exit code.
			if exitErr, ok := err.(*exec.ExitError); ok {
				output.ExitCode = exitErr.ExitCode()
			} else {
				output.ExitCode = -1
			}
			output.Completed = false
		}
	}

	rawOut, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("shell: %w", err)
	}
	return rawOut, nil
}

func envSlice(env map[string]string) []string {
	s := make([]string, 0, len(env))
	for k, v := range env {
		s = append(s, k+"="+v)
	}
	return s
}
