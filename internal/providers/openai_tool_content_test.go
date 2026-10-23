package providers

import (
	"encoding/json"
	"testing"
)

func TestOpenAIToolCallsAcceptNullOrOmittedContent(t *testing.T) {
	for _, content := range []string{`"content":null,`, ``, `"content":"",`} {
		t.Run(content, func(t *testing.T) {
			payload := `{"id":"response","model":"test","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant",` + content + `"tool_calls":[{"id":"tool-1","type":"function","function":{"name":"fs","arguments":"{\"action\":\"read\",\"path\":\"file\"}"}}]}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`
			var wire openaiResponse
			if err := json.Unmarshal([]byte(payload), &wire); err != nil {
				t.Fatal(err)
			}
			parsed, err := (&OpenAIProvider{}).parseResponse(&wire)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Content != "" || len(parsed.ToolCalls) != 1 || parsed.ToolCalls[0].ID != "tool-1" || parsed.ToolCalls[0].Name != "fs" || parsed.ToolCalls[0].Arguments != `{"action":"read","path":"file"}` {
				t.Fatalf("tool call lost: %+v", parsed)
			}
			if parsed.Usage.InputTokens != 7 || parsed.Usage.OutputTokens != 3 {
				t.Fatalf("usage lost: %+v", parsed.Usage)
			}
		})
	}
}

func TestOpenAIRejectsMalformedNonNullContent(t *testing.T) {
	for _, reason := range []string{"stop", "tool_calls", "length"} {
		for _, content := range []string{`42`, `false`, `{}`, `[]`} {
			payload := `{"choices":[{"finish_reason":"` + reason + `","message":{"role":"assistant","content":` + content + `,"tool_calls":[{"id":"tool-1","type":"function","function":{"name":"fs","arguments":"{}"}}]}}]}`
			var wire openaiResponse
			if err := json.Unmarshal([]byte(payload), &wire); err != nil {
				t.Fatal(err)
			}
			if parsed, err := (&OpenAIProvider{}).parseResponse(&wire); err == nil {
				t.Fatalf("malformed content accepted: %s %s %+v", reason, content, parsed)
			}
		}
	}
}

func TestOpenAIRejectsTruncatedMissingContentWithoutTools(t *testing.T) {
	var wire openaiResponse
	if err := json.Unmarshal([]byte(`{"choices":[{"finish_reason":"length","message":{"content":null}}]}`), &wire); err != nil {
		t.Fatal(err)
	}
	if _, err := (&OpenAIProvider{}).parseResponse(&wire); err == nil {
		t.Fatal("missing truncated response accepted")
	}
}
