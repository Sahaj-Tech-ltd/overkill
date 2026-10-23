package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

func TestDesktopCredentialRemovedBeforeAnyChildStartup(t *testing.T) {
	t.Setenv("OVERKILL_DESKTOP_PROVIDER_KEY", "desktop-test-secret")
	loaded := config.Default()
	loaded.Providers = []config.ProviderConfig{{Name: "desktop", Type: "openai", APIKey: "desktop-test-secret"}}
	err := runDesktopServer(context.Background(), loaded, desktopOptions{}, io.NopCloser(strings.NewReader("")), io.Discard)
	require.ErrorContains(t, err, "absolute paths")
	_, present := os.LookupEnv("OVERKILL_DESKTOP_PROVIDER_KEY")
	require.False(t, present)
	require.Equal(t, "desktop-test-secret", loaded.Providers[0].APIKey)
}
