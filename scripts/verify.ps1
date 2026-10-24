param([switch]$Integration,[switch]$Browser)
$ErrorActionPreference='Stop'
$taskRoot=Split-Path -Parent $PSScriptRoot
Push-Location $taskRoot
try {
    $taskFormatting=gofmt -l cmd internal
    if($LASTEXITCODE -ne 0){throw 'Formatting failed'}
    if($taskFormatting){throw 'Source required formatting; review the formatting change and rerun'}
    go vet ./...
    if($LASTEXITCODE -ne 0){throw 'go vet failed'}
    go test ./...
    if($LASTEXITCODE -ne 0){throw 'Unit tests failed'}
    go mod verify
    if($LASTEXITCODE -ne 0){throw 'Module verification failed'}
    if($Integration){& ./scripts/test-integration.ps1}
    if($Browser){node ./scripts/browser-check.cjs;if($LASTEXITCODE -ne 0){throw 'Browser checks failed'}}
} finally {Pop-Location}
