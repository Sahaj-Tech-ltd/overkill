package personality

import (
	"fmt"
	"regexp"
	"strings"
)

// ParseLevel uses the engine's existing levels. None is a configuration alias
// for Off. Unknown values fail closed; config.Validate reports invalid settings.
func ParseLevel(value string) Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "subtle":
		return LevelSubtle
	case "witty":
		return LevelWitty
	case "full":
		return LevelFull
	default:
		return LevelOff
	}
}

var languageTag = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)

// PromptStyle governs expression only. It cannot authorize tools, weaken
// verification, add facts, or override a requested machine-readable format.
func PromptStyle(level Level, language string) string {
	var style string
	switch level {
	case LevelSubtle:
		style = "Communication style (subtle): Use a calm, friendly voice. Keep personality understated and wording concise; avoid jokes and theatrical flourishes."
	case LevelWitty:
		style = "Communication style (witty): Use occasional brief, dry wit when it fits naturally. Keep the answer direct; skip jokes during errors, frustration, or serious requests."
	case LevelFull:
		style = "Communication style (full): Use a lively, expressive voice with playful phrasing when appropriate. Keep explanations useful and focused; avoid forced jokes, insults, invented experiences, or distracting performance."
	}
	var parts []string
	if style != "" {
		parts = append(parts, style, "Style changes wording only. Accuracy, security boundaries, approval requirements, tool restrictions and the user's requested output format take priority. Follow explicit user tone preferences and become restrained for serious or urgent situations.")
	}
	// A locale is data, not another free-form prompt-instruction channel.
	language = strings.TrimSpace(language)
	if len(language) <= 64 && language != "" && language != "en" && languageTag.MatchString(language) {
		parts = append(parts, fmt.Sprintf("Respond in the language identified by locale %q, while preserving code, identifiers and any required output format.", language))
	}
	return strings.Join(parts, "\n")
}

func WithPromptStyle(base string, level Level, language string) string {
	style := PromptStyle(level, language)
	if style == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return style
	}
	return base + "\n\n" + style
}
