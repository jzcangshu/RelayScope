$ErrorActionPreference = 'Stop'

$projectRoot = Split-Path -Parent $PSScriptRoot
$go = Join-Path $projectRoot '.tools\go\bin\go.exe'
$node = (Get-Command node -ErrorAction Stop).Source

if (-not (Test-Path -LiteralPath $go)) {
    $goCommand = Get-Command go -ErrorAction Stop
    $go = $goCommand.Source
}

$env:GOCACHE = Join-Path $projectRoot '.cache\go-build'
$env:GOMODCACHE = Join-Path $projectRoot '.cache\go-mod'
& $go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& $go vet ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& $node --test (Join-Path $projectRoot 'web\public\dashboard.test.cjs') (Join-Path $projectRoot 'web\admin\admin.test.cjs') (Join-Path $projectRoot 'extension\session-sync\capture.test.cjs')
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
