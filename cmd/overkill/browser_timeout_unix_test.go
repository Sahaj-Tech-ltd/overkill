//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/require"
)

func TestSessionRuntimeBrowserTimeoutReachesActualLaunch(t *testing.T) {
	chrome := filepath.Join(t.TempDir(), "controlled-chrome")
	require.NoError(t, os.WriteFile(chrome, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700))
	conf := config.Default()
	conf.Browser.Enabled = true
	conf.Browser.ChromePath = chrome
	conf.Browser.Timeout = 100 * time.Millisecond
	services, err := newRuntimeServices(conf, nil, t.TempDir())
	require.NoError(t, err)
	runtime, err := services.BuildSession(t.Context(), session.NewSession(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(runtime.Cleanup)
	open, err := runtime.Tools.Get("browser_open")
	require.NoError(t, err)
	started := time.Now()
	_, err = open.Execute(t.Context(), []byte(`{"url":"about:blank"}`))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 2*time.Second, "configured timeout must reach the browser bound by the shared CLI/API runtime factory")
}
