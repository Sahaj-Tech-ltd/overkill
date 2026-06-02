package providers

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// ==========================================================================
// Adversarial stress tests: Provider factory with unknown types,
// malformed configs, edge cases.
// ==========================================================================

// P-STRESS-1: Unknown provider type
func TestStress_UnknownProviderType(t *testing.T) {
	_, err := NewProvider(FactoryConfig{
		Name:   "test",
		Type:   "not_a_real_provider_at_all_xyz123",
		APIKey: "key",
	})
	if err == nil {
		t.Error("expected error for unknown provider type, got nil")
	} else {
		t.Logf("Unknown provider correctly rejected: %v", err)
	}
}

// P-STRESS-2: Empty provider type (zero-length string)
func TestStress_EmptyProviderType(t *testing.T) {
	_, err := NewProvider(FactoryConfig{
		Name: "test-empty",
		Type: "",
	})
	if err == nil {
		t.Error("expected error for empty provider type")
	} else {
		t.Logf("Empty provider type correctly rejected: %v", err)
	}
}

// P-STRESS-3: Custom provider without base URL
func TestStress_CustomNoBaseURL(t *testing.T) {
	_, err := NewProvider(FactoryConfig{
		Name: "custom-no-url",
		Type: "custom",
	})
	if err == nil {
		t.Error("expected error for custom provider with no base_url")
	} else {
		t.Logf("Custom without base URL correctly rejected: %v", err)
	}
}

// P-STRESS-4: Extremely long provider name
func TestStress_LongProviderName(t *testing.T) {
	longName := strings.Repeat("x", 100000)
	_, err := NewProvider(FactoryConfig{
		Name:    longName,
		Type:    "custom",
		APIKey:  "key",
		BaseURL: "https://api.example.com",
	})
	if err != nil && strings.Contains(err.Error(), "base_url") {
		t.Errorf("rejected valid custom provider with long name: %v", err)
	}
}

// P-STRESS-5: Emoji and Unicode in provider name and API key
func TestStress_EmojiInConfig(t *testing.T) {
	_, err := NewProvider(FactoryConfig{
		Name:    "😀🎉🚀🔥💩",
		Type:    "custom",
		APIKey:  "sk-🍕🍔🌮🍣🍜",
		BaseURL: "https://api.example.com/😀",
	})
	// Should not panic; may error on URL parse
	if err != nil {
		t.Logf("Emoji config: %v", err)
	} else {
		t.Log("Emoji config accepted")
	}
}

// P-STRESS-6: NUL bytes in API key
func TestStress_NULInAPIKey(t *testing.T) {
	_, err := NewProvider(FactoryConfig{
		Name:    "test",
		Type:    "custom",
		APIKey:  "sk-key\x00injected",
		BaseURL: "https://api.example.com",
	})
	if err != nil {
		t.Logf("NUL in API key: %v", err)
	}
}

// P-STRESS-7: Invalid URL in base URL
func TestStress_InvalidBaseURL(t *testing.T) {
	_, err := NewProvider(FactoryConfig{
		Name:    "test",
		Type:    "custom",
		APIKey:  "key",
		BaseURL: "not-a-valid-url://///bad::://",
	})
	if err != nil {
		t.Logf("Invalid base URL: %v", err)
	} else {
		t.Log("Invalid base URL accepted by factory (provider may fail later)")
	}
}

// P-STRESS-8: Bedrock with missing credentials
func TestStress_BedrockNoCreds(t *testing.T) {
	_, err := NewProvider(FactoryConfig{
		Name: "bedrock-no-creds",
		Type: "bedrock",
	})
	if err == nil {
		t.Error("expected error for bedrock without credentials")
	} else {
		t.Logf("Bedrock without credentials correctly rejected: %v", err)
	}
}

// P-STRESS-9: Bedrock with colon-separated API key but empty parts
func TestStress_BedrockEmptyParts(t *testing.T) {
	_, err := NewProvider(FactoryConfig{
		Name:   "bedrock",
		Type:   "bedrock",
		APIKey: ":", // both parts empty after split
	})
	if err == nil {
		t.Error("expected error for bedrock with empty credentials")
	} else {
		t.Logf("Bedrock with empty creds rejected: %v", err)
	}
}

// P-STRESS-10: Vertex without project
func TestStress_VertexNoProject(t *testing.T) {
	// Clear GOOGLE_CLOUD_PROJECT if set
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	_, err := NewProvider(FactoryConfig{
		Name: "vertex",
		Type: "vertex",
	})
	if err == nil {
		t.Error("expected error for vertex without project")
	} else {
		t.Logf("Vertex without project correctly rejected: %v", err)
	}
}

// P-STRESS-11: Azure without resource
func TestStress_AzureNoResource(t *testing.T) {
	t.Setenv("AZURE_OPENAI_RESOURCE", "")
	_, err := NewProvider(FactoryConfig{
		Name: "azure",
		Type: "azure",
	})
	if err == nil {
		t.Error("expected error for azure without resource")
	} else {
		t.Logf("Azure without resource correctly rejected: %v", err)
	}
}

// P-STRESS-12: Concurrent factory calls
func TestStress_ConcurrentFactory(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 50)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := NewProvider(FactoryConfig{
				Name:    fmt.Sprintf("provider%d", n),
				Type:    "custom",
				APIKey:  fmt.Sprintf("key%d", n),
				BaseURL: "https://api.example.com",
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent factory error: %v", err)
	}
}

// P-STRESS-13: Extremely long headers
func TestStress_LongHeaders(t *testing.T) {
	headers := make(map[string]string)
	headers["X-Very-Long-Header"] = strings.Repeat("v", 1_000_000)
	headers["X-Another-Long"] = strings.Repeat("x", 500_000)

	_, err := NewProvider(FactoryConfig{
		Name:    "long-headers",
		Type:    "custom",
		APIKey:  "key",
		BaseURL: "https://api.example.com",
		Headers: headers,
	})
	if err != nil {
		t.Logf("Long headers: %v", err)
	}
}

// P-STRESS-14: NewProvider with empty name (only type given)
func TestStress_EmptyNameOnlyType(t *testing.T) {
	p, err := NewProvider(FactoryConfig{
		Name:    "",
		Type:    "custom",
		APIKey:  "key",
		BaseURL: "https://api.example.com",
	})
	if err != nil {
		t.Logf("Empty name: %v", err)
	}
	if p != nil {
		t.Logf("Provider created with empty name: name=%q", p.Name())
	}
}

// P-STRESS-15: Failover chain with zero providers
func TestStress_FailoverZeroProviders(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: FailoverChain with zero providers: %v", r)
		}
	}()
	fc := NewFailoverChain()
	if fc == nil {
		t.Error("NewFailoverChain() returned nil")
	}
}
