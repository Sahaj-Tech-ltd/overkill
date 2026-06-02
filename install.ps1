# install.ps1 — Windows PowerShell installer for overkill.
#
# Detects platform, prefers `go install` when Go is on PATH, otherwise
# downloads a pre-built binary, drops it in $env:LOCALAPPDATA\overkill\bin\,
# and bootstraps $env:LOCALAPPDATA\overkill\ for config.
#
# Env knobs:
#   $env:OVERKILL_BUILD_FROM_SOURCE = "1"   force `go install` path
#   $env:INSTALL_VERBOSE            = "1"   verbose output
#
# Usage:
#   irm https://raw.githubusercontent.com/Sahaj-Tech-ltd/overkill/main/install.ps1 | iex
#   .\install.ps1

param()

$ErrorActionPreference = "Stop"

$Repo = "Sahaj-Tech-ltd/overkill"
$GoPkg = "github.com/Sahaj-Tech-ltd/overkill/cmd/overkill"
$ReleaseBase = "https://github.com/$Repo/releases/latest/download"

$OverkillHome = "$env:LOCALAPPDATA\overkill"
$InstallDir = "$OverkillHome\bin"
$BinPath = "$InstallDir\overkill.exe"
$Verbose = [bool]($env:INSTALL_VERBOSE -eq "1")

# ---------------------------------------------------------------- helpers

function Say($msg) { Write-Host "[overkill] $msg" }
function VSay($msg) { if ($Verbose) { Write-Host "[overkill] $msg" } }
function Die($msg) { Write-Host "[overkill] error: $msg" -ForegroundColor Red; exit 1 }

function Ensure-Dir($dir) {
    if (-not (Test-Path $dir)) {
        VSay "creating $dir"
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }
}

# ---------------------------------------------------------------- steps

function Step-Platform {
    if (-not [Environment]::Is64BitOperatingSystem) {
        Die "overkill requires 64-bit Windows"
    }

    $arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }
    Say "detected windows/$arch"
    $script:Platform = "windows"
    $script:Arch = $arch
}

function Step-InstallDir {
    Ensure-Dir $InstallDir
    Say "install dir: $InstallDir"
}

function Step-BuildFromSource {
    Say "building from source via 'go install $GoPkg@latest'"

    $env:GOBIN = $InstallDir
    $result = go install "$GoPkg@latest" 2>&1
    if ($LASTEXITCODE -ne 0) {
        Die "go install failed (repo may not yet be published; try building from a local checkout)`n$result"
    }

    if (-not (Test-Path $BinPath)) {
        Die "build completed but $BinPath not found"
    }
}

function Step-DownloadBinary {
    $asset = "overkill-windows-amd64.exe"
    $url = "$ReleaseBase/$asset"
    $shaUrl = "$url.sha256"
    $tmpDir = Join-Path $env:TEMP "overkill-install-$(Get-Random)"
    Ensure-Dir $tmpDir
    $tmpBin = Join-Path $tmpDir "overkill.exe"

    try {
        Say "downloading $url"
        try {
            Invoke-WebRequest -Uri $url -OutFile $tmpBin -ErrorAction Stop
        } catch {
            Die "failed to download $url — try `$env:OVERKILL_BUILD_FROM_SOURCE=1 with Go on PATH`n$($_.Exception.Message)"
        }

        # SHA256 verify if available
        try {
            $tmpSha = Join-Path $tmpDir "overkill.exe.sha256"
            Invoke-WebRequest -Uri $shaUrl -OutFile $tmpSha -ErrorAction Stop
            Say "verifying SHA256"
            $expected = (Get-Content $tmpSha).Split(" ")[0].Trim()
            $actual = (Get-FileHash -Path $tmpBin -Algorithm SHA256).Hash.ToLower()
            if ($actual -ne $expected) {
                Die "SHA256 mismatch: expected $expected got $actual"
            }
        } catch {
            VSay "no .sha256 sibling found, skipping checksum verification (pre-release)"
        }

        Move-Item -Path $tmpBin -Destination $BinPath -Force
        Say "installed binary to $BinPath"
    } finally {
        Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
    }
}

function Step-Install {
    if ($env:OVERKILL_BUILD_FROM_SOURCE -eq "1") {
        if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
            Die "OVERKILL_BUILD_FROM_SOURCE=1 set but 'go' not on PATH"
        }
        Step-BuildFromSource
        return
    }

    if (Get-Command go -ErrorAction SilentlyContinue) {
        Step-BuildFromSource
    } else {
        Step-DownloadBinary
    }
}

function Step-PathHint {
    # Check if install dir is already in PATH
    $currentUserPath = [Environment]::GetEnvironmentVariable("PATH", "User") ?? ""
    $currentMachinePath = [Environment]::GetEnvironmentVariable("PATH", "Machine") ?? ""
    $fullPath = "$currentUserPath;$currentMachinePath"

    if ($fullPath -like "*$InstallDir*") {
        VSay "$InstallDir already in PATH"
        return
    }

    # Add to user PATH
    $newPath = if ($currentUserPath) { "$currentUserPath;$InstallDir" } else { $InstallDir }
    [Environment]::SetEnvironmentVariable("PATH", $newPath, "User")
    Say "added $InstallDir to user PATH"

    # Refresh current session's PATH
    $env:PATH = "$env:PATH;$InstallDir"
    Say "PATH updated for current session. New terminals will pick it up automatically."
}

function Step-BootstrapHome {
    Ensure-Dir $OverkillHome
    foreach ($sub in @("sessions", "plugins", "cache", "journal\raw", "memories")) {
        Ensure-Dir (Join-Path $OverkillHome $sub)
    }

    $cfg = Join-Path $OverkillHome "config.toml"
    if (-not (Test-Path $cfg)) {
        @"
# overkill config — edit freely. Run `overkill doctor` to validate.

[agent]
default_model = "anthropic/claude-sonnet-4-5"
max_tokens    = 8192
temperature   = 0.2

[providers.anthropic]
type    = "anthropic"
api_key = "`${ANTHROPIC_API_KEY}"

[providers.openai]
type    = "openai"
api_key = "`${OPENAI_API_KEY}"

[ui]
theme = "catppuccin-mocha"
"@ | Out-File -FilePath $cfg -Encoding UTF8 -NoNewline
        Say "wrote default config to $cfg"
    } else {
        VSay "config already present at $cfg, leaving untouched"
    }
}

function Step-Finish {
    Say "done."
    Write-Host ""
    Write-Host "  binary  : $BinPath"
    if (Test-Path $BinPath) {
        try {
            $ver = & $BinPath --version 2>$null
            if ($ver) { Write-Host "  version : $ver" }
        } catch { }
    }
    Write-Host ""
    Write-Host "Next steps:"
    Write-Host "  1. run 'overkill' to launch the TUI"
    Write-Host "  2. run 'overkill doctor' to self-check every subsystem"
    Write-Host "  3. run 'overkill --help' for the CLI surface"
    Write-Host ""
    Write-Host "If 'overkill' isn't found, restart your terminal or run:"
    Write-Host "  `$env:PATH = `"$InstallDir;`$env:PATH`""
}

# ---------------------------------------------------------------- main

function Main {
    Step-Platform
    Step-InstallDir
    Step-Install
    Step-PathHint
    Step-BootstrapHome
    Step-Finish
}

Main
