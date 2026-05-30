// Package settings provides a declarative, reflection-based settings system
// for Overkill, inspired by Warp's define_settings_group! macro.
//
// Settings groups are Go structs with struct tags that describe each field's
// TOML key, default value, validation bounds, and UI metadata. The package
// provides runtime helpers for applying defaults, validating, and generating
// UI schemas — no code generation required (v1).
//
// Struct tags:
//
//	setting:"key"       — TOML key name
//	default:"val"       — default value (parsed to field type)
//	desc:"help text"    — human-readable description for UI
//	min:"0"             — minimum value (int/float only)
//	max:"100"           — maximum value (int/float only)
//	required:"true"     — must be non-zero
//
// Example:
//
//	type AISettings struct {
//	    Model       string  `setting:"model" default:"claude-sonnet-4" desc:"AI model"`
//	    Temperature float64 `setting:"temperature" default:"0.7" min:"0" max:"2"`
//	    MaxTokens   int     `setting:"max_tokens" default:"4096" min:"1" max:"200000"`
//	}
//
// Register it with Register("ai", &AISettings{}), then call LoadAll(path)
// to populate from TOML, apply defaults, and validate.
package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// Group describes a named settings group registered with the system.
type Group struct {
	Key  string
	Ptr  any // pointer to the settings struct
	Type reflect.Type
}

// SettingUI is the metadata for a single setting, used by the settings UI.
type SettingUI struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	Type        string `json:"type"` // "string", "bool", "int", "float"
	Default     string `json:"default"`
	Description string `json:"description"`
	Min         string `json:"min,omitempty"`
	Max         string `json:"max,omitempty"`
	Required    bool   `json:"required"`
}

// Registry holds all registered settings groups.
type Registry struct {
	mu     sync.Mutex
	groups []Group
}

var defaultRegistry = &Registry{}

// Register adds a named settings group to the default registry.
// ptr must be a pointer to a struct (e.g. &MySettings{}).
func Register(key string, ptr any) {
	defaultRegistry.Register(key, ptr)
}

// Register adds a settings group to this registry.
func (r *Registry) Register(key string, ptr any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := reflect.ValueOf(ptr)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		panic(fmt.Sprintf("settings.Register: %s must be a pointer to struct, got %T", key, ptr))
	}
	r.groups = append(r.groups, Group{Key: key, Ptr: ptr, Type: v.Elem().Type()})
}

// LoadAll reads a TOML file (or a directory containing settings.toml),
// applies defaults, and validates all registered groups. The TOML file
// should have top-level keys matching group keys.
func (r *Registry) LoadAll(path string) error {
	// If path is a directory, look for settings.toml inside.
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Neither file nor directory exists — apply defaults only.
			for _, g := range r.groups {
				if err := ApplyDefaults(g.Ptr); err != nil {
					return fmt.Errorf("settings %s: %w", g.Key, err)
				}
				if err := ValidateStruct(g.Ptr); err != nil {
					return fmt.Errorf("settings %s: %w", g.Key, err)
				}
			}
			return nil
		}
		return fmt.Errorf("settings: stat %s: %w", path, err)
	}
	if fi.IsDir() {
		path = filepath.Join(path, "settings.toml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			for _, g := range r.groups {
				if err := ApplyDefaults(g.Ptr); err != nil {
					return fmt.Errorf("settings %s: %w", g.Key, err)
				}
				if err := ValidateStruct(g.Ptr); err != nil {
					return fmt.Errorf("settings %s: %w", g.Key, err)
				}
			}
			return nil
		}
		return fmt.Errorf("settings: read %s: %w", path, err)
	}

	// Parse TOML into a raw map, then apply each group's section.
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("settings: parse %s: %w", path, err)
	}

	for _, g := range r.groups {
		// Apply defaults first.
		if err := ApplyDefaults(g.Ptr); err != nil {
			return fmt.Errorf("settings %s: %w", g.Key, err)
		}

		// If TOML has this group's section, unmarshal it into the struct.
		if section, ok := raw[g.Key]; ok {
			sectionData, err := toml.Marshal(section)
			if err != nil {
				return fmt.Errorf("settings %s: re-marshal: %w", g.Key, err)
			}
			if err := toml.Unmarshal(sectionData, g.Ptr); err != nil {
				return fmt.Errorf("settings %s: %w", g.Key, err)
			}
		}

		// Validate.
		if err := ValidateStruct(g.Ptr); err != nil {
			return fmt.Errorf("settings %s: %w", g.Key, err)
		}
	}
	return nil
}

// LoadAll is the convenience function on the default registry.
func LoadAll(path string) error {
	return defaultRegistry.LoadAll(path)
}

// Groups returns all registered groups (for UI schema generation, etc.).
func (r *Registry) Groups() []Group {
	return r.groups
}

// UISchema returns UI metadata for all registered settings.
func (r *Registry) UISchema() map[string][]SettingUI {
	out := make(map[string][]SettingUI)
	for _, g := range r.groups {
		out[g.Key] = UISchema(g.Ptr)
	}
	return out
}

// UISchema returns UI metadata for a single settings struct.
// Recurses into nested structs, prefixing keys with the parent field name.
func UISchema(ptr any) []SettingUI {
	v := reflect.ValueOf(ptr)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return nil
	}
	return uiSchemaValue(v.Elem(), "")
}

func uiSchemaValue(v reflect.Value, prefix string) []SettingUI {
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	var fields []SettingUI
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		key := f.Tag.Get("setting")
		if key == "" {
			key = strings.ToLower(f.Name)
		}
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		// Recurse into nested structs.
		if f.Type.Kind() == reflect.Struct {
			fields = append(fields, uiSchemaValue(v.Field(i), fullKey)...)
			continue
		}

		fields = append(fields, SettingUI{
			Name:        f.Name,
			Key:         fullKey,
			Type:        fieldTypeName(f.Type),
			Default:     f.Tag.Get("default"),
			Description: f.Tag.Get("desc"),
			Min:         f.Tag.Get("min"),
			Max:         f.Tag.Get("max"),
			Required:    f.Tag.Get("required") == "true",
		})
	}
	return fields
}

// ApplyDefaults sets zero-value fields on the struct to their default values
// as declared in struct tags. Recurses into nested structs.
func ApplyDefaults(ptr any) error {
	v := reflect.ValueOf(ptr)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return fmt.Errorf("ApplyDefaults: expected non-nil pointer to struct, got %T", ptr)
	}
	return applyDefaultsValue(v.Elem())
}

func applyDefaultsValue(v reflect.Value) error {
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("ApplyDefaults: expected pointer to struct")
	}
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := v.Field(i)
		sf := t.Field(i)

		if !field.CanSet() {
			continue
		}

		// Recurse into nested structs.
		if field.Kind() == reflect.Struct {
			// If the struct field is zero, allocate it.
			if field.IsZero() {
				field.Set(reflect.New(field.Type()).Elem())
			}
			if err := applyDefaultsValue(field); err != nil {
				return err
			}
			continue
		}

		tag := sf.Tag.Get("default")
		if tag == "" {
			continue
		}
		if !field.IsZero() {
			continue
		}
		if err := setField(field, tag); err != nil {
			return fmt.Errorf("field %s: %w", sf.Name, err)
		}
	}
	return nil
}

// ValidateStruct checks all struct tags on the given pointer for constraint
// violations. Recurses into nested structs. Returns nil if everything is valid.
func ValidateStruct(ptr any) error {
	v := reflect.ValueOf(ptr)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return fmt.Errorf("ValidateStruct: expected non-nil pointer to struct, got %T", ptr)
	}
	return validateValue(v.Elem())
}

func validateValue(v reflect.Value) error {
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("ValidateStruct: expected pointer to struct")
	}
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := v.Field(i)
		sf := t.Field(i)

		// Recurse into nested structs.
		if field.Kind() == reflect.Struct {
			if err := validateValue(field); err != nil {
				return err
			}
			continue
		}

		// Required check.
		if sf.Tag.Get("required") == "true" && field.IsZero() {
			return fmt.Errorf("%s is required", sf.Name)
		}

		// Min/max for numeric fields.
		minStr := sf.Tag.Get("min")
		maxStr := sf.Tag.Get("max")
		if minStr == "" && maxStr == "" {
			continue
		}

		switch field.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			val := field.Int()
			if minStr != "" {
				min, err := strconv.ParseInt(minStr, 10, 64)
				if err != nil {
					return fmt.Errorf("%s: invalid min tag: %w", sf.Name, err)
				}
				if val < min {
					return fmt.Errorf("%s: %d < min %d", sf.Name, val, min)
				}
			}
			if maxStr != "" {
				max, err := strconv.ParseInt(maxStr, 10, 64)
				if err != nil {
					return fmt.Errorf("%s: invalid max tag: %w", sf.Name, err)
				}
				if val > max {
					return fmt.Errorf("%s: %d > max %d", sf.Name, val, max)
				}
			}
		case reflect.Float32, reflect.Float64:
			val := field.Float()
			if minStr != "" {
				min, err := strconv.ParseFloat(minStr, 64)
				if err != nil {
					return fmt.Errorf("%s: invalid min tag: %w", sf.Name, err)
				}
				if val < min {
					return fmt.Errorf("%s: %f < min %f", sf.Name, val, min)
				}
			}
			if maxStr != "" {
				max, err := strconv.ParseFloat(maxStr, 64)
				if err != nil {
					return fmt.Errorf("%s: invalid max tag: %w", sf.Name, err)
				}
				if val > max {
					return fmt.Errorf("%s: %f > max %f", sf.Name, val, max)
				}
			}
		}
	}
	return nil
}

func setField(field reflect.Value, val string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(val)
	case reflect.Bool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return err
		}
		field.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return err
		}
		field.SetInt(i)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return err
		}
		field.SetFloat(f)
	default:
		return fmt.Errorf("unsupported type %s for default tag", field.Kind())
	}
	return nil
}

func fieldTypeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Float32, reflect.Float64:
		return "float"
	default:
		return t.Kind().String()
	}
}
