BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

-- These four protocol stores already exist. No ciphertext, credential counter,
-- consent, expiry, business version or semantic audit event is rewritten.
CREATE FUNCTION core.crypto_envelopes_catalog() RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
 'relations',(SELECT jsonb_agg(jsonb_build_array(c.relname,c.relkind::text,c.relpersistence::text,c.relowner::regrole::text,
  c.relacl::text,c.relrowsecurity,c.relforcerowsecurity,c.relispartition,
  EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)) ORDER BY c.relname)
  FROM pg_class c WHERE c.oid IN('core.totp_credentials'::regclass,'core.passkey_credentials'::regclass,'core.auth_challenges'::regclass,'core.push_subscriptions'::regclass)),
 'columns',(SELECT jsonb_agg(jsonb_build_array(c.relname,a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attidentity,a.attgenerated,
  a.attacl::text,a.attcollation::regcollation::text,coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY c.relname,a.attnum)
  FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
  LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
  WHERE c.oid IN('core.totp_credentials'::regclass,'core.passkey_credentials'::regclass,'core.auth_challenges'::regclass,'core.push_subscriptions'::regclass)),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(c.relname,k.conname,pg_get_constraintdef(k.oid),k.convalidated,k.conislocal,k.coninhcount,k.connoinherit)
  ORDER BY c.relname,k.conname) FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid
  WHERE c.oid IN('core.totp_credentials'::regclass,'core.passkey_credentials'::regclass,'core.auth_challenges'::regclass,'core.push_subscriptions'::regclass)),
 'indexes',(SELECT jsonb_agg(jsonb_build_array(c.relname,pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready,i.indislive)
  ORDER BY c.relname,pg_get_indexdef(i.indexrelid)) FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid
  WHERE c.oid IN('core.totp_credentials'::regclass,'core.passkey_credentials'::regclass,'core.auth_challenges'::regclass,'core.push_subscriptions'::regclass)),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(c.relname,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled::text,t.tgdeferrable,t.tginitdeferred)
  ORDER BY c.relname,t.tgname) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid
  WHERE NOT t.tgisinternal AND c.oid IN('core.totp_credentials'::regclass,'core.passkey_credentials'::regclass,'core.auth_challenges'::regclass,'core.push_subscriptions'::regclass)),
 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY schema_name,table_name) FROM core.audit_policies p))
$$;
REVOKE ALL ON FUNCTION core.crypto_envelopes_catalog() FROM PUBLIC,household_app;
CREATE TEMP TABLE crypto_envelopes_before(snapshot jsonb NOT NULL) ON COMMIT DROP;
REVOKE ALL ON crypto_envelopes_before FROM PUBLIC,household_app;
DO $predecessor$
DECLARE baseline jsonb;reviewed_catalog jsonb;authority text;overview text;ready text;needle text;old_hash text;item text;expected text;actual text;snapshot jsonb;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=12 AND NOT dirty) FROM fuel.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=6 AND NOT dirty) FROM expenses.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=3 AND NOT dirty) FROM meters.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=3 AND NOT dirty) FROM documents.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=13 AND NOT dirty) FROM mealplan.schema_migrations)
 OR NOT core.audit_ready() OR NOT core.notification_foundation_ready() OR NOT core.notification_financial_ready()
 OR NOT mealplan.notification_ready() OR NOT documents.notification_ready() OR NOT meters.notification_ready()
 OR NOT expenses.notification_ready() OR NOT pantry.notification_observations_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
  RAISE EXCEPTION 'Crypto metadata requires accepted Core39/Expenses6/Meters3/Documents3/Mealplan13';END IF;
 -- Core is dirty40 during migration. Reconstruct all three exact Core39 Fuel
 -- definitions from their frozen Core38 evidence rather than bypass guards.
 baseline=core.notification_financial_predecessor_catalog();
 authority=baseline->'functions'->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 overview=baseline->'functions'->'fuel.overview_ready()'->>'definition';
 ready=baseline->'functions'->'fuel.module_ready()'->>'definition';
 needle='bool_and(version=38 AND NOT dirty)';old_hash=md5(authority);
 IF authority IS NULL OR overview IS NULL OR ready IS NULL
 OR (length(authority)-length(replace(authority,needle,'')))/length(needle)<>1
 OR (length(overview)-length(replace(overview,needle,'')))/length(needle)<>1
 OR (length(ready)-length(replace(ready,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'Unreviewed crypto Fuel predecessor catalog';END IF;
 authority=replace(authority,needle,'bool_and(version=39 AND NOT dirty)');
 overview=replace(overview,needle,'bool_and(version=39 AND NOT dirty)');
 ready=replace(ready,old_hash,md5(authority));
 -- Fuel's catalog also pins the authority body hash. Core39 changed exactly
 -- that entry; comparing its untouched Core38 snapshot would reject every
 -- valid installation. Reconstruct that single frozen entry, not live data.
 IF(SELECT count(*) FROM jsonb_array_elements(baseline->'fuel_catalog'->'functions') x(value)
  WHERE value->>0='module_authorize' AND value->>2=old_hash)<>1 THEN
  RAISE EXCEPTION 'Unexpected frozen crypto Fuel authority hash census';END IF;
 SELECT jsonb_set(baseline->'fuel_catalog','{functions}',jsonb_agg(
  CASE WHEN value->>0='module_authorize' AND value->>2=old_hash
   THEN jsonb_set(value,'{2}',to_jsonb(md5(authority))) ELSE value END ORDER BY ordinal)) INTO reviewed_catalog
 FROM jsonb_array_elements(baseline->'fuel_catalog'->'functions') WITH ORDINALITY x(value,ordinal);
 IF reviewed_catalog IS DISTINCT FROM fuel.module_catalog()
 OR position(quote_literal(reviewed_catalog::text)||'::jsonb' in ready)=0 THEN
  RAISE EXCEPTION 'Unreviewed reconstructed Core39 Fuel catalog';END IF;
 FOREACH item IN ARRAY ARRAY['fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)','fuel.overview_ready()','fuel.module_ready()'] LOOP
  expected=CASE item WHEN 'fuel.module_ready()' THEN ready WHEN 'fuel.overview_ready()' THEN overview ELSE authority END;
  actual=NULL;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=baseline->'functions'->item->>'owner'
   AND p.proacl::text IS NOT DISTINCT FROM baseline->'functions'->item->>'acl';
  IF actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'Unreviewed crypto Fuel predecessor: %',item;END IF;
 END LOOP;
 IF EXISTS(SELECT 1 FROM pg_class c WHERE c.oid IN('core.totp_credentials'::regclass,'core.passkey_credentials'::regclass,
  'core.auth_challenges'::regclass,'core.push_subscriptions'::regclass)
  AND(c.relowner<>'household_owner'::regrole OR c.relkind<>'r' OR c.relpersistence<>'p' OR c.relrowsecurity OR c.relforcerowsecurity
   OR c.relispartition OR has_table_privilege('household_app',c.oid,'TRUNCATE,TRIGGER,REFERENCES')
   OR EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid))) THEN
  RAISE EXCEPTION 'Unreviewed crypto protocol table ownership or privilege';END IF;
 SELECT jsonb_build_object('crypto_catalog',core.crypto_envelopes_catalog(),'fuel_catalog',fuel.module_catalog(),
  'functions',jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
   'owner',p.proowner::regrole::text,'acl',p.proacl::text))) INTO snapshot FROM pg_proc p WHERE p.oid IN(
  'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure,'fuel.overview_ready()'::regprocedure,'fuel.module_ready()'::regprocedure,
  'core.audit_ready()'::regprocedure,'pantry.prepared_capture_ready()'::regprocedure,'pantry.notification_observations_ready()'::regprocedure);
 INSERT INTO crypto_envelopes_before VALUES(snapshot);
 EXECUTE format('CREATE FUNCTION core.crypto_envelopes_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION core.crypto_envelopes_predecessor_catalog() FROM PUBLIC,household_app;

-- Constant defaults backfill without firing business/audit row updates. They
-- are removed immediately: every new protected writer chooses its format.
ALTER TABLE core.totp_credentials ADD COLUMN encryption_format smallint NOT NULL DEFAULT 1;
ALTER TABLE core.totp_credentials ALTER COLUMN encryption_format DROP DEFAULT;
ALTER TABLE core.passkey_credentials ADD COLUMN encryption_format smallint NOT NULL DEFAULT 1,
 ADD COLUMN encryption_key_id text NOT NULL DEFAULT 'v1';
ALTER TABLE core.passkey_credentials ALTER COLUMN encryption_format DROP DEFAULT,ALTER COLUMN encryption_key_id DROP DEFAULT;
ALTER TABLE core.push_subscriptions ADD COLUMN encryption_format smallint NOT NULL DEFAULT 1;
ALTER TABLE core.push_subscriptions ALTER COLUMN encryption_format DROP DEFAULT;
ALTER TABLE core.auth_challenges ADD COLUMN encryption_format smallint,ADD COLUMN encryption_key_id text;
UPDATE core.auth_challenges SET encryption_format=1,encryption_key_id='v1'
 WHERE purpose IN('passkey-login','passkey-register','passkey-reauth');

ALTER TABLE core.totp_credentials ADD CONSTRAINT totp_credentials_encryption_envelope CHECK(
 encryption_format IN(1,2) AND(encryption_format=1 OR(encryption_key_id ~ '^[0-9a-f]{64}$' AND octet_length(encrypted_secret) BETWEEN 29 AND 4096)));
ALTER TABLE core.passkey_credentials ADD CONSTRAINT passkey_credentials_encryption_envelope CHECK(
 encryption_format IN(1,2) AND(encryption_format=1 OR(encryption_key_id ~ '^[0-9a-f]{64}$' AND octet_length(encrypted_credential) BETWEEN 29 AND 131072)));
ALTER TABLE core.push_subscriptions ADD CONSTRAINT push_subscriptions_encryption_envelope CHECK(
 encryption_format IN(1,2) AND(encryption_format=1 OR(encryption_key_id ~ '^[0-9a-f]{64}$' AND octet_length(encrypted_subscription) BETWEEN 29 AND 16384)));
ALTER TABLE core.auth_challenges ADD CONSTRAINT auth_challenges_encryption_envelope CHECK(
 (purpose IN('passkey-login','passkey-register','passkey-reauth') AND encryption_format IS NOT NULL AND encryption_key_id IS NOT NULL
  AND encryption_format IN(1,2) AND(encryption_format=1 OR(encryption_key_id ~ '^[0-9a-f]{64}$'
   AND(purpose='passkey-login' OR user_id IS NOT NULL)
   AND jsonb_typeof(payload)='object' AND payload ? 'sealed' AND payload-'sealed'='{}'::jsonb
   AND jsonb_typeof(payload->'sealed')='string' AND length(payload->>'sealed') BETWEEN 40 AND 174764
   AND payload->>'sealed' ~ '^[A-Za-z0-9+/]+={0,2}$'
   AND octet_length(decode(payload->>'sealed','base64')) BETWEEN 29 AND 131072)))
 OR(purpose NOT IN('passkey-login','passkey-register','passkey-reauth') AND encryption_format IS NULL AND encryption_key_id IS NULL));
CREATE INDEX totp_credentials_encryption_rotation ON core.totp_credentials(encryption_format,encryption_key_id,user_id);
CREATE INDEX passkey_credentials_encryption_rotation ON core.passkey_credentials(encryption_format,encryption_key_id,id);
CREATE INDEX push_subscriptions_encryption_rotation ON core.push_subscriptions(encryption_format,encryption_key_id,id);
CREATE INDEX auth_challenges_encryption_rotation ON core.auth_challenges(encryption_format,encryption_key_id,id)
 WHERE purpose IN('passkey-login','passkey-register','passkey-reauth');

-- Canonical deparse makes exact predecessor reconstruction independent of
-- implicit casts retained by the original CHECK parser representation.
DO $canonical$
DECLARE item record;
BEGIN
 FOR item IN SELECT conrelid::regclass relation,conname,pg_get_constraintdef(oid) definition FROM pg_constraint
 WHERE conname IN('totp_credentials_encryption_envelope','passkey_credentials_encryption_envelope',
  'push_subscriptions_encryption_envelope','auth_challenges_encryption_envelope')
 AND connamespace='core'::regnamespace ORDER BY conname LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',item.relation,item.conname);
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s',item.relation,item.conname,item.definition);
 END LOOP;
END $canonical$;
DO $compatibility$
DECLARE old jsonb;new_catalog jsonb;key text;filtered jsonb;item text;actual text;authority text;overview text;ready text;needle text;old_hash text;
BEGIN
 SELECT snapshot INTO STRICT old FROM crypto_envelopes_before;
 new_catalog=core.crypto_envelopes_catalog();
 FOREACH key IN ARRAY ARRAY['relations','columns','constraints','indexes','triggers','policies'] LOOP
  SELECT coalesce(jsonb_agg(value ORDER BY ordinal),'[]') INTO filtered
  FROM jsonb_array_elements(new_catalog->key) WITH ORDINALITY x(value,ordinal)
  WHERE NOT(key='columns' AND(value->>1='encryption_format' OR(value->>0 IN('passkey_credentials','auth_challenges') AND value->>1='encryption_key_id')))
   AND NOT(key='constraints' AND value->>1 IN('totp_credentials_encryption_envelope','passkey_credentials_encryption_envelope','push_subscriptions_encryption_envelope','auth_challenges_encryption_envelope'))
   AND NOT(key='constraints' AND EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=c.conkey[1]
    WHERE c.contype='n' AND c.convalidated AND c.conkey=ARRAY[a.attnum] AND c.conrelid=to_regclass('core.'||(value->>0))
     AND(a.attname='encryption_format' OR(value->>0='passkey_credentials' AND a.attname='encryption_key_id'))
     AND value->>1=c.conname AND value->>2=pg_get_constraintdef(c.oid) AND(value->>3)::boolean=c.convalidated))
   AND NOT(key='indexes' AND value->>1 IN(SELECT pg_get_indexdef(i.indexrelid) FROM pg_index i WHERE i.indexrelid IN(
    'core.totp_credentials_encryption_rotation'::regclass,'core.passkey_credentials_encryption_rotation'::regclass,
    'core.push_subscriptions_encryption_rotation'::regclass,'core.auth_challenges_encryption_rotation'::regclass)));
  IF filtered IS DISTINCT FROM coalesce(old->'crypto_catalog'->key,'[]') THEN RAISE EXCEPTION 'Unrelated crypto metadata changed: %',key;END IF;
 END LOOP;
 FOREACH item IN ARRAY ARRAY['core.audit_ready()','pantry.prepared_capture_ready()','pantry.notification_observations_ready()'] LOOP
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=old->'functions'->item->>'owner' AND p.proacl::text IS NOT DISTINCT FROM old->'functions'->item->>'acl';
  IF actual IS DISTINCT FROM old->'functions'->item->>'definition' THEN RAISE EXCEPTION 'Crypto migration changed semantic audit contract';END IF;
 END LOOP;
 authority=old->'functions'->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 overview=old->'functions'->'fuel.overview_ready()'->>'definition';ready=old->'functions'->'fuel.module_ready()'->>'definition';
 needle='bool_and(version=39 AND NOT dirty)';old_hash=md5(authority);
 EXECUTE replace(authority,needle,'bool_and(version=40 AND NOT dirty)');
 EXECUTE replace(overview,needle,'bool_and(version=40 AND NOT dirty)');
 EXECUTE replace(ready,old_hash,md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)));
END $compatibility$;
DO $ready$
DECLARE expected jsonb;catalog_hash text;predecessor_hash text;
BEGIN
 expected=core.crypto_envelopes_catalog();catalog_hash=md5(pg_get_functiondef('core.crypto_envelopes_catalog()'::regprocedure));
 predecessor_hash=md5(pg_get_functiondef('core.crypto_envelopes_predecessor_catalog()'::regprocedure));
 EXECUTE format($definition$CREATE FUNCTION core.crypto_envelopes_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT core.crypto_envelopes_catalog()=%L::jsonb
 AND md5(pg_get_functiondef('core.crypto_envelopes_catalog()'::regprocedure))=%L
 AND md5(pg_get_functiondef('core.crypto_envelopes_predecessor_catalog()'::regprocedure))=%L
 AND NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid IN('core.crypto_envelopes_catalog()'::regprocedure,'core.crypto_envelopes_predecessor_catalog()'::regprocedure)
  AND(p.proowner<>'household_owner'::regrole OR NOT p.prosecdef OR p.proconfig IS DISTINCT FROM ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
  WHERE p.oid IN('core.crypto_envelopes_catalog()'::regprocedure,'core.crypto_envelopes_predecessor_catalog()'::regprocedure)
   AND(a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE'))
 $body$$definition$,expected,catalog_hash,predecessor_hash);
END $ready$;
REVOKE ALL ON FUNCTION core.crypto_envelopes_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.crypto_envelopes_ready() TO household_app;
-- Personal security events use the existing transactional inbox. Ordinary
-- module notifications retain their behavior; event content is never stored.
CREATE FUNCTION core.security_events_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF TG_TABLE_SCHEMA<>'core' OR TG_TABLE_NAME<>'notifications' OR TG_OP NOT IN('INSERT','UPDATE') THEN
  RAISE EXCEPTION 'Invalid security event trigger' USING ERRCODE='42501';END IF;
 IF TG_OP='UPDATE' AND left(OLD.kind,9)='security_' THEN
  IF (to_jsonb(NEW)-ARRAY['read_at','dismissed_at']) IS DISTINCT FROM(to_jsonb(OLD)-ARRAY['read_at','dismissed_at']) THEN
   RAISE EXCEPTION 'Security event identity is immutable' USING ERRCODE='23514';END IF;
  RETURN NEW;
 END IF;
 IF left(NEW.kind,9)<>'security_' THEN RETURN NEW;END IF;
 IF TG_OP<>'INSERT' OR NEW.kind NOT IN('security_password_changed','security_totp_enabled','security_totp_disabled',
  'security_recovery_codes_regenerated','security_recovery_code_used','security_passkey_added','security_passkey_removed',
  'security_sessions_revoked','security_account_changed','security_device_authorized')
 OR NEW.module_id IS NOT NULL OR NEW.title<>'Sicherheitsereignis' OR NEW.target_path IS DISTINCT FROM '/settings/security'
 OR NEW.id='00000000-0000-0000-0000-000000000000'::uuid
 OR NEW.dedupe_key IS DISTINCT FROM 'security-event/'||NEW.id::text
 OR NEW.read_at IS NOT NULL OR NEW.dismissed_at IS NOT NULL
 OR NOT EXISTS(SELECT 1 FROM core.memberships m WHERE m.household_id=NEW.household_id AND m.user_id=NEW.user_id) THEN
  RAISE EXCEPTION 'Invalid personal security event' USING ERRCODE='23514';END IF;
 NEW.created_at=clock_timestamp();
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.security_events_guard() FROM PUBLIC,household_app;
CREATE TRIGGER security_events_guard BEFORE INSERT OR UPDATE ON core.notifications FOR EACH ROW EXECUTE FUNCTION core.security_events_guard();
CREATE FUNCTION core.security_events_catalog() RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
 'relation',(SELECT jsonb_build_array(c.relowner::regrole::text,c.relkind::text,c.relpersistence::text,c.relacl::text,
  c.relrowsecurity,c.relforcerowsecurity,c.relispartition,EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid))
  FROM pg_class c WHERE c.oid='core.notifications'::regclass),
 'columns',(SELECT jsonb_agg(jsonb_build_array(a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attidentity,a.attgenerated,a.attacl::text,
  a.attcollation::regcollation::text,coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum)
  FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
  WHERE a.attrelid='core.notifications'::regclass AND a.attnum>0 AND NOT a.attisdropped),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(conname,pg_get_constraintdef(oid),convalidated) ORDER BY conname)
  FROM pg_constraint WHERE conrelid='core.notifications'::regclass),
 'indexes',(SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(indexrelid),indisvalid,indisready,indislive) ORDER BY pg_get_indexdef(indexrelid))
  FROM pg_index WHERE indrelid='core.notifications'::regclass),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(tgname,pg_get_triggerdef(oid),tgenabled::text,tgisinternal) ORDER BY tgname)
  FROM pg_trigger WHERE tgrelid='core.notifications'::regclass AND NOT tgisinternal),
 'guard',(SELECT jsonb_build_array(pg_get_functiondef(p.oid),p.proowner::regrole::text,p.proacl::text)
  FROM pg_proc p WHERE p.oid='core.security_events_guard()'::regprocedure),
 'policy',(SELECT to_jsonb(p) FROM core.audit_policies p WHERE schema_name='core' AND table_name='notifications'))
$$;
REVOKE ALL ON FUNCTION core.security_events_catalog() FROM PUBLIC,household_app;
DO $security_ready$
DECLARE expected jsonb;catalog_hash text;
BEGIN
 expected=core.security_events_catalog();catalog_hash=md5(pg_get_functiondef('core.security_events_catalog()'::regprocedure));
 EXECUTE format($definition$CREATE FUNCTION core.security_events_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT core.security_events_catalog()=%L::jsonb AND md5(pg_get_functiondef('core.security_events_catalog()'::regprocedure))=%L
 AND NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid IN('core.security_events_catalog()'::regprocedure,'core.security_events_guard()'::regprocedure)
  AND(p.proowner<>'household_owner'::regrole OR NOT p.prosecdef OR p.proconfig IS DISTINCT FROM ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
  WHERE p.oid IN('core.security_events_catalog()'::regprocedure,'core.security_events_guard()'::regprocedure)
   AND(a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE'))
 $body$$definition$,expected,catalog_hash);
END $security_ready$;
REVOKE ALL ON FUNCTION core.security_events_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.security_events_ready() TO household_app;
DO $complete$
BEGIN
 IF NOT core.crypto_envelopes_ready() OR NOT core.security_events_ready() OR NOT core.audit_ready() OR NOT core.notification_foundation_ready()
 OR NOT core.notification_financial_ready() OR NOT mealplan.notification_ready() OR NOT documents.notification_ready()
 OR NOT meters.notification_ready() OR NOT expenses.notification_ready() OR NOT pantry.notification_observations_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN RAISE EXCEPTION 'Incomplete crypto envelope metadata compatibility';END IF;
END $complete$;
COMMIT;
