package skills

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Loader struct {
	bundledDir string
	userDir    string
}

func NewLoader(bundledDir, userDir string) *Loader {
	return &Loader{
		bundledDir: bundledDir,
		userDir:    userDir,
	}
}

func (l *Loader) LoadAll() ([]Skill, error) {
	var skills []Skill

	if l.bundledDir != "" {
		bundled, err := l.LoadDir(l.bundledDir)
		if err != nil {
			return nil, fmt.Errorf("skills: loading bundled: %w", err)
		}
		for i := range bundled {
			bundled[i].Bundled = true
		}
		skills = append(skills, bundled...)
	}

	if l.userDir != "" {
		user, err := l.LoadDir(l.userDir)
		if err != nil {
			return nil, fmt.Errorf("skills: loading user: %w", err)
		}
		skills = append(skills, user...)
	}

	return skills, nil
}

func (l *Loader) LoadDir(dir string) ([]Skill, error) {
	var skills []Skill

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("skills: reading directory %q: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !isSkillFile(name) {
			continue
		}

		path := filepath.Join(dir, name)
		skill, err := l.LoadFile(path)
		if err != nil {
			return nil, fmt.Errorf("skills: loading %q: %w", path, err)
		}
		skills = append(skills, *skill)
	}

	return skills, nil
}

func (l *Loader) LoadFile(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skills: reading file %q: %w", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("skills: stating file %q: %w", path, err)
	}

	skill, err := parseSkillMarkdown(data)
	if err != nil {
		return nil, err
	}

	skill.FilePath = path
	skill.CreatedAt = info.ModTime().UTC()
	skill.UpdatedAt = info.ModTime().UTC()

	return skill, nil
}

func (l *Loader) Watch(ctx interface{ Done() <-chan struct{} }, onChange func(skill Skill)) error {
	return fmt.Errorf("skills: watch not implemented")
}

var knownFields = map[string]bool{
	"name": true, "version": true, "description": true, "author": true,
	"category": true, "tags": true, "triggers": true, "enabled": true, "metadata": true,
}

func parseSkillMarkdown(data []byte) (*Skill, error) {
	content := string(data)

	if !strings.HasPrefix(content, "---") {
		return nil, fmt.Errorf("skills: missing frontmatter markers")
	}

	rest := content[3:]
	idx := bytes.Index([]byte(rest), []byte("\n---"))
	if idx < 0 {
		sepIdx := strings.Index(rest, "---")
		if sepIdx < 0 {
			return nil, fmt.Errorf("skills: missing closing frontmatter marker")
		}
		idx = sepIdx - 1
	}

	fmBytes := []byte(strings.TrimSpace(rest[:idx]))
	body := strings.TrimSpace(rest[idx+4:])

	var raw map[string]interface{}
	if err := yaml.Unmarshal(fmBytes, &raw); err != nil {
		return nil, fmt.Errorf("skills: parsing frontmatter: %w", err)
	}

	skill := &Skill{
		Name:         strVal(raw["name"]),
		Version:      strVal(raw["version"]),
		Description:  strVal(raw["description"]),
		Author:       strVal(raw["author"]),
		Category:     strVal(raw["category"]),
		Tags:         sliceVal(raw["tags"]),
		Triggers:     sliceVal(raw["triggers"]),
		Enabled:      boolVal(raw["enabled"]),
		Instructions: body,
		Metadata:     make(map[string]string),
	}

	if rawMd, ok := raw["metadata"]; ok {
		if md, ok := rawMd.(map[string]interface{}); ok {
			for k, v := range md {
				skill.Metadata[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	for k, v := range raw {
		if knownFields[k] {
			continue
		}
		skill.Metadata[k] = fmt.Sprintf("%v", v)
	}

	return skill, nil
}

func strVal(v interface{}) string {
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}

func boolVal(v interface{}) bool {
	if v == nil {
		return false
	}
	b, ok := v.(bool)
	if !ok {
		return false
	}
	return b
}

func sliceVal(v interface{}) []string {
	if v == nil {
		return nil
	}
	slice, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		result = append(result, fmt.Sprintf("%v", item))
	}
	return result
}

func isSkillFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	base := strings.ToLower(name)
	if ext == ".md" {
		return true
	}
	if strings.HasPrefix(base, "skill") && (ext == ".yml" || ext == ".yaml") {
		return true
	}
	return false
}
