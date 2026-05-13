package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
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
	if strings.Contains(trimmed, overkillDoneMarker) {
		return trimmed
	}
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
	m := markerFullRe.FindStringSubmatch(output)
	if m == nil {
		return output, markerInfo{}
	}
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
	cleaned := markerFullRe.ReplaceAllString(output, "")
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
