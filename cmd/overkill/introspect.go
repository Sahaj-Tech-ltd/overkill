package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/introspection"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

var (
	introspectAll    bool
	introspectDryRun bool
)

var introspectCmd = &cobra.Command{
	Use:   "introspect",
	Short: "Generate introspection files (CODEBASE.md, MODEL_CARD.md, etc.)",
	Long: `introspect uses the current provider's cheapest model to generate
self-knowledge files in ~/.overkill/introspection/. These files describe
Overkill's codebase, model capabilities, known issues, and architecture.

Files generated:
  CODEBASE.md      — project structure and key packages
  MODEL_CARD.md    — current model capabilities, pricing, context window
  KNOWN_ISSUES.md  — common bugs, gotchas, workarounds
  ARCHITECTURE.md  — key architectural decisions and trade-offs

By default, only generates files that don't exist yet. Use --all to
regenerate everything.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		homeDir, err := config.ConfigDir()
		if err != nil {
			return fmt.Errorf("cannot resolve config dir: %w", err)
		}

		introDir := filepath.Join(homeDir, "introspection")
		if err := os.MkdirAll(introDir, 0o755); err != nil {
			return fmt.Errorf("create introspection dir: %w", err)
		}

		// Decide which file types to generate
		var toGenerate []introspection.FileType
		allTypes := []introspection.FileType{
			introspection.FileCodebase,
			introspection.FileModelCard,
			introspection.FileKnownIssues,
			introspection.FileArchitecture,
		}

		if introspectAll {
			toGenerate = allTypes
		} else {
			for _, ft := range allTypes {
				path := filepath.Join(introDir, string(ft))
				if _, err := os.Stat(path); os.IsNotExist(err) {
					toGenerate = append(toGenerate, ft)
				}
			}
		}

		if len(toGenerate) == 0 {
			fmt.Println("All introspection files already exist. Use --all to regenerate.")
			return nil
		}

		if introspectDryRun {
			fmt.Println("Would generate:")
			for _, ft := range toGenerate {
				fmt.Printf("  - %s\n", ft)
			}
			return nil
		}

		// Resolve a provider + model for generation.
		// Use the cheapest model available (introspection is a background task).
		pc, _ := resolveProvider()
		if pc == nil {
			return fmt.Errorf("no provider configured — set up a provider in config.toml first")
		}
		apiKey := pc.APIKey
		if apiKey == "" {
			apiKey = os.Getenv(providerEnvVar(pc.Name))
		}
		p, err := providers.NewProvider(providers.FactoryConfig{
			Name:    pc.Name,
			Type:    pc.Type,
			APIKey:  apiKey,
			BaseURL: pc.BaseURL,
		})
		if err != nil {
			return fmt.Errorf("create provider: %w", err)
		}

		cheapestModel := pickCheapestModel(p)
		if cheapestModel == "" {
			cheapestModel = "gpt-4o-mini"
		}

		inspector := introspection.NewIntrospector(introDir, p, cheapestModel)

		fmt.Printf("Generating introspection files using %s/%s...\n", pc.Name, cheapestModel)

		ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
		defer cancel()

		for i, ft := range toGenerate {
			fmt.Printf("  [%d/%d] %-20s ", i+1, len(toGenerate), ft)
			start := time.Now()

			f, err := inspector.Generate(ctx, ft)
			if err != nil {
				fmt.Printf("✗ %v\n", err)
				continue
			}

			elapsed := time.Since(start).Round(time.Millisecond)
			lines := strings.Count(f.Content, "\n") + 1
			fmt.Printf("✓ %d lines (%s)\n", lines, elapsed)
		}

		fmt.Printf("\nDone. Files in %s/\n", introDir)
		// List what we have now
		for _, ft := range allTypes {
			path := filepath.Join(introDir, string(ft))
			if fi, err := os.Stat(path); err == nil {
				fmt.Printf("  %-20s %s\n", ft, humanizeSize(fi.Size()))
			}
		}

		return nil
	},
}

func humanizeSize(size int64) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

func init() {
	introspectCmd.Flags().BoolVar(&introspectAll, "all", false, "regenerate all introspection files (not just missing ones)")
	introspectCmd.Flags().BoolVar(&introspectDryRun, "dry-run", false, "show what would be generated without making API calls")
	rootCmd.AddCommand(introspectCmd)
}
