BEGIN;
-- Downloads of the whole household's records and of the audit history are
-- recorded as semantic audit events, so administrators see who took a copy.

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
