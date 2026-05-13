package share

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// Uploader uploads HTML content somewhere and returns a public URL.
type Uploader interface {
	Upload(ctx context.Context, html string) (url string, err error)
	Name() string
}

// NewUploader returns an Uploader for the configured backend. When backend is
// "" defaults to gist when a token is present, otherwise transfer-sh.
func NewUploader(cfg config.ShareConfig) (Uploader, error) {
	be := cfg.Backend
	if be == "" {
		if cfg.GitHubToken != "" {
			be = "gist"
		} else {
			be = "transfer-sh"
		}
	}
	switch be {
	case "gist":
		if cfg.GitHubToken == "" {
			return nil, fmt.Errorf("share/gist: github_token required")
		}
		return &gistUploader{token: cfg.GitHubToken, endpoint: gistEndpoint, client: defaultClient()}, nil
	case "transfer-sh":
		return &transferShUploader{endpoint: transferShEndpoint, client: defaultClient()}, nil
	default:
		return nil, fmt.Errorf("share: unknown backend %q", be)
	}
}

func defaultClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

const (
	gistEndpoint       = "https://api.github.com/gists"
	transferShEndpoint = "https://transfer.sh"
)

type gistUploader struct {
	token    string
	endpoint string
	client   *http.Client
}

func (g *gistUploader) Name() string { return "gist" }

// gistPayload is exported indirectly via the JSON body. Keeping a typed struct
// makes the test assertion easier.
type gistPayload struct {
	Description string                 `json:"description"`
	Public      bool                   `json:"public"`
	Files       map[string]gistFileObj `json:"files"`
}

type gistFileObj struct {
	Content string `json:"content"`
}

func (g *gistUploader) Upload(ctx context.Context, htmlContent string) (string, error) {
	body := gistPayload{
		Description: "Overkill session",
		Public:      false,
		Files: map[string]gistFileObj{
			"session.html": {Content: htmlContent},
		},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("share/gist: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("share/gist: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("share/gist: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("share/gist: status %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		HTMLURL string `json:"html_url"`
		Files   map[string]struct {
			RawURL string `json:"raw_url"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("share/gist: decode: %w", err)
	}
	if out.HTMLURL == "" {
		return "", fmt.Errorf("share/gist: empty html_url")
	}
	return out.HTMLURL, nil
}

type transferShUploader struct {
	endpoint string
	client   *http.Client
}

func (t *transferShUploader) Name() string { return "transfer-sh" }

func (t *transferShUploader) Upload(ctx context.Context, htmlContent string) (string, error) {
	url := strings.TrimRight(t.endpoint, "/") + "/session.html"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(htmlContent))
	if err != nil {
		return "", fmt.Errorf("share/transfer-sh: new request: %w", err)
	}
	req.Header.Set("Content-Type", "text/html")
	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("share/transfer-sh: put: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("share/transfer-sh: status %d: %s", resp.StatusCode, string(b))
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("share/transfer-sh: read: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
