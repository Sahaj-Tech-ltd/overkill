package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry(t *testing.T) {
	t.Run("register and get", func(t *testing.T) {
		r := NewRegistry()
		shell := NewShellTool()
		require.NoError(t, r.Register(shell))

		got, err := r.Get("shell")
		require.NoError(t, err)
		assert.Equal(t, shell, got)
	})

	t.Run("list", func(t *testing.T) {
		r := NewRegistry()
		require.NoError(t, r.Register(NewShellTool()))
		require.NoError(t, r.Register(NewWebTool().AllowLocalForTests()))

		list := r.List()
		assert.Contains(t, list, "shell")
		assert.Contains(t, list, "web")
	})

	t.Run("duplicate register", func(t *testing.T) {
		r := NewRegistry()
		require.NoError(t, r.Register(NewShellTool()))
		assert.Error(t, r.Register(NewShellTool()))
	})

	t.Run("get missing", func(t *testing.T) {
		r := NewRegistry()
		_, err := r.Get("nonexistent")
		assert.Error(t, err)
	})

	t.Run("register nil", func(t *testing.T) {
		r := NewRegistry()
		assert.Error(t, r.Register(nil))
	})
}

func TestShellTool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		command    string
		timeout    int
		workingDir string
		env        map[string]string
		wantCode   int
		wantOut    string
		wantErr    bool
	}{
		{
			name:     "echo success",
			command:  "echo hello",
			wantCode: 0,
			wantOut:  "hello",
		},
		{
			name:     "exit failure",
			command:  "exit 1",
			wantCode: 1,
		},
		{
			name:     "command not found",
			command:  "nonexistent_command_xyz",
			wantCode: 127,
			wantErr:  false,
		},
		{
			name:     "working dir",
			command:  "pwd",
			wantCode: 0,
		},
		{
			name:     "env vars",
			command:  "echo $OVERKILL_TEST_VAR",
			env:      map[string]string{"OVERKILL_TEST_VAR": "test_value"},
			wantOut:  "test_value",
			wantCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			shell := NewShellTool()

			input := ShellInput{
				Command:        tt.command,
				TimeoutSeconds: tt.timeout,
				WorkingDir:     tt.workingDir,
				Env:            tt.env,
			}
			raw, _ := json.Marshal(input)

			out, err := shell.Execute(context.Background(), raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			var result ShellOutput
			require.NoError(t, json.Unmarshal(out, &result))
			assert.Equal(t, tt.wantCode, result.ExitCode)
			if tt.wantOut != "" {
				assert.Contains(t, result.Stdout, tt.wantOut)
			}
		})
	}

	t.Run("timeout", func(t *testing.T) {
		shell := NewShellTool()
		input := ShellInput{
			Command:        "sleep 10",
			TimeoutSeconds: 1,
		}
		raw, _ := json.Marshal(input)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		out, err := shell.Execute(ctx, raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.TimedOut)
		assert.Equal(t, -1, result.ExitCode)
	})

	t.Run("invalid json", func(t *testing.T) {
		shell := NewShellTool()
		_, err := shell.Execute(context.Background(), json.RawMessage(`{invalid`))
		require.Error(t, err)
	})

	t.Run("empty command", func(t *testing.T) {
		shell := NewShellTool()
		input := ShellInput{Command: ""}
		raw, _ := json.Marshal(input)
		_, err := shell.Execute(context.Background(), raw)
		require.Error(t, err)
	})

	t.Run("max timeout enforced", func(t *testing.T) {
		shell := NewShellTool(func(s *ShellTool) {
			s.maxTimeout = 2 * time.Second
		})
		input := ShellInput{
			Command:        "sleep 10",
			TimeoutSeconds: 200,
		}
		raw, _ := json.Marshal(input)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		out, err := shell.Execute(ctx, raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.TimedOut)
	})
}

func TestFSTool(t *testing.T) {
	root := t.TempDir()
	fs := NewFSTool(root)

	t.Run("write and read round-trip", func(t *testing.T) {
		writeInput := FSInput{
			Action:  "write",
			Path:    "test.txt",
			Content: "hello world\nline 2\nline 3",
		}
		raw, _ := json.Marshal(writeInput)
		_, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		readInput := FSInput{Action: "read", Path: "test.txt"}
		raw, _ = json.Marshal(readInput)
		out, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Success)
		assert.Contains(t, result.Output, "hello world")
		assert.Contains(t, result.Output, "1:")
	})

	t.Run("read with offset and limit", func(t *testing.T) {
		writeInput := FSInput{
			Action:  "write",
			Path:    "offset.txt",
			Content: "line1\nline2\nline3\nline4\nline5",
		}
		raw, _ := json.Marshal(writeInput)
		_, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		readInput := FSInput{Action: "read", Path: "offset.txt", Offset: 2, Limit: 2}
		raw, _ = json.Marshal(readInput)
		out, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Contains(t, result.Output, "2: line2")
		assert.Contains(t, result.Output, "3: line3")
		assert.NotContains(t, result.Output, "line1")
		assert.NotContains(t, result.Output, "line4")
	})

	t.Run("edit", func(t *testing.T) {
		writeInput := FSInput{
			Action:  "write",
			Path:    "edit.txt",
			Content: "hello world",
		}
		raw, _ := json.Marshal(writeInput)
		_, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		editInput := FSInput{
			Action: "edit",
			Path:   "edit.txt",
			Old:    "hello",
			New:    "goodbye",
		}
		raw, _ = json.Marshal(editInput)
		out, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Success)

		readInput := FSInput{Action: "read", Path: "edit.txt"}
		raw, _ = json.Marshal(readInput)
		out, err = fs.Execute(context.Background(), raw)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Contains(t, result.Output, "goodbye world")
	})

	t.Run("edit no match", func(t *testing.T) {
		writeInput := FSInput{Action: "write", Path: "nomatch.txt", Content: "foo"}
		raw, _ := json.Marshal(writeInput)
		_, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		editInput := FSInput{Action: "edit", Path: "nomatch.txt", Old: "bar", New: "baz"}
		raw, _ = json.Marshal(editInput)
		_, err = fs.Execute(context.Background(), raw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("edit multiple matches", func(t *testing.T) {
		writeInput := FSInput{Action: "write", Path: "multi.txt", Content: "aaa bbb aaa"}
		raw, _ := json.Marshal(writeInput)
		_, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		editInput := FSInput{Action: "edit", Path: "multi.txt", Old: "aaa", New: "zzz"}
		raw, _ = json.Marshal(editInput)
		_, err = fs.Execute(context.Background(), raw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "found 2 times")
	})

	t.Run("mkdir", func(t *testing.T) {
		input := FSInput{Action: "mkdir", Path: "a/b/c"}
		raw, _ := json.Marshal(input)
		out, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Success)

		info, err := os.Stat(filepath.Join(root, "a", "b", "c"))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("stat", func(t *testing.T) {
		writeInput := FSInput{Action: "write", Path: "stat.txt", Content: "content"}
		raw, _ := json.Marshal(writeInput)
		_, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		input := FSInput{Action: "stat", Path: "stat.txt"}
		raw, _ = json.Marshal(input)
		out, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Success)
		assert.Contains(t, result.Output, "stat.txt")
		assert.Contains(t, result.Output, "is_dir\":false")
	})

	t.Run("glob", func(t *testing.T) {
		for _, name := range []string{"a.go", "b.go", "c.txt"} {
			input := FSInput{Action: "write", Path: name, Content: "x"}
			raw, _ := json.Marshal(input)
			_, err := fs.Execute(context.Background(), raw)
			require.NoError(t, err)
		}

		input := FSInput{Action: "glob", Pattern: "*.go"}
		raw, _ := json.Marshal(input)
		out, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Success)
		assert.Contains(t, result.Output, "a.go")
		assert.Contains(t, result.Output, "b.go")
		assert.NotContains(t, result.Output, "c.txt")
	})

	t.Run("path traversal rejected", func(t *testing.T) {
		input := FSInput{Action: "read", Path: "../../etc/passwd"}
		raw, _ := json.Marshal(input)
		_, err := fs.Execute(context.Background(), raw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "path traversal")
	})

	t.Run("write creates directories", func(t *testing.T) {
		input := FSInput{
			Action:  "write",
			Path:    "deep/nested/dir/file.txt",
			Content: "nested",
		}
		raw, _ := json.Marshal(input)
		out, err := fs.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Success)

		data, err := os.ReadFile(filepath.Join(root, "deep", "nested", "dir", "file.txt"))
		require.NoError(t, err)
		assert.Equal(t, "nested", string(data))
	})

	t.Run("unknown action", func(t *testing.T) {
		input := FSInput{Action: "delete"}
		raw, _ := json.Marshal(input)
		_, err := fs.Execute(context.Background(), raw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown action")
	})

	t.Run("empty path", func(t *testing.T) {
		input := FSInput{Action: "read", Path: ""}
		raw, _ := json.Marshal(input)
		_, err := fs.Execute(context.Background(), raw)
		require.Error(t, err)
	})
}

func TestGrepTool(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "objects", "dummy"), []byte("should not match"), 0o644))

	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "util.go"), []byte("package main\n\nfunc helper() string {\n\treturn \"hello\"\n}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "readme.md"), []byte("# Hello\n\nSome text here.\n"), 0o644))

	grep := NewGrepTool(root)

	t.Run("basic pattern", func(t *testing.T) {
		input := GrepInput{Pattern: "hello"}
		raw, _ := json.Marshal(input)
		out, err := grep.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Success)
		assert.Contains(t, result.Output, "main.go")
		assert.Contains(t, result.Output, "util.go")
	})

	t.Run("include filter", func(t *testing.T) {
		input := GrepInput{Pattern: "hello", Include: "*.go"}
		raw, _ := json.Marshal(input)
		out, err := grep.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Contains(t, result.Output, "main.go")
		assert.NotContains(t, result.Output, "readme.md")
	})

	t.Run("max results", func(t *testing.T) {
		input := GrepInput{Pattern: "hello", MaxResults: 1}
		raw, _ := json.Marshal(input)
		out, err := grep.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))

		var matches []grepMatch
		require.NoError(t, json.Unmarshal([]byte(result.Output), &matches))
		assert.Len(t, matches, 1)
	})

	t.Run("skips git dir", func(t *testing.T) {
		input := GrepInput{Pattern: "should not match"}
		raw, _ := json.Marshal(input)
		out, err := grep.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.NotContains(t, result.Output, ".git")
	})

	t.Run("empty pattern", func(t *testing.T) {
		input := GrepInput{Pattern: ""}
		raw, _ := json.Marshal(input)
		_, err := grep.Execute(context.Background(), raw)
		require.Error(t, err)
	})

	t.Run("invalid regex", func(t *testing.T) {
		input := GrepInput{Pattern: "[invalid"}
		raw, _ := json.Marshal(input)
		_, err := grep.Execute(context.Background(), raw)
		require.Error(t, err)
	})
}

func TestGitTool(t *testing.T) {
	gitDir := t.TempDir()
	runGit(t, gitDir, "init")
	runGit(t, gitDir, "config", "user.email", "test@test.com")
	runGit(t, gitDir, "config", "user.name", "Test")

	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "test.txt"), []byte("hello"), 0o644))

	git := NewGitTool(gitDir)

	t.Run("status", func(t *testing.T) {
		input := GitInput{Action: "status"}
		raw, _ := json.Marshal(input)
		out, err := git.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Contains(t, result.Output, "??")
		assert.Contains(t, result.Output, "test.txt")
	})

	t.Run("add and diff staged", func(t *testing.T) {
		addInput := GitInput{Action: "add", Paths: []string{"test.txt"}}
		raw, _ := json.Marshal(addInput)
		_, err := git.Execute(context.Background(), raw)
		require.NoError(t, err)

		diffInput := GitInput{Action: "diff", Staged: true}
		raw, _ = json.Marshal(diffInput)
		out, err := git.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Contains(t, result.Output, "hello")
	})

	t.Run("commit and log", func(t *testing.T) {
		commitInput := GitInput{Action: "commit", Message: "initial commit"}
		raw, _ := json.Marshal(commitInput)
		out, err := git.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ToolResult
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Success)

		logInput := GitInput{Action: "log", Count: 5}
		raw, _ = json.Marshal(logInput)
		out, err = git.Execute(context.Background(), raw)
		require.NoError(t, err)

		require.NoError(t, json.Unmarshal(out, &result))
		assert.Contains(t, result.Output, "initial commit")
	})

	t.Run("commit without message", func(t *testing.T) {
		input := GitInput{Action: "commit"}
		raw, _ := json.Marshal(input)
		_, err := git.Execute(context.Background(), raw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "message is required")
	})

	t.Run("stash", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(gitDir, "stash.txt"), []byte("stash me"), 0o644))

		pushInput := GitInput{Action: "stash", StashAction: "push"}
		raw, _ := json.Marshal(pushInput)
		_, err := git.Execute(context.Background(), raw)
		require.NoError(t, err)
	})

	t.Run("unknown action", func(t *testing.T) {
		input := GitInput{Action: "unknown"}
		raw, _ := json.Marshal(input)
		_, err := git.Execute(context.Background(), raw)
		require.Error(t, err)
	})
}

func TestWebTool(t *testing.T) {
	t.Run("successful fetch", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "overkill/0.1.0-dev", r.Header.Get("User-Agent"))
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "hello from server")
		}))
		defer server.Close()

		web := NewWebTool().AllowLocalForTests()
		input := WebInput{URL: server.URL}
		raw, _ := json.Marshal(input)

		out, err := web.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result WebOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Equal(t, 200, result.StatusCode)
		assert.Equal(t, "hello from server", result.Content)
		assert.Equal(t, "text/plain", result.ContentType)
		assert.False(t, result.Truncated)
	})

	t.Run("truncation", func(t *testing.T) {
		body := strings.Repeat("x", 1000)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, body)
		}))
		defer server.Close()

		web := NewWebTool().AllowLocalForTests()
		input := WebInput{URL: server.URL, MaxSize: 100}
		raw, _ := json.Marshal(input)

		out, err := web.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result WebOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Truncated)
		assert.Len(t, result.Content, 100)
	})

	t.Run("invalid scheme", func(t *testing.T) {
		web := NewWebTool().AllowLocalForTests()
		input := WebInput{URL: "ftp://example.com"}
		raw, _ := json.Marshal(input)

		_, err := web.Execute(context.Background(), raw)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "http and https")
	})

	t.Run("empty url", func(t *testing.T) {
		web := NewWebTool().AllowLocalForTests()
		input := WebInput{URL: ""}
		raw, _ := json.Marshal(input)

		_, err := web.Execute(context.Background(), raw)
		require.Error(t, err)
	})

	t.Run("server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "internal error")
		}))
		defer server.Close()

		web := NewWebTool().AllowLocalForTests()
		input := WebInput{URL: server.URL}
		raw, _ := json.Marshal(input)

		out, err := web.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result WebOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Equal(t, 500, result.StatusCode)
		assert.Equal(t, "internal error", result.Content)
	})
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Run()
}
