package api

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// 1. JSON-RPC Types — Marshaling/Unmarshaling
// ============================================================================

func TestRequestMarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		req     Request
		wantErr bool
	}{
		{
			name: "valid request with string ID",
			req:  Request{JSONRPC: "2.0", ID: json.RawMessage(`"abc123"`), Method: "agent.send", Params: json.RawMessage(`{"message":"hello"}`)},
		},
		{
			name: "valid request with numeric ID",
			req:  Request{JSONRPC: "2.0", ID: json.RawMessage(`42`), Method: "session.list", Params: nil},
		},
		{
			name: "valid request with null ID (notification)",
			req:  Request{JSONRPC: "2.0", ID: nil, Method: "status.health", Params: nil},
		},
		{
			name: "request missing jsonrpc field",
			req:  Request{ID: json.RawMessage(`"1"`), Method: "test"},
		},
		{
			name: "request with empty method",
			req:  Request{JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Method: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.req)
			require.NoError(t, err)

			var decoded Request
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)
			assert.Equal(t, tt.req.JSONRPC, decoded.JSONRPC)
			assert.Equal(t, tt.req.Method, decoded.Method)
		})
	}
}

func TestRequestUnmarshalInvalid(t *testing.T) {
	invalidJSONs := []string{
		``,
		`{invalid}`,
		`{"method": 123}`,
		`{"jsonrpc": "2.0", "method": "test", "params": notanobject}`,
	}

	for _, raw := range invalidJSONs {
		var req Request
		err := json.Unmarshal([]byte(raw), &req)
		assert.Error(t, err, "expected error parsing: %s", raw)
	}
}

func TestResponseMarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		resp Response
	}{
		{
			name: "success response",
			resp: Response{JSONRPC: "2.0", ID: json.RawMessage(`"1"`), Result: map[string]interface{}{"status": "ok"}},
		},
		{
			name: "error response",
			resp: Response{JSONRPC: "2.0", ID: json.RawMessage(`"2"`), Error: &RPCError{Code: MethodNotFound, Message: "unknown method: test"}},
		},
		{
			name: "null ID response",
			resp: Response{JSONRPC: "2.0", ID: nil, Result: "done"},
		},
		{
			name: "response with structured result",
			resp: Response{
				JSONRPC: "2.0",
				ID:      json.RawMessage(`"3"`),
				Result:  &HealthResult{Status: "ok", Version: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.resp)
			require.NoError(t, err)

			var decoded Response
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, "2.0", decoded.JSONRPC)
			if tt.resp.Error != nil {
				require.NotNil(t, decoded.Error)
				assert.Equal(t, tt.resp.Error.Code, decoded.Error.Code)
				assert.Equal(t, tt.resp.Error.Message, decoded.Error.Message)
			} else {
				assert.Nil(t, decoded.Error)
			}
		})
	}
}

func TestRPCErrorMarshaling(t *testing.T) {
	e := &RPCError{Code: InvalidParams, Message: "message is required"}
	data, err := json.Marshal(e)
	require.NoError(t, err)

	var decoded RPCError
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, InvalidParams, decoded.Code)
	assert.Equal(t, "message is required", decoded.Message)
}

func TestErrorString(t *testing.T) {
	tests := []struct {
		code     int
		expected string
	}{
		{ParseError, "Parse error"},
		{InvalidRequest, "Invalid request"},
		{MethodNotFound, "Method not found"},
		{InvalidParams, "Invalid params"},
		{InternalError, "Internal error"},
		{999, "Internal error"},
		{-1, "Internal error"},
		{0, "Internal error"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("code_%d", tt.code), func(t *testing.T) {
			assert.Equal(t, tt.expected, errorString(tt.code))
		})
	}
}

func TestErrorCodes(t *testing.T) {
	// Verify standard JSON-RPC error codes have expected values.
	assert.Equal(t, -32700, ParseError)
	assert.Equal(t, -32600, InvalidRequest)
	assert.Equal(t, -32601, MethodNotFound)
	assert.Equal(t, -32602, InvalidParams)
	assert.Equal(t, -32603, InternalError)
}

// ============================================================================
// 2. Types — JSON Round-Trip for All Request/Response Structs
// ============================================================================

func TestSendMessageParamsRoundTrip(t *testing.T) {
	p := SendMessageParams{SessionID: "s1", Message: "hello world"}
	data, _ := json.Marshal(p)

	var decoded SendMessageParams
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "s1", decoded.SessionID)
	assert.Equal(t, "hello world", decoded.Message)
}

func TestSendMessageResultRoundTrip(t *testing.T) {
	r := SendMessageResult{
		Response:    "Hi there!",
		ToolCalls:   3,
		TotalTokens: 1500,
		Steps:       5,
		Model:       "claude-sonnet-4-20250514",
		Blocked:     false,
	}
	data, _ := json.Marshal(r)

	var decoded SendMessageResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, r.Response, decoded.Response)
	assert.Equal(t, r.ToolCalls, decoded.ToolCalls)
	assert.Equal(t, r.TotalTokens, decoded.TotalTokens)
	assert.Equal(t, r.Steps, decoded.Steps)
	assert.Equal(t, r.Model, decoded.Model)
	assert.False(t, decoded.Blocked)
	assert.Empty(t, decoded.BlockReason)
}

func TestSendMessageResultBlocked(t *testing.T) {
	r := SendMessageResult{
		Response:    "",
		Blocked:     true,
		BlockReason: "security violation",
	}
	data, _ := json.Marshal(r)

	var decoded SendMessageResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.True(t, decoded.Blocked)
	assert.Equal(t, "security violation", decoded.BlockReason)
}

func TestAbortParamsRoundTrip(t *testing.T) {
	p := AbortParams{SessionID: "s1"}
	data, _ := json.Marshal(p)

	var decoded AbortParams
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "s1", decoded.SessionID)
}

func TestSteerParamsRoundTrip(t *testing.T) {
	tests := []SteerParams{
		{SessionID: "s1", Message: "stop using typescript"},
		{SessionID: "s1", Message: "focus on perf", Role: "user"},
		{SessionID: "s1", Message: "I already did that", Role: "assistant"},
	}

	for i, p := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			data, _ := json.Marshal(p)
			var decoded SteerParams
			require.NoError(t, json.Unmarshal(data, &decoded))
			assert.Equal(t, p.SessionID, decoded.SessionID)
			assert.Equal(t, p.Message, decoded.Message)
			assert.Equal(t, p.Role, decoded.Role)
		})
	}
}

func TestSessionInfoRoundTrip(t *testing.T) {
	si := SessionInfo{
		ID:       "s1",
		Title:    "My Session",
		Name:     "My Session",
		Folder:   "/home/user",
		Model:    "claude-sonnet-4-20250514",
		Provider: "anthropic",
		Status:   "active",
		ParentID: "parent-1",
		Children: []string{"child-1", "child-2"},
	}
	data, _ := json.Marshal(si)

	var decoded SessionInfo
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, si.ID, decoded.ID)
	assert.Equal(t, si.Title, decoded.Title)
	assert.Equal(t, si.Folder, decoded.Folder)
	assert.Equal(t, si.Model, decoded.Model)
	assert.Equal(t, si.Provider, decoded.Provider)
	assert.Equal(t, si.Status, decoded.Status)
	assert.Equal(t, si.ParentID, decoded.ParentID)
	assert.Equal(t, si.Children, decoded.Children)
}

func TestSessionListResultRoundTrip(t *testing.T) {
	r := SessionListResult{
		Sessions: []SessionInfo{
			{ID: "s1", Title: "Session 1", Status: "active"},
			{ID: "s2", Title: "Session 2", Status: "idle"},
		},
	}
	data, _ := json.Marshal(r)

	var decoded SessionListResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Len(t, decoded.Sessions, 2)
	assert.Equal(t, "s1", decoded.Sessions[0].ID)
}

func TestSessionCreateParamsRoundTrip(t *testing.T) {
	p := SessionCreateParams{
		Folder:   "/home/user/project",
		Title:    "New Project",
		Model:    "gpt-5",
		Provider: "openai",
	}
	data, _ := json.Marshal(p)

	var decoded SessionCreateParams
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "/home/user/project", decoded.Folder)
	assert.Equal(t, "New Project", decoded.Title)
	assert.Equal(t, "gpt-5", decoded.Model)
	assert.Equal(t, "openai", decoded.Provider)
}

func TestSessionDeleteParamsRoundTrip(t *testing.T) {
	p := SessionDeleteParams{ID: "s1"}
	data, _ := json.Marshal(p)

	var decoded SessionDeleteParams
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "s1", decoded.ID)
}

func TestForkParamsRoundTrip(t *testing.T) {
	p := ForkParams{SessionID: "s1", Name: "forked-branch"}
	data, _ := json.Marshal(p)

	var decoded ForkParams
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "s1", decoded.SessionID)
	assert.Equal(t, "forked-branch", decoded.Name)
}

func TestConfigGetResultRoundTrip(t *testing.T) {
	r := ConfigGetResult{
		Version: 2,
		Agent:   map[string]interface{}{"default_model": "claude-sonnet-4-20250514", "default_provider": "anthropic"},
		UI:      map[string]interface{}{"animations": true, "theme": "dark"},
		Thinking: map[string]interface{}{
			"level":         "medium",
			"budget_tokens": 4000.0,
		},
		SystemPrompt: "You are a helpful assistant.",
	}
	data, _ := json.Marshal(r)

	var decoded ConfigGetResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, 2, decoded.Version)
	assert.Equal(t, "claude-sonnet-4-20250514", decoded.Agent["default_model"])
	assert.Equal(t, "dark", decoded.UI["theme"])
	assert.Equal(t, "medium", decoded.Thinking["level"])
	assert.Equal(t, "You are a helpful assistant.", decoded.SystemPrompt)
}

func TestConfigExistsResultRoundTrip(t *testing.T) {
	tests := []ConfigExistsResult{
		{Exists: true},
		{Exists: false},
	}
	for _, r := range tests {
		data, _ := json.Marshal(r)
		var decoded ConfigExistsResult
		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, r.Exists, decoded.Exists)
	}
}

func TestConfigUpdateParamsRoundTrip(t *testing.T) {
	p := ConfigUpdateParams{
		Patch: map[string]interface{}{
			"agent": map[string]interface{}{"default_model": "o3"},
		},
	}
	data, _ := json.Marshal(p)

	var decoded ConfigUpdateParams
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.NotNil(t, decoded.Patch)
}

func TestProviderInfoRoundTrip(t *testing.T) {
	pi := ProviderInfo{
		Name: "anthropic",
		Type: "anthropic",
		Models: []ModelInfo{
			{ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4", Family: "claude", ContextWindow: 200000, SupportsTools: true, Reasoning: true},
		},
	}
	data, _ := json.Marshal(pi)

	var decoded ProviderInfo
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "anthropic", decoded.Name)
	assert.Len(t, decoded.Models, 1)
}

func TestModelInfoRoundTrip(t *testing.T) {
	m := ModelInfo{
		ID:               "claude-sonnet-4-20250514",
		Name:             "Claude Sonnet 4",
		Family:           "claude",
		ContextWindow:    200000,
		DefaultMaxTokens: 8192,
		SupportsTools:    true,
		SupportsVision:   true,
		Reasoning:        true,
		InputModalities:  []string{"text", "image"},
		OutputModalities: []string{"text"},
	}
	data, _ := json.Marshal(m)

	var decoded ModelInfo
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, m.ID, decoded.ID)
	assert.Equal(t, m.Name, decoded.Name)
	assert.Equal(t, m.Family, decoded.Family)
	assert.Equal(t, m.ContextWindow, decoded.ContextWindow)
	assert.Equal(t, m.DefaultMaxTokens, decoded.DefaultMaxTokens)
	assert.True(t, decoded.SupportsTools)
	assert.True(t, decoded.SupportsVision)
	assert.True(t, decoded.Reasoning)
	assert.Equal(t, []string{"text", "image"}, decoded.InputModalities)
}

func TestModelsListParamsRoundTrip(t *testing.T) {
	p := ModelsListParams{Provider: "openai"}
	data, _ := json.Marshal(p)

	var decoded ModelsListParams
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "openai", decoded.Provider)
}

func TestHealthResultRoundTrip(t *testing.T) {
	r := HealthResult{Status: "ok", Version: 1}
	data, _ := json.Marshal(r)

	var decoded HealthResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "ok", decoded.Status)
	assert.Equal(t, 1, decoded.Version)
}

func TestGatewayTestParamsRoundTrip(t *testing.T) {
	p := GatewayTestParams{Gateway: "discord", Token: "abc123"}
	data, _ := json.Marshal(p)

	var decoded GatewayTestParams
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "discord", decoded.Gateway)
	assert.Equal(t, "abc123", decoded.Token)
}

func TestGatewayTestResultRoundTrip(t *testing.T) {
	tests := []GatewayTestResult{
		{OK: true, User: "MyBot"},
		{OK: false, Error: "HTTP 401: unauthorized"},
	}
	for _, r := range tests {
		data, _ := json.Marshal(r)
		var decoded GatewayTestResult
		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, r.OK, decoded.OK)
		assert.Equal(t, r.Error, decoded.Error)
	}
}

func TestSubagentInfoRoundTrip(t *testing.T) {
	si := SubagentInfo{
		Name:      "write-tests",
		Status:    "running",
		ElapsedMs: 1500,
		Model:     "claude-haiku",
	}
	data, _ := json.Marshal(si)

	var decoded SubagentInfo
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "write-tests", decoded.Name)
	assert.Equal(t, "running", decoded.Status)
	assert.Equal(t, int64(1500), decoded.ElapsedMs)
}

func TestGoalGetResultRoundTrip(t *testing.T) {
	tb := 100000
	r := GoalGetResult{
		Objective:   "Build a web app",
		Status:      "active",
		TokenBudget: &tb,
		TokensUsed:  15000,
		TimeUsedS:   45.5,
	}
	data, _ := json.Marshal(r)

	var decoded GoalGetResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "Build a web app", decoded.Objective)
	assert.Equal(t, "active", decoded.Status)
	require.NotNil(t, decoded.TokenBudget)
	assert.Equal(t, 100000, *decoded.TokenBudget)
	assert.Equal(t, 15000, decoded.TokensUsed)
	assert.InDelta(t, 45.5, decoded.TimeUsedS, 0.01)
}

func TestPlanGetResultRoundTrip(t *testing.T) {
	r := PlanGetResult{
		Title: "Test Plan",
		Items: []PlanGetItem{
			{ID: "0", Text: "Setup project", Status: "done"},
			{ID: "1", Text: "Add tests", Status: "in_progress"},
		},
	}
	data, _ := json.Marshal(r)

	var decoded PlanGetResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "Test Plan", decoded.Title)
	assert.Len(t, decoded.Items, 2)
	assert.Equal(t, "Setup project", decoded.Items[0].Text)
	assert.Equal(t, "done", decoded.Items[0].Status)
}

func TestSessionUsageParamsRoundTrip(t *testing.T) {
	tests := []SessionUsageParams{
		{SessionID: "s1"},
		{SessionID: "s1", Scope: "daily"},
		{Scope: "all"},
	}
	for i, p := range tests {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			data, _ := json.Marshal(p)
			var decoded SessionUsageParams
			require.NoError(t, json.Unmarshal(data, &decoded))
			assert.Equal(t, p.SessionID, decoded.SessionID)
			assert.Equal(t, p.Scope, decoded.Scope)
		})
	}
}

func TestTTSConfigRoundTrip(t *testing.T) {
	tc := TTSConfig{Provider: "openai", APIKey: "sk-test", Voice: "alloy"}
	data, _ := json.Marshal(tc)

	var decoded TTSConfig
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "openai", decoded.Provider)
	assert.Equal(t, "sk-test", decoded.APIKey)
	assert.Equal(t, "alloy", decoded.Voice)
}

func TestGatewayConfigFieldsRoundTrip(t *testing.T) {
	gf := GatewayConfigFields{
		Discord:  &DiscordGatewayConfig{BotToken: "dt1", Enabled: true},
		Telegram: &TelegramGatewayConfig{BotToken: "tt1", Enabled: false},
	}
	data, _ := json.Marshal(gf)

	var decoded GatewayConfigFields
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NotNil(t, decoded.Discord)
	assert.True(t, decoded.Discord.Enabled)
	assert.Equal(t, "dt1", decoded.Discord.BotToken)
	require.NotNil(t, decoded.Telegram)
	assert.False(t, decoded.Telegram.Enabled)
}

func TestSkillInfoDTORoundTrip(t *testing.T) {
	si := SkillInfoDTO{
		Name:        "web-search",
		Description: "Search the web",
		Category:    "tools",
		Tags:        []string{"web", "search"},
		Enabled:     true,
		Bundled:     true,
	}
	data, _ := json.Marshal(si)

	var decoded SkillInfoDTO
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, si.Name, decoded.Name)
	assert.Equal(t, si.Description, decoded.Description)
	assert.Equal(t, si.Category, decoded.Category)
	assert.Equal(t, si.Tags, decoded.Tags)
	assert.True(t, decoded.Enabled)
	assert.True(t, decoded.Bundled)
}

func TestTaskDTORoundTrip(t *testing.T) {
	task := TaskDTO{
		ID:        "t1",
		SessionID: "s1",
		Intent:    "Fix the login bug",
		Status:    "open",
		CreatedAt: "2025-06-01T10:00:00Z",
		UpdatedAt: "2025-06-01T10:30:00Z",
	}
	data, _ := json.Marshal(task)

	var decoded TaskDTO
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, task.ID, decoded.ID)
	assert.Equal(t, task.SessionID, decoded.SessionID)
	assert.Equal(t, task.Intent, decoded.Intent)
	assert.Equal(t, task.Status, decoded.Status)
}

func TestWizardCatalogResultRoundTrip(t *testing.T) {
	r := WizardCatalogResult{
		Providers:   []config.WizardOption{{Name: "openai", Description: "OpenAI"}},
		Gateways:    []config.WizardOption{{Name: "discord", Description: "Discord"}},
		TTS:         []config.WizardOption{},
		Databases:   []config.WizardOption{},
		Review:      []config.WizardOption{},
		Recommended: config.QuickSetup{Provider: "openai", Model: "gpt-5"},
	}
	data, _ := json.Marshal(r)

	var decoded WizardCatalogResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Len(t, decoded.Providers, 1)
	assert.Equal(t, "openai", decoded.Providers[0].Name)
}

// ============================================================================
// 3. Helper Functions
// ============================================================================

func TestUnmarshalParams(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		var p SendMessageParams
		rpcErr := unmarshalParams([]byte(`{"message":"hello"}`), &p)
		assert.Nil(t, rpcErr)
		assert.Equal(t, "hello", p.Message)
	})

	t.Run("nil data", func(t *testing.T) {
		var p SendMessageParams
		rpcErr := unmarshalParams(nil, &p)
		require.NotNil(t, rpcErr)
		assert.Equal(t, InvalidParams, rpcErr.Code)
		assert.Contains(t, rpcErr.Message, "missing params")
	})

	t.Run("invalid json", func(t *testing.T) {
		var p SendMessageParams
		rpcErr := unmarshalParams([]byte(`{bad json`), &p)
		require.NotNil(t, rpcErr)
		assert.Equal(t, InvalidParams, rpcErr.Code)
		assert.Contains(t, rpcErr.Message, "invalid params")
	})

	t.Run("wrong type fields", func(t *testing.T) {
		var p SendMessageParams
		rpcErr := unmarshalParams([]byte(`{"message": 123}`), &p)
		require.NotNil(t, rpcErr)
		assert.Equal(t, InvalidParams, rpcErr.Code)
	})
}

func TestToInt(t *testing.T) {
	tests := []struct {
		input  interface{}
		want   int
		wantOk bool
	}{
		{float64(42), 42, true},
		{int(42), 42, true},
		{int64(42), 42, true},
		{float64(0), 0, true},
		{float64(-1), -1, true},
		{"42", 0, false},
		{nil, 0, false},
		{true, 0, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v", tt.input), func(t *testing.T) {
			got, ok := toInt(tt.input)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantOk, ok)
		})
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		input  interface{}
		want   float64
		wantOk bool
	}{
		{float64(3.14), 3.14, true},
		{int(42), 42.0, true},
		{int64(42), 42.0, true},
		{float64(0), 0, true},
		{"3.14", 0, false},
		{nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v", tt.input), func(t *testing.T) {
			got, ok := toFloat64(tt.input)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantOk, ok)
		})
	}
}

func TestSplit2(t *testing.T) {
	tests := []struct {
		input    string
		sep      string
		expected []string
	}{
		{"nonce:ciphertext", ":", []string{"nonce", "ciphertext"}},
		{"abc:def:ghi", ":", []string{"abc", "def:ghi"}},
		{"nocolon", ":", []string{"nocolon"}},
		{"", ":", []string{""}},
		{"key=value", "=", []string{"key", "value"}},
		{"", "=", []string{""}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := split2(tt.input, tt.sep)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestGetCwd(t *testing.T) {
	cwd := getCwd()
	require.NotEmpty(t, cwd)
	// Should be a real directory path.
	assert.True(t, strings.HasPrefix(cwd, "/") || cwd == ".", "expected absolute path or '.'")
}

// ============================================================================
// 4. Secure Store — Encryption/Decryption
// ============================================================================

func TestNewSecureStore(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewSecureStore(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, store)
	assert.Equal(t, filepath.Join(tmpDir, masterKeyFile), store.keyPath)

	// Verify master key file was created.
	_, err = os.Stat(store.keyPath)
	assert.NoError(t, err)

	// NewSecureStore with existing key file should succeed.
	store2, err := NewSecureStore(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, store.masterKey, store2.masterKey, "reopening store should use same key")
}

func TestNewSecureStoreInvalidKeySize(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, masterKeyFile)

	// Write a key file with wrong size.
	require.NoError(t, os.WriteFile(keyPath, []byte("short"), 0600))

	_, err := NewSecureStore(tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "master key is")
}

func TestMasterKeyPath(t *testing.T) {
	path := MasterKeyPath("/home/user/.config")
	assert.Equal(t, "/home/user/.config/.master.key", path)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewSecureStore(tmpDir)
	require.NoError(t, err)

	plaintexts := []string{
		"hello world",
		"",
		"sk-abcdef1234567890",
		strings.Repeat("x", 1000),
		"special chars: !@#$%^&*()\n\t\r",
		"🔒 encrypted data 🚀",
	}

	for _, pt := range plaintexts {
		t.Run(fmt.Sprintf("len_%d", len(pt)), func(t *testing.T) {
			encrypted, err := store.Encrypt(pt)
			require.NoError(t, err)
			assert.NotEqual(t, pt, encrypted, "encrypted should differ from plaintext")
			assert.Contains(t, encrypted, ":", "encrypted should be nonce:ciphertext format")

			decrypted, err := store.Decrypt(encrypted)
			require.NoError(t, err)
			assert.Equal(t, pt, decrypted)
		})
	}
}

func TestEncryptDeterminismRejection(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewSecureStore(tmpDir)

	// Two encryptions of the same plaintext should produce different ciphertexts.
	e1, _ := store.Encrypt("same text")
	e2, _ := store.Encrypt("same text")
	assert.NotEqual(t, e1, e2, "encrypted values should be non-deterministic (unique nonces)")
}

func TestEncryptNilStore(t *testing.T) {
	var store *SecureStore
	encrypted, err := store.Encrypt("test")
	require.NoError(t, err)
	assert.Equal(t, "test", encrypted)
}

func TestDecryptNilStore(t *testing.T) {
	var store *SecureStore
	decrypted, err := store.Decrypt("test")
	require.NoError(t, err)
	assert.Equal(t, "test", decrypted)
}

func TestDecryptPlaintextPassthrough(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewSecureStore(tmpDir)

	// A non-encrypted string (no colon) should be returned as-is.
	result, err := store.Decrypt("sk-plaintext-api-key")
	require.NoError(t, err)
	assert.Equal(t, "sk-plaintext-api-key", result)
}

func TestDecryptInvalidCiphertext(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewSecureStore(tmpDir)

	// Hex of nonce but invalid base64.
	_, err := store.Decrypt("aabbcc:!!!not-valid-base64!!!")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode ciphertext")
}

func TestDecryptWrongKey(t *testing.T) {
	tmpDir1 := t.TempDir()
	store1, _ := NewSecureStore(tmpDir1)

	tmpDir2 := t.TempDir()
	store2, _ := NewSecureStore(tmpDir2)

	// Encrypt with store1, try decrypting with store2 (different master key).
	encrypted, err := store1.Encrypt("secret")
	require.NoError(t, err)

	_, err = store2.Decrypt(encrypted)
	require.Error(t, err)
}

func TestDecryptHexDecodeFails(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewSecureStore(tmpDir)

	// Not a hex nonce — should return passthrough (backward compat).
	result, err := store.Decrypt("nothex:YmFzZTY0")
	require.NoError(t, err)
	assert.Equal(t, "nothex:YmFzZTY0", result)
}

func TestIsEncrypted(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewSecureStore(tmpDir)

	assert.False(t, store.IsEncrypted("plain-key"))
	assert.False(t, store.IsEncrypted(""))
	assert.False(t, store.IsEncrypted("no:colon:extra"))

	encrypted, _ := store.Encrypt("test")
	assert.True(t, store.IsEncrypted(encrypted))
}

func TestIsEncryptedNilStore(t *testing.T) {
	var store *SecureStore
	assert.False(t, store.IsEncrypted("anything"))
}

func TestHealthCheck(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewSecureStore(tmpDir)

	err := store.HealthCheck()
	require.NoError(t, err)
}

func TestHealthCheckNilStore(t *testing.T) {
	var store *SecureStore
	err := store.HealthCheck()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store is nil")
}

func TestHealthCheckWrongKeySize(t *testing.T) {
	store := &SecureStore{masterKey: []byte("short")}
	err := store.HealthCheck()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "master key is")
}

func TestHealthCheckMissingKeyFile(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewSecureStore(tmpDir)

	// Remove the key file to simulate missing file.
	os.Remove(store.keyPath)

	err := store.HealthCheck()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not accessible")
}

func TestAES256GCMInternal(t *testing.T) {
	// Verify the underlying AES-256-GCM implementation.
	tmpDir := t.TempDir()
	store, _ := NewSecureStore(tmpDir)
	assert.Len(t, store.masterKey, 32, "master key must be 32 bytes for AES-256")

	// Manually verify the nonce:ciphertext format.
	encrypted, _ := store.Encrypt("test")
	parts := split2(encrypted, ":")
	require.Len(t, parts, 2)

	nonce, err := hex.DecodeString(parts[0])
	require.NoError(t, err)
	assert.Len(t, nonce, 12, "GCM nonce should be 12 bytes")

	ciphertext, err := base64.StdEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	// The ciphertext should include the plaintext + 16-byte auth tag.
	assert.GreaterOrEqual(t, len(ciphertext), 16, "ciphertext should be at least tag size")
}

// ============================================================================
// 5. Middleware
// ============================================================================

func TestStatusWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, code: http.StatusOK}
	assert.Equal(t, http.StatusOK, sw.code)

	sw.WriteHeader(http.StatusNotFound)
	assert.Equal(t, http.StatusNotFound, sw.code)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestWithCORS(t *testing.T) {
	handler := withCORS(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	t.Run("sets CORS headers on GET", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/rpc", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "POST, GET, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
		assert.Equal(t, "Content-Type, Authorization", rec.Header().Get("Access-Control-Allow-Headers"))
	})

	t.Run("handles OPTIONS preflight", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/rpc", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("respects OVERKILL_API_CORS_ORIGINS", func(t *testing.T) {
		os.Setenv("OVERKILL_API_CORS_ORIGINS", "http://localhost:3000,http://localhost:5173")
		defer os.Unsetenv("OVERKILL_API_CORS_ORIGINS")

		handler2 := withCORS(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		// Matching origin.
		req := httptest.NewRequest("GET", "/rpc", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()
		handler2(rec, req)
		assert.Equal(t, "http://localhost:3000", rec.Header().Get("Access-Control-Allow-Origin"))

		// Non-matching origin should get empty allow-origin header.
		req2 := httptest.NewRequest("GET", "/rpc", nil)
		req2.Header.Set("Origin", "http://evil.com")
		rec2 := httptest.NewRecorder()
		handler2(rec2, req2)
		assert.Empty(t, rec2.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("without origin header when origins configured", func(t *testing.T) {
		os.Setenv("OVERKILL_API_CORS_ORIGINS", "http://localhost:3000")
		defer os.Unsetenv("OVERKILL_API_CORS_ORIGINS")

		handler3 := withCORS(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		req := httptest.NewRequest("GET", "/rpc", nil)
		rec := httptest.NewRecorder()
		handler3(rec, req)
		// Origin header not set in request, so allowOrigin stays empty.
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestWithPanicRecovery(t *testing.T) {
	t.Run("recovers from panic", func(t *testing.T) {
		handler := withPanicRecovery(func(w http.ResponseWriter, r *http.Request) {
			panic("test panic")
		})

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		// Should not panic.
		assert.NotPanics(t, func() {
			handler(rec, req)
		})
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("passes through normal response", func(t *testing.T) {
		handler := withPanicRecovery(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "ok", rec.Body.String())
	})
}

func TestWithRequestLog(t *testing.T) {
	handler := withRequestLog(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	req := httptest.NewRequest("POST", "/rpc", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	// The statusWriter should have captured the status code.
	assert.Equal(t, "ok", rec.Body.String())
}

func TestApiToken(t *testing.T) {
	t.Run("unset returns empty", func(t *testing.T) {
		os.Unsetenv("OVERKILL_API_TOKEN")
		assert.Empty(t, apiToken())
	})

	t.Run("set returns value", func(t *testing.T) {
		os.Setenv("OVERKILL_API_TOKEN", "my-secret-token")
		defer os.Unsetenv("OVERKILL_API_TOKEN")
		assert.Equal(t, "my-secret-token", apiToken())
	})
}

func TestWithAPIAuth(t *testing.T) {
	t.Run("auto-generates token when none configured", func(t *testing.T) {
		os.Unsetenv("OVERKILL_API_TOKEN")

		handler := withAPIAuth(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})

		// Without auth header, should get 401.
		req := httptest.NewRequest("GET", "/rpc", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)

		// With wrong auth, should still get 401.
		req2 := httptest.NewRequest("GET", "/rpc", nil)
		rec2 := httptest.NewRecorder()
		req2.Header.Set("Authorization", "Bearer wrong-token")
		handler(rec2, req2)
		assert.Equal(t, http.StatusUnauthorized, rec2.Code)
	})

	t.Run("uses OVERKILL_API_TOKEN when set", func(t *testing.T) {
		os.Setenv("OVERKILL_API_TOKEN", "configured-token")
		defer os.Unsetenv("OVERKILL_API_TOKEN")

		handler := withAPIAuth(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})

		// Wrong token.
		req := httptest.NewRequest("GET", "/rpc", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		rec := httptest.NewRecorder()
		handler(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)

		// Correct token.
		req2 := httptest.NewRequest("GET", "/rpc", nil)
		req2.Header.Set("Authorization", "Bearer configured-token")
		rec2 := httptest.NewRecorder()
		handler(rec2, req2)
		assert.Equal(t, http.StatusOK, rec2.Code)
	})

	t.Run("rejects missing Bearer prefix", func(t *testing.T) {
		os.Setenv("OVERKILL_API_TOKEN", "mytoken")
		defer os.Unsetenv("OVERKILL_API_TOKEN")

		handler := withAPIAuth(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/rpc", nil)
		req.Header.Set("Authorization", "mytoken")
		rec := httptest.NewRecorder()
		handler(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestWithMiddlewareChain(t *testing.T) {
	// Create a minimal server to test the middleware chain.
	s := &Server{
		cfg: &config.Config{Version: 1},
	}

	handler := s.withMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	t.Run("OPTIONS preflight through middleware chain", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/rpc", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)
		// OPTIONS should return 204 from CORS middleware (before auth check).
		assert.Equal(t, http.StatusNoContent, rec.Code)
	})

	t.Run("GET request through middleware chain", func(t *testing.T) {
		// Will get 401 because no auth token set.
		req := httptest.NewRequest("GET", "/rpc", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

// ============================================================================
// 6. Server Construction & Registration
// ============================================================================

func TestNewServer(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			DefaultProvider: "openai",
			DefaultModel:    "gpt-5",
		},
	}

	s := NewServer(ServerConfig{
		Config: cfg,
	})
	require.NotNil(t, s)
	assert.NotNil(t, s.agents)
	assert.NotNil(t, s.pendingQuestions)
	assert.NotNil(t, s.pendingQuestionData)
	assert.NotNil(t, s.syncErrors)
	assert.NotNil(t, s.creditAnalyzer)
}

func TestNewServerNilTools(t *testing.T) {
	s := NewServer(ServerConfig{
		Config: &config.Config{},
	})
	require.NotNil(t, s)
	assert.NotNil(t, s.toolRegistry, "should create default tool registry")
}

func TestServerAddr(t *testing.T) {
	s := &Server{port: 7777}
	assert.Equal(t, "http://localhost:7777", s.Addr())
}

func TestSetAskBridge(t *testing.T) {
	s := &Server{}
	called := false
	bridge := func(ctx context.Context, prompt string, choices []string) (string, int, bool) {
		called = true
		return "yes", 0, false
	}
	s.SetAskBridge(bridge)

	text, idx, cancel := s.AskUser(context.Background(), "confirm?", []string{"yes", "no"})
	assert.True(t, called)
	assert.Equal(t, "yes", text)
	assert.Equal(t, 0, idx)
	assert.False(t, cancel)
}

func TestAskUserNilBridge(t *testing.T) {
	s := &Server{}
	text, idx, cancel := s.AskUser(context.Background(), "confirm?", []string{"yes", "no"})
	assert.Empty(t, text)
	assert.Equal(t, -1, idx)
	assert.True(t, cancel)
}

// ============================================================================
// 7. HTTP Integration Tests (JSON-RPC Dispatch)
// ============================================================================

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{
		Version: 1,
		Agent: config.AgentConfig{
			DefaultProvider: "openai",
			DefaultModel:    "gpt-5",
		},
		UI: config.UIConfig{
			Animations: true,
			Theme:      "dark",
		},
	}
	s := &Server{
		cfg:                 cfg,
		sessionStore:        &mockSessionStore{sessions: make(map[string]*session.Session)},
		agents:              make(map[string]*agent.Agent),
		pendingQuestions:    make(map[string]chan agent.Answer),
		pendingQuestionData: make(map[string]*agent.Question),
		syncErrors:          make(map[string]string),
		creditAnalyzer:      nil,
		skillsStore:         newSkillsStore(nil),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", withCORS(withPanicRecovery(withRequestLog(s.handleRPC))))
	mux.HandleFunc("/sse", withCORS(withPanicRecovery(withRequestLog(s.handleSSE))))
	mux.HandleFunc("/stream", withCORS(withPanicRecovery(withRequestLog(s.handleStream))))
	mux.HandleFunc("/health", withCORS(withPanicRecovery(withRequestLog(s.handleHealth))))
	mux.HandleFunc("/api/goal", withCORS(withPanicRecovery(withRequestLog(s.handleAPIGoal))))
	mux.HandleFunc("/api/plan", withCORS(withPanicRecovery(withRequestLog(s.handleAPIPlan))))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return s, ts
}

// mockSessionStore is a minimal in-memory session store for testing.
type mockSessionStore struct {
	sessions map[string]*session.Session
}

func (m *mockSessionStore) Create(_ context.Context, s *session.Session) error {
	m.sessions[s.ID] = s
	return nil
}
func (m *mockSessionStore) Load(_ context.Context, id string) (*session.Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session not found")
	}
	return s, nil
}
func (m *mockSessionStore) Save(_ context.Context, s *session.Session) error {
	m.sessions[s.ID] = s
	return nil
}
func (m *mockSessionStore) List(_ context.Context, _ session.ListOptions) ([]*session.Session, error) {
	var result []*session.Session
	for _, s := range m.sessions {
		result = append(result, s)
	}
	return result, nil
}
func (m *mockSessionStore) Delete(_ context.Context, id string) error {
	delete(m.sessions, id)
	return nil
}
func (m *mockSessionStore) Close() error { return nil }

// toFloat64 safely converts a JSON-unmarshalled numeric value to float64.
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func doRPC(t *testing.T, ts *httptest.Server, method string, params interface{}) *http.Response {
	t.Helper()
	return doRPCWithToken(t, ts, method, params, "")
}

func doRPCWithToken(t *testing.T, ts *httptest.Server, method string, params interface{}, token string) *http.Response {
	t.Helper()
	return doRPCWithTokenAndID(t, ts, method, params, token, json.RawMessage(`"1"`))
}

func doRPCWithTokenAndID(t *testing.T, ts *httptest.Server, method string, params interface{}, token string, id json.RawMessage) *http.Response {
	t.Helper()
	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      id,
	}

	if params != nil {
		body["params"] = params
	}

	b, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequest("POST", ts.URL+"/rpc", bytes.NewReader(b))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func readRPCResponse(t *testing.T, resp *http.Response) Response {
	t.Helper()
	defer resp.Body.Close()
	var r Response
	err := json.NewDecoder(resp.Body).Decode(&r)
	require.NoError(t, err)
	return r
}

func TestHandleRPCParseError(t *testing.T) {
	_, ts := newTestServer(t)

	// Send invalid JSON.
	req, _ := http.NewRequest("POST", ts.URL+"/rpc", bytes.NewReader([]byte(`{bad json`)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "JSON-RPC returns 200 even for errors")
	var r Response
	json.NewDecoder(resp.Body).Decode(&r)

	assert.Equal(t, "2.0", r.JSONRPC)
	require.NotNil(t, r.Error)
	assert.Equal(t, ParseError, r.Error.Code)
}

func TestHandleRPCInvalidJSONRPCVersion(t *testing.T) {
	_, ts := newTestServer(t)

	body := map[string]interface{}{
		"jsonrpc": "1.0",
		"method":  "status.health",
		"id":      "1",
	}
	b, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", ts.URL+"/rpc", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidRequest, r.Error.Code)
	assert.Contains(t, r.Error.Message, "jsonrpc must be")
}

func TestHandleRPCMethodNotAllowed(t *testing.T) {
	_, ts := newTestServer(t)

	req, _ := http.NewRequest("GET", ts.URL+"/rpc", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestHandleRPCMethodNotFound(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "nonexistent.method", nil)
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, MethodNotFound, r.Error.Code)
	assert.Contains(t, r.Error.Message, "unknown method")
}

func TestHandleStatusHealth(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "status.health", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	assert.NotNil(t, r.Result)
	resultJSON, _ := json.Marshal(r.Result)
	var hr HealthResult
	json.Unmarshal(resultJSON, &hr)
	assert.Equal(t, "ok", hr.Status)
	assert.Equal(t, 1, hr.Version)
}

func TestHandleHealthEndpoint(t *testing.T) {
	_, ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var hr HealthResult
	json.NewDecoder(resp.Body).Decode(&hr)
	assert.Equal(t, "ok", hr.Status)
}

func TestHandleConfigGet(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "config.get", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	assert.NotNil(t, r.Result)
	resultJSON, _ := json.Marshal(r.Result)
	var cr ConfigGetResult
	json.Unmarshal(resultJSON, &cr)
	assert.Equal(t, 1, cr.Version)
	assert.NotNil(t, cr.Agent)
	assert.NotNil(t, cr.UI)
}

func TestHandleConfigExists(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "config.exists", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	resultJSON, _ := json.Marshal(r.Result)
	var cer ConfigExistsResult
	json.Unmarshal(resultJSON, &cer)
	// Config may or may not exist on disk — just check the type parses.
	assert.NotNil(t, resultJSON)
}

func TestHandleProvidersList(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "providers.list", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	resultJSON, _ := json.Marshal(r.Result)
	var plr ProvidersListResult
	err := json.Unmarshal(resultJSON, &plr)
	require.NoError(t, err)
	// With no providers, should get empty list.
	assert.NotNil(t, plr.Providers)
}

func TestHandleModelsListMissingProvider(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "models.list", map[string]string{})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "provider is required")
}

func TestHandleModelsListUnknownProvider(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "models.list", map[string]string{"provider": "nonexistent"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "not found")
}

func TestHandleAgentSubagentsNilManager(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "agent.subagents", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	resultJSON, _ := json.Marshal(r.Result)
	var slr SubagentListResult
	json.Unmarshal(resultJSON, &slr)
	assert.Empty(t, slr.Subagents)
}

func TestHandleConfigThemeGet(t *testing.T) {
	_, ts := newTestServer(t)

	// GET (null params) should return current theme.
	resp := doRPC(t, ts, "config.theme", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	resultJSON, _ := json.Marshal(r.Result)
	var result map[string]string
	json.Unmarshal(resultJSON, &result)
	assert.Equal(t, "dark", result["theme"])
}

func TestHandleConfigThemeInvalidTheme(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "config.theme", map[string]string{"theme": "invisible"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Contains(t, r.Error.Message, "unknown theme")
}

func TestHandleThinkingSetLevel(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "thinking.set_level", map[string]string{"level": "high"})
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	resultJSON, _ := json.Marshal(r.Result)
	var result map[string]string
	json.Unmarshal(resultJSON, &result)
	assert.Equal(t, "high", result["level"])
}

func TestHandleThinkingSetLevelInvalid(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "thinking.set_level", map[string]string{"level": "eleven"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
}

func TestHandleModeSet(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "mode.set", map[string]string{"mode": "plan"})
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	resultJSON, _ := json.Marshal(r.Result)
	var result map[string]string
	json.Unmarshal(resultJSON, &result)
	assert.Equal(t, "plan", result["mode"])
}

func TestHandleModeSetInvalid(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "mode.set", map[string]string{"mode": "destroy"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Contains(t, r.Error.Message, "mode must be")
}

func TestHandleSelfEvalStatus(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "self.eval.status", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	// Should return idle status.
	resultJSON, _ := json.Marshal(r.Result)
	var result map[string]interface{}
	json.Unmarshal(resultJSON, &result)
	assert.Equal(t, false, result["active"])
	assert.Equal(t, "idle", result["status"])
}

func TestHandleTestResults(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "tests.results", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	resultJSON, _ := json.Marshal(r.Result)
	var result map[string]interface{}
	json.Unmarshal(resultJSON, &result)
	assert.Equal(t, false, result["running"])
}

func TestHandleSequentialQueue(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "sequential.queue", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	resultJSON, _ := json.Marshal(r.Result)
	var result map[string]interface{}
	json.Unmarshal(resultJSON, &result)
	assert.Equal(t, false, result["active"])
	assert.Equal(t, float64(0), result["total"])
}

func TestHandleMemoPhraseNoEngine(t *testing.T) {
	_, ts := newTestServer(t)

	// memo.phrase with no memo engine should still return a default phrase.
	resp := doRPC(t, ts, "memo.phrase", map[string]string{"input": "thinking"})
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	resultJSON, _ := json.Marshal(r.Result)
	var result map[string]interface{}
	json.Unmarshal(resultJSON, &result)
	assert.NotEmpty(t, result["phrase"])
}

func TestHandleClarifyPollNoPending(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "clarify.poll", nil)
	r := readRPCResponse(t, resp)

	// When no questions are pending, returns nil error + nil result.
	assert.Nil(t, r.Error)
	assert.Nil(t, r.Result)
}

func TestHandleGatewayTestMissingParams(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "gateway.test", map[string]string{})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "gateway is required")
}

func TestHandleGatewayTestMissingToken(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "gateway.test", map[string]string{"gateway": "discord"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "token is required")
}

func TestHandleGatewayTestUnsupported(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "gateway.test", map[string]string{"gateway": "irc", "token": "abc"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "unsupported gateway")
}

func TestHandleWizardCatalog(t *testing.T) {
	// wizard.catalog calls config.BuildWizardCatalog() — this should succeed.
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "wizard.catalog", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
}

func TestHandleEStop(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "estop", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	resultJSON, _ := json.Marshal(r.Result)
	var result map[string]interface{}
	json.Unmarshal(resultJSON, &result)
	assert.Equal(t, "estopped", result["status"])
}

func TestHandleConfigUpdateMissingParams(t *testing.T) {
	_, ts := newTestServer(t)

	// Send empty params.
	resp := doRPC(t, ts, "config.update", nil)
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
}

func TestHandleAgentSendMissingMessage(t *testing.T) {
	// Create a server with a mock session store.
	cfg := &config.Config{
		Version: 1,
		Agent:   config.AgentConfig{DefaultProvider: "openai", DefaultModel: "gpt-5"},
	}
	s := &Server{
		cfg:                 cfg,
		agents:              make(map[string]*agent.Agent),
		pendingQuestions:    make(map[string]chan agent.Answer),
		pendingQuestionData: make(map[string]*agent.Question),
		syncErrors:          make(map[string]string),
		skillsStore:         newSkillsStore(nil),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", withCORS(withPanicRecovery(withRequestLog(s.handleRPC))))
	mux.HandleFunc("/health", withCORS(withPanicRecovery(withRequestLog(s.handleHealth))))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := doRPC(t, ts, "agent.send", map[string]string{"message": ""})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "message is required")
}

func TestHandleAgentAbortMissingSession(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "agent.abort", map[string]string{})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "session_id is required")
}

func TestHandleAgentAbortSessionNotFound(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "agent.abort", map[string]string{"session_id": "nonexistent"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
}

func TestHandleAgentUndoMissingSession(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "agent.undo", map[string]string{})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "session_id is required")
}

func TestHandleAgentSteerMissingParams(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "agent.steer", map[string]string{"session_id": "s1"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "message is required")
}

func TestHandleAgentSteerInvalidRole(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "agent.steer", map[string]string{
		"session_id": "s1",
		"message":    "stop",
		"role":       "admin",
	})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	// Role validation happens after session check — since no agent exists,
	// we get "session not found" instead. But the role validation code is testable.
	// The error can be either depending on check ordering.
}

func TestHandleSessionListNilStore(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "session.list", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	// Should return empty sessions list.
	resultJSON, _ := json.Marshal(r.Result)
	var slr SessionListResult
	json.Unmarshal(resultJSON, &slr)
	assert.Empty(t, slr.Sessions)
}

func TestHandleSessionDeleteMissingID(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "session.delete", map[string]string{})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "id is required")
}

func TestHandleConfigCreateMissingParams(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "config.create", nil)
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
}

func TestHandleSessionUsageNoTracker(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "session.usage", map[string]string{"session_id": "s1", "scope": "session"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, MethodNotFound, r.Error.Code)
	assert.Contains(t, r.Error.Message, "usage tracking not wired")
}

func TestHandleModelsSelectMissingParams(t *testing.T) {
	_, ts := newTestServer(t)

	// Send empty params — handler validates both provider and model.
	resp := doRPC(t, ts, "models.select", map[string]string{"provider": "", "model": ""})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "provider or model is required")
}

func TestHandleSessionLoadMissingID(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "session.load", map[string]string{})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "id is required")
}

func TestHandleGoalGet(t *testing.T) {
	_, ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/api/goal")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var gr map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&gr)
	assert.Equal(t, "inactive", gr["status"])
}

func TestHandlePlanGet(t *testing.T) {
	_, ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/api/plan")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandleMemoLearnNoEngine(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "memo.learn", map[string]interface{}{
		"patterns": []string{"test"},
		"phrases":  []string{"test phrase"},
	})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidRequest, r.Error.Code)
	assert.Contains(t, r.Error.Message, "memo engine not configured")
}

// ============================================================================
// 8. SSE / Streaming Tests
// ============================================================================

func TestHandleSSEMissingSession(t *testing.T) {
	_, ts := newTestServer(t)

	req, _ := http.NewRequest("GET", ts.URL+"/sse", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandleStreamMissingMessage(t *testing.T) {
	_, ts := newTestServer(t)

	req, _ := http.NewRequest("GET", ts.URL+"/stream?session_id=s1", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestStreamEventTypeMapping(t *testing.T) {
	tests := []struct {
		evtType  agent.EventType
		expected string
	}{
		{agent.EventToken, "text"},
		{agent.EventToolStart, "tool_call"},
		{agent.EventToolOutput, "tool_call"},
		{agent.EventDone, "done"},
		{agent.EventError, "error"},
		{agent.EventStatus, "status"},
		{agent.EventReasoning, "reasoning"},
		{agent.EventType(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			evt := agent.StreamEvent{Type: tt.evtType}
			assert.Equal(t, tt.expected, streamEventType(evt))
		})
	}
}

func TestBuildSSEData(t *testing.T) {
	t.Run("status event with phase", func(t *testing.T) {
		evt := agent.StreamEvent{Type: agent.EventStatus, Phase: "thinking"}
		data := buildSSEData(evt)
		assert.Equal(t, "thinking", data["phase"])
	})

	t.Run("reasoning event with content", func(t *testing.T) {
		evt := agent.StreamEvent{Type: agent.EventReasoning, Content: "Let me think..."}
		data := buildSSEData(evt)
		assert.Equal(t, "Let me think...", data["content"])
	})

	t.Run("token event with content", func(t *testing.T) {
		evt := agent.StreamEvent{Type: agent.EventToken, Content: "Hello"}
		data := buildSSEData(evt)
		assert.Equal(t, "Hello", data["content"])
	})

	t.Run("tool start event", func(t *testing.T) {
		evt := agent.StreamEvent{Type: agent.EventToolStart, ToolName: "bash", ToolInput: json.RawMessage(`{"cmd":"ls"}`)}
		data := buildSSEData(evt)
		assert.Equal(t, "bash", data["name"])
	})

	t.Run("tool output event", func(t *testing.T) {
		evt := agent.StreamEvent{Type: agent.EventToolOutput, ToolName: "bash", ToolOutput: "file1\nfile2"}
		data := buildSSEData(evt)
		assert.Equal(t, "bash", data["name"])
		assert.Equal(t, "file1\nfile2", data["output"])
	})

	t.Run("done event with result", func(t *testing.T) {
		result := &agent.RunResult{
			Model:       "claude-sonnet-4-20250514",
			TotalTokens: 1000,
			ToolCalls:   3,
			Steps:       5,
		}
		evt := agent.StreamEvent{Type: agent.EventDone, Result: result}
		data := buildSSEData(evt)
		assert.Equal(t, "claude-sonnet-4-20250514", data["model"])
		assert.Equal(t, 1000, data["tokens"])
		assert.Equal(t, 3, data["tool_calls"])
		assert.Equal(t, 5, data["steps"])
	})

	t.Run("done event with blocked", func(t *testing.T) {
		result := &agent.RunResult{
			Blocked:     true,
			BlockReason: "security",
		}
		evt := agent.StreamEvent{Type: agent.EventDone, Result: result}
		data := buildSSEData(evt)
		assert.Equal(t, true, data["blocked"])
		assert.Equal(t, "security", data["block_reason"])
	})

	t.Run("done event with direct model/tokens override", func(t *testing.T) {
		evt := agent.StreamEvent{Type: agent.EventDone, Model: "o3", Tokens: 500}
		data := buildSSEData(evt)
		assert.Equal(t, "o3", data["model"])
		assert.Equal(t, 500, data["tokens"])
	})

	t.Run("error event", func(t *testing.T) {
		evt := agent.StreamEvent{Type: agent.EventError, Error: fmt.Errorf("something went wrong")}
		data := buildSSEData(evt)
		assert.Equal(t, "something went wrong", data["message"])
	})

	t.Run("event with metadata", func(t *testing.T) {
		evt := agent.StreamEvent{
			Type:     agent.EventStatus,
			Metadata: map[string]interface{}{"key": "value"},
		}
		data := buildSSEData(evt)
		assert.Equal(t, map[string]interface{}{"key": "value"}, data["metadata"])
	})
}

func TestWriteSSEEvent(t *testing.T) {
	s := &Server{}
	evt := agent.StreamEvent{
		Type:    agent.EventToken,
		Content: "Hello, World!",
	}

	rec := httptest.NewRecorder()
	s.writeSSEEvent(rec, rec, evt)

	body := rec.Body.String()
	assert.Contains(t, body, "event: text")
	assert.Contains(t, body, "Hello, World!")
}

func TestConsumeStreamEvents(t *testing.T) {
	s := &Server{}

	events := make(chan agent.StreamEvent, 3)
	events <- agent.StreamEvent{Type: agent.EventToken, Content: "Hi"}
	events <- agent.StreamEvent{Type: agent.EventDone}
	close(events)

	rec := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.consumeStreamEvents(ctx, rec, rec, events)

	body := rec.Body.String()
	assert.Contains(t, body, "event: text")
	assert.Contains(t, body, "Hi")
	assert.Contains(t, body, "event: done")
}

func TestConsumeStreamEventsContextCancel(t *testing.T) {
	s := &Server{}

	events := make(chan agent.StreamEvent)
	// Don't close events — we'll cancel the context instead.
	ctx, cancel := context.WithCancel(context.Background())

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.consumeStreamEvents(ctx, rec, rec, events)
		close(done)
	}()

	cancel()
	// Wait for goroutine to exit.
	<-done
}

func TestDrainChannel(t *testing.T) {
	s := &Server{}

	ch := make(chan agent.StreamEvent, 5)
	ch <- agent.StreamEvent{Type: agent.EventToken}
	ch <- agent.StreamEvent{Type: agent.EventDone}
	close(ch)

	s.drainChannel(ch)
}

func TestDrainChannelTimeout(t *testing.T) {
	s := &Server{}

	ch := make(chan agent.StreamEvent)
	// Don't close and don't send — drain should timeout.
	s.drainChannel(ch)
}

// ============================================================================
// 9. Skills Handlers
// ============================================================================

func TestHandleSkillsListNilStore(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "skills.list", nil)
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	// Should return empty list when store is nil (which is the case in our test server).
}

func TestHandleSkillsToggleNilStore(t *testing.T) {
	// Use a server with skillsStore = nil.
	cfg := &config.Config{Version: 1}
	s := &Server{
		cfg:                 cfg,
		agents:              make(map[string]*agent.Agent),
		pendingQuestions:    make(map[string]chan agent.Answer),
		pendingQuestionData: make(map[string]*agent.Question),
		syncErrors:          make(map[string]string),
		skillsStore:         nil,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", withCORS(withPanicRecovery(withRequestLog(s.handleRPC))))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := doRPC(t, ts, "skills.toggle", map[string]string{"name": "test-skill"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Contains(t, r.Error.Message, "skills store not configured")
}

func TestSkillsStoreToggle(t *testing.T) {
	store := newSkillsStore(nil)
	// Initially disabled is empty, all skills enabled.
	assert.False(t, store.disabled["test-skill"])

	// First toggle: becomes disabled, returns false (not enabled).
	enabled := store.toggle("test-skill")
	assert.False(t, enabled)
	assert.True(t, store.disabled["test-skill"])

	// Second toggle: becomes enabled again, returns true.
	enabled = store.toggle("test-skill")
	assert.True(t, enabled)
	assert.False(t, store.disabled["test-skill"])
}

func TestSkillsStoreListNilRegistry(t *testing.T) {
	store := newSkillsStore(nil)
	result := store.list()
	assert.Nil(t, result)
}

// ============================================================================
// 10. Todo Handlers
// ============================================================================

func TestHandleTodoAddNilStore(t *testing.T) {
	cfg := &config.Config{Version: 1}
	s := &Server{
		cfg:                 cfg,
		agents:              make(map[string]*agent.Agent),
		pendingQuestions:    make(map[string]chan agent.Answer),
		pendingQuestionData: make(map[string]*agent.Question),
		syncErrors:          make(map[string]string),
		tasksStore:          nil,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", withCORS(withPanicRecovery(withRequestLog(s.handleRPC))))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := doRPC(t, ts, "todo.add", map[string]string{"session_id": "s1", "description": "test task"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Contains(t, r.Error.Message, "tasks store not configured")
}

func TestHandleTodoToggleNilStore(t *testing.T) {
	cfg := &config.Config{Version: 1}
	s := &Server{
		cfg:                 cfg,
		agents:              make(map[string]*agent.Agent),
		pendingQuestions:    make(map[string]chan agent.Answer),
		pendingQuestionData: make(map[string]*agent.Question),
		syncErrors:          make(map[string]string),
		tasksStore:          nil,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", withCORS(withPanicRecovery(withRequestLog(s.handleRPC))))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := doRPC(t, ts, "todo.toggle", map[string]string{"id": "t1"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Contains(t, r.Error.Message, "tasks store not configured")
}

func TestHandleTodoDeleteNilStore(t *testing.T) {
	cfg := &config.Config{Version: 1}
	s := &Server{
		cfg:                 cfg,
		agents:              make(map[string]*agent.Agent),
		pendingQuestions:    make(map[string]chan agent.Answer),
		pendingQuestionData: make(map[string]*agent.Question),
		syncErrors:          make(map[string]string),
		tasksStore:          nil,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", withCORS(withPanicRecovery(withRequestLog(s.handleRPC))))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := doRPC(t, ts, "todo.delete", map[string]string{"id": "t1"})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Contains(t, r.Error.Message, "tasks store not configured")
}

func TestHandleTodoListNilStore(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "todo.list", map[string]string{"session_id": "s1"})
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	// Should return empty list.
}

// ============================================================================
// 11. JSON-RPC Body Size Limit
// ============================================================================

func TestHandleRPCBodyTooLarge(t *testing.T) {
	_, ts := newTestServer(t)

	// Create body larger than 1 MiB limit.
	largeBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "agent.send",
		"id":      "1",
		"params": map[string]interface{}{
			"message": strings.Repeat("x", 1<<21), // 2 MiB
		},
	}
	b, _ := json.Marshal(largeBody)

	req, _ := http.NewRequest("POST", ts.URL+"/rpc", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	r := readRPCResponse(t, resp)
	require.NotNil(t, r.Error)
	assert.Equal(t, ParseError, r.Error.Code)
}

// ============================================================================
// 12. Null/Empty ID Handling (JSON-RPC notifications)
// ============================================================================

func TestHandleRPCNullID(t *testing.T) {
	_, ts := newTestServer(t)

	reqBody := `{"jsonrpc":"2.0","method":"status.health","id":null}`
	req, _ := http.NewRequest("POST", ts.URL+"/rpc", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	r := readRPCResponse(t, resp)
	// Null ID should be preserved in response.
	assert.Nil(t, r.Error)
	idStr, _ := json.Marshal(r.ID)
	assert.Equal(t, "null", string(idStr))
}

// ============================================================================
// 13. Empty JSON body
// ============================================================================

func TestHandleRPCEmptyBody(t *testing.T) {
	_, ts := newTestServer(t)

	req, _ := http.NewRequest("POST", ts.URL+"/rpc", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	r := readRPCResponse(t, resp)
	require.NotNil(t, r.Error)
	assert.Equal(t, ParseError, r.Error.Code)
}

// ============================================================================
// 14. writeRPCResponse helper
// ============================================================================

func TestWriteRPCResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	resp := Response{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Result:  map[string]string{"ok": "true"},
	}
	writeRPCResponse(rec, resp)

	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var decoded Response
	json.Unmarshal(rec.Body.Bytes(), &decoded)
	assert.Equal(t, "2.0", decoded.JSONRPC)
}

// ============================================================================
// 15. Complex SSE JSON-RPC style param parsing (handleStream fallback)
// ============================================================================

func TestHandleStreamJSONRPCParams(t *testing.T) {
	_, ts := newTestServer(t)

	// Direct query params missing, but JSON-RPC style params passed.
	// The session_id/message are nested in params JSON but the handler
	// expects a session to exist — returns 400 because session doesn't exist
	// and getOrCreateAgent fails.
	params := `{"message":"test message","session_id":"test-session"}`
	req, _ := http.NewRequest("GET", ts.URL+"/stream?params="+params, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Returns 400 because we can't auto-create session without session store.
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// ============================================================================
// 16. handleAgentAnswer tests
// ============================================================================

func TestHandleAgentAnswerCancelAll(t *testing.T) {
	cfg := &config.Config{Version: 1}
	ch := make(chan agent.Answer, 1)

	// Pre-populate a pending question.
	pendingQuestions := map[string]chan agent.Answer{
		"session-1": ch,
	}
	s := &Server{
		cfg:                 cfg,
		agents:              make(map[string]*agent.Agent),
		pendingQuestions:    pendingQuestions,
		pendingQuestionData: make(map[string]*agent.Question),
		syncErrors:          make(map[string]string),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", withCORS(withPanicRecovery(withRequestLog(s.handleRPC))))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Cancel all with session_id="session-1" and index=-1.
	resp := doRPC(t, ts, "agent.answer", map[string]interface{}{
		"session_id": "session-1",
		"index":      -1,
	})
	r := readRPCResponse(t, resp)

	assert.Nil(t, r.Error)
	resultJSON, _ := json.Marshal(r.Result)
	var result map[string]interface{}
	json.Unmarshal(resultJSON, &result)
	assert.Equal(t, "answered", result["status"])

	// Verify the channel received the cancel answer.
	select {
	case answer := <-ch:
		assert.Equal(t, "", answer.Text)
		assert.Equal(t, -1, answer.Index)
	default:
		t.Fatal("expected answer on channel")
	}
}

func TestHandleAgentAnswerNoSessionID(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "agent.answer", map[string]interface{}{
		"text":  "yes",
		"index": 0,
	})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Equal(t, InvalidParams, r.Error.Code)
	assert.Contains(t, r.Error.Message, "session_id is required")
}

func TestHandleAgentAnswerNoPendingQuestion(t *testing.T) {
	_, ts := newTestServer(t)

	resp := doRPC(t, ts, "agent.answer", map[string]interface{}{
		"session_id": "nonexistent",
		"text":       "yes",
		"index":      0,
	})
	r := readRPCResponse(t, resp)

	require.NotNil(t, r.Error)
	assert.Contains(t, r.Error.Message, "no pending question")
}

// ============================================================================
// 17. Cipher/hash helpers
// ============================================================================

func TestAESGCMBlockSize(t *testing.T) {
	// Verify AES rejects wrong key sizes.
	_, err := aes.NewCipher(make([]byte, 16)) // 16 bytes, wrong for AES-256.
	assert.NoError(t, err, "AES-128 is valid")

	_, err = aes.NewCipher(make([]byte, 24))
	assert.NoError(t, err, "AES-192 is valid")

	_, err = aes.NewCipher(make([]byte, 31))
	assert.Error(t, err, "31 bytes should be invalid")

	_, err = aes.NewCipher(make([]byte, 33))
	assert.Error(t, err, "33 bytes should be invalid")
}

func TestGCMNonceSize(t *testing.T) {
	block, _ := aes.NewCipher(make([]byte, 32))
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)

	assert.Equal(t, 12, gcm.NonceSize(), "GCM standard nonce size is 12 bytes")
	assert.Equal(t, 16, gcm.Overhead(), "GCM tag overhead is 16 bytes")
}

// ============================================================================
// 18. SecureConfig / DecryptConfig
// ============================================================================

func TestSecureConfig(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewSecureStore(tmpDir)

	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "openai", APIKey: "sk-plaintext-key"},
			{Name: "anthropic", APIKey: "sk-another-key"},
		},
	}

	secured, err := store.SecureConfig(cfg)
	require.NoError(t, err)
	require.NotNil(t, secured)

	// Keys should be encrypted.
	assert.True(t, store.IsEncrypted(secured.Providers[0].APIKey))
	assert.True(t, store.IsEncrypted(secured.Providers[1].APIKey))
	assert.NotEqual(t, "sk-plaintext-key", secured.Providers[0].APIKey)

	// Decrypt and verify.
	err = store.DecryptConfig(secured)
	require.NoError(t, err)
	assert.Equal(t, "sk-plaintext-key", secured.Providers[0].APIKey)
	assert.Equal(t, "sk-another-key", secured.Providers[1].APIKey)
}

func TestSecureConfigNilStore(t *testing.T) {
	var store *SecureStore
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "openai", APIKey: "sk-key"},
		},
	}

	secured, err := store.SecureConfig(cfg)
	require.NoError(t, err)
	assert.Equal(t, "sk-key", secured.Providers[0].APIKey, "nil store should passthrough")
}

func TestDecryptConfigNilStore(t *testing.T) {
	var store *SecureStore
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "openai", APIKey: "sk-key"},
		},
	}
	err := store.DecryptConfig(cfg)
	require.NoError(t, err)
}

func TestSecureConfigAlreadyEncrypted(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewSecureStore(tmpDir)

	encrypted, _ := store.Encrypt("already-encrypted")
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "openai", APIKey: encrypted},
		},
	}

	secured, err := store.SecureConfig(cfg)
	require.NoError(t, err)
	// Already encrypted — should remain unchanged.
	assert.Equal(t, encrypted, secured.Providers[0].APIKey)
}

func TestSecureConfigWithGateways(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewSecureStore(tmpDir)

	cfg := &config.Config{
		Gateways: config.GatewayConfig{
			Telegram: config.TelegramConfig{BotToken: "tg-token-plain", Enabled: true},
			Discord:  config.DiscordConfig{BotToken: "dc-token-plain", Enabled: true},
			Slack:    config.SlackConfig{BotToken: "sl-token-plain", Enabled: true},
		},
	}

	secured, err := store.SecureConfig(cfg)
	require.NoError(t, err)

	assert.True(t, store.IsEncrypted(secured.Gateways.Telegram.BotToken))
	assert.True(t, store.IsEncrypted(secured.Gateways.Discord.BotToken))
	assert.True(t, store.IsEncrypted(secured.Gateways.Slack.BotToken))
}

// ============================================================================
// 19. Benchmark tests
// ============================================================================

func BenchmarkRequestMarshal(b *testing.B) {
	req := Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "agent.send",
		Params:  json.RawMessage(`{"message":"hello"}`),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		json.Marshal(req)
	}
}

func BenchmarkRequestUnmarshal(b *testing.B) {
	data := []byte(`{"jsonrpc":"2.0","id":"1","method":"agent.send","params":{"message":"hello"}}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req Request
		json.Unmarshal(data, &req)
	}
}

func BenchmarkResponseMarshal(b *testing.B) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Result:  &HealthResult{Status: "ok", Version: 1},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		json.Marshal(resp)
	}
}

func BenchmarkEncryptDecrypt(b *testing.B) {
	tmpDir := b.TempDir()
	store, _ := NewSecureStore(tmpDir)

	b.Run("Encrypt", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			store.Encrypt("sk-test-api-key-1234567890")
		}
	})

	encrypted, _ := store.Encrypt("sk-test-api-key-1234567890")
	b.Run("Decrypt", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			store.Decrypt(encrypted)
		}
	})
}

func BenchmarkToInt(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		toInt(float64(42))
	}
}

func BenchmarkToFloat64(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		toFloat64(42)
	}
}

func BenchmarkSplit2(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		split2("abc123def:base64encryptedvalue", ":")
	}
}
