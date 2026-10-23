package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/require"
)

func TestSandboxRuntimeAssemblyRejectsBeforeCreatingStores(t *testing.T) {
	cfg := config.Default()
	cfg.Security.SandboxEnabled = true
	home := filepath.Join(t.TempDir(), "uncreated")
	runtime, err := newRuntimeServices(cfg, nil, home)
	require.Nil(t, runtime)
	require.ErrorIs(t, err, config.ErrSandboxUnsupported)
	require.True(t, cfg.Security.SandboxEnabled)
	_, err = os.Stat(home)
	require.True(t, os.IsNotExist(err))
}

func TestSandboxSessionAssemblyRejectsInMemoryUnsupportedRequest(t *testing.T) {
	cfg := config.Default()
	runtime, err := newRuntimeServices(cfg, nil, t.TempDir())
	require.NoError(t, err)
	cfg.Security.SandboxEnabled = true
	built, err := runtime.BuildSession(context.Background(), session.NewSession(t.TempDir()))
	require.Nil(t, built)
	require.ErrorIs(t, err, config.ErrSandboxUnsupported)
	require.True(t, cfg.Security.SandboxEnabled)
	_, bound, err := runtime.bindSession(context.Background(), session.NewSession(t.TempDir()))
	require.Nil(t, bound)
	require.ErrorIs(t, err, config.ErrSandboxUnsupported)
}
