package session

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
)

type ExportRitual struct {
	store      Store
	exportPath string
}

func NewExportRitual(store Store, exportPath string) *ExportRitual {
	return &ExportRitual{
		store:      store,
		exportPath: exportPath,
	}
}

func (er *ExportRitual) Export(ctx context.Context) error {
	if err := os.MkdirAll(dirOf(er.exportPath), 0o750); err != nil {
		return fmt.Errorf("export: creating dirs: %w", err)
	}

	sessions, err := er.store.List(ctx, ListOptions{})
	if err != nil {
		return fmt.Errorf("export: listing sessions: %w", err)
	}

	var b strings.Builder
	b.WriteString("# Overkill Memory Export\n")
	b.WriteString(fmt.Sprintf("Generated: %s\n\n", time.Now().UTC().Format(time.RFC3339)))

	if len(sessions) == 0 {
		b.WriteString("No sessions found.\n")
	} else {
		b.WriteString(fmt.Sprintf("Total sessions: %d\n\n", len(sessions)))
		b.WriteString("---\n\n")

		for _, s := range sessions {
			b.WriteString(fmt.Sprintf("## %s\n\n", titleOr(s.Title, "Untitled")))
			b.WriteString(fmt.Sprintf("- **ID**: %s\n", s.ID))
			b.WriteString(fmt.Sprintf("- **Folder**: %s\n", s.Folder))
			b.WriteString(fmt.Sprintf("- **Model**: %s\n", modelOr(s.Model, "unknown")))
			b.WriteString(fmt.Sprintf("- **Provider**: %s\n", providerOr(s.Provider, "unknown")))
			b.WriteString(fmt.Sprintf("- **Turns**: %d\n", s.TurnCount))
			b.WriteString(fmt.Sprintf("- **Tokens**: %d\n", s.TokenCount))
			b.WriteString(fmt.Sprintf("- **Cost**: $%.4f\n", s.CostUSD))
			b.WriteString(fmt.Sprintf("- **Status**: %s\n", s.Status))
			b.WriteString(fmt.Sprintf("- **Created**: %s\n", s.CreatedAt.Format(time.RFC3339)))
			b.WriteString(fmt.Sprintf("- **Updated**: %s\n", s.UpdatedAt.Format(time.RFC3339)))

			if s.ParentID != "" {
				b.WriteString(fmt.Sprintf("- **Parent**: %s\n", s.ParentID))
			}

			b.WriteString("\n---\n\n")
		}
	}

	if err := atomicfile.WriteFile(er.exportPath, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("export: writing file: %w", err)
	}

	return nil
}

func dirOf(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return "."
	}
	return path[:idx]
}

func titleOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func modelOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func providerOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
