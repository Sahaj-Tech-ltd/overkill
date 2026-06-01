// Package verify — syntax verifiers for declarative formats.
//
// TOML/JSON/YAML files are easy to write code-completing into
// invalid states (unmatched braces, trailing commas, etc). Inline
// parse-only verification catches these before they propagate into
// "agent thinks the config is valid, downstream consumer crashes".
//
// Each verifier reads the on-disk file (the write tool has already
// flushed) and parses. No shell-out, no temp files — pure in-process
// validation, sub-millisecond for typical config sizes.
package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// TOMLVerifier checks .toml syntax via the standard parser.
type TOMLVerifier struct{}

func NewTOMLVerifier() *TOMLVerifier           { return &TOMLVerifier{} }
func (t *TOMLVerifier) Name() string           { return "toml parse" }
func (t *TOMLVerifier) Timeout() time.Duration { return 2 * time.Second }

func (t *TOMLVerifier) Verify(ctx context.Context, absPath string, content []byte) (bool, string, bool) {
	data, err := readVerifyFile(absPath, content)
	if err != nil {
		return false, "read file: " + err.Error(), true
	}
	var sink any
	if err := toml.Unmarshal(data, &sink); err != nil {
		return false, err.Error(), false
	}
	return true, "", false
}

// JSONVerifier checks .json syntax. We unmarshal into any so the
// parser accepts both objects and arrays at the root.
type JSONVerifier struct{}

func NewJSONVerifier() *JSONVerifier           { return &JSONVerifier{} }
func (j *JSONVerifier) Name() string           { return "json parse" }
func (j *JSONVerifier) Timeout() time.Duration { return 2 * time.Second }

func (j *JSONVerifier) Verify(ctx context.Context, absPath string, content []byte) (bool, string, bool) {
	data, err := readVerifyFile(absPath, content)
	if err != nil {
		return false, "read file: " + err.Error(), true
	}
	var sink any
	if err := json.Unmarshal(data, &sink); err != nil {
		return false, err.Error(), false
	}
	return true, "", false
}

// YAMLVerifier checks .yaml/.yml syntax via yaml.v3.
type YAMLVerifier struct{}

func NewYAMLVerifier() *YAMLVerifier           { return &YAMLVerifier{} }
func (y *YAMLVerifier) Name() string           { return "yaml parse" }
func (y *YAMLVerifier) Timeout() time.Duration { return 2 * time.Second }

func (y *YAMLVerifier) Verify(ctx context.Context, absPath string, content []byte) (bool, string, bool) {
	data, err := readVerifyFile(absPath, content)
	if err != nil {
		return false, "read file: " + err.Error(), true
	}
	var sink any
	if err := yaml.Unmarshal(data, &sink); err != nil {
		return false, err.Error(), false
	}
	return true, "", false
}

// readVerifyFile prefers the supplied content bytes when non-nil
// (allows pre-flight checks before the tool flushes) and falls back
// to os.ReadFile. The agent's current Edit/Write tools always flush
// before calling us, so the read path is the common case.
//
// File-not-found is returned via an error sentinel callers map to
// "skipped" because verifying a path that doesn't exist is a meta-
// problem (race with another tool deleting?) not a syntax issue.
func readVerifyFile(path string, content []byte) ([]byte, error) {
	if content != nil {
		return content, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("file %s does not exist", path)
		}
		return nil, err
	}
	return b, nil
}
