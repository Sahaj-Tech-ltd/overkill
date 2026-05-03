// Package animation provides a shared kill-switch and helpers for every
// animated TUI component. The goal is to keep animations cheap over SSH:
// when the gate is closed, animation models render statically and emit no
// tick commands at all.
package animation

import (
	"os"
	"sync/atomic"
)

// MinTermWidth is the minimum terminal width (in cells) below which all
// animations are suppressed. This avoids expensive per-cell math on tiny
// terminals where the visual payoff is nil.
const MinTermWidth = 60

// configEnabled is a process-wide override set by the boot path once the
// config is loaded. Defaults to true so unit tests using components without
// loading config still see animations enabled.
var configEnabled atomic.Bool

func init() { configEnabled.Store(true) }

// SetEnabled is called once at startup by the TUI bootstrap to reflect
// cfg.UI.Animations. It is safe to call concurrently with Enabled().
func SetEnabled(on bool) { configEnabled.Store(on) }

// Enabled reports whether animations should run for the given terminal
// width. It returns false when:
//   - cfg.UI.Animations is false
//   - ETHOS_NO_ANIMATIONS=1 is set in the environment
//   - TERM=dumb
//   - termWidth < MinTermWidth
func Enabled(termWidth int) bool {
	if !configEnabled.Load() {
		return false
	}
	if os.Getenv("ETHOS_NO_ANIMATIONS") == "1" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	if termWidth > 0 && termWidth < MinTermWidth {
		return false
	}
	return true
}
