package config

import "errors"

// SandboxAvailable describes enforced process/filesystem isolation across tools.
// Workspace path checks and command deny patterns do not provide that boundary.
const SandboxAvailable = false

const SandboxUnavailableReason = "execution sandbox unavailable: tools are not isolated from the host"

var ErrSandboxUnsupported = errors.New("config: security.sandbox_enabled=true is unsupported: no execution sandbox is enforced; set security.sandbox_enabled=false")

// ValidateSupported rejects requested security guarantees the runtime cannot
// enforce. It never clears an explicit request or treats deny rules as isolation.
func (s SecurityConfig) ValidateSupported() error {
	if s.SandboxEnabled {
		return ErrSandboxUnsupported
	}
	return nil
}
