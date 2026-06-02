// Package api — Secure key storage for API credentials.
//
// Overkill's security edge over OpenClaw: API keys are encrypted at rest
// using AES-256-GCM with a per-machine master key. Keys are never stored
// in plaintext in the config file. The master key is generated on first
// run and stored in ~/.overkill/.master.key (0600 permissions).
//
// Architecture:
//   - Master key: 32 random bytes, stored in ~/.overkill/.master.key
//   - Encrypted keys: stored alongside config as {key}_encrypted fields
//   - Decryption: on-demand, in-memory only, never persisted plain
//   - Migration: existing plaintext keys are encrypted on next save

package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"

	"github.com/rs/zerolog/log"
)

const (
	masterKeyFile = ".master.key"
	masterKeySize = 32 // AES-256
)

// SecureStore handles encryption/decryption of sensitive config values.
type SecureStore struct {
	masterKey []byte
	keyPath   string
}

// NewSecureStore initialises the secure key store. If no master key exists,
// one is generated and persisted. Returns nil if the config directory is
// not writable (keys will be stored plain).
func NewSecureStore(cfgDir string) (*SecureStore, error) {
	keyPath := filepath.Join(cfgDir, masterKeyFile)

	key, err := os.ReadFile(keyPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("secure: read master key: %w", err)
		}
		// Generate new master key.
		key = make([]byte, masterKeySize)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("secure: generate master key: %w", err)
		}
		if err := os.MkdirAll(cfgDir, 0700); err != nil {
			return nil, fmt.Errorf("secure: create config dir: %w", err)
		}
		if err := os.WriteFile(keyPath, key, 0600); err != nil {
			return nil, fmt.Errorf("secure: write master key: %w", err)
		}
		log.Info().Str("path", keyPath).Msg("generated new master key")
	}

	if len(key) != masterKeySize {
		return nil, fmt.Errorf("secure: master key is %d bytes, expected %d", len(key), masterKeySize)
	}

	log.Debug().Str("path", keyPath).Msg("secure store initialised")

	return &SecureStore{
		masterKey: key,
		keyPath:   keyPath,
	}, nil
}

// MasterKeyPath returns the absolute path to the master key file.
// Useful for diagnostics: "can the agent read this file?"
func MasterKeyPath(cfgDir string) string {
	return filepath.Join(cfgDir, masterKeyFile)
}

// HealthCheck verifies the secure store can encrypt and decrypt.
// Returns nil if everything works, or an error describing what's broken.
// Call this during agent startup to catch env/directory issues early.
func (s *SecureStore) HealthCheck() error {
	if s == nil {
		return fmt.Errorf("secure: store is nil (encryption disabled)")
	}

	// Verify master key is the right size.
	if len(s.masterKey) != masterKeySize {
		return fmt.Errorf("secure: master key is %d bytes, expected %d", len(s.masterKey), masterKeySize)
	}

	// Verify encrypt/decrypt round-trip.
	testValue := "health-check-" + hex.EncodeToString(s.masterKey[:4])
	encrypted, err := s.Encrypt(testValue)
	if err != nil {
		return fmt.Errorf("secure: encrypt failed: %w", err)
	}

	decrypted, err := s.Decrypt(encrypted)
	if err != nil {
		return fmt.Errorf("secure: decrypt failed: %w", err)
	}

	if decrypted != testValue {
		return fmt.Errorf("secure: round-trip mismatch: got %q, want %q", decrypted, testValue)
	}

	// Verify key file is readable.
	if _, err := os.Stat(s.keyPath); err != nil {
		return fmt.Errorf("secure: master key file not accessible at %s: %w", s.keyPath, err)
	}

	return nil
}

// Encrypt encrypts a plaintext value using AES-256-GCM.
// Returns a base64-encoded ciphertext prefixed with the hex nonce.
func (s *SecureStore) Encrypt(plaintext string) (string, error) {
	if s == nil {
		return plaintext, nil // no encryption available
	}

	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return "", fmt.Errorf("secure: create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("secure: create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secure: generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)

	// Format: hex_nonce:base64_ciphertext
	encoded := hex.EncodeToString(nonce) + ":" + base64.StdEncoding.EncodeToString(ciphertext)
	return encoded, nil
}

// Decrypt decrypts a value previously encrypted with Encrypt.
// Returns the plaintext or an error if the ciphertext is malformed.
func (s *SecureStore) Decrypt(encoded string) (string, error) {
	if s == nil {
		return encoded, nil // no encryption
	}

	// Split nonce:ciphertext.
	parts := split2(encoded, ":")
	if len(parts) != 2 {
		// Not encrypted — return as-is (backward compat with plain keys).
		return encoded, nil
	}

	nonce, err := hex.DecodeString(parts[0])
	if err != nil {
		return encoded, nil // not our format
	}

	ciphertext, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("secure: decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return "", fmt.Errorf("secure: create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("secure: create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("secure: decrypt: %w", err)
	}

	return string(plaintext), nil
}

// IsEncrypted returns true if the value looks like an encrypted string
// (hex_nonce:base64_ciphertext format).
func (s *SecureStore) IsEncrypted(value string) bool {
	if s == nil {
		return false
	}
	parts := split2(value, ":")
	if len(parts) != 2 {
		return false
	}
	_, err := hex.DecodeString(parts[0])
	return err == nil
}

// SecureConfig encrypts all plaintext API keys in a config and returns
// a new config with encrypted values. The original is not modified.
// Call this before saving config to disk.
func (s *SecureStore) SecureConfig(cfg *config.Config) (*config.Config, error) {
	if s == nil {
		return cfg, nil
	}

	// Deep copy via JSON round-trip (simple, works for all config shapes).
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("secure: marshal config: %w", err)
	}

	var secured config.Config
	if err := json.Unmarshal(data, &secured); err != nil {
		return nil, fmt.Errorf("secure: unmarshal config: %w", err)
	}

	encryptIfPlain := func(field *string) error {
		if field == nil || *field == "" {
			return nil
		}
		if s.IsEncrypted(*field) {
			return nil // already encrypted
		}
		enc, err := s.Encrypt(*field)
		if err != nil {
			return err
		}
		*field = enc
		return nil
	}

	// Encrypt all provider API keys.
	for i := range secured.Providers {
		if err := encryptIfPlain(&secured.Providers[i].APIKey); err != nil {
			return nil, fmt.Errorf("secure: provider %s: %w", secured.Providers[i].Name, err)
		}
	}

	// Encrypt gateway tokens.
	if err := encryptIfPlain(&secured.Gateways.Telegram.BotToken); err != nil {
		return nil, err
	}
	if err := encryptIfPlain(&secured.Gateways.Discord.BotToken); err != nil {
		return nil, err
	}
	if err := encryptIfPlain(&secured.Gateways.Slack.BotToken); err != nil {
		return nil, err
	}

	return &secured, nil
}

// DecryptConfig decrypts all API keys in a config in-place.
func (s *SecureStore) DecryptConfig(cfg *config.Config) error {
	if s == nil {
		return nil
	}

	decryptField := func(field *string) error {
		if field == nil || *field == "" {
			return nil
		}
		if !s.IsEncrypted(*field) {
			return nil // plain or empty
		}
		plain, err := s.Decrypt(*field)
		if err != nil {
			return fmt.Errorf("secure: decrypt: %w", err)
		}
		*field = plain
		return nil
	}

	for i := range cfg.Providers {
		if err := decryptField(&cfg.Providers[i].APIKey); err != nil {
			return err
		}
	}

	_ = decryptField(&cfg.Gateways.Telegram.BotToken)
	_ = decryptField(&cfg.Gateways.Discord.BotToken)
	_ = decryptField(&cfg.Gateways.Slack.BotToken)

	return nil
}

func split2(s, sep string) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == sep[0] {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

// --- Wizard API handlers ---

// handleWizardCatalog returns the full setup wizard catalog with ⭐ ratings.
func (s *Server) handleWizardCatalog(_ context.Context, _ []byte) (interface{}, *RPCError) {
	cat := config.BuildWizardCatalog()

	return &WizardCatalogResult{
		Providers:   config.SortedOptions(cat.Providers),
		Gateways:    config.SortedOptions(cat.Gateways),
		TTS:         config.SortedOptions(cat.TTS),
		Databases:   config.SortedOptions(cat.Databases),
		Review:      config.SortedOptions(cat.Review),
		Recommended: cat.RecommendedQuickSetup(),
	}, nil
}

// handleWizardQuickSetup applies a one-click recommended setup.
func (s *Server) handleWizardQuickSetup(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p WizardQuickSetupParams
	if len(params) > 0 {
		if err := unmarshalParams(params, &p); err != nil {
			return nil, err
		}
	}

	cat := config.BuildWizardCatalog()
	qs := cat.RecommendedQuickSetup()

	// Override with user choices if provided.
	if p.Provider != "" {
		qs.Provider = p.Provider
	}
	if p.Model != "" {
		qs.Model = p.Model
	}
	if p.Gateway != "" {
		qs.Gateway = p.Gateway
	}
	if p.TTS != "" {
		qs.TTS = p.TTS
	}
	if p.Database != "" {
		qs.Database = p.Database
	}
	if p.ReviewProvider != "" {
		qs.ReviewProvider = p.ReviewProvider
	}
	if p.ReviewModel != "" {
		qs.ReviewModel = p.ReviewModel
	}

	// Build config from recommended defaults.
	cfg := config.Default()
	cat.ApplyQuickSetup(cfg)

	// If user picked a specific provider, make sure it's in the provider list.
	wizard := config.NewSetupWizard(cfg)
	for _, ps := range wizard.AvailableProviders() {
		if ps.Name == qs.Provider || strings.EqualFold(ps.Name, qs.Provider) {
			cfg.Agent.DefaultProvider = qs.Provider
			if qs.Model != "" {
				cfg.Agent.DefaultModel = qs.Model
			}
			break
		}
	}

	// Enable the chosen gateway.
	switch qs.Gateway {
	case "telegram":
		cfg.Gateways.Telegram.Enabled = true
	case "discord":
		cfg.Gateways.Discord.Enabled = true
	}

	// Apply user's review provider choice — ApplyQuickSetup used catalog
	// defaults, so we must override with the user's actual selection.
	if qs.ReviewProvider != "" && qs.ReviewProvider != "same_as_main" {
		cfg.Ouroboros.Provider = qs.ReviewProvider
		cfg.RedTeam.Provider = qs.ReviewProvider
		if qs.ReviewModel != "" {
			cfg.Ouroboros.Model = qs.ReviewModel
			cfg.RedTeam.Model = qs.ReviewModel
		}
	} else if qs.ReviewProvider == "same_as_main" {
		cfg.Ouroboros.Provider = qs.Provider
		cfg.RedTeam.Provider = qs.Provider
	}

	// Save the config.
	cfgPath, err := config.ConfigPath()
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}

	if err := cfg.Save(cfgPath); err != nil {
		return nil, &RPCError{Code: InternalError, Message: fmt.Sprintf("failed to save config: %v", err)}
	}

	// Update in-memory config.
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()

	return &WizardQuickSetupResult{
		Status: "ok",
		Message: fmt.Sprintf("Setup complete: %s / %s / %s. Config saved to %s",
			qs.Provider, qs.Model, qs.Gateway, cfgPath),
	}, nil
}
