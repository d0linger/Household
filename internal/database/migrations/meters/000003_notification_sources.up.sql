BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

CREATE TEMP TABLE meters_notification_before(snapshot jsonb NOT NULL) ON COMMIT DROP;
REVOKE ALL ON meters_notification_before FROM PUBLIC,household_app;
DO $predecessor$
DECLARE reviewed jsonb;snapshot jsonb;expected text;actual text;item text;catalog_hash text;catalog_owner text;catalog_acl text;
 old_audit text;new_audit text;new_pantry text;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=39 AND NOT dirty) FROM core.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=6 AND NOT dirty) FROM expenses.schema_migrations)
 OR NOT meters.expansion_ready() OR NOT core.audit_ready() OR NOT core.finance_ready()
 OR NOT core.notification_financial_ready() OR NOT expenses.notification_ready()
 OR NOT pantry.notification_observations_ready() OR NOT fuel.notification_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
  RAISE EXCEPTION 'Meter notifications require accepted Core39/Expenses6/Meters2';END IF;
 -- Reconstruct the released readiness implementation, rather than trusting an
 -- arbitrary live boolean function to approve a new catalog snapshot.
 IF NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='meters.expansion_catalog()'::regprocedure
  AND md5(replace(p.prosrc,E'\r\n',E'\n'))='2a58e0cc260410b75a5beaba55128a96'
  AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.provolatile='s'
  AND p.proconfig=ARRAY['search_path=pg_catalog'] AND NOT EXISTS(
   SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
   WHERE a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE')) THEN
  RAISE EXCEPTION 'Unreviewed Meters2 catalog implementation';END IF;
 SELECT encode(sha256(convert_to(pg_get_functiondef(p.oid),'UTF8')),'hex'),p.proowner::regrole::text,p.proacl::text
 INTO catalog_hash,catalog_owner,catalog_acl FROM pg_proc p WHERE p.oid='meters.expansion_catalog()'::regprocedure;
 expected=format('SELECT meters.expansion_catalog()=%L::jsonb AND (SELECT count(*) FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname=''meters'' AND t.relkind IN(''r'',''p'') AND t.relname<>''schema_migrations'')=20 AND EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid=''meters.expansion_catalog()''::regprocedure AND encode(sha256(convert_to(pg_get_functiondef(p.oid),''UTF8'')),''hex'')=%L AND p.proowner::regrole::text=%L AND p.proacl::text IS NOT DISTINCT FROM %L)',meters.expansion_catalog()::text,catalog_hash,catalog_owner,catalog_acl);
 IF NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='meters.expansion_ready()'::regprocedure
  AND p.prosrc=expected AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.provolatile='s'
  AND p.proconfig=ARRAY['search_path=pg_catalog']) THEN RAISE EXCEPTION 'Unreviewed Meters2 readiness implementation';END IF;
 reviewed=expenses.notification_predecessor_catalog();
 old_audit=reviewed->'functions'->'core.audit_ready()'->>'definition';
 new_audit=replace(old_audit,'AND NOT(n.nspname=''pantry'' AND c.relname IN(''lot_balances'',''notification_stock_observations''))',
  'AND NOT(n.nspname=''pantry'' AND c.relname IN(''lot_balances'',''notification_stock_observations''))'||E'\n AND NOT(n.nspname=''expenses'' AND c.relname IN(''notification_payment_reminders'',''notification_budget_observations''))');
 new_pantry=replace(reviewed->'functions'->'pantry.prepared_capture_ready()'->>'definition',md5(old_audit),md5(new_audit));
 IF old_audit IS NULL OR new_audit=old_audit OR new_pantry IS NULL
 OR pantry.notification_observations_catalog()-'compatibility' IS DISTINCT FROM (reviewed->'pantry_catalog')-'compatibility' THEN
  RAISE EXCEPTION 'Unreviewed Expenses6 audit composition';END IF;
 FOREACH item IN ARRAY ARRAY['core.audit_ready()','pantry.prepared_capture_ready()','pantry.notification_observations_ready()'] LOOP
  expected=CASE item WHEN 'core.audit_ready()' THEN new_audit WHEN 'pantry.prepared_capture_ready()' THEN new_pantry
   ELSE replace(reviewed->'functions'->item->>'definition',quote_literal((reviewed->'pantry_catalog')::text)||'::jsonb',
    quote_literal(pantry.notification_observations_catalog()::text)||'::jsonb') END;
  actual=NULL;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=reviewed->'functions'->item->>'owner'
   AND p.proacl::text IS NOT DISTINCT FROM reviewed->'functions'->item->>'acl';
  IF expected IS NULL OR actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'Unreviewed meter predecessor: %',item;END IF;
 END LOOP;
 SELECT jsonb_build_object('meters_catalog',meters.expansion_catalog(),'pantry_catalog',pantry.notification_observations_catalog(),
  'functions',jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
   'owner',p.proowner::regrole::text,'acl',p.proacl::text))) INTO snapshot FROM pg_proc p WHERE p.oid IN(
  'meters.expansion_ready()'::regprocedure,'meters.expansion_catalog()'::regprocedure,'core.audit_ready()'::regprocedure,
  'pantry.prepared_capture_ready()'::regprocedure,'pantry.notification_observations_ready()'::regprocedure);
 INSERT INTO meters_notification_before VALUES(snapshot);
 EXECUTE format('CREATE FUNCTION meters.notification_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION meters.notification_predecessor_catalog() FROM PUBLIC,household_app;

CREATE TABLE meters.notification_settings(
 id uuid PRIMARY KEY,household_id uuid NOT NULL,
 responsible_member_id uuid,responsible_since timestamptz,
 reading_enabled boolean NOT NULL DEFAULT false,anomaly_enabled boolean NOT NULL DEFAULT false,stale_enabled boolean NOT NULL DEFAULT false,
 stale_after_seconds integer NOT NULL DEFAULT 86400 CHECK(stale_after_seconds BETWEEN 300 AND 31622400),
 version bigint NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
 created_by uuid NOT NULL REFERENCES core.users(id),changed_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id),
 FOREIGN KEY(household_id,id) REFERENCES meters.registers(household_id,id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,responsible_member_id) REFERENCES core.memberships(household_id,id),
 CHECK((responsible_member_id IS NULL)=(responsible_since IS NULL)),
 CHECK(NOT(reading_enabled OR anomaly_enabled OR stale_enabled) OR responsible_member_id IS NOT NULL),
 CHECK(isfinite(created_at) AND created_at>='2000-01-01'::timestamptz AND isfinite(updated_at)
  AND created_at<=updated_at AND updated_at<'10000-01-01'::timestamptz
  AND(responsible_since IS NULL OR(isfinite(responsible_since) AND responsible_since>=created_at AND responsible_since<=updated_at)))
);
CREATE INDEX notification_settings_responsible ON meters.notification_settings(household_id,responsible_member_id,id)
 WHERE reading_enabled OR anomaly_enabled OR stale_enabled;

CREATE TABLE meters.notification_observations(
 household_id uuid NOT NULL,register_id uuid NOT NULL,kind text NOT NULL CHECK(kind IN('consumption','base_load','adapter')),
 module_generation bigint NOT NULL CHECK(module_generation BETWEEN 1 AND 9007199254740991),module_activated_at timestamptz NOT NULL,
 config_version bigint NOT NULL CHECK(config_version BETWEEN 1 AND 9007199254740991),
 register_version bigint NOT NULL CHECK(register_version BETWEEN 1 AND 9007199254740991),
 source_stamp text NOT NULL CHECK(source_stamp~'^[0-9a-f]{64}$'),
 observed_at timestamptz NOT NULL,last_healthy_at timestamptz,
 state text NOT NULL CHECK(state IN('unknown','healthy','baseline','alert','recovered')),
 episode bigint NOT NULL DEFAULT 0 CHECK(episode BETWEEN 0 AND 9007199254740991),
 detected_at timestamptz,consent_cutoff timestamptz,last_alert_at timestamptz,
 PRIMARY KEY(household_id,register_id,kind),
 FOREIGN KEY(household_id,register_id) REFERENCES meters.notification_settings(household_id,id) ON DELETE CASCADE,
 CHECK(isfinite(observed_at) AND observed_at>='2000-01-01'::timestamptz AND observed_at<'10000-01-01'::timestamptz
  AND isfinite(module_activated_at) AND module_activated_at>='2000-01-01'::timestamptz AND module_activated_at<=observed_at),
 CHECK(last_healthy_at IS NULL OR(isfinite(last_healthy_at) AND module_activated_at<=last_healthy_at AND last_healthy_at<=observed_at)),
 CHECK(detected_at IS NULL OR(isfinite(detected_at) AND module_activated_at<=detected_at AND detected_at<=observed_at)),
 CHECK(consent_cutoff IS NULL OR(isfinite(consent_cutoff) AND module_activated_at<=consent_cutoff AND consent_cutoff<=observed_at)),
 CHECK(last_alert_at IS NULL OR(isfinite(last_alert_at) AND last_alert_at>='2000-01-01'::timestamptz AND last_alert_at<=observed_at)),
 CHECK(state<>'healthy' OR last_healthy_at IS NOT NULL),
 CHECK(state NOT IN('alert','recovered') OR(episode>0 AND last_healthy_at IS NOT NULL AND detected_at IS NOT NULL
  AND consent_cutoff IS NOT NULL AND consent_cutoff<=detected_at))
);
CREATE INDEX notification_observations_active ON meters.notification_observations(household_id,module_generation,detected_at,register_id,kind)
 WHERE state IN('alert','recovered');
REVOKE ALL ON meters.notification_settings,meters.notification_observations FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON meters.notification_settings,meters.notification_observations TO household_app;

CREATE FUNCTION meters.notification_settings_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE context_value jsonb;actor uuid;now_at timestamptz=clock_timestamp();member_changed boolean;
BEGIN
 IF TG_TABLE_SCHEMA<>'meters' OR TG_TABLE_NAME<>'notification_settings' OR TG_OP NOT IN('INSERT','UPDATE','DELETE') THEN
  RAISE EXCEPTION 'invalid meter notification settings trigger' USING ERRCODE='42501';END IF;
 context_value=core.audit_context();actor=(context_value->>'actor')::uuid;
 IF session_user<>'household_owner' AND(context_value->>'kind'<>'user' OR actor IS NULL
  OR(context_value->>'household')::uuid IS DISTINCT FROM coalesce(NEW.household_id,OLD.household_id)
  OR NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
   WHERE m.household_id=coalesce(NEW.household_id,OLD.household_id) AND m.user_id=actor AND m.role IN('admin','editor')
   AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password)) THEN
  RAISE EXCEPTION 'current household writer required' USING ERRCODE='42501';END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;
 IF TG_OP='UPDATE' AND(NEW.household_id IS DISTINCT FROM OLD.household_id OR NEW.id IS DISTINCT FROM OLD.id
  OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at) THEN
  RAISE EXCEPTION 'immutable notification settings identity' USING ERRCODE='23514';END IF;
 member_changed=TG_OP='INSERT' OR NEW.responsible_member_id IS DISTINCT FROM OLD.responsible_member_id;
 IF NEW.responsible_member_id IS NOT NULL AND(member_changed OR NEW.reading_enabled OR NEW.anomaly_enabled OR NEW.stale_enabled) THEN
  PERFORM 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=NEW.household_id
   AND m.id=NEW.responsible_member_id AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password FOR SHARE OF m,u NOWAIT;
  IF NOT FOUND THEN RAISE EXCEPTION 'active household recipient required' USING ERRCODE='23514';END IF;
 END IF;
 IF TG_OP='INSERT' THEN NEW.version=1;NEW.created_at=now_at;NEW.created_by=coalesce(actor,NEW.created_by);
 ELSE NEW.version=OLD.version+1;END IF;
 NEW.changed_by=coalesce(actor,NEW.created_by);NEW.updated_at=now_at;
 IF member_changed THEN NEW.responsible_since=CASE WHEN NEW.responsible_member_id IS NULL THEN NULL ELSE now_at END;
 ELSE NEW.responsible_since=OLD.responsible_since;END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION meters.notification_settings_guard() FROM PUBLIC,household_app;
CREATE TRIGGER notification_settings_guard BEFORE INSERT OR UPDATE OR DELETE ON meters.notification_settings
 FOR EACH ROW EXECUTE FUNCTION meters.notification_settings_guard();

-- Anchor the content-free configuration audit to the existing register. Worker
-- observations are operational state and do not create an unbounded audit stream.
CREATE FUNCTION meters.notification_settings_audit() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE context_value jsonb;register_id uuid;household uuid;transition jsonb;
BEGIN
 IF TG_TABLE_SCHEMA<>'meters' OR TG_TABLE_NAME<>'notification_settings' OR TG_OP NOT IN('INSERT','UPDATE','DELETE') THEN
  RAISE EXCEPTION 'invalid meter notification settings audit trigger' USING ERRCODE='42501';END IF;
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 context_value=core.audit_context();register_id=coalesce(NEW.id,OLD.id);household=coalesce(NEW.household_id,OLD.household_id);
 transition=jsonb_build_object('notification_configuration_version',jsonb_build_object('before',CASE WHEN TG_OP='INSERT' THEN NULL ELSE OLD.version END,
  'after',CASE WHEN TG_OP='DELETE' THEN NULL ELSE NEW.version END));
 INSERT INTO core.audit_log(household_id,actor_user_id,module_id,resource_type,resource_id,action,outcome,request_id,details,actor_kind,db_role,entity_key,entity_label,changes,created_at)
 VALUES(household,(context_value->>'actor')::uuid,'meters','registers',register_id,'notification_settings_'||lower(TG_OP),'success',
  (context_value->>'request')::uuid,'{}',context_value->>'kind',session_user,jsonb_build_object('id',register_id),'meters.registers',transition,clock_timestamp());
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION meters.notification_settings_audit() FROM PUBLIC,household_app;
CREATE TRIGGER notification_settings_audit AFTER INSERT OR UPDATE OR DELETE ON meters.notification_settings
 FOR EACH ROW EXECUTE FUNCTION meters.notification_settings_audit();

DO $canonical$
DECLARE item record;
BEGIN
 FOR item IN SELECT conrelid::regclass relation,conname,pg_get_constraintdef(oid) definition FROM pg_constraint
 WHERE contype='c' AND conrelid IN('meters.notification_settings'::regclass,'meters.notification_observations'::regclass) ORDER BY conrelid,conname LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',item.relation,item.conname);
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s',item.relation,item.conname,item.definition);
 END LOOP;
END $canonical$;

CREATE FUNCTION meters.notification_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
 'tables',(SELECT jsonb_agg(jsonb_build_array(c.relname,c.relowner::regrole::text,c.relkind,c.relpersistence,c.relrowsecurity,c.relforcerowsecurity,c.relispartition,c.relacl::text,
  EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)) ORDER BY c.relname)
  FROM pg_class c WHERE c.oid IN('meters.notification_settings'::regclass,'meters.notification_observations'::regclass)),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(tgrelid::regclass::text,tgname,pg_get_triggerdef(oid),tgenabled,tgisinternal) ORDER BY tgrelid::regclass::text,tgname)
  FROM pg_trigger WHERE NOT tgisinternal AND(tgrelid IN('meters.notification_settings'::regclass,'meters.notification_observations'::regclass))),
 -- Internal FK trigger names contain OIDs that change on restore. Pin both
 -- referencing checks and referenced actions by their full stable semantics.
 'constraint_triggers',(SELECT jsonb_agg(identity ORDER BY identity) FROM(
  SELECT jsonb_build_array(k.conrelid::regclass::text,k.conname,k.contype,pg_get_constraintdef(k.oid),k.convalidated,
   t.tgrelid::regclass::text,t.tgconstrrelid::regclass::text,t.tgconstrindid::regclass::text,
   t.tgfoid::regprocedure::text,t.tgtype,t.tgenabled,t.tgisinternal,t.tgdeferrable,t.tginitdeferred,
   t.tgnargs,encode(t.tgargs,'hex'),t.tgattr::text,pg_get_expr(t.tgqual,t.tgrelid)) identity
  FROM pg_trigger t LEFT JOIN pg_constraint k ON k.oid=t.tgconstraint
  WHERE t.tgisinternal AND(t.tgrelid IN('meters.notification_settings'::regclass,'meters.notification_observations'::regclass) OR
   k.conrelid IN('meters.notification_settings'::regclass,'meters.notification_observations'::regclass))
 ) fk_triggers),
 'functions',(SELECT jsonb_agg(jsonb_build_array(p.oid::regprocedure::text,pg_get_functiondef(p.oid),p.proowner::regrole::text,p.proacl::text) ORDER BY p.oid::regprocedure::text)
  FROM pg_proc p WHERE p.oid IN('meters.notification_settings_guard()'::regprocedure,'meters.notification_settings_audit()'::regprocedure)),
 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY table_name) FROM core.audit_policies p
  WHERE schema_name='meters' AND table_name IN('notification_settings','notification_observations')))
$$;
REVOKE ALL ON FUNCTION meters.notification_catalog() FROM PUBLIC,household_app;
DO $ready$
DECLARE expected jsonb;catalog_hash text;
BEGIN
 expected=meters.notification_catalog();catalog_hash=md5(pg_get_functiondef('meters.notification_catalog()'::regprocedure));
 EXECUTE format($definition$CREATE FUNCTION meters.notification_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT meters.expansion_ready() AND meters.notification_catalog()=%L::jsonb
 AND md5(pg_get_functiondef('meters.notification_catalog()'::regprocedure))=%L
 AND NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid IN('meters.notification_catalog()'::regprocedure,'meters.notification_predecessor_catalog()'::regprocedure)
  AND(p.proowner<>'household_owner'::regrole OR NOT p.prosecdef OR p.proconfig IS DISTINCT FROM ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
  WHERE p.oid IN('meters.notification_catalog()'::regprocedure,'meters.notification_predecessor_catalog()'::regprocedure)
   AND(a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE'))
 $body$$definition$,expected,catalog_hash);
END $ready$;
REVOKE ALL ON FUNCTION meters.notification_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION meters.notification_ready() TO household_app;

DO $compatibility$
DECLARE old jsonb;new_catalog jsonb;key text;values_before jsonb;values_after jsonb;ready text;needle text;
 old_audit text;new_audit text;
BEGIN
 SELECT snapshot INTO STRICT old FROM meters_notification_before;
 new_catalog=meters.expansion_catalog();
 -- Every old relation/function remains byte-for-byte unchanged. The existing
 -- expansion catalog already covers columns/constraints/indexes and now also
 -- pins the new notification readiness function without a recursive snapshot.
 FOREACH key IN ARRAY ARRAY['relations','columns','constraints','indexes','triggers','functions'] LOOP
  SELECT coalesce(jsonb_agg(value ORDER BY ordinal),'[]') INTO values_after
  FROM jsonb_array_elements(new_catalog->key) WITH ORDINALITY x(value,ordinal)
  WHERE CASE WHEN key='functions' THEN value->>0 NOT IN('meters.notification_predecessor_catalog()',
   'meters.notification_settings_guard()','meters.notification_settings_audit()','meters.notification_catalog()','meters.notification_ready()')
   ELSE value->>0 NOT IN('notification_settings','notification_observations') END;
  values_before=coalesce(old->'meters_catalog'->key,'[]');
  IF values_after IS DISTINCT FROM values_before THEN RAISE EXCEPTION 'Unrelated Meters metadata changed: %',key;END IF;
 END LOOP;
 ready=old->'functions'->'meters.expansion_ready()'->>'definition';needle=quote_literal((old->'meters_catalog')::text)||'::jsonb';
 IF (length(ready)-length(replace(ready,needle,'')))/length(needle)<>1 OR position(')=20 AND EXISTS' in ready)=0 THEN
  RAISE EXCEPTION 'Unexpected Meters readiness snapshot';END IF;
 EXECUTE replace(replace(ready,needle,quote_literal(new_catalog::text)||'::jsonb'),')=20 AND EXISTS',')=22 AND EXISTS');
 old_audit=old->'functions'->'core.audit_ready()'->>'definition';
 needle='AND NOT(n.nspname=''expenses'' AND c.relname IN(''notification_payment_reminders'',''notification_budget_observations''))';
 IF (length(old_audit)-length(replace(old_audit,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'Unexpected meter audit census predecessor';END IF;
 new_audit=replace(old_audit,needle,needle||E'\n AND NOT(n.nspname=''meters'' AND c.relname IN(''notification_settings'',''notification_observations''))');
 EXECUTE new_audit;
 EXECUTE replace(old->'functions'->'pantry.prepared_capture_ready()'->>'definition',md5(old_audit),md5(new_audit));
 new_catalog=pantry.notification_observations_catalog();
 IF new_catalog-'compatibility' IS DISTINCT FROM(old->'pantry_catalog')-'compatibility' THEN RAISE EXCEPTION 'Unrelated Pantry metadata changed';END IF;
 EXECUTE replace(old->'functions'->'pantry.notification_observations_ready()'->>'definition',quote_literal((old->'pantry_catalog')::text)||'::jsonb',quote_literal(new_catalog::text)||'::jsonb');
 IF NOT meters.expansion_ready() OR NOT meters.notification_ready() OR NOT core.audit_ready()
 OR NOT pantry.prepared_capture_ready() OR NOT pantry.notification_observations_ready()
 OR NOT expenses.finance_ready() OR NOT expenses.notification_ready() OR NOT core.finance_ready()
 OR NOT core.notification_financial_ready() OR NOT fuel.notification_ready() OR NOT fuel.module_ready()
 OR NOT chores.expansion_ready() OR NOT documents.expansion_ready() OR NOT mealplan.expansion_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN RAISE EXCEPTION 'Incomplete meter notification compatibility';END IF;
END $compatibility$;
COMMIT;
