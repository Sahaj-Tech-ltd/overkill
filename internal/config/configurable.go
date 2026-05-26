// Package config — the Configurable interface.
//
// Tools, scanners, skills, and any other extension point that wants
// to surface settings in the v2.0 Advanced tab implements Configurable.
// The Settings page enumerates registered things, calls ConfigSchema()
// on each, renders a card, and pushes user edits back through
// ApplyConfig().
//
// The interface is OPTIONAL — a tool that doesn't implement it gets
// rendered as a basic on/off toggle, same as today. Implementing it
// means a third-party tool can ship with rich settings without
// touching any of the Settings page code.
//
// Design notes:
//
//   - Schema is a tiny JSON-schema-shaped struct, not full JSON
//     Schema. Validation lives in ApplyConfig; the Schema is purely
//     for UI rendering hints.
//   - ApplyConfig takes map[string]any (the parsed YAML/JSON kv) and
//     returns an error. Returning an error keeps the prior value and
//     surfaces the message in the Settings toast bar.
//   - We deliberately do NOT use reflection to derive Schema from
//     struct tags. Two reasons: (1) it makes the Schema explicit and
//     greppable; (2) Go reflection over generic interface fields is
//     a wormhole we don't want users falling into.
package config

import "fmt"

// Configurable is implemented by anything that wants a per-instance
// settings card in the Advanced tab.
type Configurable interface {
	// ConfigSchema returns the descriptor the Settings UI renders.
	// Returning nil opts the implementer out of the rich-card flow
	// (the basic on/off toggle still works).
	ConfigSchema() *Schema

	// ApplyConfig is called when the user saves the settings card.
	// Implementations validate + apply atomically. Returning an
	// error keeps the prior in-memory state and surfaces the
	// message to the user.
	ApplyConfig(raw map[string]any) error
}

// Schema describes a settings card. The shape mirrors a stripped
// JSON Schema: a name, a description, and a flat list of fields.
// Nested objects aren't supported — flatten with dotted names if you
// need hierarchy.
type Schema struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Fields      []Field  `json:"fields"`
	// Category groups multiple Configurables in the Advanced tab
	// (e.g. "scanners", "tools", "memory"). Empty falls back to
	// "tools".
	Category string `json:"category,omitempty"`
}

// FieldKind is the renderer hint for the Settings UI.
type FieldKind string

const (
	FieldString  FieldKind = "string"
	FieldInt     FieldKind = "int"
	FieldFloat   FieldKind = "float"
	FieldBool    FieldKind = "bool"
	FieldEnum    FieldKind = "enum"     // values come from Field.Options
	FieldList    FieldKind = "list"     // string[]; one entry per line in the UI
	FieldSecret  FieldKind = "secret"   // string, masked in the UI
	FieldText    FieldKind = "text"     // multi-line string (system_prompt etc.)
)

// Field is one row in a settings card.
type Field struct {
	Key         string    `json:"key"`
	Label       string    `json:"label"`
	Kind        FieldKind `json:"kind"`
	Description string    `json:"description,omitempty"`
	Default     any       `json:"default,omitempty"`
	Options     []string  `json:"options,omitempty"` // for Kind=enum
	// Min / Max apply to int / float fields. Zero means "no bound."
	Min float64 `json:"min,omitempty"`
	Max float64 `json:"max,omitempty"`
	// Required: an empty value on save returns a validation error.
	Required bool `json:"required,omitempty"`
}

// ValidateField returns nil when the raw value passes Kind / range
// / required checks. Used by ApplyConfig implementations and (for
// dry-run previews) by the Settings page before the user clicks save.
func ValidateField(f Field, raw any) error {
	if raw == nil {
		if f.Required {
			return fmt.Errorf("%s: required", f.Key)
		}
		return nil
	}
	switch f.Kind {
	case FieldInt:
		n, ok := raw.(int)
		if !ok {
			// YAML decoders sometimes hand us int64. Try harder.
			if n64, ok64 := raw.(int64); ok64 {
				n = int(n64)
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("%s: want int, got %T", f.Key, raw)
		}
		if f.Min != 0 && float64(n) < f.Min {
			return fmt.Errorf("%s: %d below min %v", f.Key, n, f.Min)
		}
		if f.Max != 0 && float64(n) > f.Max {
			return fmt.Errorf("%s: %d above max %v", f.Key, n, f.Max)
		}
	case FieldFloat:
		v, ok := raw.(float64)
		if !ok {
			return fmt.Errorf("%s: want float, got %T", f.Key, raw)
		}
		if f.Min != 0 && v < f.Min {
			return fmt.Errorf("%s: %v below min %v", f.Key, v, f.Min)
		}
		if f.Max != 0 && v > f.Max {
			return fmt.Errorf("%s: %v above max %v", f.Key, v, f.Max)
		}
	case FieldBool:
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("%s: want bool, got %T", f.Key, raw)
		}
	case FieldEnum:
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("%s: want string for enum, got %T", f.Key, raw)
		}
		valid := false
		for _, opt := range f.Options {
			if s == opt {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("%s: %q not in %v", f.Key, s, f.Options)
		}
	case FieldString, FieldSecret, FieldText:
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("%s: want string, got %T", f.Key, raw)
		}
		if f.Required && s == "" {
			return fmt.Errorf("%s: required", f.Key)
		}
	case FieldList:
		if _, ok := raw.([]any); !ok {
			if _, ok := raw.([]string); !ok {
				return fmt.Errorf("%s: want list, got %T", f.Key, raw)
			}
		}
	}
	return nil
}
