// Package agent — @path mention pre-loader.
//
// When a user message contains tokens like `@README.md` or `@internal/foo.go`,
// we pre-fetch the file contents (best effort, capped per file and total) and
// inject them into the conversation as a system message before the model is
// asked to respond.
package agent

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

// mentionRe matches @path tokens. Path = a non-whitespace sequence with no
// at least one path-friendly char. All paths still require workspace containment.
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
	root, err := os.Getwd()
	if err != nil {
		return ""
	}
	return loadWorkspaceMentions(context.Background(), root, userInput)
}

func loadWorkspaceMentions(ctx context.Context, root, userInput string) string {
	if root == "" {
		return ""
	}
	matches := mentionRe.FindAllStringSubmatch(userInput, 128)
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

	var b strings.Builder
	for _, p := range paths {
		if len(p) > 4096 {
			fmt.Fprintf(&b, "@%s… — (path exceeds workspace read bound)\n", p[:128])
			continue
		}
		data, truncated, err := tools.ReadWorkspaceFile(ctx, root, p, maxMentionFileBytes)
		if err != nil {
			fmt.Fprintf(&b, "@%s — (not a readable file within workspace)\n", p)
			continue
		}
		fmt.Fprintf(&b, "--- @%s ---\n%s\n", p, string(data))
		if truncated {
			fmt.Fprintf(&b, "[truncated at %d bytes]\n", maxMentionFileBytes)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
