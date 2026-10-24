param([ValidatePattern('^[^\r\n]+$')][string]$Run='.')
$ErrorActionPreference='Stop'
$taskRoot=Split-Path -Parent $PSScriptRoot
Push-Location $taskRoot
try {
    docker compose -f compose.test.yaml up -d --wait
    if($LASTEXITCODE -ne 0){throw 'Isolated race database unavailable'}
    $taskOutput=Join-Path $taskRoot 'outputs/qa'
    New-Item -ItemType Directory -Force $taskOutput | Out-Null
    $taskOwner=[IO.File]::ReadAllText((Join-Path $taskRoot '.local/secrets/owner_password'))
    $taskApp=[IO.File]::ReadAllText((Join-Path $taskRoot '.local/secrets/app_password'))
    $taskEnvironment=Join-Path $taskRoot '.local/secrets/race_test.env'
    [IO.File]::WriteAllLines($taskEnvironment,@(
      "HOUSEHOLD_TEST_OWNER_URL=postgres://household_owner:${taskOwner}@db:5432/household_test?sslmode=disable",
      "HOUSEHOLD_TEST_APP_URL=postgres://household_app:${taskApp}@db:5432/household_test?sslmode=disable",
      'HOUSEHOLD_REQUIRE_INTEGRATION=1','CGO_ENABLED=1','GOCACHE=/cache/build',"HOUSEHOLD_TEST_PATTERN=$Run"))
    try {
      $taskImage=[regex]::Match([IO.File]::ReadAllText((Join-Path $taskRoot 'Dockerfile')),'(?m)^FROM (golang:\S+@sha256:[0-9a-f]{64}) AS build').Groups[1].Value
      if(!$taskImage){throw 'No pinned Go builder found'}
      # Packages share this disposable database; their intentional concurrency remains enabled.
      $taskCommand='apk add --no-cache build-base >/dev/null && go test -race -p 1 -tags=integration -shuffle=on -coverpkg=./internal/... -covermode=atomic -coverprofile=/evidence/linux-race-coverage.out ./... -count=1 -timeout=15m'
      if($Run -ne '.'){$taskCommand='apk add --no-cache build-base >/dev/null && go test -race -p 1 -tags=integration -shuffle=on -run "$HOUSEHOLD_TEST_PATTERN" ./... -count=1 -timeout=15m'}
      docker run --rm --cpus 2 --memory 2g --network household-tests_default --env-file $taskEnvironment --mount "type=bind,source=$taskRoot,target=/src,readonly" --mount "type=bind,source=$taskOutput,target=/evidence" --mount type=volume,source=household-go-mod-cache,target=/go/pkg/mod --mount type=volume,source=household-go-build-cache,target=/cache/build -w /src $taskImage sh -c $taskCommand
      if($LASTEXITCODE -ne 0){throw 'Linux race/integration checks failed'}
      if($Run -eq '.'){
        node scripts/check-coverage.cjs outputs/qa/linux-race-coverage.out 55 outputs/qa/linux-race-coverage.json
        if($LASTEXITCODE -ne 0){throw 'Race coverage report failed'}
      }
    } finally {Remove-Item -LiteralPath $taskEnvironment -ErrorAction SilentlyContinue}
} finally {Pop-Location}
