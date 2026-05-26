package animation

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type FrameTickMsg struct{}

type AnimState struct {
	Active  bool
	Name    string
	Frame   int
	Running bool
}

func (s AnimState) CurrentChar() string {
	sp, ok := ByName[s.Name]
	if !ok || len(sp.Frames) == 0 {
		return ""
	}
	return sp.Frames[s.Frame%len(sp.Frames)]
}

func Tick(name string) tea.Cmd {
	sp, ok := ByName[name]
	if !ok {
		sp = Braille
	}
	return tea.Tick(sp.Interval, func(t time.Time) tea.Msg {
		return FrameTickMsg{}
	})
}

func StopTick() tea.Cmd {
	return func() tea.Msg {
		return nil
	}
}

func StartAnim(state *AnimState, name string) tea.Cmd {
	state.Active = true
	state.Name = name
	state.Frame = 0
	state.Running = true
	return Tick(name)
}

func StopAnim(state *AnimState) {
	state.Active = false
	state.Running = false
	state.Frame = 0
}

func AdvanceFrame(s *AnimState) tea.Cmd {
	s.Frame++
	return Tick(s.Name)
}

func (s AnimState) View() string {
	if !s.Active {
		return ""
	}
	return s.CurrentChar() + " "
}
