BEGIN;
-- Core's version is also part of Fuel's fail-closed authority contract.
-- Reconstruct the reviewed Fuel11 predecessor from the frozen Documents
-- catalog; never accept arbitrary live function definitions as a baseline.
DO $compatibility$
DECLARE baseline jsonb;item text;expected text;actual text;authority text;overview text;ready text;
 old_hash text;needle text;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=11 AND NOT dirty) FROM fuel.schema_migrations) THEN
  RAISE EXCEPTION 'Navigation preferences require reviewed Fuel11';END IF;
 baseline=documents.predecessor_catalog()->'functions';
 authority=baseline->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 overview=baseline->'fuel.overview_ready()'->>'definition';
 ready=baseline->'fuel.module_ready()'->>'definition';
 needle='bool_and(version=33 AND NOT dirty)';old_hash=md5(authority);
 IF authority IS NULL OR overview IS NULL OR ready IS NULL
  OR (length(authority)-length(replace(authority,needle,'')))/length(needle)<>1
  OR (length(overview)-length(replace(overview,needle,'')))/length(needle)<>1
  OR (length(ready)-length(replace(ready,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'Navigation preferences Fuel catalog differs from its frozen predecessor';END IF;
 authority=replace(authority,needle,'bool_and(version=36 AND NOT dirty)');
 overview=replace(overview,needle,'bool_and(version=36 AND NOT dirty)');
 ready=replace(ready,old_hash,md5(authority));
 IF position(quote_literal(fuel.module_catalog()::text)||'::jsonb' in ready)=0
  OR position(quote_literal(md5(pg_get_functiondef('fuel.module_catalog()'::regprocedure))) in ready)=0 THEN
  RAISE EXCEPTION 'Navigation preferences Fuel11 catalog differs from its frozen predecessor';END IF;
 FOREACH item IN ARRAY ARRAY['fuel.module_ready()','fuel.overview_ready()','fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'] LOOP
  expected=CASE item WHEN 'fuel.module_ready()' THEN ready WHEN 'fuel.overview_ready()' THEN overview ELSE authority END;
  actual=NULL;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=baseline->item->>'owner' AND p.proacl::text IS NOT DISTINCT FROM baseline->item->>'acl';
  IF actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'unreviewed Navigation preferences Fuel predecessor: %',item;END IF;
 END LOOP;
 old_hash=md5(authority);needle='bool_and(version=36 AND NOT dirty)';
 EXECUTE replace(overview,needle,'bool_and(version=37 AND NOT dirty)');
 EXECUTE replace(authority,needle,'bool_and(version=37 AND NOT dirty)');
 EXECUTE replace(ready,old_hash,md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)));
END $compatibility$;
-- A small, account-owned order; absent modules are appended by the renderer.
-- These are the nine installed domain IDs, never display names or arbitrary URLs.
CREATE FUNCTION core.valid_module_order(value text[]) RETURNS boolean
LANGUAGE sql IMMUTABLE PARALLEL SAFE SET search_path=pg_catalog AS $$
 SELECT CASE WHEN coalesce(array_ndims(value),1)<>1 OR coalesce(array_lower(value,1),1)<>1
 THEN false ELSE coalesce(cardinality(value)<=9 AND array_position(value,NULL) IS NULL
  AND value <@ ARRAY['shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan']::text[]
  AND (SELECT count(*)=count(DISTINCT item) FROM unnest(value) AS entries(item)),false) END
$$;
REVOKE ALL ON FUNCTION core.valid_module_order(text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.valid_module_order(text[]) TO household_app;
ALTER TABLE core.user_preferences
 ADD COLUMN module_order text[] NOT NULL DEFAULT '{}'::text[],
 ADD CONSTRAINT user_preferences_module_order_check CHECK(core.valid_module_order(module_order));
-- Module IDs are non-secret preference values. Capture exact before/after order
-- under the existing account policy, without adding a new table or audit policy.
DO $audit_policy$
BEGIN
 UPDATE core.audit_policies SET safe_columns=array_append(safe_columns,'module_order')
 WHERE schema_name='core' AND table_name='user_preferences'
 AND safe_columns=ARRAY['user_id','theme','locale','default_module_id','offline_storage_enabled']::text[];
 IF NOT FOUND THEN RAISE EXCEPTION 'unreviewed Navigation preferences audit policy';END IF;
END $audit_policy$;
-- The audit writer rejects structured values by default. Admit only this
-- bounded array of public module IDs; every other array/object stays rejected.
-- Verify the reviewed Core21 writer before changing its exact branch.
DO $audit_writer$
DECLARE source text;definition text;needle text;replacement text;
BEGIN
 SELECT replace(p.prosrc,chr(13),''),pg_get_functiondef(p.oid) INTO source,definition
 FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='core.write_audit_row(text,text,text,jsonb,jsonb)'::regprocedure
 AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.prokind='f'
 AND p.provolatile='v' AND p.prorettype='void'::regtype AND l.lanname='plpgsql'
 AND p.proconfig=ARRAY['search_path=pg_catalog']
 AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
  WHERE a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE');
 IF source IS NULL OR encode(sha256(convert_to(source,'UTF8')),'hex')<>'c178c6409253ad42f3a68419f8da855cd2d7311610e1afb3b1914f5fd9e92a62' THEN
  RAISE EXCEPTION 'unreviewed Navigation preferences audit writer predecessor';END IF;
 needle='  ELSIF jsonb_typeof(old_value) IN(''object'',''array'') OR jsonb_typeof(new_value) IN(''object'',''array'') THEN';
 replacement=$branch$  ELSIF p_schema='core' AND p_table='user_preferences' AND column_name='module_order' THEN
   IF old_present AND (jsonb_typeof(old_value) IS DISTINCT FROM 'array' OR octet_length(old_value::text)>256)
    OR new_present AND (jsonb_typeof(new_value) IS DISTINCT FROM 'array' OR octet_length(new_value::text)>256) THEN
    RAISE EXCEPTION 'invalid bounded navigation audit value';END IF;
   IF old_present AND NOT core.valid_module_order(ARRAY(SELECT jsonb_array_elements_text(old_value)))
    OR new_present AND NOT core.valid_module_order(ARRAY(SELECT jsonb_array_elements_text(new_value))) THEN
    RAISE EXCEPTION 'invalid bounded navigation audit value';END IF;
$branch$||needle;
 IF (length(definition)-length(replace(definition,needle,'')))/length(needle)<>1 THEN
  RAISE EXCEPTION 'Navigation preferences audit writer branch differs';END IF;
 EXECUTE replace(definition,needle,replacement);
END $audit_writer$;
COMMIT;
