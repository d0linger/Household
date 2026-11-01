BEGIN;
-- Change only the exact captured Core28 audit definition to reviewed Core31.
DO $compatibility$
DECLARE accepted_current text:=$current_audit$
 SELECT core.finance_ready() AND count(*)=121 AND bool_and(EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name))
 AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
 AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate'
 AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure))
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN('r','p')
 AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan') AND c.relname<>'schema_migrations'
 AND NOT(n.nspname='fuel' AND c.relname IN('fuel_types','household_state','segment_state','consumption_segments','segment_jobs','module_operations','module_effects','capture_transitions','legacy_operation_fences'))
 AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
 AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states',
 'financial_requirement_sets','financial_requirement_members','financial_artifact_scope_versions','financial_policy_grants','finance_private_owners','finance_key_tombstones','finance_operations','finance_origin_operations','finance_origin_resources','finance_origin_effects',
 'finance_audit_bindings','finance_ingest_claims','finance_background_authorities','finance_transaction_authorities','finance_artifact_job_authorities','finance_security_events'))
 AND NOT EXISTS(SELECT 1 FROM core.audit_policies a WHERE a.schema_name=n.nspname AND a.table_name=c.relname)) FROM core.audit_policies p
$current_audit$;accepted_prior text:=$prior_audit$
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
$prior_audit$;audit_source text;audit_definition text;ready_definition text;old_definition text;old_hash text;new_hash text;candidate text;accepted_hash text;matches integer:=0;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=31 AND NOT dirty) FROM core.schema_migrations)
 OR NOT core.finance_ready() OR NOT core.audit_ready() THEN RAISE EXCEPTION 'prepared capture compatibility requires reviewed Core31 Finance';END IF;
 SELECT p.prosrc,pg_get_functiondef(p.oid) INTO audit_source,audit_definition FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='core.audit_ready()'::regprocedure AND p.prokind='f' AND p.prosecdef AND p.provolatile='s'
 AND p.prorettype='boolean'::regtype AND p.proowner='household_owner'::regrole AND l.lanname='sql' AND p.proconfig=ARRAY['search_path=pg_catalog'];
 SELECT pg_get_functiondef(p.oid) INTO ready_definition FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='pantry.prepared_capture_ready()'::regprocedure AND p.prokind='f' AND p.prosecdef AND p.provolatile='s'
 AND p.prorettype='boolean'::regtype AND p.proowner='household_owner'::regrole AND l.lanname='sql' AND p.proconfig=ARRAY['search_path=pg_catalog'];
 IF replace(audit_source,chr(13),'') IS DISTINCT FROM accepted_current OR ready_definition IS NULL
 OR (length(audit_definition)-length(replace(audit_definition,audit_source,'')))/length(audit_source)<>1 THEN
 RAISE EXCEPTION 'unreviewed prepared capture Finance compatibility predecessor';END IF;
 new_hash=md5(audit_definition);
 -- Prior installations can have LF or CRLF migration bodies; admit only these
 -- two exact byte forms, and bind every other catalog entry to its old literal.
 FOREACH candidate IN ARRAY ARRAY[accepted_prior,replace(accepted_prior,chr(10),chr(13)||chr(10))] LOOP
 old_definition=replace(audit_definition,audit_source,candidate);old_hash=md5(old_definition);
 IF old_hash<>new_hash AND (length(ready_definition)-length(replace(ready_definition,old_hash,'')))/length(old_hash)=1
 AND position(quote_literal(replace(pantry.prepared_capture_catalog()::text,new_hash,old_hash))||'::jsonb' in ready_definition)>0
 AND position(quote_literal(md5(pg_get_functiondef('pantry.prepared_capture_catalog()'::regprocedure))) in ready_definition)>0 THEN
 matches=matches+1;accepted_hash=old_hash;END IF;
 END LOOP;
 IF matches<>1 THEN RAISE EXCEPTION 'prepared capture differs outside its one accepted Core audit predecessor';END IF;
 EXECUTE replace(ready_definition,accepted_hash,new_hash);
 IF NOT pantry.prepared_capture_ready() THEN RAISE EXCEPTION 'prepared capture Finance compatibility failed';END IF;
END $compatibility$;
COMMIT;