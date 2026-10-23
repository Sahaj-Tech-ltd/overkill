# Native PowerShell installer fixtures; no network or user profile mutations.
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$work = Join-Path ([IO.Path]::GetTempPath()) ("overkill-installer-test-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $work | Out-Null
$env:LOCALAPPDATA = $work
$env:OVERKILL_HOME = Join-Path $work 'home'
$env:OVERKILL_INSTALLER_SOURCE_ONLY = '1'
$env:TEMP = $work
try {
    . (Join-Path $repoRoot 'install.ps1')
    function Die($message) { throw $message }
    $assets = Join-Path $work 'assets'
    New-Item -ItemType Directory -Path $assets | Out-Null
    function Invoke-WebRequest($Uri, $OutFile, $ErrorAction) {
        Copy-Item (Join-Path $assets ([uri]$Uri).Segments[-1]) $OutFile -ErrorAction Stop
    }
    Step-InstallDir
    $asset = 'overkill-windows-amd64.exe'
    [IO.File]::WriteAllText((Join-Path $assets $asset), 'fixture executable bytes')
    $digest = (Get-FileHash (Join-Path $assets $asset) -Algorithm SHA256).Hash.ToLowerInvariant()
    $manifest = Join-Path $assets 'checksums.txt'
    foreach ($text in @('', ('g' * 64 + "  $asset`n"), ('0' * 64 + "  $asset`n"), "$digest  another-asset`n", "$digest  $asset`n$digest  $asset`n")) {
        [IO.File]::WriteAllText($manifest, $text)
        [IO.File]::WriteAllText($BinPath, 'preserve installed binary')
        $failed = $false
        try { Step-DownloadBinary } catch { $failed = $true }
        if (-not $failed) { throw 'Unverified download was accepted' }
        if ([IO.File]::ReadAllText($BinPath) -ne 'preserve installed binary') { throw 'Failed verification replaced executable' }
    }
    Remove-Item $manifest
    $failed = $false
    try { Step-DownloadBinary } catch { $failed = $true }
    if (-not $failed) { throw 'Missing manifest was accepted' }
    [IO.File]::WriteAllText($manifest, "$digest  $asset`n")
    Step-DownloadBinary
    if ([IO.File]::ReadAllText($BinPath) -ne 'fixture executable bytes') { throw 'Verified download was not installed' }
    # Run the actual installed executable for schema generation and loading;
    # checking a handwritten TOML sample does not establish installer behavior.
    if (-not $env:OVERKILL_INSTALLER_TEST_BINARY -or -not (Test-Path $env:OVERKILL_INSTALLER_TEST_BINARY)) {
        throw 'OVERKILL_INSTALLER_TEST_BINARY must point to a freshly built native application'
    }
    Step-Platform
    if ($Platform -ne 'windows' -or $Arch -ne 'amd64') { throw 'Unexpected native release target' }
    Remove-Item (Join-Path $assets $asset)
    $prior = (Get-FileHash $BinPath -Algorithm SHA256).Hash
    $failed = $false
    try { Step-DownloadBinary } catch { $failed = $true }
    if (-not $failed -or (Get-FileHash $BinPath -Algorithm SHA256).Hash -ne $prior) { throw 'Failed asset download replaced executable' }
    Copy-Item $env:OVERKILL_INSTALLER_TEST_BINARY (Join-Path $assets $asset)
    $digest = (Get-FileHash (Join-Path $assets $asset) -Algorithm SHA256).Hash
    [IO.File]::WriteAllText($manifest, "$digest  $asset`n")
    Step-DownloadBinary
    if ((Get-FileHash $BinPath -Algorithm SHA256).Hash -ne $digest) { throw 'Real native executable download differs' }
    & $BinPath --version
    if ($LASTEXITCODE -ne 0) { throw 'Installed native version failed' }
    & $BinPath --help | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Installed native help failed' }
    Step-BootstrapHome
    $cfg = Join-Path $OverkillHome 'config.toml'
    & $BinPath --config $cfg config validate
    if ($LASTEXITCODE -ne 0) { throw 'Installer-created configuration did not load in the application' }
    $original = (Get-FileHash $cfg -Algorithm SHA256).Hash
    Step-BootstrapHome
    if ((Get-FileHash $cfg -Algorithm SHA256).Hash -ne $original) { throw 'Bootstrap overwrote existing configuration' }
    [IO.File]::WriteAllText($cfg, "[providers.openai]`napi_key = 'test-only'`n")
    & $BinPath --config $cfg config validate 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { throw 'Invalid legacy provider tables were silently accepted' }
    Write-Host 'Windows installer checksum and real configuration fixtures PASS'
} finally {
    Remove-Item $work -Recurse -Force
}
