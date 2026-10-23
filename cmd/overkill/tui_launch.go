package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
)

// resolveFrontend gives explicit configuration and installed assets precedence
// over development sources. A user's unrelated Go module is never an install.
func resolveFrontend(executable, cwd string) (string, error) {
	if explicit := os.Getenv("OVERKILL_TUI_DIR"); explicit != "" {
		path, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if frontendExists(path) {
			return path, nil
		}
		return "", fmt.Errorf("OVERKILL_TUI_DIR has no built or development frontend: %s", path)
	}
	exeDir := filepath.Dir(executable)
	candidates := []string{filepath.Join(exeDir, "..", "Resources", "tui"), filepath.Join(exeDir, "tui"), filepath.Join(exeDir, "..", "share", "overkill", "tui")}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(mod), "module github.com/Sahaj-Tech-ltd/overkill\n") {
			candidates = append(candidates, filepath.Join(dir, "tui"))
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".overkill", "repo", "tui"))
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "overkill", "repo", "tui"))
	}
	for _, candidate := range candidates {
		if frontendExists(candidate) {
			return filepath.Clean(candidate), nil
		}
	}
	return "", fmt.Errorf("terminal frontend not found; install the frontend alongside Overkill or set OVERKILL_TUI_DIR to its directory")
}

func frontendExists(dir string) bool {
	for _, file := range []string{"dist/entry.js", "src/entry.tsx"} {
		if st, err := os.Stat(filepath.Join(dir, file)); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

func frontendCommand() (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate executable: %w", err)
	}
	if real, err := filepath.EvalSymlinks(executable); err == nil {
		executable = real
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	dir, err := resolveFrontend(executable, cwd)
	if err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	if st, err := os.Stat(filepath.Join(dir, "dist", "entry.js")); err == nil && !st.IsDir() {
		node := ""
		for _, candidate := range []string{filepath.Join(filepath.Dir(dir), "node"), filepath.Join(filepath.Dir(executable), "node")} {
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
				node = candidate
				break
			}
		}
		if node == "" {
			node, err = exec.LookPath("node")
			if err != nil {
				return nil, fmt.Errorf("frontend requires Node.js: %w", err)
			}
		}
		cmd = exec.Command(node, filepath.Join(dir, "dist", "entry.js"))
	} else {
		tsx := filepath.Join(dir, "node_modules", ".bin", "tsx")
		if _, err := os.Stat(tsx); err != nil {
			return nil, fmt.Errorf("development frontend dependencies missing in %s: run npm ci there: %w", dir, err)
		}
		cmd = exec.Command(tsx, filepath.Join(dir, "src", "entry.tsx"))
	}
	// Preserve the workspace cwd for tools; the entry point uses an absolute path.
	cmd.Dir = cwd
	return cmd, nil
}

func apiPort(addr string) string {
	parsed, err := url.Parse(addr)
	if err != nil {
		return ""
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return ""
	}
	return port
}

func waitAPIReady(ctx context.Context, ready <-chan struct{}, result <-chan error) error {
	select {
	case err := <-result:
		if err == nil {
			return fmt.Errorf("API server stopped before frontend launch")
		}
		return fmt.Errorf("start API: %w", err)
	case <-ctx.Done():
		return fmt.Errorf("waiting for API: %w", ctx.Err())
	case <-ready:
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("waiting for API: %w", err)
		}
		return nil
	}
}

func launchFrontend(parent context.Context, server *api.Server, frontend *exec.Cmd) error {
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); result <- server.Start(ctx) }()
	defer func() { cancel(); <-finished }()
	readyCtx, readyCancel := context.WithTimeout(ctx, 10*time.Second)
	err := waitAPIReady(readyCtx, server.Ready(), result)
	readyCancel()
	if err != nil {
		cancel()
		return err
	}
	// Replace inherited connection variables rather than creating duplicate keys.
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "OVERKILL_API_URL=") || strings.HasPrefix(entry, "OVERKILL_API_PORT=") || strings.HasPrefix(entry, "OVERKILL_API_TOKEN=") {
			continue
		}
		env = append(env, entry)
	}
	frontend.Env = append(env, "OVERKILL_API_URL="+server.Addr(), "OVERKILL_API_PORT="+apiPort(server.Addr()), "OVERKILL_API_TOKEN="+server.AuthToken())
	frontend.Stdin, frontend.Stdout, frontend.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := frontend.Start(); err != nil {
		return fmt.Errorf("launch terminal frontend: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- frontend.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("terminal frontend exited: %w", err)
		}
		return nil
	case err := <-result:
		_ = frontend.Process.Kill()
		<-done
		if err == nil {
			return fmt.Errorf("API server stopped unexpectedly")
		}
		return fmt.Errorf("API server stopped: %w", err)
	case <-ctx.Done():
		_ = frontend.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = frontend.Process.Kill()
			<-done
		}
		return ctx.Err()
	}
}

// findRepoRoot resolves the current project for the check command. It is not
// used to locate installed frontend assets.
func findRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			return cwd, nil
		}
	}
}
