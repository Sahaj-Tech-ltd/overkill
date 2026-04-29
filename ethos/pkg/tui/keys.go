package tui

import "github.com/charmbracelet/bubbles/key"

type KeyMap struct {
	Quit          key.Binding
	Help          key.Binding
	SwitchSession key.Binding
	Commands      key.Binding
	Models        key.Binding
}

var Keys = KeyMap{
	Quit:          key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
	Help:          key.NewBinding(key.WithKeys("ctrl+?"), key.WithHelp("ctrl+?", "help")),
	SwitchSession: key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "sessions")),
	Commands:      key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("ctrl+k", "commands")),
	Models:        key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "models")),
}
