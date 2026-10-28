#requires -Version 7.4
param(
 [Parameter(Mandatory)][ValidatePattern('^sha256:[0-9a-f]{64}$')][string]$RuntimeImageId,
 [Parameter(Mandatory)][ValidatePattern('^sha256:[0-9a-f]{64}$')][string]$BackupImageId,
 [Parameter(Mandatory)][string]$NativeSummary,
 [Parameter(Mandatory)][string]$NativeSourceManifest,
 [Parameter(Mandatory)][ValidatePattern('^[0-9a-f]{64}$')][string]$NativeManifestSha256,
 [switch]$ResetIsolatedDatabase,
 [ValidatePattern('^(?:|[0-9]+-[0-9]+)$')][string]$CIRun=''
)
$ErrorActionPreference='Stop'
$PSNativeCommandUseErrorActionPreference=$false
$taskRoot=Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $taskRoot
$taskWindows=[OperatingSystem]::IsWindows()
$taskLock=$null
if(!$ResetIsolatedDatabase){throw 'Explicit -ResetIsolatedDatabase is required after all parent native tests finish'}
$taskSourceContainer='household-tests-db-1'
$taskNetwork='household-tests_default'
$taskRun=[guid]::NewGuid().ToString('N')
$taskPrefix="household-fuel-browser-$taskRun"
$taskPrivate=Join-Path $taskRoot ".local/fuel-browser-auth-$taskRun"
$taskOutput=Join-Path $taskRoot 'outputs/fuel-module-review'
$taskArtifacts=Join-Path $taskOutput "standalone-$taskRun"
$taskNames=@{app="$taskPrefix-app";toolbox="$taskPrefix-toolbox";restore="$taskPrefix-restore"}
$taskVolumes=@{uploads="$taskPrefix-uploads";work="$taskPrefix-work";repository="$taskPrefix-repository"}
$taskKnownSecrets=@()
$taskCreatedContainers=@()
$taskCreatedVolumes=@()
$taskController=$null
$taskDockerReady=$false
$taskCleanupOK=$true
$taskGoEnvironment=@{}
foreach($taskKey in @('GOMODCACHE','GOCACHE','GOTMPDIR','TMP','TEMP','GOTOOLCHAIN')){$taskGoEnvironment[$taskKey]=[Environment]::GetEnvironmentVariable($taskKey)}
$taskReport=[ordered]@{passed=$false;run_id=$taskRun;ci_run=$CIRun;source_database='household_test';source_port=55434;origin='http://127.0.0.1:8101';restore_database='household_restore';native_summary=$NativeSummary;native_manifest_sha256=$NativeManifestSha256;images=@{runtime=$RuntimeImageId;backup=$BackupImageId};resources=$taskNames;volumes=$taskVolumes;cleanup=@();live_8100_changed=$false}

function Protect-FuelLog([string]$Text){
 foreach($taskValue in $taskKnownSecrets){if($taskValue){$Text=$Text.Replace($taskValue,'[private value]')}}
 return [regex]::Replace($Text,'postgres(?:ql)?://[^\s"'']+','[private database URI]')
}
function Invoke-FuelDocker([string[]]$Arguments){
 $taskResult=@(& docker @Arguments 2>&1)
 if($LASTEXITCODE -ne 0){$taskMessage=Protect-FuelLog (($taskResult | ForEach-Object {"$_"}) -join "`n");throw "Isolated Docker operation failed: $($taskMessage.Substring(0,[Math]::Min(4000,$taskMessage.Length)))"}
 return (($taskResult | ForEach-Object {"$_"}) -join "`n").Trim()
}
function Invoke-FuelSQL([string]$Database,[string]$SQL){
 if($Database -notin @('postgres','household_test')){throw 'Unexpected fixed preflight database'}
 $taskResult=@($SQL | & docker exec -i $taskSourceContainer psql --no-psqlrc -U postgres -d $Database -qAt -v ON_ERROR_STOP=1 --file=- 2>&1)
 if($LASTEXITCODE -ne 0){throw 'Isolated preflight SQL failed'}
 return (($taskResult | ForEach-Object {"$_"}) -join "`n").Trim()
}
function Invoke-FuelContract([string[]]$Arguments,[string]$InputJSON=''){
 $taskNode=(Get-Command node -ErrorAction Stop).Source
 $taskTool=Join-Path $taskRoot 'scripts/fuel-browser-contracts.cjs'
 if($InputJSON){$taskResult=@($InputJSON | & $taskNode $taskTool @Arguments 2>&1)}else{$taskResult=@(& $taskNode $taskTool @Arguments 2>&1)}
 if($LASTEXITCODE -ne 0){throw ('Fuel guard refused: '+(($taskResult | ForEach-Object {"$_"}) -join ' '))}
 return (($taskResult | ForEach-Object {"$_"}) -join "`n") | ConvertFrom-Json
}
function Assert-FuelSources($Manifest){$null=Invoke-FuelContract -Arguments @('source',$taskRoot,$taskManifestPath,$NativeManifestSha256)}
function Assert-FuelPath([string]$Path,[string]$AllowedRoot){
 $taskFull=[IO.Path]::GetFullPath($Path);$taskAllowed=[IO.Path]::GetFullPath($AllowedRoot).TrimEnd('\','/')+[IO.Path]::DirectorySeparatorChar
 $taskComparison=if($taskWindows){[StringComparison]::OrdinalIgnoreCase}else{[StringComparison]::Ordinal}
 if(!$taskFull.StartsWith($taskAllowed,$taskComparison)){throw 'Private/artifact path leaves its exact allowed root'}
 $taskProbe=$taskFull
 while($taskProbe){if((Test-Path -LiteralPath $taskProbe) -and ((Get-Item -LiteralPath $taskProbe -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)){throw 'Private/artifact path refuses symbolic links and reparse points'};$taskParent=Split-Path -Parent $taskProbe;if(!$taskParent -or $taskParent -eq $taskProbe){break};$taskProbe=$taskParent}
 return $taskFull
}
function Set-FuelPrivateFile([string]$Path){if(!$taskWindows){[IO.File]::SetUnixFileMode($Path,([IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::UserWrite))}}
function Write-FuelPrivate([string]$Name,[string]$Value){
 if($Name -notmatch '^[a-z-]+$' -or $Value.Contains("`0")){throw 'Invalid private fixture value'}
 [IO.File]::WriteAllText((Join-Path $taskPrivate $Name),$Value,[Text.UTF8Encoding]::new($false))
 # Only mounted secrets are0444 inside the host0700 directory, allowing containerUIDs999/10001 to read them. Auth/config remain0600.
 if(!$taskWindows){[IO.File]::SetUnixFileMode((Join-Path $taskPrivate $Name),([IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::GroupRead -bor [IO.UnixFileMode]::OtherRead))}
}
function Add-FuelMount([string[]]$Arguments,[string]$Source,[string]$Target){
 $taskSourceField='"'+('source='+$Source).Replace('"','""')+'"'
 return $Arguments+@('--mount',"type=bind,$taskSourceField,target=$Target,readonly")
}
function Wait-FuelRestore {
 for($taskAttempt=0;$taskAttempt -lt 60;$taskAttempt++){
  $null=& docker exec $taskNames.restore pg_isready -U postgres -d household_restore 2>&1
  if($LASTEXITCODE -eq 0){return}
  Start-Sleep -Seconds 1
 }
 throw 'Own disposable restore database did not become ready'
}

try {
 $null=Assert-FuelPath $taskPrivate (Join-Path $taskRoot '.local')
 $null=Assert-FuelPath $taskArtifacts (Join-Path $taskRoot 'outputs')
 New-Item -ItemType Directory -Path (Join-Path $taskRoot '.local') -Force | Out-Null
 $taskLock=[IO.File]::Open((Join-Path $taskRoot '.local/fuel-browser-orchestration.lock'),[IO.FileMode]::OpenOrCreate,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
 $taskSummaryPath=(Resolve-Path -LiteralPath $NativeSummary).Path
 $taskManifestPath=(Resolve-Path -LiteralPath $NativeSourceManifest).Path
 $taskNativeProof=Invoke-FuelContract -Arguments @('native-preflight',$taskRoot,$taskSummaryPath,$taskManifestPath,$NativeManifestSha256)
 $taskReport.required_native_mains=$taskNativeProof.required_mains
 $taskManifest=@(Get-Content -LiteralPath $taskManifestPath -Raw | ConvertFrom-Json)
 $taskBrowserSources=@(Invoke-FuelContract -Arguments @('browser-source',$taskRoot))
 $taskBrowserSourceJSON=$taskBrowserSources | ConvertTo-Json -Depth 5 -Compress
 $null=Get-Command docker -ErrorAction Stop
 $taskDockerReady=$true
 foreach($taskImage in @($RuntimeImageId,$BackupImageId)){
  $taskImageInfo=Invoke-FuelDocker -Arguments @('image','inspect',$taskImage,'--format','{"id":{{json .Id}},"user":{{json .Config.User}},"source_manifest_sha256":{{json (index .Config.Labels "household.source-manifest-sha256")}}}')
  $null=Invoke-FuelContract -Arguments @('image',$taskImage,$NativeManifestSha256) -InputJSON $taskImageInfo
 }
 $taskDBLabel=Invoke-FuelDocker -Arguments @('inspect',$taskSourceContainer,'--format','{{index .Config.Labels "com.docker.compose.project"}}|{{index .Config.Labels "com.docker.compose.service"}}')
 if($taskDBLabel -ne 'household-tests|db'){throw 'The source is not the exact isolated Compose database'}
 if((Invoke-FuelDocker -Arguments @('port',$taskSourceContainer,'5432/tcp')) -ne '127.0.0.1:55434'){throw 'Isolated database port differs from127.0.0.1:55434'}
 $taskNetworks=Invoke-FuelDocker -Arguments @('inspect',$taskSourceContainer,'--format','{{json .NetworkSettings.Networks}}') | ConvertFrom-Json
 if(!$taskNetworks.PSObject.Properties[$taskNetwork]){throw 'Source database is not on the exact test network'}
 if((Invoke-FuelDocker -Arguments @('network','inspect',$taskNetwork,'--format','{{.Name}}')) -ne $taskNetwork){throw 'Test network is unavailable'}
 $taskDBMetadata=@{container=$taskSourceContainer;project=$taskDBLabel.Split('|')[0];service=$taskDBLabel.Split('|')[1];port=(Invoke-FuelDocker -Arguments @('port',$taskSourceContainer,'5432/tcp'));networks=@($taskNetworks.PSObject.Properties.Name)}
 $null=Invoke-FuelContract -Arguments @('database') -InputJSON ($taskDBMetadata | ConvertTo-Json -Compress)
 if(@([Net.NetworkInformation.IPGlobalProperties]::GetIPGlobalProperties().GetActiveTcpListeners() | Where-Object Port -eq 8101).Count){throw 'Port8101 is already owned; preserve that review process'}
 if(Test-Path -LiteralPath (Join-Path $taskOutput 'browser-report.json')){throw 'Preserve the prior Fuel browser evidence before starting a new standalone run'}
 foreach($taskName in $taskNames.Values){if(@(& docker ps -a --filter "name=^/$taskName`$" --format '{{.Names}}').Count){throw 'An exact new run container name already exists'}}
 New-Item -ItemType Directory -Path $taskPrivate,$taskArtifacts -Force | Out-Null
 if($taskWindows){
  $taskActor=[Security.Principal.WindowsIdentity]::GetCurrent().Name
  & icacls $taskPrivate /inheritance:r /grant:r "${taskActor}:(OI)(CI)F" 'SYSTEM:(OI)(CI)F' /Q | Out-Null
  if($LASTEXITCODE -ne 0){throw 'Could not restrict private browser state/fixture secrets'}
 }else{[IO.File]::SetUnixFileMode($taskPrivate,([IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::UserWrite -bor [IO.UnixFileMode]::UserExecute))}
 $taskBrowserSources | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $taskArtifacts 'browser-source-manifest.json') -Encoding utf8
 foreach($taskPair in @(@('owner','owner_password'),@('app','app_password'),@('postgres','postgres_password'),@('csrf','csrf_secret'),@('encryption','encryption_key'),@('rate','rate_secret'))){
  $taskValue=[IO.File]::ReadAllText((Join-Path $taskRoot ('.local/secrets/'+$taskPair[1]))).TrimEnd("`r","`n")
  if(!$taskValue){throw 'Required current fixture credential/key is empty'}
  $taskKnownSecrets+=$taskValue;Write-FuelPrivate $taskPair[0] $taskValue
 }
 $taskOwner=[IO.File]::ReadAllText((Join-Path $taskPrivate 'owner'));$taskApp=[IO.File]::ReadAllText((Join-Path $taskPrivate 'app'))
 $taskBootstrap='Fuel browser '+[Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(20))
 $taskRestic=[Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
 $taskKnownSecrets+=@($taskBootstrap,$taskRestic)
 Write-FuelPrivate 'bootstrap' $taskBootstrap;Write-FuelPrivate 'restic' $taskRestic
 $taskOwnerURI=[uri]::EscapeDataString($taskOwner);$taskAppURI=[uri]::EscapeDataString($taskApp)
 Write-FuelPrivate 'source-url' "postgres://household_owner:${taskOwnerURI}@db:5432/household_test?sslmode=disable"
 Write-FuelPrivate 'app-url' "postgres://household_app:${taskAppURI}@db:5432/household_test?sslmode=disable"
 Write-FuelPrivate 'target-url' "postgres://household_owner:${taskOwnerURI}@$($taskNames.restore):5432/household_restore?sslmode=disable"

 $taskGuard=Invoke-FuelSQL 'postgres' @'
SELECT json_build_object('version',current_setting('server_version_num')::integer,
 'owner_safe',EXISTS(SELECT 1 FROM pg_roles WHERE rolname='household_owner' AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole),
 'app_safe',EXISTS(SELECT 1 FROM pg_roles WHERE rolname='household_app' AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole),
 'exists',EXISTS(SELECT 1 FROM pg_database WHERE datname='household_test'),
 'clients',(SELECT count(*) FROM pg_stat_activity WHERE datname='household_test' AND backend_type='client backend'));
'@ | ConvertFrom-Json
 if($taskGuard.version -lt 180000 -or $taskGuard.version -ge 190000 -or !$taskGuard.owner_safe -or !$taskGuard.app_safe -or !$taskGuard.exists -or $taskGuard.clients -ne 0){throw 'Fresh isolated source guard refused version/roles/database or active clients; no reset performed'}
 # Exact named disposable database only. No --force: a racing client prevents reset.
 $null=Invoke-FuelDocker -Arguments @('exec',$taskSourceContainer,'dropdb','-U','postgres','household_test')
 $null=Invoke-FuelDocker -Arguments @('exec',$taskSourceContainer,'createdb','-U','postgres','-O','household_owner','household_test')
 $null=Invoke-FuelSQL 'household_test' 'REVOKE ALL ON DATABASE household_test FROM PUBLIC; GRANT CONNECT ON DATABASE household_test TO household_app,household_owner; REVOKE CREATE ON SCHEMA public FROM PUBLIC;'
 $taskReport.isolated_database_reset=$true

 $taskGoVersion=[regex]::Match([IO.File]::ReadAllText((Join-Path $taskRoot 'go.mod')),'(?m)^go (\d+\.\d+\.\d+)$').Groups[1].Value
 if(!$taskGoVersion){throw 'Exact Go toolchain version missing'}
 $env:GOTOOLCHAIN='go'+$taskGoVersion
 $taskFixture=@{
  HOUSEHOLD_ENV='development';HOUSEHOLD_ORIGIN='http://127.0.0.1:8101';HOUSEHOLD_ADMIN_USERNAME='bootstrap';HOUSEHOLD_ADMIN_PASSWORD=$taskBootstrap;
  HOUSEHOLD_DATABASE_URL="postgres://household_owner:${taskOwnerURI}@127.0.0.1:55434/household_test?sslmode=disable";
  HOUSEHOLD_CSRF_SECRET=[IO.File]::ReadAllText((Join-Path $taskPrivate 'csrf'));HOUSEHOLD_ENCRYPTION_KEY=[IO.File]::ReadAllText((Join-Path $taskPrivate 'encryption'));HOUSEHOLD_RATE_SECRET=[IO.File]::ReadAllText((Join-Path $taskPrivate 'rate'));
  HOUSEHOLD_UPLOAD_DIR=(Join-Path $taskPrivate 'fixture-unused-uploads')
 }
 $taskPrevious=@{}
 try {
  foreach($taskKey in $taskFixture.Keys){foreach($taskVariable in @($taskKey,($taskKey+'_FILE'))){$taskPrevious[$taskVariable]=[Environment]::GetEnvironmentVariable($taskVariable);[Environment]::SetEnvironmentVariable($taskVariable,$null)};[Environment]::SetEnvironmentVariable($taskKey,$taskFixture[$taskKey])}
  $taskFixtureOutput=@(& go run -mod=readonly -tags=testtools ./cmd/test-fixture 2>&1);$taskFixtureExit=$LASTEXITCODE
  (Protect-FuelLog (($taskFixtureOutput | ForEach-Object {"$_"}) -join "`n")) | Set-Content -LiteralPath (Join-Path $taskArtifacts 'fixture.log') -Encoding utf8
  if($taskFixtureExit -ne 0){throw 'Guarded synthetic bootstrap preparation failed; see sanitized fixture log'}
 } finally {foreach($taskKey in $taskPrevious.Keys){[Environment]::SetEnvironmentVariable($taskKey,$taskPrevious[$taskKey])}}
 $taskFresh=Invoke-FuelSQL 'household_test' "SELECT json_build_object('database',current_database(),'households',(SELECT count(*) FROM core.households),'vehicles',(SELECT count(*) FROM fuel.vehicles),'operations',(SELECT count(*) FROM fuel.module_operations));" | ConvertFrom-Json
 if($taskFresh.database -ne 'household_test' -or $taskFresh.households -ne 1 -or $taskFresh.vehicles -ne 0 -or $taskFresh.operations -ne 0){throw 'Guarded fresh browser household is not empty/single tenant'}

 foreach($taskVolume in $taskVolumes.Values){$taskCreateArgs=@('volume','create','--label',"household.fuel.browser.run=$taskRun");if($CIRun){$taskCreateArgs+=@('--label',"household.fuel.browser.ci=$CIRun")};$taskCreated=Invoke-FuelDocker -Arguments ($taskCreateArgs+@($taskVolume));if($taskCreated -ne $taskVolume){throw 'Unexpected volume creation result'};$taskCreatedVolumes+=$taskVolume}
 $taskPGImage='postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873'
 $taskRestoreArgs=@('run','--detach','--name',$taskNames.restore,'--label',"household.fuel.browser.run=$taskRun",'--network',$taskNetwork,'--memory','1g','--pids-limit','150','--security-opt','no-new-privileges:true','--tmpfs','/var/lib/postgresql:rw,size=1g','--env','POSTGRES_DB=household_restore','--env','POSTGRES_PASSWORD_FILE=/run/secrets/postgres_password','--env','POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256 --data-checksums')
 if($CIRun){$taskRestoreArgs+=@('--label',"household.fuel.browser.ci=$CIRun")}
 foreach($taskPair in @(@('postgres','postgres_password'),@('owner','owner_password'),@('app','app_password'))){$taskRestoreArgs=Add-FuelMount $taskRestoreArgs (Join-Path $taskPrivate $taskPair[0]) ('/run/secrets/'+$taskPair[1])}
 $taskRestoreArgs=Add-FuelMount $taskRestoreArgs (Join-Path $taskRoot 'deploy/postgres/init.sh') '/docker-entrypoint-initdb.d/10-household.sh'
 $null=Invoke-FuelDocker -Arguments ($taskRestoreArgs+@($taskPGImage));$taskCreatedContainers+=$taskNames.restore
 Wait-FuelRestore

 $taskCommon=@('HOUSEHOLD_ENV=development','HOUSEHOLD_ORIGIN=http://127.0.0.1:8101','HOUSEHOLD_ADMIN_USERNAME=bootstrap','HOUSEHOLD_CSRF_SECRET_FILE=/run/secrets/csrf','HOUSEHOLD_ENCRYPTION_KEY_FILE=/run/secrets/encryption','HOUSEHOLD_RATE_SECRET_FILE=/run/secrets/rate','HOUSEHOLD_UPLOAD_DIR=/data/uploads','GOMEMLIMIT=400MiB')
 $taskAppEnv=Join-Path $taskPrivate 'app.env';$taskToolEnv=Join-Path $taskPrivate 'toolbox.env'
 [IO.File]::WriteAllLines($taskAppEnv,$taskCommon+@('HOUSEHOLD_LISTEN=:8080','HOUSEHOLD_DATABASE_URL_FILE=/run/secrets/app-url','HOUSEHOLD_ADMIN_PASSWORD_FILE=/run/secrets/bootstrap','HOUSEHOLD_OCR_ENABLED=true'),[Text.UTF8Encoding]::new($false))
 [IO.File]::WriteAllLines($taskToolEnv,$taskCommon+@('HOUSEHOLD_DATABASE_URL_FILE=/run/secrets/source-url','HOUSEHOLD_RESTORE_DATABASE_URL_FILE=/run/secrets/target-url','HOUSEHOLD_BACKUP_WORK=/work','RESTIC_REPOSITORY=/repository','RESTIC_PASSWORD_FILE=/run/secrets/restic','RESTIC_CACHE_DIR=/tmp/restic-cache'),[Text.UTF8Encoding]::new($false))
 $taskBase=@('run','--detach','--label',"household.fuel.browser.run=$taskRun",'--network',$taskNetwork,'--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','--memory','512m','--pids-limit','150','--init','--tmpfs','/tmp:rw,noexec,nosuid,size=128m')
 if($CIRun){$taskBase+=@('--label',"household.fuel.browser.ci=$CIRun")}
 $taskAppArgs=$taskBase+@('--name',$taskNames.app,'--publish','127.0.0.1:8101:8080','--env-file',$taskAppEnv,'--mount',"type=volume,source=$($taskVolumes.uploads),target=/data/uploads")
 foreach($taskName in @('csrf','encryption','rate','bootstrap','app-url')){$taskAppArgs=Add-FuelMount $taskAppArgs (Join-Path $taskPrivate $taskName) ('/run/secrets/'+$taskName)}
 $null=Invoke-FuelDocker -Arguments ($taskAppArgs+@($RuntimeImageId));$taskCreatedContainers+=$taskNames.app
 $taskReady=$false
 for($taskAttempt=0;$taskAttempt -lt 60;$taskAttempt++){try{$taskResponse=Invoke-WebRequest -Uri 'http://127.0.0.1:8101/health/ready' -TimeoutSec 3;if($taskResponse.StatusCode -eq 200){$taskReady=$true;break}}catch{};Start-Sleep -Seconds 1}
 if(!$taskReady){throw 'Own OCR runtime did not become ready on8101'}
 $null=Invoke-FuelDocker -Arguments @('exec',$taskNames.app,'tesseract','--version')
 $null=Invoke-FuelDocker -Arguments @('exec',$taskNames.app,'pdftoppm','-v')
 $taskToolArgs=$taskBase+@('--name',$taskNames.toolbox,'--env-file',$taskToolEnv,'--mount',"type=volume,source=$($taskVolumes.uploads),target=/data/uploads,readonly",'--mount',"type=volume,source=$($taskVolumes.work),target=/work",'--mount',"type=volume,source=$($taskVolumes.repository),target=/repository",'--entrypoint','sleep')
 foreach($taskName in @('csrf','encryption','rate','source-url','target-url','restic')){$taskToolArgs=Add-FuelMount $taskToolArgs (Join-Path $taskPrivate $taskName) ('/run/secrets/'+$taskName)}
 $null=Invoke-FuelDocker -Arguments ($taskToolArgs+@($BackupImageId,'1800'));$taskCreatedContainers+=$taskNames.toolbox
 $null=Invoke-FuelDocker -Arguments @('exec',$taskNames.toolbox,'pg_dump','--version');$null=Invoke-FuelDocker -Arguments @('exec',$taskNames.toolbox,'pg_restore','--version')
 $null=Invoke-FuelDocker -Arguments @('exec',$taskNames.toolbox,'restic','init')
 if((Invoke-FuelDocker -Arguments @('exec',$taskNames.toolbox,'restic','snapshots','--json')) -ne '[]'){throw 'Dedicated encrypted repository was not initially empty'}
 Assert-FuelSources $taskManifest
 if((@(Invoke-FuelContract -Arguments @('browser-source',$taskRoot)) | ConvertTo-Json -Depth 5 -Compress) -ne $taskBrowserSourceJSON){throw 'Browser source changed during preparation'}
 $taskConfig=[ordered]@{run_id=$taskRun;source_container=$taskSourceContainer;toolbox_container=$taskNames.toolbox;app_container=$taskNames.app;restore_container=$taskNames.restore;control_state=(Join-Path $taskPrivate 'control-state.json');handshake=(Join-Path $taskArtifacts 'transition');artifacts=$taskArtifacts;source_manifest_sha256=$NativeManifestSha256;images=@{runtime=$RuntimeImageId;backup=$BackupImageId}}
 $taskConfigFile=Join-Path $taskPrivate 'controller.json';$taskConfig | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $taskConfigFile -Encoding utf8
 foreach($taskPath in @($taskConfigFile,$taskAppEnv,$taskToolEnv)){Set-FuelPrivateFile $taskPath}
 $taskPasswordBefore=[Environment]::GetEnvironmentVariable('HOUSEHOLD_BROWSER_PASSWORD')
 try {
  [Environment]::SetEnvironmentVariable('HOUSEHOLD_BROWSER_PASSWORD',$taskBootstrap)
  $taskNode=(Get-Command node).Source
  $taskStart=[Diagnostics.ProcessStartInfo]::new();$taskStart.FileName=$taskNode;$taskStart.WorkingDirectory=$taskRoot;$taskStart.UseShellExecute=$false
  $taskStart.RedirectStandardOutput=$true;$taskStart.RedirectStandardError=$true
  if($taskWindows){$taskStart.CreateNoWindow=$true;$taskStart.WindowStyle=[Diagnostics.ProcessWindowStyle]::Hidden}
  $taskStart.ArgumentList.Add((Join-Path $taskRoot 'scripts/fuel-module-browser-controller.cjs'));$taskStart.ArgumentList.Add($taskConfigFile)
  $taskController=[Diagnostics.Process]::new();$taskController.StartInfo=$taskStart
  if(!$taskController.Start()){throw 'Could not start exact controller process'}
  $taskStdout=$taskController.StandardOutput.ReadToEndAsync();$taskStderr=$taskController.StandardError.ReadToEndAsync()

 } finally {[Environment]::SetEnvironmentVariable('HOUSEHOLD_BROWSER_PASSWORD',$taskPasswordBefore)}
 if(!$taskController.WaitForExit(25*60*1000)){throw 'Standalone browser controller exceeded25minutes'}
 (Protect-FuelLog $taskStdout.GetAwaiter().GetResult()) | Set-Content -LiteralPath (Join-Path $taskArtifacts 'controller-stdout.log') -Encoding utf8
 (Protect-FuelLog $taskStderr.GetAwaiter().GetResult()) | Set-Content -LiteralPath (Join-Path $taskArtifacts 'controller-stderr.log') -Encoding utf8
 if($taskController.ExitCode -ne 0){throw 'Standalone browser/controller failed; preserve this run report and logs'}
 $taskControlReport=Get-Content -LiteralPath (Join-Path $taskArtifacts 'controller-report.json') -Raw | ConvertFrom-Json
 if(!$taskControlReport.passed -or !$taskControlReport.acknowledged_generation){throw 'Controller did not prove real backup/purge/current generation'}
 Assert-FuelSources $taskManifest
 if((@(Invoke-FuelContract -Arguments @('browser-source',$taskRoot)) | ConvertTo-Json -Depth 5 -Compress) -ne $taskBrowserSourceJSON){throw 'Browser source changed during execution'}
 $taskReport.passed=$true;$taskReport.current_generation=$taskControlReport.acknowledged_generation;$taskReport.native_sources_unchanged=$true
} catch {
 $taskReport.failure=Protect-FuelLog "$($_.Exception.Message)"
 Write-Error -Message $taskReport.failure -ErrorAction Continue
} finally {
 if($taskController -and !$taskController.HasExited){try{$taskController.Kill($true);if(!$taskController.WaitForExit(10000)){$taskCleanupOK=$false}}catch{$taskCleanupOK=$false}}
 if($taskController){
  $taskCleanupProof=Join-Path $taskArtifacts 'controller-report.json'
  try {
   $taskProof=Get-Content -LiteralPath $taskCleanupProof -Raw | ConvertFrom-Json
   if($taskProof.run_id -ne $taskRun -or $taskProof.driver_cleanup_verified -ne $true){$taskCleanupOK=$false;$taskReport.driver_cleanup_unverified=$true}
  }catch{$taskCleanupOK=$false;$taskReport.driver_cleanup_unverified=$true}
 }
 if($taskDockerReady){foreach($taskKind in @('toolbox','app','restore')){
  $taskName=$taskNames[$taskKind]
  $taskLabel=@(& docker inspect $taskName --format '{{index .Config.Labels "household.fuel.browser.run"}}' 2>$null)
  if($LASTEXITCODE -eq 0 -and (Invoke-FuelContract -Arguments @('cleanup',$taskName,($taskLabel -join ''),$taskRun,'container')).owned){
   $null=& docker stop --time 10 $taskName 2>&1;$null=& docker rm $taskName 2>&1
   $taskRemoved=$LASTEXITCODE -eq 0;$taskReport.cleanup+=@{container=$taskName;removed=$taskRemoved};if(!$taskRemoved){$taskCleanupOK=$false}
  } elseif($taskName -in $taskCreatedContainers){$taskCleanupOK=$false;$taskReport.cleanup+=@{container=$taskName;removed=$false;state='ownership or daemon could not be verified'}}
 }
 foreach($taskVolume in $taskVolumes.Values){
  $taskLabel=@(& docker volume inspect $taskVolume --format '{{index .Labels "household.fuel.browser.run"}}' 2>$null)
  if($LASTEXITCODE -eq 0 -and (Invoke-FuelContract -Arguments @('cleanup',$taskVolume,($taskLabel -join ''),$taskRun,'volume')).owned){$null=& docker volume rm $taskVolume 2>&1;$taskRemoved=$LASTEXITCODE -eq 0;$taskReport.cleanup+=@{volume=$taskVolume;removed=$taskRemoved};if(!$taskRemoved){$taskCleanupOK=$false}}
  elseif($taskVolume -in $taskCreatedVolumes){$taskCleanupOK=$false;$taskReport.cleanup+=@{volume=$taskVolume;removed=$false;state='ownership or daemon could not be verified'}}
 }}
 if(!$taskCleanupOK){$taskReport.passed=$false;$taskReport.cleanup_failure='Driver/browser cleanup or exact run resources could not be verified; private recovery data is retained'}
 if($taskCleanupOK -and (Test-Path -LiteralPath $taskPrivate)){
  $taskResolved=(Resolve-Path -LiteralPath $taskPrivate).Path
  $taskPrivateRoot=(Resolve-Path -LiteralPath (Join-Path $taskRoot '.local')).Path
  if($taskResolved.StartsWith($taskPrivateRoot+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase) -and (Split-Path -Leaf $taskResolved) -eq "fuel-browser-auth-$taskRun"){
   try {Remove-Item -LiteralPath $taskResolved -Recurse -Force} catch {$taskReport.passed=$false;$taskReport.cleanup_failure='Exact private run directory could not be removed'}
  }else{$taskReport.passed=$false;$taskReport.cleanup_failure='Private cleanup target did not match the validated run path'}
 }
 if($taskLock){$taskLock.Dispose()}
 if(Test-Path -LiteralPath $taskArtifacts){
  # Request/ack are operational handoff artifacts, never a public auth export.
  foreach($taskSuffix in @('.request.json','.ack.json','.ack.pending')){$taskFile=Join-Path $taskArtifacts ('transition'+$taskSuffix);if(Test-Path -LiteralPath $taskFile){Remove-Item -LiteralPath $taskFile -Force}}
  $taskReport | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $taskArtifacts 'standalone-report.json') -Encoding utf8
 }
 foreach($taskKey in $taskGoEnvironment.Keys){[Environment]::SetEnvironmentVariable($taskKey,$taskGoEnvironment[$taskKey])}
}
if(!$taskReport.passed){exit 1}
Write-Output "Standalone Fuel browser passed; evidence: $taskArtifacts"
