BEGIN;
-- Core's version is also part of Fuel's fail-closed authority contract.
-- Reconstruct the reviewed Fuel11 predecessor from the frozen Documents
-- catalog; never accept arbitrary live function definitions as a baseline.
DO $compatibility$
DECLARE baseline jsonb;item text;expected text;actual text;authority text;overview text;ready text;
 old_hash text;needle text;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=11 AND NOT dirty) FROM fuel.schema_migrations) THEN
  RAISE EXCEPTION 'Theme preferences require reviewed Fuel11';END IF;
 baseline=documents.predecessor_catalog()->'functions';
 authority=baseline->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 overview=baseline->'fuel.overview_ready()'->>'definition';
 ready=baseline->'fuel.module_ready()'->>'definition';
 needle='bool_and(version=33 AND NOT dirty)';old_hash=md5(authority);
 IF authority IS NULL OR overview IS NULL OR ready IS NULL
  OR (length(authority)-length(replace(authority,needle,'')))/length(needle)<>1
  OR (length(overview)-length(replace(overview,needle,'')))/length(needle)<>1
  OR (length(ready)-length(replace(ready,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'Theme preferences Fuel catalog differs from its frozen predecessor';END IF;
 authority=replace(authority,needle,'bool_and(version=34 AND NOT dirty)');
 overview=replace(overview,needle,'bool_and(version=34 AND NOT dirty)');
 ready=replace(ready,old_hash,md5(authority));
 IF position(quote_literal(fuel.module_catalog()::text)||'::jsonb' in ready)=0
  OR position(quote_literal(md5(pg_get_functiondef('fuel.module_catalog()'::regprocedure))) in ready)=0 THEN
  RAISE EXCEPTION 'Theme preferences Fuel11 catalog differs from its frozen predecessor';END IF;
 FOREACH item IN ARRAY ARRAY['fuel.module_ready()','fuel.overview_ready()','fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'] LOOP
  expected=CASE item WHEN 'fuel.module_ready()' THEN ready WHEN 'fuel.overview_ready()' THEN overview ELSE authority END;
  actual=NULL;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=baseline->item->>'owner' AND p.proacl::text IS NOT DISTINCT FROM baseline->item->>'acl';
  IF actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'unreviewed Theme preferences Fuel predecessor: %',item;END IF;
 END LOOP;
 old_hash=md5(authority);needle='bool_and(version=34 AND NOT dirty)';
 EXECUTE replace(overview,needle,'bool_and(version=35 AND NOT dirty)');
 EXECUTE replace(authority,needle,'bool_and(version=35 AND NOT dirty)');
 EXECUTE replace(ready,old_hash,md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)));
END $compatibility$;
ALTER TABLE core.user_preferences
 DROP CONSTRAINT user_preferences_theme_check,
 ADD CONSTRAINT user_preferences_theme_check
 CHECK (theme IN ('system', 'light', 'dark', 'pink-light', 'pink-dark'));
COMMIT;
