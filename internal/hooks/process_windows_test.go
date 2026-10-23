//go:build windows

package hooks

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func setTestHookDACL(t *testing.T, path, rule string) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString(rule)
	require.NoError(t, err)
	acl, _, err := descriptor.DACL()
	require.NoError(t, err)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	require.NoError(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, user.User.Sid, nil, acl, nil))
}

func TestWindowsHookRequiresSafeOwnershipAndDACL(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	sid := user.User.Sid.String()
	path := filepath.Join(t.TempDir(), "hook.ps1")
	require.NoError(t, os.WriteFile(path, []byte("exit 0"), 0600))
	safe := "D:P(A;;FA;;;" + sid + ")(A;;FA;;;SY)(A;;FA;;;BA)"
	setTestHookDACL(t, path, safe)
	require.True(t, isExecutable(path))
	setTestHookDACL(t, path, safe+"(A;;FR;;;WD)")
	require.True(t, isExecutable(path), "world read is safe")
	setTestHookDACL(t, path, safe+"(A;;FW;;;WD)")
	require.False(t, isExecutable(path), "world write is unsafe")
	setTestHookDACL(t, path, safe+"(A;;FW;;;BU)")
	require.False(t, isExecutable(path), "other users must not modify hooks")
	require.False(t, isExecutable(filepath.Dir(path)))
	require.False(t, isExecutable(filepath.Join(t.TempDir(), "missing.exe")))
}

func TestWindowsPowerShellHookReceivesEvent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "hooks")
	require.NoError(t, os.Mkdir(dir, 0700))
	path := filepath.Join(dir, "hook.ps1")
	output := filepath.Join(dir, "event.json")
	body := "$event = [Console]::In.ReadToEnd(); [System.IO.File]::WriteAllText('" + strings.ReplaceAll(output, "'", "''") + "', $event)"
	require.NoError(t, os.WriteFile(path, []byte(body), 0600))
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	for _, owned := range []string{root, dir, path} {
		setTestHookDACL(t, owned, "D:P(A;;FA;;;"+user.User.Sid.String()+")(A;;FA;;;SY)(A;;FA;;;BA)")
	}
	hook := makeShellHook(path)
	_, err = hook(context.Background(), Event{ToolName: "fixture"})
	require.NoError(t, err)
	raw, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Contains(t, string(raw), "fixture")
}

func TestWindowsHookCancellationTerminatesDescendants(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executable, err := os.Executable()
	require.NoError(t, err)
	marker := filepath.Join(t.TempDir(), "child.pid")
	cmd, err := newHookCommand(ctx, executable)
	require.NoError(t, err)
	cmd.Args = append(cmd.Args, "-test.run=^TestWindowsHookProcessHelper$")
	cmd.Env = append(os.Environ(), "OVERKILL_WINDOWS_HOOK_HELPER=root", "OVERKILL_WINDOWS_HOOK_PID="+marker)
	done := make(chan error, 1)
	go func() { done <- runHookCommand(ctx, cmd) }()
	var pid int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, readErr := os.ReadFile(marker)
		if readErr == nil {
			pid, err = strconv.Atoi(string(raw))
			require.NoError(t, err)
			break
		}
		select {
		case err := <-done:
			t.Fatalf("hook exited before child was ready: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	require.NotZero(t, pid, "descendant never started")
	child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	require.NoError(t, err)
	defer windows.CloseHandle(child)
	cancel()
	select {
	case err := <-done:
		require.True(t, errors.Is(err, context.Canceled), "%v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("hook failed to cancel")
	}
	status, err := windows.WaitForSingleObject(child, 5000)
	require.NoError(t, err)
	require.Equal(t, uint32(windows.WAIT_OBJECT_0), status)
}

func TestWindowsHookProcessHelper(t *testing.T) {
	switch os.Getenv("OVERKILL_WINDOWS_HOOK_HELPER") {
	case "root":
		executable, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(executable, "-test.run=^TestWindowsHookProcessHelper$")
		env := []string{}
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "OVERKILL_WINDOWS_HOOK_HELPER=") {
				env = append(env, entry)
			}
		}
		cmd.Env = append(env, "OVERKILL_WINDOWS_HOOK_HELPER=leaf")
		require.NoError(t, cmd.Start())
		require.NoError(t, os.WriteFile(os.Getenv("OVERKILL_WINDOWS_HOOK_PID"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600))
		_ = cmd.Wait()
	case "leaf":
		time.Sleep(30 * time.Second)
	}
}
