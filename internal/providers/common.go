package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type BaseProvider struct {
	name       string
	baseURL    string
	apiKey     string
	models     []Model
	httpClient *http.Client
	headers    map[string]string
}

func NewBaseProvider(name, baseURL, apiKey string, models []Model) *BaseProvider {
	return &BaseProvider{
		name:    name,
		baseURL: baseURL,
		apiKey:  apiKey,
		models:  models,
		httpClient: &http.Client{
			// No request-level timeout. http.Client.Timeout applies to the
			// WHOLE request including reading the body, which silently kills
			// streaming responses at wall-clock 30s. Cancellation is driven
			// entirely by the context passed into doRequest — callers wrap
			// with context.WithTimeout when they need a deadline for
			// non-streaming requests. Streaming Stream() callers rely on
			// the agent loop's ctx for cancellation.
			Timeout: 0,
		},
		headers: make(map[string]string),
	}
}

func (bp *BaseProvider) Name() string {
	return bp.name
}

func (bp *BaseProvider) Models() []Model {
	return bp.models
}

// SetCustomHeaders merges the given headers into the provider's
// per-request header map. Existing keys are overwritten.
// Restricted headers (Authorization, X-Api-Key, Content-Type) are
// ignored to prevent credential replacement or content-type breakage.
func (bp *BaseProvider) SetCustomHeaders(h map[string]string) {
	restricted := map[string]bool{
		"Authorization": true,
		"X-Api-Key":     true,
		"Content-Type":  true,
	}
	for k, v := range h {
		if restricted[http.CanonicalHeaderKey(k)] {
			continue
		}
		bp.headers[k] = v
	}
}

func (bp *BaseProvider) doRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("providers: marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, bp.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("providers: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "overkill/"+Version)

	for k, v := range bp.headers {
		req.Header.Set(k, v)
	}

	return bp.httpClient.Do(req)
}

func (bp *BaseProvider) handleHTTPError(resp *http.Response) error {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("providers: HTTP %d: failed to read body: %w", resp.StatusCode, err)
	}
	return &HTTPError{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
}
