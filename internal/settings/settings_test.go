package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// Test structs with the declarative struct tags.
type testAgentSettings struct {
	Name    string  `toml:"name"     setting:"name"     default:"Overkill"  desc:"Agent display name"`
	Model   string  `toml:"model"    setting:"model"    default:"gpt-4o"    desc:"Default model"`
	MaxTurn int     `toml:"max_turn" setting:"max_turn" default:"42"        min:"1" max:"200"`
	Temp    float64 `toml:"temp"     setting:"temp"     default:"0.7"       min:"0" max:"2.0"`
	Enabled bool    `toml:"enabled"  setting:"enabled"  default:"true"`
	APIKey  string  `toml:"api_key"  setting:"api_key"  required:"true"     desc:"Provider API key"`
}

type testGroup struct {
	Agent   testAgentSettings `toml:"agent"`
	Version int               `toml:"version" setting:"version" default:"1" min:"1"`
}

func TestApplyDefaults(t *testing.T) {
	g := &testGroup{}
	err := ApplyDefaults(g)
	if err != nil {
		t.Fatal(err)
	}

	if g.Agent.Name != "Overkill" {
		t.Errorf("Name = %q, want Overkill", g.Agent.Name)
	}
	if g.Agent.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", g.Agent.Model)
	}
	if g.Agent.MaxTurn != 42 {
		t.Errorf("MaxTurn = %d, want 42", g.Agent.MaxTurn)
	}
	if g.Agent.Temp != 0.7 {
		t.Errorf("Temp = %f, want 0.7", g.Agent.Temp)
	}
	if !g.Agent.Enabled {
		t.Error("Enabled should be true")
	}
	if g.Version != 1 {
		t.Errorf("Version = %d, want 1", g.Version)
	}
	// APIKey has required but no default — should stay empty.
	if g.Agent.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", g.Agent.APIKey)
	}
}

func TestApplyDefaultsNoOverride(t *testing.T) {
	g := &testGroup{
		Agent: testAgentSettings{
			Name:  "CustomBot",
			Model: "claude-3",
		},
	}
	err := ApplyDefaults(g)
	if err != nil {
		t.Fatal(err)
	}

	if g.Agent.Name != "CustomBot" {
		t.Errorf("Name = %q, want CustomBot", g.Agent.Name)
	}
	if g.Agent.Model != "claude-3" {
		t.Errorf("Model = %q, want claude-3", g.Agent.Model)
	}
	if g.Agent.MaxTurn != 42 {
		t.Errorf("MaxTurn = %d, want 42", g.Agent.MaxTurn)
	}
}

func TestApplyDefaultsNilPointer(t *testing.T) {
	err := ApplyDefaults(nil)
	if err == nil {
		t.Fatal("expected error for nil pointer")
	}
}

func TestApplyDefaultsNonStruct(t *testing.T) {
	x := 42
	err := ApplyDefaults(&x)
	if err == nil {
		t.Fatal("expected error for non-struct pointer")
	}
}

func TestValidateStructRequired(t *testing.T) {
	g := &testGroup{}
	_ = ApplyDefaults(g)
	// APIKey has no default and is required — still empty.
	err := ValidateStruct(g)
	if err == nil {
		t.Fatal("expected error for required field")
	}
}

func TestValidateStructRequiredOK(t *testing.T) {
	g := &testGroup{}
	_ = ApplyDefaults(g)
	g.Agent.APIKey = "sk-1234"
	err := ValidateStruct(g)
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateStructMin(t *testing.T) {
	g := &testGroup{}
	_ = ApplyDefaults(g)
	g.Agent.APIKey = "sk-x" // satisfy required

	g.Agent.MaxTurn = 0 // below min of 1
	err := ValidateStruct(g)
	if err == nil {
		t.Fatal("expected error for below-min value")
	}

	g.Agent.MaxTurn = 1 // at min — ok
	err = ValidateStruct(g)
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateStructMax(t *testing.T) {
	g := &testGroup{}
	_ = ApplyDefaults(g)
	g.Agent.APIKey = "sk-x"

	g.Agent.MaxTurn = 201 // above max of 200
	err := ValidateStruct(g)
	if err == nil {
		t.Fatal("expected error for above-max value")
	}
}

func TestValidateStructFloatMinMax(t *testing.T) {
	g := &testGroup{}
	_ = ApplyDefaults(g)
	g.Agent.APIKey = "sk-x"

	g.Agent.Temp = -0.1
	err := ValidateStruct(g)
	if err == nil {
		t.Fatal("expected error for below-min float")
	}

	g.Agent.Temp = 2.1
	err = ValidateStruct(g)
	if err == nil {
		t.Fatal("expected error for above-max float")
	}

	g.Agent.Temp = 1.0
	err = ValidateStruct(g)
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateStructVersionMin(t *testing.T) {
	g := &testGroup{}
	_ = ApplyDefaults(g)
	g.Agent.APIKey = "sk-x"

	g.Version = 0
	err := ValidateStruct(g)
	if err == nil {
		t.Fatal("expected error for version below min")
	}
}

// Register + LoadAll roundtrip.
func TestRegisterAndLoadAll(t *testing.T) {
	dir := t.TempDir()

	// Fresh registry.
	reg := &Registry{}
	g := &testGroup{}
	reg.Register("test", g)

	// First LoadAll — no file, should apply defaults and validate.
	// APIKey is required and empty, so validation should fail.
	err := reg.LoadAll(dir)
	if err == nil {
		t.Fatal("expected validation error for missing APIKey")
	}

	// Set APIKey so validation passes.
	g.Agent.APIKey = "sk-test"

	err = reg.LoadAll(dir)
	if err != nil {
		t.Fatal(err)
	}

	if g.Agent.Name != "Overkill" {
		t.Errorf("Name = %q, want Overkill", g.Agent.Name)
	}
	if g.Agent.MaxTurn != 42 {
		t.Errorf("MaxTurn = %d, want 42", g.Agent.MaxTurn)
	}
	if g.Version != 1 {
		t.Errorf("Version = %d, want 1", g.Version)
	}

	// Now write a TOML file and load again.
	tomlData := `[test]
version = 2

[test.agent]
name = "Custom"
model = "claude-sonnet"
max_turn = 10
temp = 1.0
enabled = false
api_key = "sk-custom"
`
	path := filepath.Join(dir, "settings.toml")
	if err := os.WriteFile(path, []byte(tomlData), 0o644); err != nil {
		t.Fatal(err)
	}

	reg2 := &Registry{}
	g2 := &testGroup{}
	reg2.Register("test", g2)

	err = reg2.LoadAll(dir)
	if err != nil {
		t.Fatal(err)
	}

	if g2.Agent.Name != "Custom" {
		t.Errorf("Name = %q, want Custom", g2.Agent.Name)
	}
	if g2.Agent.Model != "claude-sonnet" {
		t.Errorf("Model = %q, want claude-sonnet", g2.Agent.Model)
	}
	if g2.Agent.MaxTurn != 10 {
		t.Errorf("MaxTurn = %d, want 10", g2.Agent.MaxTurn)
	}
	if g2.Agent.Temp != 1.0 {
		t.Errorf("Temp = %f, want 1.0", g2.Agent.Temp)
	}
	if g2.Agent.Enabled {
		t.Error("Enabled should be false")
	}
	if g2.Agent.APIKey != "sk-custom" {
		t.Errorf("APIKey = %q, want sk-custom", g2.Agent.APIKey)
	}
	if g2.Version != 2 {
		t.Errorf("Version = %d, want 2", g2.Version)
	}
}
