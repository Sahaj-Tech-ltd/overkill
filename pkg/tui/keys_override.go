package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/pelletier/go-toml/v2"
)

// keysOverrideFile is the TOML schema for user-overridable keybindings.
// Field names map 1:1 to KeyMap fields, lowercased. Each entry is a
// slice of key spec strings understood by bubbles/key (e.g. "ctrl+c",
// "esc", "f2"). Missing entries fall through to the compiled-in default.
//
// Example ~/.overkill/keys.toml:
//
//	quit          = ["ctrl+q"]
//	help          = ["ctrl+h", "f1"]
//	switch_session = ["ctrl+s"]
//	commands      = ["ctrl+k", "ctrl+p"]
//	models        = ["ctrl+o"]
//	themes        = ["ctrl+t"]
//	config        = ["f2"]
//	status        = ["ctrl+i"]
//	fork          = ["ctrl+f"]
//
// The package-level `Keys` var is mutated in place — call this once at
// startup BEFORE any TUI rendering reads bindings.
type keysOverrideFile struct {
	Quit          []string `toml:"quit"`
	Help          []string `toml:"help"`
	SwitchSession []string `toml:"switch_session"`
	Commands      []string `toml:"commands"`
	Models        []string `toml:"models"`
	Themes        []string `toml:"themes"`
	Config        []string `toml:"config"`
	Status        []string `toml:"status"`
	Fork          []string `toml:"fork"`
}

// LoadKeyOverrides reads a TOML keys file and applies any defined
// bindings on top of the compiled-in defaults. Missing file is NOT an
// error — most users don't customise. Parse error IS an error so the
// caller can surface a clear "your keys.toml has a problem" message.
//
// Empty slices in the TOML mean "remove this binding entirely" — useful
// to disable a binding the user doesn't want without remapping it.
func LoadKeyOverrides(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("tui: read keys override: %w", err)
	}
	var ov keysOverrideFile
	if err := toml.Unmarshal(data, &ov); err != nil {
		return fmt.Errorf("tui: parse keys.toml: %w", err)
	}
	ApplyKeyOverrides(ov)
	return nil
}

// ApplyKeyOverrides mutates the package-level Keys var with the supplied
// overrides. Exposed for tests and for callers that want to provide
// overrides from a non-file source. Nil/empty fields are no-ops; an
// explicitly empty slice (`commands = []`) disables the binding.
func ApplyKeyOverrides(ov keysOverrideFile) {
	apply := func(field *key.Binding, keys []string, helpKey, helpDesc string) {
		if keys == nil {
			return // not present in TOML — keep default
		}
		if len(keys) == 0 {
			*field = key.NewBinding(key.WithDisabled())
			return
		}
		*field = key.NewBinding(key.WithKeys(keys...), key.WithHelp(keysJoin(keys), helpDesc))
		_ = helpKey
	}
	apply(&Keys.Quit, ov.Quit, "ctrl+c / esc", "quit")
	apply(&Keys.Help, ov.Help, "ctrl+h", "help")
	apply(&Keys.SwitchSession, ov.SwitchSession, "ctrl+s", "sessions")
	apply(&Keys.Commands, ov.Commands, "ctrl+k", "commands")
	apply(&Keys.Models, ov.Models, "ctrl+o", "models")
	apply(&Keys.Themes, ov.Themes, "ctrl+t", "theme")
	apply(&Keys.Config, ov.Config, "ctrl+, / f2", "config")
	apply(&Keys.Status, ov.Status, "ctrl+i", "status")
	apply(&Keys.Fork, ov.Fork, "ctrl+f", "fork from message")
}

// keysJoin renders a slice of key spec strings into a short help label
// (e.g. ["ctrl+c", "esc"] → "ctrl+c / esc").
func keysJoin(keys []string) string {
	return strings.Join(keys, " / ")
}
