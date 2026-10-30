BEGIN;
DO $compatibility$
DECLARE audit_definition text;old_definition text;ready_definition text;old_hash text;new_hash text;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=28 AND NOT dirty) FROM core.schema_migrations)
 OR NOT core.planning_extensions_ready() THEN RAISE EXCEPTION 'prepared capture compatibility requires reviewed Core28';END IF;
 SELECT pg_get_functiondef(p.oid) INTO audit_definition FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='core.audit_ready()'::regprocedure AND p.prokind='f' AND p.prosecdef AND p.provolatile='s'
 AND p.prorettype='boolean'::regtype AND p.proowner='household_owner'::regrole AND l.lanname='sql' AND p.proconfig=ARRAY['search_path=pg_catalog']
 AND replace(p.prosrc,chr(13),'')=$current$
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
$current$;
 SELECT pg_get_functiondef(p.oid) INTO ready_definition FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='pantry.prepared_capture_ready()'::regprocedure AND p.prokind='f' AND p.prosecdef AND p.provolatile='s'
 AND p.prorettype='boolean'::regtype AND p.proowner='household_owner'::regrole AND l.lanname='sql' AND p.proconfig=ARRAY['search_path=pg_catalog'];
 IF audit_definition IS NULL OR ready_definition IS NULL THEN RAISE EXCEPTION 'unreviewed prepared capture compatibility predecessor';END IF;
 old_definition:=replace(audit_definition,'count(*)=(99+','count(*)=(94+');
 old_hash:=md5(old_definition);new_hash:=md5(audit_definition);
 IF old_hash=new_hash OR (length(ready_definition)-length(replace(ready_definition,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'expected single captured Core audit predecessor';END IF;
 IF position(quote_literal(replace(pantry.prepared_capture_catalog()::text,new_hash,old_hash))||'::jsonb' in ready_definition)=0 THEN
  RAISE EXCEPTION 'prepared capture changed outside the reviewed Core audit fingerprint';END IF;
 EXECUTE replace(ready_definition,old_hash,new_hash);
END $compatibility$;
COMMIT;
