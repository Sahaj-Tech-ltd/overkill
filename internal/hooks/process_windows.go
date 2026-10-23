//go:build windows

package hooks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
	"golang.org/x/sys/windows"
)

// Windows hooks are native executables or PowerShell scripts. Unix shell
// scripts and batch command strings are not executable Windows hook formats.
func newHookCommand(_ context.Context, path string) (*exec.Cmd, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe":
		return exec.Command(path), nil
	case ".ps1":
		system, err := windows.GetSystemDirectory()
		if err != nil {
			return nil, err
		}
		return exec.Command(filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoLogo", "-NoProfile", "-NonInteractive", "-File", path), nil
	default:
		return nil, fmt.Errorf("unsupported Windows hook format: %s", filepath.Ext(path))
	}
}

func runHookCommand(ctx context.Context, cmd *exec.Cmd) error { return processrunner.Run(ctx, cmd) }

// Windows has no Unix executable bit. Require a recognized format, current
// user ownership and a DACL that prevents untrusted principals modifying it.
func isExecutable(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".exe" && ext != ".ps1" {
		return false
	}
	return isTrustedHookPath(path)
}

func isTrustedDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && isTrustedHookPath(path)
}

func isTrustedHookPath(path string) bool {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return false
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || !owner.Equals(user.User.Sid) {
		return false
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return false
	} // null DACL grants everyone full access
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	const writeMask = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil {
			return false
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			if uint32(ace.Mask)&writeMask == 0 {
				continue
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !sid.Equals(owner) && !sid.Equals(system) && !sid.Equals(admins) {
				return false
			}
		default:
			return false // unrecognized access rules cannot establish safety
		}
	}
	return true
}
