BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
DO $predecessor$
DECLARE source text;
BEGIN
 SELECT replace(p.prosrc,chr(13),'') INTO source FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='core.audit_ready()'::regprocedure AND p.prokind='f' AND p.prosecdef AND p.provolatile='s'
 AND p.prorettype='boolean'::regtype AND p.proowner='household_owner'::regrole AND l.lanname='sql'
 AND p.proconfig=ARRAY['search_path=pg_catalog'];
 IF source IS DISTINCT FROM $accepted$
 SELECT core.financial_requirements_ready() AND count(*)=(94+(SELECT count(*) FROM core.audit_policies WHERE schema_name='shopping' AND table_name='barcodes')) AND bool_and(
 EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
 AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure))
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
 AND c.relname<>'schema_migrations' AND NOT(n.nspname='fuel' AND c.relname IN('fuel_types','household_state','segment_state','consumption_segments','segment_jobs','module_operations','module_effects','capture_transitions','legacy_operation_fences'))
 AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
 AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states',
 'financial_requirement_sets','financial_requirement_members','financial_artifact_scope_versions'))
 AND NOT EXISTS(SELECT 1 FROM core.audit_policies p WHERE p.schema_name=n.nspname AND p.table_name=c.relname)) FROM core.audit_policies p
$accepted$ OR (SELECT count(*) FROM core.audit_policies)<>95 THEN
  RAISE EXCEPTION 'planning extensions require the exact accepted audit predecessor';END IF;
 IF NOT mealplan.expansion_ready() THEN RAISE EXCEPTION 'reviewed Mealplan8 schema required';END IF;
END $predecessor$;
INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('mealplan','leftover_sources',ARRAY['id'],ARRAY['plan_id','source_meal_id','source_component_id','reviewed_meal_version','reviewed_component_version','version'],NULL),
 ('mealplan','leftover_allocations',ARRAY['id'],ARRAY['plan_id','source_id','target_meal_id','reviewed_source_version','reviewed_target_version','version'],NULL),
 ('mealplan','weekly_settings',ARRAY['id'],ARRAY['plan_id','week_start','version'],NULL),
 ('mealplan','calendar_feeds',ARRAY['id'],ARRAY['plan_id','week_start','module_generation','expires_at','revoked_at','version'],NULL),
 ('mealplan','calendar_tombstones',ARRAY['household_id','module_generation','meal_id','planned_on'],ARRAY['plan_id','module_generation','sequence','deleted_at'],NULL);
CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT core.financial_requirements_ready() AND count(*)=(99+(SELECT count(*) FROM core.audit_policies WHERE schema_name='shopping' AND table_name='barcodes')) AND bool_and(
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
CREATE FUNCTION core.planning_extensions_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT NOT EXISTS(SELECT 1 FROM (VALUES
 ('leftover_sources',ARRAY['id'],ARRAY['plan_id','source_meal_id','source_component_id','reviewed_meal_version','reviewed_component_version','version']),
 ('leftover_allocations',ARRAY['id'],ARRAY['plan_id','source_id','target_meal_id','reviewed_source_version','reviewed_target_version','version']),
 ('weekly_settings',ARRAY['id'],ARRAY['plan_id','week_start','version']),
 ('calendar_feeds',ARRAY['id'],ARRAY['plan_id','week_start','module_generation','expires_at','revoked_at','version']),
 ('calendar_tombstones',ARRAY['household_id','module_generation','meal_id','planned_on'],ARRAY['plan_id','module_generation','sequence','deleted_at'])
 ) expected(name,keys,safe) WHERE NOT EXISTS(SELECT 1 FROM core.audit_policies p WHERE p.schema_name='mealplan' AND p.table_name=expected.name
 AND p.key_columns=expected.keys AND p.safe_columns=expected.safe AND p.label_column IS NULL))
 AND (SELECT count(*) FROM core.audit_policies)=100
$$;
REVOKE ALL ON FUNCTION core.planning_extensions_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.planning_extensions_ready() TO household_app;
COMMIT;
