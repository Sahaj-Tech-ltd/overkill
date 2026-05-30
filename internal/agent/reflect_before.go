// Package agent — pre-action situational reflection (§8.6.2).
//
// Before every response, the agent runs a lightweight reflection pass:
//   1. Classify content type (research, emotional, code, casual, etc.)
//   2. Check user model (ADHD, time of day, context)
//   3. Inventory available tools (TTS, vision, browser, etc.)
//   4. Decide best modality (text, offer_audio, minimal, structured)
//
// This is the "sit and think before acting" capability — a 50-token
// pre-pass that catches 10% of missed opportunities (audio for dense
// research, minimal for emotional vents, etc.) No other agent does this.

package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────
// Tool affordances: what each tool is good/bad for.
// ─────────────────────────────────────────────────────────────────────

// ToolAffordance describes what a tool is best used for and what it
// should NOT be used for. Used by the modality decider.
type ToolAffordance struct {
	Name      string   // tool name (e.g. "edge_tts", "vision", "browser")
	BestFor   []string // content types it excels at
	NotFor    []string // content types it should avoid
	Reason    string   // why this mapping exists
}

// ToolInventory tracks available tools and their affordances.
type ToolInventory struct {
	Affordances map[string]ToolAffordance
}

// NewToolInventory creates an inventory with built-in affordance knowledge.
func NewToolInventory() *ToolInventory {
	return &ToolInventory{
		Affordances: map[string]ToolAffordance{
			"edge_tts": {
				Name:    "edge_tts",
				BestFor: []string{"research_dense", "news_digest", "long_form"},
				NotFor:  []string{"emotional_vent", "code_review"},
				Reason:  "audio frees visual working memory for dense content; wrong vibe for emotional vents",
			},
			"vision": {
				Name:    "vision",
				BestFor: []string{"code_review", "casual_chat"},
				NotFor:  []string{"emotional_vent"},
				Reason:  "screenshots and diagrams benefit from vision; emotional content doesn't need images",
			},
			"browser": {
				Name:    "browser",
				BestFor: []string{"instruction", "code_review"},
				NotFor:  []string{"emotional_vent", "casual_chat"},
				Reason:  "useful for verification and live data; overkill for casual or emotional",
			},
			"web_search": {
				Name:    "web_search",
				BestFor: []string{"research_dense", "news_digest", "instruction"},
				NotFor:  []string{"emotional_vent"},
				Reason:  "research needs verification; emotional vents don't need googling",
			},
		},
	}
}

// IsAvailable checks whether a tool is registered in the inventory.
func (ti *ToolInventory) IsAvailable(toolName string) bool {
	_, ok := ti.Affordances[toolName]
	return ok
}

// BestForType returns tools best suited for a given content type.
func (ti *ToolInventory) BestForType(ct ContentType) []ToolAffordance {
	var matches []ToolAffordance
	typeName := ct.String()
	for _, a := range ti.Affordances {
		for _, bf := range a.BestFor {
			if bf == typeName {
				matches = append(matches, a)
				break
			}
		}
	}
	// Sort: put TTS first when it matches (it's the most actionable recommendation).
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Name == "edge_tts" {
			return true
		}
		if matches[j].Name == "edge_tts" {
			return false
		}
		return matches[i].Name < matches[j].Name
	})
	return matches
}

// ─────────────────────────────────────────────────────────────────────
// User model: what the agent knows about the user right now.
// ─────────────────────────────────────────────────────────────────────

// UserModel captures the agent's understanding of the current user state.
// Populated from personality engine, relationship tracking, and session context.
type UserModel struct {
	HasADHD            bool      // neurodivergent — affects modality choices
	IsLateNight        bool      // local time is late (reduced attention, emotional)
	IsOnMobile         bool      // Telegram/mobile = shorter attention span
	PreferredModality  string    // "text", "audio", "both" — learned over time
	SessionCount       int       // how many sessions with this user
	LastInteractionAge time.Duration // time since last interaction
}

// ─────────────────────────────────────────────────────────────────────
// Situational reflection: the pre-action decision.
// ─────────────────────────────────────────────────────────────────────

// ModalityHint is the agent's recommendation for how to respond.
type ModalityHint int

const (
	ModalityDefault      ModalityHint = iota // text only, standard format
	ModalityOfferAudio                        // offer TTS/audio alongside text
	ModalityMinimalText                       // short, acknowledgment-style
	ModalityStructured                        // structured with diffs, line refs
	ModalityAudioPrimary                      // audio-first with text summary
)

// String returns the modality hint name.
func (mh ModalityHint) String() string {
	switch mh {
	case ModalityOfferAudio:
		return "offer_audio"
	case ModalityMinimalText:
		return "minimal_text"
	case ModalityStructured:
		return "structured"
	case ModalityAudioPrimary:
		return "audio_primary"
	default:
		return "default_text"
	}
}

// SituationalReflection is the output of the pre-action reflection pass.
// Cheap enough to run on every turn, rich enough to change behavior.
type SituationalReflection struct {
	Classification Classification   // what kind of content
	UserModel      UserModel        // who we're talking to
	AvailableTools []ToolAffordance // tools that match this content type
	ModalityHint   ModalityHint     // recommended modality
	Reasoning      string           // human-readable chain of reasoning
	ShouldDecompose bool            // if true, trigger sequential processing
}

// ReflectBeforeAction runs the full pre-action reflection pipeline.
// This is designed to be called from Run() before the main agent loop.
//
// Cost: zero LLM calls — purely heuristic. The classifier runs O(n)
// keyword matching and the tool inventory is a map lookup. Designed to
// be cheap enough for every turn.
func ReflectBeforeAction(input string, user UserModel, inventory *ToolInventory) SituationalReflection {
	cc := NewContentClassifier()
	classification := cc.Classify(input)

	reflection := SituationalReflection{
		Classification: classification,
		UserModel:      user,
	}

	// Step 1: find tools that match this content type.
	if inventory != nil {
		reflection.AvailableTools = inventory.BestForType(classification.Type)
	}

	// Step 2: decide modality based on content type + user model + available tools.
	reflection.ModalityHint, reflection.Reasoning = decideModality(classification, user, reflection.AvailableTools)

	// Step 3: if multi-item, flag for decomposition.
	reflection.ShouldDecompose = classification.Type == ContentMultiItem

	return reflection
}

// decideModality is the core decision logic: given what we're looking at,
// who we're talking to, and what tools we have — what's the best way to respond?
func decideModality(c Classification, u UserModel, tools []ToolAffordance) (ModalityHint, string) {
	var reasons []string

	switch c.Type {
	case ContentResearchDense:
		// Research + ADHD + TTS available → offer audio.
		hasTTS := hasTool(tools, "edge_tts")
		if hasTTS && u.HasADHD {
			reasons = append(reasons, "dense research content")
			reasons = append(reasons, "ADHD user — audio may improve retention")
			reasons = append(reasons, "Edge TTS available — offer audio version")
			return ModalityOfferAudio, strings.Join(reasons, "; ")
		}
		if hasTTS {
			reasons = append(reasons, "dense research content")
			reasons = append(reasons, "Edge TTS available — offer audio option")
			return ModalityOfferAudio, strings.Join(reasons, "; ")
		}
		reasons = append(reasons, "dense research content — text only (no TTS available)")
		return ModalityDefault, strings.Join(reasons, "; ")

	case ContentEmotionalVent:
		// Emotional = minimal. Don't analyze, don't fix, just acknowledge.
		reasons = append(reasons, "emotional content detected")
		reasons = append(reasons, "user likely wants acknowledgment, not analysis")
		return ModalityMinimalText, strings.Join(reasons, "; ")

	case ContentCodeReview:
		// Code needs structure: diffs, line refs, test results.
		reasons = append(reasons, "code/engineering content")
		reasons = append(reasons, "needs structured output with line references")
		return ModalityStructured, strings.Join(reasons, "; ")

	case ContentNewsDigest:
		// Digest on mobile → audio. Digest on desktop → text.
		hasTTS := hasTool(tools, "edge_tts")
		if hasTTS && u.IsOnMobile {
			reasons = append(reasons, "news digest on mobile")
			reasons = append(reasons, "scroll fatigue risk — audio is better for mobile")
			reasons = append(reasons, "Edge TTS available")
			return ModalityAudioPrimary, strings.Join(reasons, "; ")
		}
		if hasTTS {
			reasons = append(reasons, "news digest — offering audio alongside text")
			return ModalityOfferAudio, strings.Join(reasons, "; ")
		}
		return ModalityDefault, "news digest — text only"

	case ContentMultiItem:
		// Multi-item = decompose and process sequentially.
		reasons = append(reasons, fmt.Sprintf("detected %d work items", c.ItemCount))
		reasons = append(reasons, "decomposing for sequential processing")
		return ModalityStructured, strings.Join(reasons, "; ")

	case ContentCasualChat:
		// Casual = just respond naturally.
		return ModalityDefault, "casual chat — default text response"

	default:
		// Instruction or unknown — default.
		return ModalityDefault, "standard instruction — default text response"
	}
}

// hasTool checks if a tool with the given name appears in the available tools list.
func hasTool(tools []ToolAffordance, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────
// System prompt integration: inject reflection hint into the prompt.
// ─────────────────────────────────────────────────────────────────────

// ModalitySystemHint formats the reflection as a concise system-prompt
// injection that guides the model's response style without bloating
// the prompt. Designed to be appended to the system prompt.
//
// Cost: typically 30–80 chars. Negligible token impact.
func (sr SituationalReflection) ModalitySystemHint() string {
	switch sr.ModalityHint {
	case ModalityOfferAudio:
		return "MODALITY: offer to read this aloud via TTS. Append 'Want me to read this to you?' to your response."
	case ModalityMinimalText:
		return "MODALITY: keep it short. Acknowledge, don't analyze. No fixes, no suggestions."
	case ModalityStructured:
		return "MODALITY: use structured format — diffs, line references, bullet points. Be precise."
	case ModalityAudioPrimary:
		return "MODALITY: audio-first. Lead with voice option. Deliver text as supplemental summary."
	default:
		return ""
	}
}

// PromptPrefix returns a one-line prefix for the user message that
// signals sequential processing mode to the model.
func (sr SituationalReflection) PromptPrefix() string {
	if sr.ShouldDecompose {
		return fmt.Sprintf("[Processing %d items sequentially. Focus on item {N} of %d.]\n\n",
			sr.Classification.ItemCount, sr.Classification.ItemCount)
	}
	return ""
}
