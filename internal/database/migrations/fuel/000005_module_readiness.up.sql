BEGIN;

-- Admit only the two reviewed Core26 audit definitions, preserving the optional
-- local-barcode census. An unfamiliar predecessor is never silently replaced.
DO $audit_predecessor$
DECLARE prior_source text; expected_source text:=$prior_core26$
 SELECT core.financial_requirements_ready() AND count(*)=77 AND bool_and(
  EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
  AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure)
 ) AND NOT EXISTS(
  SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
  AND c.relname<>'schema_migrations'
  AND NOT(n.nspname='fuel' AND c.relname='fuel_types') AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
  AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states',
   'financial_requirement_sets','financial_requirement_members','financial_artifact_scope_versions'))
  AND NOT EXISTS(SELECT 1 FROM core.audit_policies policy WHERE policy.schema_name=n.nspname AND policy.table_name=c.relname)
 ) FROM core.audit_policies p
$prior_core26$;barcode_policy boolean;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=26 AND NOT dirty) FROM core.schema_migrations) THEN
  RAISE EXCEPTION 'Fuel module requires the reviewed Core26 visibility and audit contract';
 END IF;
 SELECT replace(p.prosrc,chr(13),'') INTO prior_source FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='core.audit_ready()'::regprocedure AND p.prokind='f' AND p.prosecdef AND p.provolatile='s'
 AND p.prorettype='boolean'::regtype AND p.proowner='household_owner'::regrole AND l.lanname='sql' AND p.proconfig=ARRAY['search_path=pg_catalog'];
 SELECT EXISTS(SELECT 1 FROM core.audit_policies WHERE schema_name='shopping' AND table_name='barcodes') INTO barcode_policy;
 IF prior_source IS DISTINCT FROM(CASE WHEN barcode_policy THEN replace(expected_source,'count(*)=77','count(*)=78') ELSE expected_source END) THEN
  RAISE EXCEPTION 'unsupported Core26 audit predecessor; preserve existing security checks';
 END IF;
END $audit_predecessor$;

INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('fuel','odometer_observations',ARRAY['id'],ARRAY['vehicle_id','odometer_series_id','observed_on','reading','source_kind','version'],NULL),
 ('fuel','charging_sessions',ARRAY['id'],ARRAY['vehicle_id','odometer_series_id','occurred_on','odometer','energy_kwh','measurement_basis','total_minor','currency_code','charge_kind','soc_start','soc_end','version'],NULL),
 ('fuel','trips',ARRAY['id'],ARRAY['vehicle_id','odometer_series_id','started_on','ended_on','start_odometer','end_odometer','version'],NULL),
 ('fuel','vehicle_costs',ARRAY['id'],ARRAY['vehicle_id','occurred_on','kind','total_minor','currency_code','corrects_id','version'],NULL),
 ('fuel','maintenance_plans',ARRAY['id'],ARRAY['vehicle_id','due_on','due_odometer','odometer_series_id','repeat_days','repeat_distance','active','cycle','task_opt_in','assignee_id','version'],NULL),
 ('fuel','maintenance_events',ARRAY['id'],ARRAY['vehicle_id','plan_id','cycle','completed_on','odometer','odometer_series_id','version'],NULL),
 ('fuel','user_settings',ARRAY['household_id','user_id'],ARRAY['user_id','generation','version'],NULL),
 ('fuel','import_jobs',ARRAY['id'],ARRAY['vehicle_id','user_id','generation','profile','state','version'],NULL),
 ('fuel','import_rows',ARRAY['job_id','row_number'],ARRAY['job_id','row_number','decision','record_id'],NULL),
 ('fuel','capture_jobs',ARRAY['id'],ARRAY['vehicle_id','user_id','generation','attachment_id','kind','state','record_id','version'],NULL),
 ('fuel','integration_bindings',ARRAY['id'],ARRAY['vehicle_id','source_entity','source_id','source_cycle','target_module','target_entity','target_id','target_generation','relation','state','version'],NULL);
UPDATE core.audit_policies SET safe_columns=safe_columns||ARRAY['current_series_id','tank_capacity','battery_capacity'] WHERE schema_name='fuel' AND table_name='vehicles';
UPDATE core.audit_policies SET safe_columns=safe_columns||ARRAY['final_reading','previous_series_id','distance_offset'] WHERE schema_name='fuel' AND table_name='odometer_series';
UPDATE core.audit_policies SET safe_columns=safe_columns||ARRAY['quantity_unit','currency_code','source_kind'] WHERE schema_name='fuel' AND table_name='fillups';
DO $install$
DECLARE name text;
BEGIN
 FOREACH name IN ARRAY ARRAY['vehicles','odometer_series','fillups','odometer_observations','charging_sessions','trips','vehicle_costs','maintenance_plans','maintenance_events',
 'user_settings','import_jobs','import_rows','capture_jobs','integration_bindings'] LOOP
  EXECUTE format('DROP TRIGGER IF EXISTS household_audit_change ON fuel.%I',name);
  EXECUTE format('CREATE TRIGGER household_audit_change AFTER INSERT OR UPDATE OR DELETE ON fuel.%I FOR EACH ROW EXECUTE FUNCTION core.capture_audit_change()',name);
  EXECUTE format('DROP TRIGGER IF EXISTS household_audit_truncate ON fuel.%I',name);
  EXECUTE format('CREATE TRIGGER household_audit_truncate BEFORE TRUNCATE ON fuel.%I FOR EACH STATEMENT EXECUTE FUNCTION core.capture_audit_truncate()',name);
 END LOOP;
END $install$;

CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT core.financial_requirements_ready() AND count(*)=(88+(SELECT count(*) FROM core.audit_policies WHERE schema_name='shopping' AND table_name='barcodes')) AND bool_and(
 EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
 AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure))
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
 AND c.relname<>'schema_migrations' AND NOT(n.nspname='fuel' AND c.relname IN('fuel_types','household_state','segment_state','consumption_segments','segment_jobs','module_operations','module_effects','capture_transitions','legacy_operation_fences'))
 AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
 AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states',
 'financial_requirement_sets','financial_requirement_members','financial_artifact_scope_versions'))
 AND NOT EXISTS(SELECT 1 FROM core.audit_policies p WHERE p.schema_name=n.nspname AND p.table_name=c.relname)) FROM core.audit_policies p
$$;

-- Snapshot schema, predicates, function definitions and trigger modes, not just
-- their names. This catalog is owner managed; no App DDL or policy privileges.
CREATE FUNCTION fuel.module_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
 'columns',(SELECT jsonb_agg(jsonb_build_array(c.relname,a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attidentity,a.attgenerated,
 coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY c.relname,a.attnum)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
 LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE n.nspname='fuel' AND c.relkind='r' AND c.relname<>'schema_migrations'),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(c.relname,k.conname,pg_get_constraintdef(k.oid),k.convalidated) ORDER BY c.relname,k.conname)
 FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fuel' AND c.relname<>'schema_migrations'),
 'indexes',(SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready,i.indislive) ORDER BY pg_get_indexdef(i.indexrelid))
 FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fuel' AND c.relname<>'schema_migrations'),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(c.relname,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled::text,t.tgdeferrable,t.tginitdeferred) ORDER BY c.relname,t.tgname)
 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fuel' AND NOT t.tgisinternal),
 'functions',(SELECT jsonb_agg(jsonb_build_array(p.proname,pg_get_function_identity_arguments(p.oid),md5(pg_get_functiondef(p.oid)),pg_get_userbyid(p.proowner),p.prosecdef,p.provolatile::text,p.proconfig)
 ORDER BY p.proname,pg_get_function_identity_arguments(p.oid)) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname='fuel' AND p.proname NOT IN('module_catalog','module_ready','overview_ready')),
 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY p.table_name) FROM core.audit_policies p WHERE p.schema_name='fuel'))
 ||jsonb_build_object('fuel_types',(SELECT jsonb_agg(to_jsonb(t) ORDER BY t.code) FROM fuel.fuel_types t))
 ||jsonb_build_object('core_link_trigger',(SELECT jsonb_build_array(pg_get_triggerdef(t.oid),t.tgenabled::text)
 FROM pg_trigger t WHERE t.tgrelid='core.resource_links'::regclass AND t.tgname='fuel_module_link' AND NOT t.tgisinternal))
$$;
REVOKE ALL ON FUNCTION fuel.module_catalog() FROM PUBLIC,household_app;
DO $install$
DECLARE expected jsonb;catalog_hash text;
BEGIN
 expected:=fuel.module_catalog();
 SELECT md5(pg_get_functiondef('fuel.module_catalog()'::regprocedure)) INTO catalog_hash;
 EXECUTE replace(replace($definition$
 CREATE FUNCTION fuel.module_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT fuel.module_catalog()=#EXPECTED#::jsonb
 AND md5(pg_get_functiondef('fuel.module_catalog()'::regprocedure))=#HASH#
 AND core.audit_ready() AND fuel.overview_ready()
 AND NOT pg_has_role('household_app','household_owner','MEMBER')
 AND NOT has_table_privilege('household_app','fuel.module_operations','SELECT,INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','fuel.module_effects','SELECT,INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','fuel.capture_transitions','SELECT,INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','fuel.legacy_operation_fences','SELECT,INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','fuel.household_state','INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','fuel.fuel_types','INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','fuel.vehicle_costs','UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','fuel.maintenance_events','UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT EXISTS(SELECT 1 FROM(VALUES('vehicles'),('odometer_series'),('fillups'),('odometer_observations'),('charging_sessions'),('trips'),
 ('maintenance_plans'),('user_settings'),('import_jobs'),('import_rows'),('capture_jobs'),('integration_bindings')) t(name)
 CROSS JOIN(VALUES('SELECT'),('INSERT'),('UPDATE'),('DELETE')) required(privilege)
 WHERE NOT has_table_privilege('household_app','fuel.'||t.name,required.privilege))
 AND NOT EXISTS(SELECT 1 FROM(VALUES('vehicle_costs'),('maintenance_events')) t(name) CROSS JOIN(VALUES('SELECT'),('INSERT')) required(privilege)
 WHERE NOT has_table_privilege('household_app','fuel.'||t.name,required.privilege))
 AND NOT has_function_privilege('household_app','fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)','EXECUTE')
 AND NOT has_function_privilege('household_app','fuel.guard_module_fact()','EXECUTE')
 AND NOT has_function_privilege('household_app','fuel.capture_module_effect()','EXECUTE')
 AND NOT has_function_privilege('household_app','fuel.guard_core_link()','EXECUTE')
 AND NOT has_function_privilege('household_app','fuel.check_module_operation_complete()','EXECUTE')
 AND has_function_privilege('household_app','fuel.module_begin(uuid,uuid,bigint,text,bytea,jsonb,uuid,bigint,uuid)','EXECUTE')
 AND has_function_privilege('household_app','fuel.module_finish(uuid,jsonb)','EXECUTE')
 AND has_function_privilege('household_app','fuel.module_status(uuid,bigint,uuid,bigint,uuid)','EXECUTE')
 AND has_function_privilege('household_app','fuel.capture_transition(uuid,uuid,text,text,jsonb,jsonb,text)','EXECUTE')
 $body$;
 $definition$,'#EXPECTED#',quote_literal(expected::text)),'#HASH#',quote_literal(catalog_hash));
END $install$;
REVOKE ALL ON FUNCTION fuel.module_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION fuel.module_ready() TO household_app;
COMMIT;
