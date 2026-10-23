package seahorse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/modelusage"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
)

type wireRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}
type providerFixture struct {
	mu        sync.Mutex
	requests  []wireRequest
	fail      bool
	oversized bool
	provider  *providers.OpenAIProvider
}

func newProviderFixture(t *testing.T) *providerFixture {
	t.Helper()
	f := &providerFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Method != "POST" {
			t.Errorf("unexpected wire request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		var request wireRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
			t.Errorf("wire JSON: %v", err)
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, request)
		number := len(f.requests)
		fail, oversized := f.fail, f.oversized
		f.mu.Unlock()
		if fail {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"error":{"message":"fixture unavailable"}}`)
			return
		}
		content := fmt.Sprintf("Observed constraint preserved; unresolved item %d remains.", number)
		if oversized {
			content = strings.Repeat("overflow ", 2000)
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "local-fixture", "model": "summary-fixture", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 40, "completion_tokens": 12}})
	}))
	t.Cleanup(server.Close)
	f.provider = providers.NewOpenAIProvider("local-fixture-only", nil)
	f.provider.BaseProvider = providers.NewBaseProvider("openai", server.URL, "local-fixture-only", []providers.Model{{ID: "summary-fixture", ContextWindow: 4096, MaxOutputTokens: 256}})
	return f
}
func (f *providerFixture) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.requests) }
func fixtureOptions() HistoryOptions {
	return HistoryOptions{FreshTailCount: 2, LeafTargetTokens: 128, CondensedTargetTokens: 128, MaxInputTokens: 350, SummaryModel: "summary-fixture", FanIn: 4}
}
func fixtureHistory() []providers.Message {
	var history []providers.Message
	for i := 0; i < 30; i++ {
		history = append(history, providers.Message{Role: "user", Content: fmt.Sprintf("Constraint %d: %s", i, strings.Repeat("preserve exact file state; ", 10))}, providers.Message{Role: "assistant", Content: fmt.Sprintf("Evidence %d: %s", i, strings.Repeat("observed test status; ", 10))})
	}
	history = append(history, providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call-one", Name: "fs_read", Arguments: `{"path":"one.go"}`}, {ID: "call-two", Name: "fs_read", Arguments: `{"path":"two.go"}`}}}, providers.Message{Role: "tool", ToolCallID: "call-two", Content: "second file result"}, providers.Message{Role: "tool", ToolCallID: "call-one", Content: "first file result"}, providers.Message{Role: "user", Content: "Current question remains actionable."})
	return history
}
func testCompactor(t *testing.T, store Store, session string, f *providerFixture, opts HistoryOptions) *HistoryCompactor {
	t.Helper()
	c, err := NewHistoryCompactor(f.provider, tokenizer.NewEstimator(), store, session, opts)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func expandRaw(t *testing.T, store Store, session, id string, depth int) []providers.Message {
	t.Helper()
	if depth > 20 {
		t.Fatal("unexpected DAG depth")
	}
	node, err := store.Node(context.Background(), session, id)
	if err != nil {
		t.Fatal(err)
	}
	var messages []providers.Message
	for _, parent := range node.Summary.ParentIDs {
		messages = append(messages, expandRaw(t, store, session, parent, depth+1)...)
	}
	for _, source := range node.SourceIDs {
		record, err := store.Source(context.Background(), session, source)
		if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, record.Message)
	}
	return messages
}
func TestDurableDAGRealProviderWireReloadAndSessionIsolation(t *testing.T) {
	ctx := context.Background()
	db, _ := testpg.Open(t)
	store, err := NewPostgresStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	f := newProviderFixture(t)
	history := fixtureHistory()
	original := digest(history)
	var reportedCalls atomic.Int32
	ctx = modelusage.WithAccounting(ctx, modelusage.Accounting{Record: func(_ context.Context, model string, usage providers.Usage, reported bool) error {
		if model != "summary-fixture" || !reported || usage.InputTokens != 40 || usage.OutputTokens != 12 {
			t.Errorf("incorrect actual model accounting: %s %#v %v", model, usage, reported)
		}
		reportedCalls.Add(1)
		return nil
	}})
	c := testCompactor(t, store, "session-a", f, fixtureOptions())
	output, summary, err := c.Compact(ctx, history, "parent-fixture", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if summary == "" || digest(history) != original {
		t.Fatal("empty summary or input mutated")
	}
	// PreserveLast=2 would split the parallel result batch. All four messages,
	// including the assistant call and both reversed-order results, remain raw.
	if !reflect.DeepEqual(output[1:], history[len(history)-4:]) {
		t.Fatal("tool batch/raw tail changed")
	}
	if err = validateToolGroups(output); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(ctx, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	head, err := store.Node(ctx, "session-a", state.HeadID)
	if err != nil {
		t.Fatal(err)
	}
	if head.Summary.Kind != KindCondensed || head.Summary.Depth < 2 {
		t.Fatalf("not a hierarchical DAG: %#v", head.Summary)
	}
	if !reflect.DeepEqual(expandRaw(t, store, "session-a", state.HeadID, 0), history[:len(history)-4]) {
		t.Fatal("DAG ancestry does not reconstruct every raw prefix message in order")
	}
	// Each protected raw tail message is durable too, including full tool data.
	for _, message := range history[len(history)-4:] {
		if _, err = store.Source(ctx, "session-a", sourceFor(message).ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.Node(ctx, "session-b", state.HeadID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-session node leaked: %v", err)
	}
	if _, err = store.Source(ctx, "session-b", sourceFor(history[0]).ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-session raw source leaked: %v", err)
	}
	empty, _ := store.Load(ctx, "session-b")
	if empty.Revision != 0 || empty.HeadID != "" {
		t.Fatal("cross-session head leaked")
	}
	// Recreate store/compactor after completion, replay the exact input (crash
	// between durable publication and Agent history replacement), no new LLM call.
	reloaded, err := NewPostgresStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	before := f.count()
	restarted := testCompactor(t, reloaded, "session-a", f, fixtureOptions())
	again, _, err := restarted.Compact(ctx, history, "parent-fixture", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, output) || f.count() != before {
		t.Fatal("restart/idempotent replay changed output or repeated provider requests")
	}
	// Extend the restored context; the next root must retain the previous root
	// and therefore every earlier source, rather than summarize-and-forget it.
	continuation := append(append([]providers.Message(nil), again...), fixtureHistory()[:10]...)
	next, _, err := restarted.Compact(ctx, continuation, "parent-fixture", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(next) < 2 {
		t.Fatal("missing continued raw tail")
	}
	newer, _ := reloaded.Load(ctx, "session-a")
	if newer.Revision != 2 {
		t.Fatalf("revision=%d", newer.Revision)
	}
	expected := append(append([]providers.Message(nil), history...), fixtureHistory()[:8]...)
	if !reflect.DeepEqual(expandRaw(t, reloaded, "session-a", newer.HeadID, 0), expected) {
		t.Fatal("continued DAG lost/duplicated source lineage")
	}
	f.mu.Lock()
	requests := append([]wireRequest(nil), f.requests...)
	f.mu.Unlock()
	if int(reportedCalls.Load()) != len(requests) {
		t.Fatal("model accounting omitted summary calls")
	}
	sawLeaf, sawCondensed := false, false
	for _, request := range requests {
		if request.Model != "summary-fixture" || request.MaxTokens <= 0 || request.MaxTokens > 256 || len(request.Messages) != 2 || request.Messages[0].Role != "system" {
			t.Fatalf("incorrect actual provider request: %#v", request)
		}
		text := ""
		for _, m := range request.Messages {
			text += m.Content
		}
		if tokenizer.NewEstimator().Estimate(text)+request.MaxTokens+16 > 4096 {
			t.Fatal("request exceeded declared model context budget")
		}
		sawLeaf = sawLeaf || strings.Contains(text, "these raw messages")
		sawCondensed = sawCondensed || strings.Contains(text, "child summaries")
	}
	if !sawLeaf || !sawCondensed {
		t.Fatal("actual provider wire did not exercise both leaf and hierarchical condensation")
	}
}

type failingCommitStore struct{ Store }

func (s failingCommitStore) Commit(context.Context, string, int64, []Source, []Node, Snapshot) error {
	return errors.New("fixture durable store unavailable")
}
func TestDAGFailuresPreserveOriginalHistoryAndHead(t *testing.T) {
	db, _ := testpg.Open(t)
	ctx := context.Background()
	store, err := NewPostgresStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"provider", "oversized", "store", "budget", "incomplete-tool", "orphan-tool", "before-accounting", "after-accounting", "cancelled", "input-bound"} {
		t.Run(mode, func(t *testing.T) {
			f := newProviderFixture(t)
			opts := fixtureOptions()
			var useStore Store = store
			history := fixtureHistory()
			budget := 4096
			callCtx := ctx
			switch mode {
			case "provider":
				f.fail = true
			case "oversized":
				f.oversized = true
			case "store":
				useStore = failingCommitStore{store}
			case "budget":
				budget = 100
			case "incomplete-tool":
				history = history[:len(history)-2]
			case "orphan-tool":
				history = append([]providers.Message{{Role: "tool", ToolCallID: "missing", Content: "orphan"}}, history...)
			case "before-accounting":
				callCtx = modelusage.WithAccounting(ctx, modelusage.Accounting{Before: func(context.Context) error { return errors.New("fixture budget denied") }})
			case "after-accounting":
				callCtx = modelusage.WithAccounting(ctx, modelusage.Accounting{Record: func(context.Context, string, providers.Usage, bool) error {
					return errors.New("fixture accounting denied")
				}})
			case "cancelled":
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel()
				callCtx = cancelCtx
			case "input-bound":
				opts.MaxInputBytes = 100
			}
			original := digest(history)
			c := testCompactor(t, useStore, mode, f, opts)
			output, summary, err := c.Compact(callCtx, history, "parent-fixture", budget)
			if err == nil || summary != "" || !reflect.DeepEqual(output, history) || digest(history) != original {
				t.Fatalf("failure did not retain exact history: mode=%s err=%v", mode, err)
			}
			state, e := store.Load(ctx, mode)
			if e != nil || state.Revision != 0 || state.HeadID != "" {
				t.Fatalf("failed operation published head: %#v %v", state, e)
			}
		})
	}
}

type barrierLoadStore struct {
	Store
	arrivals atomic.Int32
	ready    chan struct{}
}

func (s *barrierLoadStore) Load(ctx context.Context, session string) (Snapshot, error) {
	snapshot, err := s.Store.Load(ctx, session)
	if err != nil {
		return snapshot, err
	}
	number := s.arrivals.Add(1)
	if number == 2 {
		close(s.ready)
	}
	if number <= 2 {
		select {
		case <-s.ready:
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
	return snapshot, nil
}
func TestDAGConcurrentCommitCASAndExactReplay(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(fmt.Sprintf("same_input_%v", same), func(t *testing.T) {
			ctx := context.Background()
			db, _ := testpg.Open(t)
			base, err := NewPostgresStore(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			store := &barrierLoadStore{Store: base, ready: make(chan struct{})}
			f := newProviderFixture(t)
			first, second := fixtureHistory(), fixtureHistory()
			if !same {
				second[0].Content = "Distinct competing branch"
			}
			var wg sync.WaitGroup
			var results [2][]providers.Message
			var errs [2]error
			for i, messages := range [][]providers.Message{first, second} {
				wg.Add(1)
				go func(i int, messages []providers.Message) {
					defer wg.Done()
					c := testCompactor(t, store, "concurrent", f, fixtureOptions())
					results[i], _, errs[i] = c.Compact(ctx, messages, "parent", 4096)
				}(i, messages)
			}
			wg.Wait()
			state, err := base.Load(ctx, "concurrent")
			if err != nil || state.Revision != 1 {
				t.Fatalf("non-atomic head: %#v %v", state, err)
			}
			if same {
				if errs[0] != nil || errs[1] != nil || !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("exact concurrent replay failed: %v", errs)
				}
			} else {
				success := 0
				for i, err := range errs {
					if err == nil {
						success++
					} else {
						if !errors.Is(err, ErrConflict) {
							t.Fatalf("wrong conflict: %v", err)
						}
						expected := first
						if i == 1 {
							expected = second
						}
						if !reflect.DeepEqual(results[i], expected) {
							t.Fatal("losing branch history changed")
						}
					}
				}
				if success != 1 {
					t.Fatalf("successes=%d", success)
				}
			}
		})
	}
}
func TestDAGIntegrityAndAtomicRollback(t *testing.T) {
	ctx := context.Background()
	db, _ := testpg.Open(t)
	store, err := NewPostgresStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	f := newProviderFixture(t)
	c := testCompactor(t, store, "integrity", f, fixtureOptions())
	history := fixtureHistory()
	output, _, err := c.Compact(ctx, history, "parent", 4096)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load(ctx, "integrity")
	if _, err = db.Exec(`UPDATE seahorse_nodes SET payload=jsonb_set(payload,'{summary,content}','"tampered"') WHERE session_id=$1 AND node_id=$2`, "integrity", state.HeadID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Node(ctx, "integrity", state.HeadID); err == nil {
		t.Fatal("corrupt summary accepted")
	}
	replay, _, err := c.Compact(ctx, history, "parent", 4096)
	if err == nil || !reflect.DeepEqual(replay, history) {
		t.Fatal("corrupt persisted replay published")
	}
	forged := append([]providers.Message(nil), output...)
	forged[0].Content += "forged"
	if _, _, err = c.Compact(ctx, forged, "parent", 4096); err == nil {
		t.Fatal("forged marker accepted")
	}
	// Missing cross-session parent causes rollback after raw insert; no partial
	// source, node or session state may survive the rejected transaction.
	source := sourceFor(providers.Message{Role: "user", Content: "rollback raw source"})
	bad := sealNode(Node{Summary: Summary{Kind: KindCondensed, Depth: 1, Content: "bad", ParentIDs: []string{state.HeadID, state.HeadID}}})
	if err = store.Commit(ctx, "isolated", 0, []Source{source}, []Node{bad}, Snapshot{HeadID: bad.Hash}); err == nil {
		t.Fatal("cross-session ancestry committed")
	}
	if _, err = store.Source(ctx, "isolated", source.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source insert escaped transaction: %v", err)
	}
	empty, _ := store.Load(ctx, "isolated")
	if empty.Revision != 0 || empty.HeadID != "" {
		t.Fatal("rollback created published session")
	}
}

func TestDAGBoundedExpansionSearchAttachmentsAndDataset(t *testing.T) {
	ctx := context.Background()
	db, _ := testpg.Open(t)
	store, err := NewPostgresStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	f := newProviderFixture(t)
	c := testCompactor(t, store, "retrieval", f, fixtureOptions())
	history := fixtureHistory()
	history[0].Content = "Distinctive wombat source evidence"
	history[0].Attachments = []providers.Attachment{{Kind: providers.AttachmentImage, MediaType: "image/png", Data: []byte{0, 1, 2, 3, 254, 255}}}
	_, _, err = c.Compact(ctx, history, "parent", 4096)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load(ctx, "retrieval")
	expanded, err := Expand(ctx, store, "retrieval", state.HeadID, 4096, 4*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expanded.Messages, history[:len(history)-4]) {
		t.Fatal("public bounded expansion changed exact raw data")
	}
	if expansion, err := Expand(ctx, store, "retrieval", state.HeadID, 1, 4*1024*1024); err == nil || len(expansion.Messages) != 0 {
		t.Fatal("limited expansion returned misleading partial evidence")
	}
	if expansion, err := Expand(ctx, store, "retrieval", state.HeadID, 4096, 8); err == nil || len(expansion.Messages) != 0 {
		t.Fatal("byte limit returned misleading partial evidence")
	}
	hits, err := store.SearchSources(ctx, "retrieval", "wombat", 5)
	if err != nil || len(hits) != 1 || hits[0].SourceID != sourceFor(history[0]).ID || hits[0].Role != "user" {
		t.Fatalf("raw FTS retrieval: %#v %v", hits, err)
	}
	if hits, err = store.SearchSources(ctx, "another-session", "wombat", 5); err != nil || len(hits) != 0 {
		t.Fatal("cross-session search leak")
	}
	raw, err := store.Source(ctx, "retrieval", hitsID(history[0]))
	if err != nil || !reflect.DeepEqual(raw.Message.Attachments, history[0].Attachments) {
		t.Fatal("attachment bytes were not durable")
	}
	// Enforce dataset cap by transaction rollback, without evicting existing data.
	store.limits.sources = 1
	before, _ := store.Load(ctx, "bounded")
	original := digest(history)
	output, _, err := testCompactor(t, store, "bounded", f, fixtureOptions()).Compact(ctx, history, "parent", 4096)
	if err == nil || digest(history) != original || !reflect.DeepEqual(output, history) {
		t.Fatal("dataset cap did not preserve input")
	}
	after, _ := store.Load(ctx, "bounded")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("dataset cap published partial state")
	}
	if _, err = store.Source(ctx, "bounded", sourceFor(history[0]).ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("dataset cap left inserted raw data")
	}
}
func hitsID(message providers.Message) string { return sourceFor(message).ID }
func TestDAGConcurrentSchemaInitialization(t *testing.T) {
	db, _ := testpg.Open(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := NewPostgresStore(context.Background(), db); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent schema init: %v", err)
		}
	}
}

func TestDAGForgedMarkersAndRawSourceTamperingFailClosed(t *testing.T) {
	ctx := context.Background()
	db, _ := testpg.Open(t)
	store, err := NewPostgresStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	f := newProviderFixture(t)
	c := testCompactor(t, store, "tamper", f, fixtureOptions())
	history := fixtureHistory()
	output, _, err := c.Compact(ctx, history, "parent", 4096)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load(ctx, "tamper")
	forged := append(append([]providers.Message(nil), output...), providers.Message{Role: "user", Content: "next step"})
	forged[0].Content += " altered marker"
	if after, _, err := c.Compact(ctx, forged, "parent", 4096); err == nil || !reflect.DeepEqual(after, forged) {
		t.Fatal("forged marker changed history")
	}
	// Raw content has a separate content-addressed integrity check from summaries.
	id := sourceFor(history[0]).ID
	if _, err = db.Exec(`UPDATE seahorse_sources SET payload=jsonb_set(payload,'{message,Content}','"tampered raw source"') WHERE session_id=$1 AND source_id=$2`, "tamper", id); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Source(ctx, "tamper", id); err == nil {
		t.Fatal("corrupt raw record accepted")
	}
	if expansion, err := Expand(ctx, store, "tamper", state.HeadID, 4096, 4*1024*1024); err == nil || len(expansion.Messages) != 0 {
		t.Fatal("corrupt expansion returned partial evidence")
	}
	// Re-inserting the same content hash must not bless a corrupt existing row.
	head, err := store.Node(ctx, "tamper", state.HeadID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Commit(ctx, "tamper", state.Revision, []Source{sourceFor(history[0])}, nil, Snapshot{HeadID: head.Hash, InputHash: digest(history), Output: output}); err == nil {
		t.Fatal("corrupt immutable conflict silently accepted")
	}
	after, _ := store.Load(ctx, "tamper")
	if after.Revision != state.Revision {
		t.Fatal("corrupt-source transaction published")
	}
}
