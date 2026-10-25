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
      $taskPack=[guid]::NewGuid().ToString();$taskStockMovement=[guid]::NewGuid().ToString();$taskStockOperation=[guid]::NewGuid().ToString();$taskStockKey=[guid]::NewGuid().ToString()
      $taskRecipe=[guid]::NewGuid().ToString()
      $taskPlanningBinding=[guid]::NewGuid().ToString()
      $taskIngredientCatalog=[guid]::NewGuid().ToString();$taskIngredientBinding=[guid]::NewGuid().ToString()
      $taskCooking=[guid]::NewGuid().ToString();$taskCooked=[guid]::NewGuid().ToString();$taskCookingRecord=[guid]::NewGuid().ToString()
      $taskCookingPosting=[guid]::NewGuid().ToString();$taskCookingPostingKey=[guid]::NewGuid().ToString()
      $taskBatchDraft=[guid]::NewGuid().ToString();$taskBatchLine=[guid]::NewGuid().ToString();$taskBatchReceipt=[guid]::NewGuid().ToString()
      $taskBatchLot=[guid]::NewGuid().ToString();$taskBatchMovement=[guid]::NewGuid().ToString();$taskPrivateDraft=[guid]::NewGuid().ToString()
      $taskPlan=[guid]::NewGuid().ToString();$taskCopiedPlan=[guid]::NewGuid().ToString();$taskMeal=[guid]::NewGuid().ToString();$taskCopyKey=[guid]::NewGuid().ToString()
      $taskReservationPlan=[guid]::NewGuid().ToString();$taskReservationMeal=[guid]::NewGuid().ToString();$taskReservation=[guid]::NewGuid().ToString();$taskReservationKey=[guid]::NewGuid().ToString()
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
INSERT INTO core.module_installations(module_id,schema_name,last_seen_build) VALUES('pantry','pantry','test');
INSERT INTO core.module_states(household_id,module_id,state) VALUES('$taskHousehold','shopping','enabled');
INSERT INTO core.module_states(household_id,module_id,state) VALUES('$taskHousehold','pantry','enabled');
INSERT INTO core.module_installations(module_id,schema_name,last_seen_build) VALUES('mealplan','mealplan','test');
INSERT INTO core.module_states(household_id,module_id,state) VALUES('$taskHousehold','mealplan','enabled');
INSERT INTO shopping.lists(id,household_id,name,created_by) VALUES('$taskList','$taskHousehold','Recovery verification','$taskUser');
INSERT INTO shopping.items(id,household_id,list_id,name,quantity,unit_code,created_by) VALUES('$taskItem','$taskHousehold','$taskList','Synthetic rice',2.5,'kg','$taskUser');
INSERT INTO pantry.products(id,household_id,name,unit_code,low_stock_threshold,target_stock,created_by) VALUES('$taskProduct','$taskHousehold','Recovery stock','kg',1,3,'$taskUser');
INSERT INTO pantry.products(household_id,name,unit_code,created_by) VALUES('$taskHousehold','Recovery no target','kg','$taskUser');
INSERT INTO pantry.locations(id,household_id,name,created_by) VALUES('$taskLocation','$taskHousehold','Recovery shelf','$taskUser');
INSERT INTO pantry.lots(id,household_id,product_id,location_id,received_on,opened_on,after_open_days,after_open_source,after_open_note,created_by) VALUES('$taskLot','$taskHousehold','$taskProduct','$taskLocation','2026-01-01','2026-10-24',3,'manufacturer','Synthetic package label','$taskUser');
INSERT INTO pantry.packs(id,household_id,product_id,label,quantity,unit_code,created_by) VALUES('$taskPack','$taskHousehold','$taskProduct','Recovery exact pack',500.001000,'g','$taskUser');
INSERT INTO pantry.barcodes(household_id,product_id,pack_id,code,created_by) VALUES('$taskHousehold','$taskProduct','$taskPack','00073Recovery','$taskUser');
INSERT INTO pantry.stock_movements(household_id,lot_id,quantity_delta,reason,created_by) VALUES('$taskHousehold','$taskLot',9007199254.740993,'receive','$taskUser');
INSERT INTO pantry.stock_movements(id,household_id,lot_id,quantity_delta,reason,created_by,correlation_id) VALUES('$taskStockMovement','$taskHousehold','$taskLot',-0.000001,'consume','$taskUser','$taskCookingPosting');
INSERT INTO pantry.stock_operations(id,household_id,lot_id,movement_id,kind,request_key,request_hash,snapshot,created_by) VALUES('$taskStockOperation','$taskHousehold','$taskLot','$taskStockMovement','consume','$taskStockKey',decode(repeat('ef',32),'hex'),jsonb_build_object('quantity','0.000001','stock_after','9007199254.740992','pack_label','Recovery exact pack'),'$taskUser');
INSERT INTO core.attachments(id,household_id,module_id,module_generation,resource_type,resource_id,storage_key,original_name,media_type,size_bytes,sha256,uploaded_by) VALUES('$taskAttachment','$taskHousehold','shopping',1,'items','$taskItem','$taskOriginal','fixture.png','image/png',$($taskPng.Length),decode('$taskHash','hex'),'$taskUser');
INSERT INTO core.attachment_derivatives(attachment_id,variant,storage_key,media_type,width,height,size_bytes,sha256) VALUES('$taskAttachment','preview','$taskPreview','image/png',1,1,$($taskPng.Length),decode('$taskHash','hex'));
INSERT INTO shopping.checkouts(household_id,list_id,request_hash,item_count,snapshot,created_by) VALUES('$taskHousehold','$taskList',decode(repeat('cd',32),'hex'),1,jsonb_build_object('restored_completion','once-only','quantity','9007199254.740992'),'$taskUser');
INSERT INTO recipes.recipes(id,household_id,title,base_servings,created_by) VALUES('$taskRecipe','$taskHousehold','Recovery recipe',2.500001,'$taskUser');
INSERT INTO recipes.ingredient_catalog(id,household_id,name,created_by) VALUES('$taskIngredientCatalog','$taskHousehold','Recovery rice identity','$taskUser');
INSERT INTO recipes.ingredient_aliases(household_id,catalog_id,name,created_by) VALUES('$taskHousehold','$taskIngredientCatalog','Recovery rice alias','$taskUser');
INSERT INTO core.ingredient_product_bindings(id,household_id,catalog_id,source_generation,product_id,target_generation,created_by,changed_by) VALUES('$taskIngredientBinding','$taskHousehold','$taskIngredientCatalog',1,'$taskProduct',1,'$taskUser','$taskUser');
INSERT INTO recipes.ingredients(household_id,recipe_id,name,quantity,unit_code,catalog_id,created_by) VALUES('$taskHousehold','$taskRecipe','Exact ingredient',9007199254.740993,'g','$taskIngredientCatalog','$taskUser');
INSERT INTO recipes.steps(household_id,recipe_id,instruction,position,created_by) VALUES('$taskHousehold','$taskRecipe','Synthetic preparation',1,'$taskUser');
COMMIT;
BEGIN;
SET ROLE household_owner;
INSERT INTO recipes.cooking_sessions(id,household_id,user_id,source_version_id,servings) SELECT s.id,'$taskHousehold','$taskUser',v.id,2.500001 FROM recipes.versions v CROSS JOIN (VALUES('$taskCooking'::uuid),('$taskCooked'::uuid)) s(id) WHERE v.recipe_id='$taskRecipe' AND v.revision=1;
INSERT INTO recipes.cooking_timers(household_id,session_id,name,duration_seconds,state,deadline) VALUES('$taskHousehold','$taskCooking','Persisted deadline',120,'running','2026-10-25T01:30:00Z');
INSERT INTO recipes.cooking_timers(household_id,session_id,name,duration_seconds,state,remaining_millis) VALUES('$taskHousehold','$taskCooking','Persisted pause',120,'paused',77501);
INSERT INTO recipes.cooking_records(id,household_id,session_id,user_id,source_version_id,actual_servings,cooked_on,notes) SELECT '$taskCookingRecord','$taskHousehold','$taskCooked','$taskUser',v.id,3.000001,'2026-10-24','Synthetic confirmed note' FROM recipes.versions v WHERE v.recipe_id='$taskRecipe' AND v.revision=1;
UPDATE recipes.cooking_sessions SET state='completed' WHERE id='$taskCooked';
INSERT INTO core.cooking_stock_postings(id,household_id,recipes_generation,cooking_record_id,source_version_id,pantry_generation,created_by,request_key,request_hash,decision_count,movement_count,mode)
 SELECT '$taskCookingPosting','$taskHousehold',1,'$taskCookingRecord',source_version_id,1,'$taskUser','$taskCookingPostingKey',decode(repeat('cd',32),'hex'),1,1,'stock' FROM recipes.cooking_records WHERE id='$taskCookingRecord';
INSERT INTO pantry.cooking_stock_receipts(id,household_id,snapshot)
 SELECT '$taskCookingPosting','$taskHousehold',jsonb_build_object('source',jsonb_build_object('cooking_record_id','$taskCookingRecord','source_version_id',source_version_id,'actual_servings',actual_servings::text,'title','Recovery recipe'),
 'decisions',jsonb_build_array(jsonb_build_object('name','Exact ingredient','action','consume','actual','0.000001','reason','Synthetic measured quantity'))) FROM recipes.cooking_records WHERE id='$taskCookingRecord';
INSERT INTO pantry.cooking_stock_movements(id,household_id,posting_id,lot_id,operation_id,quantity,unit_code) VALUES('$taskStockMovement','$taskHousehold','$taskCookingPosting','$taskLot','$taskStockOperation',0.000001,'kg');
INSERT INTO pantry.stock_batch_drafts(id,household_id,owner_id,generation,kind,title,payload) VALUES('$taskBatchDraft','$taskHousehold','$taskUser',1,'intake','Synthetic batch intake',jsonb_build_object('lines',jsonb_build_array(jsonb_build_object('id','$taskBatchLine','quantity','0.125001'))));
INSERT INTO pantry.stock_batch_drafts(id,household_id,owner_id,generation,kind,title,payload) VALUES('$taskPrivateDraft','$taskHousehold','$taskUser',1,'intake','Private incomplete progress',jsonb_build_object('lines',jsonb_build_array(jsonb_build_object('id',gen_random_uuid(),'quantity','1,'))));
INSERT INTO pantry.stock_batch_receipts(id,household_id,draft_id,generation,draft_version,kind,created_by,request_key,request_hash,line_count,movement_count,snapshot)
 VALUES('$taskBatchReceipt','$taskHousehold','$taskBatchDraft',1,1,'intake','$taskUser',gen_random_uuid(),decode(repeat('bc',32),'hex'),1,1,jsonb_build_object('kind','intake','lines',jsonb_build_array(jsonb_build_object('line_id','$taskBatchLine','lot_id','$taskBatchLot','counted','0.125001','unit','kg'))));
INSERT INTO pantry.lots(id,household_id,product_id,location_id,received_on,notes,created_by) VALUES('$taskBatchLot','$taskHousehold','$taskProduct','$taskLocation','2026-10-25','Synthetic separate intake lot','$taskUser');
INSERT INTO pantry.stock_movements(id,household_id,lot_id,quantity_delta,reason,created_by,correlation_id) VALUES('$taskBatchMovement','$taskHousehold','$taskBatchLot',0.125001,'receive','$taskUser','$taskBatchReceipt');
INSERT INTO pantry.stock_batch_lines(household_id,receipt_id,line_id,lot_id,movement_id,before_quantity,counted_quantity,quantity_delta,unit_code) VALUES('$taskHousehold','$taskBatchReceipt','$taskBatchLine','$taskBatchLot','$taskBatchMovement',0,0.125001,0.125001,'kg');
UPDATE pantry.stock_batch_drafts SET state='posted' WHERE id='$taskBatchDraft';
INSERT INTO mealplan.plans(id,household_id,name,starts_on,ends_on,created_by) VALUES('$taskPlan','$taskHousehold','Original planning week','2026-10-22','2026-10-28','$taskUser');
INSERT INTO mealplan.plans(id,household_id,name,starts_on,ends_on,created_by,copy_source_plan,copy_key,copy_request_hash) VALUES('$taskCopiedPlan','$taskHousehold','Independent copied week','2026-10-29','2026-11-04','$taskUser','$taskPlan','$taskCopyKey',decode(repeat('ab',32),'hex'));
INSERT INTO mealplan.meals(id,household_id,plan_id,planned_on,title,servings,kind,status,created_by) VALUES('$taskMeal','$taskHousehold','$taskCopiedPlan','2026-10-30','Frozen component meal',3.000001,'recipe','open','$taskUser');
INSERT INTO mealplan.components(household_id,meal_id,position,servings,source_recipe_id,source_version_id,source_generation,source_revision,snapshot,created_by)
 SELECT '$taskHousehold','$taskMeal',s.position,s.servings,'$taskRecipe',v.id,1,v.revision,v.snapshot,'$taskUser' FROM recipes.versions v CROSS JOIN (VALUES(0,1.500001::numeric),(1,2.500001::numeric)) s(position,servings) WHERE v.recipe_id='$taskRecipe' AND v.revision=1;
INSERT INTO core.planning_ingredient_bindings(id,household_id,mealplan_generation,plan_id,meal_id,component_id,ingredient_id,source_hash,product_id,pantry_generation,unit_code,created_by,changed_by)
 SELECT '$taskPlanningBinding','$taskHousehold',1,'$taskCopiedPlan','$taskMeal',c.id,(c.snapshot->'ingredients'->0->>'id')::uuid,sha256(convert_to(c.snapshot::text,'UTF8')),'$taskProduct',1,'kg','$taskUser','$taskUser'
 FROM mealplan.components c WHERE c.household_id='$taskHousehold' AND c.meal_id='$taskMeal' AND c.position=0;
INSERT INTO mealplan.plans(id,household_id,name,starts_on,ends_on,created_by)
 SELECT '$taskReservationPlan','$taskHousehold','Recovery reservation',(clock_timestamp() AT TIME ZONE timezone)::date+1,(clock_timestamp() AT TIME ZONE timezone)::date+1,'$taskUser' FROM core.households WHERE id='$taskHousehold';
INSERT INTO mealplan.meals(id,household_id,plan_id,planned_on,title,servings,created_by)
 SELECT '$taskReservationMeal','$taskHousehold','$taskReservationPlan',starts_on,'Recovery reserved meal',1,'$taskUser' FROM mealplan.plans WHERE id='$taskReservationPlan';
INSERT INTO core.meal_reservations(id,household_id,mealplan_generation,plan_id,meal_id,pantry_generation,source_basis,created_by,request_key,request_hash,allocation_count,expires_at)
 VALUES('$taskReservation','$taskHousehold',1,'$taskReservationPlan','$taskReservationMeal',1,mealplan.reservation_basis('$taskHousehold','$taskReservationMeal'),'$taskUser','$taskReservationKey',decode(repeat('9a',32),'hex'),1,clock_timestamp()+interval '1 hour');
INSERT INTO core.meal_reservation_allocations(household_id,reservation_id,product_id,lot_id,unit_code,quantity) VALUES('$taskHousehold','$taskReservation','$taskProduct','$taskLot','kg',0.250001);
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
      $taskTarget=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT low_stock_threshold::text||'|'||target_stock::text||'|'||pantry.replenishment_ready()::text||'|'||(SELECT target_stock IS NULL FROM pantry.products WHERE household_id='$taskHousehold' AND name='Recovery no target')::text FROM pantry.products WHERE id='$taskProduct'"
      if($LASTEXITCODE -ne 0 -or $taskTarget -ne '1.000000|3.000000|true|true'){throw 'Restored minimum, target, unknown target or target readiness differs'}
      $taskPantryDaily=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT p.quantity::text||'|'||b.code||'|'||(l.opened_on+l.after_open_days)::text||'|'||l.after_open_source||'|'||(o.snapshot->>'quantity')||'|'||(o.snapshot->>'stock_after') FROM pantry.stock_operations o JOIN pantry.lots l ON l.id=o.lot_id JOIN pantry.packs p ON p.product_id=l.product_id JOIN pantry.barcodes b ON b.pack_id=p.id WHERE o.id='$taskStockOperation'"
      if($LASTEXITCODE -ne 0 -or $taskPantryDaily -ne '500.001000|00073Recovery|2026-10-27|manufacturer|0.000001|9007199254.740992'){throw 'Restored pack, leading-zero barcode, opening hint or durable stock receipt differs'}
      $taskStockDuplicate=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "WITH attempted AS (INSERT INTO pantry.stock_operations(household_id,lot_id,movement_id,kind,request_key,request_hash,snapshot,created_by) SELECT household_id,lot_id,movement_id,kind,request_key,request_hash,snapshot,created_by FROM pantry.stock_operations WHERE id='$taskStockOperation' ON CONFLICT(household_id,created_by,request_key) DO NOTHING RETURNING id) SELECT count(*) FROM attempted"
      if($LASTEXITCODE -ne 0 -or $taskStockDuplicate -ne '0'){throw 'Restored durable stock-operation uniqueness failed'}
      $taskCheckout=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT (snapshot->>'restored_completion')||'|'||(snapshot->>'quantity')||'|'||item_count::text FROM shopping.checkouts WHERE list_id='$taskList'"
      if($LASTEXITCODE -ne 0 -or $taskCheckout -ne 'once-only|9007199254.740992|1'){throw 'Restored durable completion differs'}
      $taskRecipeBefore=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT count(*)::text||'|'||min(snapshot->'ingredients'->0->>'quantity') FROM recipes.versions WHERE recipe_id='$taskRecipe'"
      if($LASTEXITCODE -ne 0 -or $taskRecipeBefore -ne '1|9007199254.740993'){throw 'Restored recipe version lost exact history'}
      $taskIngredientIdentity=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT c.name||'|'||a.name||'|'||i.catalog_label||'|'||b.version::text||'|'||b.source_generation::text||'|'||b.target_generation::text||'|'||(b.product_id='$taskProduct')::text||'|'||(v.snapshot->'ingredients'->0->>'catalog_id'='$taskIngredientCatalog')::text FROM recipes.ingredient_catalog c JOIN recipes.ingredient_aliases a ON a.catalog_id=c.id JOIN recipes.ingredients i ON i.catalog_id=c.id JOIN core.ingredient_product_bindings b ON b.catalog_id=c.id JOIN recipes.versions v ON v.recipe_id=i.recipe_id AND v.revision=1 WHERE c.id='$taskIngredientCatalog'"
      if($LASTEXITCODE -ne 0 -or $taskIngredientIdentity -ne 'Recovery rice identity|Recovery rice alias|Recovery rice identity|1|1|1|true|true'){throw 'Restored catalogue, alias, captured label, generation-bound binding or source snapshot differs'}
      $taskCookingBefore=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT count(*)::text||'|'||count(*) FILTER(WHERE state='running' AND deadline='2026-10-25T01:30:00Z')::text||'|'||count(*) FILTER(WHERE state='paused' AND remaining_millis=77501)::text FROM recipes.cooking_timers WHERE session_id='$taskCooking'"
      if($LASTEXITCODE -ne 0 -or $taskCookingBefore -ne '2|1|1'){throw 'Restored timer deadline or paused milliseconds changed'}
      $taskCookingResult=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT s.state||'|'||r.actual_servings::text||'|'||r.notes||'|'||v.revision::text FROM recipes.cooking_records r JOIN recipes.cooking_sessions s ON s.id=r.session_id JOIN recipes.versions v ON v.id=r.source_version_id WHERE r.id='$taskCookingRecord'"
      if($LASTEXITCODE -ne 0 -or $taskCookingResult -ne 'completed|3.000001|Synthetic confirmed note|1'){throw 'Restored immutable cooking result differs'}
      $taskCookingDuplicate=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "WITH attempted AS (INSERT INTO recipes.cooking_records(household_id,session_id,user_id,source_version_id,actual_servings,cooked_on) SELECT household_id,session_id,user_id,source_version_id,actual_servings,cooked_on FROM recipes.cooking_records WHERE id='$taskCookingRecord' ON CONFLICT(household_id,session_id) DO NOTHING RETURNING id) SELECT count(*) FROM attempted"
      if($LASTEXITCODE -ne 0 -or $taskCookingDuplicate -ne '0'){throw 'Restored completion uniqueness failed'}
      $taskCookingPostingResult=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT p.mode||'|'||p.movement_count::text||'|'||m.quantity::text||'|'||(r.snapshot->'source'->>'actual_servings')||'|'||(r.snapshot->'decisions'->0->>'actual')||'|'||(p.cooking_record_id='$taskCookingRecord')::text FROM core.cooking_stock_postings p JOIN pantry.cooking_stock_receipts r ON r.id=p.id JOIN pantry.cooking_stock_movements m ON m.posting_id=p.id WHERE p.id='$taskCookingPosting'"
      if($LASTEXITCODE -ne 0 -or $taskCookingPostingResult -ne 'stock|1|0.000001|3.000001|0.000001|true'){throw 'Restored cooking stock proof, receipt or exact movement differs'}
      $taskCookingPostingDuplicate=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "WITH attempted AS (INSERT INTO core.cooking_stock_postings(household_id,recipes_generation,cooking_record_id,source_version_id,pantry_generation,created_by,request_key,request_hash,decision_count,movement_count,mode) SELECT household_id,recipes_generation,cooking_record_id,source_version_id,pantry_generation,created_by,gen_random_uuid(),request_hash,decision_count,movement_count,mode FROM core.cooking_stock_postings WHERE id='$taskCookingPosting' ON CONFLICT(household_id,recipes_generation,cooking_record_id) DO NOTHING RETURNING id) SELECT count(*) FROM attempted"
      if($LASTEXITCODE -ne 0 -or $taskCookingPostingDuplicate -ne '0'){throw 'Restored permanent cooking-stock uniqueness failed'}
      $taskPantryBatch=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT d.state||'|'||r.kind||'|'||r.line_count::text||'|'||r.movement_count::text||'|'||l.counted_quantity::text||'|'||m.quantity_delta::text||'|'||b.quantity::text FROM pantry.stock_batch_receipts r JOIN pantry.stock_batch_drafts d ON d.id=r.draft_id JOIN pantry.stock_batch_lines l ON l.receipt_id=r.id JOIN pantry.stock_movements m ON m.id=l.movement_id JOIN pantry.lot_balances b ON b.lot_id=l.lot_id WHERE r.id='$taskBatchReceipt'"
      if($LASTEXITCODE -ne 0 -or $taskPantryBatch -ne 'posted|intake|1|1|0.125001|0.125001|0.125001'){throw 'Restored intake draft, receipt, journal or projection differs'}
      $taskPantryDraft=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT state||'|'||version::text||'|'||(payload->'lines'->0->>'quantity')||'|'||(owner_id='$taskUser')::text FROM pantry.stock_batch_drafts WHERE id='$taskPrivateDraft'"
      if($LASTEXITCODE -ne 0 -or $taskPantryDraft -ne 'active|1|1,|true'){throw 'Restored private incomplete draft differs'}
      $taskPantryBatchDuplicate=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT count(*) FROM pg_constraint WHERE conrelid='pantry.stock_batch_receipts'::regclass AND contype='u' AND pg_get_constraintdef(oid)='UNIQUE (household_id, draft_id)'"
      if($LASTEXITCODE -ne 0 -or $taskPantryBatchDuplicate -ne '1'){throw 'Restored durable same-draft uniqueness constraint missing'}
      $taskPantryBatchIndexes=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT count(*) FROM pg_indexes WHERE schemaname='pantry' AND indexname IN('pantry_inventory_page','pantry_batch_lot_history','pantry_batch_owner')"
      if($LASTEXITCODE -ne 0 -or $taskPantryBatchIndexes -ne '3'){throw 'Restored batch query indexes missing'}
      docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -v ON_ERROR_STOP=1 -c "BEGIN; SET ROLE household_owner; UPDATE recipes.recipes SET title='After restore',version=version+1 WHERE id='$taskRecipe'; UPDATE recipes.ingredients SET quantity=9007199254.740992 WHERE recipe_id='$taskRecipe'; COMMIT;"
      if($LASTEXITCODE -ne 0){throw 'Recipe edit after restore failed'}
      $taskRecipeAfter=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT count(*)::text||'|'||max(snapshot->'ingredients'->0->>'quantity') FILTER(WHERE revision=1)||'|'||max(snapshot->'ingredients'->0->>'quantity') FILTER(WHERE revision=2) FROM recipes.versions WHERE recipe_id='$taskRecipe'"
      if($LASTEXITCODE -ne 0 -or $taskRecipeAfter -ne '2|9007199254.740993|9007199254.740992'){throw 'Recipe history after restore was overwritten or not appended once'}
      $taskPlanning=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT count(*)::text||'|'||min(c.servings)::text||'|'||max(c.servings)::text||'|'||min(c.snapshot->'ingredients'->0->>'quantity')||'|'||bool_and(c.source_revision=1 AND m.status='open' AND p.copy_source_plan='$taskPlan' AND p.copy_key='$taskCopyKey' AND p.copy_request_hash=decode(repeat('ab',32),'hex'))::text FROM mealplan.components c JOIN mealplan.meals m ON m.id=c.meal_id JOIN mealplan.plans p ON p.id=m.plan_id WHERE m.id='$taskMeal'"
      if($LASTEXITCODE -ne 0 -or $taskPlanning -ne '2|1.500001|2.500001|9007199254.740993|true'){throw 'Restored plan lost component portions, frozen source or durable copy provenance'}
      $taskPlanningDuplicate=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "WITH attempted AS (INSERT INTO mealplan.plans(household_id,name,starts_on,ends_on,created_by,copy_source_plan,copy_key,copy_request_hash) VALUES('$taskHousehold','Repeated copy','2026-10-29','2026-11-04','$taskUser','$taskPlan','$taskCopyKey',decode(repeat('ab',32),'hex')) ON CONFLICT(household_id,created_by,copy_source_plan,copy_key) DO NOTHING RETURNING id) SELECT count(*) FROM attempted"
      if($LASTEXITCODE -ne 0 -or $taskPlanningDuplicate -ne '0'){throw 'Restored durable week-copy uniqueness failed'}
      $taskReservationResult=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT r.revision::text||'|'||r.version::text||'|'||a.quantity::text||'|'||(r.state='active' AND r.expires_at>clock_timestamp())::text||'|'||(r.request_key='$taskReservationKey' AND r.request_hash=decode(repeat('9a',32),'hex') AND r.source_basis=mealplan.reservation_basis(r.household_id,r.meal_id))::text||'|'||core.meal_reservations_ready()::text FROM core.meal_reservations r JOIN core.meal_reservation_allocations a ON a.household_id=r.household_id AND a.reservation_id=r.id WHERE r.id='$taskReservation'"
      if($LASTEXITCODE -ne 0 -or $taskReservationResult -ne '1|1|0.250001|true|true|true'){throw 'Restored reservation lost exact amount, monotone revision, immutable operation identity or readiness'}
      $taskPlanningBindingResult=docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db psql -U postgres -d household_restore -Atc "SELECT b.version::text||'|'||(b.source_hash=sha256(convert_to(c.snapshot::text,'UTF8')))::text||'|'||(b.ingredient_id=(c.snapshot->'ingredients'->0->>'id')::uuid)::text||'|'||(b.product_id='$taskProduct' AND b.mealplan_generation=1 AND b.pantry_generation=1 AND b.unit_code='kg')::text||'|'||core.planning_bindings_ready()::text FROM core.planning_ingredient_bindings b JOIN mealplan.components c ON c.household_id=b.household_id AND c.id=b.component_id WHERE b.household_id='$taskHousehold' AND b.id='$taskPlanningBinding'"
      if($LASTEXITCODE -ne 0 -or $taskPlanningBindingResult -ne '1|true|true|true|true'){throw 'Restored plan mapping lost its version, frozen source identity, current product/unit or readiness'}
      $taskRestoreTimer.Stop()
      $taskRehearsalTimer.Stop()
      [ordered]@{passed=$true;source='disposable household_fixture';target='disposable household_restore';snapshot=$taskSnapshot;domainValue=$taskRestored;stockProjection=$taskStock;pantryTarget=$taskTarget;pantryDaily=$taskPantryDaily;stockDuplicateInserted=$taskStockDuplicate;pantryBatch=$taskPantryBatch;pantryPrivateDraft=$taskPantryDraft;pantryBatchUniqueConstraint=$taskPantryBatchDuplicate;durableCompletion=$taskCheckout;recipeVersionsAfterEdit=$taskRecipeAfter;cookingTimers=$taskCookingBefore;cookingRecord=$taskCookingResult;cookingDuplicateInserted=$taskCookingDuplicate;cookingStockPosting=$taskCookingPostingResult;cookingStockDuplicateInserted=$taskCookingPostingDuplicate;planningComponents=$taskPlanning;planningDuplicateInserted=$taskPlanningDuplicate;mealReservation=$taskReservationResult;planningIngredientBinding=$taskPlanningBindingResult;hashedFiles=2;includesOriginalAndPreview=$true;backupSeconds=[Math]::Round($taskBackupTimer.Elapsed.TotalSeconds,2);restoreAndVerifySeconds=[Math]::Round($taskRestoreTimer.Elapsed.TotalSeconds,2);rehearsalSeconds=[Math]::Round($taskRehearsalTimer.Elapsed.TotalSeconds,2);scope='Small synthetic local fixture; not acceptance of a production RTO/RPO';verifiedAt=[DateTime]::UtcNow.ToString('o')} | ConvertTo-Json | Set-Content outputs/recovery-review/report.json
      Get-Content outputs/recovery-review/report.json
    } finally {
      docker compose -f compose.yaml -f compose.backup.yaml exec -T restore-db dropdb -U postgres household_fixture
      docker volume rm $taskRepoVolume $taskWorkVolume | Out-Null
    }
} finally {Pop-Location}
