package tui

import "github.com/charmbracelet/bubbles/key"

type KeyMap struct {
	Quit          key.Binding
	Help          key.Binding
	SwitchSession key.Binding
	Commands      key.Binding
	Models        key.Binding
	Themes        key.Binding
	Config        key.Binding
	Status        key.Binding
	Fork          key.Binding
}

// Keys is the live keybinding map. Mutated by LoadKeyOverrides at boot
// (see keys_override.go) when ~/.overkill/keys.toml is present.
var Keys = defaultKeys()

// defaultKeys returns the compiled-in default bindings. Function (not
// inlined literal) so tests can reset the package var after applying
// overrides.
func defaultKeys() KeyMap {
	return KeyMap{
		Quit:          key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("ctrl+c / esc", "quit")),
		Help:          key.NewBinding(key.WithKeys("ctrl+h"), key.WithHelp("ctrl+h", "help")),
		SwitchSession: key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "sessions")),
		Commands:      key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("ctrl+k", "commands")),
		Models:        key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "models")),
		Themes:        key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("ctrl+t", "theme")),
		Config:        key.NewBinding(key.WithKeys("ctrl+_", "f2"), key.WithHelp("ctrl+, / f2", "config")),
		Status:        key.NewBinding(key.WithKeys("ctrl+i"), key.WithHelp("ctrl+i", "status")),
		Fork:          key.NewBinding(key.WithKeys("ctrl+f"), key.WithHelp("ctrl+f", "fork from message")),
	}
}

// AllBindings is the help-overlay-friendly list of bindings.
func AllBindings() []key.Binding {
	return []key.Binding{
		Keys.Commands,
		Keys.Models,
		Keys.SwitchSession,
		Keys.Themes,
		Keys.Config,
		Keys.Status,
		Keys.Fork,
		Keys.Help,
		Keys.Quit,
	}
}
