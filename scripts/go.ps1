# Keep native Go flags out of PowerShell's named-parameter binding.
$GoArgs = $args
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$go = Join-Path $repoRoot '.tools/go-1.27.3/go/bin/go.exe'
if (-not (Test-Path $go)) { throw 'Run scripts/setup-go.ps1 first.' }
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:GOCACHE = Join-Path $repoRoot '.cache/go-build'
$env:GOMODCACHE = Join-Path $repoRoot '.cache/go-mod'
Push-Location $repoRoot
try { & $go @GoArgs; $result = $LASTEXITCODE } finally { Pop-Location }
exit $result
