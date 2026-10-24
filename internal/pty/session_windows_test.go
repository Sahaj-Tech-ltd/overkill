//go:build windows

package pty

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// The subprocess calls the Windows console APIs; redirected stdin/stdout is
// insufficient for this fixture. It uses no shell or vendor credentials.
func TestWindowsConsoleHelper(t *testing.T) {
	mode := os.Getenv("OVERKILL_CONPTY_HELPER")
	if mode == "" {
		t.Skip("owned subprocess fixture")
	}
	if mode == "late-child" {
		time.Sleep(1200 * time.Millisecond)
		require.NoError(t, os.WriteFile(os.Getenv("OVERKILL_CONPTY_LATE_FILE"), []byte("escaped"), 0600))
		return
	}
	var consoleMode uint32
	require.NoError(t, windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &consoleMode), "stdin must be an actual console")
	if mode == "descendant" {
		executable, err := os.Executable()
		require.NoError(t, err)
		child := exec.Command(executable, "-test.run=^TestWindowsConsoleHelper$")
		child.Env = append(os.Environ(), "OVERKILL_CONPTY_HELPER=late-child")
		require.NoError(t, child.Start())
		fmt.Printf("CHILD_PID:%d\n", child.Process.Pid)
		require.NoError(t, child.Process.Release())
	}
	fmt.Println("NATIVE_CONSOLE_READY")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	require.NoError(t, err)
	var size windows.ConsoleScreenBufferInfo
	require.NoError(t, windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &size))
	fmt.Printf("NATIVE_SIZE:%dx%d\nNATIVE_ECHO:%s\nNATIVE_ENV:%s\n", size.Size.X, size.Size.Y, strings.TrimSpace(line), os.Getenv("OVERKILL_CONPTY_VALUE"))
}

type consoleCapture struct {
	mu   sync.Mutex
	text strings.Builder
	done chan struct{}
}

func captureConsole(s *Session) *consoleCapture {
	c := &consoleCapture{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		buf := make([]byte, 4096)
		for {
			n, err := s.Read(buf)
			if n > 0 {
				c.mu.Lock()
				c.text.Write(buf[:n])
				c.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return c
}
func (c *consoleCapture) output() string { c.mu.Lock(); defer c.mu.Unlock(); return c.text.String() }
func windowsHelper(t *testing.T, mode string) (*Session, *consoleCapture) {
	t.Helper()
	require.True(t, Supported(), "Windows native acceptance requires ConPTY")
	executable, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(executable, "-test.run=^TestWindowsConsoleHelper$")
	cmd.Env = append(os.Environ(), "OVERKILL_CONPTY_HELPER="+mode, "OVERKILL_CONPTY_VALUE=héllo 世界")
	s := New()
	require.NoError(t, s.Start(cmd))
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	c := captureConsole(s)
	require.Eventually(t, func() bool { return strings.Contains(c.output(), "NATIVE_CONSOLE_READY") }, 5*time.Second, 10*time.Millisecond, c.output())
	return s, c
}

func TestWindowsNativeConsoleInputUnicodeResizeAndExit(t *testing.T) {
	s, c := windowsHelper(t, "echo")
	require.NoError(t, s.Resize(33, 107))
	_, err := s.Write([]byte("héllo "))
	require.NoError(t, err)
	require.NotContains(t, c.output(), "NATIVE_ECHO:", "partial input must stay in the same live console")
	_, err = s.Write([]byte("世界\r"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return strings.Contains(c.output(), "NATIVE_ECHO:héllo 世界") }, 5*time.Second, 10*time.Millisecond)
	code, err := s.WaitExit()
	require.NoError(t, err)
	require.Zero(t, code)
	require.NoError(t, s.Close())
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		t.Fatal("console reader did not retire")
	}
	require.Contains(t, c.output(), "NATIVE_SIZE:107x33")
	require.Contains(t, c.output(), "NATIVE_ENV:héllo 世界")
}

func TestWindowsNativeCloseRetiresDescendantsAndRead(t *testing.T) {
	target := filepath.Join(t.TempDir(), "late-effect")
	t.Setenv("OVERKILL_CONPTY_LATE_FILE", target)
	s, c := windowsHelper(t, "descendant")
	text := c.output()
	start := strings.Index(text, "CHILD_PID:")
	require.NotEqual(t, -1, start)
	line := strings.SplitN(text[start+len("CHILD_PID:"):], "\n", 2)[0]
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	require.NoError(t, err)
	child, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	require.NoError(t, err)
	defer windows.CloseHandle(child)
	require.NoError(t, s.Close())
	status, err := windows.WaitForSingleObject(child, 3000)
	require.NoError(t, err)
	require.Equal(t, uint32(windows.WAIT_OBJECT_0), status, "the exact descendant process must stop")
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		t.Fatal("blocked reader survived terminal close")
	}
	time.Sleep(1300 * time.Millisecond)
	_, err = os.Stat(target)
	require.ErrorIs(t, err, os.ErrNotExist, "descendant must not make its delayed file effect")
}

func TestWindowsNativeRejectsInvalidStartupWithoutLaunching(t *testing.T) {
	s := New()
	require.Error(t, s.Start(nil))
	require.NoError(t, s.Close())
	require.Error(t, s.Start(ShellCommand("Write-Output 'not launched'")))
	cmd := ShellCommand("Write-Output 'not launched'")
	cmd.Env = []string{""}
	require.ErrorContains(t, New().Start(cmd), "malformed environment")
	cmd = ShellCommand("Write-Output 'not launched'")
	cmd.Env = []string{"BAD=NUL\x00VALUE"}
	require.ErrorContains(t, New().Start(cmd), "NUL")
}
