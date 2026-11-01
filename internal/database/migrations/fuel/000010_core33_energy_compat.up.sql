BEGIN;
DO $compatibility$
DECLARE baseline jsonb;item text;expected text;actual text;authority text;overview text;ready text;needle text;
 old_hash text;new_hash text;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=33 AND NOT dirty) FROM core.schema_migrations)
  OR NOT core.finance_ready() OR NOT core.audit_ready() OR NOT chores.expansion_ready() THEN RAISE EXCEPTION 'Fuel compatibility requires reviewed Core33';END IF;
 baseline=meters.predecessor_catalog()->'functions';
 FOREACH item IN ARRAY ARRAY['fuel.module_ready()','fuel.overview_ready()','fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'] LOOP
  expected=baseline->item->>'definition';
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=baseline->item->>'owner' AND p.proacl::text IS NOT DISTINCT FROM baseline->item->>'acl';
  IF actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'unreviewed Fuel Chores predecessor';END IF;
 END LOOP;
 authority=baseline->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 overview=baseline->'fuel.overview_ready()'->>'definition';ready=baseline->'fuel.module_ready()'->>'definition';
 needle='bool_and(version=32 AND NOT dirty)';old_hash=md5(authority);
 IF (length(authority)-length(replace(authority,needle,'')))/length(needle)<>1
  OR (length(overview)-length(replace(overview,needle,'')))/length(needle)<>1
  OR (length(ready)-length(replace(ready,old_hash,'')))/length(old_hash)<>1
  OR position(quote_literal(fuel.module_catalog()::text)||'::jsonb' in ready)=0
  OR position(quote_literal(md5(pg_get_functiondef('fuel.module_catalog()'::regprocedure))) in ready)=0 THEN
  RAISE EXCEPTION 'Fuel Chores catalog differs from the frozen predecessor';
 END IF;
 EXECUTE replace(overview,needle,'bool_and(version=33 AND NOT dirty)');
 EXECUTE replace(authority,needle,'bool_and(version=33 AND NOT dirty)');
 new_hash=md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure));
 IF new_hash=old_hash THEN RAISE EXCEPTION 'Fuel Chores authority delta was not installed';END IF;
 EXECUTE replace(ready,old_hash,new_hash);
 IF NOT fuel.module_ready() OR NOT fuel.overview_ready() THEN RAISE EXCEPTION 'Fuel Chores compatibility failed';END IF;
END $compatibility$;
COMMIT;
