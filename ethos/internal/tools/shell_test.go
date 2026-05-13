package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendMarker(t *testing.T) {
	t.Parallel()

	t.Run("appends marker to simple command", func(t *testing.T) {
		t.Parallel()
		result := appendMarker("echo hello")
		assert.Equal(t, "echo hello && echo __OVERKILL_DONE__", result)
	})

	t.Run("does not double-append if marker already present", func(t *testing.T) {
		t.Parallel()
		result := appendMarker("echo hello && echo __OVERKILL_DONE__")
		assert.Equal(t, "echo hello && echo __OVERKILL_DONE__", result)
	})

	t.Run("handles trailing newline", func(t *testing.T) {
		t.Parallel()
		result := appendMarker("echo hello\n")
		assert.Contains(t, result, "&& echo __OVERKILL_DONE__")
	})

	t.Run("handles trailing spaces", func(t *testing.T) {
		t.Parallel()
		result := appendMarker("echo hello   ")
		assert.Contains(t, result, "&& echo __OVERKILL_DONE__")
	})

	t.Run("detects marker mid-command", func(t *testing.T) {
		t.Parallel()
		cmd := "echo __OVERKILL_DONE__ && echo hello"
		result := appendMarker(cmd)
		assert.Equal(t, cmd, result)
	})
}

func TestStripMarker(t *testing.T) {
	t.Parallel()

	t.Run("strips marker from output", func(t *testing.T) {
		t.Parallel()
		cleaned, found := stripMarker("hello\n__OVERKILL_DONE__\n")
		assert.True(t, found)
		assert.Equal(t, "hello\n", cleaned)
	})

	t.Run("returns false when marker absent", func(t *testing.T) {
		t.Parallel()
		cleaned, found := stripMarker("hello world\n")
		assert.False(t, found)
		assert.Equal(t, "hello world\n", cleaned)
	})

	t.Run("handles marker on same line", func(t *testing.T) {
		t.Parallel()
		cleaned, found := stripMarker("hello __OVERKILL_DONE__\n")
		assert.True(t, found)
		assert.Equal(t, "hello\n", cleaned)
	})

	t.Run("handles empty output with marker", func(t *testing.T) {
		t.Parallel()
		cleaned, found := stripMarker("__OVERKILL_DONE__\n")
		assert.True(t, found)
		assert.Equal(t, "", cleaned)
	})

	t.Run("handles empty output without marker", func(t *testing.T) {
		t.Parallel()
		cleaned, found := stripMarker("")
		assert.False(t, found)
		assert.Equal(t, "", cleaned)
	})
}

func TestShellCompleted(t *testing.T) {
	t.Parallel()

	shell := NewShellTool()

	t.Run("successful command sets completed true", func(t *testing.T) {
		t.Parallel()
		input := ShellInput{Command: "echo hello"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Equal(t, 0, result.ExitCode)
		assert.True(t, result.Completed)
		assert.Contains(t, result.Stdout, "hello")
		assert.NotContains(t, result.Stdout, "__OVERKILL_DONE__")
	})

	t.Run("failing command sets completed false", func(t *testing.T) {
		t.Parallel()
		input := ShellInput{Command: "exit 42"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Equal(t, 42, result.ExitCode)
		assert.False(t, result.Completed)
	})

	t.Run("command with stderr output completes", func(t *testing.T) {
		t.Parallel()
		input := ShellInput{Command: "echo stdout; echo stderr >&2"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Completed)
		assert.Equal(t, 0, result.ExitCode)
	})

	t.Run("timed out command sets completed false", func(t *testing.T) {
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
		assert.False(t, result.Completed)
		assert.Equal(t, -1, result.ExitCode)
	})

	t.Run("multiline output strips marker cleanly", func(t *testing.T) {
		t.Parallel()
		input := ShellInput{Command: "echo line1; echo line2; echo line3"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Completed)
		assert.Contains(t, result.Stdout, "line1")
		assert.Contains(t, result.Stdout, "line2")
		assert.Contains(t, result.Stdout, "line3")
		assert.NotContains(t, result.Stdout, "__OVERKILL_DONE__")
	})

	t.Run("command producing no stdout completes", func(t *testing.T) {
		t.Parallel()
		input := ShellInput{Command: "true"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Equal(t, 0, result.ExitCode)
		assert.True(t, result.Completed)
	})

	t.Run("marker not visible in env var commands", func(t *testing.T) {
		t.Parallel()
		input := ShellInput{
			Command: "echo $ETHOS_TEST_SHELL",
			Env:     map[string]string{"ETHOS_TEST_SHELL": "works"},
		}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Completed)
		assert.Contains(t, result.Stdout, "works")
		assert.NotContains(t, result.Stdout, "__OVERKILL_DONE__")
	})

	t.Run("pre-existing marker in command is not double-appended", func(t *testing.T) {
		t.Parallel()
		input := ShellInput{Command: "echo hello && echo __OVERKILL_DONE__"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Completed)
		assert.Equal(t, 0, result.ExitCode)
		assert.Contains(t, result.Stdout, "hello")
		assert.NotContains(t, result.Stdout, "__OVERKILL_DONE__")
	})
}

func TestShellCompletedEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("empty command returns error", func(t *testing.T) {
		t.Parallel()
		shell := NewShellTool()
		input := ShellInput{Command: ""}
		raw, _ := json.Marshal(input)
		_, err := shell.Execute(context.Background(), raw)
		require.Error(t, err)
	})

	t.Run("command not found completed false", func(t *testing.T) {
		t.Parallel()
		shell := NewShellTool()
		input := ShellInput{Command: "nonexistent_command_xyz"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.NotEqual(t, 0, result.ExitCode)
		assert.False(t, result.Completed)
	})

	t.Run("compound command with && completes", func(t *testing.T) {
		t.Parallel()
		shell := NewShellTool()
		input := ShellInput{Command: "echo first && echo second"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Completed)
		assert.Contains(t, result.Stdout, "first")
		assert.Contains(t, result.Stdout, "second")
		assert.NotContains(t, result.Stdout, "__OVERKILL_DONE__")
	})

	t.Run("compound command fails midway completed false", func(t *testing.T) {
		t.Parallel()
		shell := NewShellTool()
		input := ShellInput{Command: "echo first && false && echo second"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.NotEqual(t, 0, result.ExitCode)
		assert.False(t, result.Completed)
	})

	t.Run("pipe command completes", func(t *testing.T) {
		t.Parallel()
		shell := NewShellTool()
		input := ShellInput{Command: "echo hello | tr 'h' 'H'"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Completed)
		assert.Contains(t, result.Stdout, "Hello")
		assert.NotContains(t, result.Stdout, "__OVERKILL_DONE__")
	})

	t.Run("subshell exit does not complete", func(t *testing.T) {
		t.Parallel()
		shell := NewShellTool()
		input := ShellInput{Command: "exit 1"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Equal(t, 1, result.ExitCode)
		assert.False(t, result.Completed)
	})

	t.Run("true command completes with empty stdout", func(t *testing.T) {
		t.Parallel()
		shell := NewShellTool()
		input := ShellInput{Command: "true"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Equal(t, 0, result.ExitCode)
		assert.True(t, result.Completed)
		assert.NotContains(t, result.Stdout, "__OVERKILL_DONE__")
	})
}
