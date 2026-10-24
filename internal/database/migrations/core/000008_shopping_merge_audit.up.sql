BEGIN;
CREATE OR REPLACE FUNCTION core.record_audit_event(p_module text,p_entity text,p_resource uuid,p_action text,p_details jsonb) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
 context_value jsonb := core.audit_context();
 safe_details jsonb := '{}';
 key text;
 household uuid;
 event_outcome text := 'success';
BEGIN
 IF p_action NOT IN('module_enabled','module_disabled','record_write','record_delete','attachment_upload','import_confirm','capture_confirm','user_create','user_update','module_purged','archive','activity','clone','merge','check','post','reverse','complete','shopping','tags','recipe','expense','transfer','login_success','login_failed','logout','password_changed','totp_setup','totp_enabled','recovery_code_used','session_revoked','operator_password_recovery','bootstrap','worker_job_failed','worker_job_unsupported','worker_lease_exhausted') THEN
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
 IF p_action IN('login_failed','worker_job_failed','worker_job_unsupported','worker_lease_exhausted') THEN event_outcome := 'failure'; END IF;
 household := coalesce((context_value->>'household')::uuid,(SELECT id FROM core.households WHERE singleton=1));
 INSERT INTO core.audit_log(household_id,actor_user_id,module_id,resource_type,resource_id,action,outcome,request_id,details,actor_kind,db_role,entity_key,entity_label,created_at)
 VALUES(household,(context_value->>'actor')::uuid,p_module,p_entity,p_resource,p_action,event_outcome,(context_value->>'request')::uuid,safe_details,context_value->>'kind',session_user,CASE WHEN p_resource IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('id',p_resource) END,p_module||'.'||p_entity||CASE WHEN p_resource IS NULL THEN '' ELSE ' · '||left(p_resource::text,8) END,clock_timestamp());
END $$;
COMMIT;
