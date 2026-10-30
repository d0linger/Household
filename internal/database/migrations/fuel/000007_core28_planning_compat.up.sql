BEGIN;
-- The accepted Fuel 6 overview guard pins Core 27. Preserve its captured
-- column/constraint/index fingerprints while admitting the reviewed Core 28.
DO $install$
DECLARE prior_body text; required text := 'bool_and(version=27 AND NOT dirty)';
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=28 AND NOT dirty) FROM core.schema_migrations)
 OR NOT core.planning_extensions_ready() THEN
  RAISE EXCEPTION 'Fuel overview compatibility requires reviewed Core 28';
 END IF;
 SELECT p.prosrc INTO prior_body FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='fuel.overview_ready()'::regprocedure AND p.prokind='f'
 AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.provolatile='s'
 AND p.prorettype='boolean'::regtype AND p.proconfig=ARRAY['search_path=pg_catalog']
 AND l.lanname='sql';
 IF prior_body IS NULL OR
  (length(prior_body)-length(replace(prior_body,required,'')))/length(required)<>1 THEN
  RAISE EXCEPTION 'unreviewed Fuel overview predecessor';
 END IF;
 EXECUTE format('CREATE OR REPLACE FUNCTION fuel.overview_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  replace(prior_body,required,'bool_and(version=28 AND NOT dirty)'));
 IF NOT fuel.overview_ready() THEN
  RAISE EXCEPTION 'Fuel overview compatibility failed';
 END IF;
END $install$;
-- Module commands have the same exact Core predecessor fence. Replace only
-- that predicate, then update its one captured definition hash in readiness.
-- All other accepted catalog fingerprints and function metadata stay intact.
DO $authority$
DECLARE authority_definition text; ready_definition text; old_hash text; new_hash text;
 required text := 'bool_and(version=27 AND NOT dirty)';
BEGIN
 SELECT pg_get_functiondef(p.oid) INTO authority_definition
 FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure
 AND p.prokind='f' AND p.proowner='household_owner'::regrole
 AND p.prosecdef AND p.provolatile='v' AND p.prorettype='void'::regtype
 AND p.proconfig=ARRAY['search_path=pg_catalog'] AND l.lanname='plpgsql';
 SELECT pg_get_functiondef(p.oid) INTO ready_definition
 FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='fuel.module_ready()'::regprocedure AND p.prokind='f'
 AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.provolatile='s'
 AND p.prorettype='boolean'::regtype AND p.proconfig=ARRAY['search_path=pg_catalog']
 AND l.lanname='sql';
 IF authority_definition IS NULL OR ready_definition IS NULL OR
  (length(authority_definition)-length(replace(authority_definition,required,'')))/length(required)<>1 THEN
  RAISE EXCEPTION 'unreviewed Fuel authority predecessor';
 END IF;
 old_hash:=md5(authority_definition);
 IF (length(ready_definition)-length(replace(ready_definition,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'Fuel authority fingerprint is not the accepted readiness baseline';
 END IF;
 EXECUTE replace(authority_definition,required,'bool_and(version=28 AND NOT dirty)');
 SELECT md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)) INTO new_hash;
 IF new_hash=old_hash THEN RAISE EXCEPTION 'Fuel authority compatibility was not applied'; END IF;
 EXECUTE replace(ready_definition,old_hash,new_hash);
END $authority$;
COMMIT;
