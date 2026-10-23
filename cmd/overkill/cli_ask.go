package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"golang.org/x/term"
)

var errCLIAskUnsupported = errors.New("ask_user unsupported: requires an interactive terminal and text output; headless/JSONL input is reserved for user messages")

// cliConsole owns stdin for both the REPL and mid-turn questions. Reading only
// the requested line avoids a second scanner stealing a buffered answer.
type cliConsole struct {
	input       *os.File
	output      io.Writer
	interactive bool
	mu          sync.Mutex
}

func newCLIConsole(input *os.File, output io.Writer, jsonl bool) *cliConsole {
	return &cliConsole{input: input, output: output, interactive: cliTerminalAskSupported && !jsonl && term.IsTerminal(int(input.Fd()))}
}

func (c *cliConsole) readLine(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var line []byte
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var b [1]byte
		n, err := readCLIByte(ctx, c.input, b[:])
		if n > 0 {
			if b[0] == '\n' {
				return strings.TrimSuffix(string(line), "\r"), nil
			}
			line = append(line, b[0])
			if len(line) > 64*1024 {
				return "", fmt.Errorf("CLI input exceeds 64 KiB")
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return string(line), nil
			}
			return "", err
		}
	}
}

func (c *cliConsole) ask(ctx context.Context, question string, choices []string) (string, int, bool) {
	if ctx.Err() != nil {
		return "", -1, true
	}
	fmt.Fprintf(c.output, "\n%s\n", question)
	for i, choice := range choices {
		fmt.Fprintf(c.output, "  %d. %s\n", i+1, choice)
	}
	fmt.Fprintln(c.output, "Enter an answer or /cancel (EOF cancels).")
	for {
		fmt.Fprint(c.output, "Answer: ")
		answer, err := c.readLine(ctx)
		if err != nil {
			fmt.Fprintln(c.output, "\nQuestion cancelled.")
			return "", -1, true
		}
		answer = strings.TrimSpace(answer)
		if answer == "/cancel" {
			return "", -1, true
		}
		if answer == "" {
			continue
		}
		if len(choices) > 0 {
			if index, err := strconv.Atoi(answer); err == nil {
				if index < 1 || index > len(choices) {
					fmt.Fprintf(c.output, "Choose 1–%d, enter text, or /cancel.\n", len(choices))
					continue
				}
				return choices[index-1], index - 1, false
			}
		}
		return answer, -1, false
	}
}

type cliAskTool struct{ console *cliConsole }

func (t *cliAskTool) Name() string { return "ask_user" }
func (t *cliAskTool) Description() string {
	return "Ask the terminal user a question, optionally with numbered choices. Unsupported in headless or JSONL mode."
}
func (t *cliAskTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"question":{"type":"string","minLength":1},"choices":{"type":"array","items":{"type":"string"}}},"required":["question"],"additionalProperties":false}`)
}
func (t *cliAskTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !t.console.interactive {
		return nil, errCLIAskUnsupported
	}
	result, err := tools.NewAskUserTool(t.console.ask).Execute(ctx, input)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return result, err
}
