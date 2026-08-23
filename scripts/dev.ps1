$ErrorActionPreference = 'Stop'

$projectRoot = Split-Path -Parent $PSScriptRoot
$go = Join-Path $projectRoot '.tools\go\bin\go.exe'

if (-not (Test-Path -LiteralPath $go)) {
    throw 'Project-local Go toolchain is missing. See docs/development.md.'
}

$env:GOCACHE = Join-Path $projectRoot '.cache\go-build'
$env:GOMODCACHE = Join-Path $projectRoot '.cache\go-mod'
& $go run ./cmd/relaypulse @args

