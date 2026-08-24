$ErrorActionPreference = 'Stop'

$projectRoot = Split-Path -Parent $PSScriptRoot
$go = Join-Path $projectRoot '.tools\go\bin\go.exe'

if (-not (Test-Path -LiteralPath $go)) {
    $goCommand = Get-Command go -ErrorAction Stop
    $go = $goCommand.Source
}

$env:GOCACHE = Join-Path $projectRoot '.cache\go-build'
$env:GOMODCACHE = Join-Path $projectRoot '.cache\go-mod'
& $go run ./cmd/relayscope @args

