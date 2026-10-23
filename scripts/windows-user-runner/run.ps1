param([Parameter(ValueFromRemainingArguments=$true)][string[]]$Command)
$ErrorActionPreference = 'Stop'
$user = 'oktest' + ([guid]::NewGuid().ToString('N').Substring(0, 10))
$password = [guid]::NewGuid().ToString('N') + 'aA9!'
Write-Output "::add-mask::$password"
$secure = ConvertTo-SecureString $password -AsPlainText -Force
New-LocalUser -Name $user -Password $secure -PasswordNeverExpires -UserMayNotChangePassword | Out-Null
Add-LocalGroupMember -Group (Get-LocalGroup -SID 'S-1-5-32-545').Name -Member $user
try {
  $env:OVERKILL_ACCEPTANCE_USER = $user
  $env:OVERKILL_ACCEPTANCE_PASSWORD = $password
  $output = Join-Path $env:GITHUB_WORKSPACE '.cache/windows-desktop'
  New-Item -ItemType Directory -Force $output | Out-Null
  $env:OVERKILL_ACCEPTANCE_LOG = Join-Path $output ('ordinary-user-' + $user + '.log')
  icacls $env:GITHUB_WORKSPACE /grant "${user}:(OI)(CI)RX" /Q
  if ($LASTEXITCODE -ne 0) { throw 'Could not grant acceptance source read access' }
  icacls $output /grant "${user}:(OI)(CI)M" /T /Q | Out-Null
  if ($LASTEXITCODE -ne 0) { throw 'Could not grant acceptance artifact write access' }
  & (Join-Path $env:GITHUB_WORKSPACE '.cache/windows-user-runner.exe') @Command
  if ($LASTEXITCODE -ne 0) { throw "Ordinary-user native acceptance failed: $LASTEXITCODE" }
} finally {
  Remove-Item Env:OVERKILL_ACCEPTANCE_PASSWORD -ErrorAction SilentlyContinue
  Remove-LocalUser -Name $user -ErrorAction Continue
}
