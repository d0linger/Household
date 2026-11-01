BEGIN;
DO $compatibility$
DECLARE baseline jsonb;expected text;actual text;needle text:='(SELECT count(*) FROM core.audit_policies)=121';
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=32 AND NOT dirty) FROM core.schema_migrations)
  OR NOT core.finance_ready() OR NOT core.audit_ready() OR NOT chores.expansion_ready() THEN RAISE EXCEPTION 'Mealplan compatibility requires reviewed Core32';END IF;
 baseline=chores.predecessor_catalog()->'functions';expected=baseline->'core.planning_extensions_ready()'->>'definition';
 SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid='core.planning_extensions_ready()'::regprocedure
  AND p.proowner::regrole::text=baseline->'core.planning_extensions_ready()'->>'owner'
  AND p.proacl::text IS NOT DISTINCT FROM baseline->'core.planning_extensions_ready()'->>'acl';
 IF actual IS DISTINCT FROM expected OR (length(expected)-length(replace(expected,needle,'')))/length(needle)<>1 THEN
  RAISE EXCEPTION 'unreviewed Mealplan Chores policy predecessor';
 END IF;
 EXECUTE replace(expected,needle,'(SELECT count(*) FROM core.audit_policies)=135');
 IF NOT core.planning_extensions_ready() OR NOT mealplan.expansion_ready() THEN RAISE EXCEPTION 'Mealplan Chores compatibility failed';END IF;
END $compatibility$;
COMMIT;
