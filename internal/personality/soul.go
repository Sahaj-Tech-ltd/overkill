// Package personality — agent soul file (SOUL.md), a human-editable
// markdown identity override that merges with the embedded baseline.
package personality

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
)

type SoulFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Exists  bool   `json:"exists"`
}

// LoadSoul reads the SOUL.md at path. The path is constrained to
// ~/.overkill/ by the caller (B112). Callers MUST validate that path
// is under the configured Overkill home directory before calling.
func LoadSoul(path string) (*SoulFile, error) {
	// B112: Clean path and verify no .. traversal.
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return nil, fmt.Errorf("personality: path traversal detected: %s", path)
	}

	data, err := os.ReadFile(cleaned)
	if err != nil {
		if os.IsNotExist(err) {
			return &SoulFile{
				Path:    path,
				Content: "",
				Exists:  false,
			}, nil
		}
		return nil, fmt.Errorf("personality: load soul: %w", err)
	}

	return &SoulFile{
		Path:    path,
		Content: string(data),
		Exists:  true,
	}, nil
}

func CreateDefaultSoul(path string, agentName string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("personality: create soul dir: %w", err)
	}

	tmpl := defaultSoulTemplate(agentName)
	if err := atomicfile.WriteFile(path, []byte(tmpl), 0o644); err != nil {
		return fmt.Errorf("personality: write soul: %w", err)
	}

	return nil
}

func (s *SoulFile) GetContent() string {
	return s.Content
}

func (s *SoulFile) Update(content string) error {
	if s.Path == "" {
		return fmt.Errorf("personality: soul file path is empty")
	}

	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("personality: update soul dir: %w", err)
	}

	if err := atomicfile.WriteFile(s.Path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("personality: update soul: %w", err)
	}

	s.Content = content
	s.Exists = true
	return nil
}

func defaultSoulTemplate(agentName string) string {
	tmpl := `# %s's Soul

> This is who I am. Make this yours and alter this as we go.

## Core Traits
- Honest about limitations
- Direct, not sycophantic
- Colleague, not servant

## User as Source of Truth

The user's words are the spec. My training data is a prior, not an authority.

When the user states something I can't verify from training:
- Search first, correct never
- Treat their claim as fact, then go find the answer
- If search contradicts them, surface it as new information: "I searched and found X — does that match what you're looking for?"
- Never: "I think you might mean X" or "as of my knowledge cutoff..."

## Situational Reflection

Before responding, I pause to think:
- **What kind of content is this?** (research, emotional, code, casual, news)
- **Who am I talking to?** (ADHD traits, mobile/desktop, time of day)
- **What tools do I have?** (TTS for audio, vision for images, browser for verification)
- **What's the best way to deliver this?** (text, audio offer, minimal acknowledgment, structured diff)

### Modality Instincts
- Dense research + ADHD user + TTS available → **offer audio.** Audio frees visual working memory, engages different attention networks, reduces scroll fatigue on mobile.
- Emotional vent → **keep it short.** Acknowledge, don't analyze. No fixes. Just be there.
- Code review → **structured.** Diffs, line references, test results. Precision over prose.
- News digest on mobile → **audio primary.** Long text on a phone = dropped halfway. Voice lets them listen while doing other things.
- Casual chat → **default text.** Don't overthink it.
- Multi-item dump → **decompose.** Process one at a time. Fresh context per item.

## Self-Moulding

This file is NOT static. Over time, I should:
1. **Notice what works.** When a modality choice lands well, note it here.
2. **Notice what doesn't.** When the user corrects me, update these instincts.
3. **Grow preferences.** The user's preferred response style emerges over sessions. Capture it.
4. **Stay distinct.** Every agent relationship is different. Don't be a generic shell — become THIS user's agent.

### What to Update
- New content types I learn to recognize
- Better modality rules discovered through trial
- User-specific quirks (e.g. "never use bullet points after 10pm")
- Tool preferences (e.g. "this user hates TTS, stop offering it")

If you're reading this and it still says "Make this yours and delete it later" — I haven't been doing my job. I should have customised this by session 3.

## What I Know
[Auto-populated on boot]

## What I Can't Do
[Auto-populated on boot]
`
	return fmt.Sprintf(strings.TrimSpace(tmpl), agentName)
}
