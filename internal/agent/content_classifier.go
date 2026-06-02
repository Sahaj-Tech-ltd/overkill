// Package agent — content classifier for situational tool awareness (§8.6.2).
//
// Classifies user input into content types so the agent can decide
// modality (text only, offer audio, be concise, be structured, etc.)
// BEFORE acting. This is the first step in the pre-action reflection
// chain: classify → check user model → inventory tools → decide modality.

package agent

import (
	"strings"
	"unicode"
)

// ContentType classifies what kind of content the user is sending.
type ContentType int

const (
	ContentUnknown       ContentType = iota
	ContentResearchDense             // papers, long-form technical, academic
	ContentEmotionalVent             // frustration, sadness, vulnerability
	ContentCodeReview                // PRs, diffs, bugs, architecture
	ContentCasualChat                // small talk, jokes, low-stakes
	ContentMultiItem                 // 3+ distinct requests in one message
	ContentInstruction               // single clear directive
	ContentNewsDigest                // summaries, digests, briefing
)

// String returns the content type name.
func (ct ContentType) String() string {
	switch ct {
	case ContentResearchDense:
		return "research_dense"
	case ContentEmotionalVent:
		return "emotional_vent"
	case ContentCodeReview:
		return "code_review"
	case ContentCasualChat:
		return "casual_chat"
	case ContentMultiItem:
		return "multi_item"
	case ContentInstruction:
		return "instruction"
	case ContentNewsDigest:
		return "news_digest"
	default:
		return "unknown"
	}
}

// Classification is the result of content classification.
type Classification struct {
	Type       ContentType
	Confidence float64 // 0.0–1.0, heuristic confidence
	Reason     string  // why this classification
	ItemCount  int     // if MultiItem, how many items detected
}

// ContentClassifier classifies user input into content types using
// lightweight keyword + pattern heuristics. Designed to be cheap enough
// to run on every turn (no LLM call). Can be extended with an optional
// LLM-based classifier for higher accuracy.
type ContentClassifier struct {
	// ResearchSignals — words strongly correlated with academic/research content.
	ResearchSignals []string
	// EmotionalSignals — words correlated with emotional content.
	EmotionalSignals []string
	// CodeSignals — words correlated with code/engineering content.
	CodeSignals []string
	// MultiItemSeparators — patterns that split multi-item dumps.
	MultiItemSeparators []string
	// MinItemLength for multi-item detection — items shorter than this
	// are not counted as separate work items.
	MinItemLength int
}

// NewContentClassifier returns a classifier with sensible defaults.
func NewContentClassifier() *ContentClassifier {
	return &ContentClassifier{
		ResearchSignals: []string{
			"paper", "arxiv", "research", "study",
			"journal", "conference", "findings", "methodology",
			"abstract", "introduction", "related work", "results",
			"implications", "limitations", "future work",
			"transformer", "attention", "LLM", "language model",
			"neural network", "training", "fine-tuning", "benchmark",
			"architecture", "embedding", "token", "alignment",
			"deep learning", "machine learning", "RLHF",
			"doi.org", "et al.",
		},
		EmotionalSignals: []string{
			"frustrated", "angry", "sad", "upset", "tired",
			"exhausted", "anxious", "worried", "scared",
			"overwhelmed", "burned out", "done with",
			"can't deal", "too much", "hate this",
		},
		CodeSignals: []string{
			"bug", "fix", "refactor", "implement", "deploy",
			"PR", "pull request", "merge", "commit", "push",
			"build", "test", "CI", "pipeline", "review",
			"function", "struct", "interface", "package",
			"import", "export", "type", "class", "method",
			"error", "panic", "crash", "segfault", "race",
		},
		MultiItemSeparators: []string{
			"\n- ", "\n• ", "\n* ", "\n1. ", "\n2. ",
			"\nand ", "\nthen ", "\nalso ", "\nplus ",
			". also ", ". then ", ". next ", ". plus ",
			"; also ", "; then ", "; next ",
			" | ", " || ",
		},
		MinItemLength: 10,
	}
}

// Classify runs the classifier on user input. Returns the best-matching
// type with a confidence score and reason.
func (cc *ContentClassifier) Classify(input string) Classification {
	inputLower := strings.ToLower(input)
	inputLen := len(input)

	// Check multi-item first — it's the most actionable classification
	// (triggers sequential processing mode).
	if itemCount := cc.countItems(input); itemCount >= 3 {
		return Classification{
			Type:       ContentMultiItem,
			Confidence: 0.85,
			Reason:     "detected 3+ distinct work items",
			ItemCount:  itemCount,
		}
	}

	// Score each type by signal density.
	researchScore := cc.score(inputLower, cc.ResearchSignals)
	emotionalScore := cc.score(inputLower, cc.EmotionalSignals)
	codeScore := cc.score(inputLower, cc.CodeSignals)

	// News/digest detection: starts with header-like patterns.
	// Check BEFORE research dominance since digests often contain research keywords.
	if cc.isNewsDigest(input) {
		return Classification{
			Type:       ContentNewsDigest,
			Confidence: 0.8,
			Reason:     "matches news/digest format",
		}
	}

	// If research signals dominate strongly, classify as research even on short inputs.
	if researchScore >= 2 && researchScore > codeScore && researchScore > emotionalScore {
		return Classification{
			Type:       ContentResearchDense,
			Confidence: clampConfidence(float64(researchScore) / 5.0),
			Reason:     "research signals dominate",
		}
	}

	// Long input with high research signals → research dense (stricter).
	if inputLen > 200 && researchScore >= 2 {
		return Classification{
			Type:       ContentResearchDense,
			Confidence: clampConfidence(float64(researchScore) / 5.0),
			Reason:     "long input with research keywords",
		}
	}

	// Emotional signals dominate → emotional vent.
	if emotionalScore >= 2 && emotionalScore > codeScore && emotionalScore > researchScore {
		return Classification{
			Type:       ContentEmotionalVent,
			Confidence: clampConfidence(float64(emotionalScore) / 4.0),
			Reason:     "emotional signals dominate",
		}
	}

	// Code signals dominate → code review.
	if codeScore >= 2 && codeScore > researchScore {
		return Classification{
			Type:       ContentCodeReview,
			Confidence: clampConfidence(float64(codeScore) / 5.0),
			Reason:     "code/engineering signals dominate",
		}
	}

	// Short, single-line, no strong signals → casual.
	if inputLen < 200 && researchScore+emotionalScore+codeScore == 0 {
		// Distinguish casual/greeting from short instructions by checking
		// for greeting/question words.
		if cc.isGreeting(inputLower) {
			return Classification{
				Type:       ContentCasualChat,
				Confidence: 0.8,
				Reason:     "greeting/smalltalk pattern",
			}
		}
		return Classification{
			Type:       ContentInstruction,
			Confidence: 0.6,
			Reason:     "short instruction, no strong signals",
		}
	}

	// Default: single instruction.
	return Classification{
		Type:       ContentInstruction,
		Confidence: 0.6,
		Reason:     "default — single instruction",
	}
}

// score counts how many signal words appear in the text.
func (cc *ContentClassifier) score(lower string, signals []string) int {
	count := 0
	for _, s := range signals {
		if strings.Contains(lower, s) {
			count++
		}
	}
	return count
}

// countItems estimates the number of discrete work items in a multi-item dump.
func (cc *ContentClassifier) countItems(input string) int {
	// Normalize newlines for multi-item separator detection.
	normalized := strings.ReplaceAll(input, "\r\n", "\n")

	// Also check patterns at the start of the string by adding a leading newline.
	prefixed := "\n" + normalized

	bestCount := 0
	for _, sep := range cc.MultiItemSeparators {
		parts := strings.Split(prefixed, sep)
		// Only count when the separator actually split the input.
		if len(parts) < 2 {
			continue
		}
		count := 0
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if len(p) >= cc.MinItemLength {
				count++
			}
		}
		if count > bestCount {
			bestCount = count
		}
		if bestCount >= 3 {
			return bestCount
		}
	}

	return bestCount
}

// isNewsDigest checks if the input matches a news digest format
// (starts with emoji header, contains bulleted links, etc.)
func (cc *ContentClassifier) isNewsDigest(input string) bool {
	trimmed := strings.TrimSpace(input)

	// Emoji header like "📊 **Research Digest**" or "🤖 **AI Digest**"
	emojiStart := false
	for _, r := range trimmed {
		if unicode.Is(unicode.So, r) || unicode.Is(unicode.Sm, r) {
			emojiStart = true
			break
		}
		if !unicode.IsSpace(r) {
			break
		}
	}

	if !emojiStart {
		return false
	}

	// Contains digest-like patterns
	digestWords := []string{"digest", "briefing", "summary", "top", "papers", "roundup"}
	for _, w := range digestWords {
		if strings.Contains(strings.ToLower(trimmed), w) {
			return true
		}
	}

	return false
}

// isGreeting checks for casual greeting/smalltalk patterns.
func (cc *ContentClassifier) isGreeting(lower string) bool {
	greetings := []string{
		"hey", "hi", "hello", "yo", "sup", "howdy",
		"how are you", "what's up", "good morning", "good evening",
	}
	for _, g := range greetings {
		if strings.HasPrefix(lower, g) {
			// For short words like "hi", require word boundary after
			// to avoid matching "history", "hide", etc.
			if len(g) <= 3 {
				if len(lower) > len(g) && lower[len(g)] != ' ' && lower[len(g)] != ',' && lower[len(g)] != '.' && lower[len(g)] != '!' && lower[len(g)] != '?' {
					continue
				}
			}
			return true
		}
		if strings.Contains(lower, " "+g) {
			return true
		}
	}
	return false
}

// clampConfidence bounds a raw score to [0.0, 1.0].
func clampConfidence(raw float64) float64 {
	if raw < 0.0 {
		return 0.0
	}
	if raw > 1.0 {
		return 1.0
	}
	return raw
}
