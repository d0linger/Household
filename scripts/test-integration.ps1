$ErrorActionPreference='Stop'
$taskRoot=Split-Path -Parent $PSScriptRoot
$taskPrevious=@{}
foreach($taskName in @('HOUSEHOLD_TEST_OWNER_URL','HOUSEHOLD_TEST_APP_URL','HOUSEHOLD_REQUIRE_INTEGRATION')){$taskPrevious[$taskName]=[Environment]::GetEnvironmentVariable($taskName)}
Push-Location $taskRoot
try {
    docker compose -f compose.test.yaml up -d --wait
    if ($LASTEXITCODE -ne 0) {throw 'Test database failed to start'}
    $taskOwner=[uri]::EscapeDataString([IO.File]::ReadAllText((Join-Path $taskRoot '.local/secrets/owner_password')).TrimEnd("`r","`n"))
    $taskApp=[uri]::EscapeDataString([IO.File]::ReadAllText((Join-Path $taskRoot '.local/secrets/app_password')).TrimEnd("`r","`n"))
    $env:HOUSEHOLD_TEST_OWNER_URL="postgres://household_owner:${taskOwner}@localhost:55434/household_test?sslmode=disable"
    $env:HOUSEHOLD_TEST_APP_URL="postgres://household_app:${taskApp}@localhost:55434/household_test?sslmode=disable"
    $env:HOUSEHOLD_REQUIRE_INTEGRATION='1'
    New-Item -ItemType Directory -Force outputs/qa | Out-Null
    go test -tags=integration,testtools -p 1 -shuffle=on '-coverpkg=./internal/...' '-coverprofile=outputs/qa/integration-coverage.out' ./... -count=1 -timeout=25m
    if ($LASTEXITCODE -ne 0) {throw 'Integration tests failed'}
    node scripts/check-coverage.cjs outputs/qa/integration-coverage.out 55 outputs/qa/integration-coverage.json
    if ($LASTEXITCODE -ne 0) {throw 'Coverage report failed'}
} finally {
    foreach($taskName in $taskPrevious.Keys){[Environment]::SetEnvironmentVariable($taskName,$taskPrevious[$taskName])}
    Pop-Location
}
