//go:build unix

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

// This acceptance test builds main and launches two real CLI processes. It
// deliberately does not construct API/runtime services in the test process.
// It runs in the ordinary native/CI PostgreSQL gate; testpg enforces the gate's
// required-database setting rather than silently substituting in-memory state.
func TestTwoACPCLIProcessesExchangeCorrelatedWorkspaceWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	type instance struct {
		name, workspace, home, dsn, configPath string
		database                               *sql.DB
		store                                  session.Store
		sess                                   *session.Session
		process                                *acpCLIProcess
	}
	instances := make([]*instance, 0, 2)
	for _, name := range []string{"frontend", "backend"} {
		database, _ := testpg.Open(t)
		var schema string
		require.NoError(t, database.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema))
		store, err := session.NewPostgresStore(database)
		require.NoError(t, err)
		item := &instance{name: name, workspace: t.TempDir(), home: t.TempDir(), database: database, store: store, dsn: acpProcessSchemaDSN(t, schema)}
		item.sess = session.NewSession(item.workspace)
		item.sess.Title = name
		item.sess.Provider = "fixture"
		item.sess.Model = "fixture-model"
		require.NoError(t, store.Create(ctx, item.sess))
		instances = append(instances, item)
	}
	require.NotEqual(t, instances[0].sess.ID, instances[1].sess.ID)
	require.NotEqual(t, instances[0].dsn, instances[1].dsn, "processes must use independent PostgreSQL schemas")

	binary := filepath.Join(t.TempDir(), "overkill")
	buildCtx, buildCancel := context.WithTimeout(ctx, 60*time.Second)
	build := exec.CommandContext(buildCtx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	buildOutput, buildErr := build.CombinedOutput()
	buildCancel()
	require.NoError(t, buildErr, "build actual CLI main: %s", buildOutput)

	const fixtureKey = "local-fixture-key"
	const acpToken = "local-acceptance-token"
	const enabledSkill = "CLI_ENABLED_SKILL_SENTINEL"
	const disabledSkill = "CLI_DISABLED_SKILL_SENTINEL"
	correlation := "frontend-to-backend:" + instances[0].sess.ID + ":" + instances[1].sess.ID
	var fixtureMu sync.Mutex
	backendURL := ""
	calls := map[string]int{}
	failures := make(chan string, 8)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || !input.Stream {
			select {
			case failures <- "provider received malformed or non-streaming request":
			default:
			}
			http.Error(w, "invalid fixture request", 400)
			return
		}
		role := strings.Trim(strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0], " ")
		if role != "frontend" && role != "backend" {
			select {
			case failures <- "provider received unexpected role route":
			default:
			}
			http.Error(w, "unknown fixture route", 400)
			return
		}
		fixtureMu.Lock()
		calls[role]++
		remoteURL := backendURL
		fixtureMu.Unlock()
		hasTool := false
		hasReply := false
		hasEnabledSkill, hasDisabledSkill := false, false
		user := ""
		for _, m := range input.Messages {
			var text string
			_ = json.Unmarshal(m.Content, &text)
			if m.Role == "system" {
				hasEnabledSkill = hasEnabledSkill || strings.Contains(text, enabledSkill)
				hasDisabledSkill = hasDisabledSkill || strings.Contains(text, disabledSkill)
			}
			if m.Role == "user" {
				user = text
			}
			if m.Role == "tool" {
				hasTool = true
				if strings.Contains(text, "backend-reply:"+correlation) {
					hasReply = true
				}
			}
		}
		if !hasEnabledSkill || hasDisabledSkill {
			select {
			case failures <- "CLI-loaded enabled/disabled skill instructions did not reach the actual provider correctly":
			default:
			}
			http.Error(w, "invalid skill fixture prompt", http.StatusBadRequest)
			return
		}
		if !strings.Contains(user, correlation) {
			select {
			case failures <- "provider request lost its correlation marker":
			default:
			}
			http.Error(w, "missing correlation", 400)
			return
		}
		var delta any
		finish := "stop"
		if !hasTool {
			fileArgs, _ := json.Marshal(map[string]string{"action": "write", "path": role + "-result.txt", "content": role + ":" + correlation})
			toolCalls := []any{map[string]any{"index": 0, "id": role + "-write", "type": "function", "function": map[string]string{"name": "fs", "arguments": string(fileArgs)}}}
			if role == "frontend" {
				sendArgs, _ := json.Marshal(map[string]string{"agent_url": remoteURL, "token": acpToken, "session": instances[1].sess.ID, "content": "implement backend and reply for " + correlation})
				toolCalls = append(toolCalls, map[string]any{"index": 1, "id": "frontend-delegate", "type": "function", "function": map[string]string{"name": "acp_send", "arguments": string(sendArgs)}})
			}
			delta = map[string]any{"tool_calls": toolCalls}
			finish = "tool_calls"
		} else {
			if role == "frontend" && !hasReply {
				select {
				case failures <- "frontend did not receive the backend's correlated acp_send reply":
				default:
				}
				http.Error(w, "uncorrelated remote result", 400)
				return
			}
			delta = map[string]string{"content": role + "-reply:" + correlation}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"id": "fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 3, "completion_tokens": 2}})
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	t.Cleanup(provider.Close)
	for _, item := range instances {
		for _, skill := range []struct {
			name, sentinel string
			enabled        bool
		}{{"enabled-fixture", enabledSkill, true}, {"disabled-fixture", disabledSkill, false}} {
			directory := filepath.Join(item.home, "skills", skill.name)
			require.NoError(t, os.MkdirAll(directory, 0700))
			body := fmt.Sprintf("---\nname: %s\ndescription: CLI runtime acceptance fixture\nenabled: %t\n---\n%s: Check workspace acceptance criteria.\n", skill.name, skill.enabled, skill.sentinel)
			require.NoError(t, os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(body), 0600))
		}
		cfg := config.Default()
		cfg.Rewriter.Enabled = false
		cfg.Browser.Enabled = false
		cfg.Slack.Enabled = false
		cfg.Agent.DefaultProvider = "fixture"
		cfg.Agent.DefaultModel = "fixture-model"
		cfg.Agent.MaxTurns = 6
		cfg.Security.AutonomyLevel = "yolo"
		cfg.DatabaseURL = item.dsn
		cfg.ACP.Listen = "127.0.0.1:0"
		cfg.ACP.Token = acpToken
		cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: fixtureKey, BaseURL: provider.URL + "/" + item.name, Models: []config.ModelConfig{{ID: "fixture-model", ContextWindow: 32000, MaxOutputTokens: 1024}}}}
		item.configPath = filepath.Join(item.home, "config.toml")
		require.NoError(t, cfg.Save(item.configPath))
		item.process = startACPCLIProcess(t, ctx, binary, item.configPath, item.home, item.workspace, item.dsn, []string{fixtureKey, acpToken})
	}
	// Both foreground CLI processes are alive together before any agent work.
	for _, item := range instances {
		item.process.awaitReady(t, ctx)
	}
	require.NotEqual(t, instances[0].process.command.Process.Pid, instances[1].process.command.Process.Pid)
	fixtureMu.Lock()
	backendURL = "http://" + instances[1].process.address
	fixtureMu.Unlock()
	for _, item := range instances {
		info, err := acp.NewClient("http://"+item.process.address, acpToken).GetInfo(ctx)
		require.NoError(t, err)
		require.Equal(t, "overkill", info.Name)
	}

	// Retain the POST's actual message/session IDs, then subscribe to precisely
	// that message's SSE endpoint. Frontend invokes real acp_send across processes.
	body, err := json.Marshal(acp.SendRequest{From: "cli-process-acceptance", SessionID: instances[0].sess.ID, Content: "write frontend and delegate backend for " + correlation})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+instances[0].process.address+"/v1/messages", bytes.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+acpToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	var sent acp.SendResponse
	decodeErr := json.NewDecoder(response.Body).Decode(&sent)
	require.NoError(t, response.Body.Close())
	require.NoError(t, decodeErr)
	require.NotEmpty(t, sent.MessageID)
	require.Equal(t, instances[0].sess.ID, sent.SessionID)
	events, err := acp.NewClient("http://"+instances[0].process.address, acpToken).Stream(ctx, sent.MessageID)
	require.NoError(t, err)
	done := 0
	var reply strings.Builder
	for event := range events {
		require.NotEqual(t, "error", event.Type, "ACP terminal error: %s", event.Error)
		if event.Type == "text_delta" {
			reply.WriteString(event.Content)
		}
		if event.Type == "done" {
			done++
		}
	}
	require.Equal(t, 1, done, "one correlated request must produce exactly one terminal success")
	require.Equal(t, "frontend-reply:"+correlation, reply.String())
	select {
	case failure := <-failures:
		t.Fatal(failure)
	default:
	}
	fixtureMu.Lock()
	frontendCalls, backendCalls := calls["frontend"], calls["backend"]
	fixtureMu.Unlock()
	require.Equal(t, 2, frontendCalls, "frontend must write/delegate then consume the reply")
	require.Equal(t, 2, backendCalls, "backend must execute a real edit then reply")
	for index, item := range instances {
		content, err := os.ReadFile(filepath.Join(item.workspace, item.name+"-result.txt"))
		require.NoError(t, err)
		require.Equal(t, item.name+":"+correlation, string(content))
		_, err = os.Stat(filepath.Join(item.workspace, instances[1-index].name+"-result.txt"))
		require.ErrorIs(t, err, os.ErrNotExist, "peer edit escaped its independent workspace")
		// The final done event is a commit boundary for both full transcripts.
		persisted, err := item.store.Load(ctx, item.sess.ID)
		require.NoError(t, err)
		require.Equal(t, item.workspace, persisted.Folder)
		require.GreaterOrEqual(t, len(persisted.Messages), 4)
		require.Equal(t, "assistant", persisted.Messages[len(persisted.Messages)-1].Role)
		require.Equal(t, item.name+"-reply:"+correlation, persisted.Messages[len(persisted.Messages)-1].Content)
		roles := map[string]bool{}
		for _, message := range persisted.Messages {
			roles[message.Role] = true
		}
		require.True(t, roles["user"] && roles["assistant"] && roles["tool"], "transcript must contain user, tool dispatch/result, and final reply")
		_, err = item.store.Load(ctx, instances[1-index].sess.ID)
		require.Error(t, err, "separate schemas must not resolve the peer session")
		sessions, err := item.store.List(ctx, session.ListOptions{})
		require.NoError(t, err)
		require.Len(t, sessions, 1, "ACP addressing must reuse the explicitly requested session")
	}
	for _, item := range instances {
		require.NoError(t, item.process.stop(), "%s graceful shutdown: %s", item.name, item.process.safeOutput())
		connection, err := net.DialTimeout("tcp", item.process.address, 200*time.Millisecond)
		if connection != nil {
			_ = connection.Close()
		}
		require.Error(t, err, "listener survived process shutdown")
		require.True(t, item.process.command.ProcessState.Exited(), "child must be reaped")
	}
	// The standalone terminal loop has its own agent constructor. Verify loaded
	// skill bodies there as well, rather than inferring parity from ACP alone.
	standalonePrompts := make(chan string, 1)
	standaloneProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			http.Error(w, "invalid standalone fixture", http.StatusBadRequest)
			return
		}
		for _, message := range input.Messages {
			if message.Role == "system" {
				select {
				case standalonePrompts <- message.Content:
				default:
					http.Error(w, "unexpected additional standalone request", http.StatusBadRequest)
					return
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"standalone-skill-fixture-completed"},"finish_reason":"stop"}]}`))
	}))
	defer standaloneProvider.Close()
	item := instances[0]
	standaloneConfig := config.Default()
	standaloneConfig.Rewriter.Enabled, standaloneConfig.Browser.Enabled = false, false
	standaloneConfig.Agent.DefaultProvider, standaloneConfig.Agent.DefaultModel = "fixture", "fixture-model"
	standaloneConfig.DatabaseURL = item.dsn
	standaloneConfig.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: fixtureKey, BaseURL: standaloneProvider.URL, Models: []config.ModelConfig{{ID: "fixture-model", ContextWindow: 32000, MaxOutputTokens: 1024}}}}
	standalonePath := filepath.Join(item.home, "standalone.toml")
	require.NoError(t, standaloneConfig.Save(standalonePath))
	standalone := exec.CommandContext(ctx, binary, "--config", standalonePath, "run", "--no-personality", "--no-boot")
	standalone.Env, standalone.Dir = item.process.command.Env, item.workspace
	standalone.Stdin = strings.NewReader("Check loaded skill instructions.\n")
	output, err := standalone.CombinedOutput()
	require.NoError(t, err, "standalone CLI: %s", output)
	require.Contains(t, string(output), "standalone-skill-fixture-completed")
	select {
	case prompt := <-standalonePrompts:
		require.Contains(t, prompt, enabledSkill)
		require.NotContains(t, prompt, disabledSkill)
	default:
		t.Fatal("standalone CLI never reached the actual provider")
	}
}

func acpProcessSchemaDSN(t *testing.T, schema string) string {
	t.Helper()
	base := os.Getenv("PG_TEST_URL")
	if base == "" {
		base = os.Getenv("DATABASE_URL")
	}
	if strings.HasPrefix(base, "postgres://") || strings.HasPrefix(base, "postgresql://") {
		parsed, err := url.Parse(base)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return base + " search_path=" + schema
}

type acpCLIOutput struct {
	mu    sync.Mutex
	bytes []byte
	ready chan string
}

func (o *acpCLIOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.bytes = append(o.bytes, data...)
	for _, line := range strings.Split(string(o.bytes), "\n") {
		if strings.HasPrefix(line, "ACP server listening on ") {
			select {
			case o.ready <- strings.TrimSpace(strings.TrimPrefix(line, "ACP server listening on ")):
			default:
			}
		}
	}
	// Runtime output is bounded in this fixture; keep readiness parsing intact.
	if len(o.bytes) > 128*1024 {
		o.bytes = o.bytes[len(o.bytes)-64*1024:]
	}
	return len(data), nil
}

type acpCLIProcess struct {
	command      *exec.Cmd
	output       *acpCLIOutput
	address, dsn string
	secrets      []string
	done         chan struct{}
	waitErr      error
	once         sync.Once
	stopErr      error
}

func startACPCLIProcess(t *testing.T, ctx context.Context, binary, configPath, home, workspace, dsn string, secrets []string) *acpCLIProcess {
	t.Helper()
	p := &acpCLIProcess{command: exec.Command(binary, "--config", configPath, "acp", "serve"), dsn: dsn, secrets: secrets, done: make(chan struct{}), output: &acpCLIOutput{ready: make(chan string, 1)}}
	p.command.Dir = workspace
	p.command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p.command.Stdout = p.output
	p.command.Stderr = p.output
	// Do not inherit provider keys, gateways, API endpoints, plugins, or daemons.
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(key); ok {
			p.command.Env = append(p.command.Env, key+"="+value)
		}
	}
	p.command.Env = append(p.command.Env, "OVERKILL_HOME="+home, "DATABASE_URL="+dsn, "OVERKILL_NO_HALLUSCAN=1")
	require.NoError(t, p.command.Start())
	go func() { p.waitErr = p.command.Wait(); close(p.done) }()
	t.Cleanup(func() {
		if err := p.stop(); err != nil {
			t.Errorf("CLI process cleanup: %v; %s", err, p.safeOutput())
		}
	})
	return p
}
func (p *acpCLIProcess) awaitReady(t *testing.T, ctx context.Context) {
	t.Helper()
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case p.address = <-p.output.ready:
		host, port, err := net.SplitHostPort(p.address)
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1", host)
		require.NotEqual(t, "0", port)
	case <-p.done:
		t.Fatalf("CLI exited before readiness: %v; %s", p.waitErr, p.safeOutput())
	case <-timer.C:
		t.Fatalf("CLI readiness timed out; %s", p.safeOutput())
	case <-ctx.Done():
		t.Fatalf("CLI startup context: %v; %s", ctx.Err(), p.safeOutput())
	}
}
func (p *acpCLIProcess) safeOutput() string {
	p.output.mu.Lock()
	text := string(p.output.bytes)
	p.output.mu.Unlock()
	text = strings.ReplaceAll(text, p.dsn, "[private test database]")
	for _, secret := range p.secrets {
		text = strings.ReplaceAll(text, secret, "[fixture credential]")
	}
	return text
}
func (p *acpCLIProcess) stop() error {
	p.once.Do(func() {
		select {
		case <-p.done:
			p.stopErr = fmt.Errorf("CLI exited before graceful shutdown: %w", p.waitErr)
		default:
			if err := p.command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
				p.stopErr = err
			}
			timer := time.NewTimer(12 * time.Second)
			defer timer.Stop()
			select {
			case <-p.done:
				if p.waitErr != nil {
					p.stopErr = fmt.Errorf("CLI shutdown: %w", p.waitErr)
				}
			case <-timer.C:
				_ = syscall.Kill(-p.command.Process.Pid, syscall.SIGKILL)
				<-p.done
				p.stopErr = fmt.Errorf("CLI did not exit after SIGTERM")
			}
		}
		// A separate process group lets the test prove children were not orphaned,
		// and clean up the whole group even if an assertion failed during startup.
		if err := syscall.Kill(-p.command.Process.Pid, 0); err == nil {
			_ = syscall.Kill(-p.command.Process.Pid, syscall.SIGKILL)
			p.stopErr = fmt.Errorf("CLI left processes in its process group")
		} else if !errors.Is(err, syscall.ESRCH) {
			p.stopErr = fmt.Errorf("inspect CLI child group: %w", err)
		}
	})
	return p.stopErr
}
