param([string]$BackupImage='household-backup:local')
$ErrorActionPreference='Stop'
$taskRoot=Split-Path -Parent $PSScriptRoot
Push-Location $taskRoot
try {
    # Only the disposable recovery service is used; the live Household database is never a test source/target.
    docker compose -f compose.yaml -f compose.backup.yaml --profile recovery up -d --wait restore-db
    if($LASTEXITCODE -ne 0){throw 'Recovery database unavailable'}
    $taskDirectory=Join-Path $taskRoot '.local/recovery-test'
    $taskSecrets=Join-Path $taskDirectory 'secrets'
    $taskUploads=Join-Path $taskDirectory 'uploads'
    New-Item -ItemType Directory -Force $taskSecrets,$taskUploads,'outputs/recovery-review' | Out-Null
    $taskActor=[Security.Principal.WindowsIdentity]::GetCurrent().Name
    & icacls $taskSecrets /inheritance:r /grant:r "${taskActor}:(OI)(CI)F" 'SYSTEM:(OI)(CI)F' /Q | Out-Null
    if($LASTEXITCODE -ne 0){throw 'Could not restrict fixture secret directory'}
    foreach($taskExistingSecret in (Get-ChildItem -LiteralPath $taskSecrets -File)){
      & icacls $taskExistingSecret.FullName /inheritance:e /grant:r "${taskActor}:F" 'SYSTEM:F' /Q | Out-Null
      if($LASTEXITCODE -ne 0){throw 'Could not set fixture secret permissions'}
    }
    $taskOwner=[IO.File]::ReadAllText((Join-Path $taskRoot '.local/secrets/owner_password'))
    $taskRandom={ [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)) }
    [IO.File]::WriteAllText((Join-Path $taskSecrets 'source_url'),"postgres://household_owner:${taskOwner}@restore-db:5432/household_fixture?sslmode=disable")
    [IO.File]::WriteAllText((Join-Path $taskSecrets 'target_url'),"postgres://household_owner:${taskOwner}@restore-db:5432/household_restore?sslmode=disable")
    foreach($taskSecretName in @('csrf','encryption','rate','restic')){[IO.File]::WriteAllText((Join-Path $taskSecrets $taskSecretName),(& $taskRandom))}
    $taskEnvironment=Join-Path $taskDirectory 'fixture.env'
    [IO.File]::WriteAllLines($taskEnvironment,@(
      'HOUSEHOLD_ENV=development','HOUSEHOLD_ORIGIN=http://localhost:8102',
      'HOUSEHOLD_DATABASE_URL_FILE=/run/secrets/source_url','HOUSEHOLD_RESTORE_DATABASE_URL_FILE=/run/secrets/target_url',
      'HOUSEHOLD_CSRF_SECRET_FILE=/run/secrets/csrf','HOUSEHOLD_ENCRYPTION_KEY_FILE=/run/secrets/encryption',
      'HOUSEHOLD_RATE_SECRET_FILE=/run/secrets/rate','HOUSEHOLD_UPLOAD_DIR=/data/uploads',
      'HOUSEHOLD_BACKUP_WORK=/work','RESTIC_REPOSITORY=/repository','RESTIC_PASSWORD_FILE=/run/secrets/restic','RESTIC_CACHE_DIR=/tmp/restic-cache'))
    $taskSuffix=[guid]::NewGuid().ToString('N').Substring(0,12)
    $taskRepoVolume="household-recovery-test-repository-$taskSuffix"
    $taskWorkVolume="household-recovery-test-work-$taskSuffix"
    $taskDockerArguments=@('run','--rm','--network','household_database','--env-file',$taskEnvironment)
    foreach($taskSecretName in @('source_url','target_url','csrf','encryption','rate','restic')){
      $taskDockerArguments+=@('--mount',"type=bind,source=$(Join-Path $taskSecrets $taskSecretName),target=/run/secrets/$taskSecretName,readonly")
    }
    $taskDockerArguments+=@('--mount',"type=bind,source=$taskUploads,target=/data/uploads,readonly",
      '--mount',"type=volume,source=$taskRepoVolume,target=/repository",
      '--mount',"type=volume,source=$taskWorkVolume,target=/work",$BackupImage)
    docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db createdb -U postgres -O household_owner household_fixture
    if($LASTEXITCODE -ne 0){throw 'Fixture database already exists or could not be created; refusing to overwrite it'}
    try {
      $taskRehearsalTimer=[Diagnostics.Stopwatch]::StartNew()
      & docker @taskDockerArguments migrate
      if($LASTEXITCODE -ne 0){throw 'Fixture migrations failed'}
      $taskHousehold=[guid]::NewGuid().ToString();$taskUser=[guid]::NewGuid().ToString();$taskList=[guid]::NewGuid().ToString();$taskItem=[guid]::NewGuid().ToString();$taskAttachment=[guid]::NewGuid().ToString()
      $taskProduct=[guid]::NewGuid().ToString();$taskLocation=[guid]::NewGuid().ToString();$taskLot=[guid]::NewGuid().ToString()
      $taskOriginal=[guid]::NewGuid().ToString();$taskPreview=[guid]::NewGuid().ToString()
      # A tiny valid PNG is sufficient to exercise binary integrity and private-file metadata.
      $taskPng=[Convert]::FromBase64String('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jN1sAAAAASUVORK5CYII=')
      [IO.File]::WriteAllBytes((Join-Path $taskUploads $taskOriginal),$taskPng)
      [IO.File]::WriteAllBytes((Join-Path $taskUploads $taskPreview),$taskPng)
      $taskHash=[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($taskPng)).ToLowerInvariant()
      $taskSQL=@"
BEGIN;
SET ROLE household_owner;
INSERT INTO core.households(id,name) VALUES('$taskHousehold','Synthetic recovery fixture');
INSERT INTO core.users(id,username,display_name,password_hash) VALUES('$taskUser','recovery_fixture','Synthetic fixture','disabled-fixture-with-no-login');
INSERT INTO core.memberships(household_id,user_id,role) VALUES('$taskHousehold','$taskUser','admin');
INSERT INTO core.module_installations(module_id,schema_name,last_seen_build) VALUES('shopping','shopping','test');
INSERT INTO core.module_states(household_id,module_id,state) VALUES('$taskHousehold','shopping','enabled');
INSERT INTO shopping.lists(id,household_id,name,created_by) VALUES('$taskList','$taskHousehold','Recovery verification','$taskUser');
INSERT INTO shopping.items(id,household_id,list_id,name,quantity,unit_code,created_by) VALUES('$taskItem','$taskHousehold','$taskList','Synthetic rice',2.5,'kg','$taskUser');
INSERT INTO pantry.products(id,household_id,name,unit_code,created_by) VALUES('$taskProduct','$taskHousehold','Recovery stock','kg','$taskUser');
INSERT INTO pantry.locations(id,household_id,name,created_by) VALUES('$taskLocation','$taskHousehold','Recovery shelf','$taskUser');
INSERT INTO pantry.lots(id,household_id,product_id,location_id,received_on,created_by) VALUES('$taskLot','$taskHousehold','$taskProduct','$taskLocation','2026-01-01','$taskUser');
INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES('$taskHousehold','$taskLot',9007199254.740993,'receive','$taskUser'),('$taskHousehold','$taskLot',-0.000001,'consume','$taskUser');
INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by) VALUES('$taskAttachment','$taskHousehold','shopping',1,'items','$taskItem','$taskOriginal','fixture.png','image/png',$($taskPng.Length),decode('$taskHash','hex'),'$taskUser');
INSERT INTO core.attachment_derivatives(attachment_id,variant,storage_key,media_type,width,height,size_bytes,sha256) VALUES('$taskAttachment','preview','$taskPreview','image/png',1,1,$($taskPng.Length),decode('$taskHash','hex'));
INSERT INTO shopping.checkouts(household_id,list_id,request_hash,item_count,snapshot,created_by) VALUES('$taskHousehold','$taskList',decode(repeat('cd',32),'hex'),1,jsonb_build_object('restored_completion','once-only','quantity','9007199254.740992'),'$taskUser');
COMMIT;
"@
      $taskSQL | docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_fixture -v ON_ERROR_STOP=1
      if($LASTEXITCODE -ne 0){throw 'Fixture insertion failed'}
      $taskBackupTimer=[Diagnostics.Stopwatch]::StartNew()
      $taskBackupOutput=& docker @taskDockerArguments backup
      if($LASTEXITCODE -ne 0){throw 'Fixture encrypted backup failed'}
      $taskBackupTimer.Stop()
      $taskEvent=$taskBackupOutput | Where-Object {$_ -match '"snapshot"'} | Select-Object -Last 1 | ConvertFrom-Json
      $taskSnapshot=$taskEvent.snapshot
      if($taskSnapshot -notmatch '^[0-9a-f]{64}$'){throw 'No exact snapshot ID returned'}
      $taskRestoreTimer=[Diagnostics.Stopwatch]::StartNew()
      & docker @taskDockerArguments restore-check $taskSnapshot
      if($LASTEXITCODE -ne 0){throw 'Fixture restore or binary hash verification failed'}
      $taskRestored=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT name||'|'||quantity::text||'|'||unit_code FROM shopping.items WHERE id='$taskItem'"
      if($LASTEXITCODE -ne 0 -or $taskRestored -ne 'Synthetic rice|2.500000|kg'){throw 'Restored domain values differ'}
      $taskStock=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT quantity::text||'|'||movement_count FROM pantry.lot_balances WHERE lot_id='$taskLot'"
      if($LASTEXITCODE -ne 0 -or $taskStock -ne '9007199254.740992|2'){throw 'Restored stock projection was lost or counted twice'}
      $taskCheckout=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT (snapshot->>'restored_completion')||'|'||(snapshot->>'quantity')||'|'||item_count::text FROM shopping.checkouts WHERE list_id='$taskList'"
      if($LASTEXITCODE -ne 0 -or $taskCheckout -ne 'once-only|9007199254.740992|1'){throw 'Restored durable completion differs'}
      $taskRestoreTimer.Stop()
      $taskRehearsalTimer.Stop()
      [ordered]@{passed=$true;source='disposable household_fixture';target='disposable household_restore';snapshot=$taskSnapshot;domainValue=$taskRestored;stockProjection=$taskStock;durableCompletion=$taskCheckout;hashedFiles=2;includesOriginalAndPreview=$true;backupSeconds=[Math]::Round($taskBackupTimer.Elapsed.TotalSeconds,2);restoreAndVerifySeconds=[Math]::Round($taskRestoreTimer.Elapsed.TotalSeconds,2);rehearsalSeconds=[Math]::Round($taskRehearsalTimer.Elapsed.TotalSeconds,2);scope='Small synthetic local fixture; not acceptance of a production RTO/RPO';verifiedAt=[DateTime]::UtcNow.ToString('o')} | ConvertTo-Json | Set-Content outputs/recovery-review/report.json
      Get-Content outputs/recovery-review/report.json
    } finally {
      docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db dropdb -U postgres household_fixture
      docker volume rm $taskRepoVolume $taskWorkVolume | Out-Null
    }
} finally {Pop-Location}
