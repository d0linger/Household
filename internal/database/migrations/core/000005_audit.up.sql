BEGIN;

-- Existing semantic history is retained. Legacy rows have no fabricated diffs.
ALTER TABLE core.audit_log
 ADD COLUMN actor_kind text NOT NULL DEFAULT 'legacy' CHECK(actor_kind IN('legacy','user','system','worker','operator','bootstrap','database')),
 ADD COLUMN db_role text NOT NULL DEFAULT '',
 ADD COLUMN entity_key jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(entity_key)='object'),
 ADD COLUMN entity_label text NOT NULL DEFAULT '',
 ADD COLUMN changes jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(changes)='object');
CREATE INDEX audit_household_cursor ON core.audit_log(household_id,id DESC);
CREATE INDEX audit_resource_cursor ON core.audit_log(household_id,module_id,resource_type,resource_id,id DESC);

-- Policy is owned by the migration role. Unknown columns are detected but their
-- values are redacted before storage. Safe fields are explicit, never inferred
-- from SQL types or an application-supplied list.
CREATE TABLE core.audit_policies(
 schema_name text NOT NULL,
 table_name text NOT NULL,
 key_columns text[] NOT NULL,
 safe_columns text[] NOT NULL DEFAULT '{}',
 label_column text,
 ignored_columns text[] NOT NULL DEFAULT ARRAY['created_at','updated_at'],
 PRIMARY KEY(schema_name,table_name)
);
REVOKE ALL ON core.audit_policies FROM PUBLIC,household_app;

INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('shopping','lists',ARRAY['id'],ARRAY['name','sort_order','archived_at'],'name'),
 ('shopping','items',ARRAY['id'],ARRAY['name','list_id','quantity','unit_code','sort_order','checked_at','checked_by'],'name'),
 ('shopping','item_events',ARRAY['id'],ARRAY['item_id','action','actor_user_id'],NULL),
 ('fuel','vehicles',ARRAY['id'],ARRAY['name','fuel_type','archived_at'],'name'),
 ('fuel','odometer_series',ARRAY['id'],ARRAY['vehicle_id','started_on','ended_on','initial_reading'],NULL),
 ('fuel','fillups',ARRAY['id'],ARRAY['vehicle_id','odometer_series_id','occurred_on','fuel_type','odometer','quantity','total_minor','unit_price','full_tank','previous_fill_missing'],NULL),
 ('recipes','recipes',ARRAY['id'],ARRAY['title','base_servings','prep_minutes','cook_minutes','archived_at'],'title'),
 ('recipes','ingredients',ARRAY['id'],ARRAY['recipe_id','position','name','quantity','unit_code'],'name'),
 ('recipes','steps',ARRAY['id'],ARRAY['recipe_id','position','timer_seconds'],NULL),
 ('recipes','tags',ARRAY['id'],ARRAY['name'],'name'),
 ('recipes','recipe_tags',ARRAY['household_id','recipe_id','tag_id'],ARRAY['recipe_id','tag_id'],NULL),
 ('pantry','products',ARRAY['id'],ARRAY['name','unit_code','low_stock_threshold','integer_quantities','archived_at'],'name'),
 ('pantry','locations',ARRAY['id'],ARRAY['name','sort_order','archived_at'],'name'),
 ('pantry','lots',ARRAY['id'],ARRAY['product_id','location_id','received_on','best_before_on','use_by_on','opened_on'],NULL),
 ('pantry','stock_movements',ARRAY['id'],ARRAY['lot_id','quantity_delta','reason','correlation_id','reverses_movement_id'],NULL),
 ('expenses','categories',ARRAY['id'],ARRAY['name','sort_order','archived_at'],'name'),
 ('expenses','entries',ARRAY['id'],ARRAY['occurred_on','amount_minor','kind','paid_by_membership_id','state','reverses_entry_id','replaces_entry_id','posted_at'],NULL),
 ('expenses','entry_lines',ARRAY['id'],ARRAY['entry_id','category_id','amount_minor','position'],NULL),
 ('chores','task_templates',ARRAY['id'],ARRAY['title','assignee_id','repeat_days','timezone','active'],'title'),
 ('chores','task_occurrences',ARRAY['id'],ARRAY['template_id','due_at','assignee_id','state','completed_at','completed_by'],NULL),
 ('meters','meters',ARRAY['id'],ARRAY['name','unit_code','archived_at'],'name'),
 ('meters','devices',ARRAY['id'],ARRAY['meter_id','installed_on','removed_on','initial_reading','rollover_value'],NULL),
 ('meters','readings',ARRAY['id'],ARRAY['device_id','measured_at','value','kind'],NULL),
 ('documents','documents',ARRAY['id'],ARRAY['valid_from','valid_until','warranty_until','remind_days','archived_at'],NULL),
 ('mealplan','plans',ARRAY['id'],ARRAY['name','starts_on','ends_on'],'name'),
 ('mealplan','meals',ARRAY['id'],ARRAY['plan_id','planned_on','meal_slot','title','servings','recipe_reference'],'title'),
 ('core','households',ARRAY['id'],ARRAY['locale','timezone','currency_code','module_revision'],NULL),
 ('core','users',ARRAY['id'],ARRAY['must_change_password','auth_version','password_changed_at','disabled_at'],NULL),
 ('core','memberships',ARRAY['id'],ARRAY['user_id','role','inactive_at'],NULL),
 ('core','user_preferences',ARRAY['household_id','user_id'],ARRAY['user_id','theme','locale','default_module_id','offline_storage_enabled'],NULL),
 ('core','module_states',ARRAY['household_id','module_id'],ARRAY['module_id','state','state_version','data_generation','changed_by','changed_at'],NULL),
 ('core','sessions',ARRAY['id'],ARRAY['user_id','auth_version','absolute_expires_at','revoked_at'],NULL),
 ('core','totp_credentials',ARRAY['user_id'],ARRAY['user_id','enabled_at'],NULL),
 ('core','recovery_codes',ARRAY['id'],ARRAY['user_id','consumed_at'],NULL),
 ('core','resource_links',ARRAY['id'],ARRAY['source_module','source_type','source_id','target_module','target_type','target_id','relation'],NULL),
 ('core','module_purge_requests',ARRAY['id'],ARRAY['module_id','expected_generation','requested_by','state','confirmed_at','expires_at','finished_at'],NULL),
 ('core','attachments',ARRAY['id'],ARRAY['module_id','module_generation','resource_type','resource_id','media_type','size_bytes','state','uploaded_by'],NULL),
 ('core','attachment_derivatives',ARRAY['id'],ARRAY['attachment_id','variant','media_type','width','height','size_bytes'],NULL),
 ('core','capture_jobs',ARRAY['id'],ARRAY['module_id','module_generation','source_attachment_id','target_schema','schema_version','provider','state','confirmed_by','confirmed_at','result_resource_id','expires_at'],NULL),
 ('core','reminders',ARRAY['id'],ARRAY['module_id','module_generation','resource_type','resource_id','recipient_user_id','kind','scheduled_at','timezone','state','delivered_at','cancelled_at'],NULL),
 ('core','notifications',ARRAY['id'],ARRAY['user_id','module_id','kind','read_at','dismissed_at'],NULL),
 ('core','push_subscriptions',ARRAY['id'],ARRAY['user_id','disabled_at'],NULL),
 ('core','import_batches',ARRAY['id'],ARRAY['module_id','module_generation','entity','state','row_count','valid_count','error_count','confirmed_by','confirmed_at'],NULL),
 ('core','import_rows',ARRAY['batch_id','row_number'],ARRAY['batch_id','row_number','state','result_resource_id'],NULL),
 ('core','backup_runs',ARRAY['id'],ARRAY['started_at','finished_at','state','application_version','verified_at'],NULL);

-- These credentials change during ordinary reads/authentication. Their lifecycle
-- still produces events; heartbeat/counter updates must not copy request activity.
UPDATE core.audit_policies SET ignored_columns=ignored_columns||ARRAY['last_seen_at','idle_expires_at'] WHERE schema_name='core' AND table_name='sessions';
UPDATE core.audit_policies SET ignored_columns=ignored_columns||ARRAY['last_accepted_counter'] WHERE schema_name='core' AND table_name='totp_credentials';

-- Deliberate exclusions: audit history/policy itself; schema/version and seeded
-- reference catalogs; search documents; rate buckets; auth challenge payloads;
-- outbox/delivery and idempotency receipts; job/file-deletion queues; alert-state
-- projection. Their user-visible effects and explicit security outcomes are
-- audited at the authoritative tables/semantic boundary, without duplicate data.

CREATE FUNCTION core.audit_context() RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
 kind text := nullif(current_setting('household.audit_kind',true),'');
 actor uuid := nullif(current_setting('household.audit_user',true),'')::uuid;
 household uuid := nullif(current_setting('household.audit_household',true),'')::uuid;
 request uuid := nullif(current_setting('household.audit_request',true),'')::uuid;
BEGIN
 kind := coalesce(kind,CASE WHEN session_user='household_owner' THEN 'operator' ELSE 'database' END);
 IF kind NOT IN('user','system','worker','operator','bootstrap','database') OR (kind='user')<>(actor IS NOT NULL) THEN
  RAISE EXCEPTION 'invalid audit identity' USING ERRCODE='22023';
 END IF;
 IF kind='user' AND household IS NULL THEN RAISE EXCEPTION 'audit household required' USING ERRCODE='22023'; END IF;
 RETURN jsonb_build_object('kind',kind,'actor',actor,'household',household,'request',coalesce(request,gen_random_uuid()));
END $$;

CREATE FUNCTION core.write_audit_row(p_schema text,p_table text,p_action text,p_before jsonb,p_after jsonb) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
 policy core.audit_policies%ROWTYPE;
 row_value jsonb := coalesce(p_after,p_before);
 context_value jsonb;
 changed jsonb := '{}';
 identity_key jsonb := '{}';
 column_name text;
 old_value jsonb;
 new_value jsonb;
 old_present boolean;
 new_present boolean;
 permitted boolean;
 household uuid;
 context_household uuid;
 resource uuid;
 label text;
BEGIN
 IF p_action NOT IN('CREATE','UPDATE','DELETE') THEN RAISE EXCEPTION 'invalid audit operation'; END IF;
 SELECT * INTO STRICT policy FROM core.audit_policies WHERE schema_name=p_schema AND table_name=p_table;
 FOR column_name IN SELECT jsonb_object_keys(coalesce(p_before,'{}')||coalesce(p_after,'{}')) LOOP
  IF column_name=ANY(policy.ignored_columns) THEN CONTINUE; END IF;
  old_present := coalesce(p_before ? column_name,false);
  new_present := coalesce(p_after ? column_name,false);
  old_value := p_before->column_name;
  new_value := p_after->column_name;
  IF old_present=new_present AND old_value IS NOT DISTINCT FROM new_value THEN CONTINUE; END IF;
  permitted := column_name=ANY(policy.safe_columns||ARRAY['id','household_id','created_by','version']);
  -- Compare before redaction: a password or private-note edit remains visible.
  IF NOT permitted THEN
   IF old_present THEN old_value := to_jsonb('***REDACTED***'::text); END IF;
   IF new_present THEN new_value := to_jsonb('***REDACTED***'::text); END IF;
  ELSIF jsonb_typeof(old_value) IN('object','array') OR jsonb_typeof(new_value) IN('object','array') THEN
   RAISE EXCEPTION 'audit policy permits an unbounded structured value';
  ELSE
   IF jsonb_typeof(old_value)='string' THEN old_value := to_jsonb(left(old_value#>>'{}',256)); END IF;
   IF jsonb_typeof(new_value)='string' THEN new_value := to_jsonb(left(new_value#>>'{}',256)); END IF;
  END IF;
  changed := changed||jsonb_build_object(column_name,jsonb_build_object('before',old_value,'after',new_value,'before_present',old_present,'after_present',new_present,'redacted',NOT permitted));
 END LOOP;
 IF changed='{}'::jsonb THEN RETURN; END IF;
 FOREACH column_name IN ARRAY policy.key_columns LOOP
  IF NOT row_value ? column_name THEN RAISE EXCEPTION 'audit key missing'; END IF;
  identity_key := identity_key||jsonb_build_object(column_name,row_value->column_name);
 END LOOP;
 context_value := core.audit_context();
 context_household := (context_value->>'household')::uuid;
 household := (row_value->>'household_id')::uuid;
 IF p_schema='core' AND p_table='households' THEN household := (row_value->>'id')::uuid; END IF;
 IF household IS NOT NULL AND context_household IS NOT NULL AND household<>context_household THEN
  RAISE EXCEPTION 'audit household mismatch' USING ERRCODE='42501';
 END IF;
 household := coalesce(household,context_household,(SELECT id FROM core.households WHERE singleton=1));
 resource := coalesce(row_value->>'id',CASE WHEN p_table='recipe_tags' THEN row_value->>'recipe_id' ELSE row_value->>'user_id' END)::uuid;
 label := p_schema||'.'||p_table;
 IF policy.label_column IS NOT NULL AND policy.label_column=ANY(policy.safe_columns) THEN
  label := coalesce(nullif(left(row_value->>policy.label_column,160),''),label);
 ELSIF resource IS NOT NULL THEN label := label||' · '||left(resource::text,8);
 END IF;
 INSERT INTO core.audit_log(household_id,actor_user_id,module_id,resource_type,resource_id,action,outcome,request_id,actor_kind,db_role,entity_key,entity_label,changes,created_at)
 VALUES(household,(context_value->>'actor')::uuid,p_schema,p_table,resource,p_action,'success',(context_value->>'request')::uuid,context_value->>'kind',session_user,identity_key,label,changed,clock_timestamp());
END $$;

CREATE FUNCTION core.capture_audit_change() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='INSERT' THEN PERFORM core.write_audit_row(TG_TABLE_SCHEMA,TG_TABLE_NAME,'CREATE',NULL,to_jsonb(NEW));
 ELSIF TG_OP='UPDATE' THEN PERFORM core.write_audit_row(TG_TABLE_SCHEMA,TG_TABLE_NAME,'UPDATE',to_jsonb(OLD),to_jsonb(NEW));
 ELSIF TG_OP='DELETE' THEN PERFORM core.write_audit_row(TG_TABLE_SCHEMA,TG_TABLE_NAME,'DELETE',to_jsonb(OLD),NULL);
 ELSE RAISE EXCEPTION 'unsupported audit trigger'; END IF;
 RETURN NULL;
END $$;

-- TRUNCATE does not fire row DELETE triggers. Stream rows before truncation so
-- an operator purge remains reconstructable without loading a table into JSON.
CREATE FUNCTION core.capture_audit_truncate() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE item record;
BEGIN
 IF TG_OP<>'TRUNCATE' OR session_user<>'household_owner' THEN RAISE EXCEPTION 'audit truncate requires maintenance role' USING ERRCODE='42501'; END IF;
 FOR item IN EXECUTE format('SELECT to_jsonb(t) AS value FROM %I.%I t',TG_TABLE_SCHEMA,TG_TABLE_NAME) LOOP
  PERFORM core.write_audit_row(TG_TABLE_SCHEMA,TG_TABLE_NAME,'DELETE',item.value,NULL);
 END LOOP;
 RETURN NULL;
END $$;

CREATE FUNCTION core.record_audit_event(p_module text,p_entity text,p_resource uuid,p_action text,p_details jsonb) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
 context_value jsonb := core.audit_context();
 safe_details jsonb := '{}';
 key text;
 household uuid;
 event_outcome text := 'success';
BEGIN
 IF p_action NOT IN('module_enabled','module_disabled','record_write','record_delete','attachment_upload','import_confirm','capture_confirm','user_create','user_update','module_purged','archive','check','post','reverse','complete','shopping','tags','recipe','expense','transfer','login_success','login_failed','logout','password_changed','totp_setup','totp_enabled','recovery_code_used','session_revoked','operator_password_recovery','bootstrap','worker_job_failed','worker_job_unsupported','worker_lease_exhausted') THEN
  RAISE EXCEPTION 'unsupported audit event' USING ERRCODE='22023';
 END IF;
 IF coalesce(p_module,'')!~'^[a-z][a-z0-9_]{0,63}$' OR coalesce(p_entity,'')!~'^[a-z][a-z0-9_]{0,63}$' THEN
  RAISE EXCEPTION 'invalid audit resource type' USING ERRCODE='22023';
 END IF;
 IF p_details IS NULL OR jsonb_typeof(p_details)<>'object' OR octet_length(p_details::text)>4096 THEN
  RAISE EXCEPTION 'invalid audit metadata' USING ERRCODE='22023';
 END IF;
 FOR key IN SELECT jsonb_object_keys(p_details) LOOP
  IF key='mfa_reset' AND jsonb_typeof(p_details->key)='boolean' THEN
   safe_details := safe_details||jsonb_build_object(key,p_details->key);
  ELSIF key IN('target_user_id','requested_by') AND jsonb_typeof(p_details->key)='string' THEN
   safe_details := safe_details||jsonb_build_object(key,(p_details->>key)::uuid);
  ELSE RAISE EXCEPTION 'unsupported audit metadata' USING ERRCODE='22023'; END IF;
 END LOOP;
 IF p_action IN('login_failed','worker_job_failed','worker_job_unsupported','worker_lease_exhausted') THEN event_outcome := 'failure'; END IF;
 household := coalesce((context_value->>'household')::uuid,(SELECT id FROM core.households WHERE singleton=1));
 INSERT INTO core.audit_log(household_id,actor_user_id,module_id,resource_type,resource_id,action,outcome,request_id,details,actor_kind,db_role,entity_key,entity_label,created_at)
 VALUES(household,(context_value->>'actor')::uuid,p_module,p_entity,p_resource,p_action,event_outcome,(context_value->>'request')::uuid,safe_details,context_value->>'kind',session_user,CASE WHEN p_resource IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('id',p_resource) END,p_module||'.'||p_entity||CASE WHEN p_resource IS NULL THEN '' ELSE ' · '||left(p_resource::text,8) END,clock_timestamp());
END $$;

-- The runner calls this only after every independent module schema is migrated.
-- Repeatable installation also restores a missing/disabled trigger before serve.
CREATE FUNCTION core.install_audit_triggers() RETURNS void
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE policy record;
BEGIN
 IF current_user<>'household_owner' THEN RAISE EXCEPTION 'audit installation requires owner role' USING ERRCODE='42501'; END IF;
 FOR policy IN SELECT schema_name,table_name FROM core.audit_policies ORDER BY schema_name,table_name LOOP
  IF to_regclass(format('%I.%I',policy.schema_name,policy.table_name)) IS NULL THEN RAISE EXCEPTION 'audited table missing: %.%',policy.schema_name,policy.table_name; END IF;
  EXECUTE format('DROP TRIGGER IF EXISTS household_audit_change ON %I.%I',policy.schema_name,policy.table_name);
  EXECUTE format('CREATE TRIGGER household_audit_change AFTER INSERT OR UPDATE OR DELETE ON %I.%I FOR EACH ROW EXECUTE FUNCTION core.capture_audit_change()',policy.schema_name,policy.table_name);
  EXECUTE format('DROP TRIGGER IF EXISTS household_audit_truncate ON %I.%I',policy.schema_name,policy.table_name);
  EXECUTE format('CREATE TRIGGER household_audit_truncate BEFORE TRUNCATE ON %I.%I FOR EACH STATEMENT EXECUTE FUNCTION core.capture_audit_truncate()',policy.schema_name,policy.table_name);
 END LOOP;
END $$;

CREATE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT count(*)=45 AND bool_and(
  EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
  AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure)
 ) AND NOT EXISTS(
  SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
  AND c.relname<>'schema_migrations'
  AND NOT(n.nspname='fuel' AND c.relname='fuel_types')
  AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states'))
  AND NOT EXISTS(SELECT 1 FROM core.audit_policies policy WHERE policy.schema_name=n.nspname AND policy.table_name=c.relname)
 ) FROM core.audit_policies p
$$;

REVOKE ALL ON FUNCTION core.audit_context(),core.write_audit_row(text,text,text,jsonb,jsonb),core.capture_audit_change(),core.capture_audit_truncate(),core.record_audit_event(text,text,uuid,text,jsonb),core.install_audit_triggers(),core.audit_ready() FROM PUBLIC,household_app;
GRANT EXECUTE ON FUNCTION core.record_audit_event(text,text,uuid,text,jsonb),core.audit_ready() TO household_app;
REVOKE INSERT,UPDATE,DELETE,TRUNCATE ON core.audit_log FROM household_app;
REVOKE USAGE,SELECT ON SEQUENCE core.audit_log_id_seq FROM household_app;
COMMIT;
