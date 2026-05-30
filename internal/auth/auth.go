// Package auth implements OAuth device-code flows for providers that don't
// use static API keys (Anthropic Claude.ai, GitHub Copilot, etc.).
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

// DeviceFlow is the state returned by StartDeviceFlow. The TUI shows
// VerificationURL + UserCode to the user; the deviceCode is opaque and used
// internally for polling.
type DeviceFlow struct {
	UserCode        string    `json:"user_code"`
	VerificationURL string    `json:"verification_url"`
	ExpiresAt       time.Time `json:"expires_at"`
	Interval        int       `json:"interval"`

	deviceCode string
	provider   string
	clientID   string
}

// DeviceCode returns the opaque device code for polling.
func (d *DeviceFlow) DeviceCode() string { return d.deviceCode }

// Provider returns which provider this flow targets.
func (d *DeviceFlow) Provider() string { return d.provider }

// Token is a stored OAuth credential.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Provider     string    `json:"provider"`
}

// providerConfig holds the OAuth endpoints/clientID per provider. Values are
// public client IDs published by the upstream apps (Claude Code & Copilot
// both ship with public client IDs; the user is the client).
type providerConfig struct {
	ClientID     string
	DeviceURL    string
	TokenURL     string
	Scope        string
	GrantType    string
	HeaderEditor string // e.g. "anthropic-beta: oauth-2025-04-20" optional
}

var providerConfigs = map[string]providerConfig{
	"anthropic": {
		ClientID:  "9d1c250a-e61b-44d9-88ed-5944d1962f5e", // Claude Code public client
		DeviceURL: "https://console.anthropic.com/v1/oauth/device/code",
		TokenURL:  "https://console.anthropic.com/v1/oauth/token",
		Scope:     "org:create_api_key user:profile user:inference",
		GrantType: "urn:ietf:params:oauth:grant-type:device_code",
	},
	"copilot": {
		ClientID:  "Iv1.b507a08c87ecfe98", // GitHub Copilot CLI client (public)
		DeviceURL: "https://github.com/login/device/code",
		TokenURL:  "https://github.com/login/oauth/access_token",
		Scope:     "read:user",
		GrantType: "urn:ietf:params:oauth:grant-type:device_code",
	},
}

// httpClient is overrideable for tests.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// endpointOverride lets tests redirect URLs.
var (
	endpointMu       sync.Mutex
	endpointOverride = map[string]providerConfig{}
)

// SetEndpointOverride is exposed for tests to redirect the device/token URLs
// to an httptest.Server.
func SetEndpointOverride(provider string, cfg providerConfig) {
	endpointMu.Lock()
	defer endpointMu.Unlock()
	endpointOverride[provider] = cfg
}

// ClearEndpointOverride removes a test override.
func ClearEndpointOverride(provider string) {
	endpointMu.Lock()
	defer endpointMu.Unlock()
	delete(endpointOverride, provider)
}

// SetHTTPClient is for test injection.
func SetHTTPClient(c *http.Client) {
	if c != nil {
		httpClient = c
	}
}

func cfgFor(provider string) (providerConfig, bool) {
	endpointMu.Lock()
	defer endpointMu.Unlock()
	if c, ok := endpointOverride[provider]; ok {
		return c, true
	}
	c, ok := providerConfigs[provider]
	return c, ok
}

// StartDeviceFlow kicks off a device-code authorization. The returned flow
// contains a code/URL the user must visit, and the deviceCode used for polling.
func StartDeviceFlow(ctx context.Context, provider string) (*DeviceFlow, error) {
	pc, ok := cfgFor(provider)
	if !ok {
		return nil, fmt.Errorf("auth: provider %q not supported", provider)
	}
	form := url.Values{}
	form.Set("client_id", pc.ClientID)
	form.Set("scope", pc.Scope)

	req, err := http.NewRequestWithContext(ctx, "POST", pc.DeviceURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: device request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("auth: device endpoint returned %d", resp.StatusCode)
	}
	var body struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURL string `json:"verification_uri"`
		VerificationAlt string `json:"verification_url"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("auth: decode device response: %w", err)
	}
	verURL := body.VerificationURL
	if verURL == "" {
		verURL = body.VerificationAlt
	}
	if body.Interval <= 0 {
		body.Interval = 5
	}
	if body.ExpiresIn <= 0 {
		body.ExpiresIn = 900
	}
	return &DeviceFlow{
		UserCode:        body.UserCode,
		VerificationURL: verURL,
		ExpiresAt:       time.Now().Add(time.Duration(body.ExpiresIn) * time.Second),
		Interval:        body.Interval,
		deviceCode:      body.DeviceCode,
		provider:        provider,
		clientID:        pc.ClientID,
	}, nil
}

// PollForToken polls the token endpoint until the user authorizes (returns
// Token), denies (returns error), or the ctx is canceled.
func PollForToken(ctx context.Context, flow *DeviceFlow) (*Token, error) {
	if flow == nil {
		return nil, errors.New("auth: nil flow")
	}
	pc, ok := cfgFor(flow.provider)
	if !ok {
		return nil, fmt.Errorf("auth: provider %q not supported", flow.provider)
	}
	interval := time.Duration(flow.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		if time.Now().After(flow.ExpiresAt) {
			return nil, errors.New("auth: device flow expired")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		tok, err, retry := exchangeOnce(ctx, pc, flow.deviceCode)
		if retry {
			continue
		}
		if err != nil {
			return nil, err
		}
		tok.Provider = flow.provider
		return tok, nil
	}
}

func exchangeOnce(ctx context.Context, pc providerConfig, deviceCode string) (*Token, error, bool) {
	form := url.Values{}
	form.Set("client_id", pc.ClientID)
	form.Set("device_code", deviceCode)
	form.Set("grant_type", pc.GrantType)

	req, err := http.NewRequestWithContext(ctx, "POST", pc.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err, false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: token request: %w", err), false
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		TokenType        string `json:"token_type"`
		ExpiresIn        int    `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err, false
	}
	if body.Error != "" {
		switch body.Error {
		case "authorization_pending", "slow_down":
			return nil, nil, true
		case "access_denied":
			return nil, errors.New("auth: user denied"), false
		case "expired_token":
			return nil, errors.New("auth: token expired"), false
		default:
			return nil, fmt.Errorf("auth: %s: %s", body.Error, body.ErrorDescription), false
		}
	}
	if body.AccessToken == "" {
		return nil, nil, true
	}
	tok := &Token{
		AccessToken:  body.AccessToken,
		RefreshToken: body.RefreshToken,
		TokenType:    body.TokenType,
	}
	if body.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	}
	return tok, nil, false
}

// SaveToken persists a token to ~/.overkill/auth/<provider>.json with mode 0600.
// authDirOverride lets tests redirect storage.
var authDirOverride string

// SetAuthDir is a test hook to override the auth storage directory.
func SetAuthDir(dir string) { authDirOverride = dir }

func authDir() (string, error) {
	if authDirOverride != "" {
		return authDirOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".overkill", "auth"), nil
}

// SaveToken writes a token to disk with restrictive permissions.
func SaveToken(tok *Token) error {
	if tok == nil || tok.Provider == "" {
		return errors.New("auth: invalid token")
	}
	dir, err := authDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path, err := security.SafePath(dir, tok.Provider+".json")
	if err != nil {
		return fmt.Errorf("auth: save token: %w", err)
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, data, 0o600)
}

// LoadToken reads a token for the given provider, or returns (nil, nil) if
// not present.
func LoadToken(provider string) (*Token, error) {
	dir, err := authDir()
	if err != nil {
		return nil, err
	}
	path, err := security.SafePath(dir, provider+".json")
	if err != nil {
		return nil, fmt.Errorf("auth: load token: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var tok Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// DeleteToken removes a stored token (used by /logout).
func DeleteToken(provider string) error {
	dir, err := authDir()
	if err != nil {
		return err
	}
	path, err := security.SafePath(dir, provider+".json")
	if err != nil {
		return fmt.Errorf("auth: delete token: %w", err)
	}
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SupportedProviders returns the list of providers with OAuth support.
func SupportedProviders() []string {
	out := make([]string, 0, len(providerConfigs))
	for k := range providerConfigs {
		out = append(out, k)
	}
	return out
}
