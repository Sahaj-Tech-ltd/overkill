//go:build windows

// Test launcher: run the unchanged installed desktop without administrator
// membership. PostgreSQL correctly refuses the elevated GitHub runner token.
// https://learn.microsoft.com/windows/win32/secauthz/restricted-tokens
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
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
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	if os.Args[1] == "--restricted-child" {
		admin, err := windows.Token(0).IsMember(admins)
		if err != nil {
			return err
		}
		if admin {
			return fmt.Errorf("acceptance must not run as administrator")
		}
		if len(os.Args) < 3 {
			return fmt.Errorf("missing acceptance command")
		}
		fmt.Println("Native acceptance token verified: administrator membership disabled; maximum privileges removed")
		cmd := exec.Command(os.Args[2], os.Args[3:]...)
		cmd.Env = append(os.Environ(), "OVERKILL_ACCEPTANCE_TOKEN=restricted-native-user")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	var source windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY, &source); err != nil {
		return err
	}
	defer source.Close()
	powerUsers, err := windows.CreateWellKnownSid(windows.WinBuiltinPowerUsersSid)
	if err != nil {
		return err
	}
	disabled := []windows.SIDAndAttributes{{Sid: admins}, {Sid: powerUsers}}
	var restricted windows.Token
	create := windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")
	const disableMaximumPrivileges = 1
	ok, _, callErr := create.Call(uintptr(source), disableMaximumPrivileges, uintptr(len(disabled)), uintptr(unsafe.Pointer(&disabled[0])), 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	if ok == 0 {
		return fmt.Errorf("create restricted acceptance token: %w", callErr)
	}
	defer restricted.Close()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, append([]string{"--restricted-child"}, os.Args[1:]...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(restricted)}
	cmd.Env = os.Environ()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
