BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

-- Advance only the reviewed Fuel11 authority's Core version. Reconstruct its
-- predecessor from the frozen Documents catalog rather than trust live code.
DO $fuel_compatibility$
DECLARE baseline jsonb;item text;expected text;actual text;authority text;overview text;ready text;
 old_hash text;needle text;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=11 AND NOT dirty) FROM fuel.schema_migrations) THEN
  RAISE EXCEPTION 'Notifications require reviewed Fuel11';END IF;
 baseline=documents.predecessor_catalog()->'functions';
 authority=baseline->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 overview=baseline->'fuel.overview_ready()'->>'definition';
 ready=baseline->'fuel.module_ready()'->>'definition';
 needle='bool_and(version=33 AND NOT dirty)';old_hash=md5(authority);
 IF authority IS NULL OR overview IS NULL OR ready IS NULL
  OR (length(authority)-length(replace(authority,needle,'')))/length(needle)<>1
  OR (length(overview)-length(replace(overview,needle,'')))/length(needle)<>1
  OR (length(ready)-length(replace(ready,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'Notifications Fuel catalog differs from its frozen predecessor';END IF;
 authority=replace(authority,needle,'bool_and(version=37 AND NOT dirty)');
 overview=replace(overview,needle,'bool_and(version=37 AND NOT dirty)');
 ready=replace(ready,old_hash,md5(authority));
 IF position(quote_literal(fuel.module_catalog()::text)||'::jsonb' in ready)=0
  OR position(quote_literal(md5(pg_get_functiondef('fuel.module_catalog()'::regprocedure))) in ready)=0 THEN
  RAISE EXCEPTION 'Notifications Fuel11 catalog differs from its frozen predecessor';END IF;
 FOREACH item IN ARRAY ARRAY['fuel.module_ready()','fuel.overview_ready()','fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'] LOOP
  expected=CASE item WHEN 'fuel.module_ready()' THEN ready WHEN 'fuel.overview_ready()' THEN overview ELSE authority END;
  actual=NULL;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=baseline->item->>'owner' AND p.proacl::text IS NOT DISTINCT FROM baseline->item->>'acl';
  IF actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'unreviewed Notifications Fuel predecessor: %',item;END IF;
 END LOOP;
 old_hash=md5(authority);needle='bool_and(version=37 AND NOT dirty)';
 EXECUTE replace(overview,needle,'bool_and(version=38 AND NOT dirty)');
 EXECUTE replace(authority,needle,'bool_and(version=38 AND NOT dirty)');
 EXECUTE replace(ready,old_hash,md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)));
END $fuel_compatibility$;

-- Consent is never backfilled. Absence means off. Each new consent interval is
-- a replay cutoff; turning a category off removes its former activation date.
CREATE TABLE core.notification_preferences(
 household_id uuid NOT NULL,user_id uuid NOT NULL,
 category text NOT NULL CHECK(category IN('chores_due','chores_digest','chores_escalation','cooking_timer','shopping','pantry','fuel','expenses','meters','documents','mealplan','security','test')),
 enabled boolean NOT NULL DEFAULT false,
 timezone text NOT NULL DEFAULT 'Europe/Vienna' CHECK(length(timezone) BETWEEN 1 AND 100 AND timezone<>'Local'),
 quiet_start integer,quiet_end integer,
 preview_policy text NOT NULL DEFAULT 'generic' CHECK(preview_policy='generic'),
 version bigint NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
 enabled_since timestamptz,updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(household_id,user_id,category),
 FOREIGN KEY(household_id,user_id) REFERENCES core.memberships(household_id,user_id) ON DELETE CASCADE,
 CHECK((quiet_start IS NULL AND quiet_end IS NULL) OR
  (quiet_start IS NOT NULL AND quiet_end IS NOT NULL AND quiet_start BETWEEN 0 AND 1439 AND quiet_end BETWEEN 0 AND 1439 AND quiet_start<>quiet_end)),
 CHECK(enabled=(enabled_since IS NOT NULL)),
 CHECK(enabled_since IS NULL OR isfinite(enabled_since))
);

-- Composite identity keys prevent a reference to another user's subscription
-- or a job from another household, even if a caller supplies a valid UUID.
ALTER TABLE core.push_subscriptions ADD CONSTRAINT push_subscriptions_notification_owner UNIQUE(household_id,user_id,id);
ALTER TABLE core.jobs ADD CONSTRAINT jobs_notification_household UNIQUE(household_id,id);
CREATE TABLE core.notification_device_preferences(
 subscription_id uuid NOT NULL,household_id uuid NOT NULL,user_id uuid NOT NULL,
 category text NOT NULL CHECK(category IN('chores_due','chores_digest','chores_escalation','cooking_timer','shopping','pantry','fuel','expenses','meters','documents','mealplan','security','test')),
 enabled boolean NOT NULL DEFAULT false,paused boolean NOT NULL DEFAULT false,
 name text NOT NULL DEFAULT '' CHECK(length(name)<=80 AND name !~ '[[:cntrl:]]'),
 version bigint NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
 enabled_since timestamptz,updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(subscription_id,category),
 FOREIGN KEY(household_id,user_id,subscription_id) REFERENCES core.push_subscriptions(household_id,user_id,id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,user_id) REFERENCES core.memberships(household_id,user_id) ON DELETE CASCADE,
 CHECK(enabled=(enabled_since IS NOT NULL)),
 CHECK(enabled_since IS NULL OR isfinite(enabled_since))
);
CREATE INDEX notification_device_preferences_account ON core.notification_device_preferences(household_id,user_id,category,subscription_id);

CREATE TABLE core.notification_intents(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL,user_id uuid NOT NULL,
 module_id text,module_generation bigint NOT NULL,
 resource_type text NOT NULL CHECK(resource_type ~ '^[A-Za-z0-9_:./-]{1,64}$'),resource_id uuid NOT NULL,
 source_revision bigint NOT NULL CHECK(source_revision BETWEEN 1 AND 9007199254740991),
 occurrence text NOT NULL CHECK(occurrence ~ '^[A-Za-z0-9_:./-]{1,160}$'),
 category text NOT NULL CHECK(category IN('chores_due','chores_digest','chores_escalation','cooking_timer','shopping','pantry','fuel','expenses','meters','documents','mealplan','security','test')),
 event_at timestamptz NOT NULL,not_before timestamptz NOT NULL,expires_at timestamptz NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','accepted','cancelled','expired','failed')),
 dedupe_key text NOT NULL UNIQUE CHECK(dedupe_key ~ '^notification-v1/[0-9a-f]{64}$'),
 schema_version integer NOT NULL DEFAULT 1 CHECK(schema_version=1),
 version bigint NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,user_id,id),
 FOREIGN KEY(household_id,user_id) REFERENCES core.memberships(household_id,user_id) ON DELETE CASCADE,
 CHECK((category IN('security','test') AND module_id IS NULL AND module_generation=0) OR
  (category NOT IN('security','test') AND module_id IS NOT NULL AND module_generation BETWEEN 1 AND 9007199254740991 AND
   module_id=CASE WHEN category IN('chores_due','chores_digest','chores_escalation') THEN 'chores'
    WHEN category='cooking_timer' THEN 'recipes' ELSE category END)),
 CHECK(event_at>='2000-01-01 00:00:00+00'::timestamptz AND isfinite(event_at) AND isfinite(not_before) AND isfinite(expires_at)
  AND not_before>=event_at AND expires_at>not_before AND expires_at<'10000-01-01 00:00:00+00'::timestamptz),
 CHECK(expires_at-not_before<=CASE WHEN category IN('cooking_timer','test') THEN interval '60 seconds'
  WHEN category IN('chores_due','chores_escalation','security') THEN interval '1 hour'
  WHEN category IN('chores_digest','mealplan') THEN interval '4 hours' ELSE interval '24 hours' END)
);
CREATE INDEX notification_intents_due ON core.notification_intents(not_before,id) WHERE state='pending';
CREATE INDEX notification_intents_source ON core.notification_intents(household_id,module_id,resource_type,resource_id,source_revision) WHERE state='pending';
CREATE INDEX notification_intents_recipient ON core.notification_intents(household_id,user_id,category,created_at DESC);
CREATE INDEX notification_intents_retention ON core.notification_intents(updated_at,id) WHERE state<>'pending';

CREATE TABLE core.notification_deliveries(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL,user_id uuid NOT NULL,
 intent_id uuid NOT NULL,subscription_id uuid NOT NULL,job_id uuid UNIQUE,
 preference_version bigint NOT NULL CHECK(preference_version BETWEEN 1 AND 9007199254740991),
 device_version bigint NOT NULL CHECK(device_version BETWEEN 1 AND 9007199254740991),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','accepted','cancelled','expired','failed')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
 safe_code text NOT NULL DEFAULT '' CHECK(safe_code ~ '^[a-z0-9_]{0,64}$'),
 last_attempt_at timestamptz,accepted_at timestamptz,finished_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(intent_id,subscription_id),
 FOREIGN KEY(household_id,user_id,intent_id) REFERENCES core.notification_intents(household_id,user_id,id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,user_id,subscription_id) REFERENCES core.push_subscriptions(household_id,user_id,id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,job_id) REFERENCES core.jobs(household_id,id) ON DELETE SET NULL (job_id)
);
CREATE INDEX notification_deliveries_subscription ON core.notification_deliveries(subscription_id,state);
CREATE INDEX notification_deliveries_retention ON core.notification_deliveries(updated_at,id) WHERE state<>'pending';

GRANT SELECT,INSERT,UPDATE,DELETE ON core.notification_preferences,core.notification_device_preferences,
 core.notification_intents,core.notification_deliveries TO household_app;
INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('core','notification_preferences',ARRAY['household_id','user_id','category'],ARRAY['user_id','category','enabled','timezone','quiet_start','quiet_end','preview_policy'],NULL),
 ('core','notification_device_preferences',ARRAY['subscription_id','category'],ARRAY['user_id','category','enabled','paused'],NULL);

-- Two scalar-only consent audit policies; operational intents/deliveries are
-- retained separately and contain no message body, credentials or provider URL.
-- Reconstruct every accepted Core37 function before advancing its exact pin.
DO $audit_compatibility$
DECLARE snapshot jsonb;baseline jsonb;original_catalog jsonb;old_catalog jsonb;new_catalog jsonb;expected_catalog jsonb;
 old_producer text;new_producer text;old_finance text;old_audit text;new_audit text;old_pantry text;
 original_audit text;item text;expected text;actual text;needle text;
BEGIN
 snapshot=documents.predecessor_catalog();baseline=snapshot->'functions';original_catalog=snapshot->'finance_catalog';
 old_producer=replace(baseline->'core.finance_producers_ready()'->>'definition',
  '(SELECT count(*) FROM core.audit_policies)=152','(SELECT count(*) FROM core.audit_policies)=163');
 SELECT jsonb_set(original_catalog,'{functions}',jsonb_agg(CASE WHEN value->>0='core.finance_producers_ready()'
  THEN jsonb_set(value,'{1}',to_jsonb(old_producer)) ELSE value END ORDER BY ordinal)) INTO old_catalog
 FROM jsonb_array_elements(original_catalog->'functions') WITH ORDINALITY entries(value,ordinal);
 old_finance=replace(baseline->'core.finance_ready()'->>'definition',quote_literal(original_catalog::text)||'::jsonb',quote_literal(old_catalog::text)||'::jsonb');
 original_audit=baseline->'core.audit_ready()'->>'definition';
 old_audit=replace(original_audit,'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND count(*)=152',
  'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND documents.expansion_ready() AND count(*)=163');
 old_audit=replace(old_audit,'''auth_challenges'',''rate_limit_buckets''','''auth_challenges'',''passkey_credentials'',''rate_limit_buckets''');
 old_pantry=replace(baseline->'pantry.prepared_capture_ready()'->>'definition',md5(original_audit),md5(old_audit));
 IF old_catalog IS NULL OR old_producer IS NULL OR old_finance IS NULL OR old_audit IS NULL OR old_pantry IS NULL
  OR core.finance_catalog() IS DISTINCT FROM old_catalog
  OR position(quote_literal(old_catalog::text)||'::jsonb' in old_finance)=0
  OR position(quote_literal(pantry.prepared_capture_catalog()::text)||'::jsonb' in old_pantry)=0
  OR position(quote_literal(md5(pg_get_functiondef('pantry.prepared_capture_catalog()'::regprocedure))) in old_pantry)=0 THEN
  RAISE EXCEPTION 'Notifications audit catalogs differ from their frozen predecessors';END IF;
 FOREACH item IN ARRAY ARRAY['core.finance_producers_ready()','core.finance_ready()','core.audit_ready()','pantry.prepared_capture_ready()'] LOOP
  expected=CASE item WHEN 'core.finance_producers_ready()' THEN old_producer WHEN 'core.finance_ready()' THEN old_finance
   WHEN 'core.audit_ready()' THEN old_audit ELSE old_pantry END;actual=NULL;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=baseline->item->>'owner' AND p.proacl::text IS NOT DISTINCT FROM baseline->item->>'acl';
  IF actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'unreviewed Notifications audit predecessor: %',item;END IF;
 END LOOP;
 needle='(SELECT count(*) FROM core.audit_policies)=163';
 IF (length(old_producer)-length(replace(old_producer,needle,'')))/length(needle)<>1 THEN
  RAISE EXCEPTION 'Notifications Finance producer count differs';END IF;
 new_producer=replace(old_producer,needle,'(SELECT count(*) FROM core.audit_policies)=165');
 EXECUTE new_producer;
 new_catalog=core.finance_catalog();
 SELECT jsonb_set(old_catalog,'{functions}',jsonb_agg(CASE WHEN value->>0='core.finance_producers_ready()'
  THEN jsonb_set(value,'{1}',to_jsonb(new_producer)) ELSE value END ORDER BY ordinal)) INTO expected_catalog
 FROM jsonb_array_elements(old_catalog->'functions') WITH ORDINALITY entries(value,ordinal);
 IF new_catalog IS DISTINCT FROM expected_catalog THEN RAISE EXCEPTION 'Notifications changed unrelated Finance metadata';END IF;
 EXECUTE replace(old_finance,quote_literal(old_catalog::text)||'::jsonb',quote_literal(new_catalog::text)||'::jsonb');
 new_audit=replace(old_audit,'documents.expansion_ready() AND count(*)=163','documents.expansion_ready() AND count(*)=165');
 needle='''jobs'',''outbox_events''';
 IF (length(new_audit)-length(replace(new_audit,needle,'')))/length(needle)<>1 THEN
  RAISE EXCEPTION 'Notifications operational table classification differs';END IF;
 new_audit=replace(new_audit,needle,'''jobs'',''notification_intents'',''notification_deliveries'',''outbox_events''');
 EXECUTE new_audit;
 EXECUTE replace(old_pantry,md5(old_audit),md5(new_audit));
END $audit_compatibility$;

-- Mealplan's Core-side policy contract pins the global audit count as well.
-- Preserve its five exact domain policies and advance only that reviewed count.
DO $planning_compatibility$
DECLARE baseline jsonb;expected text;actual text;needle text;
BEGIN
 baseline=documents.predecessor_catalog()->'functions';
 expected=baseline->'core.planning_extensions_ready()'->>'definition';
 needle='(SELECT count(*) FROM core.audit_policies)=152';
 IF expected IS NULL OR (length(expected)-length(replace(expected,needle,'')))/length(needle)<>1 THEN
  RAISE EXCEPTION 'Notifications planning catalog differs from its frozen predecessor';END IF;
 expected=replace(expected,needle,'(SELECT count(*) FROM core.audit_policies)=163');
 SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid='core.planning_extensions_ready()'::regprocedure
  AND p.proowner::regrole::text=baseline->'core.planning_extensions_ready()'->>'owner'
  AND p.proacl::text IS NOT DISTINCT FROM baseline->'core.planning_extensions_ready()'->>'acl';
 IF actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'unreviewed Notifications planning predecessor';END IF;
 EXECUTE replace(expected,'(SELECT count(*) FROM core.audit_policies)=163','(SELECT count(*) FROM core.audit_policies)=165');
END $planning_compatibility$;

SELECT core.install_audit_triggers();
DO $ready$
BEGIN
 IF NOT core.audit_ready() OR NOT core.finance_ready() OR NOT pantry.prepared_capture_ready()
  OR NOT core.planning_extensions_ready() OR NOT mealplan.expansion_ready()
  OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
  RAISE EXCEPTION 'incomplete Notifications audit/Finance/Pantry compatibility';END IF;
END $ready$;

-- Canonical deparsing keeps the catalog stable across pg_dump/pg_restore.
DO $canonical$
DECLARE item record;
BEGIN
 FOR item IN SELECT c.conrelid::regclass relation,c.conname,pg_get_constraintdef(c.oid) definition FROM pg_constraint c
  WHERE c.contype='c' AND c.conrelid IN('core.notification_preferences'::regclass,'core.notification_device_preferences'::regclass,
   'core.notification_intents'::regclass,'core.notification_deliveries'::regclass) ORDER BY c.conrelid,c.conname LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',item.relation,item.conname);
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s',item.relation,item.conname,item.definition);
 END LOOP;
END $canonical$;

CREATE FUNCTION core.notification_foundation_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 WITH relations AS(SELECT c.oid,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='core'
  AND c.relname IN('notification_preferences','notification_device_preferences','notification_intents','notification_deliveries'))
 SELECT jsonb_build_object(
 'relations',(SELECT jsonb_agg(jsonb_build_array(r.relname,c.relowner::regrole::text,c.relkind,c.relpersistence,c.relrowsecurity,c.relforcerowsecurity,c.relacl::text) ORDER BY r.relname)
  FROM relations r JOIN pg_class c ON c.oid=r.oid),
 'columns',(SELECT jsonb_agg(jsonb_build_array(r.relname,a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attidentity,a.attgenerated,coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY r.relname,a.attnum)
  FROM relations r JOIN pg_attribute a ON a.attrelid=r.oid LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attnum>0 AND NOT a.attisdropped),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(r.relname,c.conname,pg_get_constraintdef(c.oid),c.convalidated) ORDER BY r.relname,c.conname)
  FROM relations r JOIN pg_constraint c ON c.conrelid=r.oid),
 'indexes',(SELECT jsonb_agg(jsonb_build_array(r.relname,pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY r.relname,i.indexrelid::regclass::text)
  FROM relations r JOIN pg_index i ON i.indrelid=r.oid),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(r.relname,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled) ORDER BY r.relname,t.tgname)
  FROM relations r JOIN pg_trigger t ON t.tgrelid=r.oid WHERE NOT t.tgisinternal),
 'ownership_keys',(SELECT jsonb_agg(jsonb_build_array(c.conrelid::regclass::text,c.conname,pg_get_constraintdef(c.oid),c.convalidated) ORDER BY c.conname)
  FROM pg_constraint c WHERE c.conname IN('push_subscriptions_notification_owner','jobs_notification_household')
   AND c.conrelid IN('core.push_subscriptions'::regclass,'core.jobs'::regclass)),
 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY p.table_name) FROM core.audit_policies p WHERE p.schema_name='core'
  AND p.table_name IN('notification_preferences','notification_device_preferences')))
$$;
REVOKE ALL ON FUNCTION core.notification_foundation_catalog() FROM PUBLIC,household_app;
DO $notification_ready$
DECLARE expected jsonb;catalog_hash text;
BEGIN
 expected=core.notification_foundation_catalog();
 catalog_hash=md5(pg_get_functiondef('core.notification_foundation_catalog()'::regprocedure));
 EXECUTE format($definition$CREATE FUNCTION core.notification_foundation_ready() RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT core.notification_foundation_catalog()=%L::jsonb
 AND md5(pg_get_functiondef('core.notification_foundation_catalog()'::regprocedure))=%L
 AND EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='core.notification_foundation_catalog()'::regprocedure
  AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']
  AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
   WHERE a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE'))
 $body$$definition$,expected,catalog_hash);
END $notification_ready$;
REVOKE ALL ON FUNCTION core.notification_foundation_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.notification_foundation_ready() TO household_app;
COMMIT;
