//go:build windows

// Disposable native acceptance launcher. A real ordinary account is required:
// restricting the elevated runner token breaks Chromium's own sandbox broker.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if e, ok := err.(*exec.ExitError); ok {
			os.Exit(e.ExitCode())
		}
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: windows-user-runner command [args]")
	}
	if os.Args[1] == "--ordinary-child" {
		return child()
	}
	user, password := os.Getenv("OVERKILL_ACCEPTANCE_USER"), os.Getenv("OVERKILL_ACCEPTANCE_PASSWORD")
	if user == "" || password == "" {
		return fmt.Errorf("disposable ordinary account credentials required")
	}
	sid, _, _, err := windows.LookupSID("", user)
	if err != nil {
		return err
	}
	restore, err := grantDesktop(sid)
	if err != nil {
		return err
	}
	defer restore()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	args := append([]string{self, "--ordinary-child", os.Getenv("OVERKILL_ACCEPTANCE_LOG"), user}, os.Args[1:]...)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}
	command, err := windows.UTF16FromString(strings.Join(quoted, " "))
	if err != nil {
		return err
	}
	up, _ := windows.UTF16PtrFromString(user)
	domain, _ := windows.UTF16PtrFromString(".")
	pw, _ := windows.UTF16PtrFromString(password)
	app, _ := windows.UTF16PtrFromString(self)
	dir, _ := windows.UTF16PtrFromString(cwd)
	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	startup := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{})), Desktop: desktop}
	var info windows.ProcessInformation
	proc := windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateProcessWithLogonW")
	// LOGON_WITH_PROFILE loads the actual ordinary user's HKCU/DPAPI profile.
	environment := os.Environ()
	sort.Slice(environment, func(i, j int) bool { return strings.ToUpper(environment[i]) < strings.ToUpper(environment[j]) })
	block := utf16.Encode([]rune(strings.Join(environment, "\x00") + "\x00\x00"))
	ok, _, callErr := proc.Call(uintptr(unsafe.Pointer(up)), uintptr(unsafe.Pointer(domain)), uintptr(unsafe.Pointer(pw)), 1, uintptr(unsafe.Pointer(app)), uintptr(unsafe.Pointer(&command[0])), windows.CREATE_UNICODE_ENVIRONMENT, uintptr(unsafe.Pointer(&block[0])), uintptr(unsafe.Pointer(dir)), uintptr(unsafe.Pointer(&startup)), uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return fmt.Errorf("ordinary account logon launch: %w", callErr)
	}
	defer windows.CloseHandle(info.Process)
	defer windows.CloseHandle(info.Thread)
	state, err := windows.WaitForSingleObject(info.Process, uint32((8*time.Minute)/time.Millisecond))
	if err != nil {
		return err
	}
	if state == uint32(windows.WAIT_TIMEOUT) {
		_ = exec.Command("taskkill.exe", "/PID", fmt.Sprint(info.ProcessId), "/T", "/F").Run()
		return fmt.Errorf("ordinary-user acceptance exceeded eight-minute bound; owned process tree terminated")
	}
	if logFile := os.Getenv("OVERKILL_ACCEPTANCE_LOG"); logFile != "" {
		if bytes, e := os.ReadFile(logFile); e == nil {
			_, _ = os.Stdout.Write(bytes)
		}
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.Process, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("ordinary-user acceptance exited %d", code)
	}
	return nil
}
func child() (resultErr error) {
	if len(os.Args) < 5 {
		return fmt.Errorf("missing ordinary acceptance command")
	}
	// CreateProcessWithLogonW has no inherited-handle option. Persist the child
	// transcript in the shared output tree; the parent emits it after bounded wait.
	logFile, err := os.OpenFile(os.Args[2], os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	phase := "open ordinary process token"
	defer func() {
		if resultErr != nil {
			fmt.Fprintln(logFile, phase+":", resultErr)
		}
		_ = logFile.Close()
	}()
	os.Stdout, os.Stderr = logFile, logFile
	os.Unsetenv("OVERKILL_ACCEPTANCE_PASSWORD")
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer token.Close()
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	admin, err := windows.Token(0).IsMember(admins)
	if err != nil {
		return err
	}
	phase = "query ordinary token restrictions"
	var restrictions, returned uint32
	if err := windows.GetTokenInformation(token, windows.TokenHasRestrictions, (*byte)(unsafe.Pointer(&restrictions)), uint32(unsafe.Sizeof(restrictions)), &returned); err != nil {
		return err
	}
	restricted := restrictions != 0
	if admin || restricted {
		return fmt.Errorf("acceptance requires an ordinary non-admin, non-restricted user token")
	}
	current, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	expected, _, _, err := windows.LookupSID("", os.Args[3])
	if err != nil {
		return err
	}
	if !windows.EqualSid(current.User.Sid, expected) {
		return fmt.Errorf("acceptance account SID mismatch")
	}
	phase = "resolve loaded ordinary profile"
	home, err := token.GetUserProfileDirectory()
	if err != nil {
		return err
	}
	// Expandable default-profile registry paths use USERPROFILE. Do not let
	// the elevated caller's inherited environment resolve them to its profile.
	if err := os.Setenv("USERPROFILE", home); err != nil {
		return err
	}
	if err := os.Setenv("HOME", home); err != nil {
		return err
	}
	phase = "resolve ordinary LocalAppData"
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_CREATE)
	if err != nil {
		return err
	}
	phase = "resolve ordinary roaming AppData"
	roaming, err := windows.KnownFolderPath(windows.FOLDERID_RoamingAppData, windows.KF_FLAG_CREATE)
	if err != nil {
		return err
	}
	phase = "create ordinary temporary directory"
	temporary := filepath.Join(local, "Temp")
	if err := os.MkdirAll(temporary, 0700); err != nil {
		return err
	}
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "LOCALAPPDATA": local, "APPDATA": roaming, "TEMP": temporary, "TMP": temporary, "USERNAME": os.Args[3], "OVERKILL_ACCEPTANCE_TOKEN": "ordinary-local-user"} {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	phase = "probe Electron named mutex permissions"
	mutexName, _ := windows.UTF16PtrFromString(`Local\OverkillProcessSingletonStartup`)
	mutex, mutexErr := windows.CreateMutex(nil, false, mutexName)
	if mutexErr != nil && mutexErr != windows.ERROR_ALREADY_EXISTS {
		return fmt.Errorf("exact Electron startup mutex: %w", mutexErr)
	}
	if mutex != 0 {
		_ = windows.CloseHandle(mutex)
	}
	fmt.Println("Ordinary user can create/open exact Electron startup mutex")
	fmt.Println("Native acceptance token verified: actual ordinary local account, administrator=false, restricted=false, own loaded profile")
	phase = "run Node acceptance command"
	cmd := exec.Command(os.Args[4], os.Args[5:]...)
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = logFile, logFile
	return cmd.Run()
}
func grantDesktop(sid *windows.SID) (func(), error) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	station, _, e := user32.NewProc("GetProcessWindowStation").Call()
	if station == 0 {
		return nil, e
	}
	desktop, _, e := user32.NewProc("GetThreadDesktop").Call(uintptr(windows.GetCurrentThreadId()))
	if desktop == 0 {
		return nil, e
	}
	var undo []func()
	restore := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}
	for i, raw := range []uintptr{station, desktop} {
		handle := windows.Handle(raw)
		sd, err := windows.GetSecurityInfo(handle, windows.SE_WINDOW_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			restore()
			return nil, err
		}
		old, _, err := sd.DACL()
		if err != nil {
			restore()
			return nil, err
		}
		mask := uint32(0x000f037f)
		if i == 1 {
			mask = 0x000f01ff
		}
		acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{AccessPermissions: windows.ACCESS_MASK(mask), AccessMode: windows.GRANT_ACCESS, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(sid)}}}, old)
		if err != nil {
			restore()
			return nil, err
		}
		if err := windows.SetSecurityInfo(handle, windows.SE_WINDOW_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			restore()
			return nil, err
		}
		undo = append(undo, func() {
			_ = windows.SetSecurityInfo(handle, windows.SE_WINDOW_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, old, nil)
			runtime.KeepAlive(sd)
		})
	}
	return restore, nil
}
