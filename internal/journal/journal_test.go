package journal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	times "time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

func TestFlightRecorder_RecordInput(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "sess-1")

	if err := r.RecordInput("hello world"); err != nil {
		t.Fatalf("RecordInput: %v", err)
	}

	entries := readJSONL(t, dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Type != EntryUserInput {
		t.Errorf("expected type %s, got %s", EntryUserInput, entries[0].Type)
	}
	if entries[0].Content != "hello world" {
		t.Errorf("expected content 'hello world', got %s", entries[0].Content)
	}
	if entries[0].SessionID != "sess-1" {
		t.Errorf("expected sessionID 'sess-1', got %s", entries[0].SessionID)
	}
	if entries[0].ID == "" {
		t.Error("expected non-empty ID")
	}
}

func TestFlightRecorder_RecordReply(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "sess-2")

	if err := r.RecordReply("I will help you with that."); err != nil {
		t.Fatalf("RecordReply: %v", err)
	}

	entries := readJSONL(t, dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Type != EntryAgentReply {
		t.Errorf("expected type %s, got %s", EntryAgentReply, entries[0].Type)
	}
	if entries[0].Content != "I will help you with that." {
		t.Errorf("unexpected content: %s", entries[0].Content)
	}
}

func TestFlightRecorder_RecordToolCall(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "sess-3")

	input := json.RawMessage(`{"command": "ls -la"}`)
	if err := r.RecordToolCall("shell", input); err != nil {
		t.Fatalf("RecordToolCall: %v", err)
	}

	entries := readJSONL(t, dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Type != EntryToolCall {
		t.Errorf("expected type %s, got %s", EntryToolCall, entries[0].Type)
	}
	if entries[0].Content != "shell" {
		t.Errorf("expected content 'shell', got %s", entries[0].Content)
	}
	var meta map[string]string
	if err := json.Unmarshal(entries[0].Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta["command"] != "ls -la" {
		t.Errorf("expected command 'ls -la', got %s", meta["command"])
	}
}

func TestFlightRecorder_RecordError(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "sess-4")

	if err := r.RecordError(os.ErrNotExist); err != nil {
		t.Fatalf("RecordError: %v", err)
	}

	entries := readJSONL(t, dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Type != EntryError {
		t.Errorf("expected type %s, got %s", EntryError, entries[0].Type)
	}
	if !strings.Contains(entries[0].Content, "file does not exist") {
		t.Errorf("expected error content to contain 'file does not exist', got %s", entries[0].Content)
	}
}

func TestFlightRecorder_ReadSession(t *testing.T) {
	dir := t.TempDir()
	r1 := NewFlightRecorder(dir, "sess-a")
	r2 := NewFlightRecorder(dir, "sess-b")

	if err := r1.RecordInput("hello from a"); err != nil {
		t.Fatalf("RecordInput: %v", err)
	}
	if err := r2.RecordInput("hello from b"); err != nil {
		t.Fatalf("RecordInput: %v", err)
	}
	if err := r1.RecordReply("reply to a"); err != nil {
		t.Fatalf("RecordReply: %v", err)
	}

	entriesA, err := r1.ReadSession("sess-a")
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if len(entriesA) != 2 {
		t.Fatalf("expected 2 entries for sess-a, got %d", len(entriesA))
	}
	for _, e := range entriesA {
		if e.SessionID != "sess-a" {
			t.Errorf("expected sessionID sess-a, got %s", e.SessionID)
		}
	}

	entriesB, err := r2.ReadSession("sess-b")
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if len(entriesB) != 1 {
		t.Fatalf("expected 1 entry for sess-b, got %d", len(entriesB))
	}
}

func TestFlightRecorder_ReadDay(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "sess-day")

	if err := r.RecordInput("today entry"); err != nil {
		t.Fatalf("RecordInput: %v", err)
	}

	now := times.Now()
	entries, err := r.ReadDay(now)
	if err != nil {
		t.Fatalf("ReadDay: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for today, got %d", len(entries))
	}

	old := times.Date(2020, 1, 1, 0, 0, 0, 0, times.UTC)
	entriesOld, err := r.ReadDay(old)
	if err != nil {
		t.Fatalf("ReadDay old: %v", err)
	}
	if len(entriesOld) != 0 {
		t.Fatalf("expected 0 entries for old date, got %d", len(entriesOld))
	}
}

func TestFlightRecorder_AppendOnly(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "sess-append")

	for i := 0; i < 5; i++ {
		if err := r.RecordInput("entry"); err != nil {
			t.Fatalf("RecordInput %d: %v", i, err)
		}
	}

	entries := readJSONL(t, dir)
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	seen := make(map[string]bool)
	for _, e := range entries {
		if seen[e.ID] {
			t.Errorf("duplicate ID: %s", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestAlertStore_Create(t *testing.T) {
	dir := t.TempDir()
	s := NewAlertStore(dir)

	if err := s.Create(AlertFrustration, "user seems annoyed", "sess-1"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	pending := s.Pending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending alert, got %d", len(pending))
	}
	if pending[0].Type != AlertFrustration {
		t.Errorf("expected type %s, got %s", AlertFrustration, pending[0].Type)
	}
	if pending[0].Message != "user seems annoyed" {
		t.Errorf("unexpected message: %s", pending[0].Message)
	}
	if pending[0].Acknowledged {
		t.Error("new alert should not be acknowledged")
	}
}

func TestAlertStore_Pending(t *testing.T) {
	dir := t.TempDir()
	s := NewAlertStore(dir)

	_ = s.Create(AlertCompactionSkip, "skip 1", "sess-1")
	_ = s.Create(AlertTaskDeferred, "deferred 1", "sess-2")
	_ = s.Create(AlertPatternDetected, "pattern 1", "sess-3")

	pending := s.Pending()
	if len(pending) != 3 {
		t.Fatalf("expected 3 pending, got %d", len(pending))
	}

	_ = s.Acknowledge(pending[0].ID)

	pending = s.Pending()
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending after acknowledge, got %d", len(pending))
	}
}

func TestAlertStore_Acknowledge(t *testing.T) {
	dir := t.TempDir()
	s := NewAlertStore(dir)

	_ = s.Create(AlertFrustration, "test", "sess-1")
	pending := s.Pending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}

	if err := s.Acknowledge(pending[0].ID); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	pending = s.Pending()
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending after acknowledge, got %d", len(pending))
	}

	err := s.Acknowledge("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent alert ID")
	}
}

func TestAlertStore_DismissAll(t *testing.T) {
	dir := t.TempDir()
	s := NewAlertStore(dir)

	_ = s.Create(AlertCompactionSkip, "a", "s1")
	_ = s.Create(AlertFrustration, "b", "s2")
	_ = s.Create(AlertPatternDetected, "c", "s3")

	if err := s.DismissAll(); err != nil {
		t.Fatalf("DismissAll: %v", err)
	}

	pending := s.Pending()
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending after dismiss all, got %d", len(pending))
	}
}

func TestAlertStore_Persistence(t *testing.T) {
	dir := t.TempDir()

	s1 := NewAlertStore(dir)
	_ = s1.Create(AlertFrustration, "persist me", "sess-1")
	_ = s1.Create(AlertCompactionSkip, "skip me", "sess-2")
	_ = s1.Save()

	s2 := NewAlertStore(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	pending := s2.Pending()
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending after load, got %d", len(pending))
	}

	found := map[string]bool{}
	for _, a := range pending {
		found[a.Message] = true
	}
	if !found["persist me"] || !found["skip me"] {
		t.Errorf("expected both alerts to be loaded, got %v", found)
	}
}

func TestSummarizer_Summarize(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "sess-sum")

	_ = r.RecordInput("build the auth feature")
	_ = r.RecordReply("I'll implement JWT auth for you")

	mock := providers.NewMockProvider("mock", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{
			Content: "4/29: Built JWT auth. User asked for the feature, agent implemented it.",
		}, nil
	})

	s := NewSummarizer(r, mock, "test-model")
	summary, err := s.Summarize(context.Background(), "sess-sum")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if !strings.Contains(summary, "JWT auth") {
		t.Errorf("expected summary to mention auth, got: %s", summary)
	}
}

func TestSummarizer_WriteSummary(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "sess-write")

	mock := providers.NewMockProvider("mock", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{Content: "summary"}, nil
	})

	s := NewSummarizer(r, mock, "test-model")

	if err := s.WriteSummary(dir, "sess-write", "# Session Summary\n\nWorked on tests."); err != nil {
		t.Fatalf("WriteSummary: %v", err)
	}

	entriesDir := filepath.Join(dir, "entries")
	files, err := os.ReadDir(entriesDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	name := files[0].Name()
	if !strings.Contains(name, "sess-write") {
		t.Errorf("expected filename to contain session ID, got %s", name)
	}
	if !strings.HasSuffix(name, ".md") {
		t.Errorf("expected .md extension, got %s", name)
	}

	data, err := os.ReadFile(filepath.Join(entriesDir, name))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "Session Summary") {
		t.Errorf("expected file to contain 'Session Summary', got: %s", string(data))
	}
}

func TestSummarizer_ContextCancelled(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "sess-ctx")

	_ = r.RecordInput("do something")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock := providers.NewMockProvider("mock", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{}, ctx.Err()
	})

	s := NewSummarizer(r, mock, "test-model")

	_, err := s.Summarize(ctx, "sess-ctx")
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func readJSONL(t *testing.T, dir string) []Entry {
	t.Helper()

	rawDir := filepath.Join(dir, "raw")
	entries, err := os.ReadDir(rawDir)
	if err != nil {
		t.Fatalf("reading raw dir: %v", err)
	}

	var result []Entry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(rawDir, e.Name()))
		if err != nil {
			t.Fatalf("reading jsonl: %v", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var entry Entry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("unmarshaling entry: %v", err)
			}
			result = append(result, entry)
		}
	}
	return result
}
