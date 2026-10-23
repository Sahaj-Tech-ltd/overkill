package learning

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

const MaxStoredDrafts = 256

var draftIDPattern = regexp.MustCompile(`^[a-z2-7]{26}$`)

func (e *Engine) draftRoot() (*os.Root, error) {
	return privateLearningRoot(filepath.Join(e.recordDir, "drafts"))
}
func (e *Engine) persistDraft(ctx context.Context, draft SkillDraft) error {
	unlock, err := e.operationLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return e.saveDraftUnlocked(ctx, draft)
}
func (e *Engine) saveDraftUnlocked(ctx context.Context, draft SkillDraft) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !draftIDPattern.MatchString(draft.ID) || draft.WorkspaceHash == "" {
		return fmt.Errorf("learning: invalid draft identity")
	}
	root, err := e.draftRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(MaxStoredDrafts + 1)
	dir.Close()
	if err != nil && err != io.EOF {
		return err
	}
	if len(entries) >= MaxStoredDrafts {
		if _, err := root.Stat(draft.ID + ".json"); os.IsNotExist(err) {
			return fmt.Errorf("learning: draft retention capacity reached; review and remove old drafts")
		}
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		return fmt.Errorf("learning: invalid draft")
	}
	if len(raw) > MaxDraftBytes+8192 {
		return fmt.Errorf("learning: oversized draft metadata")
	}
	return atomicLearningWrite(root, draft.ID+".json", raw)
}
func (e *Engine) readDraft(id string) (SkillDraft, error) {
	if !draftIDPattern.MatchString(id) {
		return SkillDraft{}, fmt.Errorf("learning: invalid draft ID")
	}
	root, err := e.draftRoot()
	if err != nil {
		return SkillDraft{}, err
	}
	defer root.Close()
	if err := learningRegularFile(root, id+".json"); err != nil {
		return SkillDraft{}, err
	}
	file, err := root.Open(id + ".json")
	if err != nil {
		return SkillDraft{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, MaxDraftBytes+8193))
	if err != nil {
		return SkillDraft{}, err
	}
	if len(raw) > MaxDraftBytes+8192 {
		return SkillDraft{}, fmt.Errorf("learning: oversized draft")
	}
	var draft SkillDraft
	if json.Unmarshal(raw, &draft) != nil || draft.ID != id {
		return SkillDraft{}, fmt.Errorf("learning: invalid stored draft")
	}
	scope := WorkspaceScope(e.options.Workspace)
	if scope == "" || draft.WorkspaceHash != scope {
		return SkillDraft{}, fmt.Errorf("learning: draft belongs to a different workspace")
	}
	return draft, nil
}

// ListDrafts returns bounded, persisted proposals for this explicit workspace.
func (e *Engine) ListDrafts(ctx context.Context) ([]SkillDraft, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := e.draftRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(MaxStoredDrafts + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > MaxStoredDrafts {
		return nil, fmt.Errorf("learning: draft directory exceeds capacity")
	}
	var drafts []SkillDraft
	for _, entry := range entries {
		name := entry.Name()
		if len(name) < 6 {
			continue
		}
		id := name[:len(name)-5]
		if !draftIDPattern.MatchString(id) || name != id+".json" {
			continue
		}
		draft, err := e.readDraft(id)
		if err != nil {
			continue
		}
		drafts = append(drafts, draft)
	}
	sort.Slice(drafts, func(i, j int) bool { return drafts[i].GeneratedAt.Before(drafts[j].GeneratedAt) })
	return drafts, nil
}

// ReviewDraft is a trusted operator API, intentionally absent from model tools.
func (e *Engine) ReviewDraft(ctx context.Context, id string, approve bool) (SkillDraft, error) {
	unlock, err := e.operationLock(ctx)
	if err != nil {
		return SkillDraft{}, err
	}
	defer unlock()
	draft, err := e.readDraft(id)
	if err != nil {
		return SkillDraft{}, err
	}
	if draft.Status != DraftCandidate {
		return SkillDraft{}, fmt.Errorf("learning: only candidate drafts can be reviewed")
	}
	draft.ReviewedAt = time.Now().UTC()
	if approve {
		if !safeSkillName.MatchString(draft.TargetSkillName) || len(reviewDraftContent(draft.BodyOrPatch, e.options.KnownSecrets)) > 0 {
			return SkillDraft{}, fmt.Errorf("learning: draft failed security review")
		}
		if draft.ChangeKind != ChangeCreate && draft.ChangeKind != ChangeReplace && draft.ChangeKind != ChangeAppend {
			return SkillDraft{}, fmt.Errorf("learning: unsupported change kind")
		}
		old, exists, err := e.currentSkill(draft.TargetSkillName)
		if err != nil {
			return SkillDraft{}, err
		}
		if draft.ChangeKind == ChangeCreate && exists {
			return SkillDraft{}, fmt.Errorf("learning: skill already exists")
		}
		if draft.ChangeKind == ChangeAppend && !exists {
			return SkillDraft{}, fmt.Errorf("learning: append requires an existing skill")
		}
		draft.OriginalExists = exists
		draft.BaseHash = hashSkill(old, exists)
		draft.Status = DraftAccepted
	} else {
		draft.Status = DraftRejected
	}
	if err := e.saveDraftUnlocked(ctx, draft); err != nil {
		return SkillDraft{}, err
	}
	return draft, nil
}

// ApplyReviewedDraft durably records intent before mutation. Repeating it after
// a crash finalizes the same content, rather than appending a second time.
func (e *Engine) ApplyReviewedDraft(ctx context.Context, id string) (SkillDraft, error) {
	unlock, err := e.operationLock(ctx)
	if err != nil {
		return SkillDraft{}, err
	}
	defer unlock()
	draft, err := e.readDraft(id)
	if err != nil {
		return SkillDraft{}, err
	}
	if draft.Status == DraftApplied {
		return draft, nil
	}
	if draft.Status != DraftAccepted && draft.Status != DraftApplying {
		return SkillDraft{}, fmt.Errorf("learning: explicit approval required")
	}
	if !safeSkillName.MatchString(draft.TargetSkillName) || len(reviewDraftContent(draft.BodyOrPatch, e.options.KnownSecrets)) > 0 {
		return SkillDraft{}, fmt.Errorf("learning: draft failed security review")
	}
	current, exists, err := e.currentSkill(draft.TargetSkillName)
	if err != nil {
		return SkillDraft{}, err
	}
	var old []byte
	if draft.Status == DraftAccepted {
		if hashSkill(current, exists) != draft.BaseHash {
			return SkillDraft{}, fmt.Errorf("learning: skill changed since review")
		}
		old = current
		if exists {
			if err := e.backupReviewedSkill(draft.ID, old); err != nil {
				return SkillDraft{}, err
			}
		}
		draft.BackupID = draft.ID
	} else if draft.OriginalExists {
		old, err = e.readReviewedBackup(draft.BackupID)
		if err != nil {
			return SkillDraft{}, err
		}
		if hashSkill(old, true) != draft.BaseHash {
			return SkillDraft{}, fmt.Errorf("learning: backup integrity mismatch")
		}
	}
	body := []byte(draft.BodyOrPatch)
	switch draft.ChangeKind {
	case ChangeCreate, ChangeReplace:
	case ChangeAppend:
		body = append(append(append([]byte(nil), old...), '\n'), body...)
	default:
		return SkillDraft{}, fmt.Errorf("learning: unsupported change kind")
	}
	if len(body) > MaxDraftBytes {
		return SkillDraft{}, fmt.Errorf("learning: resulting skill exceeds byte budget")
	}
	desiredHash := hashSkill(body, true)
	if draft.Status == DraftApplying && hashSkill(current, exists) != draft.BaseHash && hashSkill(current, exists) != desiredHash {
		return SkillDraft{}, fmt.Errorf("learning: conflicting interrupted apply")
	}
	draft.AppliedHash = desiredHash
	draft.Status = DraftApplying
	if err := e.saveDraftUnlocked(ctx, draft); err != nil {
		return SkillDraft{}, err
	}
	if hashSkill(current, exists) != desiredHash {
		if err := e.writeReviewedSkill(draft.TargetSkillName, body, false); err != nil {
			return SkillDraft{}, err
		}
	}
	draft.Status = DraftApplied
	draft.AppliedAt = time.Now().UTC()
	if err := e.saveDraftUnlocked(ctx, draft); err != nil {
		return SkillDraft{}, fmt.Errorf("learning: apply is recoverable; status persistence failed")
	}
	return draft, nil
}

// RollbackDraft refuses to overwrite edits made after the generated skill was
// installed. Repeating an interrupted rollback finalizes the recorded base.
func (e *Engine) RollbackDraft(ctx context.Context, id string) (SkillDraft, error) {
	unlock, err := e.operationLock(ctx)
	if err != nil {
		return SkillDraft{}, err
	}
	defer unlock()
	draft, err := e.readDraft(id)
	if err != nil {
		return SkillDraft{}, err
	}
	if draft.Status == DraftRolledBack {
		return draft, nil
	}
	if draft.Status != DraftApplied && draft.Status != DraftRollingBack {
		return SkillDraft{}, fmt.Errorf("learning: draft has not been applied")
	}
	current, exists, err := e.currentSkill(draft.TargetSkillName)
	if err != nil {
		return SkillDraft{}, err
	}
	currentHash := hashSkill(current, exists)
	if currentHash != draft.AppliedHash && !(draft.Status == DraftRollingBack && currentHash == draft.BaseHash) {
		return SkillDraft{}, fmt.Errorf("learning: skill changed after apply; refusing rollback")
	}
	var old []byte
	if draft.OriginalExists {
		old, err = e.readReviewedBackup(draft.BackupID)
		if err != nil {
			return SkillDraft{}, err
		}
		if hashSkill(old, true) != draft.BaseHash {
			return SkillDraft{}, fmt.Errorf("learning: backup integrity mismatch")
		}
	}
	draft.Status = DraftRollingBack
	if err := e.saveDraftUnlocked(ctx, draft); err != nil {
		return SkillDraft{}, err
	}
	if currentHash != draft.BaseHash {
		if err := e.writeReviewedSkill(draft.TargetSkillName, old, !draft.OriginalExists); err != nil {
			return SkillDraft{}, err
		}
	}
	draft.Status = DraftRolledBack
	if err := e.saveDraftUnlocked(ctx, draft); err != nil {
		return SkillDraft{}, fmt.Errorf("learning: rollback is recoverable; status persistence failed")
	}
	return draft, nil
}
func hashSkill(body []byte, exists bool) string {
	if !exists {
		return "absent"
	}
	return fmt.Sprintf("%x", sha256.Sum256(body))
}
func (e *Engine) operationLock(ctx context.Context) (func(), error) {
	root, err := privateLearningRoot(e.recordDir)
	if err != nil {
		return nil, err
	}
	if err := learningRegularFile(root, ".operations.lock"); err != nil {
		root.Close()
		return nil, err
	}
	file, err := openLearningLock(root, ".operations.lock")
	root.Close()
	if err != nil {
		return nil, err
	}
	return lockLearningFile(ctx, file)
}
func (e *Engine) currentSkill(name string) ([]byte, bool, error) {
	if !safeSkillName.MatchString(name) {
		return nil, false, fmt.Errorf("learning: invalid skill name")
	}
	root, err := privateLearningRoot(e.skillDir)
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("learning: invalid skill directory")
	}
	parent, err := root.OpenRoot(name)
	if err != nil {
		return nil, false, err
	}
	defer parent.Close()
	if err := learningRegularFile(parent, "SKILL.md"); err != nil {
		return nil, false, err
	}
	body, err := readBoundedSkill(parent, "SKILL.md")
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return body, err == nil, err
}
func (e *Engine) writeReviewedSkill(name string, body []byte, remove bool) error {
	if !safeSkillName.MatchString(name) {
		return fmt.Errorf("learning: invalid skill name")
	}
	root, err := privateLearningRoot(e.skillDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(name, 0700); err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("learning: invalid skill directory")
	}
	parent, err := root.OpenRoot(name)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := learningRegularFile(parent, "SKILL.md"); err != nil {
		return err
	}
	if remove {
		err := parent.Remove("SKILL.md")
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return atomicLearningWrite(parent, "SKILL.md", body)
}
func (e *Engine) backupReviewedSkill(id string, body []byte) error {
	if !draftIDPattern.MatchString(id) {
		return fmt.Errorf("learning: invalid backup ID")
	}
	root, err := privateLearningRoot(e.backupDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(id, 0700); err != nil {
		return err
	}
	path := filepath.Join(id, "SKILL.md")
	if err := learningRegularFile(root, path); err != nil {
		return err
	}
	existing, err := readBoundedSkill(root, path)
	if err == nil {
		if hashSkill(existing, true) != hashSkill(body, true) {
			return fmt.Errorf("learning: immutable backup mismatch")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return atomicLearningWrite(root, path, body)
}
func (e *Engine) readReviewedBackup(id string) ([]byte, error) {
	if !draftIDPattern.MatchString(id) {
		return nil, fmt.Errorf("learning: invalid backup ID")
	}
	root, err := privateLearningRoot(e.backupDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	path := filepath.Join(id, "SKILL.md")
	if err := learningRegularFile(root, path); err != nil {
		return nil, err
	}
	return readBoundedSkill(root, path)
}
