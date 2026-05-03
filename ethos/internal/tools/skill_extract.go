// Package tools — skill_extract surfaces skills.Extract so the agent can
// auto-create a SKILL.md after solving a problem (master plan §6.2 Voyager).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Sahaj-Tech-ltd/ethos/internal/skills"
)

// SkillExtractTool writes a new SKILL.md under ~/.ethos/skills/<name>/.
type SkillExtractTool struct {
	outputDir string // typically ~/.ethos/skills
}

// NewSkillExtractTool wires the output directory. Pass empty to default to
// ~/.ethos/skills.
func NewSkillExtractTool(outputDir string) *SkillExtractTool {
	if outputDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			outputDir = filepath.Join(home, ".ethos", "skills")
		}
	}
	return &SkillExtractTool{outputDir: outputDir}
}

func (t *SkillExtractTool) Name() string { return "skill_extract" }

type skillExtractInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Triggers    []string `json:"triggers,omitempty"`
	Transcript  string   `json:"transcript,omitempty"`
}

func (t *SkillExtractTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.outputDir == "" {
		return errorJSON("skill_extract: output dir not configured"), nil
	}
	var req skillExtractInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("skill_extract: %w", err)
	}
	if req.Name == "" {
		return errorJSON("name is required"), nil
	}
	res, err := skills.Extract(skills.ExtractRequest{
		Name:        req.Name,
		Description: req.Description,
		Tags:        req.Tags,
		Triggers:    req.Triggers,
		Transcript:  req.Transcript,
		OutputDir:   t.outputDir,
	})
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(res)
	return out, nil
}
