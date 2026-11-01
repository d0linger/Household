BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
DO $predecessor$
DECLARE baseline jsonb;item record;actual text;policies jsonb;
BEGIN
 IF NOT meters.expansion_ready() OR NOT core.finance_ready() OR NOT chores.expansion_ready()
  OR NOT mealplan.expansion_ready() OR (SELECT count(*) FROM core.audit_policies)<>135 THEN
  RAISE EXCEPTION 'Energy audit requires its complete accepted predecessor';END IF;
 baseline=meters.predecessor_catalog();
 SELECT jsonb_agg(to_jsonb(policy) ORDER BY schema_name,table_name) INTO policies FROM core.audit_policies policy;
 IF policies IS DISTINCT FROM baseline->'audit_policies' OR core.finance_catalog() IS DISTINCT FROM baseline->'finance_catalog' THEN
  RAISE EXCEPTION 'unreviewed Energy audit/Finance predecessor';END IF;
 FOR item IN SELECT key,value FROM jsonb_each(baseline->'functions') LOOP
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item.key)
   AND p.proowner::regrole::text=item.value->>'owner' AND p.proacl::text IS NOT DISTINCT FROM item.value->>'acl';
  IF actual IS DISTINCT FROM item.value->>'definition' THEN RAISE EXCEPTION 'unreviewed Energy compatibility predecessor: %',item.key;END IF;
 END LOOP;
END $predecessor$;
INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('meters','registers',ARRAY['id'],ARRAY['meter_id','name','measurement_kind','unit_code','direction','timezone','input_mode','is_default','archived_at','data_revision','version'],'name'),
 ('meters','device_registers',ARRAY['id'],ARRAY['register_id','device_id','started_at','ended_at','initial_value','final_value','rollover_value','legacy_boundary','version'],NULL),
 ('meters','reading_revisions',ARRAY['id'],ARRAY['reading_id','register_id','device_id','revision','measured_at','kind','deleted'],NULL),
 ('meters','device_changes',ARRAY['id'],ARRAY['register_id','old_mapping_id','new_mapping_id','changed_at','gap_acknowledged'],NULL),
 ('meters','tariff_versions',ARRAY['id'],ARRAY['register_id','starts_on','ends_before','currency_code','version'],NULL),
 ('meters','payments',ARRAY['id'],ARRAY['register_id','paid_on','amount_minor','version'],NULL),
 ('meters','bills',ARRAY['id'],ARRAY['register_id','occurred_on','kind','confirmed_at','expense_id','expense_generation','version'],NULL),
 ('meters','reading_plans',ARRAY['id'],ARRAY['register_id','anchor_on','interval_days','actual_only','active','version'],NULL),
 ('meters','plan_completions',ARRAY['id'],ARRAY['plan_id','plan_version','due_on','reading_id','reading_revision'],NULL),
 ('meters','import_batches',ARRAY['id'],ARRAY['register_id','state','row_count','module_generation','register_version','device_mapping_id','mapping_version','version'],NULL),
 ('meters','import_rows',ARRAY['id'],ARRAY['batch_id','row_number','device_id','reading_id'],NULL),
 ('meters','integration_sources',ARRAY['id'],ARRAY['register_id','device_id','adapter','measurement_kind','unit_code','enabled','module_generation','status_code','consecutive_failures','version'],NULL),
 ('meters','telemetry',ARRAY['id'],ARRAY['register_id','device_id','source_id','source_version','measured_at','kind'],NULL),
 ('meters','intervals',ARRAY['id'],ARRAY['register_id','device_id','from_at','through_at','unit_code','quality','observation_kind','source_revision'],NULL),
 ('meters','aggregates',ARRAY['id'],ARRAY['register_id','bucket_kind','from_at','through_at','covered_seconds','estimated_seconds','source_revision'],NULL),
 ('meters','dirty_ranges',ARRAY['register_id'],ARRAY['from_at','through_at','phase','source_revision'],NULL),
 ('meters','command_receipts',ARRAY['household_id','user_id','command_key'],ARRAY['operation','module_generation'],NULL);
DO $compatibility$
DECLARE baseline jsonb;old_catalog jsonb;new_catalog jsonb;old_others jsonb;new_others jsonb;
 producer text;ready text;audit_definition text;needle text;replacement text;
BEGIN
 baseline=meters.predecessor_catalog();old_catalog=baseline->'finance_catalog';
 producer=baseline->'functions'->'core.finance_producers_ready()'->>'definition';
 needle='(SELECT count(*) FROM core.audit_policies)=135';
 IF (length(producer)-length(replace(producer,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'unreviewed Finance audit count';END IF;
 EXECUTE replace(producer,needle,'(SELECT count(*) FROM core.audit_policies)=152');
 new_catalog=core.finance_catalog();
 SELECT jsonb_agg(value ORDER BY ordinal) INTO old_others FROM jsonb_array_elements(old_catalog->'functions') WITH ORDINALITY x(value,ordinal)
  WHERE value->>0<>'core.finance_producers_ready()';
 SELECT jsonb_agg(value ORDER BY ordinal) INTO new_others FROM jsonb_array_elements(new_catalog->'functions') WITH ORDINALITY x(value,ordinal)
  WHERE value->>0<>'core.finance_producers_ready()';
 IF old_catalog-'functions' IS DISTINCT FROM new_catalog-'functions' OR old_others IS DISTINCT FROM new_others THEN
  RAISE EXCEPTION 'Finance compatibility differs outside its one reviewed producer count';
 END IF;
 ready=baseline->'functions'->'core.finance_ready()'->>'definition';
 needle=quote_literal(old_catalog::text)||'::jsonb';replacement=quote_literal(new_catalog::text)||'::jsonb';
 IF (length(ready)-length(replace(ready,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'unreviewed complete Finance readiness';END IF;
 EXECUTE replace(ready,needle,replacement);
 IF NOT core.finance_ready() THEN RAISE EXCEPTION 'Finance readiness lost during Energy extension';END IF;
 audit_definition=baseline->'functions'->'core.audit_ready()'->>'definition';
 needle='core.finance_ready() AND chores.expansion_ready() AND count(*)=135';
 IF (length(audit_definition)-length(replace(audit_definition,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'unreviewed complete audit predecessor';END IF;
 EXECUTE replace(audit_definition,needle,'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND count(*)=152');
END $compatibility$;
SELECT core.install_audit_triggers();
DO $ready$
BEGIN
 IF NOT chores.expansion_ready() OR NOT meters.expansion_ready() OR NOT core.finance_ready() OR NOT core.audit_ready()
  OR (SELECT count(*) FROM core.audit_policies)<>152 THEN RAISE EXCEPTION 'incomplete Energy audit/Finance composition';END IF;
END $ready$;
COMMIT;
