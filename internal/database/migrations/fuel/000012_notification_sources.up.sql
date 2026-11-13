BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

CREATE TEMP TABLE fuel_notification_predecessor(
 fuel_catalog jsonb NOT NULL,fuel_ready text NOT NULL,audit_ready text NOT NULL,
 pantry_ready text NOT NULL,pantry_catalog jsonb NOT NULL,pantry_notification_ready text NOT NULL
) ON COMMIT DROP;
REVOKE ALL ON fuel_notification_predecessor FROM PUBLIC,household_app;

-- Reconstruct released Core38/Fuel11/Pantry18 definitions before changing any
-- domain metadata. A passing replacement ready() alone is not a predecessor.
DO $predecessor$
DECLARE baseline jsonb;original_audit text;reviewed_audit text;reviewed_pantry text;
 authority text;ready text;overview text;item text;expected text;actual text;
 observation_catalog jsonb;observation_ready text;catalog_hash text;expected_body text;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=38 AND NOT dirty) FROM core.schema_migrations)
  OR NOT(SELECT count(*)=1 AND bool_and(version=18 AND NOT dirty) FROM pantry.schema_migrations)
  OR NOT core.audit_ready() OR NOT fuel.module_ready() OR NOT pantry.notification_observations_ready()
  OR NOT core.notification_foundation_ready() OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
  RAISE EXCEPTION 'Fuel notifications require reviewed Core38/Fuel11/Pantry18';END IF;
 baseline=documents.predecessor_catalog()->'functions';
 authority=baseline->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 ready=baseline->'fuel.module_ready()'->>'definition';
 ready=replace(ready,md5(authority),md5(replace(authority,'bool_and(version=33 AND NOT dirty)','bool_and(version=38 AND NOT dirty)')));
 authority=replace(authority,'bool_and(version=33 AND NOT dirty)','bool_and(version=38 AND NOT dirty)');
 overview=replace(baseline->'fuel.overview_ready()'->>'definition','bool_and(version=33 AND NOT dirty)','bool_and(version=38 AND NOT dirty)');
 original_audit=baseline->'core.audit_ready()'->>'definition';
 reviewed_audit=replace(original_audit,'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND count(*)=152',
  'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND documents.expansion_ready() AND count(*)=165');
 reviewed_audit=replace(reviewed_audit,'''auth_challenges'',''rate_limit_buckets''','''auth_challenges'',''passkey_credentials'',''rate_limit_buckets''');
 reviewed_audit=replace(reviewed_audit,'''jobs'',''outbox_events''','''jobs'',''notification_intents'',''notification_deliveries'',''outbox_events''');
 reviewed_audit=replace(reviewed_audit,'AND NOT(n.nspname=''pantry'' AND c.relname=''lot_balances'')',
  'AND NOT(n.nspname=''pantry'' AND c.relname IN(''lot_balances'',''notification_stock_observations''))');
 reviewed_pantry=replace(baseline->'pantry.prepared_capture_ready()'->>'definition',md5(original_audit),md5(reviewed_audit));
 IF authority IS NULL OR ready IS NULL OR overview IS NULL OR reviewed_audit IS NULL OR reviewed_pantry IS NULL
  OR position(quote_literal(fuel.module_catalog()::text)||'::jsonb' in ready)=0
  OR position(quote_literal(md5(pg_get_functiondef('fuel.module_catalog()'::regprocedure))) in ready)=0
  OR position(quote_literal(pantry.prepared_capture_catalog()::text)||'::jsonb' in reviewed_pantry)=0 THEN
  RAISE EXCEPTION 'Fuel notification predecessor metadata differs';END IF;
 FOREACH item IN ARRAY ARRAY['fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)','fuel.module_ready()',
  'fuel.overview_ready()','core.audit_ready()','pantry.prepared_capture_ready()'] LOOP
  expected=CASE item WHEN 'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)' THEN authority
   WHEN 'fuel.module_ready()' THEN ready WHEN 'fuel.overview_ready()' THEN overview
   WHEN 'core.audit_ready()' THEN reviewed_audit ELSE reviewed_pantry END;
  actual=NULL;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=baseline->item->>'owner'
   AND p.proacl::text IS NOT DISTINCT FROM baseline->item->>'acl';
  IF actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'unreviewed Fuel notification predecessor: %',item;END IF;
 END LOOP;
 -- This source hash is the normalized SQL body released in Pantry18.
 IF NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='pantry.notification_observations_catalog()'::regprocedure
  AND md5(replace(p.prosrc,E'\r\n',E'\n'))='f0fee082f8cb7d405a90278af784f3e3'
  AND p.proowner='household_owner'::regrole AND p.prolang=(SELECT oid FROM pg_language WHERE lanname='sql')
  AND p.provolatile='s' AND p.prosecdef AND NOT p.proisstrict AND p.prorettype='jsonb'::regtype
  AND p.proconfig=ARRAY['search_path=pg_catalog']) THEN
  RAISE EXCEPTION 'unreviewed Pantry18 observation catalog';END IF;
 observation_catalog=pantry.notification_observations_catalog();
 catalog_hash=md5(pg_get_functiondef('pantry.notification_observations_catalog()'::regprocedure));
 expected_body=format($body$
 SELECT pantry.notification_observations_catalog()=%L::jsonb
 AND md5(pg_get_functiondef('pantry.notification_observations_catalog()'::regprocedure))=%L
 AND EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='pantry.notification_observations_catalog()'::regprocedure
  AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']
  AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
   WHERE a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE'))
 $body$,observation_catalog,catalog_hash);
 SELECT pg_get_functiondef(p.oid) INTO observation_ready FROM pg_proc p
 WHERE p.oid='pantry.notification_observations_ready()'::regprocedure
 AND replace(p.prosrc,E'\r\n',E'\n')=expected_body AND p.proowner='household_owner'::regrole
 AND p.prolang=(SELECT oid FROM pg_language WHERE lanname='sql') AND p.provolatile='s'
 AND p.prosecdef AND NOT p.proisstrict AND p.prorettype='boolean'::regtype
 AND p.proconfig=ARRAY['search_path=pg_catalog']
 AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
  WHERE a.grantee NOT IN(p.proowner,'household_app'::regrole) OR a.privilege_type<>'EXECUTE');
 IF observation_ready IS NULL THEN RAISE EXCEPTION 'unreviewed Pantry18 observation readiness';END IF;
 INSERT INTO fuel_notification_predecessor VALUES(fuel.module_catalog(),ready,reviewed_audit,reviewed_pantry,
  observation_catalog,observation_ready);
END $predecessor$;

-- Owner-only frozen evidence for reviewed rollback/upgrade fixtures. The
-- snapshot is taken after exact predecessor verification and before DDL.
DO $freeze$
DECLARE snapshot jsonb;
BEGIN
 SELECT jsonb_build_object('functions',
  (SELECT jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
   'owner',p.proowner::regrole::text,'acl',p.proacl::text)) FROM pg_proc p WHERE p.oid IN(
   'core.audit_ready()'::regprocedure,'fuel.module_ready()'::regprocedure,'fuel.module_catalog()'::regprocedure,
   'fuel.overview_ready()'::regprocedure,'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure,
   'pantry.prepared_capture_ready()'::regprocedure,'pantry.notification_observations_catalog()'::regprocedure,
   'pantry.notification_observations_ready()'::regprocedure)),
  'fuel_catalog',fuel.module_catalog(),'pantry_observations_catalog',pantry.notification_observations_catalog(),
  'audit_policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY table_name) FROM core.audit_policies p
   WHERE schema_name='fuel' AND table_name IN('vehicles','maintenance_plans'))) INTO snapshot;
 EXECUTE format('CREATE FUNCTION fuel.notification_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $freeze$;
REVOKE ALL ON FUNCTION fuel.notification_predecessor_catalog() FROM PUBLIC,household_app;

ALTER TABLE fuel.vehicles ADD COLUMN responsible_member_id uuid,ADD COLUMN responsible_since timestamptz,
 ADD CONSTRAINT vehicles_responsible_member FOREIGN KEY(household_id,responsible_member_id) REFERENCES core.memberships(household_id,id),
 ADD CONSTRAINT vehicles_responsible_since CHECK((responsible_member_id IS NULL)=(responsible_since IS NULL)
  AND(responsible_since IS NULL OR(isfinite(responsible_since) AND responsible_since>='2000-01-01'::timestamptz AND responsible_since<'10000-01-01'::timestamptz)));
ALTER TABLE fuel.maintenance_plans ADD COLUMN assignee_since timestamptz;
-- Retain existing explicit assignees, but authorize no historical notification.
UPDATE fuel.maintenance_plans SET assignee_since=clock_timestamp() WHERE assignee_id IS NOT NULL;
ALTER TABLE fuel.maintenance_plans ADD CONSTRAINT maintenance_assignee_since CHECK((assignee_id IS NULL)=(assignee_since IS NULL)
 AND(assignee_since IS NULL OR(isfinite(assignee_since) AND assignee_since>='2000-01-01'::timestamptz AND assignee_since<'10000-01-01'::timestamptz)));

CREATE FUNCTION fuel.notification_assignment_clock() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE member_id uuid;changed boolean;owner_session boolean:=pg_has_role(current_user,'household_owner','MEMBER');
BEGIN
 IF TG_TABLE_SCHEMA<>'fuel' OR TG_TABLE_NAME NOT IN('vehicles','maintenance_plans') OR TG_OP NOT IN('INSERT','UPDATE') THEN
  RAISE EXCEPTION 'invalid notification assignment trigger' USING ERRCODE='42501';END IF;
 IF TG_TABLE_NAME='vehicles' THEN
  member_id=NEW.responsible_member_id;changed=TG_OP='INSERT' OR NEW.responsible_member_id IS DISTINCT FROM OLD.responsible_member_id;
  IF changed THEN NEW.responsible_since=CASE WHEN member_id IS NULL THEN NULL ELSE clock_timestamp() END;
  ELSIF NOT owner_session AND NEW.responsible_since IS DISTINCT FROM OLD.responsible_since THEN
   RAISE EXCEPTION 'assignment time is server managed' USING ERRCODE='42501';END IF;
 ELSE
  member_id=NEW.assignee_id;changed=TG_OP='INSERT' OR NEW.assignee_id IS DISTINCT FROM OLD.assignee_id;
  IF changed THEN NEW.assignee_since=CASE WHEN member_id IS NULL THEN NULL ELSE clock_timestamp() END;
  ELSIF NOT owner_session AND NEW.assignee_since IS DISTINCT FROM OLD.assignee_since THEN
   RAISE EXCEPTION 'assignment time is server managed' USING ERRCODE='42501';END IF;
 END IF;
 IF changed AND member_id IS NOT NULL AND NOT owner_session THEN
  PERFORM 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=NEW.household_id
   AND m.id=member_id AND m.inactive_at IS NULL AND u.disabled_at IS NULL FOR SHARE OF m,u NOWAIT;
  IF NOT FOUND THEN RAISE EXCEPTION 'active household responsibility required' USING ERRCODE='42501';END IF;
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION fuel.notification_assignment_clock() FROM PUBLIC,household_app;
CREATE TRIGGER fuel_notification_assignment BEFORE INSERT OR UPDATE ON fuel.vehicles
 FOR EACH ROW EXECUTE FUNCTION fuel.notification_assignment_clock();
CREATE TRIGGER fuel_notification_assignment BEFORE INSERT OR UPDATE ON fuel.maintenance_plans
 FOR EACH ROW EXECUTE FUNCTION fuel.notification_assignment_clock();
UPDATE core.audit_policies SET safe_columns=safe_columns||ARRAY['responsible_member_id','responsible_since']
 WHERE schema_name='fuel' AND table_name='vehicles';
UPDATE core.audit_policies SET safe_columns=safe_columns||ARRAY['assignee_since']
 WHERE schema_name='fuel' AND table_name='maintenance_plans';

CREATE TABLE fuel.notification_maintenance_observations(
 household_id uuid NOT NULL,plan_id uuid NOT NULL,
 module_generation bigint NOT NULL CHECK(module_generation BETWEEN 1 AND 9007199254740991),
 module_activated_at timestamptz NOT NULL,
 plan_version bigint NOT NULL CHECK(plan_version BETWEEN 1 AND 9007199254740991),
 vehicle_version bigint NOT NULL CHECK(vehicle_version BETWEEN 1 AND 9007199254740991),
 observed_at timestamptz NOT NULL,last_healthy_at timestamptz,
 state text NOT NULL CHECK(state IN('baseline','healthy','due','ineligible')),
 episode bigint NOT NULL DEFAULT 0 CHECK(episode BETWEEN 0 AND 9007199254740991),
 detected_at timestamptz,consent_cutoff timestamptz,
 PRIMARY KEY(household_id,plan_id),
 FOREIGN KEY(household_id,plan_id) REFERENCES fuel.maintenance_plans(household_id,id) ON DELETE CASCADE,
 CHECK(isfinite(observed_at) AND observed_at>='2000-01-01'::timestamptz AND observed_at<'10000-01-01'::timestamptz
  AND isfinite(module_activated_at) AND module_activated_at>='2000-01-01'::timestamptz AND module_activated_at<=observed_at),
 CHECK((state='healthy' AND last_healthy_at IS NOT NULL AND last_healthy_at=observed_at AND detected_at IS NULL AND consent_cutoff IS NULL)
  OR(state='due' AND episode>0 AND last_healthy_at IS NOT NULL AND detected_at IS NOT NULL AND consent_cutoff IS NOT NULL
   AND module_activated_at<=consent_cutoff AND consent_cutoff=last_healthy_at AND consent_cutoff<=detected_at AND detected_at<=observed_at)
  OR(state IN('baseline','ineligible') AND last_healthy_at IS NULL AND detected_at IS NULL AND consent_cutoff IS NULL))
);
CREATE INDEX notification_maintenance_observations_due ON fuel.notification_maintenance_observations
 (household_id,module_generation,detected_at,plan_id) WHERE state='due';
REVOKE ALL ON fuel.notification_maintenance_observations FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON fuel.notification_maintenance_observations TO household_app;

DO $canonical$
DECLARE item record;
BEGIN
 FOR item IN SELECT conrelid::regclass relation,conname,pg_get_constraintdef(oid) definition FROM pg_constraint
 WHERE contype='c' AND(conrelid='fuel.notification_maintenance_observations'::regclass
  OR(conrelid='fuel.vehicles'::regclass AND conname='vehicles_responsible_since')
  OR(conrelid='fuel.maintenance_plans'::regclass AND conname='maintenance_assignee_since')) ORDER BY conrelid,conname LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',item.relation,item.conname);
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s',item.relation,item.conname,item.definition);
 END LOOP;
END $canonical$;

-- Existing module_catalog pins all Fuel columns, constraints, indexes,
-- triggers and function bodies. Add the operational table's privilege/trigger
-- execution metadata that is not part of the older shared catalog.
DO $notification_ready$
DECLARE expected jsonb;
BEGIN
 SELECT jsonb_build_array(c.relowner::regrole::text,c.relacl::text,
  (SELECT jsonb_agg(jsonb_build_array(t.tgfoid::regprocedure::text,t.tgtype,t.tgenabled,t.tgisinternal) ORDER BY t.tgfoid::regprocedure::text,t.tgtype)
   FROM pg_trigger t WHERE t.tgrelid=c.oid)) INTO expected
 FROM pg_class c WHERE c.oid='fuel.notification_maintenance_observations'::regclass;
 EXECUTE format($definition$CREATE FUNCTION fuel.notification_ready() RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT EXISTS(SELECT 1 FROM pg_class c WHERE c.oid='fuel.notification_maintenance_observations'::regclass
 AND c.relkind='r' AND c.relpersistence='p' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity AND NOT c.relispartition
 AND NOT EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)
 AND jsonb_build_array(c.relowner::regrole::text,c.relacl::text,
  (SELECT jsonb_agg(jsonb_build_array(t.tgfoid::regprocedure::text,t.tgtype,t.tgenabled,t.tgisinternal) ORDER BY t.tgfoid::regprocedure::text,t.tgtype)
   FROM pg_trigger t WHERE t.tgrelid=c.oid))=%L::jsonb
 AND NOT EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped AND a.attacl IS NOT NULL))
 AND NOT EXISTS(SELECT 1 FROM core.audit_policies WHERE schema_name='fuel' AND table_name='notification_maintenance_observations')
 AND NOT EXISTS(SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
  WHERE p.oid IN('fuel.notification_assignment_clock()'::regprocedure,'fuel.notification_predecessor_catalog()'::regprocedure)
  AND(a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE'))
 $body$$definition$,expected);
END $notification_ready$;
REVOKE ALL ON FUNCTION fuel.notification_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION fuel.notification_ready() TO household_app;

DO $compatibility$
DECLARE old record;new_audit text;new_pantry text;new_catalog jsonb;needle text;
BEGIN
 SELECT * INTO STRICT old FROM fuel_notification_predecessor;
 needle='''capture_transitions'',''legacy_operation_fences''';
 IF (length(old.audit_ready)-length(replace(old.audit_ready,needle,'')))/length(needle)<>1 THEN
  RAISE EXCEPTION 'Fuel operational classification differs';END IF;
 new_audit=replace(old.audit_ready,needle,'''capture_transitions'',''legacy_operation_fences'',''notification_maintenance_observations''');
 EXECUTE new_audit;
 new_pantry=replace(old.pantry_ready,md5(old.audit_ready),md5(new_audit));
 EXECUTE new_pantry;
 new_catalog=pantry.notification_observations_catalog();
 IF new_catalog-'compatibility' IS DISTINCT FROM old.pantry_catalog-'compatibility' THEN
  RAISE EXCEPTION 'Fuel changed unrelated Pantry observation metadata';END IF;
 EXECUTE replace(old.pantry_notification_ready,quote_literal(old.pantry_catalog::text)||'::jsonb',quote_literal(new_catalog::text)||'::jsonb');
 EXECUTE replace(old.fuel_ready,quote_literal(old.fuel_catalog::text)||'::jsonb',quote_literal(fuel.module_catalog()::text)||'::jsonb');
 IF NOT core.audit_ready() OR NOT fuel.module_ready() OR NOT fuel.notification_ready()
  OR NOT pantry.prepared_capture_ready() OR NOT pantry.notification_observations_ready()
  OR NOT core.finance_ready() OR NOT core.planning_extensions_ready() OR NOT mealplan.expansion_ready()
  OR NOT core.notification_foundation_ready() OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
  RAISE EXCEPTION 'incomplete Fuel notification compatibility';END IF;
END $compatibility$;
COMMIT;
