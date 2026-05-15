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
		// New form: `;`-continuation (fires even on failure), captures
		// $? + $PWD via printf. Assert structure, not exact text.
		assert.Contains(t, result, "echo hello")
		assert.Contains(t, result, "__OVERKILL_DONE__")
		assert.Contains(t, result, `exit=%d:cwd=%s`)
	})

	t.Run("always appends real marker even when input contains marker text", func(t *testing.T) {
		t.Parallel()
		// Post-spoofing-fix: appendMarker always appends. An LLM that
		// echoes the marker string can no longer fake an exit code —
		// the parser takes the LAST occurrence as truth, so the real
		// trailing printf wins.
		input := "echo hello && echo __OVERKILL_DONE__"
		result := appendMarker(input)
		assert.NotEqual(t, input, result, "must append, never short-circuit")
		assert.Contains(t, result, "printf '__OVERKILL_DONE__")
	})

	t.Run("handles trailing newline", func(t *testing.T) {
		t.Parallel()
		result := appendMarker("echo hello\n")
		assert.Contains(t, result, "__OVERKILL_DONE__")
	})

	t.Run("handles trailing spaces", func(t *testing.T) {
		t.Parallel()
		result := appendMarker("echo hello   ")
		assert.Contains(t, result, "__OVERKILL_DONE__")
	})

	t.Run("appends real marker even when input contains marker mid-command", func(t *testing.T) {
		t.Parallel()
		// Same spoofing-fix invariant: never trust the user/LLM
		// command to already carry a real marker. Always append.
		cmd := "echo __OVERKILL_DONE__ && echo hello"
		result := appendMarker(cmd)
		assert.NotEqual(t, cmd, result)
		assert.Contains(t, result, "printf '__OVERKILL_DONE__")
	})
}

func TestStripMarker(t *testing.T) {
	t.Parallel()

	t.Run("strips bare marker (legacy form)", func(t *testing.T) {
		t.Parallel()
		cleaned, info := stripMarker("hello\n__OVERKILL_DONE__\n")
		assert.True(t, info.Found)
		assert.Equal(t, "hello\n", cleaned)
		assert.Equal(t, 0, info.Exit) // bare form has no exit field
		assert.Equal(t, "", info.Cwd)
	})

	t.Run("strips structured marker with exit and cwd", func(t *testing.T) {
		t.Parallel()
		cleaned, info := stripMarker("hello\n__OVERKILL_DONE__:exit=0:cwd=/tmp/x\n")
		assert.True(t, info.Found)
		assert.Equal(t, "hello\n", cleaned)
		assert.Equal(t, 0, info.Exit)
		assert.Equal(t, "/tmp/x", info.Cwd)
	})

	t.Run("captures nonzero exit", func(t *testing.T) {
		t.Parallel()
		_, info := stripMarker("__OVERKILL_DONE__:exit=42:cwd=/home/u\n")
		assert.True(t, info.Found)
		assert.Equal(t, 42, info.Exit)
		assert.Equal(t, "/home/u", info.Cwd)
	})

	t.Run("captures negative exit", func(t *testing.T) {
		t.Parallel()
		_, info := stripMarker("__OVERKILL_DONE__:exit=-1:cwd=/\n")
		assert.True(t, info.Found)
		assert.Equal(t, -1, info.Exit)
	})

	t.Run("returns Found=false when marker absent", func(t *testing.T) {
		t.Parallel()
		cleaned, info := stripMarker("hello world\n")
		assert.False(t, info.Found)
		assert.Equal(t, "hello world\n", cleaned)
	})

	t.Run("handles empty output with bare marker", func(t *testing.T) {
		t.Parallel()
		cleaned, info := stripMarker("__OVERKILL_DONE__\n")
		assert.True(t, info.Found)
		assert.Equal(t, "", cleaned)
	})

	t.Run("handles empty output without marker", func(t *testing.T) {
		t.Parallel()
		cleaned, info := stripMarker("")
		assert.False(t, info.Found)
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

	t.Run("explicit shell exit terminates before marker", func(t *testing.T) {
		t.Parallel()
		// `exit 42` kills the wrapper shell before our `;` continuation
		// runs, so the marker is never printed. Completed reflects that.
		// ExitCode falls back to Go's exec.ExitError view (the process's
		// shell exit code = 42).
		input := ShellInput{Command: "exit 42"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Equal(t, 42, result.ExitCode)
		assert.False(t, result.Completed)
	})

	t.Run("failing command keeps shell alive so marker fires", func(t *testing.T) {
		t.Parallel()
		// `false` returns 1 but does NOT exit the shell — the `;`
		// continuation fires, marker captures exit=1, Completed=true.
		// This is the new behaviour: Completed = "marker reached"
		// (a useful "did it finish?" signal), exit code reports
		// success/failure separately.
		input := ShellInput{Command: "false"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.Equal(t, 1, result.ExitCode)
		assert.True(t, result.Completed)
		assert.NotEmpty(t, result.Cwd, "marker should carry cwd")
		assert.Greater(t, result.ElapsedMs, int64(-1), "should record elapsed time")
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
			Command: "echo $OVERKILL_TEST_SHELL",
			Env:     map[string]string{"OVERKILL_TEST_SHELL": "works"},
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

	t.Run("LLM-echoed marker does not spoof exit code", func(t *testing.T) {
		t.Parallel()
		// Spoofing-fix verification: a command that prints what looks
		// like a real marker (with a fake exit code) still gets the
		// REAL trailing marker appended by appendMarker, and the
		// parser uses the LAST marker as truth. The earlier echo'd
		// marker remains visible in stdout (it was actual shell
		// output; eliding it would hide real content), but it cannot
		// influence Completed/ExitCode/Cwd.
		input := ShellInput{Command: "echo hello && echo '__OVERKILL_DONE__:exit=42:cwd=/spoof'"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Completed)
		assert.Equal(t, 0, result.ExitCode, "exit code must come from trailing marker, not echo'd spoof")
		assert.NotEqual(t, "/spoof", result.Cwd, "cwd must not be spoofable")
		assert.Contains(t, result.Stdout, "hello")
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

	t.Run("command not found: shell continues, marker fires", func(t *testing.T) {
		t.Parallel()
		// `nonexistent_command_xyz` returns 127 from sh but does NOT
		// terminate the shell. Our `;` continuation fires; marker reports
		// exit=127. New semantics: Completed=true (marker reached),
		// ExitCode=127 (it failed). Both signals available separately.
		shell := NewShellTool()
		input := ShellInput{Command: "nonexistent_command_xyz"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.NotEqual(t, 0, result.ExitCode)
		assert.True(t, result.Completed)
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

	t.Run("compound command fails midway: shell alive, marker fires", func(t *testing.T) {
		t.Parallel()
		// `echo first && false && echo second` — `&&` short-circuits on
		// `false` (exit 1) but the shell process stays alive. Our `;`
		// continuation fires; marker captures exit=1 from $?. New
		// semantics distinguishes "did it finish cleanly" (Completed=
		// marker reached) from "did it succeed" (ExitCode).
		shell := NewShellTool()
		input := ShellInput{Command: "echo first && false && echo second"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.NotEqual(t, 0, result.ExitCode)
		assert.True(t, result.Completed)
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

	t.Run("cd command updates cwd in marker", func(t *testing.T) {
		t.Parallel()
		// `cd /tmp && pwd` — the marker fires AFTER the cd, so $PWD
		// in the printf captures /tmp. This is the whole point of
		// reading cwd from the shell vs trusting the WorkingDir input.
		shell := NewShellTool()
		input := ShellInput{Command: "cd /tmp && pwd"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Completed)
		// macOS resolves /tmp → /private/tmp; accept either.
		assert.Contains(t, []string{"/tmp", "/private/tmp"}, result.Cwd,
			"marker should capture the cd target")
	})

	t.Run("elapsed time recorded", func(t *testing.T) {
		t.Parallel()
		shell := NewShellTool()
		input := ShellInput{Command: "sleep 0.1"}
		raw, _ := json.Marshal(input)

		out, err := shell.Execute(context.Background(), raw)
		require.NoError(t, err)

		var result ShellOutput
		require.NoError(t, json.Unmarshal(out, &result))
		assert.True(t, result.Completed)
		assert.GreaterOrEqual(t, result.ElapsedMs, int64(100),
			"sleep 0.1 should take >=100ms")
	})
}
