package agent

import "github.com/Sahaj-Tech-ltd/overkill/internal/instructions"

// SetInstructionSource installs explicitly authorized guidance. Passing nil
// revokes it for subsequent requests without retaining contents in history.
func (a *Agent) SetInstructionSource(source *instructions.Source) {
	a.mu.Lock()
	a.instructionSource = source
	a.mu.Unlock()
}

func (a *Agent) instructionPrompt() string {
	a.mu.RLock()
	source := a.instructionSource
	a.mu.RUnlock()
	return source.Render()
}

func (a *Agent) withInstructions(prompt string) string {
	if block := a.instructionPrompt(); block != "" {
		return prompt + "\n\n" + block
	}
	return prompt
}
