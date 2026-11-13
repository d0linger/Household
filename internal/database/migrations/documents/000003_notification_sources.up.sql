BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

CREATE TEMP TABLE documents_notification_before(snapshot jsonb NOT NULL) ON COMMIT DROP;
REVOKE ALL ON documents_notification_before FROM PUBLIC,household_app;
DO $predecessor$
DECLARE reviewed jsonb;snapshot jsonb;expected text;actual text;item text;catalog_hash text;catalog_owner text;catalog_acl text;
 old_audit text;new_audit text;new_pantry text;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=39 AND NOT dirty) FROM core.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=6 AND NOT dirty) FROM expenses.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=3 AND NOT dirty) FROM meters.schema_migrations)
 OR NOT documents.expansion_ready() OR NOT meters.notification_ready() OR NOT core.audit_ready() OR NOT core.finance_ready()
 OR NOT core.notification_financial_ready() OR NOT expenses.notification_ready()
 OR NOT pantry.notification_observations_ready() OR NOT fuel.notification_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
  RAISE EXCEPTION 'Document notifications require accepted Core39/Expenses6/Meters3/Documents2';END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='documents.expansion_catalog()'::regprocedure
  AND md5(replace(p.prosrc,E'\r\n',E'\n'))='61999ccf9fb17c07be12356fc5164be5'
  AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.provolatile='s'
  AND p.proconfig=ARRAY['search_path=pg_catalog'] AND NOT EXISTS(
   SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
   WHERE a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE')) THEN
  RAISE EXCEPTION 'Unreviewed Documents2 catalog implementation';END IF;
 SELECT encode(sha256(convert_to(pg_get_functiondef(p.oid),'UTF8')),'hex'),p.proowner::regrole::text,p.proacl::text
 INTO catalog_hash,catalog_owner,catalog_acl FROM pg_proc p WHERE p.oid='documents.expansion_catalog()'::regprocedure;
 expected=format('SELECT documents.expansion_catalog()=%L::jsonb AND (SELECT count(*) FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname=''documents'' AND t.relkind IN(''r'',''p'') AND t.relname<>''schema_migrations'')=12 AND EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid=''documents.expansion_catalog()''::regprocedure AND encode(sha256(convert_to(pg_get_functiondef(p.oid),''UTF8'')),''hex'')=%L AND p.proowner::regrole::text=%L AND p.proacl::text IS NOT DISTINCT FROM %L)',documents.expansion_catalog()::text,catalog_hash,catalog_owner,catalog_acl);
 IF NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='documents.expansion_ready()'::regprocedure
  AND p.prosrc=expected AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.provolatile='s'
  AND p.proconfig=ARRAY['search_path=pg_catalog']) THEN RAISE EXCEPTION 'Unreviewed Documents2 readiness implementation';END IF;
 -- Reconstruct the accepted Meters3 audit composition from its frozen, reviewed
 -- predecessor rather than accepting an arbitrary readiness function returning true.
 reviewed=meters.notification_predecessor_catalog();
 old_audit=reviewed->'functions'->'core.audit_ready()'->>'definition';
 new_audit=replace(old_audit,'AND NOT(n.nspname=''expenses'' AND c.relname IN(''notification_payment_reminders'',''notification_budget_observations''))',
  'AND NOT(n.nspname=''expenses'' AND c.relname IN(''notification_payment_reminders'',''notification_budget_observations''))'||E'\n AND NOT(n.nspname=''meters'' AND c.relname IN(''notification_settings'',''notification_observations''))');
 new_pantry=replace(reviewed->'functions'->'pantry.prepared_capture_ready()'->>'definition',md5(old_audit),md5(new_audit));
 IF old_audit IS NULL OR new_audit=old_audit OR new_pantry IS NULL
 OR pantry.notification_observations_catalog()-'compatibility' IS DISTINCT FROM (reviewed->'pantry_catalog')-'compatibility' THEN
  RAISE EXCEPTION 'Unreviewed Meters3 audit composition';END IF;
 FOREACH item IN ARRAY ARRAY['core.audit_ready()','pantry.prepared_capture_ready()','pantry.notification_observations_ready()'] LOOP
  expected=CASE item WHEN 'core.audit_ready()' THEN new_audit WHEN 'pantry.prepared_capture_ready()' THEN new_pantry
   ELSE replace(reviewed->'functions'->item->>'definition',quote_literal((reviewed->'pantry_catalog')::text)||'::jsonb',
    quote_literal(pantry.notification_observations_catalog()::text)||'::jsonb') END;
  actual=NULL;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=reviewed->'functions'->item->>'owner'
   AND p.proacl::text IS NOT DISTINCT FROM reviewed->'functions'->item->>'acl';
  IF expected IS NULL OR actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'Unreviewed document predecessor: %',item;END IF;
 END LOOP;
 SELECT jsonb_build_object('documents_catalog',documents.expansion_catalog(),'pantry_catalog',pantry.notification_observations_catalog(),
  'functions',jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
   'owner',p.proowner::regrole::text,'acl',p.proacl::text))) INTO snapshot FROM pg_proc p WHERE p.oid IN(
  'documents.expansion_ready()'::regprocedure,'documents.expansion_catalog()'::regprocedure,'core.audit_ready()'::regprocedure,
  'pantry.prepared_capture_ready()'::regprocedure,'pantry.notification_observations_ready()'::regprocedure);
 INSERT INTO documents_notification_before VALUES(snapshot);
 EXECUTE format('CREATE FUNCTION documents.notification_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION documents.notification_predecessor_catalog() FROM PUBLIC,household_app;

-- The old deadline has no modification clock. Existing records begin at this
-- migration boundary; their old dates must never be replayed as new reminders.
ALTER TABLE documents.deadlines ADD COLUMN notification_updated_at timestamptz NOT NULL DEFAULT clock_timestamp();
ALTER TABLE documents.deadlines ADD CONSTRAINT document_deadline_notification_clock
 CHECK(isfinite(notification_updated_at) AND notification_updated_at>='2000-01-01'::timestamptz
  AND notification_updated_at<'10000-01-01'::timestamptz);
ALTER TABLE documents.documents ADD COLUMN notification_active_since timestamptz NOT NULL DEFAULT clock_timestamp();
ALTER TABLE documents.documents ADD CONSTRAINT document_notification_active_clock
 CHECK(isfinite(notification_active_since) AND notification_active_since>='2000-01-01'::timestamptz
  AND notification_active_since<'10000-01-01'::timestamptz);

CREATE TABLE documents.notification_settings(
 id uuid PRIMARY KEY,household_id uuid NOT NULL,
 responsible_member_id uuid,responsible_since timestamptz,enabled boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
 created_by uuid NOT NULL REFERENCES core.users(id),changed_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id),
 FOREIGN KEY(household_id,id) REFERENCES documents.deadlines(household_id,id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,responsible_member_id) REFERENCES core.memberships(household_id,id),
 CHECK((responsible_member_id IS NULL)=(responsible_since IS NULL)),CHECK(NOT enabled OR responsible_member_id IS NOT NULL),
 CHECK(isfinite(created_at) AND created_at>='2000-01-01'::timestamptz AND isfinite(updated_at)
  AND created_at<=updated_at AND updated_at<'10000-01-01'::timestamptz
  AND(responsible_since IS NULL OR(isfinite(responsible_since) AND responsible_since>=created_at AND responsible_since<=updated_at)))
);
CREATE INDEX notification_settings_responsible ON documents.notification_settings(household_id,responsible_member_id,id) WHERE enabled;

CREATE TABLE documents.notification_processing_events(
 id uuid PRIMARY KEY,household_id uuid NOT NULL,
 epoch bigint NOT NULL CHECK(epoch BETWEEN 0 AND 9007199254740991),active boolean NOT NULL,
 occurred_at timestamptz,source_generation bigint NOT NULL CHECK(source_generation BETWEEN 1 AND 9007199254740991),
 recipient_user_id uuid NOT NULL REFERENCES core.users(id),UNIQUE(household_id,id),
 FOREIGN KEY(household_id,id) REFERENCES documents.processing(household_id,id) ON DELETE CASCADE,
 CHECK(NOT active OR(epoch>0 AND occurred_at IS NOT NULL)),
 CHECK(occurred_at IS NULL OR(isfinite(occurred_at) AND occurred_at>='2000-01-01'::timestamptz AND occurred_at<'10000-01-01'::timestamptz))
);
CREATE INDEX notification_processing_active ON documents.notification_processing_events(household_id,source_generation,occurred_at,id) WHERE active;
CREATE TABLE documents.notification_import_events(
 id uuid PRIMARY KEY,household_id uuid NOT NULL,
 epoch bigint NOT NULL CHECK(epoch BETWEEN 0 AND 9007199254740991),active boolean NOT NULL,
 occurred_at timestamptz,source_generation bigint NOT NULL CHECK(source_generation BETWEEN 1 AND 9007199254740991),
 source_version bigint NOT NULL CHECK(source_version BETWEEN 1 AND 9007199254740991),
 recipient_user_id uuid NOT NULL REFERENCES core.users(id),UNIQUE(household_id,id),
 FOREIGN KEY(household_id,id) REFERENCES documents.integration_sources(household_id,id) ON DELETE CASCADE,
 CHECK(NOT active OR(epoch>0 AND occurred_at IS NOT NULL)),
 CHECK(occurred_at IS NULL OR(isfinite(occurred_at) AND occurred_at>='2000-01-01'::timestamptz AND occurred_at<'10000-01-01'::timestamptz))
);
CREATE INDEX notification_import_active ON documents.notification_import_events(household_id,source_generation,occurred_at,id) WHERE active;
REVOKE ALL ON documents.notification_settings,documents.notification_processing_events,documents.notification_import_events FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON documents.notification_settings TO household_app;
GRANT SELECT ON documents.notification_processing_events,documents.notification_import_events TO household_app;

CREATE FUNCTION documents.notification_deadline_clock() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF TG_TABLE_SCHEMA<>'documents' OR TG_TABLE_NAME<>'deadlines' OR TG_OP NOT IN('INSERT','UPDATE') THEN
  RAISE EXCEPTION 'invalid document notification deadline trigger' USING ERRCODE='42501';END IF;
 IF TG_OP='INSERT' OR(to_jsonb(NEW)-'notification_updated_at') IS DISTINCT FROM(to_jsonb(OLD)-'notification_updated_at') THEN
  NEW.notification_updated_at=clock_timestamp();
 ELSE NEW.notification_updated_at=OLD.notification_updated_at;END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION documents.notification_deadline_clock() FROM PUBLIC,household_app;
CREATE TRIGGER notification_deadline_clock BEFORE INSERT OR UPDATE ON documents.deadlines
 FOR EACH ROW EXECUTE FUNCTION documents.notification_deadline_clock();

CREATE FUNCTION documents.notification_document_clock() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF TG_TABLE_SCHEMA<>'documents' OR TG_TABLE_NAME<>'documents' OR TG_OP NOT IN('INSERT','UPDATE') THEN
  RAISE EXCEPTION 'invalid document notification activity trigger' USING ERRCODE='42501';END IF;
 IF TG_OP='INSERT' OR NEW.archived_at IS DISTINCT FROM OLD.archived_at THEN
  NEW.notification_active_since=clock_timestamp();
 ELSE NEW.notification_active_since=OLD.notification_active_since;END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION documents.notification_document_clock() FROM PUBLIC,household_app;
CREATE TRIGGER notification_document_clock BEFORE INSERT OR UPDATE ON documents.documents
 FOR EACH ROW EXECUTE FUNCTION documents.notification_document_clock();

CREATE FUNCTION documents.notification_settings_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE context_value jsonb;actor uuid;now_at timestamptz=clock_timestamp();member_changed boolean;
BEGIN
 IF TG_TABLE_SCHEMA<>'documents' OR TG_TABLE_NAME<>'notification_settings' OR TG_OP NOT IN('INSERT','UPDATE','DELETE') THEN
  RAISE EXCEPTION 'invalid document notification settings trigger' USING ERRCODE='42501';END IF;
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
 PERFORM 1 FROM documents.deadlines x JOIN documents.documents d ON d.household_id=x.household_id AND d.id=x.document_id
  WHERE x.household_id=NEW.household_id AND x.id=NEW.id AND x.cancelled_at IS NULL AND d.archived_at IS NULL FOR SHARE OF x,d NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'active confirmed document deadline required' USING ERRCODE='23514';END IF;
 member_changed=TG_OP='INSERT' OR NEW.responsible_member_id IS DISTINCT FROM OLD.responsible_member_id;
 IF NEW.responsible_member_id IS NOT NULL AND(member_changed OR NEW.enabled) THEN
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
REVOKE ALL ON FUNCTION documents.notification_settings_guard() FROM PUBLIC,household_app;
CREATE TRIGGER notification_settings_guard BEFORE INSERT OR UPDATE OR DELETE ON documents.notification_settings
 FOR EACH ROW EXECUTE FUNCTION documents.notification_settings_guard();

CREATE FUNCTION documents.notification_settings_audit() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE context_value jsonb;deadline_id uuid;household uuid;transition jsonb;
BEGIN
 IF TG_TABLE_SCHEMA<>'documents' OR TG_TABLE_NAME<>'notification_settings' OR TG_OP NOT IN('INSERT','UPDATE','DELETE') THEN
  RAISE EXCEPTION 'invalid document notification settings audit trigger' USING ERRCODE='42501';END IF;
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 context_value=core.audit_context();deadline_id=coalesce(NEW.id,OLD.id);household=coalesce(NEW.household_id,OLD.household_id);
 transition=jsonb_build_object('notification_configuration_version',jsonb_build_object('before',CASE WHEN TG_OP='INSERT' THEN NULL ELSE OLD.version END,
  'after',CASE WHEN TG_OP='DELETE' THEN NULL ELSE NEW.version END));
 INSERT INTO core.audit_log(household_id,actor_user_id,module_id,resource_type,resource_id,action,outcome,request_id,details,actor_kind,db_role,entity_key,entity_label,changes,created_at)
 VALUES(household,(context_value->>'actor')::uuid,'documents','deadlines',deadline_id,'notification_settings_'||lower(TG_OP),'success',
  (context_value->>'request')::uuid,'{}',context_value->>'kind',session_user,jsonb_build_object('id',deadline_id),'documents.deadlines',transition,clock_timestamp());
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION documents.notification_settings_audit() FROM PUBLIC,household_app;
CREATE TRIGGER notification_settings_audit AFTER INSERT OR UPDATE OR DELETE ON documents.notification_settings
 FOR EACH ROW EXECUTE FUNCTION documents.notification_settings_audit();

-- These triggers capture real transitions in the same transaction as the source
-- failure. Repeated polling cannot move the event time or manufacture new alerts.
-- No existing failed source is seeded during upgrade.
CREATE FUNCTION documents.notification_processing_transition() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF TG_TABLE_SCHEMA<>'documents' OR TG_TABLE_NAME<>'processing' OR TG_OP<>'UPDATE' THEN
  RAISE EXCEPTION 'invalid document processing notification trigger' USING ERRCODE='42501';END IF;
 IF NEW.state<>'failed' OR NEW.status_code NOT IN('local_processing_failed','processing_interrupted','worker_failed')
  OR ROW(NEW.household_id,NEW.id,NEW.document_id,NEW.requested_by,NEW.module_generation)
   IS DISTINCT FROM ROW(OLD.household_id,OLD.id,OLD.document_id,OLD.requested_by,OLD.module_generation) THEN
  UPDATE documents.notification_processing_events SET active=false WHERE household_id=OLD.household_id AND id=OLD.id AND active;
  RETURN NULL;
 END IF;
 IF OLD.state='failed' THEN RETURN NULL;END IF;
 INSERT INTO documents.notification_processing_events AS e(id,household_id,epoch,active,occurred_at,source_generation,recipient_user_id)
 VALUES(NEW.id,NEW.household_id,1,true,clock_timestamp(),NEW.module_generation,NEW.requested_by)
 ON CONFLICT(id) DO UPDATE SET epoch=e.epoch+1,active=true,occurred_at=excluded.occurred_at,
  source_generation=excluded.source_generation,recipient_user_id=excluded.recipient_user_id;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION documents.notification_processing_transition() FROM PUBLIC,household_app;
CREATE TRIGGER notification_processing_transition AFTER UPDATE ON documents.processing
 FOR EACH ROW EXECUTE FUNCTION documents.notification_processing_transition();

CREATE FUNCTION documents.notification_import_transition() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF TG_TABLE_SCHEMA<>'documents' OR TG_TABLE_NAME<>'integration_sources' OR TG_OP<>'UPDATE' THEN
  RAISE EXCEPTION 'invalid document import notification trigger' USING ERRCODE='42501';END IF;
 IF NOT NEW.enabled OR NEW.status_code<>'input_failed'
  OR ROW(NEW.household_id,NEW.id,NEW.version,NEW.module_generation,NEW.approved_by,NEW.enabled)
   IS DISTINCT FROM ROW(OLD.household_id,OLD.id,OLD.version,OLD.module_generation,OLD.approved_by,OLD.enabled) THEN
  UPDATE documents.notification_import_events SET active=false WHERE household_id=OLD.household_id AND id=OLD.id AND active;
  RETURN NULL;
 END IF;
 IF OLD.status_code='input_failed' THEN RETURN NULL;END IF;
 INSERT INTO documents.notification_import_events AS e(id,household_id,epoch,active,occurred_at,source_generation,source_version,recipient_user_id)
 VALUES(NEW.id,NEW.household_id,1,true,clock_timestamp(),NEW.module_generation,NEW.version,NEW.approved_by)
 ON CONFLICT(id) DO UPDATE SET epoch=e.epoch+1,active=true,occurred_at=excluded.occurred_at,
  source_generation=excluded.source_generation,source_version=excluded.source_version,recipient_user_id=excluded.recipient_user_id;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION documents.notification_import_transition() FROM PUBLIC,household_app;
CREATE TRIGGER notification_import_transition AFTER UPDATE ON documents.integration_sources
 FOR EACH ROW EXECUTE FUNCTION documents.notification_import_transition();

DO $canonical$
DECLARE item record;
BEGIN
 FOR item IN SELECT conrelid::regclass relation,conname,pg_get_constraintdef(oid) definition FROM pg_constraint
 WHERE contype='c' AND(conrelid IN('documents.notification_settings'::regclass,'documents.notification_processing_events'::regclass,
  'documents.notification_import_events'::regclass) OR conrelid='documents.deadlines'::regclass AND conname='document_deadline_notification_clock'
  OR conrelid='documents.documents'::regclass AND conname='document_notification_active_clock')
 ORDER BY conrelid,conname LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',item.relation,item.conname);
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s',item.relation,item.conname,item.definition);
 END LOOP;
END $canonical$;

CREATE FUNCTION documents.notification_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
 'tables',(SELECT jsonb_agg(jsonb_build_array(c.relname,c.relowner::regrole::text,c.relkind,c.relpersistence,c.relrowsecurity,c.relforcerowsecurity,c.relispartition,c.relacl::text,
  EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)) ORDER BY c.relname)
  FROM pg_class c WHERE c.oid IN('documents.notification_settings'::regclass,'documents.notification_processing_events'::regclass,'documents.notification_import_events'::regclass)),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(tgrelid::regclass::text,tgname,pg_get_triggerdef(oid),tgenabled,tgisinternal) ORDER BY tgrelid::regclass::text,tgname)
  FROM pg_trigger WHERE NOT tgisinternal AND(tgrelid IN('documents.notification_settings'::regclass,'documents.notification_processing_events'::regclass,
   'documents.notification_import_events'::regclass) OR tgrelid IN('documents.documents'::regclass,'documents.deadlines'::regclass,'documents.processing'::regclass,
   'documents.integration_sources'::regclass) AND tgname LIKE 'notification_%')),
 -- Internal FK trigger names contain OIDs that change on restore. Pin both
 -- referencing checks and referenced actions by their full stable semantics.
 'constraint_triggers',(SELECT jsonb_agg(identity ORDER BY identity) FROM(
  SELECT jsonb_build_array(k.conrelid::regclass::text,k.conname,k.contype,pg_get_constraintdef(k.oid),k.convalidated,
   t.tgrelid::regclass::text,t.tgconstrrelid::regclass::text,t.tgconstrindid::regclass::text,
   t.tgfoid::regprocedure::text,t.tgtype,t.tgenabled,t.tgisinternal,t.tgdeferrable,t.tginitdeferred,
   t.tgnargs,encode(t.tgargs,'hex'),t.tgattr::text,pg_get_expr(t.tgqual,t.tgrelid)) identity
  FROM pg_trigger t LEFT JOIN pg_constraint k ON k.oid=t.tgconstraint
  WHERE t.tgisinternal AND(t.tgrelid IN('documents.notification_settings'::regclass,'documents.notification_processing_events'::regclass,'documents.notification_import_events'::regclass) OR
   k.conrelid IN('documents.notification_settings'::regclass,'documents.notification_processing_events'::regclass,'documents.notification_import_events'::regclass))
 ) fk_triggers),
 'functions',(SELECT jsonb_agg(jsonb_build_array(p.oid::regprocedure::text,pg_get_functiondef(p.oid),p.proowner::regrole::text,p.proacl::text) ORDER BY p.oid::regprocedure::text)
  FROM pg_proc p WHERE p.oid IN('documents.notification_settings_guard()'::regprocedure,'documents.notification_settings_audit()'::regprocedure,
   'documents.notification_deadline_clock()'::regprocedure,'documents.notification_document_clock()'::regprocedure,
   'documents.notification_processing_transition()'::regprocedure,'documents.notification_import_transition()'::regprocedure)),
 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY table_name) FROM core.audit_policies p
  WHERE schema_name='documents' AND table_name IN('notification_settings','notification_processing_events','notification_import_events')))
$$;
REVOKE ALL ON FUNCTION documents.notification_catalog() FROM PUBLIC,household_app;
DO $ready$
DECLARE expected jsonb;catalog_hash text;
BEGIN
 expected=documents.notification_catalog();catalog_hash=md5(pg_get_functiondef('documents.notification_catalog()'::regprocedure));
 EXECUTE format($definition$CREATE FUNCTION documents.notification_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT documents.expansion_ready() AND documents.notification_catalog()=%L::jsonb
 AND md5(pg_get_functiondef('documents.notification_catalog()'::regprocedure))=%L
 AND NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid IN('documents.notification_catalog()'::regprocedure,'documents.notification_predecessor_catalog()'::regprocedure)
  AND(p.proowner<>'household_owner'::regrole OR NOT p.prosecdef OR p.proconfig IS DISTINCT FROM ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
  WHERE p.oid IN('documents.notification_catalog()'::regprocedure,'documents.notification_predecessor_catalog()'::regprocedure)
   AND(a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE'))
 $body$$definition$,expected,catalog_hash);
END $ready$;
REVOKE ALL ON FUNCTION documents.notification_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION documents.notification_ready() TO household_app;

DO $compatibility$
DECLARE old jsonb;new_catalog jsonb;key text;values_before jsonb;values_after jsonb;ready text;needle text;
 old_audit text;new_audit text;
BEGIN
 SELECT snapshot INTO STRICT old FROM documents_notification_before;
 new_catalog=documents.expansion_catalog();
 -- Permit only the three sidecars, the owned source clocks and the exact new
 -- transition triggers. Everything else in the accepted Documents2 stays pinned.
 FOREACH key IN ARRAY ARRAY['relations','columns','constraints','indexes','triggers','functions'] LOOP
  SELECT coalesce(jsonb_agg(value ORDER BY ordinal),'[]') INTO values_after
  FROM jsonb_array_elements(new_catalog->key) WITH ORDINALITY x(value,ordinal)
  WHERE CASE WHEN key='functions' THEN value->>0 NOT IN('documents.notification_predecessor_catalog()',
   'documents.notification_settings_guard()','documents.notification_settings_audit()','documents.notification_deadline_clock()',
   'documents.notification_document_clock()',
   'documents.notification_processing_transition()','documents.notification_import_transition()',
   'documents.notification_catalog()','documents.notification_ready()')
   ELSE value->>0 NOT IN('notification_settings','notification_processing_events','notification_import_events') END
   AND NOT(key='columns' AND value->>0='deadlines' AND value->>1='notification_updated_at')
   AND NOT(key='columns' AND value->>0='documents' AND value->>1='notification_active_since')
   AND NOT(key='constraints' AND value->>0='deadlines' AND value->>1='document_deadline_notification_clock')
   AND NOT(key='constraints' AND value->>0='documents' AND value->>1='document_notification_active_clock')
   -- PostgreSQL18 catalogs NOT NULL as native constraints. Exclude only the
   -- validated one-column constraints created for these two new clocks from
   -- the predecessor comparison; the final catalog below still pins both.
   AND NOT(key='constraints' AND EXISTS(
    SELECT 1 FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid
    JOIN pg_attribute a ON a.attrelid=r.oid AND a.attnum=c.conkey[1]
    WHERE c.contype='n' AND c.convalidated AND c.conkey=ARRAY[a.attnum]
     AND(r.oid,a.attname) IN(('documents.deadlines'::regclass,'notification_updated_at'),
      ('documents.documents'::regclass,'notification_active_since'))
     AND value->>0=r.relname AND value->>1=c.conname AND value->>2=pg_get_constraintdef(c.oid)
     AND(value->>3)::boolean=c.convalidated))
   AND NOT(key='triggers' AND(value->>0,value->>1) IN(('deadlines','notification_deadline_clock'),
    ('documents','notification_document_clock'),('processing','notification_processing_transition'),('integration_sources','notification_import_transition')));
  values_before=coalesce(old->'documents_catalog'->key,'[]');
  IF values_after IS DISTINCT FROM values_before THEN RAISE EXCEPTION 'Unrelated Documents metadata changed: %',key;END IF;
 END LOOP;
 ready=old->'functions'->'documents.expansion_ready()'->>'definition';needle=quote_literal((old->'documents_catalog')::text)||'::jsonb';
 IF (length(ready)-length(replace(ready,needle,'')))/length(needle)<>1 OR position(')=12 AND EXISTS' in ready)=0 THEN
  RAISE EXCEPTION 'Unexpected Documents readiness snapshot';END IF;
 EXECUTE replace(replace(ready,needle,quote_literal(new_catalog::text)||'::jsonb'),')=12 AND EXISTS',')=15 AND EXISTS');
 old_audit=old->'functions'->'core.audit_ready()'->>'definition';
 needle='AND NOT(n.nspname=''meters'' AND c.relname IN(''notification_settings'',''notification_observations''))';
 IF (length(old_audit)-length(replace(old_audit,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'Unexpected document audit census predecessor';END IF;
 new_audit=replace(old_audit,needle,needle||E'\n AND NOT(n.nspname=''documents'' AND c.relname IN(''notification_settings'',''notification_processing_events'',''notification_import_events''))');
 EXECUTE new_audit;
 EXECUTE replace(old->'functions'->'pantry.prepared_capture_ready()'->>'definition',md5(old_audit),md5(new_audit));
 new_catalog=pantry.notification_observations_catalog();
 IF new_catalog-'compatibility' IS DISTINCT FROM(old->'pantry_catalog')-'compatibility' THEN RAISE EXCEPTION 'Unrelated Pantry metadata changed';END IF;
 EXECUTE replace(old->'functions'->'pantry.notification_observations_ready()'->>'definition',quote_literal((old->'pantry_catalog')::text)||'::jsonb',quote_literal(new_catalog::text)||'::jsonb');
 IF NOT documents.expansion_ready() OR NOT documents.notification_ready() OR NOT meters.expansion_ready() OR NOT meters.notification_ready() OR NOT core.audit_ready()
 OR NOT pantry.prepared_capture_ready() OR NOT pantry.notification_observations_ready()
 OR NOT expenses.finance_ready() OR NOT expenses.notification_ready() OR NOT core.finance_ready()
 OR NOT core.notification_financial_ready() OR NOT fuel.notification_ready() OR NOT fuel.module_ready()
 OR NOT chores.expansion_ready() OR NOT mealplan.expansion_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN RAISE EXCEPTION 'Incomplete document notification compatibility';END IF;
END $compatibility$;
COMMIT;
