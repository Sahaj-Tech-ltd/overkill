package skills

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"

	"github.com/Sahaj-Tech-ltd/overkill/internal/skills/safety"
)

// watchDebounce coalesces editor save bursts (e.g. write-then-rename) so we
// don't fire onChange multiple times for a single logical edit.
const watchDebounce = 500 * time.Millisecond

type Loader struct {
	bundledDir string
	userDir    string

	// scanner gates user-dir skills at load time (§6.4 safety
	// scanning). Bundled skills bypass — they ship with the binary
	// and are trusted by definition. nil scanner → noop (open).
	scanner safety.Scanner
	// blocked captures user-dir skills that were rejected by the
	// scanner. Exposed via BlockedSkills so the TUI can show
	// "we found <name> but VT flagged it; review or set
	// OVERKILL_SKILL_ALLOW_UNKNOWN=1 if you trust it".
	blockedMu sync.Mutex
	blocked   []BlockedSkill
}

// BlockedSkill is one skill that the safety scanner refused to
// install. Surface to the user via overkill skill block-list.
type BlockedSkill struct {
	Path    string
	Verdict safety.Verdict
	Reason  string
}

func NewLoader(bundledDir, userDir string) *Loader {
	return &Loader{
		bundledDir: bundledDir,
		userDir:    userDir,
	}
}

// WithScanner installs a safety scanner. The loader calls
// ScanDir on each user-dir skill before adding it to the registry;
// Malicious / Suspicious verdicts are blocked and recorded in
// BlockedSkills. Bundled skills are not scanned. Nil scanner →
// today's open behaviour.
func (l *Loader) WithScanner(s safety.Scanner) *Loader {
	l.scanner = s
	return l
}

// BlockedSkills returns the list of skills rejected by the
// scanner since the loader was constructed. Caller-side filtering
// (per-session, per-day) is the consumer's responsibility — this
// is a flat append log.
func (l *Loader) BlockedSkills() []BlockedSkill {
	l.blockedMu.Lock()
	defer l.blockedMu.Unlock()
	return append([]BlockedSkill(nil), l.blocked...)
}

// recordBlock appends to the blocked list under mutex. Best-effort
// — never errors back to the caller.
func (l *Loader) recordBlock(b BlockedSkill) {
	l.blockedMu.Lock()
	l.blocked = append(l.blocked, b)
	l.blockedMu.Unlock()
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
		// Safety gate: scan each user-dir skill's containing
		// directory and drop the ones that come back Malicious /
		// Suspicious. Clean and Unknown pass through (Unknown is
		// the "new file, VT hasn't seen it" state — alarming on
		// that would block every brand-new skill the user wrote).
		if l.scanner != nil {
			user = l.filterUnsafe(user)
		}
		skills = append(skills, user...)
	}

	return skills, nil
}

// filterUnsafe walks the candidate skills and drops the ones whose
// directory (or single file, for top-level .md skills) the scanner
// flags Malicious or Suspicious. Dropped entries land in
// BlockedSkills. Errors are recorded as Unknown but don't drop
// the skill — the scanner's job is to surface concrete signal,
// not to fail-closed on operational glitches.
func (l *Loader) filterUnsafe(in []Skill) []Skill {
	out := in[:0]
	for _, s := range in {
		// Scope of scan: the containing directory for a SKILL.md
		// (so we catch sibling scripts) or just the file itself
		// for a top-level <name>.md. Filepath helps us decide.
		target := s.FilePath
		if filepath.Base(target) == "SKILL.md" || filepath.Base(target) == "skill.md" {
			target = filepath.Dir(target)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		results, worst, err := safety.ScanDir(ctx, l.scanner, target)
		cancel()
		if err != nil {
			log.Printf("skills: safety scan error on %s: %v", target, err)
			// Don't drop — proceed and let the user decide. We
			// recorded the error.
		}
		switch worst {
		case safety.VerdictMalicious, safety.VerdictSuspicious:
			reason := summariseScan(results, worst)
			l.recordBlock(BlockedSkill{Path: target, Verdict: worst, Reason: reason})
			log.Printf("skills: BLOCKED %s — %s: %s", s.Name, worst, reason)
			continue
		default:
			out = append(out, s)
		}
	}
	return out
}

// summariseScan produces a one-line reason for a blocked skill,
// listing up to three engine detections from the worst result.
func summariseScan(results []safety.Result, worst safety.Verdict) string {
	for _, r := range results {
		if r.Verdict != worst {
			continue
		}
		if len(r.Detections) == 0 {
			return r.Reason
		}
		labels := []string{}
		for engine, label := range r.Detections {
			labels = append(labels, fmt.Sprintf("%s:%s", engine, label))
			if len(labels) >= 3 {
				break
			}
		}
		return fmt.Sprintf("%s (%s)", r.Reason, strings.Join(labels, ", "))
	}
	return string(worst)
}

func (l *Loader) LoadDir(dir string) ([]Skill, error) {
	var skills []Skill

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("skills: reading directory %q: %w", dir, err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			// Bundled-skill convention: skills/<name>/SKILL.md (mirrors
			// claude-code/openclaw/mattpocock layout). Look for SKILL.md or
			// skill.md inside each subdirectory; ignore subdirs that don't
			// have one.
			sub := filepath.Join(dir, name)
			for _, fname := range []string{"SKILL.md", "skill.md"} {
				path := filepath.Join(sub, fname)
				if _, err := os.Stat(path); err == nil {
					skill, lerr := l.LoadFile(path)
					if lerr != nil {
						return nil, fmt.Errorf("skills: loading %q: %w", path, lerr)
					}
					skills = append(skills, *skill)
					break
				}
			}
			continue
		}

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

// Watch monitors bundledDir and userDir for .md skill file changes and invokes
// onChange whenever a skill is created, modified, or deleted. On delete the
// callback receives a Skill with the inferred Name and Enabled=false so the
// caller can drop it from its registry. Per-path debouncing (~500ms) coalesces
// editor save bursts. The watcher stops cleanly when ctx.Done() fires.
//
// Errors during reload are logged via the standard log package and never
// block the watcher loop.
func (l *Loader) Watch(ctx interface{ Done() <-chan struct{} }, onChange func(skill Skill)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("skills: creating fsnotify watcher: %w", err)
	}

	// Track which directories actually got added so we don't error out when
	// only one of bundled/user exists. Recursively add subdirectories since
	// bundled-skill layout is skills/<name>/SKILL.md.
	addTree := func(root string) {
		if root == "" {
			return
		}
		if _, err := os.Stat(root); err != nil {
			return
		}
		_ = filepath.Walk(root, func(path string, info os.FileInfo, werr error) error {
			if werr != nil {
				return nil
			}
			if info.IsDir() {
				if aerr := w.Add(path); aerr != nil {
					log.Printf("skills: watch add %q: %v", path, aerr)
				}
			}
			return nil
		})
	}
	addTree(l.bundledDir)
	addTree(l.userDir)

	// Per-path debounce: each path gets its own timer; latest event wins.
	var (
		mu     sync.Mutex
		timers = make(map[string]*time.Timer)
	)

	// Remember last-known skill name per path so Remove events can emit a
	// disabled stub with the correct Name (the file is gone, we can't parse).
	var (
		nameMu sync.Mutex
		names  = make(map[string]string)
	)

	rememberName := func(path, name string) {
		nameMu.Lock()
		names[path] = name
		nameMu.Unlock()
	}
	forgetName := func(path string) string {
		nameMu.Lock()
		defer nameMu.Unlock()
		n := names[path]
		delete(names, path)
		return n
	}

	fire := func(path string, removed bool) {
		if removed {
			name := forgetName(path)
			if name == "" {
				// Fall back to filename stem so callers at least see a key.
				name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			}
			onChange(Skill{Name: name, FilePath: path, Enabled: false})
			return
		}
		skill, err := l.LoadFile(path)
		if err != nil {
			log.Printf("skills: watch reload %q: %v", path, err)
			return
		}
		// Bundled skills should keep their flag — heuristic: path under bundledDir.
		if l.bundledDir != "" {
			if rel, rerr := filepath.Rel(l.bundledDir, path); rerr == nil && !strings.HasPrefix(rel, "..") {
				skill.Bundled = true
			}
		}
		rememberName(path, skill.Name)
		onChange(*skill)
	}

	schedule := func(path string, removed bool) {
		mu.Lock()
		defer mu.Unlock()
		if t, ok := timers[path]; ok {
			t.Stop()
		}
		timers[path] = time.AfterFunc(watchDebounce, func() {
			fire(path, removed)
			mu.Lock()
			delete(timers, path)
			mu.Unlock()
		})
	}

	go func() {
		defer w.Close()
		for {
			select {
			case <-ctx.Done():
				mu.Lock()
				for _, t := range timers {
					t.Stop()
				}
				mu.Unlock()
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				// New directories (e.g. user creates skills/<name>/) need to be
				// added so we see SKILL.md drops inside them.
				if ev.Op&fsnotify.Create != 0 {
					if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
						if aerr := w.Add(ev.Name); aerr != nil {
							log.Printf("skills: watch add %q: %v", ev.Name, aerr)
						}
						continue
					}
				}
				if !isSkillFile(filepath.Base(ev.Name)) {
					continue
				}
				switch {
				case ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
					schedule(ev.Name, true)
				case ev.Op&(fsnotify.Create|fsnotify.Write) != 0:
					schedule(ev.Name, false)
				}
			case werr, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Printf("skills: watch error: %v", werr)
			}
		}
	}()

	return nil
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
