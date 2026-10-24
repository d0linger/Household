BEGIN;
INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('shopping','stores',ARRAY['id'],ARRAY['name'],'name'),
 ('shopping','aisles',ARRAY['id'],ARRAY['store_id','name','sort_order'],'name'),
 ('shopping','products',ARRAY['id'],ARRAY['name','brand','pack_quantity','pack_unit'],'name'),
 ('shopping','price_observations',ARRAY['id'],ARRAY['product_id','store_id','observed_on','quantity','unit_code','price_minor'],NULL);
UPDATE core.audit_policies SET safe_columns=safe_columns||ARRAY['store_id','shopper_id','shopper_until'] WHERE schema_name='shopping' AND table_name='lists';
UPDATE core.audit_policies SET safe_columns=safe_columns||ARRAY['product_id','replacement_product_id','availability','assignee_id'] WHERE schema_name='shopping' AND table_name='items';
CREATE OR REPLACE FUNCTION core.record_audit_event(p_module text,p_entity text,p_resource uuid,p_action text,p_details jsonb) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
 context_value jsonb := core.audit_context();
 safe_details jsonb := '{}';
 key text;
 household uuid;
 event_outcome text := 'success';
BEGIN
 IF p_action NOT IN('module_enabled','module_disabled','record_write','record_delete','attachment_upload','import_confirm','capture_confirm','user_create','user_update','module_purged','archive','activity','check','post','reverse','complete','shopping','tags','recipe','expense','transfer','login_success','login_failed','logout','password_changed','totp_setup','totp_enabled','recovery_code_used','session_revoked','operator_password_recovery','bootstrap','worker_job_failed','worker_job_unsupported','worker_lease_exhausted') THEN
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
CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT count(*)=49 AND bool_and(
  EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
  AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure)
 ) AND NOT EXISTS(
  SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
  AND c.relname<>'schema_migrations'
  AND NOT(n.nspname='fuel' AND c.relname='fuel_types')
  AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states'))
  AND NOT EXISTS(SELECT 1 FROM core.audit_policies policy WHERE policy.schema_name=n.nspname AND policy.table_name=c.relname)
 ) FROM core.audit_policies p
$$;
COMMIT;
