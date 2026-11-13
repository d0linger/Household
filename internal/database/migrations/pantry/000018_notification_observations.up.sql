BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

DO $predecessor$
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=38 AND NOT dirty) FROM core.schema_migrations)
  OR NOT core.audit_ready() OR NOT core.notification_foundation_ready() OR NOT pantry.prepared_capture_ready()
  OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
  RAISE EXCEPTION 'Pantry observations require reviewed Core38/Pantry17';
 END IF;
END $predecessor$;

CREATE TABLE pantry.notification_stock_observations(
 household_id uuid NOT NULL,product_id uuid NOT NULL,
 module_generation bigint NOT NULL CHECK(module_generation BETWEEN 1 AND 9007199254740991),
 module_activated_at timestamptz NOT NULL,
 observed_quantity numeric(40,6) NOT NULL CHECK(observed_quantity>=0 AND observed_quantity<>'NaN'::numeric),
 observed_threshold numeric(18,6) CHECK(observed_threshold>=0 AND observed_threshold<>'NaN'::numeric),
 unit_code text NOT NULL CHECK(unit_code ~ '^[A-Za-z0-9_]{1,16}$'),
 observed_at timestamptz NOT NULL,last_healthy_at timestamptz,
 state text NOT NULL CHECK(state IN('baseline','healthy','low','ineligible')),
 episode bigint NOT NULL DEFAULT 0 CHECK(episode BETWEEN 0 AND 9007199254740991),
 detected_at timestamptz,consent_cutoff timestamptz,
 PRIMARY KEY(household_id,product_id),
 FOREIGN KEY(household_id,product_id) REFERENCES pantry.products(household_id,id) ON DELETE CASCADE,
 CHECK(isfinite(observed_at) AND observed_at>='2000-01-01 00:00:00+00'::timestamptz
  AND observed_at<'10000-01-01 00:00:00+00'::timestamptz
  AND isfinite(module_activated_at) AND module_activated_at>='2000-01-01 00:00:00+00'::timestamptz
  AND module_activated_at<=observed_at),
 CHECK((state='healthy' AND observed_threshold IS NOT NULL AND observed_quantity>=observed_threshold
   AND last_healthy_at=observed_at AND last_healthy_at IS NOT NULL AND detected_at IS NULL AND consent_cutoff IS NULL)
  OR(state='low' AND observed_threshold IS NOT NULL AND observed_quantity<observed_threshold AND episode>0
   AND last_healthy_at IS NOT NULL AND detected_at IS NOT NULL AND consent_cutoff IS NOT NULL
   AND consent_cutoff=last_healthy_at AND module_activated_at<=consent_cutoff
   AND consent_cutoff<=detected_at AND detected_at<=observed_at)
  OR(state IN('baseline','ineligible') AND last_healthy_at IS NULL AND detected_at IS NULL AND consent_cutoff IS NULL
   AND(state='ineligible' OR(observed_threshold IS NOT NULL AND observed_quantity<observed_threshold))))
);
CREATE INDEX notification_stock_observations_low ON pantry.notification_stock_observations
 (household_id,module_generation,detected_at,product_id) WHERE state='low';
REVOKE ALL ON pantry.notification_stock_observations FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON pantry.notification_stock_observations TO household_app;

-- This is bounded worker observation state, not a user edit stream. Preserve
-- the 165 audited domain/consent policies and exempt exactly this operational
-- table alongside the stock projection. Reconstruct the accepted Core38 text
-- from the frozen Documents predecessor, never bless arbitrary live functions.
DO $audit_compatibility$
DECLARE baseline jsonb;original_audit text;old_audit text;new_audit text;old_pantry text;
 item text;expected text;actual text;needle text;
BEGIN
 baseline=documents.predecessor_catalog()->'functions';
 original_audit=baseline->'core.audit_ready()'->>'definition';
 old_audit=replace(original_audit,'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND count(*)=152',
  'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND documents.expansion_ready() AND count(*)=165');
 old_audit=replace(old_audit,'''auth_challenges'',''rate_limit_buckets''','''auth_challenges'',''passkey_credentials'',''rate_limit_buckets''');
 old_audit=replace(old_audit,'''jobs'',''outbox_events''','''jobs'',''notification_intents'',''notification_deliveries'',''outbox_events''');
 old_pantry=replace(baseline->'pantry.prepared_capture_ready()'->>'definition',md5(original_audit),md5(old_audit));
 IF original_audit IS NULL OR old_audit IS NULL OR old_pantry IS NULL
  OR position(quote_literal(pantry.prepared_capture_catalog()::text)||'::jsonb' in old_pantry)=0
  OR position(quote_literal(md5(pg_get_functiondef('pantry.prepared_capture_catalog()'::regprocedure))) in old_pantry)=0 THEN
  RAISE EXCEPTION 'Pantry observation predecessor catalog differs';END IF;
 FOREACH item IN ARRAY ARRAY['core.audit_ready()','pantry.prepared_capture_ready()'] LOOP
  expected=CASE item WHEN 'core.audit_ready()' THEN old_audit ELSE old_pantry END;actual=NULL;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=baseline->item->>'owner'
   AND p.proacl::text IS NOT DISTINCT FROM baseline->item->>'acl';
  IF actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'unreviewed Pantry observation predecessor: %',item;END IF;
 END LOOP;
 needle='AND NOT(n.nspname=''pantry'' AND c.relname=''lot_balances'')';
 IF (length(old_audit)-length(replace(old_audit,needle,'')))/length(needle)<>1 THEN
  RAISE EXCEPTION 'Pantry operational classification differs';END IF;
 new_audit=replace(old_audit,needle,'AND NOT(n.nspname=''pantry'' AND c.relname IN(''lot_balances'',''notification_stock_observations''))');
 EXECUTE new_audit;
 EXECUTE replace(old_pantry,md5(old_audit),md5(new_audit));
 IF NOT core.audit_ready() OR NOT pantry.prepared_capture_ready() OR NOT core.finance_ready()
  OR NOT core.planning_extensions_ready() OR NOT mealplan.expansion_ready() THEN
  RAISE EXCEPTION 'incomplete Pantry observation compatibility';END IF;
END $audit_compatibility$;

-- Pin the deparsed form used by pg_dump/pg_restore rather than parser spelling.
DO $canonical$
DECLARE item record;
BEGIN
 FOR item IN SELECT conname,pg_get_constraintdef(oid) definition FROM pg_constraint
  WHERE conrelid='pantry.notification_stock_observations'::regclass AND contype='c' ORDER BY conname LOOP
  EXECUTE format('ALTER TABLE pantry.notification_stock_observations DROP CONSTRAINT %I',item.conname);
  EXECUTE format('ALTER TABLE pantry.notification_stock_observations ADD CONSTRAINT %I %s',item.conname,item.definition);
 END LOOP;
END $canonical$;

CREATE FUNCTION pantry.notification_observations_catalog() RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
 'table',(SELECT jsonb_build_array(c.relowner::regrole::text,c.relkind,c.relpersistence,c.relrowsecurity,c.relforcerowsecurity,
  c.relispartition,c.relacl::text,EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid))
  FROM pg_class c WHERE c.oid='pantry.notification_stock_observations'::regclass),
 'columns',(SELECT jsonb_agg(jsonb_build_array(a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attidentity,a.attgenerated,
  a.attcollation::regcollation::text,a.attacl::text,coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum)
  FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
  WHERE a.attrelid='pantry.notification_stock_observations'::regclass AND a.attnum>0 AND NOT a.attisdropped),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(conname,pg_get_constraintdef(oid),convalidated,conislocal,coninhcount,connoinherit) ORDER BY conname)
  FROM pg_constraint WHERE conrelid='pantry.notification_stock_observations'::regclass),
 'indexes',(SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(indexrelid),indisvalid,indisready,indislive) ORDER BY indexrelid::regclass::text)
  FROM pg_index WHERE indrelid='pantry.notification_stock_observations'::regclass),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(tgname,pg_get_triggerdef(oid),tgenabled) ORDER BY tgname)
  FROM pg_trigger WHERE tgrelid='pantry.notification_stock_observations'::regclass AND NOT tgisinternal),
 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY table_name) FROM core.audit_policies p
  WHERE schema_name='pantry' AND table_name='notification_stock_observations'),
 'compatibility',(SELECT jsonb_agg(jsonb_build_array(p.oid::regprocedure::text,md5(pg_get_functiondef(p.oid)),
  p.proowner::regrole::text,p.proacl::text,p.proconfig,p.prosecdef) ORDER BY p.oid::regprocedure::text)
  FROM pg_proc p WHERE p.oid IN('core.audit_ready()'::regprocedure,'pantry.prepared_capture_ready()'::regprocedure)))
$$;
REVOKE ALL ON FUNCTION pantry.notification_observations_catalog() FROM PUBLIC,household_app;
DO $ready$
DECLARE expected jsonb;catalog_hash text;
BEGIN
 expected=pantry.notification_observations_catalog();
 catalog_hash=md5(pg_get_functiondef('pantry.notification_observations_catalog()'::regprocedure));
 EXECUTE format($definition$CREATE FUNCTION pantry.notification_observations_ready() RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT pantry.notification_observations_catalog()=%L::jsonb
 AND md5(pg_get_functiondef('pantry.notification_observations_catalog()'::regprocedure))=%L
 AND EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='pantry.notification_observations_catalog()'::regprocedure
  AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']
  AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
   WHERE a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE'))
 $body$$definition$,expected,catalog_hash);
END $ready$;
REVOKE ALL ON FUNCTION pantry.notification_observations_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.notification_observations_ready() TO household_app;
COMMIT;
