package learning

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls/promptinject"
)

// SecretScanner contains immutable compiled patterns; sharing it avoids
// recompiling every regex while resanitizing a retained dataset.
var learningSecretScanner = security.NewSecretScanner()

const MaxLearningFileBytes = 4 * 1024 * 1024
const MaxLearningRecordBytes = 16 * 1024
const MaxLearningRecords = 512
const MaxDraftBytes = 64 * 1024

type EngineOptions struct {
	// KnownSecrets are resolved configured credentials. They are never serialized.
	KnownSecrets []string
	Workspace    string
	MaxRecords   int
	Retention    time.Duration
}

func normalizedEngineOptions(options EngineOptions) EngineOptions {
	options.KnownSecrets = append([]string(nil), options.KnownSecrets...)
	if options.MaxRecords <= 0 || options.MaxRecords > MaxLearningRecords {
		options.MaxRecords = MaxLearningRecords
	}
	if options.Retention <= 0 || options.Retention > 30*24*time.Hour {
		options.Retention = 30 * 24 * time.Hour
	}
	return options
}
func boundedLearningText(text string, limit int) string {
	if len(text) > limit {
		text = text[:limit]
	}
	return strings.ToValidUTF8(text, "�")
}
func sanitizedLearningText(text string, known []string, limit int) string {
	// Match configured values before truncation, so a truncation boundary does
	// not persist the prefix of a known credential. Whole-field replacement also
	// avoids retaining normalized/split secrets the scanner cannot redact exactly.
	for _, secret := range known {
		if secret != "" && strings.Contains(text, secret) {
			return "[redacted sensitive content]"
		}
	}
	text = boundedLearningText(text, limit)
	scan, err := learningSecretScanner.Scan(text)
	if err != nil || scan == nil || len(scan.Findings) > 0 || len(promptinject.Scan(text)) > 0 {
		return "[redacted unsafe content]"
	}
	return text
}
func (e *Engine) sanitizeRecord(record LearningRecord) LearningRecord {
	options := normalizedEngineOptions(e.options)
	sensitiveHash := false
	for _, secret := range options.KnownSecrets {
		if secret != "" && strings.Contains(record.TaskHash, secret) {
			sensitiveHash = true
		}
	}
	if raw, err := hex.DecodeString(record.TaskHash); sensitiveHash || err != nil || len(raw) != sha256.Size {
		record.TaskHash = fmt.Sprintf("%x", sha256.Sum256([]byte(record.TaskHash+record.FinalOutput)))
	}
	if raw, err := hex.DecodeString(record.WorkspaceHash); err != nil || len(raw) != sha256.Size {
		record.WorkspaceHash = ""
	}
	record.FinalOutput = sanitizedLearningText(record.FinalOutput, options.KnownSecrets, 2048)
	record.SessionID = sanitizedLearningText(record.SessionID, options.KnownSecrets, 128)
	record.ModelID = sanitizedLearningText(record.ModelID, options.KnownSecrets, 128)
	record.ClusterReason = sanitizedLearningText(record.ClusterReason, options.KnownSecrets, 512)
	if len(record.ToolExecutions) > 32 {
		record.ToolExecutions = record.ToolExecutions[:32]
	}
	// Copy slices before stripping fields so the live execution record is untouched.
	record.ToolExecutions = append([]ToolExecution(nil), record.ToolExecutions...)
	for i := range record.ToolExecutions {
		item := &record.ToolExecutions[i]
		item.ToolName = sanitizedLearningText(item.ToolName, options.KnownSecrets, 128)
		item.Args = ""
		item.Result = ""
		item.Error = sanitizedLearningText(item.Error, options.KnownSecrets, 256)
	}
	// Raw attempts/late guidance can contain secrets or attacker instructions.
	record.AttemptTrail = nil
	record.LateAddedSkills = nil
	record.WinningPath = nil
	return record
}
func privateLearningRoot(dir string) (*os.Root, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if err := root.Chmod(".", 0700); err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}
func learningRegularFile(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("learning: refusing non-regular store file")
	}
	return nil
}
func readLearningRecords(root *os.Root) ([]LearningRecord, error) {
	if err := learningRegularFile(root, "turns.jsonl"); err != nil {
		return nil, err
	}
	file, err := root.Open("turns.jsonl")
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	offset := max(int64(0), info.Size()-MaxLearningFileBytes)
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxLearningFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxLearningFileBytes {
		return nil, fmt.Errorf("learning: concurrent store size change")
	}
	if offset > 0 {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			data = data[newline+1:]
		} else {
			return nil, nil
		}
	}
	var records []LearningRecord
	// Walk backwards so retention reads only the newest bounded records without
	// allocating one slice per newline in an attacker-controlled legacy file.
	for len(data) > 0 && len(records) < MaxLearningRecords {
		if data[len(data)-1] == '\n' {
			data = data[:len(data)-1]
			continue
		}
		newline := bytes.LastIndexByte(data, '\n')
		line := data[newline+1:]
		if newline < 0 {
			data = nil
		} else {
			data = data[:newline]
		}
		if len(line) == 0 || len(line) > MaxLearningRecordBytes || !utf8.Valid(line) {
			continue
		}
		var record LearningRecord
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		records = append(records, record)
	}
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}

	return records, nil
}
func (e *Engine) readBoundedRecords(dir string) ([]LearningRecord, error) {
	root, err := privateLearningRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	records, err := readLearningRecords(root)
	if err != nil {
		return nil, err
	}
	records = e.retainedRecords(records)
	if scope := WorkspaceScope(e.options.Workspace); scope != "" {
		scoped := records[:0]
		for _, record := range records {
			if record.WorkspaceHash == scope {
				scoped = append(scoped, record)
			}
		}
		records = scoped
	}
	return records, nil
}
func (e *Engine) retainedRecords(records []LearningRecord) []LearningRecord {
	options := normalizedEngineOptions(e.options)
	cutoff := time.Now().UTC().Add(-options.Retention)
	out := make([]LearningRecord, 0, min(len(records), options.MaxRecords))
	for _, record := range records {
		if record.RecordedAt.Before(cutoff) {
			continue
		}
		out = append(out, e.sanitizeRecord(record))
		if len(out) > options.MaxRecords {
			out = out[1:]
		}
	}
	return out
}
func (e *Engine) recordBounded(ctx context.Context, record LearningRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	root, err := privateLearningRoot(e.recordDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := learningRegularFile(root, ".store.lock"); err != nil {
		return err
	}
	lock, err := openLearningLock(root, ".store.lock")
	if err != nil {
		return err
	}
	unlock, err := lockLearningFile(ctx, lock)
	if err != nil {
		lock.Close()
		return err
	}
	defer unlock()
	records, err := readLearningRecords(root)
	if err != nil {
		return err
	}
	record.RecordedAt = time.Now().UTC()
	if e.options.Workspace != "" {
		record.WorkspaceHash = WorkspaceScope(e.options.Workspace)
	}
	records = append(records, e.sanitizeRecord(record))
	records = e.retainedRecords(records)
	var data bytes.Buffer
	var encoded [][]byte
	total := 0
	for _, item := range records {
		raw, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("learning: invalid record")
		}
		if len(raw) > MaxLearningRecordBytes {
			return fmt.Errorf("learning: record exceeds byte budget")
		}
		encoded = append(encoded, raw)
		total += len(raw) + 1
		for total > MaxLearningFileBytes && len(encoded) > 0 {
			total -= len(encoded[0]) + 1
			encoded = encoded[1:]
		}
	}
	for _, raw := range encoded {
		data.Write(raw)
		data.WriteByte('\n')
	}
	if data.Len() > MaxLearningFileBytes {
		return fmt.Errorf("learning: store exceeds byte budget")
	}
	return atomicLearningWrite(root, "turns.jsonl", data.Bytes())
}
func atomicLearningWrite(root *os.Root, name string, data []byte) error {
	parent, err := root.OpenRoot(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer parent.Close()
	base := filepath.Base(name)
	if err := learningRegularFile(parent, base); err != nil {
		return err
	}
	temp := ".learning-" + rand.Text() + ".tmp"
	file, err := parent.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer parent.Remove(temp)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return parent.Rename(temp, base)
}

var safeSkillName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func reviewDraftContent(content string, known []string) []string {
	if len(content) == 0 || len(content) > MaxDraftBytes || !utf8.ValidString(content) {
		return []string{"invalid draft byte budget or encoding"}
	}
	for _, secret := range known {
		if secret != "" && strings.Contains(content, secret) {
			return []string{"configured credential detected"}
		}
	}
	scan, err := learningSecretScanner.Scan(content)
	if err != nil || scan == nil {
		return []string{"secret review unavailable"}
	}
	var findings []string
	if len(scan.Findings) > 0 {
		findings = append(findings, "potential credential detected")
	}
	if len(promptinject.Scan(content)) > 0 {
		findings = append(findings, "potential instruction injection detected")
	}
	return findings
}
func (e *Engine) applyContainedDraft(draft SkillDraft) error {
	if !safeSkillName.MatchString(draft.TargetSkillName) {
		return fmt.Errorf("learning: invalid skill name")
	}
	if findings := reviewDraftContent(draft.BodyOrPatch, e.options.KnownSecrets); len(findings) > 0 {
		return fmt.Errorf("learning: draft failed security review")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	root, err := privateLearningRoot(e.skillDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(draft.TargetSkillName, 0700); err != nil {
		return err
	}
	info, err := root.Lstat(draft.TargetSkillName)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("learning: invalid skill directory")
	}
	parent, err := root.OpenRoot(draft.TargetSkillName)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := learningRegularFile(parent, "SKILL.md"); err != nil {
		return err
	}
	old, err := readBoundedSkill(parent, "SKILL.md")
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(old) > MaxDraftBytes {
		return fmt.Errorf("learning: existing skill exceeds backup budget")
	}
	if draft.ChangeKind == ChangeCreate && exists {
		return fmt.Errorf("learning: skill already exists")
	}
	body := []byte(draft.BodyOrPatch)
	switch draft.ChangeKind {
	case ChangeCreate, ChangeReplace:
	case ChangeAppend:
		if !exists {
			return fmt.Errorf("learning: append requires existing skill")
		}
		body = append(append(old, '\n'), body...)
	default:
		return fmt.Errorf("learning: unsupported change kind")
	}
	if len(body) > MaxDraftBytes {
		return fmt.Errorf("learning: resulting skill exceeds byte budget")
	}
	if exists {
		backup, err := privateLearningRoot(e.backupDir)
		if err != nil {
			return err
		}
		defer backup.Close()
		name := filepath.Join(draft.TargetSkillName, fmt.Sprintf("%d-%s", time.Now().UnixNano(), rand.Text()))
		if err := backup.MkdirAll(name, 0700); err != nil {
			return err
		}
		if err := atomicLearningWrite(backup, filepath.Join(name, "SKILL.md"), old); err != nil {
			return err
		}
	}
	return atomicLearningWrite(parent, "SKILL.md", body)
}

func WorkspaceScope(workspace string) string {
	if workspace == "" {
		return ""
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = real
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(absolute)))
}
func newEvolutionID() string { return strings.ToLower(rand.Text()) }
func readBoundedSkill(root *os.Root, name string) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxDraftBytes {
		return nil, fmt.Errorf("learning: invalid skill file")
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxDraftBytes+1))
	if len(data) > MaxDraftBytes {
		return nil, fmt.Errorf("learning: oversized skill")
	}
	return data, err
}

// Concurrent O_CREATE opens through os.Root can report transient ENOENT on
// macOS even after another descriptor created the file. Exclusive creation plus
// bounded existing-file retries handles that race while retaining containment.
func openLearningLock(root *os.Root, name string) (*os.File, error) {
	var last error
	for attempt := 0; attempt < 8; attempt++ {
		if err := learningRegularFile(root, name); err != nil {
			return nil, err
		}
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if os.IsExist(err) || os.IsNotExist(err) {
			file, err = root.OpenFile(name, os.O_RDWR, 0600)
		}
		if err != nil {
			last = err
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		info, err := file.Stat()
		named, namedErr := root.Lstat(name)
		if err != nil || namedErr != nil || !info.Mode().IsRegular() || !named.Mode().IsRegular() || !os.SameFile(info, named) {
			file.Close()
			return nil, fmt.Errorf("learning: invalid lock file identity")
		}
		if err := file.Chmod(0600); err != nil {
			file.Close()
			return nil, err
		}
		return file, nil
	}
	return nil, last
}
