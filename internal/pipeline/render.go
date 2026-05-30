// Package pipeline — DeepWiki-style plan renderer.
//
// RenderPlan takes markdown plan text and produces a self-contained HTML file
// with dark theme, auto-generated table of contents, syntax-highlighted code
// blocks, collapsible sections, file tree visualization, and mobile-responsive
// layout. Output is written to ~/.overkill/plans/<name>.html.
package pipeline

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// RenderConfig controls the HTML output.
type RenderConfig struct {
	// Title overrides the auto-extracted title from the first H1.
	Title string
	// Name is the base filename (without .html) written to the plans directory.
	Name string
}

// RenderPlan converts markdown plan text to a self-contained HTML string.
// Use this for standalone rendering without file I/O.
func RenderPlan(markdown []byte, cfg RenderConfig) string {
	content := string(markdown)
	toc := extractTOC(content)
	htmlBody := renderMarkdown(content)
	title := cfg.Title
	if title == "" {
		title = extractTitle(content)
	}
	fileTree := extractFileTree(content)

	return buildHTML(title, toc, htmlBody, fileTree, time.Now().UTC())
}

// RenderPlanToFile renders markdown and writes the HTML to
// ~/.overkill/plans/<name>.html. Returns the full path to the written file.
func RenderPlanToFile(markdown []byte, cfg RenderConfig) (string, error) {
	name := cfg.Name
	if name == "" {
		name = "plan"
	}
	htmlStr := RenderPlan(markdown, cfg)

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("render: home dir: %w", err)
	}
	plansDir := filepath.Join(home, ".overkill", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		return "", fmt.Errorf("render: mkdir plans: %w", err)
	}

	path := filepath.Join(plansDir, name+".html")
	if err := os.WriteFile(path, []byte(htmlStr), 0o644); err != nil {
		return "", fmt.Errorf("render: write html: %w", err)
	}
	return path, nil
}

// tocItem represents one entry in the table of contents.
type tocItem struct {
	Level   int
	Text    string
	ID      string
	Num     int // hierarchical number like 1, 1.1, 2
	NumStr  string
}

func extractTOC(content string) []tocItem {
	headingRe := regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)
	matches := headingRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}

	// Build hierarchical numbering.
	counters := make([]int, 7) // counters[level]
	var items []tocItem
	for _, m := range matches {
		level := len(m[1])
		text := strings.TrimSpace(m[2])
		// Strip any existing markdown formatting for TOC text.
		text = stripFormatting(text)
		id := slugify(text)

		// Increment counter at this level and reset deeper levels.
		if level < len(counters) {
			counters[level]++
			for i := level + 1; i < len(counters); i++ {
				counters[i] = 0
			}
		}

		// Build hierarchical number string.
		var parts []string
		for i := 1; i <= level; i++ {
			if counters[i] > 0 {
				parts = append(parts, fmt.Sprintf("%d", counters[i]))
			}
		}

		items = append(items, tocItem{
			Level:  level,
			Text:   text,
			ID:     id,
			Num:    counters[level],
			NumStr: strings.Join(parts, "."),
		})
	}
	return items
}

func extractTitle(content string) string {
	re := regexp.MustCompile(`(?m)^#\s+(.+)$`)
	match := re.FindStringSubmatch(content)
	if len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	// Fallback: first non-empty line.
	for _, line := range strings.Split(content, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return truncate(t, 80)
		}
	}
	return "Plan"
}

// fileTreeEntry is a node in the file tree visualization.
type fileTreeEntry struct {
	Name     string
	Path     string
	IsDir    bool
	Children []fileTreeEntry
}

func extractFileTree(content string) []fileTreeEntry {
	// Look for file paths: relative paths, code block filenames, and common patterns.
	pathRe := regexp.MustCompile("(?m)(?:^|[\\s(])([a-zA-Z0-9_/.\\-]+\\.[a-zA-Z]{1,6})(?:[\\s:,)]|$)")
	codeFileRe := regexp.MustCompile("(?m)^```[a-z]+\\s+(\\S+)$")
	pathRe2 := regexp.MustCompile("(?m)`([a-zA-Z0-9_/.\\-]+\\.[a-zA-Z]{1,6})`")

	seen := make(map[string]bool)
	var paths []string

	// Code block filenames (```go filename.go)
	for _, m := range codeFileRe.FindAllStringSubmatch(content, -1) {
		p := strings.TrimSpace(m[1])
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	// Inline code paths
	for _, m := range pathRe2.FindAllStringSubmatch(content, -1) {
		p := strings.TrimSpace(m[1])
		if p != "" && !seen[p] && looksLikeFilePath(p) {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	// Loose paths
	for _, m := range pathRe.FindAllStringSubmatch(content, -1) {
		p := strings.TrimSpace(m[1])
		if p != "" && !seen[p] && looksLikeFilePath(p) {
			seen[p] = true
			paths = append(paths, p)
		}
	}

	if len(paths) == 0 {
		return nil
	}

	return buildFileTree(paths)
}

func looksLikeFilePath(s string) bool {
	// Avoid plain words or known non-path patterns.
	lower := strings.ToLower(s)
	// Must have a dot or slash.
	if !strings.Contains(s, ".") && !strings.Contains(s, "/") {
		return false
	}
	// Known extensions.
	if strings.HasSuffix(lower, ".go") || strings.HasSuffix(lower, ".py") ||
		strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".ts") ||
		strings.HasSuffix(lower, ".rs") || strings.HasSuffix(lower, ".java") ||
		strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".css") ||
		strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".yaml") ||
		strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".toml") ||
		strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".sql") ||
		strings.HasSuffix(lower, ".proto") || strings.HasSuffix(lower, ".sh") ||
		strings.HasSuffix(lower, ".dockerfile") || strings.HasSuffix(lower, ".tf") ||
		strings.HasSuffix(lower, ".xml") || strings.HasSuffix(lower, ".csv") ||
		strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".mod") || strings.HasSuffix(lower, ".sum") ||
		strings.HasSuffix(lower, ".lock") || strings.HasSuffix(lower, ".cfg") ||
		strings.HasSuffix(lower, ".conf") || strings.HasSuffix(lower, ".ini") ||
		strings.HasSuffix(lower, ".env") || strings.HasSuffix(lower, ".svg") ||
		strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") ||
		strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".gif") ||
		strings.HasSuffix(lower, ".mp4") || strings.HasSuffix(lower, ".mp3") ||
		strings.HasSuffix(lower, ".wasm") || strings.HasSuffix(lower, ".so") ||
		strings.HasSuffix(lower, ".dll") || strings.HasSuffix(lower, ".a") ||
		strings.HasSuffix(lower, ".o") || strings.HasSuffix(lower, ".h") ||
		strings.HasSuffix(lower, ".hpp") || strings.HasSuffix(lower, ".c") ||
		strings.HasSuffix(lower, ".cpp") || strings.HasSuffix(lower, ".rb") ||
		strings.HasSuffix(lower, ".php") || strings.HasSuffix(lower, ".swift") ||
		strings.HasSuffix(lower, ".kt") || strings.HasSuffix(lower, ".scala") ||
		strings.HasSuffix(lower, ".ex") || strings.HasSuffix(lower, ".exs") ||
		strings.HasSuffix(lower, ".lua") || strings.HasSuffix(lower, ".r") ||
		strings.HasSuffix(lower, ".dart") || strings.HasSuffix(lower, ".vue") ||
		strings.HasSuffix(lower, ".svelte") || strings.HasSuffix(lower, ".elm") ||
		strings.HasSuffix(lower, ".hs") || strings.HasSuffix(lower, ".clj") {
		return true
	}
	return false
}

// fileTreeNode is an internal node for file tree construction.
type fileTreeNode struct {
	Entry    fileTreeEntry
	Children map[string]*fileTreeNode
}

func buildFileTree(paths []string) []fileTreeEntry {
	// Build a tree using a nested map structure.
	root := &fileTreeNode{Children: make(map[string]*fileTreeNode)}

	for _, path := range paths {
		parts := splitPath(path)
		if len(parts) == 0 {
			continue
		}

		current := root
		for i, part := range parts {
			isLast := i == len(parts)-1
			if existing, ok := current.Children[part]; ok {
				current = existing
				if !isLast {
					existing.Entry.IsDir = true
				}
			} else {
				newNode := &fileTreeNode{
					Entry: fileTreeEntry{
						Name:  part,
						Path:  strings.Join(parts[:i+1], "/"),
						IsDir: !isLast,
					},
					Children: make(map[string]*fileTreeNode),
				}
				current.Children[part] = newNode
				current = newNode
			}
		}
	}

	return flattenNodeTree(root)
}

func flattenNodeTree(n *fileTreeNode) []fileTreeEntry {
	var result []fileTreeEntry
	for _, child := range n.Children {
		entry := child.Entry
		if len(child.Children) > 0 {
			entry.Children = flattenNodeTree(child)
			entry.IsDir = true
		}
		result = append(result, entry)
	}
	sortFileTree(result)
	return result
}

func splitPath(p string) []string {
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(p, "/")
	var result []string
	for _, part := range parts {
		if part != "" && part != "." {
			result = append(result, part)
		}
	}
	return result
}

func sortFileTree(entries []fileTreeEntry) {
	// Simple bubble sort: directories first, then alphabetical.
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			// Dir before file.
			if entries[i].IsDir != entries[j].IsDir {
				if !entries[i].IsDir && entries[j].IsDir {
					entries[i], entries[j] = entries[j], entries[i]
				}
			} else if entries[i].Name > entries[j].Name {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
	for i := range entries {
		if len(entries[i].Children) > 0 {
			sortFileTree(entries[i].Children)
		}
	}
}

// ── Markdown to HTML ─────────────────────────────────────────────────────

var (
	codeBlockRe = regexp.MustCompile("(?s)```(\\w*)\\s*\\n(.*?)```")
	inlineCodeRe = regexp.MustCompile("`([^`]+)`")
	boldRe       = regexp.MustCompile(`\*\*(.+?)\*\*`)
	italicRe     = regexp.MustCompile(`\*(.+?)\*`)
	strikeRe     = regexp.MustCompile(`~~(.+?)~~`)
	linkRe       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	headingRe    = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)
	hrRe         = regexp.MustCompile(`(?m)^---+$`)
	ulRe         = regexp.MustCompile(`(?m)^[\t ]*[-*+]\s+(.+)$`)
	olRe         = regexp.MustCompile(`(?m)^[\t ]*\d+\.\s+(.+)$`)
	blockquoteRe = regexp.MustCompile(`(?m)^>\s?(.*)$`)
)

func renderMarkdown(content string) string {
	// First, extract and replace code blocks with placeholders to protect them.
	codeBlocks := make(map[string]string)
	placeholderRe := regexp.MustCompile(`%%CODEBLOCK_(\d+)%%`)

	processed := codeBlockRe.ReplaceAllStringFunc(content, func(match string) string {
		sub := codeBlockRe.FindStringSubmatch(match)
		lang := ""
		code := match
		if len(sub) > 2 {
			lang = strings.TrimSpace(sub[1])
			code = sub[2]
		}
		id := fmt.Sprintf("%d", len(codeBlocks))
		codeBlocks[id] = renderCodeBlock(code, lang)
		return "%%CODEBLOCK_" + id + "%%"
	})

	// Split into lines for block-level processing.
	lines := strings.Split(processed, "\n")
	var out strings.Builder
	inList := false
	inBlockquote := false
	inParagraph := false

	flushList := func() {
		if inList {
			out.WriteString("</ul>\n")
			inList = false
		}
	}
	flushBlockquote := func() {
		if inBlockquote {
			out.WriteString("</blockquote>\n")
			inBlockquote = false
		}
	}
	flushParagraph := func() {
		if inParagraph {
			out.WriteString("</p>\n")
			inParagraph = false
		}
	}
	flushBlocks := func() {
		flushParagraph()
		flushList()
		flushBlockquote()
	}

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Headings.
		if headingRe.MatchString(line) {
			flushBlocks()
			m := headingRe.FindStringSubmatch(line)
			level := len(m[1])
			text := m[2]
			id := slugify(stripFormatting(text))
			out.WriteString(fmt.Sprintf("<h%d id=\"%s\">%s</h%d>\n", level, id, renderInline(text), level))
			i++
			continue
		}

		// Horizontal rule.
		if hrRe.MatchString(trimmed) && len(trimmed) >= 3 && strings.Count(trimmed, "-") == len(trimmed) {
			flushBlocks()
			out.WriteString("<hr>\n")
			i++
			continue
		}

		// Code block placeholder.
		if placeholderRe.MatchString(trimmed) {
			flushBlocks()
			out.WriteString(placeholderRe.ReplaceAllStringFunc(trimmed, func(m string) string {
				sub := placeholderRe.FindStringSubmatch(m)
				return codeBlocks[sub[1]]
			}))
			out.WriteString("\n")
			i++
			continue
		}

		// Unordered list.
		if ulRe.MatchString(line) {
			flushBlockquote()
			flushParagraph()
			if !inList {
				out.WriteString("<ul>\n")
				inList = true
			}
			m := ulRe.FindStringSubmatch(line)
			content := renderInline(m[1])
			out.WriteString(fmt.Sprintf("  <li>%s</li>\n", content))

			// Consume continuation lines (indented lines without bullet).
			for i+1 < len(lines) {
				next := lines[i+1]
				nextTrim := strings.TrimSpace(next)
				if nextTrim == "" || ulRe.MatchString(next) || olRe.MatchString(next) ||
					headingRe.MatchString(next) || placeholderRe.MatchString(nextTrim) {
					break
				}
				i++
				if nextTrim != "" {
					out.WriteString(fmt.Sprintf("  %s\n", renderInline(nextTrim)))
				}
			}
			i++
			continue
		}

		// Ordered list.
		if olRe.MatchString(line) {
			flushBlockquote()
			flushParagraph()
			if !inList {
				inList = true
				out.WriteString("<ul>\n")
			}
			m := olRe.FindStringSubmatch(line)
			content := renderInline(m[1])
			out.WriteString(fmt.Sprintf("  <li>%s</li>\n", content))

			for i+1 < len(lines) {
				next := lines[i+1]
				nextTrim := strings.TrimSpace(next)
				if nextTrim == "" || ulRe.MatchString(next) || olRe.MatchString(next) ||
					headingRe.MatchString(next) || placeholderRe.MatchString(nextTrim) {
					break
				}
				i++
				if nextTrim != "" {
					out.WriteString(fmt.Sprintf("  %s\n", renderInline(nextTrim)))
				}
			}
			i++
			continue
		}

		// Blockquote.
		if blockquoteRe.MatchString(line) {
			flushList()
			flushParagraph()
			if !inBlockquote {
				out.WriteString("<blockquote>\n")
				inBlockquote = true
			}
			m := blockquoteRe.FindStringSubmatch(line)
			content := renderInline(m[1])
			out.WriteString(fmt.Sprintf("  <p>%s</p>\n", content))
			i++
			continue
		}

		// Blank line — flush blocks, start new paragraph.
		if trimmed == "" {
			flushBlocks()
			i++
			continue
		}

		// Regular paragraph text.
		flushList()
		flushBlockquote()
		if !inParagraph {
			out.WriteString("<p>")
			inParagraph = true
		} else {
			out.WriteString("<br>")
		}
		out.WriteString(renderInline(trimmed))
		i++

		// If next line is blank or block-level, close paragraph.
		if i < len(lines) {
			next := strings.TrimSpace(lines[i])
			if next == "" || headingRe.MatchString(lines[i]) || ulRe.MatchString(lines[i]) ||
				olRe.MatchString(lines[i]) || blockquoteRe.MatchString(lines[i]) ||
				placeholderRe.MatchString(next) || hrRe.MatchString(next) {
				flushParagraph()
			}
		}
	}

	flushBlocks()
	return out.String()
}

func renderInline(text string) string {
	// Protect code blocks already used as placeholders.
	// Escape HTML entities.
	text = html.EscapeString(text)

	// Inline code: `code`
	text = inlineCodeRe.ReplaceAllString(text, `<code class="inline">$1</code>`)

	// Bold: **text**
	text = boldRe.ReplaceAllString(text, `<strong>$1</strong>`)

	// Italic: *text* (after bold to avoid conflict)
	text = italicRe.ReplaceAllString(text, `<em>$1</em>`)

	// Strikethrough: ~~text~~
	text = strikeRe.ReplaceAllString(text, `<del>$1</del>`)

	// Links: [text](url)
	text = linkRe.ReplaceAllString(text, `<a href="$2" target="_blank">$1</a>`)

	// Replace placeholder references back to code blocks (if any inline).
	phRe := regexp.MustCompile(`%%CODEBLOCK_(\d+)%%`)
	text = phRe.ReplaceAllString(text, `<code>${1}</code>`)

	return text
}

func renderCodeBlock(code, lang string) string {
	escaped := html.EscapeString(code)
	highlighted := basicHighlight(escaped, lang)
	langClass := ""
	if lang != "" {
		langClass = fmt.Sprintf(` data-lang="%s"`, html.EscapeString(lang))
	}
	return fmt.Sprintf(`<pre class="code-block"%s><code>%s</code></pre>`, langClass, highlighted)
}

// basicHighlight applies simple token coloring for common languages.
func basicHighlight(code, lang string) string {
	if lang == "" {
		return code
	}
	lower := strings.ToLower(lang)

	// Go keywords.
	goKeywords := []string{
		"break", "case", "chan", "const", "continue", "default", "defer",
		"else", "fallthrough", "for", "func", "go", "goto", "if", "import",
		"interface", "map", "package", "range", "return", "select", "struct",
		"switch", "type", "var",
	}
	// Common keywords for JS/TS/Python/etc.
	commonKeywords := []string{
		"function", "class", "return", "if", "else", "for", "while", "do",
		"switch", "case", "break", "continue", "new", "this", "super",
		"try", "catch", "finally", "throw", "async", "await", "yield",
		"import", "export", "from", "default", "extends", "implements",
		"const", "let", "var", "static", "public", "private", "protected",
	}
	pyKeywords := []string{
		"def", "class", "return", "if", "elif", "else", "for", "while",
		"import", "from", "as", "try", "except", "finally", "raise",
		"with", "yield", "lambda", "pass", "break", "continue", "and",
		"or", "not", "in", "is", "None", "True", "False", "self",
	}
	rsKeywords := []string{
		"fn", "let", "mut", "impl", "trait", "enum", "match", "use", "mod",
		"pub", "crate", "self", "super", "where", "async", "await", "move",
		"ref", "dyn", "unsafe", "extern", "loop", "while", "for", "if",
		"else", "return", "struct", "type", "const", "static", "macro_rules!",
	}

	var kwList []string
	switch lower {
	case "go", "golang":
		kwList = append(kwList, goKeywords...)
		kwList = append(kwList, "nil", "true", "false", "iota")
	case "py", "python":
		kwList = pyKeywords
	case "rs", "rust":
		kwList = rsKeywords
	case "js", "javascript", "ts", "typescript", "java", "c", "cpp", "c++", "cs", "csharp":
		kwList = commonKeywords
	default:
		kwList = commonKeywords
	}

	for _, kw := range kwList {
		wordRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(kw) + `\b`)
		code = wordRe.ReplaceAllString(code, `<span class="kw">`+kw+`</span>`)
	}

	// Strings: "..." and '...'
	strRe := regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	code = strRe.ReplaceAllString(code, `<span class="str">$0</span>`)
	strRe2 := regexp.MustCompile(`'(?:[^'\\]|\\.)*'`)
	code = strRe2.ReplaceAllString(code, `<span class="str">$0</span>`)

	// Comments: // and /* */
	lineCommentRe := regexp.MustCompile(`(//.*)$`)
	code = lineCommentRe.ReplaceAllString(code, `<span class="cm">$1</span>`)
	blockCommentRe := regexp.MustCompile(`(/\*.*?\*/)`)
	code = blockCommentRe.ReplaceAllString(code, `<span class="cm">$1</span>`)

	// Numbers.
	numRe := regexp.MustCompile(`\b(\d+\.?\d*(?:[eE][+-]?\d+)?)\b`)
	code = numRe.ReplaceAllString(code, `<span class="num">$1</span>`)

	return code
}

// ── Helpers ────────────────────────────────────────────────────────────────

func stripFormatting(text string) string {
	text = boldRe.ReplaceAllString(text, "$1")
	text = italicRe.ReplaceAllString(text, "$1")
	text = inlineCodeRe.ReplaceAllString(text, "$1")
	text = strikeRe.ReplaceAllString(text, "$1")
	text = linkRe.ReplaceAllString(text, "$1")
	return text
}

func slugify(text string) string {
	text = strings.ToLower(text)
	nonWord := regexp.MustCompile(`[^a-z0-9]+`)
	text = nonWord.ReplaceAllString(text, "-")
	text = strings.Trim(text, "-")
	return text
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// ── Full HTML document builder ─────────────────────────────────────────────

func buildHTML(title string, toc []tocItem, body string, fileTree []fileTreeEntry, genTime time.Time) string {
	var b strings.Builder

	b.WriteString("<!DOCTYPE html>\n")
	b.WriteString(`<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(` — Overkill Plan</title>
<style>
:root {
  --bg: #1a1a2e;
  --bg-card: #16213e;
  --bg-hover: #1f2f50;
  --accent: #e94560;
  --accent-dim: #c23152;
  --text: #e0e0e0;
  --text-muted: #8899aa;
  --text-heading: #f0f0f0;
  --border: #2a3a5e;
  --code-bg: #0f1923;
  --kw: #e94560;
  --str: #a3be8c;
  --cm: #616e88;
  --num: #d08770;
  --inline-code-bg: #1a2740;
  --sidebar-width: 280px;
  --font-mono: 'JetBrains Mono', 'Fira Code', 'Cascadia Code', 'SF Mono', Menlo, Consolas, monospace;
  --font-sans: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
}
*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
html { scroll-behavior: smooth; font-size: 15px; }
body {
  font-family: var(--font-sans);
  background: var(--bg);
  color: var(--text);
  line-height: 1.7;
  display: flex;
  min-height: 100vh;
}
a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }

/* ── Sidebar ── */
.sidebar {
  position: fixed;
  top: 0; left: 0; bottom: 0;
  width: var(--sidebar-width);
  background: var(--bg-card);
  border-right: 1px solid var(--border);
  overflow-y: auto;
  padding: 1.5rem;
  z-index: 100;
  display: flex;
  flex-direction: column;
}
.sidebar h2 {
  font-size: 0.85rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-muted);
  margin-bottom: 0.75rem;
  font-weight: 700;
}
.sidebar .logo {
  font-size: 1.1rem;
  font-weight: 800;
  color: var(--accent);
  margin-bottom: 1.5rem;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.sidebar .logo::before {
  content: '🔧';
  font-size: 1.3rem;
}

.toc-list { list-style: none; }
.toc-list li { margin-bottom: 0.15rem; }
.toc-list a {
  display: block;
  padding: 0.3rem 0.5rem;
  border-radius: 4px;
  font-size: 0.85rem;
  color: var(--text-muted);
  transition: all 0.15s;
  border-left: 2px solid transparent;
}
.toc-list a:hover {
  background: var(--bg-hover);
  color: var(--text);
  text-decoration: none;
}
.toc-list a.active {
  color: var(--accent);
  border-left-color: var(--accent);
  background: rgba(233, 69, 96, 0.08);
}
.toc-list .toc-h1 a { font-weight: 700; color: var(--text-heading); font-size: 0.9rem; }
.toc-list .toc-h2 a { padding-left: 1rem; }
.toc-list .toc-h3 a { padding-left: 1.8rem; }
.toc-list .toc-h4 a { padding-left: 2.5rem; }
.toc-list .toc-h5 a { padding-left: 3rem; font-size: 0.8rem; }
.toc-list .toc-num { color: var(--accent); font-size: 0.75em; margin-right: 0.3rem; opacity: 0.7; }

.sidebar-footer {
  margin-top: auto;
  padding-top: 1rem;
  border-top: 1px solid var(--border);
  font-size: 0.75rem;
  color: var(--text-muted);
}

/* ── Main content ── */
.main {
  margin-left: var(--sidebar-width);
  flex: 1;
  max-width: 900px;
  padding: 3rem;
}
.main h1 {
  font-size: 2rem;
  color: var(--text-heading);
  margin-bottom: 0.5rem;
  padding-bottom: 0.5rem;
  border-bottom: 2px solid var(--accent);
}
.main h2 {
  font-size: 1.5rem;
  color: var(--text-heading);
  margin-top: 2.5rem; margin-bottom: 0.75rem;
  padding-bottom: 0.3rem;
  border-bottom: 1px solid var(--border);
}
.main h3 { font-size: 1.2rem; color: var(--text-heading); margin-top: 1.75rem; margin-bottom: 0.5rem; }
.main h4 { font-size: 1.05rem; color: var(--text-heading); margin-top: 1.25rem; margin-bottom: 0.4rem; }
.main h5, .main h6 { font-size: 1rem; color: var(--text-muted); margin-top: 1rem; margin-bottom: 0.3rem; }

.main p { margin-bottom: 1rem; }
.main ul, .main ol { margin-bottom: 1rem; padding-left: 1.5rem; }
.main li { margin-bottom: 0.3rem; }
.main hr { border: none; border-top: 1px solid var(--border); margin: 2rem 0; }
.main blockquote {
  border-left: 3px solid var(--accent);
  padding: 0.5rem 1rem;
  margin: 1rem 0;
  background: var(--bg-card);
  border-radius: 0 6px 6px 0;
}
.main blockquote p { margin: 0; }

/* ── Code blocks ── */
.main pre.code-block {
  background: var(--code-bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 1rem 1.25rem;
  overflow-x: auto;
  margin: 1rem 0;
  position: relative;
}
.main pre.code-block::before {
  content: attr(data-lang);
  position: absolute;
  top: 0; right: 0.75rem;
  font-size: 0.7rem;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  padding: 0.2rem 0.5rem;
  background: var(--bg-card);
  border-radius: 0 8px 0 6px;
}
.main pre.code-block code {
  font-family: var(--font-mono);
  font-size: 0.85rem;
  line-height: 1.6;
  color: var(--text);
}
code.inline {
  font-family: var(--font-mono);
  background: var(--inline-code-bg);
  padding: 0.15em 0.4em;
  border-radius: 3px;
  font-size: 0.9em;
  color: var(--accent);
}

/* ── Syntax highlighting ── */
.kw  { color: var(--kw); font-weight: 600; }
.str { color: var(--str); }
.cm  { color: var(--cm); font-style: italic; }
.num { color: var(--num); }

/* ── Collapsible sections ── */
details.section {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 8px;
  margin: 1rem 0;
  padding: 0;
  overflow: hidden;
}
details.section > summary {
  padding: 0.75rem 1rem;
  cursor: pointer;
  font-weight: 600;
  color: var(--text-heading);
  user-select: none;
  transition: background 0.15s;
  list-style: none;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
details.section > summary::-webkit-details-marker { display: none; }
details.section > summary::before {
  content: '▸';
  display: inline-block;
  transition: transform 0.2s;
  font-size: 0.8rem;
  color: var(--accent);
}
details.section[open] > summary::before { transform: rotate(90deg); }
details.section > summary:hover { background: var(--bg-hover); }
details.section .section-content { padding: 0 1.25rem 1rem 1.25rem; }

/* ── File tree ── */
.file-tree-section {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 1.25rem;
  margin: 1.5rem 0;
}
.file-tree-section h3 {
  margin-top: 0;
  font-size: 0.9rem;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  margin-bottom: 0.75rem;
}
.file-tree { font-family: var(--font-mono); font-size: 0.85rem; }
.file-tree .tree-entry { padding: 0.2rem 0; display: flex; align-items: center; gap: 0.35rem; color: var(--text-muted); }
.file-tree .tree-dir  { color: var(--accent); font-weight: 600; }
.file-tree .tree-file { color: var(--text); }
.file-tree .tree-icon { width: 1.2rem; text-align: center; flex-shrink: 0; }
.file-tree .tree-indent { margin-left: 1.5rem; }

/* ── Architecture diagram placeholder ── */
.arch-diagram {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 2rem;
  margin: 1.5rem 0;
  text-align: center;
}
.arch-diagram .arch-icon {
  font-size: 2.5rem;
  margin-bottom: 0.75rem;
  display: block;
}
.arch-diagram h4 { margin: 0 0 0.5rem 0; color: var(--text-heading); }
.arch-diagram p { color: var(--text-muted); font-size: 0.85rem; margin: 0; }

/* ── Status badges ── */
.badge {
  display: inline-block;
  padding: 0.2em 0.6em;
  border-radius: 3px;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.03em;
}
.badge-green  { background: rgba(163, 190, 140, 0.15); color: #a3be8c; }
.badge-red    { background: rgba(233, 69, 96, 0.12); color: var(--accent); }
.badge-blue   { background: rgba(94, 129, 172, 0.15); color: #81a1c1; }

/* ── Generation footer ── */
.gen-footer {
  margin-top: 3rem;
  padding-top: 1rem;
  border-top: 1px solid var(--border);
  font-size: 0.8rem;
  color: var(--text-muted);
}

/* ── Mobile ── */
@media (max-width: 768px) {
  .sidebar { display: none; }
  .main { margin-left: 0; padding: 1.5rem; }
  .main h1 { font-size: 1.5rem; }
  .main h2 { font-size: 1.2rem; }
  body { font-size: 14px; }
}
@media (max-width: 1024px) {
  :root { --sidebar-width: 240px; }
  .main { padding: 2rem; }
}
</style>
</head>
<body>
`)

	// Sidebar.
	b.WriteString(`<aside class="sidebar">
<div class="logo">Overkill Plan</div>
`)
	if len(toc) > 0 {
		b.WriteString("<h2>Contents</h2>\n<ul class=\"toc-list\">\n")
		for _, item := range toc {
			cls := fmt.Sprintf("toc-h%d", item.Level)
			numSpan := ""
			if item.NumStr != "" {
				numSpan = fmt.Sprintf(`<span class="toc-num">%s</span>`, item.NumStr)
			}
			b.WriteString(fmt.Sprintf(`<li class="%s"><a href="#%s">%s%s</a></li>`+"\n",
				cls, item.ID, numSpan, html.EscapeString(item.Text)))
		}
		b.WriteString("</ul>\n")
	}

	b.WriteString(fmt.Sprintf(`<div class="sidebar-footer">
Generated %s<br>Overkill Pipeline
</div>
</aside>
`, genTime.Format("2006-01-02 15:04 UTC")))

	// Main content.
	b.WriteString("<main class=\"main\">\n")
	b.WriteString(fmt.Sprintf("<h1>%s</h1>\n", html.EscapeString(title)))

	// Architecture diagram placeholder.
	b.WriteString(`<div class="arch-diagram">
<span class="arch-icon">🏗️</span>
<h4>Architecture Overview</h4>
<p>This plan defines the system architecture, components, and their interactions.</p>
</div>
`)

	// File tree.
	if len(fileTree) > 0 {
		b.WriteString("<div class=\"file-tree-section\">\n<h3>📁 Files</h3>\n")
		b.WriteString("<div class=\"file-tree\">\n")
		renderFileTreeHTML(&b, fileTree, 0)
		b.WriteString("</div>\n</div>\n")
	}

	b.WriteString(body)
	b.WriteString(fmt.Sprintf(`<div class="gen-footer">
Generated by <strong>Overkill Pipeline</strong> on %s
</div>
`, genTime.Format("January 2, 2006 at 15:04 UTC")))

	b.WriteString("\n</main>\n</body>\n</html>\n")

	return b.String()
}

func renderFileTreeHTML(b *strings.Builder, entries []fileTreeEntry, depth int) {
	indent := ""
	if depth > 0 {
		indent = ` style="margin-left: ` + fmt.Sprintf("%.1f", float64(depth)*1.5) + `rem"`
	}
	for _, e := range entries {
		icon := "📄"
		cls := "tree-file"
		if e.IsDir {
			icon = "📁"
			cls = "tree-dir"
		}
		b.WriteString(fmt.Sprintf(`<div class="tree-entry"%s><span class="tree-icon">%s</span><span class="%s">%s</span></div>`+"\n",
			indent, icon, cls, html.EscapeString(e.Name)))
		if len(e.Children) > 0 {
			renderFileTreeHTML(b, e.Children, depth+1)
		}
	}
}
