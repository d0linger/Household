$ErrorActionPreference='Stop'
$taskRoot=Split-Path -Parent $PSScriptRoot
Push-Location $taskRoot
try {
    docker compose -f compose.test.yaml up -d --wait
    if ($LASTEXITCODE -ne 0) {throw 'Test database failed to start'}
    $taskOwner=[IO.File]::ReadAllText((Join-Path $taskRoot '.local/secrets/owner_password'))
    $taskApp=[IO.File]::ReadAllText((Join-Path $taskRoot '.local/secrets/app_password'))
    $env:HOUSEHOLD_TEST_OWNER_URL="postgres://household_owner:${taskOwner}@localhost:55434/household_test?sslmode=disable"
    $env:HOUSEHOLD_TEST_APP_URL="postgres://household_app:${taskApp}@localhost:55434/household_test?sslmode=disable"
    go test -tags=integration ./internal/... -count=1 -timeout=3m
    if ($LASTEXITCODE -ne 0) {throw 'Integration tests failed'}
} finally {
    Remove-Item Env:HOUSEHOLD_TEST_OWNER_URL -ErrorAction SilentlyContinue
    Remove-Item Env:HOUSEHOLD_TEST_APP_URL -ErrorAction SilentlyContinue
    Pop-Location
}
