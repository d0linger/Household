param([switch]$Integration,[switch]$Browser,[switch]$Race)
$ErrorActionPreference='Stop'
$taskRoot=Split-Path -Parent $PSScriptRoot
Push-Location $taskRoot
try {
    $taskFormatting=gofmt -l cmd internal
    if($LASTEXITCODE -ne 0){throw 'Formatting failed'}
    if($taskFormatting){throw 'Source required formatting; review the formatting change and rerun'}
    go vet ./...
    if($LASTEXITCODE -ne 0){throw 'go vet failed'}
    New-Item -ItemType Directory -Force outputs/qa | Out-Null
    go test ./... -count=1 -shuffle=on '-coverprofile=outputs/qa/unit-coverage.out' -timeout=3m
    if($LASTEXITCODE -ne 0){throw 'Unit tests failed'}
    node scripts/check-coverage.cjs outputs/qa/unit-coverage.out 14 outputs/qa/unit-coverage.json
    if($LASTEXITCODE -ne 0){throw 'Coverage report failed'}
    go mod verify
    if($LASTEXITCODE -ne 0){throw 'Module verification failed'}
    node --test scripts/quality-tools.test.cjs scripts/shopping-input.test.cjs scripts/shopping-checkout.test.cjs scripts/recipe-workspace.test.cjs
    if($LASTEXITCODE -ne 0){throw 'Quality tool tests failed'}
    node scripts/check-workflows.cjs
    if($LASTEXITCODE -ne 0){throw 'Workflow policy failed'}
    node scripts/check-migrations.cjs
    if($LASTEXITCODE -ne 0){throw 'Migration checksums failed'}
    if($Integration){& ./scripts/test-integration.ps1}
    if($Race){& ./scripts/test-race.ps1}
    if($Browser){node ./scripts/browser-check.cjs;if($LASTEXITCODE -ne 0){throw 'Browser checks failed'}}
} finally {Pop-Location}
