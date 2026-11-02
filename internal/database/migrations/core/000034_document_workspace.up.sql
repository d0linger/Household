BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
DO $predecessor$
DECLARE baseline jsonb;item record;actual text;policies jsonb;
BEGIN
 IF NOT documents.expansion_ready() OR NOT meters.expansion_ready() OR NOT core.finance_ready() OR NOT chores.expansion_ready()
  OR NOT mealplan.expansion_ready() OR (SELECT count(*) FROM core.audit_policies)<>152 THEN
  RAISE EXCEPTION 'Documents audit requires its complete accepted predecessor';END IF;
 baseline=documents.predecessor_catalog();
 SELECT jsonb_agg(to_jsonb(policy) ORDER BY schema_name,table_name) INTO policies FROM core.audit_policies policy;
 IF policies IS DISTINCT FROM baseline->'audit_policies' OR core.finance_catalog() IS DISTINCT FROM baseline->'finance_catalog' THEN
  RAISE EXCEPTION 'unreviewed Documents audit/Finance predecessor';END IF;
 FOR item IN SELECT key,value FROM jsonb_each(baseline->'functions') LOOP
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item.key)
   AND p.proowner::regrole::text=item.value->>'owner' AND p.proacl::text IS NOT DISTINCT FROM item.value->>'acl';
  IF actual IS DISTINCT FROM item.value->>'definition' THEN RAISE EXCEPTION 'unreviewed Documents compatibility predecessor: %',item.key;END IF;
 END LOOP;
END $predecessor$;
INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('documents','profiles',ARRAY['document_id'],ARRAY['review_state'],NULL),
 ('documents','file_versions',ARRAY['id'],ARRAY['household_id'],NULL),
 ('documents','scan_sets',ARRAY['id'],ARRAY['household_id'],NULL),
 ('documents','scan_pages',ARRAY['id'],ARRAY['household_id'],NULL),
 ('documents','deadlines',ARRAY['id'],ARRAY['household_id'],NULL),
 ('documents','saved_views',ARRAY['id'],ARRAY['household_id'],NULL),
 ('documents','rules',ARRAY['id'],ARRAY['household_id'],NULL),
 ('documents','processing',ARRAY['id'],ARRAY['state'],NULL),
 ('documents','integration_sources',ARRAY['id'],ARRAY['adapter'],NULL),
 ('documents','import_receipts',ARRAY['id'],ARRAY['household_id'],NULL),
 ('documents','command_receipts',ARRAY['id'],ARRAY['operation'],NULL);
DO $compatibility$
DECLARE baseline jsonb;old_catalog jsonb;new_catalog jsonb;old_others jsonb;new_others jsonb;
 producer text;ready text;audit_definition text;needle text;replacement text;
BEGIN
 baseline=documents.predecessor_catalog();old_catalog=baseline->'finance_catalog';
 producer=baseline->'functions'->'core.finance_producers_ready()'->>'definition';
 needle='(SELECT count(*) FROM core.audit_policies)=152';
 IF (length(producer)-length(replace(producer,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'unreviewed Finance audit count';END IF;
 EXECUTE replace(producer,needle,'(SELECT count(*) FROM core.audit_policies)=163');
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
 IF NOT core.finance_ready() THEN RAISE EXCEPTION 'Finance readiness lost during Documents extension';END IF;
 audit_definition=baseline->'functions'->'core.audit_ready()'->>'definition';
 needle='core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND count(*)=152';
 IF (length(audit_definition)-length(replace(audit_definition,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'unreviewed complete audit predecessor';END IF;
 EXECUTE replace(audit_definition,needle,'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND documents.expansion_ready() AND count(*)=163');
END $compatibility$;
SELECT core.install_audit_triggers();
DO $ready$
BEGIN
 IF NOT chores.expansion_ready() OR NOT meters.expansion_ready() OR NOT core.finance_ready() OR NOT core.audit_ready()
  OR (SELECT count(*) FROM core.audit_policies)<>163 THEN RAISE EXCEPTION 'incomplete Documents audit/Finance composition';END IF;
END $ready$;
COMMIT;
