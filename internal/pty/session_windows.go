//go:build windows

package pty

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	consoleDLL       = windows.NewLazySystemDLL("kernel32.dll")
	createConsole    = consoleDLL.NewProc("CreatePseudoConsole")
	resizeConsole    = consoleDLL.NewProc("ResizePseudoConsole")
	closeConsole     = consoleDLL.NewProc("ClosePseudoConsole")
	updateAttributes = consoleDLL.NewProc("UpdateProcThreadAttribute")
)

// Supported probes exports instead of assuming every Windows host has ConPTY.
func Supported() bool {
	return createConsole.Find() == nil && resizeConsole.Find() == nil && closeConsole.Find() == nil
}

// PowerShell ships with the supported Windows host. EncodedCommand preserves
// the exact script through CreateProcess quoting; profiles are never loaded.
// There is no ExecutionPolicy override and interactive console input remains
// available to hosted commands.
func ShellCommand(command string) *exec.Cmd {
	encoded := utf16.Encode([]rune(command))
	data := make([]byte, len(encoded)*2)
	for i, value := range encoded {
		binary.LittleEndian.PutUint16(data[i*2:], value)
	}
	return exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoLogo", "-NoProfile", "-EncodedCommand", base64.StdEncoding.EncodeToString(data))
}

func ShellName() string { return "Windows PowerShell" }

// Session uses ConPTY for I/O and an unnamed kill-on-close Job Object for
// process ownership. The root is assigned while suspended, before user code
// can spawn a descendant. This is lifetime control, not a security sandbox.
// Start consumes Cmd's executable/arguments/environment/directory only. The
// owner must select its cancellation/deadline and call Close, as the tools do.
type Session struct {
	mu                sync.Mutex
	console           windows.Handle
	input, output     *os.File
	job               windows.Handle
	done              chan struct{}
	started, closed   bool
	exit              int
	waitErr, closeErr error
	close             sync.Once
}

func New() *Session { return &Session{} }

func environmentBlock(env []string) ([]uint16, error) {
	if env == nil {
		env = os.Environ()
	}
	// Match Windows' case-insensitive last-value precedence without mutating
	// a caller-owned environment. Preserve drive-current-directory entries.
	values := make(map[string]string, len(env))
	for _, entry := range env {
		if len(entry) < 2 {
			return nil, fmt.Errorf("pty: malformed environment entry")
		}
		if strings.ContainsRune(entry, 0) {
			return nil, fmt.Errorf("pty: environment contains NUL")
		}
		i := strings.IndexByte(entry[1:], '=') + 1
		if i <= 0 {
			return nil, fmt.Errorf("pty: malformed environment entry")
		}
		values[strings.ToUpper(entry[:i])] = entry
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var block []uint16
	for _, key := range keys {
		block = append(block, utf16.Encode([]rune(values[key]))...)
		block = append(block, 0)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func (s *Session) Start(cmd *exec.Cmd) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("pty: session already closed")
	}
	if s.started {
		return errors.New("pty: session already started")
	}
	if !Supported() {
		return errors.New("pty: this Windows host does not expose ConPTY")
	}
	if cmd == nil || cmd.Path == "" {
		return errors.New("pty: executable is required")
	}
	if cmd.Err != nil {
		return cmd.Err
	}
	if cmd.SysProcAttr != nil || cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil || cmd.Process != nil {
		return errors.New("pty: incompatible process/stdio attributes; ConPTY owns startup and I/O")
	}
	application, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return err
	}
	args := cmd.Args
	if len(args) == 0 {
		args = []string{cmd.Path}
	}
	command, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
	if err != nil {
		return err
	}
	var directory *uint16
	if cmd.Dir != "" {
		directory, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return err
		}
	}
	env, err := environmentBlock(cmd.Env)
	if err != nil {
		return err
	}
	var inRead, inWrite, outRead, outWrite, console, job windows.Handle
	var process windows.ProcessInformation
	started := false
	defer func() {
		if inRead != 0 {
			windows.CloseHandle(inRead)
		}
		if outWrite != 0 {
			windows.CloseHandle(outWrite)
		}
		if started {
			return
		}
		if process.Process != 0 {
			_ = windows.TerminateProcess(process.Process, 1)
			windows.WaitForSingleObject(process.Process, 2000)
			windows.CloseHandle(process.Process)
		}
		if process.Thread != 0 {
			windows.CloseHandle(process.Thread)
		}
		if job != 0 {
			windows.CloseHandle(job)
		}
		if inWrite != 0 {
			windows.CloseHandle(inWrite)
		}
		if outRead != 0 {
			windows.CloseHandle(outRead)
		}
		if console != 0 {
			_ = closePseudoConsole(console)
		}
	}()
	if err = windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return fmt.Errorf("pty: input pipe: %w", err)
	}
	if err = windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		return fmt.Errorf("pty: output pipe: %w", err)
	}
	if err = windows.CreatePseudoConsole(windows.Coord{X: 80, Y: 24}, inRead, outWrite, 0, &console); err != nil {
		return fmt.Errorf("pty: create console: %w", err)
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attributes.Delete()
	// This one attribute takes HPCON by value, not the address of a Go handle.
	// Pass the numeric handle directly rather than manufacturing a Go pointer.
	ok, _, callErr := updateAttributes.Call(uintptr(unsafe.Pointer(attributes.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(console), unsafe.Sizeof(console), 0, 0)
	if ok == 0 {
		return fmt.Errorf("pty: console startup attribute: %w", callErr)
	}
	job, err = windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return fmt.Errorf("pty: configure owned job: %w", err)
	}
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESHOWWINDOW
	startup.ShowWindow = windows.SW_HIDE
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err = windows.CreateProcess(application, command, nil, nil, false, flags, &env[0], directory, &startup.StartupInfo, &process); err != nil {
		return fmt.Errorf("pty: create process: %w", err)
	}
	if err = windows.AssignProcessToJobObject(job, process.Process); err != nil {
		return fmt.Errorf("pty: assign owned job: %w", err)
	}
	if _, err = windows.ResumeThread(process.Thread); err != nil {
		return fmt.Errorf("pty: resume process: %w", err)
	}
	windows.CloseHandle(process.Thread)
	process.Thread = 0
	s.console = console
	s.job = job
	s.input = os.NewFile(uintptr(inWrite), "conpty-input")
	s.output = os.NewFile(uintptr(outRead), "conpty-output")
	s.started = true
	s.done = make(chan struct{})
	started = true
	go s.wait(process.Process)
	return nil
}

func (s *Session) wait(process windows.Handle) {
	status, err := windows.WaitForSingleObject(process, windows.INFINITE)
	code := uint32(0)
	if err == nil && status != windows.WAIT_OBJECT_0 {
		err = fmt.Errorf("pty: unexpected wait result %d", status)
	}
	if err == nil {
		err = windows.GetExitCodeProcess(process, &code)
	}
	windows.CloseHandle(process)
	s.mu.Lock()
	s.exit = int(code)
	s.waitErr = err
	if err != nil {
		s.exit = -1
	}
	s.mu.Unlock()
	close(s.done)
}

func (s *Session) Read(buf []byte) (int, error) {
	s.mu.Lock()
	file := s.output
	s.mu.Unlock()
	if file == nil {
		return 0, io.EOF
	}
	return file.Read(buf)
}
func (s *Session) Write(buf []byte) (int, error) {
	s.mu.Lock()
	file := s.input
	s.mu.Unlock()
	if file == nil {
		return 0, errors.New("pty: not started or closed")
	}
	return file.Write(buf)
}
func (s *Session) Resize(rows, cols uint16) error {
	if rows == 0 || cols == 0 || rows > 32767 || cols > 32767 {
		return errors.New("pty: invalid console dimensions")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.console == 0 || s.closed {
		return errors.New("pty: not started or closed")
	}
	return windows.ResizePseudoConsole(s.console, windows.Coord{X: int16(cols), Y: int16(rows)})
}
func (s *Session) WaitExit() (int, error) {
	s.mu.Lock()
	started, done := s.started, s.done
	s.mu.Unlock()
	if !started {
		return -1, errors.New("pty: session was never started")
	}
	<-done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exit, s.waitErr
}

// See Microsoft's ClosePseudoConsole guidance: close or continuously drain
// output before closing ConPTY; older Windows can otherwise wait forever.
func closePseudoConsole(console windows.Handle) error {
	done := make(chan struct{})
	go func() { windows.ClosePseudoConsole(console); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("pty: console closure is unconfirmed")
	}
}

type jobAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

func retireJob(job windows.Handle) error {
	if err := windows.TerminateJobObject(job, 1); err != nil {
		return fmt.Errorf("pty: terminate owned job: %w", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var info jobAccounting
		if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
			return fmt.Errorf("pty: verify owned job stopped: %w", err)
		}
		if info.ActiveProcesses == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("pty: owned process termination is unconfirmed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *Session) Close() error {
	s.close.Do(func() {
		s.mu.Lock()
		s.closed = true
		input, output, console, job := s.input, s.output, s.console, s.job
		s.input = nil
		s.output = nil
		s.console = 0
		s.job = 0
		s.mu.Unlock()
		var err error
		if job != 0 {
			err = retireJob(job)
			err = errors.Join(err, windows.CloseHandle(job))
		}
		if input != nil {
			err = errors.Join(err, input.Close())
		}
		if output != nil {
			err = errors.Join(err, output.Close())
		}
		if console != 0 {
			err = errors.Join(err, closePseudoConsole(console))
		}
		s.mu.Lock()
		s.closeErr = err
		s.mu.Unlock()
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeErr
}
