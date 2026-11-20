BEGIN;
-- Backup operations: administrators schedule and request backups, offsite
-- copies, restore checks and live restores in the app. The web role only
-- records requests and settings; the owner backup service, which alone holds
-- repository credentials, validates and performs them.

-- Freeze the exact Core41 definitions this migration replaces, so disposable
-- upgrade fixtures can reconstruct the predecessor instead of bypassing guards.
DO $predecessor$
DECLARE snapshot jsonb;
BEGIN
 SELECT jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
  'owner',p.proowner::regrole::text,'acl',p.proacl::text)) INTO snapshot FROM pg_proc p WHERE p.oid IN(
  'core.record_audit_event(text,text,uuid,text,jsonb)'::regprocedure,'core.audit_ready()'::regprocedure,
  'pantry.prepared_capture_ready()'::regprocedure,'pantry.notification_observations_ready()'::regprocedure,
  'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure,'fuel.overview_ready()'::regprocedure,'fuel.module_ready()'::regprocedure);
 IF (SELECT count(*) FROM jsonb_object_keys(snapshot))<>7 THEN RAISE EXCEPTION 'Unreviewed Core41 backup operations predecessor';END IF;
 EXECUTE format('CREATE FUNCTION core.backup_operations_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION core.backup_operations_predecessor_catalog() FROM PUBLIC,household_app;

-- Schedule and retention per household. The web role may change only the
-- schedule columns; service_* and label columns are written by the backup service.
CREATE TABLE core.backup_settings(
 household_id uuid PRIMARY KEY REFERENCES core.households(id) ON DELETE CASCADE,
 local_time time NOT NULL DEFAULT '03:00',
 local_interval_hours smallint NOT NULL DEFAULT 24 CHECK(local_interval_hours IN(1,2,3,4,6,8,12,24)),
 keep_daily smallint NOT NULL DEFAULT 7 CHECK(keep_daily BETWEEN 1 AND 90),
 keep_weekly smallint NOT NULL DEFAULT 4 CHECK(keep_weekly BETWEEN 0 AND 104),
 keep_monthly smallint NOT NULL DEFAULT 12 CHECK(keep_monthly BETWEEN 0 AND 120),
 offsite_enabled boolean NOT NULL DEFAULT false,
 offsite_interval_hours smallint NOT NULL DEFAULT 24 CHECK(offsite_interval_hours IN(1,2,3,4,6,8,12,24,48,168)),
 offsite_keep_daily smallint NOT NULL DEFAULT 7 CHECK(offsite_keep_daily BETWEEN 1 AND 90),
 offsite_keep_weekly smallint NOT NULL DEFAULT 4 CHECK(offsite_keep_weekly BETWEEN 0 AND 104),
 offsite_keep_monthly smallint NOT NULL DEFAULT 12 CHECK(offsite_keep_monthly BETWEEN 0 AND 120),
 probe_interval_days smallint NOT NULL DEFAULT 7 CHECK(probe_interval_days IN(0,1,7,14,30)),
 probe_target text NOT NULL DEFAULT 'local' CHECK(probe_target IN('local','offsite')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 updated_by uuid REFERENCES core.users(id),
 updated_at timestamptz NOT NULL DEFAULT now(),
 service_seen_at timestamptz,
 service_version text CHECK(service_version ~ '^[0-9A-Za-z][0-9A-Za-z.+_-]{0,80}$'),
 local_label text CHECK(length(local_label) BETWEEN 1 AND 512),
 offsite_label text CHECK(length(offsite_label) BETWEEN 1 AND 512),
 offsite_check_cursor integer NOT NULL DEFAULT 0 CHECK(offsite_check_cursor>=0)
);
INSERT INTO core.backup_settings(household_id) SELECT id FROM core.households ON CONFLICT DO NOTHING;
GRANT SELECT ON core.backup_settings TO household_app;
GRANT INSERT(household_id,local_time,local_interval_hours,keep_daily,keep_weekly,keep_monthly,offsite_enabled,offsite_interval_hours,
 offsite_keep_daily,offsite_keep_weekly,offsite_keep_monthly,probe_interval_days,probe_target,version,updated_by,updated_at),
 UPDATE(local_time,local_interval_hours,keep_daily,keep_weekly,keep_monthly,offsite_enabled,offsite_interval_hours,
 offsite_keep_daily,offsite_keep_weekly,offsite_keep_monthly,probe_interval_days,probe_target,version,updated_by,updated_at)
 ON core.backup_settings TO household_app;

-- Requested and running backup work. Rows are operational state like core.jobs:
-- semantic audit events record who requested, cancelled or completed what.
CREATE TABLE core.backup_jobs(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 household_id uuid NOT NULL REFERENCES core.households(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK(kind IN('backup','verify','restore','offsite_test','discard_previous')),
 target text NOT NULL CHECK(target IN('local','offsite','both')),
 snapshot_reference text CHECK(snapshot_reference ~ '^[0-9a-f]{64}$'),
 state text NOT NULL DEFAULT 'requested' CHECK(state IN('requested','running','preparing','awaiting_drain','swapping','complete','failed','cancelled')),
 phase text CHECK(phase ~ '^[a-z_]{1,40}$'),
 result_code text CHECK(result_code ~ '^[a-z_]{1,64}$'),
 requested_by uuid REFERENCES core.users(id),
 requested_session uuid,
 confirmation text CHECK(length(confirmation) BETWEEN 1 AND 64),
 result_snapshot text CHECK(result_snapshot ~ '^[0-9a-f]{64}$'),
 previous_database text CHECK(previous_database ~ '^household_pre_restore_[0-9]{10}_[0-9a-f]{8}$'),
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '30 minutes',
 started_at timestamptz,
 heartbeat_at timestamptz,
 app_drained_at timestamptz,
 finished_at timestamptz,
 CHECK((kind IN('verify','restore'))=(snapshot_reference IS NOT NULL)),
 CHECK(kind<>'backup' OR target IN('local','both')),
 CHECK(kind NOT IN('verify','restore') OR target IN('local','offsite')),
 CHECK(kind<>'offsite_test' OR target='offsite'),
 CHECK(kind<>'discard_previous' OR target='local'),
 CHECK(kind<>'restore' OR confirmation IS NOT NULL),
 CHECK(state IN('requested','running','preparing','awaiting_drain','swapping')=(finished_at IS NULL))
);
CREATE UNIQUE INDEX backup_jobs_one_active_restore ON core.backup_jobs(household_id)
 WHERE kind='restore' AND state IN('requested','running','preparing','awaiting_drain','swapping');
CREATE UNIQUE INDEX backup_jobs_one_pending ON core.backup_jobs(household_id,kind,target) WHERE state='requested';
CREATE INDEX backup_jobs_recent ON core.backup_jobs(household_id,created_at DESC);
CREATE INDEX backup_jobs_open ON core.backup_jobs(created_at) WHERE finished_at IS NULL;
GRANT SELECT ON core.backup_jobs TO household_app;
GRANT INSERT(id,household_id,kind,target,snapshot_reference,requested_by,requested_session,confirmation),
 UPDATE(state,result_code,finished_at) ON core.backup_jobs TO household_app;

-- The web role may only cancel a request the service has not claimed yet.
CREATE FUNCTION core.backup_jobs_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF current_user='household_owner' OR EXISTS(SELECT 1 FROM pg_roles WHERE rolname=current_user AND rolsuper) THEN
  IF TG_OP='DELETE' THEN RETURN OLD;END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'requested' OR NEW.requested_by IS NULL OR NEW.kind='restore' AND NEW.requested_session IS NULL OR NEW.phase IS NOT NULL OR NEW.result_code IS NOT NULL OR NEW.result_snapshot IS NOT NULL OR NEW.previous_database IS NOT NULL
   OR NEW.started_at IS NOT NULL OR NEW.heartbeat_at IS NOT NULL OR NEW.app_drained_at IS NOT NULL OR NEW.finished_at IS NOT NULL
   OR NEW.created_at>clock_timestamp()+interval '1 minute' OR NEW.expires_at<>NEW.created_at+interval '30 minutes' THEN
   RAISE EXCEPTION 'Backup requests start unclaimed' USING ERRCODE='42501';END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='UPDATE' AND OLD.state='requested' AND NEW.state='cancelled' AND NEW.result_code='cancelled'
  AND NEW.finished_at IS NOT NULL AND(to_jsonb(NEW)-ARRAY['state','result_code','finished_at'])=(to_jsonb(OLD)-ARRAY['state','result_code','finished_at']) THEN
  RETURN NEW;END IF;
 RAISE EXCEPTION 'Only the backup service changes backup work' USING ERRCODE='42501';
END $$;
CREATE TRIGGER backup_jobs_guard BEFORE INSERT OR UPDATE OR DELETE ON core.backup_jobs FOR EACH ROW EXECUTE FUNCTION core.backup_jobs_guard();
REVOKE ALL ON FUNCTION core.backup_jobs_guard() FROM PUBLIC,household_app;

-- During a live restore the web process confirms that it stopped writing.
CREATE FUNCTION core.acknowledge_backup_drain(p_job uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 UPDATE core.backup_jobs SET app_drained_at=coalesce(app_drained_at,clock_timestamp())
  WHERE id=p_job AND kind='restore' AND state='awaiting_drain';
 RETURN FOUND;
END $$;
REVOKE ALL ON FUNCTION core.acknowledge_backup_drain(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.acknowledge_backup_drain(uuid) TO household_app;

-- Runs record their repository kind, size and the job that produced them.
ALTER TABLE core.backup_runs ADD COLUMN repository_kind text NOT NULL DEFAULT 'local' CHECK(repository_kind IN('local','offsite')),
 ADD COLUMN job_id uuid REFERENCES core.backup_jobs(id) ON DELETE SET NULL,
 ADD COLUMN data_added bigint CHECK(data_added>=0),
 ADD COLUMN total_bytes bigint CHECK(total_bytes>=0);
CREATE INDEX backup_runs_listing ON core.backup_runs(repository_kind,state,started_at DESC);
CREATE INDEX backup_runs_snapshot ON core.backup_runs(snapshot_reference) WHERE snapshot_reference IS NOT NULL;

-- Semantic audit actions for backup work. The function body is the released
-- 000008 definition with these actions added to its allowlist.
CREATE OR REPLACE FUNCTION core.record_audit_event(p_module text,p_entity text,p_resource uuid,p_action text,p_details jsonb) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
 context_value jsonb := core.audit_context();
 safe_details jsonb := '{}';
 key text;
 household uuid;
 event_outcome text := 'success';
BEGIN
 IF p_action NOT IN('module_enabled','module_disabled','record_write','record_delete','attachment_upload','import_confirm','capture_confirm','user_create','user_update','module_purged','archive','activity','clone','merge','check','post','reverse','complete','shopping','tags','recipe','expense','transfer','login_success','login_failed','logout','password_changed','totp_setup','totp_enabled','recovery_code_used','session_revoked','operator_password_recovery','bootstrap','worker_job_failed','worker_job_unsupported','worker_lease_exhausted',
  'backup_requested','backup_cancelled','backup_settings_changed','restore_completed','restore_failed') THEN
  RAISE EXCEPTION 'unsupported audit event' USING ERRCODE='22023';
 END IF;
 IF coalesce(p_module,'')!~'^[a-z][a-z0-9_]{0,63}$' OR coalesce(p_entity,'')!~'^[a-z][a-z0-9_]{0,63}$' THEN
  RAISE EXCEPTION 'invalid audit resource type' USING ERRCODE='22023';
 END IF;
 IF p_details IS NULL OR jsonb_typeof(p_details)<>'object' OR octet_length(p_details::text)>4096 THEN
  RAISE EXCEPTION 'invalid audit metadata' USING ERRCODE='22023';
 END IF;
 FOR key IN SELECT jsonb_object_keys(p_details) LOOP
  IF key='mfa_reset' AND jsonb_typeof(p_details->key)='boolean' THEN
   safe_details := safe_details||jsonb_build_object(key,p_details->key);
  ELSIF key IN('target_user_id','requested_by') AND jsonb_typeof(p_details->key)='string' THEN
   safe_details := safe_details||jsonb_build_object(key,(p_details->>key)::uuid);
  ELSE RAISE EXCEPTION 'unsupported audit metadata' USING ERRCODE='22023'; END IF;
 END LOOP;
 IF p_action IN('login_failed','worker_job_failed','worker_job_unsupported','worker_lease_exhausted','restore_failed') THEN event_outcome := 'failure'; END IF;
 household := coalesce((context_value->>'household')::uuid,(SELECT id FROM core.households WHERE singleton=1));
 INSERT INTO core.audit_log(household_id,actor_user_id,module_id,resource_type,resource_id,action,outcome,request_id,details,actor_kind,db_role,entity_key,entity_label,created_at)
 VALUES(household,(context_value->>'actor')::uuid,p_module,p_entity,p_resource,p_action,event_outcome,(context_value->>'request')::uuid,safe_details,context_value->>'kind',session_user,CASE WHEN p_resource IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('id',p_resource) END,p_module||'.'||p_entity||CASE WHEN p_resource IS NULL THEN '' ELSE ' · '||left(p_resource::text,8) END,clock_timestamp());
END $$;

-- Classify both new tables as operational state, then advance the exact
-- audit, Pantry and Fuel pins that depend on the audit contract or Core version.
DO $compatibility$
DECLARE audit_old text;audit_new text;old_audit_hash text;new_audit_hash text;
 pantry_old text;pantry_new text;old_pantry_hash text;observations text;
 authority text;overview text;ready text;needle text;old_hash text;
BEGIN
 audit_old=pg_get_functiondef('core.audit_ready()'::regprocedure);
 needle='''jobs'',''notification_intents''';
 IF (length(audit_old)-length(replace(audit_old,needle,'')))/length(needle)<>1 THEN
  RAISE EXCEPTION 'Unreviewed Core41 audit classification';END IF;
 old_audit_hash=md5(audit_old);
 EXECUTE replace(audit_old,needle,'''jobs'',''backup_jobs'',''backup_settings'',''notification_intents''');
 audit_new=pg_get_functiondef('core.audit_ready()'::regprocedure);new_audit_hash=md5(audit_new);

 pantry_old=pg_get_functiondef('pantry.prepared_capture_ready()'::regprocedure);old_pantry_hash=md5(pantry_old);
 IF position(old_audit_hash in pantry_old)=0 THEN RAISE EXCEPTION 'Unreviewed Pantry audit pin';END IF;
 EXECUTE replace(pantry_old,old_audit_hash,new_audit_hash);
 pantry_new=pg_get_functiondef('pantry.prepared_capture_ready()'::regprocedure);

 observations=pg_get_functiondef('pantry.notification_observations_ready()'::regprocedure);
 IF position(old_audit_hash in observations)=0 OR position(old_pantry_hash in observations)=0 THEN
  RAISE EXCEPTION 'Unreviewed Pantry observation pins';END IF;
 EXECUTE replace(replace(observations,old_audit_hash,new_audit_hash),old_pantry_hash,md5(pantry_new));

 needle='bool_and(version=41 AND NOT dirty)';
 authority=pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure);
 overview=pg_get_functiondef('fuel.overview_ready()'::regprocedure);
 ready=pg_get_functiondef('fuel.module_ready()'::regprocedure);
 old_hash=md5(authority);
 IF (length(authority)-length(replace(authority,needle,'')))/length(needle)<>1
 OR (length(overview)-length(replace(overview,needle,'')))/length(needle)<>1
 OR (length(ready)-length(replace(ready,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'Unreviewed Core41 Fuel predecessor contract';END IF;
 EXECUTE replace(authority,needle,'bool_and(version=42 AND NOT dirty)');
 EXECUTE replace(overview,needle,'bool_and(version=42 AND NOT dirty)');
 EXECUTE replace(ready,old_hash,md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)));
END $compatibility$;

-- The audit contract and its dependants must hold before the Core42 ledger
-- advances; Fuel readiness is checked by the application after this commit.
DO $ready$
BEGIN
 IF NOT core.audit_ready() OR NOT pantry.prepared_capture_ready() OR NOT pantry.notification_observations_ready() THEN
  RAISE EXCEPTION 'incomplete Core42 audit or Pantry compatibility';END IF;
END $ready$;
COMMIT;
