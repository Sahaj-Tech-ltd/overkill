package dialog

// Window returns a slice of items centered around the cursor that fits within
// max rows, plus the count of items hidden above and below the visible window.
//
// max <= 0 or len(items) <= max returns the full slice with before=after=0.
// cursor is clamped to [0, len(items)-1].
//
// Used by long scrollable dialogs (commands, models, permissions ledger,
// subagents, workspaces, ...) so cursor movement stays inside the visible
// rows instead of being truncated by the parent dialog's MaxHeight cap.
func Window(items []string, cursor, max int) (visible []string, before, after int) {
	n := len(items)
	if n == 0 {
		return nil, 0, 0
	}
	if max <= 0 || n <= max {
		return items, 0, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= n {
		cursor = n - 1
	}

	// Center cursor in window when possible.
	half := max / 2
	start := cursor - half
	if start < 0 {
		start = 0
	}
	end := start + max
	if end > n {
		end = n
		start = end - max
		if start < 0 {
			start = 0
		}
	}
	return items[start:end], start, n - end
}

// WindowSize returns the number of rows the windowed view should target,
// given the parent dialog's totalHeight. Centralized here so every long
// dialog uses the same budget (-8 chrome, capped at 15, floor 5).
func WindowSize(totalHeight int) int {
	maxRows := totalHeight - 8
	if maxRows > 15 {
		maxRows = 15
	}
	if maxRows < 5 {
		maxRows = 5
	}
	return maxRows
}
