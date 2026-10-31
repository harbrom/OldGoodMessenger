$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$repoRoot = Split-Path -Parent $PSScriptRoot
$toolsRoot = Join-Path $repoRoot '.tools'
$version = '1.27.3'
$archive = Join-Path $toolsRoot "go$version.windows-amd64.zip"
$expectedHash = 'f26081f438319757f8a1b46d7d0b2c5ec7583d4038e485e0aa725cfde7885fff'
New-Item -ItemType Directory -Force -Path $toolsRoot | Out-Null
if (-not (Test-Path -LiteralPath $archive)) {
    Invoke-WebRequest -UseBasicParsing "https://go.dev/dl/go$version.windows-amd64.zip" -OutFile $archive
}
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant() -ne $expectedHash) {
    throw 'Go archive checksum mismatch. Do not execute or extract it.'
}
$destination = Join-Path $toolsRoot "go-$version"
if (-not (Test-Path -LiteralPath $destination)) {
    Expand-Archive -LiteralPath $archive -DestinationPath $destination
}
$go = Join-Path $destination 'go/bin/go.exe'
& $go version
if ($LASTEXITCODE -ne 0) { throw 'Go version check failed.' }
