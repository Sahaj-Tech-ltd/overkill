# Platforms

Overkill runs on:

| Platform | Status | Install |
|---|---|---|
| **Linux** (x86_64, arm64) | ✅ Full | `install.sh` |
| **macOS** (Intel, Apple Silicon) | ✅ Full | `install.sh` |
| **Windows** (x86_64) | ✅ Full | `install.ps1` |
| **WSL2** | ✅ Full | `install.sh` (it's Linux) |

## Platform-specific notes

### Windows

- Config lives at `%LOCALAPPDATA%\overkill\` (not `~/.overkill`)
- Binary: `overkill.exe`
- TUI tested on Windows Terminal. Legacy `cmd.exe` and PowerShell 5 may have rendering quirks.
- `go install` works if Go is on PATH.

### WSL2

No special configuration needed. It's just Linux. The installer works directly.

### macOS

Both Intel and Apple Silicon binaries are available. The installer auto-detects.
