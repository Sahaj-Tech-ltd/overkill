package compaction

import (
	"context"
	"database/sql"
	"github.com/Sahaj-Tech-ltd/overkill/internal/compaction/seahorse"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"testing"
)

func TestHistoryFactoryStrategiesAndModelPolicy(t *testing.T) {
	db, _ := testpg.Open(t)
	ctx := context.Background()
	cfg := config.Default().Compaction
	cfg.PreserveMessages = 1
	cfg.MaxSummaryTokens = 128
	var selected []string
	provider := providers.NewMockProvider("fixture", []providers.Model{{ID: "expensive", CostOut: 10, ContextWindow: 8192}, {ID: "cheap", CostOut: 1, ContextWindow: 8192}}, func(r providers.Request) (providers.Response, error) {
		selected = append(selected, r.Model)
		return providers.Response{Model: r.Model, Content: "Observed state retained."}, nil
	})
	factory, err := NewHistoryFactory(ctx, cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	if factory.Strategy() != "seahorse" || factory.Store == nil {
		t.Fatal("durable default not selected")
	}
	c, use, err := factory.Build(provider, "cheap-session")
	if err != nil || !use {
		t.Fatalf("DAG build: %v", err)
	}
	if _, ok := c.(*seahorse.HistoryCompactor); !ok {
		t.Fatalf("wrong durable strategy %T", c)
	}
	history := []providers.Message{{Role: "user", Content: "prior constraint"}, {Role: "assistant", Content: "observed state"}, {Role: "user", Content: "current request"}}
	if _, _, err = c.Compact(ctx, history, "parent", 8192); err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0] != "cheap" {
		t.Fatalf("existing cheapest-provider policy ignored: %v", selected)
	}
	cfg.Model = "expensive"
	factory, err = NewHistoryFactory(ctx, cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	c, _, err = factory.Build(provider, "explicit-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = c.Compact(ctx, history, "parent", 8192); err != nil {
		t.Fatal(err)
	}
	if selected[len(selected)-1] != "expensive" {
		t.Fatal("explicit compaction model ignored")
	}
	cfg.Strategy = "lcm"
	factory, err = NewHistoryFactory(ctx, cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	c, use, err = factory.Build(provider, "lcm-session")
	if err != nil || !use || factory.Store != nil {
		t.Fatal("explicit LCM changed strategy")
	}
	if _, ok := c.(*AgentCompactor); !ok {
		t.Fatalf("wrong explicit LCM %T", c)
	}
	cfg.UseLCM = false
	cfg.Strategy = "seahorse"
	factory, err = NewHistoryFactory(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, use, err = factory.Build(nil, "")
	if err != nil || use || c != nil {
		t.Fatal("legacy kill switch ignored")
	}
	cfg.UseLCM = true
	closed, err := sql.Open("postgres", "postgres://unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	if _, err = NewHistoryFactory(ctx, cfg, closed); err == nil {
		t.Fatal("selected unavailable DAG silently downgraded")
	}
}
