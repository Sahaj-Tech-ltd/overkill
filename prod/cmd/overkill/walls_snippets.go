package main

import (
	"os"

	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
)

// loadArchSnippet reads OVERKILL_ARCH.md from the current working dir
// and returns a truncated snippet for the per-turn context provider.
// Empty when the file is missing — first run of the project just
// produces it via EnsureArch and picks up next turn.
func loadArchSnippet(maxChars int) string {
	if maxChars <= 0 {
		maxChars = 4000
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(cwd + "/" + walls.ArchFile)
	if err != nil {
		return ""
	}
	s := string(data)
	if len(s) > maxChars {
		s = s[:maxChars] + "\n…(truncated; full file at " + walls.ArchFile + ")"
	}
	return "## Project architecture (Wall 2 reference)\n\n" + s
}

// loadGlossarySnippet reads CONTEXT.md from cwd. Same truncation
// policy as the arch snippet.
func loadGlossarySnippet(maxChars int) string {
	if maxChars <= 0 {
		maxChars = 2000
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(cwd + "/" + walls.GlossaryFile)
	if err != nil {
		return ""
	}
	s := string(data)
	if len(s) > maxChars {
		s = s[:maxChars] + "\n…(truncated; full file at " + walls.GlossaryFile + ")"
	}
	return "## Domain glossary\n\n" + s
}
