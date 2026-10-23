package config

import "strings"

// RedactionSecrets is for trusted local recorders. It must never be exposed by
// an API or logged: it contains resolved credentials, not masked settings.
func (c *Config) RedactionSecrets() []string {
	if c == nil {
		return nil
	}
	seen := make(map[string]bool)
	var values []string
	for _, field := range c.credentialFields() {
		value := field.get()
		if !field.secret || value == "" || strings.HasPrefix(value, keyringPrefix) || envVarPattern.MatchString(value) || seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	return values
}
