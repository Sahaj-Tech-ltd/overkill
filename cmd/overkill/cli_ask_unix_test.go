//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
)

func TestCLIAskTerminalSharedInputAndChoices(t *testing.T) {
	master, slave, err := pty.Open()
	require.NoError(t, err)
	defer master.Close()
	defer slave.Close()
	var output bytes.Buffer
	console := newCLIConsole(slave, &output, false)
	require.True(t, console.interactive)
	_, err = master.Write([]byte("initial message\n2\na follow-up\n"))
	require.NoError(t, err)
	line, err := console.readLine(t.Context())
	require.NoError(t, err)
	require.Equal(t, "initial message", line)
	tool := &cliAskTool{console: console}
	result, err := tool.Execute(t.Context(), json.RawMessage(`{"question":"Choose a color","choices":["red","blue"]}`))
	require.NoError(t, err)
	var answer tools.AskUserOutput
	require.NoError(t, json.Unmarshal(result, &answer))
	require.Equal(t, "blue", answer.Answer)
	require.Equal(t, 1, answer.Index)
	require.False(t, answer.Cancelled)
	line, err = console.readLine(t.Context())
	require.NoError(t, err)
	require.Equal(t, "a follow-up", line)
	require.Contains(t, output.String(), "1. red")
	require.Contains(t, output.String(), "2. blue")
}

func TestCLIAskFreeTextCancelInvalidChoiceAndEOF(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		cancel      bool
		answer      string
	}{{"free text", "custom answer\n", false, "custom answer"}, {"cancel", "/cancel\n", true, ""}, {"invalid choice then answer", "99\n1\n", false, "red"}} {
		t.Run(tc.name, func(t *testing.T) {
			master, slave, err := pty.Open()
			require.NoError(t, err)
			defer master.Close()
			defer slave.Close()
			var out bytes.Buffer
			console := newCLIConsole(slave, &out, false)
			_, err = master.Write([]byte(tc.input))
			require.NoError(t, err)
			result, err := (&cliAskTool{console}).Execute(t.Context(), json.RawMessage(`{"question":"Color?","choices":["red","blue"]}`))
			require.NoError(t, err)
			var answer tools.AskUserOutput
			require.NoError(t, json.Unmarshal(result, &answer))
			require.Equal(t, tc.cancel, answer.Cancelled)
			require.Equal(t, tc.answer, answer.Answer)
		})
	}
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer reader.Close()
	require.NoError(t, writer.Close())
	var output bytes.Buffer
	console := &cliConsole{input: reader, output: &output, interactive: true}
	result, err := (&cliAskTool{console}).Execute(t.Context(), json.RawMessage(`{"question":"EOF?"}`))
	require.NoError(t, err)
	var answer tools.AskUserOutput
	require.NoError(t, json.Unmarshal(result, &answer))
	require.True(t, answer.Cancelled)
	require.Equal(t, -1, answer.Index)
}

func TestCLIAskCancellationDoesNotConsumeNextInput(t *testing.T) {
	master, slave, err := pty.Open()
	require.NoError(t, err)
	defer master.Close()
	defer slave.Close()
	var out bytes.Buffer
	console := newCLIConsole(slave, &out, false)
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = (&cliAskTool{console}).Execute(ctx, json.RawMessage(`{"question":"Waiting?"}`))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second)
	_, err = master.Write([]byte("next user message\n"))
	require.NoError(t, err)
	line, err := console.readLine(t.Context())
	require.NoError(t, err)
	require.Equal(t, "next user message", line)
	cancelled, cancelNow := context.WithCancel(t.Context())
	cancelNow()
	_, err = (&cliAskTool{console}).Execute(cancelled, json.RawMessage(`{"question":"Never shown"}`))
	require.True(t, errors.Is(err, context.Canceled))
	require.NotContains(t, out.String(), "Never shown")
}

func TestCLIAskHeadlessAndJSONLDoNotReadInput(t *testing.T) {
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer reader.Close()
	defer writer.Close()
	_, err = writer.Write([]byte("still a user message\n"))
	require.NoError(t, err)
	var output bytes.Buffer
	console := newCLIConsole(reader, &output, false)
	_, err = (&cliAskTool{console}).Execute(t.Context(), json.RawMessage(`{"question":"Unsupported?"}`))
	require.ErrorIs(t, err, errCLIAskUnsupported)
	require.Empty(t, output.String())
	line, err := console.readLine(t.Context())
	require.NoError(t, err)
	require.Equal(t, "still a user message", line)
	master, slave, err := pty.Open()
	require.NoError(t, err)
	defer master.Close()
	defer slave.Close()
	console = newCLIConsole(slave, &output, true)
	_, err = (&cliAskTool{console}).Execute(t.Context(), json.RawMessage(`{"question":"JSONL?"}`))
	require.ErrorIs(t, err, errCLIAskUnsupported)
}
