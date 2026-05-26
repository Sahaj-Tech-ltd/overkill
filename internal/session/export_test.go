package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExport_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewBadgerStore(dir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	sess := NewSession("/home/user/project")
	sess.Title = "Export Test"
	require.NoError(t, store.Create(ctx, sess))

	exportPath := filepath.Join(t.TempDir(), "export.md")
	er := NewExportRitual(store, exportPath)
	require.NoError(t, er.Export(ctx))

	_, err = os.Stat(exportPath)
	assert.NoError(t, err)
}

func TestExport_ContainsSessionInfo(t *testing.T) {
	dir := t.TempDir()
	store, err := NewBadgerStore(dir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	sess := NewSession("/home/user/myproject")
	sess.Title = "My Important Session"
	sess.Model = "claude-3.5"
	sess.Provider = "anthropic"
	sess.TurnCount = 12
	sess.CostUSD = 0.42
	require.NoError(t, store.Create(ctx, sess))

	exportPath := filepath.Join(t.TempDir(), "export.md")
	er := NewExportRitual(store, exportPath)
	require.NoError(t, er.Export(ctx))

	raw, err := os.ReadFile(exportPath)
	require.NoError(t, err)

	content := string(raw)
	assert.Contains(t, content, "# Overkill Memory Export")
	assert.Contains(t, content, "My Important Session")
	assert.Contains(t, content, "/home/user/myproject")
	assert.Contains(t, content, "claude-3.5")
	assert.Contains(t, content, "anthropic")
}

func TestExport_EmptyStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewBadgerStore(dir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	exportPath := filepath.Join(t.TempDir(), "export.md")
	er := NewExportRitual(store, exportPath)
	require.NoError(t, er.Export(ctx))

	raw, err := os.ReadFile(exportPath)
	require.NoError(t, err)

	content := string(raw)
	assert.Contains(t, content, "# Overkill Memory Export")
	assert.Contains(t, content, "No sessions found")
}

func TestExport_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	store, err := NewBadgerStore(dir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	sess := NewSession("/home/user/project")
	sess.Title = "Nested Export"
	require.NoError(t, store.Create(ctx, sess))

	base := t.TempDir()
	exportPath := filepath.Join(base, "a", "b", "c", "export.md")
	er := NewExportRitual(store, exportPath)
	require.NoError(t, er.Export(ctx))

	_, err = os.Stat(exportPath)
	assert.NoError(t, err)

	info, err := os.Stat(filepath.Join(base, "a", "b", "c"))
	assert.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestExport_MultipleSessions(t *testing.T) {
	dir := t.TempDir()
	store, err := NewBadgerStore(dir)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	for i := 0; i < 3; i++ {
		sess := NewSession("/home/user/project")
		sess.Title = "Session " + string(rune('A'+i))
		require.NoError(t, store.Create(ctx, sess))
	}

	exportPath := filepath.Join(t.TempDir(), "export.md")
	er := NewExportRitual(store, exportPath)
	require.NoError(t, er.Export(ctx))

	raw, err := os.ReadFile(exportPath)
	require.NoError(t, err)

	content := string(raw)
	assert.Contains(t, content, "Total sessions: 3")
	assert.Contains(t, content, "Session A")
	assert.Contains(t, content, "Session B")
	assert.Contains(t, content, "Session C")

	sections := strings.Count(content, "## ")
	assert.Equal(t, 3, sections)
}
