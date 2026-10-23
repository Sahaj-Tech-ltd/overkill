package childruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

type auditReadTool struct{ tools.Tool }

func (t *auditReadTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var args struct{ Action string }
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, err
	}
	if args.Action != "read" && args.Action != "glob" && args.Action != "stat" {
		return nil, fmt.Errorf("audit worker: filesystem action %q is not read-only", args.Action)
	}
	return t.Tool.Execute(ctx, input)
}
func (t *auditReadTool) Description() string {
	return "Read, glob, or stat contained workspace files. Mutation operations are unavailable."
}
func (t *auditReadTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["read","glob","stat"]},"path":{"type":"string"},"pattern":{"type":"string"},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":0}},"required":["action"],"additionalProperties":false}`)
}

type auditInspectTool struct{ name, workspace string }

func (t *auditInspectTool) Name() string            { return t.name }
func (t *auditInspectTool) Description() string     { return auditToolDefinition(t.name).Description }
func (t *auditInspectTool) Schema() json.RawMessage { return auditToolDefinition(t.name).Parameters }
func (t *auditInspectTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if t.name == "audit_git" {
		var args struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		options := []string{"--no-pager", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null"}
		switch args.Action {
		case "status":
			options = append(options, "status", "--short")
		case "diff":
			options = append(options, "diff", "--no-ext-diff", "--no-textconv", "HEAD", "--")
		case "log":
			options = append(options, "log", "-5", "--oneline", "--no-show-signature")
		default:
			return nil, fmt.Errorf("audit worker: git action must be status, diff, or log")
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", options...)
		cmd.Dir = t.workspace
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		out := &auditOutput{}
		cmd.Stdout, cmd.Stderr = out, out
		err := processrunner.Run(ctx, cmd)
		data, _ := json.Marshal(map[string]any{"output": out.text, "truncated": out.truncated})
		return data, err
	}
	var args struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, err
	}
	if len(args.Pattern) == 0 || len(args.Pattern) > 1024 {
		return nil, fmt.Errorf("audit worker: bounded search pattern required")
	}
	pattern, err := regexp.Compile(args.Pattern)
	if err != nil {
		return nil, err
	}
	type match struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Text string `json:"text"`
	}
	matches := []match{}
	files, bytes := 0, int64(0)
	truncated := false
	err = filepath.WalkDir(t.workspace, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != t.workspace && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return nil
		}
		files++
		bytes += info.Size()
		if files > 4096 || bytes > 16*1024*1024 {
			truncated = true
			return io.EOF
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 64*1024)
		line := 0
		for scanner.Scan() {
			line++
			text := scanner.Text()
			if strings.ContainsRune(text, 0) {
				break
			}
			if pattern.MatchString(text) {
				rel, _ := filepath.Rel(t.workspace, path)
				if len(text) > 2048 {
					text = text[:2048]
					truncated = true
				}
				matches = append(matches, match{rel, line, text})
				if len(matches) >= 100 {
					truncated = true
					return io.EOF
				}
			}
		}
		if err := scanner.Err(); err != nil {
			truncated = true
		}
		return nil
	})
	if err == io.EOF {
		err = nil
	}
	data, _ := json.Marshal(map[string]any{"matches": matches, "truncated": truncated})
	return data, err
}

type auditOutput struct {
	text      string
	truncated bool
}

func (o *auditOutput) Write(p []byte) (int, error) {
	n := len(p)
	room := 16*1024 - len(o.text)
	if len(p) > room {
		p = p[:room]
		o.truncated = true
	}
	o.text += string(p)
	return n, nil
}
func auditToolDefinition(name string) providers.Tool {
	if name == "audit_git" {
		return providers.Tool{Name: name, Description: "Inspect fixed git status, diff, or recent log in the workspace. No arbitrary command or git arguments.", Parameters: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["status","diff","log"]}},"required":["action"],"additionalProperties":false}`)}
	}
	return providers.Tool{Name: name, Description: "Search workspace regular text files with a Go regular expression; results include paths and one-based lines. Hidden/dependency directories and symlinks are skipped; output is bounded.", Parameters: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"],"additionalProperties":false}`)}
}
