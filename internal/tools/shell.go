package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
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

var dangerousEnv = map[string]bool{
	"LD_PRELOAD":            true,
	"LD_LIBRARY_PATH":       true,
	"DYLD_INSERT_LIBRARIES": true,
	"DYLD_LIBRARY_PATH":     true,
	"PATH":                  true,
	"PYTHONPATH":            true,
	"BASH_ENV":              true,
	"PROMPT_COMMAND":        true,
	"ENV":                   true,
	"SHELLOPTS":             true,
	"BASHOPTS":              true,
	"IFS":                   true,
	"CDPATH":                true,
	"GLOBIGNORE":            true,
	"PS4":                   true,
	"ZDOTDIR":               true,
}

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
	// Raw combined bytes include ANSI escapes and the completion marker;
	// visible output retains at most 1 MiB plus a truncation notice.
	OutputTruncated bool   `json:"output_truncated,omitempty"`
	OutputBytes     int64  `json:"output_bytes,omitempty"`
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	TimedOut        bool   `json:"timed_out"`
	Completed       bool   `json:"completed"`
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
		maxTimeout:        900 * time.Second, // max throughput for long builds
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
	return appendMarkerWithID(cmd, overkillDoneMarker)
}

func appendMarkerWithID(cmd, marker string) string {
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
	return trimmed + `; { __ovrk_e=$?; printf '` + marker +
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
	return stripMarkerWithPattern(output, markerFullRe)
}

func stripMarkerWithPattern(output string, pattern *regexp.Regexp) (string, markerInfo) {
	// Take the LAST marker occurrence: appendMarker pins one to the end
	// of the command, so any earlier marker in the output is by
	// definition fake (an LLM-printed string that happens to match the
	// pattern). Using the last match closes the spoofing window.
	all := pattern.FindAllStringSubmatch(output, -1)
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
	lastIdx := pattern.FindAllStringIndex(output, -1)
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
	if _, err := security.ShellSyntaxFindings(in.Command); err != nil {
		return nil, fmt.Errorf("shell: %w", err)
	}
	if s.scanner != nil {
		res, err := s.scanner.Scan(in.Command)
		if err != nil {
			// Scanner error means we couldn't verify safety —
			// treat as a block rather than silently allowing.
			return nil, fmt.Errorf("shell: scanner error: %w", err)
		}
		if res != nil && res.Blocked {
			reason := "blocked by command scanner"
			if len(res.Findings) > 0 {
				reason = res.Findings[0].Description
			}
			return nil, fmt.Errorf("shell: %s", reason)
		}
	}

	timeout := estimateTimeout(in.Command)
	if in.TimeoutSeconds > 0 {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}
	if timeout > s.maxTimeout {
		timeout = s.maxTimeout
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// A fresh receipt prevents preprinted static markers from claiming completion
	// when exit/exec stops the shell before the trailing printf runs.
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("shell: completion receipt: %w", err)
	}
	marker := overkillDoneMarker + "_" + hex.EncodeToString(nonce[:])
	command := appendMarkerWithID(in.Command, marker)

	cmd := exec.Command("sh", "-c", command)

	workingDir, err := resolveShellWorkingDir(s.defaultWorkingDir, in.WorkingDir)
	if err != nil {
		return nil, err
	}
	cmd.Dir = workingDir
	// Inherited shell startup hooks are just as executable as supplied hooks.
	// Preserve the application's search path, while rejecting caller overrides.
	for _, entry := range cmd.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !shellStartupEnv(key) {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	for k, v := range in.Env {
		if strings.ContainsAny(k, "=\x00") || k == "" || strings.ContainsRune(v, 0) {
			return nil, fmt.Errorf("shell: invalid environment entry")
		}
		if !dangerousEnv[strings.ToUpper(k)] && !shellStartupEnv(k) && !strings.HasPrefix(strings.ToUpper(k), "LD_") && !strings.HasPrefix(strings.ToUpper(k), "DYLD_") {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	combined := processrunner.NewOutputCapture(1024 * 1024)
	cmd.Stdout = combined
	cmd.Stderr = combined

	startedAt := time.Now()
	err = processrunner.Run(cmdCtx, cmd)
	elapsed := time.Since(startedAt)

	text, total, truncated := combined.Snapshot()
	raw := ansiRe.ReplaceAllString(text, "")
	stdout, info := stripMarkerWithPattern(raw, regexp.MustCompile(regexp.QuoteMeta(marker)+`(?::exit=(-?\d+))?(?::cwd=([^\n\r]*))?\s*`))

	output := ShellOutput{
		OutputTruncated: truncated,
		OutputBytes:     total,
		ExitCode:        0,
		Stdout:          stdout,
		Stderr:          "",
		TimedOut:        false,
		Completed:       info.Found,
		Cwd:             info.Cwd,
		ElapsedMs:       elapsed.Milliseconds(),
	}
	// When the marker carries an exit code, trust it — it captures the
	// final shell exit even when the wrapper script itself succeeded.
	// Fall through to Go's exec.ExitError only when the marker is
	// missing (legacy commands, marker stripped by user output).
	if info.Found {
		output.ExitCode = info.Exit
	}

	if err != nil {
		output.Cwd = ""
		if cmdCtx.Err() == context.DeadlineExceeded {
			output.TimedOut = true
			output.ExitCode = -1
			output.Stderr = fmt.Sprintf("command timed out after %s", timeout)
			output.Completed = false
		} else {
			// A failed/terminated shell overrides any printed marker. A
			// command can exit before our appended marker is reached.
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				output.ExitCode = exitErr.ExitCode()
			} else {
				output.ExitCode = -1
			}
			output.Completed = false
			if cmdCtx.Err() != nil {
				output.Stderr = "command cancelled"
			}
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

// estimateTimeout returns a reasonable default timeout for a shell command.
// The LLM can override by setting timeout_seconds in the tool input.
// Preset tiers:
//
//	2 min — simple lookups (ls, cat, echo, grep, which, pwd, whoami)
//	5 min — common dev commands (git, npm, go, pip, docker, curl, make)
//	10 min — build/install chains (&&, |, apt, cargo, npx, python3, tests)
//	15 min — heavy operations (create, build, generate, compile, oneshot)
func estimateTimeout(cmd string) time.Duration {
	cmd = strings.TrimSpace(cmd)
	lower := strings.ToLower(cmd)

	// Heavy: multi-stage builds, code generation, recursive ops.
	heavyWords := []string{"create", "build", "generate", "compile", "oneshot",
		"cargo build", "go build", "docker build", "npm run build",
		"pip install", "apt-get", "npx create", "cargo install",
		"flutter build", "gradle", "mvn ", "cmake", "make install"}
	for _, w := range heavyWords {
		if strings.Contains(lower, w) {
			return 10 * time.Minute
		}
	}

	// Medium: dev tooling, package managers, tests, pipes.
	mediumWords := []string{"git ", "npm ", "npx ", "go ", "cargo ", "docker ",
		"pip3 ", "python3 ", "node ", "curl ", "wget ", "ssh ",
		"make ", "gcc ", "g++ ", "javac ", "rustc ", "cargo ",
		"systemctl ", "sudo ", "chmod ", "chown ",
		"scp ", "rsync ", "tar ", "unzip ",
		"test ", "pytest", "go test", "cargo test", "npm test",
		"ffmpeg", "convert ", "sqlite3"}
	hasPipe := strings.Contains(cmd, "|")
	hasRedirect := strings.Contains(cmd, ">") || strings.Contains(cmd, ">>")
	for _, w := range mediumWords {
		if strings.Contains(lower, w) {
			return 5 * time.Minute
		}
	}
	if hasPipe || hasRedirect {
		return 5 * time.Minute
	}
	// Multi-command chains.
	if strings.Contains(cmd, "&&") || strings.Contains(cmd, ";") {
		return 5 * time.Minute
	}

	// Default: simple command (ls, cat, echo, grep, etc.) — 2 min.
	return 2 * time.Minute
}

// resolveShellWorkingDir checks the actual existing directory, including symlink
// ancestors. It is containment of the initial cwd, not a filesystem sandbox; a
// command can still cd elsewhere. Concurrent hostile directory replacement needs
// OS sandboxing to provide a stronger boundary.
func resolveShellWorkingDir(base, requested string) (string, error) {
	if base == "" {
		return requested, nil
	}
	root, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("shell: resolve workspace: %w", err)
	}
	// Reject lexical escapes even when the workspace is unavailable.
	if requested != "" {
		candidate := requested
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, candidate)
		}
		rel, e := filepath.Rel(root, candidate)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("shell: working_dir is outside workspace")
		}
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("shell: resolve workspace symlinks: %w", err)
	}
	dir := requested
	if dir == "" {
		dir = root
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("shell: resolve working_dir: %w", err)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("shell: working_dir is outside workspace")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("shell: working_dir must be an existing directory")
	}
	return dir, nil
}
func shellStartupEnv(key string) bool {
	key = strings.ToUpper(key)
	switch key {
	case "ENV", "BASH_ENV", "PROMPT_COMMAND", "SHELLOPTS", "BASHOPTS", "IFS", "CDPATH", "GLOBIGNORE", "PS4", "ZDOTDIR":
		return true
	}
	return strings.HasPrefix(key, "BASH_FUNC_")
}
