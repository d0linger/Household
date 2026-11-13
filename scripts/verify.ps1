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
    node --test scripts/documents-workspace-navigation.test.cjs scripts/finance-workspace-navigation.test.cjs scripts/workspace-navigation-operational.test.cjs scripts/today.test.cjs scripts/quality-tools.test.cjs scripts/release-guard.test.cjs scripts/release-policy.test.cjs scripts/local-artifacts.test.cjs scripts/theme.test.cjs scripts/security.test.cjs scripts/finance-reminders.test.cjs scripts/localization.test.cjs scripts/shopping-input.test.cjs scripts/shopping-checkout.test.cjs scripts/recipe-workspace.test.cjs scripts/recipe-expansion.test.cjs scripts/cooking.test.cjs scripts/planning.test.cjs scripts/mealplan-extensions.test.cjs scripts/planning-suggestions.test.cjs scripts/planning-purchases.test.cjs scripts/pantry-daily.test.cjs scripts/ingredient-pantry.test.cjs scripts/cooking-stock.test.cjs scripts/pantry-batches.test.cjs scripts/expense-editor.test.cjs scripts/finance-workspace.test.cjs scripts/report-values.test.cjs scripts/shopping-repurchase.test.cjs scripts/shopping-barcode.test.cjs scripts/pantry-waste.test.cjs scripts/fuel-overview.test.cjs scripts/fuel-module-values.test.cjs scripts/go-test-shards.test.cjs scripts/fuel-browser-contracts.test.cjs scripts/pantry-extensions.test.cjs scripts/shopping-extensions.test.cjs scripts/push-worker.test.cjs scripts/notification-settings.test.cjs scripts/design-preview.test.cjs
    if($LASTEXITCODE -ne 0){throw 'Quality tool tests failed'}
    node scripts/build-design-preview.cjs --check
    if($LASTEXITCODE -ne 0){throw 'Design preview assets are stale'}
    node scripts/check-local-artifacts.cjs --index
    if($LASTEXITCODE -ne 0){throw 'Local artifact policy failed'}
    node scripts/check-workflows.cjs
    if($LASTEXITCODE -ne 0){throw 'Workflow policy failed'}
    node scripts/check-migrations.cjs
    if($LASTEXITCODE -ne 0){throw 'Migration checksums failed'}
    node scripts/update-tasklist.cjs --check
    if($LASTEXITCODE -ne 0){throw 'Task list is stale'}
    if($Integration){& ./scripts/test-integration.ps1}
    if($Race){& ./scripts/test-race.ps1}
    if($Browser){npm run test:browser;if($LASTEXITCODE -ne 0){throw 'Browser checks failed'}}
} finally {Pop-Location}
