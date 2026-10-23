package api

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// Theme saves are acknowledged only after the selected profile is durably
// published. Failure leaves both the in-memory theme and its old file intact.
func (s *Server) handleConfigTheme(_ context.Context, params []byte) (any, *RPCError) {
	var p struct {
		Theme string `json:"theme"`
	}
	if len(params) > 0 && string(params) != "null" {
		if len(params) > 2048 || coordinationParams(params, &p) != nil {
			return nil, &RPCError{Code: InvalidParams, Message: "Invalid theme parameters."}
		}
	}
	if p.Theme == "" {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return map[string]string{"theme": s.cfg.UI.Theme}, nil
	}
	valid := false
	for _, name := range config.BuiltinThemes() {
		if p.Theme == name {
			valid = true
			break
		}
	}
	if !valid {
		if dir, err := config.ThemesDir(); err == nil {
			if entries, err := os.ReadDir(dir); err == nil {
				for _, entry := range entries {
					if !entry.IsDir() && strings.TrimSuffix(entry.Name(), ".toml") == p.Theme && strings.HasSuffix(entry.Name(), ".toml") {
						valid = true
						break
					}
				}
			}
		}
	}
	if !valid {
		return nil, &RPCError{Code: InvalidParams, Message: fmt.Sprintf("unknown theme: %q", p.Theme)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := *s.cfg
	next.UI.Theme = p.Theme
	path, err := next.PersistencePath()
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: "Theme could not be saved to its configuration profile."}
	}
	if err = next.Save(path); err != nil {
		return nil, &RPCError{Code: InternalError, Message: "Theme could not be saved to its configuration profile."}
	}
	s.cfg.UI.Theme = p.Theme
	return map[string]string{"theme": p.Theme}, nil
}
