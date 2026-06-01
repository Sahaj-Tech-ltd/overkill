package settings

import (
	"testing"
)

func TestUISchema(t *testing.T) {
	g := &testGroup{}
	_ = ApplyDefaults(g)

	schema := UISchema(g)
	if len(schema) == 0 {
		t.Fatal("UISchema returned empty")
	}

	// Build a lookup map by key. Nested structs get "parent.field" keys.
	byKey := make(map[string]SettingUI, len(schema))
	for _, s := range schema {
		byKey[s.Key] = s
	}

	// Top-level field.
	v, ok := byKey["version"]
	if !ok {
		t.Logf("keys available: %v", keysOf(schema))
		t.Error("version should be in schema")
	}
	if v.Type != "int" {
		t.Errorf("version Type = %q, want int", v.Type)
	}
	if v.Default != "1" {
		t.Errorf("version Default = %q, want 1", v.Default)
	}

	// Nested Agent sub-fields.
	name, ok := byKey["agent.name"]
	if !ok {
		t.Logf("keys available: %v", keysOf(schema))
		t.Error("agent.name should be in schema")
	}
	if name.Type != "string" {
		t.Errorf("name Type = %q, want string", name.Type)
	}
	if name.Default != "Overkill" {
		t.Errorf("name Default = %q, want Overkill", name.Default)
	}
	if name.Description != "Agent display name" {
		t.Errorf("name Description = %q, want 'Agent display name'", name.Description)
	}

	maxTurn, ok := byKey["agent.max_turn"]
	if !ok {
		t.Error("agent.max_turn should be in schema")
	}
	if maxTurn.Type != "int" {
		t.Errorf("max_turn Type = %q, want int", maxTurn.Type)
	}
	if maxTurn.Min != "1" {
		t.Errorf("max_turn Min = %q, want 1", maxTurn.Min)
	}
	if maxTurn.Max != "200" {
		t.Errorf("max_turn Max = %q, want 200", maxTurn.Max)
	}

	temp, ok := byKey["agent.temp"]
	if !ok {
		t.Error("agent.temp should be in schema")
	}
	if temp.Type != "float" {
		t.Errorf("temp Type = %q, want float", temp.Type)
	}

	enabled, ok := byKey["agent.enabled"]
	if !ok {
		t.Error("agent.enabled should be in schema")
	}
	if enabled.Type != "bool" {
		t.Errorf("enabled Type = %q, want bool", enabled.Type)
	}

	apiKey, ok := byKey["agent.api_key"]
	if !ok {
		t.Error("agent.api_key should be in schema")
	}
	if apiKey.Type != "string" {
		t.Errorf("api_key Type = %q, want string", apiKey.Type)
	}
	if !apiKey.Required {
		t.Error("api_key should be Required")
	}
	if apiKey.Description != "Provider API key" {
		t.Errorf("api_key Description = %q, want 'Provider API key'", apiKey.Description)
	}
}

func TestUISchemaNestedStruct(t *testing.T) {
	type inner struct {
		Host string `setting:"host" default:"localhost" desc:"Server host"`
		Port int    `setting:"port" default:"8080"     min:"1" max:"65535"`
	}
	type outer struct {
		Server inner `setting:"server"`
		Debug  bool  `setting:"debug" default:"false"`
	}

	o := &outer{}
	_ = ApplyDefaults(o)

	schema := UISchema(o)
	byKey := make(map[string]SettingUI, len(schema))
	for _, s := range schema {
		byKey[s.Key] = s
	}

	host, ok := byKey["server.host"]
	if !ok {
		t.Logf("keys available: %v", keysOf(schema))
		t.Error("server.host should be in schema")
	}
	if host.Type != "string" {
		t.Errorf("host Type = %q, want string", host.Type)
	}
	if host.Default != "localhost" {
		t.Errorf("host Default = %q, want localhost", host.Default)
	}

	port, ok := byKey["server.port"]
	if !ok {
		t.Error("server.port should be in schema")
	}
	if port.Type != "int" {
		t.Errorf("port Type = %q, want int", port.Type)
	}
	if port.Default != "8080" {
		t.Errorf("port Default = %q, want 8080", port.Default)
	}

	debug, ok := byKey["debug"]
	if !ok {
		t.Error("debug should be in schema")
	}
	if debug.Type != "bool" {
		t.Errorf("debug Type = %q, want bool", debug.Type)
	}
	if debug.Default != "false" {
		t.Errorf("debug Default = %q, want false", debug.Default)
	}
}

func keysOf(schema []SettingUI) []string {
	keys := make([]string, len(schema))
	for i, s := range schema {
		keys[i] = s.Key
	}
	return keys
}

func TestUISchemaFallsBackToFieldName(t *testing.T) {
	type s struct {
		APIKey string `desc:"The key"`
	}
	ui := UISchema(&s{})
	if len(ui) != 1 {
		t.Fatalf("expected 1 item, got %d", len(ui))
	}
	if ui[0].Key != "apikey" {
		t.Errorf("Key = %q, want apikey", ui[0].Key)
	}
	if ui[0].Description != "The key" {
		t.Errorf("Description = %q, want 'The key'", ui[0].Description)
	}
}

func TestUISchemaRegistry(t *testing.T) {
	r := &Registry{}
	r.Register("ai", &testAgentSettings{})
	r.Register("app", &testGroup{})

	schemas := r.UISchema()
	if len(schemas) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(schemas))
	}

	aiSchema, ok := schemas["ai"]
	if !ok {
		t.Error("missing 'ai' schema")
	}
	if len(aiSchema) == 0 {
		t.Error("ai schema is empty")
	}

	appSchema, ok := schemas["app"]
	if !ok {
		t.Error("missing 'app' schema")
	}
	if len(appSchema) == 0 {
		t.Error("app schema is empty")
	}
}
