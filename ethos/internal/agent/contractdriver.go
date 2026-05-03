// Package agent — adapter that lets a contract-driven AutonomousRunner
// drive a real *agent.Agent.
//
// Each StepDriver.Step is one full Agent.Run turn (assistant response +
// any internal tool calls until the model is done with that turn). The
// AutonomousRunner owns the iteration; the driver only handles one turn.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/ethos/internal/subagent"
)

// ContractDriver adapts *Agent to subagent.StepDriver.
type ContractDriver struct {
	agent    *Agent
	contract *subagent.Contract
	workdir  string
	bootstrapped bool
}

// NewContractDriver builds an adapter. workdir is the directory used to
// resolve relative OutFile specs; defaults to CWD.
func NewContractDriver(a *Agent, c *subagent.Contract, workdir string) *ContractDriver {
	if workdir == "" {
		workdir, _ = os.Getwd()
	}
	return &ContractDriver{agent: a, contract: c, workdir: workdir}
}

// Step runs one agent turn. The first call seeds the agent with the
// contract bootstrap prompt; subsequent calls nudge it with "continue".
func (d *ContractDriver) Step(ctx context.Context) subagent.StepResult {
	prompt := "continue"
	if !d.bootstrapped {
		prompt = d.bootstrapPrompt()
		d.bootstrapped = true
	}

	res, err := d.agent.Run(ctx, prompt)
	if err != nil {
		return subagent.StepResult{Err: err}
	}
	if res == nil {
		return subagent.StepResult{Done: true}
	}
	return subagent.StepResult{
		ToolCalls: res.ToolCalls,
		Tokens:    res.TotalTokens,
		Output:    res.Response,
		// Done if the model produced a turn with zero tool calls — it has
		// nothing more to do without further input.
		Done: res.ToolCalls == 0 && strings.TrimSpace(res.Response) != "",
	}
}

// Budget reads the agent's live budget estimate.
func (d *ContractDriver) Budget() subagent.BudgetSnapshot {
	rep := d.agent.BudgetReport()
	if rep == nil {
		return subagent.BudgetSnapshot{}
	}
	return subagent.BudgetSnapshot{
		CurrentTokens: rep.TotalEstimate,
		MaxTokens:     rep.MaxTokens,
	}
}

// Compact delegates to the agent's compaction pipeline.
func (d *ContractDriver) Compact(ctx context.Context) (subagent.BudgetSnapshot, error) {
	if _, err := d.agent.Compact(ctx); err != nil {
		return d.Budget(), err
	}
	return d.Budget(), nil
}

// CheckOutput satisfies OutFile by stat'ing the file under workdir. Other
// kinds are not auto-verifiable here — they're checked later via Acceptance.
func (d *ContractDriver) CheckOutput(ctx context.Context, outputs []subagent.Output) []string {
	out := make([]string, 0, len(outputs))
	for _, o := range outputs {
		if o.Kind != subagent.OutFile {
			continue
		}
		p := o.Spec
		if !filepath.IsAbs(p) {
			p = filepath.Join(d.workdir, p)
		}
		if _, err := os.Stat(p); err == nil {
			out = append(out, o.Spec)
		}
	}
	return out
}

func (d *ContractDriver) bootstrapPrompt() string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a sub-agent operating under a frozen contract. Run the work to completion without asking the user for input.\n\n")
	fmt.Fprintf(&b, "## Goal\n%s\n\n", d.contract.Goal)
	if len(d.contract.Scope) > 0 {
		fmt.Fprintf(&b, "## Scope (writes outside these paths will be DENIED)\n")
		for _, s := range d.contract.Scope {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		b.WriteString("\n")
	}
	if len(d.contract.OutOfScope) > 0 {
		fmt.Fprintf(&b, "## Out of scope (do NOT touch)\n")
		for _, s := range d.contract.OutOfScope {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		b.WriteString("\n")
	}
	if len(d.contract.Inputs) > 0 {
		fmt.Fprintf(&b, "## Inputs\n")
		for _, in := range d.contract.Inputs {
			fmt.Fprintf(&b, "- (%s) %s\n", in.Type, in.Value)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Expected outputs\n")
	for _, o := range d.contract.ExpectedOutputs {
		fmt.Fprintf(&b, "- [%s] %s\n", o.Kind, o.Spec)
	}
	b.WriteString("\n")
	if len(d.contract.IntegrationPoints) > 0 {
		fmt.Fprintf(&b, "## Integration points\n")
		for _, ip := range d.contract.IntegrationPoints {
			fmt.Fprintf(&b, "- %s", ip.Description)
			if ip.Reference != "" {
				fmt.Fprintf(&b, " (ref: %s)", ip.Reference)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(d.contract.Acceptance) > 0 {
		fmt.Fprintf(&b, "## Acceptance checks (must pass)\n")
		for _, a := range d.contract.Acceptance {
			fmt.Fprintf(&b, "- %s: `%s` (expect exit %d)\n", a.Name, a.Cmd, a.ExpectExit)
		}
		b.WriteString("\n")
	}
	b.WriteString("Begin. Use your tools. Do not stop until every expected output exists and every acceptance check passes.")
	return b.String()
}
