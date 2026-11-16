BEGIN;
-- Security hardening from the 2026-11 audit. Function bodies are the released
-- 000029/000030 definitions with only the documented changes.

-- Freeze the exact Core40 definitions this migration replaces, so disposable
-- upgrade fixtures can reconstruct the predecessor instead of bypassing guards.
DO $predecessor$
DECLARE snapshot jsonb;
BEGIN
 SELECT jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
  'owner',p.proowner::regrole::text,'acl',p.proacl::text)) INTO snapshot FROM pg_proc p WHERE p.oid IN(
  'core.finance_access_command(uuid,uuid,uuid,uuid,bigint,bigint,text,text,uuid,bigint,bigint,text,text,uuid,jsonb,bytea)'::regprocedure,
  'core.finance_schedule_recurrences()'::regprocedure,'core.finance_audit_visible(uuid,uuid,uuid,uuid,bigint,bigint)'::regprocedure,
  'core.finance_ready()'::regprocedure,'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure,
  'fuel.overview_ready()'::regprocedure,'fuel.module_ready()'::regprocedure);
 IF (SELECT count(*) FROM jsonb_object_keys(snapshot))<>7 THEN RAISE EXCEPTION 'Unreviewed Core40 hardening predecessor';END IF;
 EXECUTE format('CREATE FUNCTION core.security_hardening_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION core.security_hardening_predecessor_catalog() FROM PUBLIC,household_app;

-- An access change revokes only the target template's own automation consent.
CREATE OR REPLACE FUNCTION core.finance_access_command(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,
 p_action text,p_kind text,p_id uuid,p_domain_version bigint,p_policy_version bigint,p_stamp text,p_mode text,p_responsible uuid,p_grants jsonb,p_reason bytea)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE actor jsonb;policy core.financial_policies%ROWTYPE;metadata jsonb;version_now bigint;grant_row jsonb;key uuid=gen_random_uuid();parent uuid;
 old_version bigint;old_mode text;old_grants jsonb;set_key uuid;inherited uuid;row_hash bytea;
BEGIN
 actor=core.finance_actor(p_household,p_user,p_membership,p_session,p_auth,p_generation);
 IF p_action NOT IN('change','assign','recover') OR p_kind NOT IN('entries','accounts','cash_events','category_budgets','savings_goals','recurring_templates','categorization_rules','reconciliations','receipt_reviews')
  OR actor->>'role' NOT IN('admin','editor') OR p_session IS NULL THEN RAISE EXCEPTION 'invalid policy operation' USING ERRCODE='42501';END IF;
 PERFORM id FROM core.households WHERE id=p_household FOR SHARE;
 SELECT * INTO policy FROM core.financial_policies WHERE household_id=p_household AND module_generation=p_generation AND resource_type=p_kind AND resource_id=p_id AND retired_at IS NULL FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'policy unavailable' USING ERRCODE='P0002';END IF;
 IF policy.version<>p_policy_version THEN RAISE EXCEPTION 'policy changed' USING ERRCODE='40001';END IF;
 IF p_action='recover' THEN
  metadata=core.finance_access_projection(p_household,p_user,p_membership,p_session,p_auth,p_generation,p_kind,p_id,true);
  IF p_domain_version<>0 OR p_stamp IS DISTINCT FROM metadata->>'access_version' OR octet_length(p_reason)<>32 THEN RAISE EXCEPTION 'reviewed recovery required' USING ERRCODE='40001';END IF;
 ELSE
  metadata=core.finance_access_projection(p_household,p_user,p_membership,p_session,p_auth,p_generation,p_kind,p_id,false);
  IF p_stamp IS DISTINCT FROM metadata->>'access_version' THEN RAISE EXCEPTION 'authority changed' USING ERRCODE='40001';END IF;
  EXECUTE format('SELECT version FROM expenses.%I WHERE household_id=$1 AND id=$2 FOR UPDATE',p_kind) INTO version_now USING p_household,p_id;
  IF version_now IS DISTINCT FROM p_domain_version THEN RAISE EXCEPTION 'resource changed' USING ERRCODE='40001';END IF;
  IF p_action='change' AND NOT coalesce((metadata->>'can_manage')::boolean,false) THEN RAISE EXCEPTION 'responsibility required' USING ERRCODE='42501';END IF;
  IF p_action='assign' AND(NOT coalesce((metadata->>'can_assign')::boolean,false) OR p_responsible<>p_membership) THEN RAISE EXCEPTION 'explicit self assignment required' USING ERRCODE='42501';END IF;
 END IF;
 IF p_action<>'change' THEN p_mode=policy.access_mode;END IF;
 IF p_mode NOT IN('household','selected') OR(p_responsible IS NULL AND(p_mode='selected' OR p_action<>'change')) OR jsonb_typeof(p_grants)<>'array' OR jsonb_array_length(p_grants)>100 THEN
  RAISE EXCEPTION 'bounded responsible policy required' USING ERRCODE='22023';END IF;
 IF p_responsible IS NOT NULL THEN
  PERFORM m.id FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=p_household AND m.id=p_responsible
   AND m.inactive_at IS NULL AND m.role IN('admin','editor') AND u.disabled_at IS NULL FOR SHARE OF m,u;
  IF NOT FOUND THEN RAISE EXCEPTION 'active responsible editor required' USING ERRCODE='22023';END IF;
 END IF;
 IF p_action='change' THEN
  IF EXISTS(SELECT 1 FROM jsonb_array_elements(p_grants) g WHERE jsonb_typeof(g)<>'object' OR EXISTS(SELECT 1 FROM jsonb_object_keys(g) k WHERE k NOT IN('membership_id','permission'))
   OR g->>'permission' NOT IN('view','edit') OR NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
    WHERE m.household_id=p_household AND m.id=(g->>'membership_id')::uuid AND m.inactive_at IS NULL AND u.disabled_at IS NULL
    AND(g->>'permission'<>'edit' OR m.role IN('admin','editor'))))
   OR(SELECT count(*) FROM jsonb_array_elements(p_grants))<>(SELECT count(DISTINCT g->>'membership_id') FROM jsonb_array_elements(p_grants) g)
   OR p_mode='selected' AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(p_grants) g WHERE g->>'membership_id'=p_responsible::text AND g->>'permission'='edit') THEN
   RAISE EXCEPTION 'complete active grants required' USING ERRCODE='22023';END IF;
 END IF;
 SELECT coalesce(jsonb_agg(jsonb_build_object('membership_id',membership_id,'permission',permission) ORDER BY membership_id),'[]') INTO old_grants
  FROM core.financial_policy_grants WHERE household_id=p_household AND policy_id=policy.id;
 SELECT o.set_id,o.row_hash INTO inherited,row_hash FROM core.finance_origin_resources o WHERE o.household_id=p_household AND o.module_generation=p_generation AND o.resource_type=p_kind AND o.resource_id=p_id;
 IF inherited IS NULL THEN RAISE EXCEPTION 'policy origin unavailable' USING ERRCODE='P0002';END IF;
 parent=nullif(current_setting('household.finance_origin',true),'')::uuid;
 IF parent IS NOT NULL THEN PERFORM core.finance_pending(p_household);END IF;
 INSERT INTO core.finance_origin_operations(id,household_id,parent_id,transaction_id,transaction_started_at,expenses_generation,actor_user_id,membership_id,session_id,auth_version,
 target_kind,target_id,origin,source_refs,inherited_set_id,depth)
 VALUES(key,p_household,parent,pg_current_xact_id(),transaction_timestamp(),p_generation,p_user,p_membership,p_session,p_auth,p_kind,p_id,'access','[]',inherited,1);
 PERFORM set_config('household.finance_origin',key::text,true);
 old_version=policy.version;old_mode=policy.access_mode;
 IF p_action='change' THEN
  DELETE FROM core.financial_policy_grants WHERE household_id=p_household AND policy_id=policy.id;
  INSERT INTO core.financial_policy_grants(household_id,policy_id,membership_id,permission,created_by)
  SELECT p_household,policy.id,(g->>'membership_id')::uuid,g->>'permission',p_user FROM jsonb_array_elements(p_grants) g;
 ELSIF p_mode='selected' THEN
  INSERT INTO core.financial_policy_grants(household_id,policy_id,membership_id,permission,created_by) VALUES(p_household,policy.id,p_responsible,'edit',p_user)
  ON CONFLICT(policy_id,membership_id) DO UPDATE SET permission='edit';
 END IF;
 UPDATE core.financial_policies SET access_mode=p_mode,responsible_membership_id=p_responsible WHERE id=policy.id RETURNING * INTO policy;
 -- Only the target template's own consent is revoked. Dependent templates fail
 -- closed through source_stamp revalidation; never revoke household-wide.
 UPDATE core.finance_background_authorities SET enabled=false,updated_at=clock_timestamp() WHERE household_id=p_household AND expenses_generation=p_generation
  AND template_id=p_id;
 INSERT INTO core.finance_origin_effects(household_id,root_id,resource_type,resource_id,effect,before_hash,after_hash,resource_version)
 VALUES(p_household,key,p_kind,p_id,'scope',row_hash,row_hash,coalesce(version_now,1));
 PERFORM core.finance_finalize_audits(key);
 UPDATE core.finance_origin_operations SET state='complete',completed_at=clock_timestamp() WHERE id=key;
 PERFORM set_config('household.finance_origin',coalesce(parent::text,''),true);
 INSERT INTO core.finance_security_events(household_id,actor_user_id,request_id,action,policy_id,policy_version,reason_code,reason_hash)
 VALUES(p_household,p_user,coalesce(nullif(current_setting('household.audit_request',true),'')::uuid,gen_random_uuid()),
 CASE p_action WHEN 'recover' THEN 'responsibility_recover' WHEN 'assign' THEN 'responsibility_assign' ELSE 'access_change' END,policy.id,policy.version,
 CASE p_action WHEN 'recover' THEN 'explicit_recovery' WHEN 'assign' THEN 'explicit_assignment' ELSE 'explicit_change' END,p_reason);
 RETURN jsonb_build_object('id',p_id,'resource_type',p_kind,'version',CASE WHEN p_action='recover' THEN policy.version ELSE version_now END,'metadata_only',p_action='recover',
  'impact',jsonb_build_object('policy_version_before',old_version,'policy_version_after',policy.version,'access_mode_before',old_mode,'access_mode_after',p_mode,
   'grants_before',CASE WHEN p_action='recover' THEN '[]'::jsonb ELSE old_grants END,'grants_after',CASE WHEN p_action='change' THEN p_grants ELSE '[]'::jsonb END));
END $$;

-- The recurrence scheduler skips consents already scheduled today or holding a
-- live job and rotates its bounded selection so later consents are reached.
CREATE OR REPLACE FUNCTION core.finance_schedule_recurrences() RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE consent record;template jsonb;created integer=0;count_now integer;
BEGIN
 IF nullif(current_setting('household.audit_kind',true),'') IS DISTINCT FROM 'worker' OR nullif(current_setting('household.audit_user',true),'') IS NOT NULL THEN
  RAISE EXCEPTION 'worker scheduler required' USING ERRCODE='42501';END IF;
 FOR consent IN SELECT c.*,h.timezone FROM core.finance_background_authorities c JOIN core.households h ON h.id=c.household_id JOIN core.module_states s ON s.household_id=c.household_id
  AND s.module_id='expenses' AND s.state='enabled' AND s.data_generation=c.expenses_generation
  JOIN core.memberships m ON m.household_id=c.household_id AND m.id=c.responsible_membership_id AND m.user_id=c.user_id AND m.inactive_at IS NULL AND m.role IN('admin','editor')
  JOIN core.users u ON u.id=c.user_id AND u.disabled_at IS NULL AND NOT u.must_change_password AND u.auth_version=c.auth_version
  JOIN core.financial_policies p ON p.household_id=c.household_id AND p.module_generation=c.expenses_generation AND p.resource_type='recurring_templates'
   AND p.resource_id=c.template_id AND p.responsible_membership_id=m.id AND p.retired_at IS NULL
  WHERE c.enabled
   -- Skip consents already scheduled today or holding a live job, then rotate
   -- the order every tick so a fixed head of ineligible consents never hides
   -- later members' consents behind the per-tick bound.
   AND NOT EXISTS(SELECT 1 FROM core.jobs j WHERE j.household_id=c.household_id AND j.kind='finance_recurrence' AND j.payload->>'template_id'=c.template_id::text
    AND(j.state IN('pending','running') AND j.attempts<j.max_attempts
     OR j.dedupe_key='finance-recurrence/'||c.household_id||'/'||c.expenses_generation||'/'||c.template_id||'/'||to_char(current_timestamp AT TIME ZONE h.timezone,'YYYY-MM-DD')))
  ORDER BY md5(c.id::text||'/'||floor(extract(epoch FROM clock_timestamp())/30)::bigint::text),c.id LIMIT 100 FOR SHARE OF c,s,m,u,p SKIP LOCKED LOOP
  EXECUTE 'SELECT to_jsonb(t) FROM expenses.recurring_templates t WHERE household_id=$1 AND id=$2' INTO template USING consent.household_id,consent.template_id;
  IF template IS NULL OR NOT(template->>'active')::boolean OR(template->>'version')::bigint<>consent.template_version
   OR consent.source_stamp IS DISTINCT FROM core.finance_automation_stamp(consent.household_id,consent.template_id,consent.responsible_membership_id,consent.expenses_generation) THEN CONTINUE;END IF;
  IF NOT core.finance_policy_allowed(consent.household_id,(SELECT policy_id FROM core.financial_subjects WHERE household_id=consent.household_id
   AND module_generation=consent.expenses_generation AND resource_type='recurring_templates' AND resource_id=consent.template_id AND retired_at IS NULL),
   consent.responsible_membership_id,consent.expenses_generation,true) THEN CONTINUE;END IF;
  INSERT INTO core.jobs(household_id,module_id,module_generation,kind,payload,dedupe_key)
  VALUES(consent.household_id,'expenses',consent.expenses_generation,'finance_recurrence',jsonb_build_object('template_id',consent.template_id),
   'finance-recurrence/'||consent.household_id||'/'||consent.expenses_generation||'/'||consent.template_id||'/'||to_char(current_timestamp AT TIME ZONE consent.timezone,'YYYY-MM-DD')) ON CONFLICT(dedupe_key) DO NOTHING;
  GET DIAGNOSTICS count_now=ROW_COUNT;created=created+count_now;
 END LOOP;
 RETURN created;
END $$;

-- Fuel binding, Meters bill and expense-editor audit rows that name an Expenses
-- entry follow that entry's Finance visibility.
CREATE OR REPLACE FUNCTION core.finance_audit_visible(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_audit bigint)
RETURNS boolean LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE item core.audit_log%ROWTYPE;head core.finance_origin_resources%ROWTYPE;generation bigint;binding core.finance_audit_bindings%ROWTYPE;classification core.financial_requirement_sets%ROWTYPE;ref text;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id JOIN core.sessions s ON s.user_id=u.id
  WHERE m.household_id=p_household AND m.id=p_membership AND m.user_id=p_user AND m.role='admin' AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
   AND u.auth_version=p_auth AND s.id=p_session AND s.auth_version=p_auth AND s.revoked_at IS NULL AND s.idle_expires_at>transaction_timestamp() AND s.absolute_expires_at>transaction_timestamp()) THEN RETURN false;END IF;
 SELECT * INTO item FROM core.audit_log WHERE id=p_audit AND(household_id=p_household OR household_id IS NULL);
 IF NOT FOUND THEN RETURN false;END IF;
 IF item.household_id IS NULL THEN
  -- The old operator recovery command wrote only this empty, scoped-by-user
  -- shape. Never classify an arbitrary or data-bearing unscoped row as shared.
  RETURN coalesce(item.actor_kind='legacy' AND item.action='operator_password_recovery'
   AND item.module_id IS NULL AND item.resource_type IS NULL AND item.resource_id IS NULL
   AND item.actor_user_id IS NOT NULL AND coalesce(item.entity_label,'')=''
   AND item.entity_key='{}'::jsonb AND item.changes='{}'::jsonb AND item.details='{}'::jsonb
   AND EXISTS(SELECT 1 FROM core.households WHERE id=p_household AND singleton=1),false);
 END IF;
 -- Rows of other tables that name an Expenses entry are Finance audit too: the
 -- named entry must be visible in the current enabled Expenses generation.
 IF item.module_id='fuel' AND item.resource_type='integration_bindings' THEN
  IF coalesce(item.changes->'target_module'->>'after',item.changes->'target_module'->>'before','expenses')='expenses' THEN
   ref=coalesce(item.changes->'target_id'->>'after',item.changes->'target_id'->>'before');
   IF ref IS NULL AND to_regclass('fuel.integration_bindings') IS NOT NULL THEN
    EXECUTE 'SELECT target_id::text FROM fuel.integration_bindings WHERE household_id=$1 AND id=$2 AND target_module=''expenses''' INTO ref USING p_household,item.resource_id;
   END IF;
  END IF;
 ELSIF item.module_id='meters' AND item.resource_type='bills' THEN
  ref=coalesce(item.changes->'expense_id'->>'after',item.changes->'expense_id'->>'before');
  IF ref IS NULL AND to_regclass('meters.bills') IS NOT NULL THEN
   EXECUTE 'SELECT expense_id::text FROM meters.bills WHERE household_id=$1 AND id=$2' INTO ref USING p_household,item.resource_id;
  END IF;
 ELSIF item.module_id='core' AND item.resource_type='expense_edit_operations' THEN
  ref=coalesce(item.changes->'entry_id'->>'after',item.changes->'entry_id'->>'before',
   (SELECT o.entry_id::text FROM core.expense_edit_operations o WHERE o.household_id=p_household AND o.id=item.resource_id));
  IF ref IS NULL THEN RETURN false;END IF;
 END IF;
 IF ref IS NOT NULL THEN
  IF ref!~'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN RETURN false;END IF;
  SELECT data_generation INTO generation FROM core.module_states WHERE household_id=p_household AND module_id='expenses' AND state='enabled';
  RETURN generation IS NOT NULL AND coalesce(core.finance_resource_visible(p_household,p_user,p_membership,p_session,p_auth,generation,'entries',ref::uuid,'view'),false);
 END IF;
 IF NOT(item.module_id='expenses' AND core.finance_domain_kind(item.resource_type) OR item.module_id='core' AND item.resource_type IN(
  'financial_policies','financial_subjects','attachments','attachment_derivatives','capture_jobs','import_batches','import_rows','resource_links')) THEN RETURN true;END IF;
 SELECT * INTO head FROM core.finance_origin_resources WHERE household_id=p_household AND resource_type='audit' AND audit_id=p_audit;
 IF NOT FOUND THEN
  SELECT * INTO binding FROM core.finance_audit_bindings WHERE household_id=p_household AND audit_id=p_audit;
  IF binding.audit_id IS NOT NULL AND binding.root_id IS NULL THEN
   SELECT * INTO classification FROM core.financial_requirement_sets WHERE household_id=p_household AND id=binding.set_id;
   IF classification.required_count=0 THEN RETURN classification.classification='nonfinancial' OR classification.classification='private_import' AND classification.creator_user_id=p_user;END IF;
  END IF;
  IF item.module_id='core' AND item.resource_type IN('attachments','attachment_derivatives','capture_jobs','import_batches') THEN
   RETURN core.finance_artifact_visible(p_household,p_user,p_membership,p_session,p_auth,CASE item.resource_type WHEN 'attachments' THEN 'attachment'
    WHEN 'attachment_derivatives' THEN 'attachment_derivative' WHEN 'capture_jobs' THEN 'capture' ELSE 'import_batch' END,item.resource_id,'view');
  END IF;
  RETURN false;
 END IF;
 IF head.private_owner_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM core.finance_private_owners WHERE household_id=p_household AND id=head.private_owner_id AND owner_user_id=p_user AND retired_at IS NULL) THEN RETURN false;END IF;
 SELECT data_generation INTO generation FROM core.module_states WHERE household_id=p_household AND module_id='expenses' AND state='enabled';
 RETURN generation=head.module_generation AND core.finance_set_visible(p_household,head.set_id,p_user,p_membership,generation,false);
END $$;

-- Re-pin the Finance catalog to the replaced function definitions.
DO $ready$
DECLARE expected jsonb;
BEGIN
 expected=core.finance_catalog();
 EXECUTE format($definition$CREATE OR REPLACE FUNCTION core.finance_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $finance_ready_body_41$
 SELECT core.finance_producers_ready() AND core.finance_catalog()=%L::jsonb AND EXISTS(SELECT 1 FROM pg_constraint c
  WHERE c.conrelid='core.financial_policies'::regclass AND c.conname='financial_access_mode' AND c.convalidated)
 AND NOT EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid='core.financial_policies'::regclass AND c.conname='financial_rollout_shared_only')
 $finance_ready_body_41$$definition$,expected);
END $ready$;
-- Fuel pins the exact Core version in its authority and overview contracts and
-- the authority body hash in its readiness. Advance exactly those pins.
DO $compatibility$
DECLARE authority text;overview text;ready text;needle text='bool_and(version=40 AND NOT dirty)';old_hash text;
BEGIN
 authority=pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure);
 overview=pg_get_functiondef('fuel.overview_ready()'::regprocedure);
 ready=pg_get_functiondef('fuel.module_ready()'::regprocedure);
 old_hash=md5(authority);
 IF (length(authority)-length(replace(authority,needle,'')))/length(needle)<>1
 OR (length(overview)-length(replace(overview,needle,'')))/length(needle)<>1
 OR (length(ready)-length(replace(ready,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'Unreviewed Core40 Fuel predecessor contract';END IF;
 EXECUTE replace(authority,needle,'bool_and(version=41 AND NOT dirty)');
 EXECUTE replace(overview,needle,'bool_and(version=41 AND NOT dirty)');
 EXECUTE replace(ready,old_hash,md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)));
END $compatibility$;
COMMIT;
