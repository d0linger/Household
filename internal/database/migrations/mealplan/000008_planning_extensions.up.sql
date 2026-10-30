BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

CREATE TABLE mealplan.leftover_sources(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),
 plan_id uuid NOT NULL,source_meal_id uuid NOT NULL,source_component_id uuid NOT NULL,
 expected_servings numeric(18,6) NOT NULL CHECK(expected_servings>0),
 reviewed_meal_version bigint NOT NULL CHECK(reviewed_meal_version>0),
 reviewed_component_version bigint NOT NULL CHECK(reviewed_component_version>0),
 source_hash bytea NOT NULL CHECK(octet_length(source_hash)=32),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),created_by uuid NOT NULL REFERENCES core.users(id),
 changed_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id,plan_id),UNIQUE(household_id,source_component_id),
 FOREIGN KEY(household_id,plan_id) REFERENCES mealplan.plans(household_id,id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,source_meal_id) REFERENCES mealplan.meals(household_id,id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,source_component_id) REFERENCES mealplan.components(household_id,id) ON DELETE CASCADE
);
CREATE TABLE mealplan.leftover_allocations(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),
 plan_id uuid NOT NULL,source_id uuid NOT NULL,target_meal_id uuid NOT NULL,
 servings numeric(18,6) NOT NULL CHECK(servings>0),reviewed_source_version bigint NOT NULL CHECK(reviewed_source_version>0),
 reviewed_target_version bigint NOT NULL CHECK(reviewed_target_version>0),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),created_by uuid NOT NULL REFERENCES core.users(id),
 changed_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),UNIQUE(household_id,target_meal_id),
 FOREIGN KEY(household_id,plan_id) REFERENCES mealplan.plans(household_id,id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,source_id,plan_id) REFERENCES mealplan.leftover_sources(household_id,id,plan_id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,target_meal_id) REFERENCES mealplan.meals(household_id,id) ON DELETE CASCADE
);
CREATE INDEX leftover_source_plan ON mealplan.leftover_sources(household_id,plan_id,source_meal_id);
CREATE INDEX leftover_allocation_source ON mealplan.leftover_allocations(household_id,source_id,id);

CREATE FUNCTION mealplan.slot_order(slot text) RETURNS integer
LANGUAGE sql IMMUTABLE SET search_path=pg_catalog AS $$
 SELECT CASE slot WHEN 'breakfast' THEN 0 WHEN 'lunch' THEN 1 WHEN 'dinner' THEN 2 WHEN 'snack' THEN 3 ELSE -1 END
$$;
REVOKE ALL ON FUNCTION mealplan.slot_order(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION mealplan.slot_order(text) TO household_app;

CREATE FUNCTION mealplan.guard_leftover_source() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE m mealplan.meals%ROWTYPE;c mealplan.components%ROWTYPE;used numeric;
BEGIN
 IF TG_OP='UPDATE' AND (NEW.id,NEW.household_id,NEW.plan_id,NEW.source_meal_id,NEW.source_component_id,NEW.created_by,NEW.created_at)
 IS DISTINCT FROM(OLD.id,OLD.household_id,OLD.plan_id,OLD.source_meal_id,OLD.source_component_id,OLD.created_by,OLD.created_at) THEN
  RAISE EXCEPTION 'planned surplus identity is immutable' USING ERRCODE='23514';
 END IF;
 PERFORM id FROM mealplan.plans WHERE household_id=NEW.household_id AND id=NEW.plan_id FOR UPDATE NOWAIT;
 SELECT * INTO m FROM mealplan.meals WHERE household_id=NEW.household_id AND id=NEW.source_meal_id AND plan_id=NEW.plan_id FOR UPDATE NOWAIT;
 SELECT * INTO c FROM mealplan.components WHERE household_id=NEW.household_id AND id=NEW.source_component_id AND meal_id=NEW.source_meal_id FOR UPDATE NOWAIT;
 IF m.id IS NULL OR c.id IS NULL OR m.kind<>'recipe' OR m.status='cancelled' OR m.servings_mode<>'manual'
 OR NEW.expected_servings>c.servings OR NEW.reviewed_meal_version<>m.version OR NEW.reviewed_component_version<>c.version
 OR NEW.source_hash IS DISTINCT FROM sha256(convert_to(c.snapshot::text,'UTF8')) THEN
  RAISE EXCEPTION 'review current manual production and expected surplus' USING ERRCODE='23514';
 END IF;
 SELECT coalesce(sum(servings),0) INTO used FROM mealplan.leftover_allocations WHERE household_id=NEW.household_id AND source_id=NEW.id;
 IF used>NEW.expected_servings THEN RAISE EXCEPTION 'existing allocations exceed expected surplus; reconcile first' USING ERRCODE='23514';END IF;
 IF TG_OP='UPDATE' THEN NEW.version:=OLD.version+1;END IF;
 NEW.updated_at:=clock_timestamp();RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.guard_leftover_source() FROM PUBLIC,household_app;
CREATE TRIGGER leftover_source_change BEFORE INSERT OR UPDATE ON mealplan.leftover_sources FOR EACH ROW EXECUTE FUNCTION mealplan.guard_leftover_source();

CREATE FUNCTION mealplan.guard_leftover_allocation() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE s mealplan.leftover_sources%ROWTYPE;m mealplan.meals%ROWTYPE;t mealplan.meals%ROWTYPE;c mealplan.components%ROWTYPE;used numeric;
BEGIN
 IF TG_OP='UPDATE' AND (NEW.id,NEW.household_id,NEW.plan_id,NEW.target_meal_id,NEW.created_by,NEW.created_at)
 IS DISTINCT FROM(OLD.id,OLD.household_id,OLD.plan_id,OLD.target_meal_id,OLD.created_by,OLD.created_at) THEN
  RAISE EXCEPTION 'planned leftover destination identity is immutable' USING ERRCODE='23514';
 END IF;
 PERFORM id FROM mealplan.plans WHERE household_id=NEW.household_id AND id=NEW.plan_id FOR UPDATE NOWAIT;
 SELECT * INTO s FROM mealplan.leftover_sources WHERE household_id=NEW.household_id AND id=NEW.source_id AND plan_id=NEW.plan_id FOR UPDATE NOWAIT;
 SELECT * INTO m FROM mealplan.meals WHERE household_id=s.household_id AND id=s.source_meal_id FOR UPDATE NOWAIT;
 SELECT * INTO c FROM mealplan.components WHERE household_id=s.household_id AND id=s.source_component_id FOR UPDATE NOWAIT;
 SELECT * INTO t FROM mealplan.meals WHERE household_id=NEW.household_id AND id=NEW.target_meal_id AND plan_id=NEW.plan_id FOR UPDATE NOWAIT;
 IF s.id IS NULL OR m.id IS NULL OR c.id IS NULL OR t.id IS NULL OR m.kind<>'recipe' OR m.status='cancelled'
 OR m.servings_mode<>'manual' OR s.reviewed_meal_version<>m.version OR s.reviewed_component_version<>c.version
 OR s.source_hash IS DISTINCT FROM sha256(convert_to(c.snapshot::text,'UTF8')) OR s.expected_servings>c.servings
 OR NEW.reviewed_source_version<>s.version OR t.kind<>'leftovers' OR t.status='cancelled' OR NEW.reviewed_target_version<>t.version OR NEW.servings>t.servings
 OR (m.planned_on,mealplan.slot_order(m.meal_slot))>=(t.planned_on,mealplan.slot_order(t.meal_slot)) THEN
  RAISE EXCEPTION 'review current source and later leftover destination' USING ERRCODE='23514';
 END IF;
 -- Stale allocations deliberately remain in this aggregate until explicit removal.
 SELECT coalesce(sum(servings),0) INTO used FROM mealplan.leftover_allocations WHERE household_id=NEW.household_id AND source_id=NEW.source_id AND id<>NEW.id;
 IF used+NEW.servings>s.expected_servings THEN RAISE EXCEPTION 'planned surplus is already allocated' USING ERRCODE='23514';END IF;
 IF TG_OP='UPDATE' THEN NEW.version:=OLD.version+1;END IF;
 NEW.updated_at:=clock_timestamp();RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.guard_leftover_allocation() FROM PUBLIC,household_app;
CREATE TRIGGER leftover_allocation_change BEFORE INSERT OR UPDATE ON mealplan.leftover_allocations FOR EACH ROW EXECUTE FUNCTION mealplan.guard_leftover_allocation();

CREATE FUNCTION mealplan.check_leftover_chronology() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM mealplan.leftover_allocations a JOIN mealplan.leftover_sources s ON s.household_id=a.household_id AND s.id=a.source_id
 JOIN mealplan.meals m ON m.household_id=s.household_id AND m.id=s.source_meal_id
 JOIN mealplan.meals t ON t.household_id=a.household_id AND t.id=a.target_meal_id
 WHERE a.household_id=NEW.household_id AND (m.id=NEW.id OR t.id=NEW.id)
 AND(m.kind<>'recipe' OR t.kind<>'leftovers' OR (m.planned_on,mealplan.slot_order(m.meal_slot))>=(t.planned_on,mealplan.slot_order(t.meal_slot)))) THEN
  RAISE EXCEPTION 'unlink planned leftovers before changing their kind or chronology' USING ERRCODE='23514',CONSTRAINT='leftover_meal_chronology';
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION mealplan.check_leftover_chronology() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER leftover_meal_chronology AFTER UPDATE ON mealplan.meals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION mealplan.check_leftover_chronology();

CREATE FUNCTION mealplan.valid_weekly_documents(estimates jsonb,rules jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
DECLARE entry record;
BEGIN
 IF jsonb_typeof(estimates) IS DISTINCT FROM 'object' OR octet_length(estimates::text)>16384
 OR jsonb_typeof(rules) IS DISTINCT FROM 'object' OR octet_length(rules::text)>1024 THEN RETURN false;END IF;
 IF (SELECT count(*) FROM jsonb_object_keys(estimates))>42 THEN RETURN false;END IF;
 FOR entry IN SELECT key,value FROM jsonb_each(estimates) LOOP
  IF entry.key !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  OR jsonb_typeof(entry.value) IS DISTINCT FROM 'object' OR NOT(entry.value ?& ARRAY['meal_version','minor'])
  OR (SELECT count(*) FROM jsonb_object_keys(entry.value))<>2
  OR jsonb_typeof(entry.value->'meal_version') IS DISTINCT FROM 'number' OR coalesce(entry.value->>'meal_version','') !~ '^[1-9][0-9]{0,17}$'
  OR jsonb_typeof(entry.value->'minor') IS DISTINCT FROM 'string' OR coalesce(entry.value->>'minor','') !~ '^(0|[1-9][0-9]{0,12})$' THEN RETURN false;END IF;
  IF (entry.value->>'minor')::numeric>1000000000000 THEN RETURN false;END IF;
 END LOOP;
 IF rules='{}'::jsonb THEN RETURN true;END IF;
 IF NOT(rules ?& ARRAY['stock','urgency','effort','cost','repetition']) OR (SELECT count(*) FROM jsonb_object_keys(rules))<>5 THEN RETURN false;END IF;
 FOR entry IN SELECT key,value FROM jsonb_each(rules) LOOP
  IF jsonb_typeof(entry.value)<>'number' OR entry.value::text !~ '^(0|[1-9][0-9]{0,2})$' THEN RETURN false;END IF;
  IF entry.value::text::integer>100 THEN RETURN false;END IF;
 END LOOP;
 RETURN true;
END $$;
REVOKE ALL ON FUNCTION mealplan.valid_weekly_documents(jsonb,jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION mealplan.valid_weekly_documents(jsonb,jsonb) TO household_app;
CREATE TABLE mealplan.weekly_settings(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),plan_id uuid NOT NULL,week_start date NOT NULL,
 budget_minor bigint CHECK(budget_minor BETWEEN 0 AND 1000000000000),manual_estimates jsonb NOT NULL DEFAULT '{}',rules jsonb NOT NULL DEFAULT '{}',
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),created_by uuid NOT NULL REFERENCES core.users(id),changed_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,plan_id,week_start),FOREIGN KEY(household_id,plan_id) REFERENCES mealplan.plans(household_id,id) ON DELETE CASCADE,
 CONSTRAINT weekly_documents_valid CHECK(mealplan.valid_weekly_documents(manual_estimates,rules))
);
CREATE FUNCTION mealplan.guard_weekly_settings() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE first_day date;last_day date;
BEGIN
 IF NOT mealplan.valid_weekly_documents(NEW.manual_estimates,NEW.rules) THEN
  RAISE EXCEPTION 'invalid bounded weekly settings document' USING ERRCODE='23514';END IF;
 IF TG_OP='UPDATE' AND (NEW.id,NEW.household_id,NEW.plan_id,NEW.week_start,NEW.created_by,NEW.created_at)
 IS DISTINCT FROM(OLD.id,OLD.household_id,OLD.plan_id,OLD.week_start,OLD.created_by,OLD.created_at) THEN
  RAISE EXCEPTION 'weekly settings identity is immutable' USING ERRCODE='23514';END IF;
 SELECT starts_on,ends_on INTO first_day,last_day FROM mealplan.plans WHERE household_id=NEW.household_id AND id=NEW.plan_id FOR UPDATE NOWAIT;
 IF first_day IS NULL OR NEW.week_start>last_day OR NEW.week_start+6<first_day OR EXISTS(
 SELECT 1 FROM jsonb_object_keys(NEW.manual_estimates) e(id) WHERE NOT EXISTS(SELECT 1 FROM mealplan.meals m
 WHERE m.household_id=NEW.household_id AND m.plan_id=NEW.plan_id AND m.id=e.id::uuid AND m.planned_on BETWEEN NEW.week_start AND NEW.week_start+6)) THEN
  RAISE EXCEPTION 'weekly settings must reference their current plan window' USING ERRCODE='23514';END IF;
 IF TG_OP='UPDATE' THEN NEW.version:=OLD.version+1;END IF;NEW.updated_at:=clock_timestamp();RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.guard_weekly_settings() FROM PUBLIC,household_app;
CREATE TRIGGER weekly_settings_change BEFORE INSERT OR UPDATE ON mealplan.weekly_settings FOR EACH ROW EXECUTE FUNCTION mealplan.guard_weekly_settings();

CREATE TABLE mealplan.calendar_feeds(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),plan_id uuid NOT NULL,week_start date NOT NULL,
 issuer_user_id uuid NOT NULL REFERENCES core.users(id),issuer_membership_id uuid NOT NULL,issuer_auth_version bigint NOT NULL CHECK(issuer_auth_version>0),
 issuer_role text NOT NULL CHECK(issuer_role IN('reader','editor','admin')),module_generation bigint NOT NULL CHECK(module_generation>0),
 token_hash bytea NOT NULL UNIQUE CHECK(octet_length(token_hash)=32),expires_at timestamptz NOT NULL CHECK(isfinite(expires_at)),revoked_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 FOREIGN KEY(household_id,plan_id) REFERENCES mealplan.plans(household_id,id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,issuer_membership_id) REFERENCES core.memberships(household_id,id)
);
CREATE INDEX calendar_feed_issuer ON mealplan.calendar_feeds(household_id,issuer_user_id,expires_at) WHERE revoked_at IS NULL;
CREATE FUNCTION mealplan.guard_calendar_feed() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE actor uuid:=nullif(current_setting('household.audit_user',true),'')::uuid;
BEGIN
 IF current_setting('household.audit_kind',true) IS DISTINCT FROM 'user' OR NEW.household_id IS DISTINCT FROM nullif(current_setting('household.audit_household',true),'')::uuid THEN
  RAISE EXCEPTION 'calendar sharing requires current personal authority' USING ERRCODE='42501';END IF;
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.household_id,NEW.plan_id,NEW.week_start,NEW.issuer_user_id,NEW.issuer_membership_id,NEW.issuer_auth_version,NEW.issuer_role,NEW.module_generation,NEW.token_hash,NEW.expires_at,NEW.created_at)
  IS DISTINCT FROM(OLD.id,OLD.household_id,OLD.plan_id,OLD.week_start,OLD.issuer_user_id,OLD.issuer_membership_id,OLD.issuer_auth_version,OLD.issuer_role,OLD.module_generation,OLD.token_hash,OLD.expires_at,OLD.created_at)
  OR OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL OR NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
   WHERE m.household_id=NEW.household_id AND m.user_id=actor AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password AND(actor=NEW.issuer_user_id OR m.role='admin')) THEN
   RAISE EXCEPTION 'only the issuer or administrator may revoke an immutable feed' USING ERRCODE='42501';END IF;
  NEW.revoked_at:=clock_timestamp();NEW.version:=OLD.version+1;
 ELSE
  NEW.created_at:=clock_timestamp();NEW.version:=1;
  IF actor IS DISTINCT FROM NEW.issuer_user_id OR NEW.revoked_at IS NOT NULL OR NEW.expires_at<=clock_timestamp() OR NEW.expires_at>NEW.created_at+interval '90 days'
  OR NOT EXISTS(SELECT 1 FROM core.module_states ms JOIN mealplan.plans p ON p.household_id=ms.household_id AND p.id=NEW.plan_id
   WHERE ms.household_id=NEW.household_id AND ms.module_id='mealplan' AND ms.state='enabled' AND ms.data_generation=NEW.module_generation
   AND NEW.week_start<=p.ends_on AND NEW.week_start+6>=p.starts_on) THEN
   RAISE EXCEPTION 'review current calendar scope and ninety day expiry' USING ERRCODE='23514';END IF;
  PERFORM m.id FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=NEW.household_id AND m.id=NEW.issuer_membership_id
   AND m.user_id=NEW.issuer_user_id AND m.role=NEW.issuer_role AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
   AND u.auth_version=NEW.issuer_auth_version FOR UPDATE OF m NOWAIT;
  IF NOT FOUND OR (SELECT count(*) FROM mealplan.calendar_feeds WHERE household_id=NEW.household_id AND issuer_user_id=NEW.issuer_user_id
   AND revoked_at IS NULL AND expires_at>clock_timestamp())>=20 THEN RAISE EXCEPTION 'current issuer and at most twenty active feeds required' USING ERRCODE='23514';END IF;
 END IF;
 NEW.updated_at:=clock_timestamp();RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.guard_calendar_feed() FROM PUBLIC,household_app;
CREATE TRIGGER calendar_feed_change BEFORE INSERT OR UPDATE ON mealplan.calendar_feeds FOR EACH ROW EXECUTE FUNCTION mealplan.guard_calendar_feed();

CREATE TABLE mealplan.calendar_tombstones(
 household_id uuid NOT NULL REFERENCES core.households(id),plan_id uuid NOT NULL,meal_id uuid NOT NULL,planned_on date NOT NULL,
 module_generation bigint NOT NULL CHECK(module_generation>0),sequence bigint NOT NULL CHECK(sequence>0),deleted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(household_id,module_generation,meal_id,planned_on),
 FOREIGN KEY(household_id,plan_id) REFERENCES mealplan.plans(household_id,id) ON DELETE CASCADE
);
CREATE INDEX calendar_tombstone_window ON mealplan.calendar_tombstones(household_id,plan_id,planned_on,module_generation);
CREATE FUNCTION mealplan.capture_calendar_tombstone() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE generation bigint;revision bigint;
BEGIN
 IF TG_RELID<>'mealplan.meals'::regclass OR TG_LEVEL<>'ROW' OR TG_WHEN<>'AFTER' OR TG_OP NOT IN('UPDATE','DELETE') THEN
  RAISE EXCEPTION 'invalid calendar tombstone trigger context' USING ERRCODE='42501';END IF;
 IF TG_OP='UPDATE' AND OLD.planned_on=NEW.planned_on THEN RETURN NULL;END IF;
 -- A deleted parent plan has no remaining subscription scope to cancel.
 IF NOT EXISTS(SELECT 1 FROM mealplan.plans WHERE household_id=OLD.household_id AND id=OLD.plan_id) THEN RETURN NULL;END IF;
 SELECT data_generation INTO generation FROM core.module_states WHERE household_id=OLD.household_id AND module_id='mealplan';
 IF generation IS NULL THEN RETURN NULL;END IF;
 revision:=OLD.version+1;IF TG_OP='UPDATE' THEN revision:=NEW.version;END IF;
 INSERT INTO mealplan.calendar_tombstones(household_id,plan_id,meal_id,planned_on,module_generation,sequence)
 VALUES(OLD.household_id,OLD.plan_id,OLD.id,OLD.planned_on,generation,revision)
 ON CONFLICT(household_id,module_generation,meal_id,planned_on) DO UPDATE SET sequence=greatest(mealplan.calendar_tombstones.sequence,excluded.sequence),deleted_at=clock_timestamp();
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION mealplan.capture_calendar_tombstone() FROM PUBLIC,household_app;
CREATE TRIGGER planning_calendar_tombstone AFTER UPDATE OF planned_on OR DELETE ON mealplan.meals FOR EACH ROW EXECUTE FUNCTION mealplan.capture_calendar_tombstone();

REVOKE ALL ON mealplan.leftover_sources,mealplan.leftover_allocations,mealplan.weekly_settings,mealplan.calendar_feeds,mealplan.calendar_tombstones FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON mealplan.leftover_sources,mealplan.leftover_allocations,mealplan.weekly_settings TO household_app;
GRANT SELECT,INSERT,UPDATE ON mealplan.calendar_feeds TO household_app;
GRANT SELECT ON mealplan.calendar_tombstones TO household_app;

-- Pin only the five new tables after PostgreSQL has rewritten their CHECKs
-- through its dump representation. Released predicates remain untouched.
DO $canonical_checks$
DECLARE item record;
BEGIN
 FOR item IN SELECT c.relname,k.conname,pg_get_constraintdef(k.oid) AS definition
 FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='mealplan' AND c.relname IN('leftover_sources','leftover_allocations','weekly_settings','calendar_feeds','calendar_tombstones')
 AND k.contype='c' ORDER BY c.relname,k.conname LOOP
  EXECUTE format('ALTER TABLE mealplan.%I DROP CONSTRAINT %I',item.relname,item.conname);
  EXECUTE format('ALTER TABLE mealplan.%I ADD CONSTRAINT %I %s',item.relname,item.conname,item.definition);
 END LOOP;
END $canonical_checks$;

CREATE FUNCTION mealplan.extension_catalog() RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
 'columns',(SELECT jsonb_agg(jsonb_build_array(c.relname,a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attidentity,a.attgenerated,
 (SELECT quote_ident(nc.nspname)||'.'||quote_ident(co.collname) FROM pg_collation co JOIN pg_namespace nc ON nc.oid=co.collnamespace WHERE co.oid=a.attcollation),
 coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY c.relname,a.attnum)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
 LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE n.nspname='mealplan'
 AND c.relname IN('leftover_sources','leftover_allocations','weekly_settings','calendar_feeds','calendar_tombstones')),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(c.relname,k.conname,pg_get_constraintdef(k.oid),k.convalidated,k.conislocal,k.coninhcount,k.connoinherit)
 ORDER BY c.relname,k.conname) FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='mealplan' AND c.relname IN('leftover_sources','leftover_allocations','weekly_settings','calendar_feeds','calendar_tombstones')),
 'indexes',(SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready,i.indislive) ORDER BY pg_get_indexdef(i.indexrelid))
 FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='mealplan' AND c.relname IN('leftover_sources','leftover_allocations','weekly_settings','calendar_feeds','calendar_tombstones')),
 'tables',(SELECT jsonb_agg(jsonb_build_array(c.relname,c.relkind::text,c.relpersistence::text,c.relispartition,c.relrowsecurity,c.relforcerowsecurity,
 EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid),
 pg_get_userbyid(c.relowner),coalesce(c.relacl,acldefault('r',c.relowner))::text,
 (SELECT coalesce(jsonb_agg(jsonb_build_array(a.attname,coalesce(a.attacl,'{}'::aclitem[])::text) ORDER BY a.attnum),'[]')
 FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped)) ORDER BY c.relname)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='mealplan'
 AND c.relname IN('leftover_sources','leftover_allocations','weekly_settings','calendar_feeds','calendar_tombstones')),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(c.relname,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled::text,t.tgdeferrable,t.tginitdeferred)
 ORDER BY c.relname,t.tgname) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='mealplan' AND NOT t.tgisinternal AND t.tgname IN('leftover_source_change','leftover_allocation_change',
 'leftover_meal_chronology','weekly_settings_change','calendar_feed_change','planning_calendar_tombstone')),
 'functions',(SELECT jsonb_agg(jsonb_build_array(p.proname,pg_get_function_identity_arguments(p.oid),md5(pg_get_functiondef(p.oid)),
 pg_get_userbyid(p.proowner),p.prosecdef,p.provolatile::text,p.proconfig,coalesce(p.proacl,'{}'::aclitem[])::text) ORDER BY p.proname)
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='mealplan'
 AND p.proname IN('slot_order','guard_leftover_source','guard_leftover_allocation','check_leftover_chronology',
 'valid_weekly_documents','guard_weekly_settings','guard_calendar_feed','capture_calendar_tombstone')))
$$;
REVOKE ALL ON FUNCTION mealplan.extension_catalog() FROM PUBLIC,household_app;
DO $ready$
DECLARE expected jsonb;catalog_hash text;
BEGIN
 expected:=mealplan.extension_catalog();
 SELECT md5(pg_get_functiondef('mealplan.extension_catalog()'::regprocedure)) INTO catalog_hash;
 EXECUTE format($definition$CREATE FUNCTION mealplan.expansion_ready() RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT mealplan.extension_catalog()=%L::jsonb
 AND md5(pg_get_functiondef('mealplan.extension_catalog()'::regprocedure))=%L
 AND NOT has_table_privilege('household_app','mealplan.calendar_tombstones','INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','mealplan.calendar_feeds','DELETE,TRUNCATE,TRIGGER')
 AND NOT has_function_privilege('household_app','mealplan.capture_calendar_tombstone()','EXECUTE')
 AND NOT has_function_privilege('household_app','mealplan.extension_catalog()','EXECUTE')
 $body$;$definition$,expected::text,catalog_hash);
END $ready$;
REVOKE ALL ON FUNCTION mealplan.expansion_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION mealplan.expansion_ready() TO household_app;
COMMIT;
