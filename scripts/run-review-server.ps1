$ErrorActionPreference='Stop'
$taskRoot=Split-Path -Parent $PSScriptRoot
Set-Location $taskRoot
docker compose -f compose.test.yaml up -d --wait
if($LASTEXITCODE -ne 0){throw 'Isolated review database failed to start'}
$taskOwner=[IO.File]::ReadAllText((Join-Path $taskRoot '.local/secrets/owner_password'))
$taskApp=[IO.File]::ReadAllText((Join-Path $taskRoot '.local/secrets/app_password'))
$env:HOUSEHOLD_ENV='development'
$env:HOUSEHOLD_ORIGIN='http://localhost:8101'
$env:HOUSEHOLD_LISTEN='127.0.0.1:8101'
$env:HOUSEHOLD_CSRF_SECRET=[IO.File]::ReadAllText((Join-Path $taskRoot '.local/secrets/csrf_secret'))
$env:HOUSEHOLD_ENCRYPTION_KEY=[IO.File]::ReadAllText((Join-Path $taskRoot '.local/secrets/encryption_key'))
$env:HOUSEHOLD_RATE_SECRET=[IO.File]::ReadAllText((Join-Path $taskRoot '.local/secrets/rate_secret'))
$env:HOUSEHOLD_ADMIN_USERNAME='bootstrap'
$env:HOUSEHOLD_ADMIN_PASSWORD='Bootstrap testing 1234'
$env:HOUSEHOLD_UPLOAD_DIR=Join-Path $taskRoot '.local/review-uploads'
go build -o bin/household.exe ./cmd/household
if($LASTEXITCODE -ne 0){throw 'Review server build failed'}
$env:HOUSEHOLD_DATABASE_URL="postgres://household_owner:${taskOwner}@localhost:55434/household_test?sslmode=disable"
go run -tags=testtools ./cmd/test-fixture
if($LASTEXITCODE -ne 0){throw 'Isolated browser fixture failed'}
$env:HOUSEHOLD_DATABASE_URL="postgres://household_app:${taskApp}@localhost:55434/household_test?sslmode=disable"
& ./bin/household.exe serve
