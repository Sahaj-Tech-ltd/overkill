package imagegen

import (
	"encoding/json"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

func TestResearchImageGenerationRejectsPrivateInitialDownload(t *testing.T) {
	var gets atomic.Int32
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/generate" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"url": base + "/private-image"}}})
			return
		}
		gets.Add(1)
		w.Write([]byte("harmless loopback image marker"))
	}))
	defer server.Close()
	base = server.URL
	t.Setenv("OPENAI_IMAGE_URL", base+"/generate")
	tool := New(config.ImageGenConfig{Provider: "openai", OpenAIKey: "research-not-a-real-key"})
	output, err := tool.Execute(t.Context(), json.RawMessage(`{"prompt":"bounded local fixture"}`))
	var got ImageGenOutput
	if json.Unmarshal(output, &got) == nil {
		for _, path := range got.Images {
			defer os.Remove(path)
		}
	}
	t.Logf("actual image_gen OpenAI request→provider URL download: err=%v private_initial_requests=%d local_outputs=%d", err, gets.Load(), len(got.Images))
	if gets.Load() != 0 || err == nil {
		t.Fatal("provider-returned loopback image URL was fetched instead of rejected")
	}
}
