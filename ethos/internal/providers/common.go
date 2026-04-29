package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
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
			Timeout: 30 * time.Second,
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
	req.Header.Set("User-Agent", "ethos/0.1.0")

	for k, v := range bp.headers {
		req.Header.Set(k, v)
	}

	return bp.httpClient.Do(req)
}

func (bp *BaseProvider) handleHTTPError(resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("providers: HTTP %d: failed to read body: %w", resp.StatusCode, err)
	}
	return &HTTPError{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
}
