package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func enablementFixture() *Skill {
	return &Skill{Name: "test-skill", Description: "Test a real skill enablement preference", Instructions: "Always use the fixture skill instruction for this test.", Enabled: true, Triggers: []string{"fixture"}}
}

func TestEnablementPersistsWithoutEditingSkillSource(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	reg, err := NewPersistentRegistry(dir)
	require.NoError(t, err)
	require.NoError(t, reg.Register(enablementFixture()))
	require.NoError(t, reg.Disable("test-skill"))
	require.Empty(t, reg.Match("fixture"))
	reopened, err := NewPersistentRegistry(dir)
	require.NoError(t, err)
	require.NoError(t, reopened.Register(enablementFixture()))
	skill, ok := reopened.Get("test-skill")
	require.True(t, ok)
	require.False(t, skill.Enabled)
	require.NoError(t, reopened.Enable("test-skill"))
	require.Len(t, reopened.Match("fixture"), 1)
}

func TestEnablementWriteFailureKeepsExecutingState(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "state")
	reg, err := NewPersistentRegistry(dir)
	require.NoError(t, err)
	require.NoError(t, reg.Register(enablementFixture()))
	require.NoError(t, os.WriteFile(dir, nil, 0600))
	require.Error(t, reg.Disable("test-skill"))
	skill, _ := reg.Get("test-skill")
	require.True(t, skill.Enabled)
	require.Len(t, reg.Match("fixture"), 1)
}

func TestEnablementExplicitStateIsIdempotentAndRejectsUnknown(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.Register(enablementFixture()))
	enabled := false
	for i := 0; i < 2; i++ {
		got, err := reg.UpdateEnabled("test-skill", &enabled)
		require.NoError(t, err)
		require.False(t, got)
	}
	_, err := reg.UpdateEnabled("unknown", &enabled)
	require.Error(t, err)
}

func TestEnablementRejectsCorruptSettings(t *testing.T) {
	for _, value := range []string{"null", "{}", "\"false\"", "invalid"} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "test-skill.json"), []byte(value), 0600))
			reg, err := NewPersistentRegistry(dir)
			require.Error(t, err)
			require.Nil(t, reg)
		})
	}
}
