BEGIN;
-- Account security policy: administrators can require a second factor for
-- administrators and editors, and every account is told about sign-ins from
-- a browser and system it has not used before.

-- Freeze the exact Core42 definitions this migration replaces, so disposable
-- upgrade fixtures can reconstruct the predecessor instead of bypassing guards.
DO $predecessor$
DECLARE snapshot jsonb;
BEGIN
 SELECT jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
  'owner',p.proowner::regrole::text,'acl',p.proacl::text)) INTO snapshot FROM pg_proc p WHERE p.oid IN(
  'core.record_audit_event(text,text,uuid,text,jsonb)'::regprocedure,'core.audit_ready()'::regprocedure,
  'core.security_events_guard()'::regprocedure,'core.security_events_ready()'::regprocedure,
  'pantry.prepared_capture_ready()'::regprocedure,'pantry.notification_observations_ready()'::regprocedure,
  'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure,'fuel.overview_ready()'::regprocedure,'fuel.module_ready()'::regprocedure);
 IF (SELECT count(*) FROM jsonb_object_keys(snapshot))<>9 THEN RAISE EXCEPTION 'Unreviewed Core42 security policy predecessor';END IF;
 EXECUTE format('CREATE FUNCTION core.security_policy_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION core.security_policy_predecessor_catalog() FROM PUBLIC,household_app;

-- One policy row per household; a missing row means no requirement.
CREATE TABLE core.security_policies(
 household_id uuid PRIMARY KEY REFERENCES core.households(id) ON DELETE CASCADE,
 require_admin_mfa boolean NOT NULL DEFAULT false,
 require_editor_mfa boolean NOT NULL DEFAULT false,
 grace_days smallint NOT NULL DEFAULT 14 CHECK(grace_days IN(0,7,14,30)),
 enforced_from timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 updated_by uuid REFERENCES core.users(id),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK(NOT require_editor_mfa OR require_admin_mfa)
);
GRANT SELECT,INSERT,UPDATE ON core.security_policies TO household_app;

-- Browser/system fingerprints an account signed in from, stored only as a
-- keyed hash of a coarse label. They expire after 400 days without use.
CREATE TABLE core.known_devices(
 user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
 device_hash bytea NOT NULL CHECK(octet_length(device_hash)=32),
 first_seen timestamptz NOT NULL DEFAULT now(),
 last_seen timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,device_hash)
);
CREATE INDEX known_devices_expiry ON core.known_devices(last_seen);
GRANT SELECT,INSERT,UPDATE,DELETE ON core.known_devices TO household_app;

-- Semantic audit action for policy changes; the body is the Core42 definition
-- with this action added.
DO $audit_event$
DECLARE body text;needle text='''restore_completed'',''restore_failed'')';
BEGIN
 body=pg_get_functiondef('core.record_audit_event(text,text,uuid,text,jsonb)'::regprocedure);
 IF (length(body)-length(replace(body,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'Unreviewed Core42 audit event allowlist';END IF;
 EXECUTE replace(body,needle,'''restore_completed'',''restore_failed'',''security_policy_changed'')');
END $audit_event$;

-- The personal security event for a sign-in from an unfamiliar browser/system.
DO $security_guard$
DECLARE body text;needle text='''security_sessions_revoked'',''security_account_changed'',''security_device_authorized'')';
BEGIN
 body=pg_get_functiondef('core.security_events_guard()'::regprocedure);
 IF (length(body)-length(replace(body,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'Unreviewed Core42 security event guard';END IF;
 EXECUTE replace(body,needle,'''security_sessions_revoked'',''security_account_changed'',''security_device_authorized'',''security_sign_in_new_device'')');
END $security_guard$;
DO $security_ready$
DECLARE expected jsonb;catalog_hash text;
BEGIN
 expected=core.security_events_catalog();catalog_hash=md5(pg_get_functiondef('core.security_events_catalog()'::regprocedure));
 EXECUTE format($definition$CREATE OR REPLACE FUNCTION core.security_events_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT core.security_events_catalog()=%L::jsonb AND md5(pg_get_functiondef('core.security_events_catalog()'::regprocedure))=%L
 AND NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid IN('core.security_events_catalog()'::regprocedure,'core.security_events_guard()'::regprocedure)
  AND(p.proowner<>'household_owner'::regrole OR NOT p.prosecdef OR p.proconfig IS DISTINCT FROM ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
  WHERE p.oid IN('core.security_events_catalog()'::regprocedure,'core.security_events_guard()'::regprocedure)
   AND(a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE'))
 $body$$definition$,expected,catalog_hash);
END $security_ready$;

-- Classify both new tables as operational state, then advance the exact
-- audit, Pantry and Fuel pins that depend on the audit contract or Core version.
DO $compatibility$
DECLARE audit_old text;old_audit_hash text;new_audit_hash text;pantry_old text;pantry_new text;old_pantry_hash text;observations text;
 authority text;overview text;ready text;needle text;old_hash text;
BEGIN
 audit_old=pg_get_functiondef('core.audit_ready()'::regprocedure);
 needle='''jobs'',''backup_jobs'',''backup_settings'',''notification_intents''';
 IF (length(audit_old)-length(replace(audit_old,needle,'')))/length(needle)<>1 THEN
  RAISE EXCEPTION 'Unreviewed Core42 audit classification';END IF;
 old_audit_hash=md5(audit_old);
 EXECUTE replace(audit_old,needle,'''jobs'',''backup_jobs'',''backup_settings'',''known_devices'',''security_policies'',''notification_intents''');
 new_audit_hash=md5(pg_get_functiondef('core.audit_ready()'::regprocedure));

 pantry_old=pg_get_functiondef('pantry.prepared_capture_ready()'::regprocedure);old_pantry_hash=md5(pantry_old);
 IF position(old_audit_hash in pantry_old)=0 THEN RAISE EXCEPTION 'Unreviewed Pantry audit pin';END IF;
 EXECUTE replace(pantry_old,old_audit_hash,new_audit_hash);
 pantry_new=pg_get_functiondef('pantry.prepared_capture_ready()'::regprocedure);

 observations=pg_get_functiondef('pantry.notification_observations_ready()'::regprocedure);
 IF position(old_audit_hash in observations)=0 OR position(old_pantry_hash in observations)=0 THEN
  RAISE EXCEPTION 'Unreviewed Pantry observation pins';END IF;
 EXECUTE replace(replace(observations,old_audit_hash,new_audit_hash),old_pantry_hash,md5(pantry_new));

 needle='bool_and(version=42 AND NOT dirty)';
 authority=pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure);
 overview=pg_get_functiondef('fuel.overview_ready()'::regprocedure);
 ready=pg_get_functiondef('fuel.module_ready()'::regprocedure);
 old_hash=md5(authority);
 IF (length(authority)-length(replace(authority,needle,'')))/length(needle)<>1
 OR (length(overview)-length(replace(overview,needle,'')))/length(needle)<>1
 OR (length(ready)-length(replace(ready,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'Unreviewed Core42 Fuel predecessor contract';END IF;
 EXECUTE replace(authority,needle,'bool_and(version=43 AND NOT dirty)');
 EXECUTE replace(overview,needle,'bool_and(version=43 AND NOT dirty)');
 EXECUTE replace(ready,old_hash,md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)));
END $compatibility$;

DO $ready$
BEGIN
 IF NOT core.audit_ready() OR NOT core.security_events_ready() OR NOT pantry.prepared_capture_ready() OR NOT pantry.notification_observations_ready() THEN
  RAISE EXCEPTION 'incomplete Core43 audit, security event or Pantry compatibility';END IF;
END $ready$;
COMMIT;
