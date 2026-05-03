# cellrender

Cell-level diff renderer for the Ethos Bubble Tea TUI. Wraps the program's
output writer, parses each rendered frame into a cell grid, diffs it against
the previous frame, and emits only the minimal escape sequences needed to
update the visible terminal. Inspired by opentui's native renderer; aims to
match SSH-class smoothness for incremental frames (spinner ticks, single
keystrokes, cursor moves) where Bubble Tea v1's lipgloss line-diffing
otherwise rewrites entire rows. Opt in with `ETHOS_CELL_RENDER=1`. Default
behavior is completely unchanged when the env var is unset.

## Known limitations

The parser handles the SGR, cursor-movement, and erase sequences that
Bubble Tea + lipgloss actually emit. It deliberately ignores private-mode
sequences (`CSI ? ... h/l`) and OSC payloads (window title, hyperlinks)
because they don't change cell contents. Exotic terminals that emit
non-standard sequences may produce a slightly degraded diff (extra full
repaints) but never an incorrect frame: if anything looks off, the writer
honors `\x1b[2J` as a "next frame is full" signal, and `Writer.Disable()`
flips it into permanent passthrough. Wide runes (East Asian width=2) are
written into a single cell — fine for English/spinner content, may produce a
trailing space artifact for CJK-heavy content.

## Benchmark and verify

```
go test -run TestBytesPerFrameRatio ./pkg/tui/cellrender -v
go test -run TestIntegrationMovingChar ./pkg/tui/cellrender -v
go test -bench=. -benchmem ./pkg/tui/cellrender
```

The first two tests fail loudly if the cell-render path doesn't beat the
naïve "write the whole frame" baseline by the documented margin (<50% for
the spinner case, <30% for the moving-char case). To verify in your own
terminal: `ETHOS_CELL_RENDER=1 ethos tui` — a one-line stderr banner is
printed at startup so you know the path is active. `unset
ETHOS_CELL_RENDER` reverts to the standard Bubble Tea renderer with zero
risk.
