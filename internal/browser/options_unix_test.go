//go:build unix

package browser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestActualBrowserLaunchFlagsDefaultHeadlessAndExplicitVisible(t *testing.T) {
	for _, tc := range []struct {
		name     string
		options  Options
		headless bool
	}{
		{"default", Options{}, true},
		{"explicit-headless", Options{Headless: true}, true},
		{"explicit-visible", Options{Visible: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			argsFile := filepath.Join(dir, "args")
			chrome := filepath.Join(dir, "controlled-chrome")
			// Exercise chromedp's real executable invocation without requiring a local
			// browser install, opening a window or performing any network navigation.
			require.NoError(t, os.WriteFile(chrome, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OVERKILL_TEST_BROWSER_ARGS\"\nexit 1\n"), 0o700))
			t.Setenv("OVERKILL_TEST_BROWSER_ARGS", argsFile)
			options := tc.options
			options.ChromePath = chrome
			manager := NewManager(options)
			defer manager.Close()
			require.Equal(t, tc.headless, manager.Status().Headless)
			// This test checks launch arguments, not a launch-time SLA. Leave
			// scheduling room for process creation under the full race suite;
			// configured deadline/cancellation behavior has separate fixtures.
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			_, launchErr := manager.Get(ctx)
			require.Error(t, launchErr, "controlled executable deliberately fails after recording arguments")
			bytes, err := os.ReadFile(argsFile)
			require.NoError(t, err, "controlled executable did not record arguments; launch error: %v", launchErr)
			flags := strings.Split(strings.TrimSpace(string(bytes)), "\n")
			if tc.headless {
				require.Contains(t, flags, "--headless")
			} else {
				require.NotContains(t, flags, "--headless")
			}
			require.False(t, manager.IsActive())
		})
	}
}
