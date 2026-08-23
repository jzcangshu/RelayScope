$ErrorActionPreference = 'Stop'

$projectRoot = Split-Path -Parent $PSScriptRoot
$go = Join-Path $projectRoot '.tools\go\bin\go.exe'
$outputDir = Join-Path $projectRoot 'dist'

if (-not (Test-Path -LiteralPath $go)) {
    throw 'Project-local Go toolchain is missing. See docs/development.md.'
}

New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
$env:CGO_ENABLED = '0'
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:GOCACHE = Join-Path $projectRoot '.cache\go-build-linux-amd64'
$env:GOMODCACHE = Join-Path $projectRoot '.cache\go-mod'
& $go build -trimpath -ldflags '-s -w' -o (Join-Path $outputDir 'relaypulse-linux-amd64') ./cmd/relaypulse
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Get-FileHash -Algorithm SHA256 (Join-Path $outputDir 'relaypulse-linux-amd64')
