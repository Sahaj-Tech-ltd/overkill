package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/stretchr/testify/require"
)

type delegatedGuardRunner struct {
	run func(context.Context) (*subagent.Result, error)
}

func (r delegatedGuardRunner) Run(ctx context.Context) (*subagent.Result, error) { return r.run(ctx) }

func TestCoordinatedDelegationAdmitsEachChildEffectWithoutNestedProjectDeadlock(t *testing.T) {
	s, front, _, _, _ := coordinationToolsFixture(t)
	manager := subagent.NewManager(subagent.Config{MaxDepth: 2, MaxChildren: 2, ChildTimeout: time.Second})
	guard := NewCoordinationExecutionGuard(s, front.ID, front.Workspace)
	manager.SetWorkerFactory(func(subagent.RealWorkerConfig) (subagent.WorkerRunner, error) {
		t.Fatal("unadmitted factory must not run")
		return nil, nil
	})
	_, err := guard(t.Context(), NewDelegateTool(manager), json.RawMessage(`{"name":"child","goal":"write fixture"}`))
	require.ErrorContains(t, err, "inherited child-tool admission")
	manager.SetGuardedWorkerFactory(func(subagent.RealWorkerConfig) (subagent.WorkerRunner, error) {
		return delegatedGuardRunner{run: func(ctx context.Context) (*subagent.Result, error) {
			_, err := guard(ctx, NewFSTool(front.Workspace), json.RawMessage(`{"action":"write","path":"owned.txt","content":"actual child effect"}`))
			return &subagent.Result{Status: "completed"}, err
		}}, nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err = guard(ctx, NewDelegateTool(manager), json.RawMessage(`{"name":"child","goal":"write fixture"}`))
	require.NoError(t, err)
	_, err = guard(ctx, NewDelegateTool(manager), json.RawMessage(`{"contract":{}}`))
	require.ErrorContains(t, err, "contract driver")
}
