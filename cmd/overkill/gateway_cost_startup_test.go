package main

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// Use a subprocess because legacy gateway construction reads process-global
// configuration and starts an agent's background services. No bot is connected:
// configured database failures must surface before credential validation.
func TestGatewayCostStartupHelper(t *testing.T) {
	name := os.Getenv("OVERKILL_GATEWAY_COST_HELPER")
	if name == "" {
		return
	}
	cfg = config.Default()
	cfg.DatabaseURL = os.Getenv("OVERKILL_GATEWAY_COST_DSN")
	cfg.Agent.DefaultModel = "fixture"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", BaseURL: "http://127.0.0.1:1", Models: []config.ModelConfig{{ID: "fixture", MaxTokens: 200000, MaxOutputTokens: 4096}}}}
	// Zero caps disable budget limits, not configured usage persistence.
	cfg.Cost = config.CostConfig{}
	commands := map[string]func(*cobra.Command, []string) error{
		"matrix": runMatrix, "signal": runSignal, "discord": runDiscord,
		"mattermost": runMattermost, "telegram": runTelegram,
	}
	run, ok := commands[name]
	require.True(t, ok)
	command := &cobra.Command{}
	command.SetContext(context.Background())
	err := run(command, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), os.Getenv("OVERKILL_GATEWAY_COST_EXPECT"))
	if observerDSN := os.Getenv("OVERKILL_GATEWAY_COST_OBSERVER"); observerDSN != "" {
		observer, err := sql.Open("postgres", observerDSN)
		require.NoError(t, err)
		defer observer.Close()
		// Check while this process is still alive, so process exit cannot hide
		// a leaked pool on the gateway's failure path.
		require.Eventually(t, func() bool {
			var count int
			err := observer.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE application_name=$1`, os.Getenv("OVERKILL_GATEWAY_COST_APPLICATION")).Scan(&count)
			return err == nil && count == 0
		}, 3*time.Second, 20*time.Millisecond, "gateway failure must close its database pool")
	}
}

func TestGatewayCostStartup(t *testing.T) {
	for _, gateway := range []struct{ name, credentials string }{
		{"matrix", "MATRIX_ACCESS_TOKEN or MATRIX_PASSWORD not set"},
		{"signal", "SIGNAL_ACCOUNT not set"},
		{"discord", "DISCORD_BOT_TOKEN not set"},
		{"mattermost", "MATTERMOST_SERVER_URL not set"},
		{"telegram", "TELEGRAM_BOT_TOKEN not set"},
	} {
		t.Run(gateway.name, func(t *testing.T) {
			for _, mode := range []string{"broken schema", "healthy schema", "no database", "invalid database URL"} {
				t.Run(mode, func(t *testing.T) {
					dsn, observerDSN, application := "", "", ""
					expected := gateway.credentials
					if mode == "broken schema" || mode == "healthy schema" {
						database, _ := testpg.Open(t)
						var schema string
						require.NoError(t, database.QueryRow(`SELECT current_schema()`).Scan(&schema))
						application = "gateway_cost_" + schema
						dsn = gatewayCostTestDSN(t, schema, application)
						observerDSN = gatewayCostTestDSN(t, schema, application+"_observer")
						if mode == "broken schema" {
							_, err := database.Exec(`CREATE TABLE cost_records (id TEXT PRIMARY KEY)`)
							require.NoError(t, err)
							expected = gateway.name + ": initialize cost tracker: cost: migrate: input token width:"
						}
					}
					if mode == "invalid database URL" {
						dsn = "postgres://%zz"
						expected = gateway.name + ": initialize cost tracker: cost: migrate: parse"
					}
					ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGatewayCostStartupHelper$")
					command.Dir = t.TempDir()
					command.Env = append(os.Environ(),
						"OVERKILL_GATEWAY_COST_HELPER="+gateway.name, "OVERKILL_GATEWAY_COST_DSN="+dsn,
						"OVERKILL_GATEWAY_COST_EXPECT="+expected, "OVERKILL_GATEWAY_COST_OBSERVER="+observerDSN,
						"OVERKILL_GATEWAY_COST_APPLICATION="+application, "OVERKILL_HOME="+t.TempDir(),
						"MATRIX_ACCESS_TOKEN=", "MATRIX_PASSWORD=", "SIGNAL_ACCOUNT=", "DISCORD_BOT_TOKEN=",
						"MATTERMOST_SERVER_URL=", "MATTERMOST_BOT_TOKEN=", "TELEGRAM_BOT_TOKEN=",
					)
					output, err := command.CombinedOutput()
					require.NoError(t, err, string(output))
				})
			}
		})
	}
}

func gatewayCostTestDSN(t *testing.T, schema, application string) string {
	t.Helper()
	base := os.Getenv("PG_TEST_URL")
	if base == "" {
		base = os.Getenv("DATABASE_URL")
	}
	if strings.HasPrefix(base, "postgres://") || strings.HasPrefix(base, "postgresql://") {
		parsed, err := url.Parse(base)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", schema)
		query.Set("application_name", application)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return base + " search_path=" + schema + " application_name=" + application
}
