package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/credentials"
	toml "github.com/pelletier/go-toml/v2"
)

const keyringPrefix = "keyring://"

type secretBinding struct{ Reference, Resolved string }
type credentialField struct {
	name   string
	secret bool
	get    func() string
	set    func(string)
}

// credentialFields is the single inventory for reference resolution and secure
// onboarding. Non-secret URLs support env references without vaulting benign URLs.
func (c *Config) credentialFields() []credentialField {
	var fields []credentialField
	add := func(name string, p *string, secret bool) {
		fields = append(fields, credentialField{name, secret, func() string { return *p }, func(v string) { *p = v }})
	}
	for i := range c.Providers {
		provider := &c.Providers[i]
		prefix := fmt.Sprintf("providers.%d", i)
		add(prefix+".api_key", &provider.APIKey, true)
		add(prefix+".base_url", &provider.BaseURL, false)
		var keys []string
		for key := range provider.Headers {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			headers, k := provider.Headers, key
			fields = append(fields, credentialField{prefix + ".headers." + k, true, func() string { return headers[k] }, func(v string) { headers[k] = v }})
		}
	}
	for i := range c.MCP.Servers {
		server := &c.MCP.Servers[i]
		var keys []string
		for key := range server.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			env, k := server.Env, key
			fields = append(fields, credentialField{fmt.Sprintf("mcp.servers.%d.env.%s", i, k), true, func() string { return env[k] }, func(v string) { env[k] = v }})
		}
	}
	add("tts.openai_key", &c.TTS.OpenAIKey, true)
	add("tts.elevenlabs_key", &c.TTS.ElevenLabsKey, true)
	add("imagegen.openai_key", &c.ImageGen.OpenAIKey, true)
	add("imagegen.stability_key", &c.ImageGen.StabilityKey, true)
	add("imagegen.replicate_token", &c.ImageGen.ReplicateToken, true)
	add("slack.app_token", &c.Slack.AppToken, true)
	add("slack.bot_token", &c.Slack.BotToken, true)
	add("gateways.slack.app_token", &c.Gateways.Slack.AppToken, true)
	add("gateways.slack.bot_token", &c.Gateways.Slack.BotToken, true)
	add("gateways.telegram.bot_token", &c.Gateways.Telegram.BotToken, true)
	add("gateways.discord.bot_token", &c.Gateways.Discord.BotToken, true)
	add("gateways.bridge.token", &c.Gateways.Bridge.Token, true)
	add("gateways.signal.auth_token", &c.Gateways.Signal.AuthToken, true)
	add("gateways.matrix.access_token", &c.Gateways.Matrix.AccessToken, true)
	add("gateways.matrix.password", &c.Gateways.Matrix.Password, true)
	add("gateways.mattermost.bot_token", &c.Gateways.Mattermost.BotToken, true)
	add("gateways.whatsapp.cloud.access_token", &c.Gateways.WhatsApp.Cloud.AccessToken, true)
	add("gateways.whatsapp.cloud.app_secret", &c.Gateways.WhatsApp.Cloud.AppSecret, true)
	add("gateways.whatsapp.cloud.verify_token", &c.Gateways.WhatsApp.Cloud.VerifyToken, true)
	add("sync.s3.access_key", &c.Sync.S3.AccessKey, true)
	add("sync.s3.secret_key", &c.Sync.S3.SecretKey, true)
	add("share.github_token", &c.Share.GitHubToken, true)
	add("acp.token", &c.ACP.Token, true)
	add("ouroboros.api_key", &c.Ouroboros.APIKey, true)
	add("vision.api_key", &c.Vision.APIKey, true)
	add("red_team.api_key", &c.RedTeam.APIKey, true)
	add("database_url", &c.DatabaseURL, true)
	return fields
}

func (c *Config) credentialCopy() (*Config, error) {
	data, err := toml.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("config: cannot copy configuration")
	}
	var cp Config
	if err := toml.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("config: cannot copy configuration")
	}
	cp.sourcePath = c.sourcePath
	cp.secretBindings = make(map[string]secretBinding, len(c.secretBindings))
	for key, value := range c.secretBindings {
		cp.secretBindings[key] = value
	}
	return &cp, nil
}

func (c *Config) persistenceCopy() (*Config, error) {
	cp, err := c.credentialCopy()
	if err != nil {
		return nil, err
	}
	for _, field := range cp.credentialFields() {
		if binding, ok := cp.secretBindings[field.name]; ok && field.get() == binding.Resolved {
			field.set(binding.Reference)
		} else {
			// Provider reordering/renaming must not turn a previously resolved secret
			// back into plaintext. Matching equal values is safe even for shared keys.
			for _, binding := range cp.secretBindings {
				if field.get() == binding.Resolved && binding.Resolved != "" {
					field.set(binding.Reference)
					break
				}
			}
		}
	}
	return cp, nil
}

func (c *Config) ResolveSecrets() error { return c.ResolveSecretsWithStore(credentials.NativeStore{}) }

// ResolveSecretsWithStore publishes only after all fields resolve. Binding
// snapshots preserve reference templates through future unrelated Save calls.
func (c *Config) ResolveSecretsWithStore(store credentials.Store) error {
	cp, err := c.persistenceCopy()
	if err != nil {
		return err
	}
	for _, field := range cp.credentialFields() {
		value := field.get()
		if binding, ok := cp.secretBindings[field.name]; ok && value == binding.Resolved {
			continue
		}
		resolved := value
		if strings.HasPrefix(value, keyringPrefix) {
			account, err := credentialAccount(value)
			if err != nil || store == nil {
				return fmt.Errorf("config: credential reference unavailable for %s", field.name)
			}
			resolved, err = store.Get(account)
			if err != nil {
				return fmt.Errorf("config: credential vault read failed for %s", field.name)
			}
		} else {
			resolved, err = resolveEnvRefs(value)
			if err != nil {
				return fmt.Errorf("config: environment reference unavailable for %s: %w", field.name, err)
			}
		}
		field.set(resolved)
		if value != resolved {
			cp.secretBindings[field.name] = secretBinding{value, resolved}
		} else {
			delete(cp.secretBindings, field.name)
		}
	}
	*c = *cp
	return nil
}

func credentialAccount(reference string) (string, error) {
	account := strings.TrimPrefix(reference, keyringPrefix)
	parts := strings.Split(account, "/")
	if !strings.HasPrefix(reference, keyringPrefix) || len(parts) != 2 || len(parts[0]) != 64 || len(parts[1]) != 32 {
		return "", fmt.Errorf("invalid credential reference")
	}
	for _, part := range parts {
		if _, err := hex.DecodeString(part); err != nil {
			return "", fmt.Errorf("invalid credential reference")
		}
	}
	return account, nil
}

// SaveWithCredentials stages new immutable vault entries before atomically
// publishing reference-only TOML. Failure never falls back to plaintext. Old
// entries are deliberately retained for backups and concurrently loaded profiles.
func (c *Config) SaveWithCredentials(path string, store credentials.Store) error {
	if store == nil {
		return fmt.Errorf("config: credential vault unavailable")
	}
	cp, err := c.persistenceCopy()
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("config: cannot resolve profile path")
	}
	cp.sourcePath = absolute
	profile := sha256.Sum256([]byte(absolute))
	var staged []string
	published := false
	defer func() {
		if !published {
			for _, account := range staged {
				_ = store.Delete(account)
			}
		}
	}()
	for _, field := range cp.credentialFields() {
		value := field.get()
		if !field.secret || value == "" || envVarPattern.MatchString(value) {
			continue
		}
		if strings.HasPrefix(value, keyringPrefix) {
			account, err := credentialAccount(value)
			if err != nil {
				return fmt.Errorf("config: invalid credential reference for %s", field.name)
			}
			if strings.HasPrefix(account, hex.EncodeToString(profile[:])+"/") {
				continue
			}
			// Explicit Save As creates independent vault entries for the new profile.
			value, err = store.Get(account)
			if err != nil {
				return fmt.Errorf("config: credential vault read failed for %s", field.name)
			}
		}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return fmt.Errorf("config: cannot generate credential identifier")
		}
		account := hex.EncodeToString(profile[:]) + "/" + hex.EncodeToString(nonce[:])
		// Record before Set: a backend can report an error after storing a value.
		staged = append(staged, account)
		if err := store.Set(account, value); err != nil {
			return fmt.Errorf("config: credential vault write failed for %s", field.name)
		}
		field.set(keyringPrefix + account)
	}
	if err := cp.ResolveSecretsWithStore(store); err != nil {
		return err
	}
	if err := cp.Save(absolute); err != nil {
		return err
	}
	published = true
	*c = *cp
	return nil
}
