BEGIN;
-- Downloads of the whole household's records and of the audit history are
-- recorded as semantic audit events, so administrators see who took a copy.
-- Sessions record whether their sign-in verified a second factor, so the
-- second-factor policy can tell a passkey or TOTP sign-in from a password-only one.

-- Freeze the exact Core43 definitions this migration replaces, so disposable
-- upgrade fixtures can reconstruct the predecessor instead of bypassing guards.
DO $predecessor$
DECLARE snapshot jsonb;
BEGIN
 SELECT jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
  'owner',p.proowner::regrole::text,'acl',p.proacl::text)) INTO snapshot FROM pg_proc p WHERE p.oid IN(
  'core.record_audit_event(text,text,uuid,text,jsonb)'::regprocedure,
  'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure,'fuel.overview_ready()'::regprocedure,'fuel.module_ready()'::regprocedure);
 IF (SELECT count(*) FROM jsonb_object_keys(snapshot))<>4 THEN RAISE EXCEPTION 'Unreviewed Core43 export audit predecessor';END IF;
 EXECUTE format('CREATE FUNCTION core.export_audit_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION core.export_audit_predecessor_catalog() FROM PUBLIC,household_app;

-- Semantic audit actions for both exports; the body is the Core43 definition
-- with these actions added.
DO $audit_event$
DECLARE body text;needle text='''restore_completed'',''restore_failed'',''security_policy_changed'')';
BEGIN
 body=pg_get_functiondef('core.record_audit_event(text,text,uuid,text,jsonb)'::regprocedure);
 IF (length(body)-length(replace(body,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'Unreviewed Core43 audit event allowlist';END IF;
 EXECUTE replace(body,needle,'''restore_completed'',''restore_failed'',''security_policy_changed'',''household_exported'',''audit_exported'')');
END $audit_event$;

-- Existing sessions of accounts whose TOTP was already active when the session
-- began passed it at sign-in; every other existing session counts as
-- password-only until it is confirmed again with a factor or passkey.
-- The backfill derives a new column from existing facts; it is no change by
-- anyone, so it bypasses the row audit inside this transaction only.
ALTER TABLE core.sessions ADD COLUMN second_factor boolean NOT NULL DEFAULT false;
ALTER TABLE core.sessions DISABLE TRIGGER household_audit_change;
UPDATE core.sessions s SET second_factor=true WHERE EXISTS(SELECT 1 FROM core.totp_credentials t
 WHERE t.user_id=s.user_id AND t.enabled_at IS NOT NULL AND t.enabled_at<=s.created_at);
ALTER TABLE core.sessions ENABLE TRIGGER household_audit_change;

-- Backup work records which repositories it used, so the history of a
-- replaced repository never counts for the configured one. Only the backup
-- service (owner) writes these columns.
ALTER TABLE core.backup_jobs ADD COLUMN local_label text CHECK(length(local_label) BETWEEN 1 AND 512),
 ADD COLUMN offsite_label text CHECK(length(offsite_label) BETWEEN 1 AND 512);

-- Advance the exact Fuel pins on the Core version.
DO $compatibility$
DECLARE authority text;overview text;ready text;needle text='bool_and(version=43 AND NOT dirty)';old_hash text;
BEGIN
 authority=pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure);
 overview=pg_get_functiondef('fuel.overview_ready()'::regprocedure);
 ready=pg_get_functiondef('fuel.module_ready()'::regprocedure);
 old_hash=md5(authority);
 IF (length(authority)-length(replace(authority,needle,'')))/length(needle)<>1
 OR (length(overview)-length(replace(overview,needle,'')))/length(needle)<>1
 OR (length(ready)-length(replace(ready,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'Unreviewed Core43 Fuel predecessor contract';END IF;
 EXECUTE replace(authority,needle,'bool_and(version=44 AND NOT dirty)');
 EXECUTE replace(overview,needle,'bool_and(version=44 AND NOT dirty)');
 EXECUTE replace(ready,old_hash,md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)));
END $compatibility$;

DO $ready$
BEGIN
 IF NOT core.audit_ready() OR NOT core.security_events_ready() OR NOT pantry.prepared_capture_ready() OR NOT pantry.notification_observations_ready() THEN
  RAISE EXCEPTION 'incomplete Core44 audit, security event or Pantry compatibility';END IF;
END $ready$;
COMMIT;
