package config

import (
	"strings"
	"testing"
)

func TestValidateField(t *testing.T) {
	tests := []struct {
		name    string
		field   Field
		raw     any
		wantErr string // substring; "" = no error expected
	}{
		{
			name:  "int in range",
			field: Field{Key: "n", Kind: FieldInt, Min: 0, Max: 100},
			raw:   42,
		},
		{
			name:    "int below min",
			field:   Field{Key: "n", Kind: FieldInt, Min: 10, Max: 100},
			raw:     5,
			wantErr: "below min",
		},
		{
			name:    "int wrong type",
			field:   Field{Key: "n", Kind: FieldInt},
			raw:     "fortytwo",
			wantErr: "want int",
		},
		{
			name:  "float in range",
			field: Field{Key: "x", Kind: FieldFloat, Min: 0, Max: 1},
			raw:   0.5,
		},
		{
			name:    "float above max",
			field:   Field{Key: "x", Kind: FieldFloat, Min: 0, Max: 1},
			raw:     1.5,
			wantErr: "above max",
		},
		{
			name:  "bool",
			field: Field{Key: "b", Kind: FieldBool},
			raw:   true,
		},
		{
			name:    "bool wrong type",
			field:   Field{Key: "b", Kind: FieldBool},
			raw:     "true",
			wantErr: "want bool",
		},
		{
			name:  "enum valid",
			field: Field{Key: "mode", Kind: FieldEnum, Options: []string{"a", "b", "c"}},
			raw:   "b",
		},
		{
			name:    "enum invalid",
			field:   Field{Key: "mode", Kind: FieldEnum, Options: []string{"a", "b"}},
			raw:     "z",
			wantErr: "not in",
		},
		{
			name:  "string",
			field: Field{Key: "s", Kind: FieldString},
			raw:   "hello",
		},
		{
			name:    "string required empty",
			field:   Field{Key: "s", Kind: FieldString, Required: true},
			raw:     "",
			wantErr: "required",
		},
		{
			name:    "required nil",
			field:   Field{Key: "s", Kind: FieldString, Required: true},
			raw:     nil,
			wantErr: "required",
		},
		{
			name:  "list as []any",
			field: Field{Key: "lst", Kind: FieldList},
			raw:   []any{"a", "b"},
		},
		{
			name:  "list as []string",
			field: Field{Key: "lst", Kind: FieldList},
			raw:   []string{"a", "b"},
		},
		{
			name:    "list wrong type",
			field:   Field{Key: "lst", Kind: FieldList},
			raw:     "a,b",
			wantErr: "want list",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateField(tc.field, tc.raw)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// exampleConfigurable shows how a Tool would implement Configurable.
type exampleConfigurable struct {
	timeout int
	cwd     string
}

func (e *exampleConfigurable) ConfigSchema() *Schema {
	return &Schema{
		Name:        "shell",
		Category:    "tools",
		Description: "Run shell commands.",
		Fields: []Field{
			{Key: "max_timeout_seconds", Label: "Timeout (s)", Kind: FieldInt, Min: 1, Max: 3600, Default: 30},
			{Key: "cwd", Label: "Working dir", Kind: FieldString},
		},
	}
}

func (e *exampleConfigurable) ApplyConfig(raw map[string]any) error {
	sch := e.ConfigSchema()
	for _, f := range sch.Fields {
		if v, ok := raw[f.Key]; ok {
			if err := ValidateField(f, v); err != nil {
				return err
			}
		}
	}
	if v, ok := raw["max_timeout_seconds"].(int); ok {
		e.timeout = v
	}
	if v, ok := raw["cwd"].(string); ok {
		e.cwd = v
	}
	return nil
}

func TestConfigurable_Example(t *testing.T) {
	c := &exampleConfigurable{}
	if err := c.ApplyConfig(map[string]any{
		"max_timeout_seconds": 60,
		"cwd":                 "/tmp/work",
	}); err != nil {
		t.Fatal(err)
	}
	if c.timeout != 60 || c.cwd != "/tmp/work" {
		t.Errorf("apply did not stick: %+v", c)
	}
	// Out-of-range value rejected.
	if err := c.ApplyConfig(map[string]any{"max_timeout_seconds": 99999}); err == nil {
		t.Fatal("expected range error")
	}
}
