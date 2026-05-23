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

func LoadSoul(path string) (*SoulFile, error) {
	data, err := os.ReadFile(path)
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

> This is who I am. Make this yours and delete this later.

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

## What I Know
[Auto-populated on boot]

## What I Can't Do
[Auto-populated on boot]
`
	return fmt.Sprintf(strings.TrimSpace(tmpl), agentName)
}
