// Package testfixture supplies a controlled local MCP subprocess for acceptance tests.
// Production code never imports or launches it.
package testfixture

import (
	"bufio"
	"encoding/json"
	"os"
	"strconv"
	"time"
)

func Run() bool {
	if os.Getenv("OVERKILL_MCP_CONTROLLED_FIXTURE") != "1" {
		return false
	}
	if marker := os.Getenv("OVERKILL_MCP_FIXTURE_LAUNCHED"); marker != "" {
		_ = os.WriteFile(marker, []byte("started"), 0600)
	}
	if delay, _ := strconv.Atoi(os.Getenv("OVERKILL_MCP_FIXTURE_DELAY_MS")); delay > 0 {
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		if len(request.ID) == 0 {
			continue
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}}
		case "tools/list":
			phase, _ := os.ReadFile(os.Getenv("OVERKILL_MCP_FIXTURE_PHASE"))
			if string(phase) == "crash" {
				return true
			}
			names := []string{"read", "forbidden", "nop"}
			if string(phase) == "old" || string(phase) == "new" {
				names = []string{string(phase), "collision"}
			}
			catalog := make([]any, 0)
			for _, name := range names {
				catalog = append(catalog, map[string]any{"name": name, "description": "Controlled local tool " + name, "inputSchema": json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}},"nested":{"type":"object","properties":{"a/b":{"type":"string"}}}}}`)})
			}
			result = map[string]any{"tools": catalog}
		case "tools/call":
			if marker := os.Getenv("OVERKILL_MCP_FIXTURE_EFFECTS"); marker != "" {
				file, err := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					return true
				}
				_, _ = file.Write(append(request.Params, '\n'))
				_ = file.Close()
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(request.Params)}}}
		default:
			result = map[string]any{}
		}
		if encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}) != nil {
			return true
		}
	}
	return true
}
