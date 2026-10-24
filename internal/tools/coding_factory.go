package tools

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/pty"
	"github.com/Sahaj-Tech-ltd/overkill/internal/terminal"
)

// NewWorkspaceCodingTools is the shared coding capability assembler for normal
// sessions and delegated workers. Every call constructs fresh tool adapters;
// only application-owned language-server services may be shared. It contains
// no delegation, account, browser, messaging or permission-changing tools.
// CWD scopes rooted file operations and command working directories; it is not
// an OS sandbox for arbitrary shell commands.
func NewWorkspaceCodingTools(deps FactoryDeps) []Tool {
	result := []Tool{
		NewShellTool(func(s *ShellTool) { s.defaultWorkingDir = deps.CWD }),
		NewFSTool(deps.CWD), NewGitTool(deps.CWD), NewGrepTool(deps.CWD),
		NewPatchTool(deps.CWD),
	}
	if pty.Supported() {
		result = append(result, NewPTYShellTool(deps.CWD))
	}
	if deps.TerminalManager != nil && terminal.Supported() {
		result = append(result, NewTerminalTool(deps.TerminalManager, deps.TerminalOwner, deps.CWD))
	}
	if deps.LSPQuerier != nil {
		result = append(result, NewLSPHoverTool(deps.LSPQuerier), NewLSPDefinitionTool(deps.LSPQuerier), NewLSPReferencesTool(deps.LSPQuerier))
	}
	if deps.LSPManager != nil {
		result = append(result, NewLSPSymbolsTool(deps.LSPManager))
	}
	return result
}
