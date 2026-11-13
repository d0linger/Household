BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

-- Snapshot the entire module, including tables outside the older extension
-- catalog. The catalog excludes its own readiness/snapshot to avoid recursion.
CREATE FUNCTION mealplan.notification_catalog() RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
 'relations',(SELECT jsonb_agg(jsonb_build_array(c.relname,c.relkind::text,c.relpersistence::text,c.relowner::regrole::text,
  c.relacl::text,c.relrowsecurity,c.relforcerowsecurity,c.relispartition,
  EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)) ORDER BY c.relname)
  FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='mealplan' AND c.relkind IN('r','p') AND c.relname<>'schema_migrations'),
 'columns',(SELECT jsonb_agg(jsonb_build_array(c.relname,a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attidentity,a.attgenerated,
  a.attacl::text,a.attcollation::regcollation::text,coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY c.relname,a.attnum)
  FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
  LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
  WHERE n.nspname='mealplan' AND c.relkind IN('r','p') AND c.relname<>'schema_migrations'),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(c.relname,k.conname,pg_get_constraintdef(k.oid),k.convalidated,k.conislocal,k.coninhcount,k.connoinherit)
  ORDER BY c.relname,k.conname) FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname='mealplan' AND c.relname<>'schema_migrations'),
 'indexes',(SELECT jsonb_agg(jsonb_build_array(c.relname,pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready,i.indislive)
  ORDER BY c.relname,pg_get_indexdef(i.indexrelid)) FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname='mealplan' AND c.relname<>'schema_migrations'),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(c.relname,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled::text,t.tgdeferrable,t.tginitdeferred)
  ORDER BY c.relname,t.tgname) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname='mealplan' AND NOT t.tgisinternal),
 'functions',(SELECT jsonb_agg(jsonb_build_array(p.oid::regprocedure::text,encode(sha256(convert_to(pg_get_functiondef(p.oid),'UTF8')),'hex'),p.proowner::regrole::text,p.proacl::text)
  ORDER BY p.oid::regprocedure::text) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname='mealplan' AND p.oid<>'mealplan.notification_catalog()'::regprocedure
   AND p.oid IS DISTINCT FROM to_regprocedure('mealplan.notification_ready()')
   AND p.oid IS DISTINCT FROM to_regprocedure('mealplan.notification_predecessor_catalog()')),
 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY table_name) FROM core.audit_policies p WHERE schema_name='mealplan'))
$$;
REVOKE ALL ON FUNCTION mealplan.notification_catalog() FROM PUBLIC,household_app;
CREATE TEMP TABLE mealplan_notification_before(snapshot jsonb NOT NULL) ON COMMIT DROP;
REVOKE ALL ON mealplan_notification_before FROM PUBLIC,household_app;
DO $predecessor$
DECLARE reviewed jsonb;snapshot jsonb;expected text;actual text;item text;catalog_hash text;
 old_audit text;new_audit text;new_pantry text;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=39 AND NOT dirty) FROM core.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=6 AND NOT dirty) FROM expenses.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=3 AND NOT dirty) FROM meters.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=3 AND NOT dirty) FROM documents.schema_migrations)
 OR NOT documents.notification_ready() OR NOT meters.notification_ready() OR NOT core.audit_ready() OR NOT core.finance_ready()
 OR NOT core.notification_financial_ready() OR NOT expenses.notification_ready() OR NOT core.planning_extensions_ready()
 OR NOT pantry.notification_observations_ready() OR NOT fuel.notification_ready() OR NOT mealplan.expansion_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
  RAISE EXCEPTION 'Meal notifications require accepted Core39/Expenses6/Meters3/Documents3/Mealplan12';END IF;
 -- The source hash and exact readiness reconstruction are generated from the
 -- released Mealplan8 implementation, unchanged through Mealplan12.
 IF NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='mealplan.extension_catalog()'::regprocedure
  AND md5(replace(p.prosrc,E'\r\n',E'\n'))='49d436fc7ff3a15d7d485574adae7190'
  AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.provolatile='s'
  AND p.proconfig=ARRAY['search_path=pg_catalog'] AND NOT EXISTS(
   SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
   WHERE a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE')) THEN
  RAISE EXCEPTION 'Unreviewed Mealplan12 extension catalog';END IF;
 catalog_hash=md5(pg_get_functiondef('mealplan.extension_catalog()'::regprocedure));
 expected=format($expected$
 SELECT mealplan.extension_catalog()=%L::jsonb
 AND md5(pg_get_functiondef('mealplan.extension_catalog()'::regprocedure))=%L
 AND NOT has_table_privilege('household_app','mealplan.calendar_tombstones','INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','mealplan.calendar_feeds','DELETE,TRUNCATE,TRIGGER')
 AND NOT has_function_privilege('household_app','mealplan.capture_calendar_tombstone()','EXECUTE')
 AND NOT has_function_privilege('household_app','mealplan.extension_catalog()','EXECUTE')
 $expected$,mealplan.extension_catalog()::text,catalog_hash);
 IF NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='mealplan.expansion_ready()'::regprocedure
  AND p.prosrc=expected AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.provolatile='s'
  AND p.proconfig=ARRAY['search_path=pg_catalog'] AND EXISTS(
   SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
   WHERE a.grantee='household_app'::regrole AND a.privilege_type='EXECUTE')
  AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
   WHERE a.grantee NOT IN(p.proowner,'household_app'::regrole) OR a.privilege_type<>'EXECUTE')) THEN
  RAISE EXCEPTION 'Unreviewed Mealplan12 readiness';END IF;
 reviewed=documents.notification_predecessor_catalog();
 old_audit=reviewed->'functions'->'core.audit_ready()'->>'definition';
 new_audit=replace(old_audit,'AND NOT(n.nspname=''meters'' AND c.relname IN(''notification_settings'',''notification_observations''))',
  'AND NOT(n.nspname=''meters'' AND c.relname IN(''notification_settings'',''notification_observations''))'||E'\n AND NOT(n.nspname=''documents'' AND c.relname IN(''notification_settings'',''notification_processing_events'',''notification_import_events''))');
 new_pantry=replace(reviewed->'functions'->'pantry.prepared_capture_ready()'->>'definition',md5(old_audit),md5(new_audit));
 IF old_audit IS NULL OR new_audit=old_audit OR new_pantry IS NULL
 OR pantry.notification_observations_catalog()-'compatibility' IS DISTINCT FROM(reviewed->'pantry_catalog')-'compatibility' THEN
  RAISE EXCEPTION 'Unreviewed Documents3 audit composition';END IF;
 FOREACH item IN ARRAY ARRAY['core.audit_ready()','pantry.prepared_capture_ready()','pantry.notification_observations_ready()'] LOOP
  expected=CASE item WHEN 'core.audit_ready()' THEN new_audit WHEN 'pantry.prepared_capture_ready()' THEN new_pantry
   ELSE replace(reviewed->'functions'->item->>'definition',quote_literal((reviewed->'pantry_catalog')::text)||'::jsonb',
    quote_literal(pantry.notification_observations_catalog()::text)||'::jsonb') END;
  actual=NULL;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=reviewed->'functions'->item->>'owner'
   AND p.proacl::text IS NOT DISTINCT FROM reviewed->'functions'->item->>'acl';
  IF expected IS NULL OR actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'Unreviewed meal notification predecessor: %',item;END IF;
 END LOOP;
 SELECT jsonb_build_object('mealplan_catalog',mealplan.notification_catalog(),'extension_catalog',mealplan.extension_catalog(),
  'pantry_catalog',pantry.notification_observations_catalog(),'functions',jsonb_object_agg(p.oid::regprocedure::text,
   jsonb_build_object('definition',pg_get_functiondef(p.oid),'owner',p.proowner::regrole::text,'acl',p.proacl::text)))
 INTO snapshot FROM pg_proc p WHERE p.oid IN('core.audit_ready()'::regprocedure,
  'pantry.prepared_capture_ready()'::regprocedure,'pantry.notification_observations_ready()'::regprocedure);
 INSERT INTO mealplan_notification_before VALUES(snapshot);
 EXECUTE format('CREATE FUNCTION mealplan.notification_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION mealplan.notification_predecessor_catalog() FROM PUBLIC,household_app;

ALTER TABLE mealplan.plans ADD COLUMN notification_active_since timestamptz NOT NULL DEFAULT clock_timestamp();
ALTER TABLE mealplan.plans ADD CONSTRAINT mealplan_notification_active_clock CHECK(isfinite(notification_active_since)
 AND notification_active_since>='2000-01-01'::timestamptz AND notification_active_since<'10000-01-01'::timestamptz);
CREATE TABLE mealplan.notification_settings(
 id uuid PRIMARY KEY,household_id uuid NOT NULL,responsible_member_id uuid,enabled boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
 meal_version bigint NOT NULL CHECK(meal_version BETWEEN 1 AND 9007199254740991),
 module_generation bigint NOT NULL CHECK(module_generation BETWEEN 1 AND 9007199254740991),
 timezone text NOT NULL CHECK(length(timezone) BETWEEN 1 AND 100),
 steps jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(steps)='array' AND jsonb_array_length(steps)<=8 AND octet_length(steps::text)<=16384),
 created_by uuid NOT NULL REFERENCES core.users(id),changed_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id),FOREIGN KEY(household_id,id) REFERENCES mealplan.meals(household_id,id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,responsible_member_id) REFERENCES core.memberships(household_id,id),
 CHECK(NOT enabled OR(responsible_member_id IS NOT NULL AND jsonb_array_length(steps)>0)),
 CHECK(isfinite(created_at) AND created_at>='2000-01-01'::timestamptz AND isfinite(updated_at)
  AND created_at<=updated_at AND updated_at<'10000-01-01'::timestamptz)
);
CREATE INDEX notification_settings_responsible ON mealplan.notification_settings(household_id,responsible_member_id,id) WHERE enabled;
REVOKE ALL ON mealplan.notification_settings FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON mealplan.notification_settings TO household_app;

CREATE FUNCTION mealplan.notification_plan_clock() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF TG_TABLE_SCHEMA<>'mealplan' OR TG_TABLE_NAME<>'plans' OR TG_OP NOT IN('INSERT','UPDATE') THEN
  RAISE EXCEPTION 'invalid meal notification activity trigger' USING ERRCODE='42501';END IF;
 IF TG_OP='INSERT' OR NEW.is_template IS DISTINCT FROM OLD.is_template THEN NEW.notification_active_since=clock_timestamp();
 ELSE NEW.notification_active_since=OLD.notification_active_since;END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.notification_plan_clock() FROM PUBLIC,household_app;
CREATE TRIGGER notification_plan_clock BEFORE INSERT OR UPDATE ON mealplan.plans FOR EACH ROW EXECUTE FUNCTION mealplan.notification_plan_clock();

CREATE FUNCTION mealplan.notification_settings_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE context_value jsonb;actor uuid;now_at timestamptz=clock_timestamp();step jsonb;step_id uuid;
 ids uuid[]='{}';start_count integer=0;local_at timestamp;scheduled timestamptz;
BEGIN
 IF TG_TABLE_SCHEMA<>'mealplan' OR TG_TABLE_NAME<>'notification_settings' OR TG_OP NOT IN('INSERT','UPDATE','DELETE') THEN
  RAISE EXCEPTION 'invalid meal notification settings trigger' USING ERRCODE='42501';END IF;
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
  RAISE EXCEPTION 'immutable meal notification identity' USING ERRCODE='23514';END IF;
 PERFORM 1 FROM core.households h JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='mealplan'
  WHERE h.id=NEW.household_id AND h.timezone=NEW.timezone AND ms.state='enabled' AND ms.data_generation=NEW.module_generation
  FOR SHARE OF h,ms NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'current meal module and household timezone required' USING ERRCODE='23514';END IF;
 PERFORM 1 FROM mealplan.meals m JOIN mealplan.plans p ON p.household_id=m.household_id AND p.id=m.plan_id
  WHERE m.household_id=NEW.household_id AND m.id=NEW.id AND m.version=NEW.meal_version
  AND m.status IN('planned','purchased') AND NOT p.is_template FOR SHARE OF p,m NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'reviewed current active meal required' USING ERRCODE='23514';END IF;
 IF NEW.responsible_member_id IS NOT NULL THEN
  PERFORM 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=NEW.household_id
   AND m.id=NEW.responsible_member_id AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password FOR SHARE OF m,u NOWAIT;
  IF NOT FOUND THEN RAISE EXCEPTION 'active household recipient required' USING ERRCODE='23514';END IF;
 END IF;
 IF NEW.steps IS NULL OR jsonb_typeof(NEW.steps)<>'array' OR jsonb_array_length(NEW.steps)>8 OR octet_length(NEW.steps::text)>16384
 OR NOT EXISTS(SELECT 1 FROM pg_timezone_names WHERE name=NEW.timezone) THEN
  RAISE EXCEPTION 'invalid meal notification schedule' USING ERRCODE='23514';END IF;
 FOR step IN SELECT value FROM jsonb_array_elements(NEW.steps) LOOP
  IF jsonb_typeof(step)<>'object' OR NOT(step ?& ARRAY['id','kind','label','local_date','local_time','scheduled_at'])
  OR step-ARRAY['id','kind','label','local_date','local_time','scheduled_at']<>'{}'::jsonb
  OR EXISTS(SELECT 1 FROM jsonb_each(step) x WHERE jsonb_typeof(x.value)<>'string')
  OR step->>'id' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  OR step->>'kind' NOT IN('start','step') OR length(btrim(step->>'label')) NOT BETWEEN 1 AND 120
  OR length(step->>'label')>120 OR step->>'label' ~ '[[:cntrl:]]'
  OR step->>'local_date' !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'
  OR step->>'local_time' !~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'
  OR step->>'scheduled_at' !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T([01][0-9]|2[0-3]):[0-5][0-9]:00Z$' THEN
   RAISE EXCEPTION 'invalid meal notification step' USING ERRCODE='23514';END IF;
  step_id=(step->>'id')::uuid;
  IF step_id=ANY(ids) OR step_id='00000000-0000-0000-0000-000000000000'::uuid THEN
   RAISE EXCEPTION 'duplicate or empty meal notification step identity' USING ERRCODE='23514';END IF;
  ids=array_append(ids,step_id);
  IF step->>'kind'='start' THEN start_count=start_count+1;END IF;
  local_at=((step->>'local_date')||' '||(step->>'local_time'))::timestamp;
  scheduled=(step->>'scheduled_at')::timestamptz;
  IF local_at<'2000-01-01'::timestamp OR local_at>='10000-01-01'::timestamp
   OR NOT isfinite(scheduled) OR scheduled<'2000-01-01'::timestamptz OR scheduled>='10000-01-01'::timestamptz
   OR scheduled AT TIME ZONE NEW.timezone IS DISTINCT FROM local_at
   OR to_char(local_at,'YYYY-MM-DD')<>step->>'local_date' OR(NEW.enabled AND scheduled<=now_at) THEN
   RAISE EXCEPTION 'invalid or elapsed local meal notification time' USING ERRCODE='23514';END IF;
 END LOOP;
 IF start_count>1 THEN RAISE EXCEPTION 'only one meal start notification allowed' USING ERRCODE='23514';END IF;
 IF TG_OP='INSERT' THEN NEW.version=1;NEW.created_at=now_at;NEW.created_by=coalesce(actor,NEW.created_by);
 ELSE NEW.version=OLD.version+1;END IF;
 NEW.changed_by=coalesce(actor,NEW.created_by);NEW.updated_at=now_at;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.notification_settings_guard() FROM PUBLIC,household_app;
CREATE TRIGGER notification_settings_guard BEFORE INSERT OR UPDATE OR DELETE ON mealplan.notification_settings
 FOR EACH ROW EXECUTE FUNCTION mealplan.notification_settings_guard();

CREATE FUNCTION mealplan.notification_settings_audit() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE context_value jsonb;meal_id uuid;household uuid;transition jsonb;
BEGIN
 IF TG_TABLE_SCHEMA<>'mealplan' OR TG_TABLE_NAME<>'notification_settings' OR TG_OP NOT IN('INSERT','UPDATE','DELETE') THEN
  RAISE EXCEPTION 'invalid meal notification settings audit trigger' USING ERRCODE='42501';END IF;
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 context_value=core.audit_context();meal_id=coalesce(NEW.id,OLD.id);household=coalesce(NEW.household_id,OLD.household_id);
 transition=jsonb_build_object('notification_configuration_version',jsonb_build_object('before',CASE WHEN TG_OP='INSERT' THEN NULL ELSE OLD.version END,
  'after',CASE WHEN TG_OP='DELETE' THEN NULL ELSE NEW.version END));
 INSERT INTO core.audit_log(household_id,actor_user_id,module_id,resource_type,resource_id,action,outcome,request_id,details,actor_kind,db_role,entity_key,entity_label,changes,created_at)
 VALUES(household,(context_value->>'actor')::uuid,'mealplan','meals',meal_id,'notification_settings_'||lower(TG_OP),'success',
  (context_value->>'request')::uuid,'{}',context_value->>'kind',session_user,jsonb_build_object('id',meal_id),'mealplan.meals',transition,clock_timestamp());
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION mealplan.notification_settings_audit() FROM PUBLIC,household_app;
CREATE TRIGGER notification_settings_audit AFTER INSERT OR UPDATE OR DELETE ON mealplan.notification_settings
 FOR EACH ROW EXECUTE FUNCTION mealplan.notification_settings_audit();

DO $canonical$
DECLARE item record;
BEGIN
 FOR item IN SELECT conrelid::regclass relation,conname,pg_get_constraintdef(oid) definition FROM pg_constraint
  WHERE contype='c' AND(conrelid='mealplan.notification_settings'::regclass
   OR(conrelid='mealplan.plans'::regclass AND conname='mealplan_notification_active_clock')) ORDER BY conname LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',item.relation,item.conname);
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s',item.relation,item.conname,item.definition);
 END LOOP;
END $canonical$;

DO $compatibility$
DECLARE old jsonb;new_catalog jsonb;key text;values_before jsonb;values_after jsonb;needle text;old_audit text;new_audit text;
BEGIN
 SELECT snapshot INTO STRICT old FROM mealplan_notification_before;
 new_catalog=mealplan.notification_catalog();
 FOREACH key IN ARRAY ARRAY['relations','columns','constraints','indexes','triggers','functions','policies'] LOOP
  SELECT coalesce(jsonb_agg(value ORDER BY ordinal),'[]') INTO values_after
  FROM jsonb_array_elements(new_catalog->key) WITH ORDINALITY x(value,ordinal)
  WHERE CASE WHEN key='functions' THEN value->>0 NOT IN('mealplan.notification_plan_clock()',
   'mealplan.notification_settings_guard()','mealplan.notification_settings_audit()')
   WHEN key='policies' THEN true ELSE value->>0<>'notification_settings' END
   AND NOT(key='columns' AND value->>0='plans' AND value->>1='notification_active_since')
   AND NOT(key='constraints' AND value->>0='plans' AND value->>1='mealplan_notification_active_clock')
   AND NOT(key='constraints' AND EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=c.conkey[1]
    WHERE c.contype='n' AND c.convalidated AND c.conkey=ARRAY[a.attnum] AND c.conrelid='mealplan.plans'::regclass
     AND a.attname='notification_active_since' AND value->>0='plans' AND value->>1=c.conname
     AND value->>2=pg_get_constraintdef(c.oid) AND(value->>3)::boolean=c.convalidated))
   AND NOT(key='triggers' AND value->>0='plans' AND value->>1='notification_plan_clock');
  values_before=coalesce(old->'mealplan_catalog'->key,'[]');
  IF values_after IS DISTINCT FROM values_before THEN RAISE EXCEPTION 'Unrelated Mealplan metadata changed: %',key;END IF;
 END LOOP;
 IF mealplan.extension_catalog() IS DISTINCT FROM old->'extension_catalog' THEN RAISE EXCEPTION 'Mealplan extension metadata changed';END IF;
 old_audit=old->'functions'->'core.audit_ready()'->>'definition';
 needle='AND NOT(n.nspname=''documents'' AND c.relname IN(''notification_settings'',''notification_processing_events'',''notification_import_events''))';
 IF (length(old_audit)-length(replace(old_audit,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'Unexpected meal audit census predecessor';END IF;
 new_audit=replace(old_audit,needle,needle||E'\n AND NOT(n.nspname=''mealplan'' AND c.relname=''notification_settings'')');
 EXECUTE new_audit;
 EXECUTE replace(old->'functions'->'pantry.prepared_capture_ready()'->>'definition',md5(old_audit),md5(new_audit));
 new_catalog=pantry.notification_observations_catalog();
 IF new_catalog-'compatibility' IS DISTINCT FROM(old->'pantry_catalog')-'compatibility' THEN RAISE EXCEPTION 'Unrelated Pantry metadata changed';END IF;
 EXECUTE replace(old->'functions'->'pantry.notification_observations_ready()'->>'definition',
  quote_literal((old->'pantry_catalog')::text)||'::jsonb',quote_literal(new_catalog::text)||'::jsonb');
END $compatibility$;

DO $ready$
DECLARE expected jsonb;catalog_hash text;predecessor_hash text;
BEGIN
 expected=mealplan.notification_catalog();catalog_hash=md5(pg_get_functiondef('mealplan.notification_catalog()'::regprocedure));
 predecessor_hash=md5(pg_get_functiondef('mealplan.notification_predecessor_catalog()'::regprocedure));
 EXECUTE format($definition$CREATE FUNCTION mealplan.notification_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT mealplan.expansion_ready() AND mealplan.notification_catalog()=%L::jsonb
 AND md5(pg_get_functiondef('mealplan.notification_catalog()'::regprocedure))=%L
 AND md5(pg_get_functiondef('mealplan.notification_predecessor_catalog()'::regprocedure))=%L
 AND NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid IN('mealplan.notification_catalog()'::regprocedure,'mealplan.notification_predecessor_catalog()'::regprocedure)
  AND(p.proowner<>'household_owner'::regrole OR NOT p.prosecdef OR p.proconfig IS DISTINCT FROM ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
  WHERE p.oid IN('mealplan.notification_catalog()'::regprocedure,'mealplan.notification_predecessor_catalog()'::regprocedure)
   AND(a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE'))
 $body$$definition$,expected,catalog_hash,predecessor_hash);
END $ready$;
REVOKE ALL ON FUNCTION mealplan.notification_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION mealplan.notification_ready() TO household_app;
DO $complete$
BEGIN
 IF NOT mealplan.notification_ready() OR NOT core.planning_extensions_ready() OR NOT documents.expansion_ready() OR NOT documents.notification_ready()
 OR NOT meters.notification_ready() OR NOT core.audit_ready() OR NOT pantry.prepared_capture_ready() OR NOT pantry.notification_observations_ready()
 OR NOT expenses.finance_ready() OR NOT expenses.notification_ready() OR NOT core.finance_ready() OR NOT core.notification_financial_ready()
 OR NOT fuel.notification_ready() OR NOT fuel.module_ready() OR NOT chores.expansion_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN RAISE EXCEPTION 'Incomplete meal notification compatibility';END IF;
END $complete$;
COMMIT;
