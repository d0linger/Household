BEGIN;

-- Preserve the old SQL insert shape through a deterministic reference lookup.
-- Typed commands still require an explicit quantity unit for user review.
CREATE FUNCTION fuel.fillup_dimensions() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF NEW.quantity_unit IS NULL THEN
  SELECT t.unit_code INTO NEW.quantity_unit FROM fuel.fuel_types t WHERE t.code=NEW.fuel_type AND t.code<>'electric';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION fuel.fillup_dimensions() FROM PUBLIC,household_app;
CREATE TRIGGER fuel_fillup_dimensions BEFORE INSERT ON fuel.fillups FOR EACH ROW EXECUTE FUNCTION fuel.fillup_dimensions();

CREATE FUNCTION fuel.module_authorize(p_generation bigint,p_dependencies jsonb,p_session uuid,p_auth bigint,p_membership uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE h uuid:=nullif(current_setting('household.audit_household',true),'')::uuid;
 u uuid:=nullif(current_setting('household.audit_user',true),'')::uuid; dependency record;
BEGIN
 IF current_setting('household.audit_kind',true) IS DISTINCT FROM 'user' OR h IS NULL OR u IS NULL OR
 jsonb_typeof(p_dependencies) IS DISTINCT FROM 'object' OR pg_column_size(p_dependencies)>4096 THEN
  RAISE EXCEPTION 'fuel authority required' USING ERRCODE='42501';
 END IF;
 PERFORM 1 FROM core.households WHERE id=h FOR SHARE;
 PERFORM 1 FROM core.memberships m JOIN core.users a ON a.id=m.user_id
 JOIN core.sessions s ON s.id=p_session AND s.user_id=a.id AND s.auth_version=a.auth_version
 WHERE m.id=p_membership AND m.household_id=h AND m.user_id=u AND m.role IN('admin','editor') AND m.inactive_at IS NULL
 AND a.disabled_at IS NULL AND NOT a.must_change_password AND a.auth_version=p_auth
 AND s.revoked_at IS NULL AND s.idle_expires_at>clock_timestamp() AND s.absolute_expires_at>clock_timestamp()
 FOR SHARE OF m,a,s;
 IF NOT FOUND THEN RAISE EXCEPTION 'fuel current actor unavailable' USING ERRCODE='42501'; END IF;
 PERFORM 1 FROM core.module_states WHERE household_id=h AND module_id='fuel' AND state='enabled' AND data_generation=p_generation FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'fuel generation unavailable' USING ERRCODE='42501'; END IF;
 IF NOT(SELECT count(*)=1 AND bool_and(version=26 AND NOT dirty) FROM core.schema_migrations) THEN
  RAISE EXCEPTION 'fuel visibility contract unavailable' USING ERRCODE='42501';
 END IF;
 FOR dependency IN SELECT key,value FROM jsonb_each(p_dependencies) ORDER BY key LOOP
  IF dependency.key NOT IN('expenses','chores','documents') OR jsonb_typeof(dependency.value)<>'number'
   OR dependency.value::text !~ '^[1-9][0-9]{0,17}$' THEN
   RAISE EXCEPTION 'invalid fuel dependency' USING ERRCODE='23514';
  END IF;
  PERFORM 1 FROM core.module_states WHERE household_id=h AND module_id=dependency.key AND state='enabled'
   AND data_generation=(dependency.value::text)::bigint FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION 'fuel dependency unavailable' USING ERRCODE='42501'; END IF;
 END LOOP;
END $$;
REVOKE ALL ON FUNCTION fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid) FROM PUBLIC,household_app;

CREATE FUNCTION fuel.module_begin(p_id uuid,p_key uuid,p_generation bigint,p_action text,p_hash bytea,p_dependencies jsonb,
 p_session uuid,p_auth bigint,p_membership uuid) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE h uuid:=nullif(current_setting('household.audit_household',true),'')::uuid;
 u uuid:=nullif(current_setting('household.audit_user',true),'')::uuid; prior fuel.module_operations%ROWTYPE;
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'current Fuel operation transaction required' USING ERRCODE='23514';
 END IF;
 PERFORM fuel.module_authorize(p_generation,p_dependencies,p_session,p_auth,p_membership);
 IF p_id IS NULL OR p_key IS NULL OR p_hash IS NULL OR octet_length(p_hash)<>32 OR p_action NOT IN(
 'vehicle.save','vehicle.archive','vehicle.select_series','series.save','fillup.save','fillup.delete','observation.save','odometer.replace','charge.save','charge.delete',
 'trip.save','trip.delete','cost.save','cost.correct','cost.link_expense','maintenance.save','maintenance.complete','maintenance.link_task',
 'vehicle.link_document','vehicle.unlink_document','import.stage','import.confirm','capture.stage','capture.confirm','settings.save','segments.rebuild') THEN
  RAISE EXCEPTION 'invalid fuel operation' USING ERRCODE='23514';
 END IF;
 IF nullif(current_setting('household.fuel_operation',true),'') IS NOT NULL THEN
  RAISE EXCEPTION 'nested fuel operation denied' USING ERRCODE='23514';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('fuel:'||h||':'||u||':'||p_generation||':'||p_key,0)) THEN
  RAISE EXCEPTION 'fuel operation in progress' USING ERRCODE='55P03';
 END IF;
 IF EXISTS(SELECT 1 FROM fuel.legacy_operation_fences WHERE household_id=h AND user_id=u AND generation=p_generation AND operation_key=p_key) THEN
  RAISE EXCEPTION 'Fuel key belongs to the previous protocol; review the existing history before a new intent' USING ERRCODE='23514';
 END IF;
 SELECT * INTO prior FROM fuel.module_operations WHERE household_id=h AND user_id=u AND generation=p_generation AND operation_key=p_key;
 IF FOUND THEN
  IF prior.request_hash<>p_hash OR prior.action<>p_action OR prior.dependencies<>p_dependencies OR prior.state<>'complete'
   OR prior.result IS NULL OR sha256(convert_to(prior.result::text,'UTF8')) IS DISTINCT FROM prior.result_sha256 THEN
   RAISE EXCEPTION 'fuel operation cannot be replayed' USING ERRCODE='23514';
  END IF;
  RETURN jsonb_build_object('replay',true,'result',prior.result,'dependencies',prior.dependencies);
 END IF;
 INSERT INTO fuel.household_state(household_id) VALUES(h) ON CONFLICT DO NOTHING;
 PERFORM 1 FROM fuel.household_state WHERE household_id=h FOR UPDATE NOWAIT;
 INSERT INTO fuel.module_operations(id,household_id,user_id,membership_id,session_id,auth_version,generation,operation_key,action,request_hash,dependencies,current_txid)
 VALUES(p_id,h,u,p_membership,p_session,p_auth,p_generation,p_key,p_action,p_hash,p_dependencies,pg_current_xact_id());
 PERFORM set_config('household.fuel_operation',p_id::text,true);
 RETURN jsonb_build_object('replay',false,'operation_id',p_id);
END $$;
REVOKE ALL ON FUNCTION fuel.module_begin(uuid,uuid,bigint,text,bytea,jsonb,uuid,bigint,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION fuel.module_begin(uuid,uuid,bigint,text,bytea,jsonb,uuid,bigint,uuid) TO household_app;

CREATE FUNCTION fuel.module_finish(p_id uuid,p_result jsonb) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE op fuel.module_operations%ROWTYPE;
BEGIN
 SELECT * INTO op FROM fuel.module_operations WHERE id=p_id FOR UPDATE;
 IF NOT FOUND OR op.state<>'pending' OR op.current_txid<>pg_current_xact_id()
 OR current_setting('household.fuel_operation',true) IS DISTINCT FROM p_id::text
 OR current_setting('household.audit_user',true) IS DISTINCT FROM op.user_id::text
 OR current_setting('household.audit_household',true) IS DISTINCT FROM op.household_id::text
 OR jsonb_typeof(p_result) IS DISTINCT FROM 'object' OR pg_column_size(p_result)>1048576
 OR p_result->>'operation_id' IS DISTINCT FROM op.id::text OR p_result->>'action' IS DISTINCT FROM op.action
 OR NOT(p_result ? 'accepted_at') OR NOT(p_result ? 'message') THEN
  RAISE EXCEPTION 'fuel completion mismatch' USING ERRCODE='23514';
 END IF;
 PERFORM fuel.module_authorize(op.generation,op.dependencies,op.session_id,op.auth_version,op.membership_id);
 IF op.effect_count<>(SELECT count(*) FROM fuel.module_effects WHERE operation_id=p_id) THEN
  RAISE EXCEPTION 'fuel observed effect count mismatch' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM fuel.module_effects WHERE operation_id=p_id) AND op.action NOT IN(
 'cost.link_expense','maintenance.link_task','vehicle.link_document','segments.rebuild') THEN
  RAISE EXCEPTION 'fuel operation has no observed effect' USING ERRCODE='23514';
 END IF;
 IF p_result ? 'record_id' AND coalesce(p_result->>'record_id','')<>'' AND op.action NOT IN('fillup.delete','charge.delete','trip.delete',
 'cost.link_expense','maintenance.link_task','vehicle.link_document','vehicle.unlink_document','segments.rebuild','settings.save')
 AND NOT EXISTS(SELECT 1 FROM fuel.module_effects WHERE operation_id=p_id AND record_id=(p_result->>'record_id')::uuid AND kind<>'DELETE') THEN
  RAISE EXCEPTION 'fuel result is not an observed record' USING ERRCODE='23514';
 END IF;
 IF coalesce(p_result->>'record_id','')<>'' AND coalesce((p_result->>'version')::bigint,0)>0
 AND op.action NOT IN('fillup.delete','charge.delete','trip.delete','cost.link_expense','maintenance.link_task','vehicle.link_document','vehicle.unlink_document','segments.rebuild')
 AND (SELECT e.after_version FROM fuel.module_effects e WHERE e.operation_id=p_id AND e.record_id=(p_result->>'record_id')::uuid AND e.relation<>'fuel.import_rows'
 ORDER BY e.ordinal DESC LIMIT 1) IS DISTINCT FROM (p_result->>'version')::bigint THEN
  RAISE EXCEPTION 'fuel result version is not the actual postimage' USING ERRCODE='23514';
 END IF;
 UPDATE fuel.module_operations SET state='complete',result=p_result,result_sha256=sha256(convert_to(p_result::text,'UTF8')),completed_at=clock_timestamp() WHERE id=p_id;
 RETURN p_result;
END $$;
REVOKE ALL ON FUNCTION fuel.module_finish(uuid,jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION fuel.module_finish(uuid,jsonb) TO household_app;

CREATE FUNCTION fuel.module_status(p_key uuid,p_generation bigint,p_session uuid,p_auth bigint,p_membership uuid) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE h uuid:=nullif(current_setting('household.audit_household',true),'')::uuid;
 u uuid:=nullif(current_setting('household.audit_user',true),'')::uuid; op fuel.module_operations%ROWTYPE;
BEGIN
 PERFORM fuel.module_authorize(p_generation,'{}',p_session,p_auth,p_membership);
 IF p_key IS NULL OR current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'current fuel status transaction required' USING ERRCODE='23514';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('fuel:'||h||':'||u||':'||p_generation||':'||p_key,0)) THEN
  RETURN jsonb_build_object('state','unknown','message','Der Vorgang wird noch verarbeitet; denselben Status erneut abrufen.');
 END IF;
 SELECT * INTO op FROM fuel.module_operations WHERE household_id=h AND user_id=u AND generation=p_generation AND operation_key=p_key;
 IF NOT FOUND OR op.state<>'complete' OR op.result IS NULL OR sha256(convert_to(op.result::text,'UTF8')) IS DISTINCT FROM op.result_sha256 THEN
  RETURN jsonb_build_object('state','unknown','message','Der Ausgang ist nicht verlässlich verfügbar. Keinen neuen Vorgang anlegen.');
 END IF;
 PERFORM fuel.module_authorize(p_generation,op.dependencies,p_session,p_auth,p_membership);
 RETURN jsonb_build_object('state',CASE WHEN op.action='operation.cancel' THEN 'refused' ELSE 'complete' END,'result',op.result,'dependencies',op.dependencies);
END $$;
REVOKE ALL ON FUNCTION fuel.module_status(uuid,bigint,uuid,bigint,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION fuel.module_status(uuid,bigint,uuid,bigint,uuid) TO household_app;

CREATE FUNCTION fuel.module_legacy_receipt(p_key uuid,p_generation bigint,p_hash bytea,p_session uuid,p_auth bigint,p_membership uuid) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE h uuid:=nullif(current_setting('household.audit_household',true),'')::uuid;
 u uuid:=nullif(current_setting('household.audit_user',true),'')::uuid; op fuel.module_operations%ROWTYPE;
BEGIN
 PERFORM fuel.module_authorize(p_generation,'{}',p_session,p_auth,p_membership);
 IF p_key IS NULL OR p_hash IS NULL OR octet_length(p_hash)<>32 OR current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'invalid fuel compatibility lookup' USING ERRCODE='23514';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('fuel:'||h||':'||u||':'||p_generation||':'||p_key,0)) THEN
  RAISE EXCEPTION 'fuel operation in progress' USING ERRCODE='55P03';
 END IF;
 SELECT * INTO op FROM fuel.module_operations WHERE household_id=h AND user_id=u AND generation=p_generation AND operation_key=p_key;
 IF NOT FOUND THEN
  IF EXISTS(SELECT 1 FROM fuel.legacy_operation_fences WHERE household_id=h AND user_id=u AND generation=p_generation AND operation_key=p_key) THEN
   RAISE EXCEPTION 'Fuel key belongs to the previous protocol; review the existing history before a new intent' USING ERRCODE='23514';
  END IF;
  RETURN NULL;
 END IF;
 IF op.request_hash<>p_hash OR op.state<>'complete' OR op.result IS NULL THEN
  RAISE EXCEPTION 'fuel compatibility replay mismatch' USING ERRCODE='23514';
 END IF;
 RETURN fuel.module_status(p_key,p_generation,p_session,p_auth,p_membership);
END $$;
REVOKE ALL ON FUNCTION fuel.module_legacy_receipt(uuid,bigint,bytea,uuid,bigint,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION fuel.module_legacy_receipt(uuid,bigint,bytea,uuid,bigint,uuid) TO household_app;

CREATE FUNCTION fuel.module_abandon(p_key uuid,p_generation bigint,p_session uuid,p_auth bigint,p_membership uuid) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE h uuid:=nullif(current_setting('household.audit_household',true),'')::uuid;
 u uuid:=nullif(current_setting('household.audit_user',true),'')::uuid; op fuel.module_operations%ROWTYPE; rid uuid:=gen_random_uuid();receipt jsonb;
BEGIN
 PERFORM fuel.module_authorize(p_generation,'{}',p_session,p_auth,p_membership);
 IF p_key IS NULL OR current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'current fuel resolution transaction required' USING ERRCODE='23514';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('fuel:'||h||':'||u||':'||p_generation||':'||p_key,0)) THEN
  RETURN jsonb_build_object('state','unknown','message','Die Verarbeitung läuft noch. Sperre später ausdrücklich erneut versuchen.');
 END IF;
 SELECT * INTO op FROM fuel.module_operations WHERE household_id=h AND user_id=u AND generation=p_generation AND operation_key=p_key;
 IF FOUND THEN
  RETURN fuel.module_status(p_key,p_generation,p_session,p_auth,p_membership);
 END IF;
 receipt:=jsonb_build_object('operation_id',rid,'action','operation.cancel','accepted_at',clock_timestamp(),
 'message','Dieser Vorgangsschlüssel wurde ausdrücklich gesperrt. Eine spätere Verarbeitung mit diesem Schlüssel ist ausgeschlossen.');
 INSERT INTO fuel.module_operations(id,household_id,user_id,membership_id,session_id,auth_version,generation,operation_key,action,
 request_hash,dependencies,current_txid,state,result,result_sha256,completed_at)
 VALUES(rid,h,u,p_membership,p_session,p_auth,p_generation,p_key,'operation.cancel',decode(repeat('00',32),'hex'),'{}',pg_current_xact_id(),
 'complete',receipt,sha256(convert_to(receipt::text,'UTF8')),clock_timestamp());
 RETURN jsonb_build_object('state','refused','result',receipt,'dependencies','{}'::jsonb);
END $$;
REVOKE ALL ON FUNCTION fuel.module_abandon(uuid,bigint,uuid,bigint,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION fuel.module_abandon(uuid,bigint,uuid,bigint,uuid) TO household_app;

CREATE FUNCTION fuel.guard_core_link() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE genuine_owner boolean;op fuel.module_operations%ROWTYPE;
BEGIN
 IF NEW.source_module<>'fuel' AND NOT(TG_OP='UPDATE' AND OLD.source_module='fuel') THEN RETURN NEW; END IF;
 SELECT r.rolsuper OR r.oid=(SELECT n.nspowner FROM pg_namespace n WHERE n.nspname='fuel') INTO genuine_owner FROM pg_roles r WHERE r.rolname=session_user;
 IF genuine_owner THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' THEN RAISE EXCEPTION 'Fuel links use explicit unlink and relink' USING ERRCODE='42501'; END IF;
 SELECT * INTO op FROM fuel.module_operations WHERE id=nullif(current_setting('household.fuel_operation',true),'')::uuid;
 IF NOT FOUND OR op.state<>'pending' OR op.current_txid<>pg_current_xact_id() OR op.household_id<>NEW.household_id
 OR current_setting('household.audit_user',true) IS DISTINCT FROM op.user_id::text
 OR current_setting('household.audit_household',true) IS DISTINCT FROM op.household_id::text
 OR NOT EXISTS(SELECT 1 FROM fuel.integration_bindings b WHERE b.household_id=NEW.household_id AND b.source_entity=NEW.source_type
 AND b.source_id=NEW.source_id AND b.target_module=NEW.target_module AND b.target_entity=NEW.target_type AND b.target_id=NEW.target_id
 AND b.relation=NEW.relation AND b.state='linked' AND b.target_generation=(op.dependencies->>NEW.target_module)::bigint) THEN
  RAISE EXCEPTION 'Fuel Core link requires the current reviewed operation and binding' USING ERRCODE='42501';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION fuel.guard_core_link() FROM PUBLIC,household_app;
CREATE TRIGGER fuel_module_link BEFORE INSERT OR UPDATE ON core.resource_links FOR EACH ROW EXECUTE FUNCTION fuel.guard_core_link();

-- Only this lease/authority-checked seam can arm an exact worker postimage.
-- Caller-set audit/GUC values alone never authorize a proposal transition.
CREATE FUNCTION fuel.capture_transition(p_job uuid,p_token uuid,p_mode text,p_text text,p_suggestions jsonb,p_warnings jsonb,p_error text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE j fuel.capture_jobs%ROWTYPE;n fuel.capture_jobs%ROWTYPE;authorized boolean;changed integer;
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' OR current_setting('household.audit_kind',true) IS DISTINCT FROM 'worker'
 OR nullif(current_setting('household.audit_user',true),'') IS NOT NULL OR p_job IS NULL OR p_mode NOT IN('claim','publish','expire') THEN
  RAISE EXCEPTION 'invalid Fuel worker transition context' USING ERRCODE='42501';
 END IF;
 SELECT * INTO j FROM fuel.capture_jobs WHERE id=p_job;
 IF NOT FOUND THEN RETURN false; END IF;
 IF current_setting('household.audit_household',true) IS DISTINCT FROM j.household_id::text THEN
  RAISE EXCEPTION 'Fuel worker household mismatch' USING ERRCODE='42501';
 END IF;
 PERFORM 1 FROM core.households WHERE id=j.household_id FOR SHARE;
 PERFORM 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
 JOIN core.sessions s ON s.id=j.session_id AND s.user_id=u.id AND s.auth_version=u.auth_version
 JOIN core.module_states ms ON ms.household_id=m.household_id AND ms.module_id='fuel' AND ms.state='enabled' AND ms.data_generation=j.generation
 JOIN core.attachments a ON a.household_id=m.household_id AND a.id=j.attachment_id AND a.module_id='fuel'
 AND a.module_generation=j.generation AND a.resource_type='vehicles' AND a.resource_id=j.vehicle_id AND a.state='ready'
 AND a.sha256=j.attachment_sha256 AND a.size_bytes BETWEEN 1 AND 10485760 AND a.media_type IN('image/jpeg','image/png','application/pdf')
 JOIN fuel.vehicles v ON v.household_id=m.household_id AND v.id=j.vehicle_id AND v.archived_at IS NULL
 WHERE m.household_id=j.household_id AND m.id=j.membership_id AND m.user_id=j.user_id AND m.inactive_at IS NULL
 AND m.role IN('admin','editor') AND u.disabled_at IS NULL AND NOT u.must_change_password AND u.auth_version=j.auth_version
 AND s.revoked_at IS NULL AND s.idle_expires_at>clock_timestamp() AND s.absolute_expires_at>clock_timestamp()
 FOR SHARE OF m,u,s,ms,a,v NOWAIT;
 authorized:=FOUND;
 SELECT * INTO j FROM fuel.capture_jobs WHERE id=p_job FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RETURN false; END IF;
 n:=j;
 IF p_mode='claim' THEN
  IF NOT authorized OR p_token IS NULL OR j.expires_at<=clock_timestamp() OR j.attempts>=5 OR j.next_attempt_at>clock_timestamp()
   OR NOT(j.state='queued' OR j.state='running' AND j.lease_until<clock_timestamp()) THEN RETURN false; END IF;
  n.state:='running';n.lease_token:=p_token;n.lease_until:=clock_timestamp()+interval '45 seconds';n.attempts:=j.attempts+1;
 ELSIF p_mode='publish' THEN
  IF NOT authorized OR p_token IS NULL OR j.state<>'running' OR j.lease_token IS DISTINCT FROM p_token
   OR j.lease_until<=clock_timestamp() OR j.expires_at<=clock_timestamp() THEN RETURN false; END IF;
  IF p_text IS NULL OR octet_length(p_text)>65536 OR jsonb_typeof(p_suggestions) IS DISTINCT FROM 'object'
   OR octet_length(p_suggestions::text)>131072 OR jsonb_typeof(p_warnings) IS DISTINCT FROM 'array'
   OR octet_length(p_warnings::text)>65536 OR coalesce(p_error,'') NOT IN('','ocr_disabled','ocr_failed','ocr_output_limit') THEN
   RAISE EXCEPTION 'invalid bounded Fuel OCR proposal' USING ERRCODE='23514';
  END IF;
  n.state:=CASE WHEN coalesce(p_error,'')='' THEN 'review' ELSE 'failed' END;
  n.suggestions:=p_suggestions;n.warnings:=p_warnings;n.source_text:=p_text;n.error_code:=nullif(p_error,'');n.lease_token:=NULL;n.lease_until:=NULL;
 ELSE
  IF NOT(j.state='queued' OR j.state='running' AND j.lease_until<clock_timestamp())
   OR(authorized AND j.expires_at>clock_timestamp() AND j.attempts<5) THEN RETURN false; END IF;
  n.state:='expired';n.error_code:='authority_unavailable';n.lease_token:=NULL;n.lease_until:=NULL;
  n.source_text:='';n.suggestions:='{}';n.warnings:='[]';
 END IF;
 n.version:=j.version+1;n.updated_at:=clock_timestamp();
 INSERT INTO fuel.capture_transitions(transaction_id,job_id,before_sha256,after_sha256)
 VALUES(pg_current_xact_id(),j.id,sha256(convert_to(to_jsonb(j)::text,'UTF8')),sha256(convert_to(to_jsonb(n)::text,'UTF8')))
 ON CONFLICT(transaction_id,job_id) DO UPDATE SET before_sha256=excluded.before_sha256,after_sha256=excluded.after_sha256;
 PERFORM set_config('household.fuel_capture_job',j.id::text,true);
 UPDATE fuel.capture_jobs SET state=n.state,suggestions=n.suggestions,warnings=n.warnings,source_text=n.source_text,error_code=n.error_code,
 lease_token=n.lease_token,lease_until=n.lease_until,attempts=n.attempts,version=n.version,updated_at=n.updated_at WHERE id=j.id AND version=j.version;
 GET DIAGNOSTICS changed=ROW_COUNT;
 DELETE FROM fuel.capture_transitions WHERE transaction_id=pg_current_xact_id() AND job_id=j.id;
 RETURN changed=1;
END $$;
REVOKE ALL ON FUNCTION fuel.capture_transition(uuid,uuid,text,text,jsonb,jsonb,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION fuel.capture_transition(uuid,uuid,text,text,jsonb,jsonb,text) TO household_app;

CREATE FUNCTION fuel.guard_module_fact() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE owner_session boolean; op fuel.module_operations%ROWTYPE; before_row jsonb; after_row jsonb; h uuid; rid uuid;
BEGIN
 SELECT r.rolsuper OR r.oid=(SELECT n.nspowner FROM pg_namespace n WHERE n.nspname='fuel') INTO owner_session FROM pg_roles r WHERE r.rolname=session_user;
 IF owner_session THEN RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END; END IF;
 IF TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'fuel truncate denied' USING ERRCODE='42501'; END IF;
 IF TG_OP<>'INSERT' THEN before_row:=to_jsonb(OLD); END IF;
 IF TG_OP<>'DELETE' THEN after_row:=to_jsonb(NEW); END IF;
 IF TG_TABLE_NAME='capture_jobs' AND TG_OP='UPDATE' AND current_setting('household.audit_kind',true)='worker'
 AND current_setting('household.fuel_capture_job',true)=after_row->>'id'
 AND EXISTS(SELECT 1 FROM fuel.capture_transitions c WHERE c.transaction_id=pg_current_xact_id() AND c.job_id=(after_row->>'id')::uuid
 AND c.before_sha256=sha256(convert_to(before_row::text,'UTF8')) AND c.after_sha256=sha256(convert_to(after_row::text,'UTF8')))
 AND before_row-ARRAY['state','suggestions','warnings','source_text','error_code','version','attempts','lease_until','lease_token','next_attempt_at','updated_at']
 =after_row-ARRAY['state','suggestions','warnings','source_text','error_code','version','attempts','lease_until','lease_token','next_attempt_at','updated_at']
 AND (after_row->>'version')::bigint=(before_row->>'version')::bigint+1
 AND before_row->>'state' IN('queued','running') AND after_row->>'state' IN('running','review','failed','expired') THEN
  RETURN NEW;
 END IF;
 h:=coalesce(after_row,before_row)->>'household_id';
 SELECT * INTO op FROM fuel.module_operations WHERE id=nullif(current_setting('household.fuel_operation',true),'')::uuid;
 IF NOT FOUND OR op.state<>'pending' OR op.current_txid<>pg_current_xact_id() OR op.household_id<>h
 OR current_setting('household.audit_user',true) IS DISTINCT FROM op.user_id::text
 OR current_setting('household.audit_household',true) IS DISTINCT FROM op.household_id::text THEN
  RAISE EXCEPTION 'fuel fact requires current pending operation' USING ERRCODE='42501';
 END IF;
 IF TG_OP='UPDATE' AND (before_row->>'household_id' IS DISTINCT FROM after_row->>'household_id'
  OR before_row->>'id' IS DISTINCT FROM after_row->>'id' OR before_row->>'created_by' IS DISTINCT FROM after_row->>'created_by'
  OR before_row->>'created_at' IS DISTINCT FROM after_row->>'created_at'
  OR (before_row ? 'version' AND (after_row->>'version')::bigint<>(before_row->>'version')::bigint+1)) THEN
  RAISE EXCEPTION 'fuel identity or version changed' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' AND after_row ? 'created_by' AND after_row->>'created_by' IS DISTINCT FROM op.user_id::text THEN
  RAISE EXCEPTION 'fuel creator mismatch' USING ERRCODE='23514';
 END IF;
 IF TG_TABLE_NAME='user_settings' AND coalesce(after_row,before_row)->>'user_id' IS DISTINCT FROM op.user_id::text THEN
  RAISE EXCEPTION 'fuel personal settings actor mismatch' USING ERRCODE='42501';
 END IF;
 IF TG_TABLE_NAME='user_settings' AND TG_OP<>'DELETE' AND (after_row->>'generation')::bigint<>op.generation THEN
  RAISE EXCEPTION 'fuel personal settings generation mismatch' USING ERRCODE='42501';
 END IF;
 IF TG_TABLE_NAME IN('import_jobs','capture_jobs') AND
 (coalesce(after_row,before_row)->>'user_id' IS DISTINCT FROM op.user_id::text OR
  (coalesce(after_row,before_row)->>'generation')::bigint<>op.generation) THEN
  RAISE EXCEPTION 'fuel staging actor or generation mismatch' USING ERRCODE='42501';
 END IF;
 IF TG_TABLE_NAME='import_rows' AND NOT EXISTS(SELECT 1 FROM fuel.import_jobs j
 WHERE j.id=(coalesce(after_row,before_row)->>'job_id')::uuid AND j.household_id=op.household_id AND j.user_id=op.user_id AND j.generation=op.generation) THEN
  RAISE EXCEPTION 'fuel import row owner mismatch' USING ERRCODE='42501';
 END IF;
 IF TG_TABLE_NAME='import_rows' AND TG_OP='UPDATE' AND
 (before_row->'job_id' IS DISTINCT FROM after_row->'job_id' OR before_row->'row_number' IS DISTINCT FROM after_row->'row_number'
 OR before_row->'source_fingerprint' IS DISTINCT FROM after_row->'source_fingerprint' OR before_row->'source_values' IS DISTINCT FROM after_row->'source_values') THEN
  RAISE EXCEPTION 'fuel staged row provenance is immutable' USING ERRCODE='23514';
 END IF;
 IF TG_TABLE_NAME IN('import_jobs','capture_jobs') AND TG_OP='INSERT' AND
 (after_row->>'membership_id' IS DISTINCT FROM op.membership_id::text OR
 TG_TABLE_NAME='capture_jobs' AND(after_row->>'session_id' IS DISTINCT FROM op.session_id::text OR (after_row->>'auth_version')::bigint<>op.auth_version)) THEN
  RAISE EXCEPTION 'fuel staged source actor mismatch' USING ERRCODE='42501';
 END IF;
 IF TG_TABLE_NAME IN('import_jobs','capture_jobs') AND TG_OP='UPDATE' AND
 (before_row->'user_id' IS DISTINCT FROM after_row->'user_id' OR before_row->'membership_id' IS DISTINCT FROM after_row->'membership_id'
 OR before_row->'generation' IS DISTINCT FROM after_row->'generation' OR before_row->'vehicle_id' IS DISTINCT FROM after_row->'vehicle_id'
 OR before_row->'session_id' IS DISTINCT FROM after_row->'session_id' OR before_row->'auth_version' IS DISTINCT FROM after_row->'auth_version'
 OR before_row->'attachment_id' IS DISTINCT FROM after_row->'attachment_id' OR before_row->'attachment_sha256' IS DISTINCT FROM after_row->'attachment_sha256'
 OR before_row->'kind' IS DISTINCT FROM after_row->'kind' OR before_row->'source_sha256' IS DISTINCT FROM after_row->'source_sha256') THEN
  RAISE EXCEPTION 'fuel staged source identity is immutable' USING ERRCODE='23514';
 END IF;
 IF TG_TABLE_NAME IN('vehicle_costs','maintenance_events') AND TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'fuel history requires correction fact' USING ERRCODE='42501';
 END IF;
 IF TG_TABLE_NAME='integration_bindings' AND TG_OP<>'DELETE' THEN
  IF (op.dependencies->>(after_row->>'target_module'))::bigint IS DISTINCT FROM (after_row->>'target_generation')::bigint THEN
   RAISE EXCEPTION 'fuel binding requires current dependency intent' USING ERRCODE='42501';
  END IF;
  IF TG_OP='UPDATE' AND(before_row->'source_entity' IS DISTINCT FROM after_row->'source_entity'
   OR before_row->'source_id' IS DISTINCT FROM after_row->'source_id' OR before_row->'source_cycle' IS DISTINCT FROM after_row->'source_cycle'
   OR before_row->'vehicle_id' IS DISTINCT FROM after_row->'vehicle_id' OR before_row->'target_module' IS DISTINCT FROM after_row->'target_module'
   OR before_row->'target_entity' IS DISTINCT FROM after_row->'target_entity' OR before_row->'target_id' IS DISTINCT FROM after_row->'target_id'
   OR before_row->'relation' IS DISTINCT FROM after_row->'relation') THEN
   RAISE EXCEPTION 'fuel integration identity is immutable' USING ERRCODE='23514';
  END IF;
  CASE after_row->>'source_entity'
  WHEN 'vehicles' THEN PERFORM 1 FROM fuel.vehicles WHERE household_id=h AND id=(after_row->>'source_id')::uuid AND id=(after_row->>'vehicle_id')::uuid;
  WHEN 'fillups' THEN PERFORM 1 FROM fuel.fillups WHERE household_id=h AND id=(after_row->>'source_id')::uuid AND vehicle_id=(after_row->>'vehicle_id')::uuid;
  WHEN 'charging_sessions' THEN PERFORM 1 FROM fuel.charging_sessions WHERE household_id=h AND id=(after_row->>'source_id')::uuid AND vehicle_id=(after_row->>'vehicle_id')::uuid;
  WHEN 'vehicle_costs' THEN PERFORM 1 FROM fuel.vehicle_costs WHERE household_id=h AND id=(after_row->>'source_id')::uuid AND vehicle_id=(after_row->>'vehicle_id')::uuid;
  WHEN 'maintenance_plans' THEN PERFORM 1 FROM fuel.maintenance_plans WHERE household_id=h AND id=(after_row->>'source_id')::uuid
   AND vehicle_id=(after_row->>'vehicle_id')::uuid AND cycle=(after_row->>'source_cycle')::bigint;
  ELSE RAISE EXCEPTION 'unsupported fuel integration source' USING ERRCODE='23514';
  END CASE;
  IF NOT FOUND THEN RAISE EXCEPTION 'fuel integration source unavailable' USING ERRCODE='23514'; END IF;
 END IF;
 IF TG_TABLE_NAME='fillups' AND TG_OP<>'DELETE' AND NOT EXISTS(SELECT 1 FROM fuel.fuel_types t
  WHERE t.code=after_row->>'fuel_type' AND t.unit_code=after_row->>'quantity_unit' AND t.code<>'electric') THEN
  RAISE EXCEPTION 'fuel type and quantity unit mismatch' USING ERRCODE='23514';
 END IF;
 RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END $$;
REVOKE ALL ON FUNCTION fuel.guard_module_fact() FROM PUBLIC,household_app;

CREATE FUNCTION fuel.capture_module_effect() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE owner_session boolean; op uuid:=nullif(current_setting('household.fuel_operation',true),'')::uuid;
 b jsonb; a jsonb; row_data jsonb; h uuid; v uuid; rid uuid; revision bigint;
BEGIN
 SELECT r.rolsuper OR r.oid=(SELECT n.nspowner FROM pg_namespace n WHERE n.nspname='fuel') INTO owner_session FROM pg_roles r WHERE r.rolname=session_user;
 IF TG_OP<>'INSERT' THEN b:=to_jsonb(OLD); END IF;
 IF TG_OP<>'DELETE' THEN a:=to_jsonb(NEW); END IF;
 row_data:=coalesce(a,b);h:=(row_data->>'household_id')::uuid;
 rid:=coalesce(row_data->>'id',row_data->>'job_id',row_data->>'user_id')::uuid;
 IF NOT owner_session AND NOT(TG_TABLE_NAME='capture_jobs' AND TG_OP='UPDATE'
 AND current_setting('household.audit_kind',true)='worker' AND current_setting('household.fuel_capture_job',true)=rid::text
 AND EXISTS(SELECT 1 FROM fuel.capture_transitions c WHERE c.transaction_id=pg_current_xact_id() AND c.job_id=rid
 AND c.before_sha256=sha256(convert_to(b::text,'UTF8')) AND c.after_sha256=sha256(convert_to(a::text,'UTF8')))) THEN
  UPDATE fuel.module_operations SET effect_count=effect_count+1 WHERE id=op AND state='pending'
   AND current_txid=pg_current_xact_id() AND effect_count<4096;
  IF NOT FOUND THEN RAISE EXCEPTION 'fuel operation effect limit' USING ERRCODE='23514'; END IF;
  INSERT INTO fuel.module_effects(operation_id,relation,record_id,kind,before_sha256,after_sha256,before_version,after_version)
  VALUES(op,TG_TABLE_SCHEMA||'.'||TG_TABLE_NAME,rid,TG_OP,sha256(convert_to(b::text,'UTF8')),sha256(convert_to(a::text,'UTF8')),
   (b->>'version')::bigint,(a->>'version')::bigint);
 END IF;
 IF TG_TABLE_NAME IN('vehicles','odometer_series','fillups','odometer_observations','charging_sessions','trips','vehicle_costs','maintenance_plans','maintenance_events','integration_bindings') THEN
  INSERT INTO fuel.household_state(household_id,source_version) VALUES(h,1)
   ON CONFLICT(household_id) DO UPDATE SET source_version=fuel.household_state.source_version+1,updated_at=clock_timestamp();
 END IF;
 IF TG_TABLE_NAME IN('vehicles','odometer_series','fillups') THEN
  v:=CASE WHEN TG_TABLE_NAME='vehicles' THEN rid ELSE (row_data->>'vehicle_id')::uuid END;
  -- A vehicle maintenance purge may already have removed the parent.
  IF EXISTS(SELECT 1 FROM fuel.vehicles WHERE household_id=h AND id=v) THEN
   INSERT INTO fuel.segment_state(household_id,vehicle_id,source_version) VALUES(h,v,1)
    ON CONFLICT(household_id,vehicle_id) DO UPDATE SET source_version=fuel.segment_state.source_version+1,status='pending',updated_at=clock_timestamp()
    RETURNING source_version INTO revision;
   INSERT INTO fuel.segment_jobs(household_id,vehicle_id,target_version) VALUES(h,v,revision)
    ON CONFLICT(household_id,vehicle_id) DO UPDATE SET target_version=excluded.target_version,state='pending',attempts=0,next_attempt_at=clock_timestamp(),lease_until=NULL,updated_at=clock_timestamp();
  END IF;
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION fuel.capture_module_effect() FROM PUBLIC,household_app;

CREATE FUNCTION fuel.check_module_operation_complete() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM fuel.module_operations WHERE id=NEW.id AND state<>'complete') THEN
  RAISE EXCEPTION 'unfinished fuel operation' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION fuel.check_module_operation_complete() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER fuel_operation_complete AFTER INSERT ON fuel.module_operations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fuel.check_module_operation_complete();
CREATE CONSTRAINT TRIGGER fuel_operation_state_complete AFTER UPDATE ON fuel.module_operations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(OLD.state IS DISTINCT FROM NEW.state)
 EXECUTE FUNCTION fuel.check_module_operation_complete();

DO $install$
DECLARE name text;
BEGIN
 FOREACH name IN ARRAY ARRAY['vehicles','odometer_series','fillups','odometer_observations','charging_sessions','trips','vehicle_costs',
  'maintenance_plans','maintenance_events','user_settings','import_jobs','import_rows','capture_jobs','integration_bindings'] LOOP
  EXECUTE format('CREATE TRIGGER fuel_operation_guard BEFORE INSERT OR UPDATE OR DELETE ON fuel.%I FOR EACH ROW EXECUTE FUNCTION fuel.guard_module_fact()',name);
  EXECUTE format('CREATE TRIGGER fuel_operation_effect AFTER INSERT OR UPDATE OR DELETE ON fuel.%I FOR EACH ROW EXECUTE FUNCTION fuel.capture_module_effect()',name);
  EXECUTE format('CREATE TRIGGER fuel_operation_truncate BEFORE TRUNCATE ON fuel.%I FOR EACH STATEMENT EXECUTE FUNCTION fuel.guard_module_fact()',name);
 END LOOP;
END $install$;

COMMIT;
