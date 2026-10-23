package tools

import (
	"context"
	"encoding/json"
	"github.com/Sahaj-Tech-ltd/overkill/internal/compaction/seahorse"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"testing"
)

func TestDAGMemoryToolsBoundIdentityAndInputs(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := seahorse.NewPostgresStore(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range NewDAGMemoryTools(store, "trusted-session") {
		metadata, ok := tool.(interface{ Schema() json.RawMessage })
		if !ok || !json.Valid(metadata.Schema()) {
			t.Fatal("missing native schema")
		}
		if security.IsWriteLikeTool(tool.Name(), json.RawMessage(`{}`)) {
			t.Errorf("read-only memory tool blocked by Reader: %s", tool.Name())
		}
		for _, input := range []string{`{"session_id":"other-chat","query":"x"}`, `{"database":"other","query":"x"}`, `{"query":"x","limit":51}`, `{"node_id":"../other"}`, `{"source_id":"invalid"}`, `{} {}`, `null`, `{"query":"x","query":"y"}`, `{"limit":0}`, `{"node_id":null}`} {
			if _, err := tool.Execute(context.Background(), json.RawMessage(input)); err == nil {
				t.Errorf("%s admitted malformed/cross-session input %s", tool.Name(), input)
			}
		}
	}
}
