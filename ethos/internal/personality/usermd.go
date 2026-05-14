// Package personality — user.md seeding from cold-start profile.
//
// The plan calls for a human-editable markdown profile of the user
// at ~/.overkill/memories/user.md. Written once on cold-start from
// the inferred 5-dim + name + timezone, then OWNED by the user —
// we don't rewrite it on subsequent boots. The agent reads it as
// context; the user edits it directly when something's wrong.
//
// Distinct from relationship.json (machine-managed, JSON) and the
// failhypo / learnings streams (append-only JSONL). user.md is the
// surface the user actually opens in an editor.
package personality

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SeedUserMD writes a markdown profile derived from the cold-start
// profile to path. Refuses to overwrite an existing file (the user
// may have edited it). Returns (true, nil) when written, (false,
// nil) when the file already exists and was left alone.
func SeedUserMD(path string, profile *ColdStartProfile) (bool, error) {
	if profile == nil {
		return false, nil
	}
	if _, err := os.Stat(path); err == nil {
		// File exists — never overwrite. User edits beat our
		// inference every time.
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("personality: stat user.md: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("personality: mkdir user.md: %w", err)
	}
	content := renderUserMD(profile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return false, fmt.Errorf("personality: write user.md: %w", err)
	}
	return true, nil
}

// renderUserMD produces the markdown body. Kept terse on purpose —
// the file is for the USER to refine; we just seed the skeleton with
// what we inferred. Anything blank is shown as an "unknown" line so
// the user knows to fill it in.
func renderUserMD(p *ColdStartProfile) string {
	var b strings.Builder
	b.WriteString("# User\n\n")
	b.WriteString("> Auto-seeded by Overkill on cold-start (")
	b.WriteString(time.Now().UTC().Format("2006-01-02"))
	b.WriteString("). Edit freely — Overkill never overwrites this file.\n\n")

	b.WriteString("## Identity\n\n")
	b.WriteString(field("name", p.UserName))
	b.WriteString(field("timezone", p.Timezone))
	b.WriteByte('\n')

	b.WriteString("## How they work\n\n")
	b.WriteString(field("communication style", p.CommunicationStyle))
	b.WriteString(field("verbosity preference", p.VerbosityPreference))
	b.WriteString(field("technical depth", p.TechnicalDepth))
	b.WriteString(field("tone tolerance", p.ToneTolerance))
	b.WriteString(field("urgency baseline", p.UrgencyBaseline))
	b.WriteByte('\n')

	b.WriteString("## Notes\n\n")
	b.WriteString("- _Add anything Overkill should remember about you here. " +
		"Personal context (\"prefers tabs over spaces\"), domain shorthand, " +
		"or hard rules (\"never auto-commit, ever\")._\n")

	return b.String()
}

// field renders one "- **k**: v" line, substituting an italic
// "(unknown)" when v is empty.
func field(k, v string) string {
	if strings.TrimSpace(v) == "" {
		return "- **" + k + "**: _(unknown — fill in when convenient)_\n"
	}
	return "- **" + k + "**: " + v + "\n"
}
