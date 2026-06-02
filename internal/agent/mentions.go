// Package agent — @path mention pre-loader.
//
// When a user message contains tokens like `@README.md` or `@internal/foo.go`,
// we pre-fetch the file contents (best effort, capped per file and total) and
// inject them into the conversation as a system message before the model is
// asked to respond.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// mentionRe matches @path tokens. Path = a non-whitespace sequence with no
// leading slash that contains at least one path-friendly char.
var mentionRe = regexp.MustCompile(`(?:^|\s)@([\w./\-+_]+)`)

const (
	maxMentionFileBytes = 16 * 1024
	maxMentionFiles     = 10
)

// containsAtMention reports whether the input contains a routable @path token.
// Cheap probe used by the smart router; doesn't read files.
func containsAtMention(userInput string) bool {
	return mentionRe.MatchString(userInput)
}

// loadAtMentions scans userInput for @path tokens and returns a formatted
// block with the contents of each referenced file. Returns "" if none found.
func loadAtMentions(userInput string) string {
	matches := mentionRe.FindAllStringSubmatch(userInput, -1)
	if len(matches) == 0 {
		return ""
	}
	seen := make(map[string]bool)
	var paths []string
	for _, m := range matches {
		p := m[1]
		if seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
		if len(paths) >= maxMentionFiles {
			break
		}
	}
	sort.Strings(paths)

	cwd, _ := os.Getwd()
	var b strings.Builder
	for _, p := range paths {
		full := p
		if !filepath.IsAbs(full) {
			full = filepath.Join(cwd, p)
		}
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			fmt.Fprintf(&b, "@%s — (not a readable file)\n", p)
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			fmt.Fprintf(&b, "@%s — read error: %s\n", p, err.Error())
			continue
		}
		truncated := false
		if len(data) > maxMentionFileBytes {
			data = data[:maxMentionFileBytes]
			truncated = true
		}
		fmt.Fprintf(&b, "--- @%s ---\n%s\n", p, string(data))
		if truncated {
			fmt.Fprintf(&b, "[truncated at %d bytes]\n", maxMentionFileBytes)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
