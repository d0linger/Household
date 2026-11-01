BEGIN;
SET LOCAL search_path=pg_catalog;
SET LOCAL timezone='UTC';
SELECT id FROM core.households ORDER BY id FOR UPDATE;
DO $predecessor$ BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=29 AND NOT dirty OR version=30 AND dirty) FROM core.schema_migrations) OR NOT core.finance_authority_ready()
 OR NOT expenses.finance_ready() OR(SELECT count(*) FROM core.audit_policies)<>100 THEN RAISE EXCEPTION 'Finance producers require reviewed Core29 and Expenses5';END IF;
END $predecessor$;

ALTER TABLE core.import_batches ADD COLUMN created_by uuid REFERENCES core.users(id),ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 ADD COLUMN source_hash bytea CHECK(source_hash IS NULL OR octet_length(source_hash)=32),
 ADD COLUMN profile_id uuid,ADD COLUMN profile_version bigint CHECK(profile_version>0),ADD COLUMN account_id uuid,ADD COLUMN account_version bigint CHECK(account_version>0),
 ADD COLUMN review_data jsonb CHECK(review_data IS NULL OR(jsonb_typeof(review_data)='object' AND octet_length(review_data::text)<=65536));
ALTER TABLE core.import_batches DROP CONSTRAINT recipe_import_batch_purpose;
ALTER TABLE core.import_batches ADD CONSTRAINT recipe_import_batch_purpose CHECK(
 (purpose='flat_csv' AND owner_user_id IS NULL AND expires_at IS NULL AND preview_sha256 IS NULL) OR
 (purpose='recipe_review' AND module_id='recipes' AND entity='recipes' AND owner_user_id IS NOT NULL AND expires_at IS NOT NULL
  AND preview_sha256 IS NOT NULL AND octet_length(preview_sha256)=32 AND row_count BETWEEN 1 AND 20) OR
 (purpose='finance_bank' AND module_id='expenses' AND entity='bank' AND created_by IS NOT NULL AND expires_at IS NOT NULL
  AND source_hash IS NOT NULL AND octet_length(source_hash)=32 AND profile_id IS NOT NULL AND profile_version>0 AND account_id IS NOT NULL AND account_version>0));
UPDATE core.audit_policies SET safe_columns=safe_columns||ARRAY['created_by','version','expires_at','source_hash','profile_id','profile_version','account_id','account_version']
 WHERE schema_name='core' AND table_name='import_batches';

CREATE FUNCTION core.finance_import_identity() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF core.finance_real_maintenance() THEN RETURN NEW;END IF;
 IF TG_OP='UPDATE' AND(NEW.created_by,NEW.source_hash,NEW.profile_id,NEW.profile_version,NEW.account_id,NEW.account_version,NEW.review_data)
  IS DISTINCT FROM(OLD.created_by,OLD.source_hash,OLD.profile_id,OLD.profile_version,OLD.account_id,OLD.account_version,OLD.review_data) THEN
  RAISE EXCEPTION 'import source and review identity are immutable' USING ERRCODE='23514';END IF;
 IF NEW.purpose='finance_bank' THEN
  IF NEW.created_by IS DISTINCT FROM core.finance_business_actor(NEW.household_id)
   OR NEW.expires_at<=transaction_timestamp() OR NEW.expires_at>NEW.created_at+interval '30 minutes'
   OR NEW.owner_user_id IS NOT NULL OR NEW.preview_sha256 IS NOT NULL THEN RAISE EXCEPTION 'current bounded bank preview owner required' USING ERRCODE='42501';END IF;
  IF TG_OP='UPDATE' AND(NEW.row_count,NEW.valid_count,NEW.error_count) IS DISTINCT FROM(OLD.row_count,OLD.valid_count,OLD.error_count) THEN
   RAISE EXCEPTION 'bank preview census is immutable' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.finance_import_identity() FROM PUBLIC,household_app;
CREATE TRIGGER finance_import_identity BEFORE INSERT OR UPDATE ON core.import_batches FOR EACH ROW EXECUTE FUNCTION core.finance_import_identity();

CREATE FUNCTION core.finance_row_sources(p_kind text,p_row jsonb) RETURNS jsonb LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
DECLARE refs jsonb='[]';parent jsonb;key text;kind text;entry jsonb;
BEGIN
 parent=core.finance_parent(p_kind,p_row);IF parent IS NOT NULL THEN refs=refs||jsonb_build_array(parent);END IF;
 IF p_row->>'account_id' IS NOT NULL THEN refs=refs||jsonb_build_array(jsonb_build_object('kind','accounts','id',p_row->>'account_id'));END IF;
 IF p_kind='recurring_templates' AND p_row->'template'->'payment'->>'account_id' IS NOT NULL THEN
  refs=refs||jsonb_build_array(jsonb_build_object('kind','accounts','id',p_row->'template'->'payment'->>'account_id'));
 END IF;
 FOREACH key IN ARRAY ARRAY['original_entry_id','reversal_entry_id','reverses_entry_id','replaces_entry_id','entry_id','event_id','movement_id','reverses_event_id','reverses_contribution_id','reopens_id','capture_job_id','attachment_id','entry_line_id'] LOOP
  IF p_row->>key IS NULL THEN CONTINUE;END IF;
  kind=CASE key WHEN 'event_id' THEN 'cash_events' WHEN 'movement_id' THEN 'account_movements' WHEN 'reverses_event_id' THEN 'cash_events'
   WHEN 'reverses_contribution_id' THEN 'goal_contributions' WHEN 'reopens_id' THEN 'reconciliations' WHEN 'capture_job_id' THEN 'capture'
   WHEN 'attachment_id' THEN 'attachment' WHEN 'entry_line_id' THEN 'entry_lines' ELSE 'entries' END;
  IF kind=p_kind AND p_row->>key=p_row->>'id' THEN CONTINUE;END IF;
  refs=refs||jsonb_build_array(jsonb_build_object('kind',kind,'id',p_row->>key));
 END LOOP;
 IF p_kind IN('saved_filters','category_budgets') THEN
  IF coalesce(p_row->'criteria'->>'account',p_row->'scope'->>'account','')<>'' THEN
   refs=refs||jsonb_build_array(jsonb_build_object('kind','accounts','id',coalesce(p_row->'criteria'->>'account',p_row->'scope'->>'account')));
  END IF;
  FOR entry IN SELECT value FROM jsonb_array_elements(coalesce(p_row->'criteria'->'account_ids',p_row->'scope'->'account_ids','[]')) LOOP
   refs=refs||jsonb_build_array(jsonb_build_object('kind','accounts','id',entry#>>'{}'));
  END LOOP;
 END IF;
 IF p_kind='settlement_details' THEN
  FOR entry IN SELECT value FROM jsonb_array_elements(coalesce(p_row->'reviewed_sources','[]')) LOOP
   IF entry ? 'entry_id' THEN refs=refs||jsonb_build_array(jsonb_build_object('kind','entries','id',entry->>'entry_id'));END IF;
   IF entry ? 'event_id' THEN refs=refs||jsonb_build_array(jsonb_build_object('kind','cash_events','id',entry->>'event_id'));END IF;
  END LOOP;
 END IF;
 RETURN(SELECT coalesce(jsonb_agg(DISTINCT value ORDER BY value),'[]') FROM jsonb_array_elements(refs));
END $$;
REVOKE ALL ON FUNCTION core.finance_row_sources(text,jsonb) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_effect_operation(p_household uuid,p_kind text,p_row jsonb) RETURNS core.finance_origin_operations
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE operation core.finance_origin_operations%ROWTYPE;candidate core.finance_origin_operations%ROWTYPE;cursor_key uuid;parent jsonb;parents jsonb='[]';target jsonb;target_parent jsonb;visited integer=0;parent_depth integer=0;
BEGIN
 operation=core.finance_pending(p_household);cursor_key=operation.id;parent=core.finance_parent(p_kind,p_row);
 WHILE parent IS NOT NULL LOOP
  parent_depth=parent_depth+1;IF parent_depth>16 THEN RAISE EXCEPTION 'typed financial parent depth bound' USING ERRCODE='23514';END IF;
  parents=parents||jsonb_build_array(parent);
  IF NOT core.finance_domain_kind(parent->>'kind') THEN RAISE EXCEPTION 'invalid typed parent' USING ERRCODE='42501';END IF;
  EXECUTE format('SELECT to_jsonb(t) FROM expenses.%I t WHERE household_id=$1 AND id=$2',parent->>'kind') INTO target USING p_household,(parent->>'id')::uuid;
  IF target IS NULL THEN EXIT;END IF;
  parent=core.finance_parent(parent->>'kind',target);
 END LOOP;
 LOOP
  SELECT * INTO STRICT operation FROM core.finance_origin_operations WHERE id=cursor_key AND household_id=p_household AND state='pending'
   AND transaction_id=pg_current_xact_id_if_assigned() AND transaction_started_at=transaction_timestamp();
  IF operation.target_kind=p_kind AND operation.target_id=(p_row->>'id')::uuid THEN RETURN operation;END IF;
  IF candidate.id IS NULL AND EXISTS(SELECT 1 FROM jsonb_array_elements(parents) a WHERE a->>'kind'=operation.target_kind AND(a->>'id')::uuid=operation.target_id) THEN candidate=operation;END IF;
  IF core.finance_domain_kind(operation.target_kind) THEN
   EXECUTE format('SELECT to_jsonb(t) FROM expenses.%I t WHERE household_id=$1 AND id=$2',operation.target_kind) INTO target USING p_household,operation.target_id;
   target_parent=core.finance_parent(operation.target_kind,target);
   IF candidate.id IS NULL AND target_parent IS NOT NULL AND target_parent->>'kind'=p_kind AND(target_parent->>'id')::uuid=(p_row->>'id')::uuid THEN candidate=operation;END IF;
  END IF;
  visited=visited+1;IF operation.parent_id IS NULL OR visited>=16 THEN EXIT;END IF;cursor_key=operation.parent_id;
 END LOOP;
 IF candidate.id IS NOT NULL THEN RETURN candidate;END IF;
 RAISE EXCEPTION 'financial effect is outside its reserved typed root' USING ERRCODE='42501';
END $$;
REVOKE ALL ON FUNCTION core.finance_effect_operation(uuid,text,jsonb) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_policy_create(p_operation core.finance_origin_operations,p_kind text,p_id uuid) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE key uuid;mode text='household';responsible uuid;grants jsonb='[]';spec jsonb;
BEGIN
 spec=p_operation.initial_access;
 IF spec IS NOT NULL AND p_operation.target_kind=p_kind AND p_operation.target_id=p_id THEN
  IF EXISTS(SELECT 1 FROM jsonb_object_keys(spec) k WHERE k NOT IN('access_mode','responsible_membership_id','grants')) THEN RAISE EXCEPTION 'invalid initial access fields' USING ERRCODE='22023';END IF;
  mode=spec->>'access_mode';responsible=(spec->>'responsible_membership_id')::uuid;grants=spec->'grants';
  IF mode NOT IN('household','selected') OR jsonb_typeof(grants)<>'array' OR jsonb_array_length(grants)>100 OR(mode='selected' AND responsible IS NULL) THEN RAISE EXCEPTION 'complete initial access required' USING ERRCODE='22023';END IF;
  IF responsible IS NOT NULL AND NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=p_operation.household_id AND m.id=responsible
   AND m.inactive_at IS NULL AND m.role IN('admin','editor') AND u.disabled_at IS NULL) THEN RAISE EXCEPTION 'active responsible editor required' USING ERRCODE='22023';END IF;
  IF EXISTS(SELECT 1 FROM jsonb_array_elements(grants) g WHERE jsonb_typeof(g)<>'object' OR EXISTS(SELECT 1 FROM jsonb_object_keys(g) k WHERE k NOT IN('membership_id','permission'))
   OR g->>'permission' NOT IN('view','edit') OR NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
    WHERE m.household_id=p_operation.household_id AND m.id=(g->>'membership_id')::uuid AND m.inactive_at IS NULL AND u.disabled_at IS NULL
    AND(g->>'permission'<>'edit' OR m.role IN('admin','editor'))))
   OR(SELECT count(*) FROM jsonb_array_elements(grants))<>(SELECT count(DISTINCT g->>'membership_id') FROM jsonb_array_elements(grants) g)
   OR mode='selected' AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(grants) g WHERE(g->>'membership_id')::uuid=p_operation.membership_id AND g->>'permission'='edit')
   OR mode='selected' AND responsible IS NOT NULL AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(grants) g WHERE(g->>'membership_id')::uuid=responsible AND g->>'permission'='edit') THEN
   RAISE EXCEPTION 'initial grants must explicitly preserve creator and responsible edit' USING ERRCODE='22023';END IF;
 END IF;
 INSERT INTO core.financial_policies(household_id,module_id,module_generation,resource_type,resource_id,access_mode,responsible_membership_id,created_by,changed_by)
 VALUES(p_operation.household_id,'expenses',p_operation.expenses_generation,p_kind,p_id,mode,responsible,p_operation.actor_user_id,p_operation.actor_user_id) RETURNING id INTO key;
 INSERT INTO core.financial_policy_grants(household_id,policy_id,membership_id,permission,created_by)
 SELECT p_operation.household_id,key,(g->>'membership_id')::uuid,g->>'permission',p_operation.actor_user_id FROM jsonb_array_elements(grants) g;
 RETURN key;
END $$;
REVOKE ALL ON FUNCTION core.finance_policy_create(core.finance_origin_operations,text,uuid) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_union_sets(p_household uuid,p_sets uuid[],p_exclude uuid) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE key uuid=gen_random_uuid();total integer;excluded_policy uuid;
BEGIN
 IF p_exclude IS NOT NULL THEN
  SELECT policy_id INTO excluded_policy FROM core.financial_subjects WHERE household_id=p_household AND id=p_exclude;
  IF excluded_policy IS NULL THEN RAISE EXCEPTION 'own financial policy unavailable' USING ERRCODE='42501';END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM unnest(p_sets) v LEFT JOIN core.financial_requirement_sets s ON s.household_id=p_household AND s.id=v
  WHERE v IS NOT NULL AND(s.id IS NULL OR s.classification NOT IN('financial','nonfinancial'))) THEN RAISE EXCEPTION 'unresolved financial union' USING ERRCODE='42501';END IF;
 -- The exact own policy is checked separately; its typed descendants share it.
 -- Retaining those anchors as external dependencies would poison valid deletion.
 SELECT count(DISTINCT subject_id) INTO total FROM core.financial_requirement_members WHERE household_id=p_household AND set_id=ANY(p_sets)
  AND subject_id IS DISTINCT FROM p_exclude AND policy_id IS DISTINCT FROM excluded_policy;
 IF total>1024 THEN RAISE EXCEPTION 'financial closure exceeds bound' USING ERRCODE='22023';END IF;
 INSERT INTO core.financial_requirement_sets(id,household_id,classification,required_count,reason_code) VALUES(key,p_household,
  CASE WHEN total=0 THEN 'nonfinancial' ELSE 'financial' END,total,CASE WHEN total=0 THEN 'nonfinancial_explicit' ELSE 'financial_sources' END);
 INSERT INTO core.financial_requirement_members(household_id,set_id,subject_id,policy_id)
 SELECT DISTINCT p_household,key,subject_id,policy_id FROM core.financial_requirement_members WHERE household_id=p_household AND set_id=ANY(p_sets)
  AND subject_id IS DISTINCT FROM p_exclude AND policy_id IS DISTINCT FROM excluded_policy;
 RETURN key;
END $$;
REVOKE ALL ON FUNCTION core.finance_union_sets(uuid,uuid[],uuid) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_audience_set(p_household uuid,p_set uuid,p_subject uuid) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE key uuid=gen_random_uuid();total integer;
BEGIN
 SELECT count(*) INTO total FROM(SELECT subject_id FROM core.financial_requirement_members WHERE household_id=p_household AND set_id=p_set
  UNION SELECT p_subject WHERE p_subject IS NOT NULL) subjects;
 IF total>1024 THEN RAISE EXCEPTION 'financial audience exceeds bound' USING ERRCODE='22023';END IF;
 INSERT INTO core.financial_requirement_sets(id,household_id,classification,required_count,reason_code) VALUES(key,p_household,
  CASE WHEN total=0 THEN 'nonfinancial' ELSE 'financial' END,total,CASE WHEN total=0 THEN 'nonfinancial_explicit' ELSE 'financial_sources' END);
 INSERT INTO core.financial_requirement_members(household_id,set_id,subject_id,policy_id)
 SELECT p_household,key,s.id,s.policy_id FROM core.financial_subjects s WHERE s.household_id=p_household AND(s.id=p_subject OR s.id IN(
  SELECT subject_id FROM core.financial_requirement_members WHERE household_id=p_household AND set_id=p_set));
 RETURN key;
END $$;
REVOKE ALL ON FUNCTION core.finance_audience_set(uuid,uuid,uuid) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_bind_subject() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE row_value jsonb;operation core.finance_origin_operations%ROWTYPE;policy_key uuid;parent jsonb;subject_key uuid;
BEGIN
 row_value=CASE WHEN TG_OP='DELETE' THEN to_jsonb(OLD) ELSE to_jsonb(NEW) END;
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 operation=core.finance_effect_operation((row_value->>'household_id')::uuid,TG_TABLE_NAME,row_value);
 IF TG_TABLE_NAME IN('saved_filters','bank_profiles') THEN RETURN NULL;END IF;
 IF TG_OP='DELETE' THEN
  UPDATE core.financial_subjects SET retired_at=clock_timestamp() WHERE household_id=operation.household_id AND module_generation=operation.expenses_generation
   AND resource_type=TG_TABLE_NAME AND resource_id=(row_value->>'id')::uuid AND retired_at IS NULL;
  UPDATE core.financial_policies SET retired_at=clock_timestamp() WHERE household_id=operation.household_id AND module_generation=operation.expenses_generation
   AND resource_type=TG_TABLE_NAME AND resource_id=(row_value->>'id')::uuid AND retired_at IS NULL;
  RETURN NULL;
 END IF;
 parent=core.finance_parent(TG_TABLE_NAME,row_value);
 IF parent IS NOT NULL THEN SELECT policy_id INTO policy_key FROM core.financial_subjects WHERE household_id=operation.household_id AND module_generation=operation.expenses_generation
  AND resource_type=parent->>'kind' AND resource_id=(parent->>'id')::uuid AND retired_at IS NULL;
 ELSE policy_key=core.finance_policy_create(operation,TG_TABLE_NAME,(row_value->>'id')::uuid);END IF;
 IF policy_key IS NULL THEN RAISE EXCEPTION 'parent policy unavailable' USING ERRCODE='42501';END IF;
 IF parent IS NOT NULL AND NOT core.finance_policy_allowed(operation.household_id,policy_key,operation.membership_id,operation.expenses_generation,true) THEN
  RAISE EXCEPTION 'parent edit authority required' USING ERRCODE='42501';END IF;
 INSERT INTO core.financial_subjects(household_id,module_id,module_generation,resource_type,resource_id,policy_id,created_by,changed_by)
 VALUES(operation.household_id,'expenses',operation.expenses_generation,TG_TABLE_NAME,(row_value->>'id')::uuid,policy_key,operation.actor_user_id,operation.actor_user_id);
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.finance_bind_subject() FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_domain_scope() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE row_value jsonb;prior_value jsonb;operation core.finance_origin_operations%ROWTYPE;prior core.finance_origin_resources%ROWTYPE;
 refs jsonb;set_key uuid;subject_key uuid;owner_key uuid;scope_key uuid;previous_scope uuid;scope_revision bigint;parent jsonb;own_ref jsonb;active_key text;
 sets uuid[];child record;audience uuid;
BEGIN
 row_value=CASE WHEN TG_OP='DELETE' THEN to_jsonb(OLD) ELSE to_jsonb(NEW) END;prior_value=CASE WHEN TG_OP='INSERT' THEN NULL ELSE to_jsonb(OLD) END;
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 operation=core.finance_effect_operation((row_value->>'household_id')::uuid,TG_TABLE_NAME,row_value);
 active_key=current_setting('household.finance_origin',true);PERFORM set_config('household.finance_origin',operation.id::text,true);
 SELECT * INTO prior FROM core.finance_origin_resources WHERE household_id=operation.household_id AND module_generation=operation.expenses_generation
  AND resource_type=TG_TABLE_NAME AND resource_id=(row_value->>'id')::uuid AND row_number=0 FOR UPDATE;
 IF TG_OP<>'INSERT' AND(prior.id IS NULL OR prior.retired_at IS NOT NULL OR prior.row_hash<>sha256(convert_to(prior_value::text,'UTF8'))) THEN
  RAISE EXCEPTION 'financial OLD image is not proven' USING ERRCODE='42501';END IF;
 IF TG_OP='INSERT' AND prior.id IS NOT NULL THEN RAISE EXCEPTION 'financial identity cannot be reused' USING ERRCODE='23514';END IF;
 refs=core.finance_row_sources(TG_TABLE_NAME,row_value);
 IF TG_TABLE_NAME='saved_filters' AND TG_OP='DELETE' THEN refs='[]';END IF;
 -- A parent's own anchor is not inherited by its sidecar closure: the sidecar
 -- already uses that exact policy. Other original/account anchors are retained.
 parent=core.finance_parent(TG_TABLE_NAME,row_value);
 SELECT id INTO subject_key FROM core.financial_subjects WHERE household_id=operation.household_id AND module_generation=operation.expenses_generation
  AND resource_type=TG_TABLE_NAME AND resource_id=(row_value->>'id')::uuid;
 IF subject_key IS NOT NULL AND parent IS NOT NULL AND NOT EXISTS(SELECT 1 FROM core.financial_subjects own JOIN core.financial_subjects p
  ON p.household_id=own.household_id AND p.module_generation=own.module_generation AND p.policy_id=own.policy_id
  WHERE own.id=subject_key AND p.resource_type=parent->>'kind' AND p.resource_id=(parent->>'id')::uuid) THEN
  RAISE EXCEPTION 'financial child cannot substitute its policy parent' USING ERRCODE='23514';END IF;
 IF subject_key IS NOT NULL AND parent IS NULL AND NOT EXISTS(SELECT 1 FROM core.financial_subjects own JOIN core.financial_policies p
  ON p.household_id=own.household_id AND p.id=own.policy_id WHERE own.id=subject_key AND p.resource_type=TG_TABLE_NAME AND p.resource_id=(row_value->>'id')::uuid) THEN
  RAISE EXCEPTION 'financial primary cannot substitute its own policy' USING ERRCODE='23514';END IF;
 IF parent IS NOT NULL THEN
  refs=(SELECT coalesce(jsonb_agg(value),'[]') FROM jsonb_array_elements(refs) WHERE value<>parent);
 END IF;
 set_key=core.finance_requirements(operation.household_id,operation.actor_user_id,operation.membership_id,operation.session_id,
  operation.auth_version,operation.expenses_generation,refs,CASE WHEN TG_TABLE_NAME='saved_filters' AND TG_OP<>'DELETE' THEN operation.inherited_set_id ELSE coalesce(prior.set_id,operation.inherited_set_id) END);
 sets=ARRAY[set_key,operation.inherited_set_id];
 IF parent IS NOT NULL THEN
  SELECT o.set_id INTO previous_scope FROM core.finance_origin_resources o WHERE o.household_id=operation.household_id AND o.module_generation=operation.expenses_generation
   AND o.resource_type=parent->>'kind' AND o.resource_id=(parent->>'id')::uuid;
  IF previous_scope IS NOT NULL THEN
   sets=array_append(sets,previous_scope);
  END IF;
 END IF;
 IF TG_TABLE_NAME='entries' AND TG_OP<>'DELETE' THEN
  FOR child IN SELECT o.set_id FROM core.finance_origin_resources o WHERE o.household_id=operation.household_id AND o.module_generation=operation.expenses_generation
   AND o.retired_at IS NULL AND(
    o.resource_type='entry_finance' AND EXISTS(SELECT 1 FROM expenses.entry_finance f WHERE f.household_id=o.household_id AND f.id=o.resource_id AND f.entry_id=(row_value->>'id')::uuid)
    OR o.resource_type='entry_relations' AND EXISTS(SELECT 1 FROM expenses.entry_relations f WHERE f.household_id=o.household_id AND f.id=o.resource_id AND f.entry_id=(row_value->>'id')::uuid)
    OR o.resource_type='cash_events' AND EXISTS(SELECT 1 FROM expenses.cash_events f WHERE f.household_id=o.household_id AND f.id=o.resource_id AND f.entry_id=(row_value->>'id')::uuid)) LOOP sets=array_append(sets,child.set_id);END LOOP;
 ELSIF TG_TABLE_NAME='cash_events' AND TG_OP<>'DELETE' THEN
  FOR child IN SELECT o.set_id FROM core.finance_origin_resources o JOIN expenses.account_movements f ON f.household_id=o.household_id AND f.id=o.resource_id
   WHERE o.household_id=operation.household_id AND o.module_generation=operation.expenses_generation AND o.resource_type='account_movements'
    AND o.retired_at IS NULL AND f.event_id=(row_value->>'id')::uuid LOOP sets=array_append(sets,child.set_id);END LOOP;
 END IF;
 set_key=core.finance_union_sets(operation.household_id,sets,subject_key);
 audience=core.finance_audience_set(operation.household_id,set_key,subject_key);
 IF TG_TABLE_NAME IN('saved_filters','bank_profiles') THEN
  IF TG_OP='INSERT' THEN
   IF row_value->>'owner_user_id' IS DISTINCT FROM operation.actor_user_id::text THEN RAISE EXCEPTION 'private preference creator mismatch' USING ERRCODE='42501';END IF;
   INSERT INTO core.finance_private_owners(household_id,resource_type,resource_id,module_generation,owner_user_id)
   VALUES(operation.household_id,TG_TABLE_NAME,(row_value->>'id')::uuid,operation.expenses_generation,operation.actor_user_id) RETURNING id INTO owner_key;
  ELSE owner_key=prior.private_owner_id;END IF;
 END IF;
 IF subject_key IS NOT NULL THEN
  SELECT id,revision+1 INTO previous_scope,scope_revision FROM core.financial_artifact_scope_versions WHERE household_id=operation.household_id AND artifact_kind='financial_subject' AND object_id=subject_key ORDER BY revision DESC LIMIT 1;
  INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,revision,previous_id,set_id,source_module,source_generation)
  VALUES(operation.household_id,'financial_subject',subject_key,coalesce(scope_revision,1),previous_scope,set_key,'expenses',operation.expenses_generation) RETURNING id INTO scope_key;
 END IF;
 INSERT INTO core.finance_origin_resources(household_id,resource_type,resource_id,module_generation,subject_id,private_owner_id,root_id,provenance_root_id,set_id,audit_set_id,scope_id,resource_version,row_hash,retired_at)
 VALUES(operation.household_id,TG_TABLE_NAME,(row_value->>'id')::uuid,operation.expenses_generation,subject_key,owner_key,operation.id,coalesce(prior.provenance_root_id,prior.root_id,operation.provenance_root_id,operation.id),set_key,audience,scope_key,
  (row_value->>'version')::bigint,sha256(convert_to(row_value::text,'UTF8')),CASE WHEN TG_OP='DELETE' THEN clock_timestamp() END)
 ON CONFLICT(household_id,module_generation,resource_type,resource_id) WHERE resource_type NOT IN('audit','import_row') DO UPDATE
 SET root_id=EXCLUDED.root_id,provenance_root_id=EXCLUDED.provenance_root_id,set_id=EXCLUDED.set_id,audit_set_id=EXCLUDED.audit_set_id,scope_id=EXCLUDED.scope_id,resource_version=EXCLUDED.resource_version,row_hash=EXCLUDED.row_hash,retired_at=EXCLUDED.retired_at;
 IF TG_OP='DELETE' AND owner_key IS NOT NULL THEN UPDATE core.finance_private_owners SET retired_at=clock_timestamp() WHERE id=owner_key;END IF;
 INSERT INTO core.finance_origin_effects(household_id,root_id,resource_type,resource_id,effect,before_hash,after_hash,resource_version)
 VALUES(operation.household_id,operation.id,TG_TABLE_NAME,(row_value->>'id')::uuid,lower(TG_OP),
  CASE WHEN prior_value IS NOT NULL THEN sha256(convert_to(prior_value::text,'UTF8')) END,
  CASE WHEN TG_OP<>'DELETE' THEN sha256(convert_to(row_value::text,'UTF8')) END,(row_value->>'version')::bigint+CASE WHEN TG_OP='DELETE' THEN 1 ELSE 0 END);
 PERFORM set_config('household.finance_origin',coalesce(active_key,''),true);
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.finance_domain_scope() FROM PUBLIC,household_app;

DROP TRIGGER financial_subject ON expenses.entries;
DROP TRIGGER financial_subject ON expenses.entry_lines;
DO $hooks$
DECLARE relation text;
BEGIN
 FOREACH relation IN ARRAY ARRAY['entries','entry_lines','accounts','entry_finance','entry_original_lines','cash_events','account_movements','entry_relations','entry_split_sets','entry_person_shares',
 'settlement_details','saved_filters','category_budgets','savings_goals','goal_contributions','recurring_templates','recurring_occurrences','categorization_rules','bank_profiles','reconciliations','reconciliation_items','receipt_reviews','receipt_review_lines'] LOOP
  EXECUTE format('CREATE TRIGGER financial_subject AFTER INSERT OR DELETE ON expenses.%I FOR EACH ROW EXECUTE FUNCTION core.finance_bind_subject()',relation);
  EXECUTE format('CREATE TRIGGER financial_visibility_scope AFTER INSERT OR UPDATE OR DELETE ON expenses.%I FOR EACH ROW EXECUTE FUNCTION core.finance_domain_scope()',relation);
 END LOOP;
END $hooks$;

CREATE FUNCTION core.finance_finalize_target_sources(p_operation uuid) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE operation core.finance_origin_operations%ROWTYPE;head core.finance_origin_resources%ROWTYPE;child record;sets uuid[];set_key uuid;audience uuid;scope_key uuid;revision_now bigint;
BEGIN
 SELECT * INTO STRICT operation FROM core.finance_origin_operations WHERE id=p_operation AND state='pending' AND transaction_id=pg_current_xact_id_if_assigned() AND transaction_started_at=transaction_timestamp();
 FOR head IN SELECT o.* FROM core.finance_origin_resources o WHERE o.household_id=operation.household_id AND o.root_id=operation.id AND o.retired_at IS NULL
  AND o.resource_type IN('entries','cash_events') ORDER BY CASE o.resource_type WHEN 'cash_events' THEN 0 ELSE 1 END,o.resource_id LOOP
  sets=ARRAY[head.set_id];
  IF head.resource_type='entries' THEN
   FOR child IN SELECT o.set_id FROM core.finance_origin_resources o WHERE o.household_id=head.household_id AND o.module_generation=head.module_generation AND o.retired_at IS NULL AND(
    o.resource_type='entry_lines' AND EXISTS(SELECT 1 FROM expenses.entry_lines f WHERE f.household_id=o.household_id AND f.id=o.resource_id AND f.entry_id=head.resource_id)
    OR o.resource_type='entry_finance' AND EXISTS(SELECT 1 FROM expenses.entry_finance f WHERE f.household_id=o.household_id AND f.id=o.resource_id AND f.entry_id=head.resource_id)
    OR o.resource_type='entry_original_lines' AND EXISTS(SELECT 1 FROM expenses.entry_original_lines f WHERE f.household_id=o.household_id AND f.id=o.resource_id AND f.entry_id=head.resource_id)
    OR o.resource_type='entry_relations' AND EXISTS(SELECT 1 FROM expenses.entry_relations f WHERE f.household_id=o.household_id AND f.id=o.resource_id AND f.entry_id=head.resource_id)
    OR o.resource_type='entry_split_sets' AND EXISTS(SELECT 1 FROM expenses.entry_split_sets f WHERE f.household_id=o.household_id AND f.id=o.resource_id AND f.entry_id=head.resource_id)
    OR o.resource_type='entry_person_shares' AND EXISTS(SELECT 1 FROM expenses.entry_person_shares f WHERE f.household_id=o.household_id AND f.id=o.resource_id AND f.entry_id=head.resource_id)
    OR o.resource_type='cash_events' AND EXISTS(SELECT 1 FROM expenses.cash_events f WHERE f.household_id=o.household_id AND f.id=o.resource_id AND f.entry_id=head.resource_id)) LOOP sets=array_append(sets,child.set_id);END LOOP;
  ELSE
   FOR child IN SELECT o.set_id FROM core.finance_origin_resources o WHERE o.household_id=head.household_id AND o.module_generation=head.module_generation AND o.retired_at IS NULL AND(
    o.resource_type='account_movements' AND EXISTS(SELECT 1 FROM expenses.account_movements f WHERE f.household_id=o.household_id AND f.id=o.resource_id AND f.event_id=head.resource_id)
    OR o.resource_type='settlement_details' AND EXISTS(SELECT 1 FROM expenses.settlement_details f WHERE f.household_id=o.household_id AND f.id=o.resource_id AND f.event_id=head.resource_id)) LOOP sets=array_append(sets,child.set_id);END LOOP;
  END IF;
  set_key=core.finance_union_sets(head.household_id,sets,head.subject_id);audience=core.finance_audience_set(head.household_id,set_key,head.subject_id);
  SELECT revision+1 INTO revision_now FROM core.financial_artifact_scope_versions WHERE id=head.scope_id;
  INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,revision,previous_id,set_id,source_module,source_generation)
  VALUES(head.household_id,'financial_subject',head.subject_id,coalesce(revision_now,1),head.scope_id,set_key,'expenses',head.module_generation) RETURNING id INTO scope_key;
  UPDATE core.finance_origin_resources SET set_id=set_key,audit_set_id=audience,scope_id=scope_key WHERE id=head.id;
 END LOOP;
END $$;
REVOKE ALL ON FUNCTION core.finance_finalize_target_sources(uuid) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_finalize_audits(p_root uuid) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE binding core.finance_audit_bindings%ROWTYPE;head core.finance_origin_resources%ROWTYPE;audience uuid;anchor uuid;scope_key uuid;
BEGIN
 FOR binding IN SELECT * FROM core.finance_audit_bindings WHERE root_id=p_root AND set_id IS NULL ORDER BY audit_id LOOP
  SELECT * INTO head FROM core.finance_origin_resources WHERE household_id=binding.household_id AND module_generation=binding.source_generation
   AND resource_type=binding.resource_type AND resource_id=binding.resource_id AND row_number=binding.row_number;
  IF head.id IS NOT NULL THEN audience=core.finance_union_sets(binding.household_id,ARRAY[binding.inherited_set_id,head.audit_set_id],NULL);
  ELSIF binding.policy_id IS NOT NULL THEN
   SELECT s.id INTO anchor FROM core.financial_subjects s JOIN core.financial_policies p ON p.id=s.policy_id AND p.household_id=s.household_id
    WHERE s.household_id=binding.household_id AND s.policy_id=binding.policy_id AND s.resource_type=p.resource_type AND s.resource_id=p.resource_id;
   IF anchor IS NULL THEN RAISE EXCEPTION 'causal policy audit has no bound source' USING ERRCODE='23514';END IF;
   audience=core.finance_audience_set(binding.household_id,binding.inherited_set_id,anchor);
  ELSE RAISE EXCEPTION 'causal financial audit has no source' USING ERRCODE='23514';END IF;
  INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,audit_id,revision,set_id,source_module,source_generation)
   VALUES(binding.household_id,'audit',binding.audit_id,1,audience,'expenses',binding.source_generation) RETURNING id INTO scope_key;
  INSERT INTO core.finance_origin_resources(household_id,resource_type,audit_id,module_generation,private_owner_id,root_id,set_id,audit_set_id,scope_id,resource_version,row_hash)
  SELECT binding.household_id,'audit',binding.audit_id,binding.source_generation,head.private_owner_id,p_root,audience,audience,scope_key,1,sha256(convert_to(to_jsonb(a)::text,'UTF8'))
   FROM core.audit_log a WHERE a.id=binding.audit_id;
  UPDATE core.finance_audit_bindings SET set_id=audience WHERE audit_id=binding.audit_id;
 END LOOP;
END $$;
REVOKE ALL ON FUNCTION core.finance_finalize_audits(uuid) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_bind_audit() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE head core.finance_origin_resources%ROWTYPE;operation core.finance_origin_operations%ROWTYPE;policy_key uuid;kind text;key uuid;generation bigint;own_set uuid;row_key integer=0;
 artifact_scope core.financial_artifact_scope_versions%ROWTYPE;descriptor jsonb;side text;ref_kind text;ref_id uuid;sets uuid[]='{}';audience uuid;source_root uuid;scope_key uuid;
BEGIN
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 kind=NEW.resource_type;key=NEW.resource_id;
 IF NEW.module_id='expenses' AND core.finance_domain_kind(kind) THEN NULL;
 ELSIF NEW.module_id='core' AND kind IN('attachments','attachment_derivatives','capture_jobs','import_batches') THEN
  kind=CASE kind WHEN 'attachments' THEN 'attachment' WHEN 'attachment_derivatives' THEN 'attachment_derivative' WHEN 'capture_jobs' THEN 'capture' ELSE 'import_batch' END;
 ELSIF NEW.module_id='core' AND kind='import_rows' THEN
  kind='import_row';key=(NEW.entity_key->>'batch_id')::uuid;row_key=(NEW.entity_key->>'row_number')::integer;
 ELSIF NEW.module_id='core' AND kind IN('financial_policies','financial_subjects') THEN
  IF kind='financial_policies' THEN
   SELECT p.id,p.resource_type,p.resource_id,p.module_generation INTO policy_key,kind,key,generation FROM core.financial_policies p WHERE p.household_id=NEW.household_id AND p.id=key;
  ELSE
   SELECT p.id,s.resource_type,s.resource_id,s.module_generation INTO policy_key,kind,key,generation FROM core.financial_subjects s
    JOIN core.financial_policies p ON p.id=s.policy_id AND p.household_id=s.household_id WHERE s.household_id=NEW.household_id AND s.id=key;
  END IF;
 ELSIF NEW.module_id='core' AND kind='resource_links' THEN
  SELECT to_jsonb(l) INTO descriptor FROM core.resource_links l WHERE l.household_id=NEW.household_id AND l.id=NEW.resource_id;
  IF descriptor IS NULL THEN SELECT jsonb_object_agg(k,coalesce(nullif(v->'after','null'::jsonb),v->'before')) INTO descriptor FROM jsonb_each(NEW.changes) f(k,v);END IF;
  IF descriptor->>'source_module' IS NULL OR descriptor->>'target_module' IS NULL OR descriptor->>'source_type' IS NULL
   OR descriptor->>'target_type' IS NULL OR descriptor->>'source_id' IS NULL OR descriptor->>'target_id' IS NULL THEN RETURN NULL;END IF;
  FOREACH side IN ARRAY ARRAY['source','target'] LOOP
   IF descriptor->>(side||'_module')='expenses' THEN
    ref_kind=descriptor->>(side||'_type');ref_id=(descriptor->>(side||'_id'))::uuid;
    SELECT * INTO head FROM core.finance_origin_resources WHERE household_id=NEW.household_id AND resource_type=ref_kind AND resource_id=ref_id AND row_number=0 ORDER BY module_generation DESC LIMIT 1;
    IF head.id IS NULL OR head.retired_at IS NOT NULL THEN RETURN NULL;END IF;
    audience=core.finance_audience_set(NEW.household_id,head.set_id,head.subject_id);sets=array_append(sets,audience);source_root=head.root_id;generation=head.module_generation;
   END IF;
  END LOOP;
  IF source_root IS NULL THEN
   INSERT INTO core.financial_requirement_sets(household_id,classification,required_count,reason_code) VALUES(NEW.household_id,'nonfinancial',0,'nonfinancial_explicit') RETURNING id INTO audience;
  ELSE audience=core.finance_union_sets(NEW.household_id,sets,NULL);END IF;
  INSERT INTO core.finance_audit_bindings(audit_id,household_id,root_id,resource_type,resource_id,inherited_set_id,set_id,source_generation)
  VALUES(NEW.id,NEW.household_id,source_root,'resource_links',NEW.resource_id,audience,audience,coalesce(generation,1));
  IF source_root IS NOT NULL THEN
   INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,audit_id,revision,set_id,source_module,source_generation)
   VALUES(NEW.household_id,'audit',NEW.id,1,audience,'expenses',generation) RETURNING id INTO scope_key;
   INSERT INTO core.finance_origin_resources(household_id,resource_type,audit_id,module_generation,root_id,set_id,audit_set_id,scope_id,resource_version,row_hash)
   VALUES(NEW.household_id,'audit',NEW.id,generation,source_root,audience,audience,scope_key,1,sha256(convert_to(to_jsonb(NEW)::text,'UTF8')));
  END IF;
  RETURN NULL;
 ELSE RETURN NULL;END IF;
 SELECT * INTO head FROM core.finance_origin_resources WHERE household_id=NEW.household_id AND resource_type=kind AND resource_id=key
  AND row_number=row_key ORDER BY module_generation DESC LIMIT 1;
 IF head.id IS NULL AND NEW.module_id='core' AND kind IN('attachment','attachment_derivative','capture','import_batch','import_row') THEN
  SELECT * INTO artifact_scope FROM core.financial_artifact_scope_versions WHERE household_id=NEW.household_id AND artifact_kind=kind AND object_id=key
   AND row_number IS NOT DISTINCT FROM nullif(row_key,0) ORDER BY revision DESC LIMIT 1;
  IF artifact_scope.id IS NOT NULL THEN
   INSERT INTO core.finance_audit_bindings(audit_id,household_id,resource_type,resource_id,row_number,inherited_set_id,set_id,source_generation)
   VALUES(NEW.id,NEW.household_id,kind,key,row_key,artifact_scope.set_id,artifact_scope.set_id,coalesce(artifact_scope.source_generation,1));
  END IF;
  RETURN NULL;
 END IF;
 IF nullif(current_setting('household.finance_origin',true),'') IS NOT NULL THEN operation=core.finance_pending(NEW.household_id);END IF;
 IF operation.id IS NULL THEN
  IF head.id IS NULL THEN RETURN NULL;END IF;
  SELECT * INTO operation FROM core.finance_origin_operations WHERE household_id=NEW.household_id AND id=head.root_id AND state='complete';
 END IF;
 IF operation.id IS NULL THEN RETURN NULL;END IF;
 INSERT INTO core.finance_audit_bindings(audit_id,household_id,root_id,resource_type,resource_id,row_number,policy_id,inherited_set_id,source_generation)
 VALUES(NEW.id,NEW.household_id,operation.id,kind,key,row_key,policy_key,coalesce(head.set_id,operation.inherited_set_id),operation.expenses_generation);
 IF operation.state='complete' THEN PERFORM core.finance_finalize_audits(operation.id);END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.finance_bind_audit() FROM PUBLIC,household_app;
CREATE TRIGGER finance_audit_binding AFTER INSERT ON core.audit_log FOR EACH ROW EXECUTE FUNCTION core.finance_bind_audit();

CREATE FUNCTION core.finance_artifact_visible(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_kind text,p_id uuid,p_intent text)
RETURNS boolean LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE scope core.financial_artifact_scope_versions%ROWTYPE;requirements core.financial_requirement_sets%ROWTYPE;generation bigint;valid boolean;
BEGIN
 IF p_kind NOT IN('attachment','attachment_derivative','capture','import_batch') OR p_intent NOT IN('view','edit') THEN RETURN false;END IF;
 IF NOT core.finance_artifact_live(p_household,p_kind,p_id) THEN RETURN false;END IF;
 IF NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id JOIN core.sessions s ON s.user_id=u.id
  WHERE m.household_id=p_household AND m.id=p_membership AND m.user_id=p_user AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password
   AND u.auth_version=p_auth AND s.id=p_session AND s.auth_version=p_auth AND s.revoked_at IS NULL AND s.idle_expires_at>transaction_timestamp() AND s.absolute_expires_at>transaction_timestamp()
   AND(p_intent='view' OR m.role IN('admin','editor'))) THEN RETURN false;END IF;
 SELECT * INTO scope FROM core.financial_artifact_scope_versions WHERE household_id=p_household AND artifact_kind=p_kind AND object_id=p_id ORDER BY revision DESC LIMIT 1;
 IF NOT FOUND THEN RETURN false;END IF;
 SELECT * INTO requirements FROM core.financial_requirement_sets WHERE household_id=p_household AND id=scope.set_id;
 IF requirements.classification='nonfinancial' AND requirements.required_count=0 THEN RETURN true;END IF;
 IF requirements.classification='private_import' AND requirements.creator_user_id=p_user AND requirements.required_count=0 THEN RETURN true;END IF;
 SELECT data_generation INTO generation FROM core.module_states WHERE household_id=p_household AND module_id='expenses' AND state='enabled';
 IF generation IS NULL THEN RETURN false;END IF;
 RETURN core.finance_resource_visible(p_household,p_user,p_membership,p_session,p_auth,generation,p_kind,p_id,p_intent);
END $$;
REVOKE ALL ON FUNCTION core.finance_artifact_visible(uuid,uuid,uuid,uuid,bigint,text,uuid,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_artifact_visible(uuid,uuid,uuid,uuid,bigint,text,uuid,text) TO household_app;

CREATE FUNCTION core.finance_audit_visible(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_audit bigint)
RETURNS boolean LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE item core.audit_log%ROWTYPE;head core.finance_origin_resources%ROWTYPE;generation bigint;binding core.finance_audit_bindings%ROWTYPE;classification core.financial_requirement_sets%ROWTYPE;
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
REVOKE ALL ON FUNCTION core.finance_audit_visible(uuid,uuid,uuid,uuid,bigint,bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_audit_visible(uuid,uuid,uuid,uuid,bigint,bigint) TO household_app;

CREATE FUNCTION core.finance_artifact_sources(p_kind text,p_row jsonb) RETURNS jsonb LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE refs jsonb='[]';parent core.attachments%ROWTYPE;result record;result_kind text;
BEGIN
 IF p_kind='attachment' AND p_row->>'module_id'='expenses' AND core.finance_domain_kind(p_row->>'resource_type') THEN
  refs=jsonb_build_array(jsonb_build_object('kind',p_row->>'resource_type','id',p_row->>'resource_id'));
 ELSIF p_kind='attachment_derivative' THEN refs=jsonb_build_array(jsonb_build_object('kind','attachment','id',p_row->>'attachment_id'));
 ELSIF p_kind='capture' THEN
  refs=jsonb_build_array(jsonb_build_object('kind','attachment','id',p_row->>'source_attachment_id'));
  IF p_row->>'result_resource_id' IS NOT NULL AND p_row->>'module_id'='expenses' THEN
   refs=refs||jsonb_build_array(jsonb_build_object('kind',CASE WHEN p_row->>'target_schema'='receipt_reviews' THEN 'receipt_reviews' ELSE p_row->>'target_schema' END,'id',p_row->>'result_resource_id'));
  END IF;
 ELSIF p_kind='import_batch' THEN
  IF p_row->>'account_id' IS NOT NULL THEN refs=refs||jsonb_build_array(jsonb_build_object('kind','accounts','id',p_row->>'account_id'));END IF;
  IF p_row->>'profile_id' IS NOT NULL THEN refs=refs||jsonb_build_array(jsonb_build_object('kind','bank_profiles','id',p_row->>'profile_id'));END IF;
  IF p_row->>'module_id'='expenses' THEN
   FOR result IN SELECT row_number,result_resource_id,normalized_values FROM core.import_rows WHERE batch_id=(p_row->>'id')::uuid AND result_resource_id IS NOT NULL ORDER BY row_number LOOP
    result_kind=CASE WHEN p_row->>'entity'='bank' THEN CASE WHEN(result.normalized_values->>'amount_minor')::bigint>0 THEN 'cash_events' ELSE 'entries' END ELSE p_row->>'entity' END;
    IF NOT core.finance_domain_kind(result_kind) THEN RAISE EXCEPTION 'unknown imported result kind' USING ERRCODE='42501';END IF;
    refs=refs||jsonb_build_array(jsonb_build_object('kind',result_kind,'id',result.result_resource_id));
   END LOOP;
  END IF;
 END IF;
 RETURN refs;
END $$;
REVOKE ALL ON FUNCTION core.finance_artifact_sources(text,jsonb) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_nonfinancial_parent(p_household uuid,p_row jsonb) RETURNS boolean LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE schema_name text=p_row->>'module_id';table_name text=p_row->>'resource_type';found boolean;
BEGIN
 IF schema_name NOT IN('shopping','fuel','recipes','pantry','chores','meters','documents','mealplan') OR table_name !~'^[a-z][a-z0-9_]{0,63}$'
  OR to_regclass(format('%I.%I',schema_name,table_name)) IS NULL THEN RETURN false;END IF;
 EXECUTE format('SELECT EXISTS(SELECT 1 FROM %I.%I WHERE household_id=$1 AND id=$2)',schema_name,table_name) INTO found USING p_household,(p_row->>'resource_id')::uuid;
 RETURN coalesce(found,false) AND EXISTS(SELECT 1 FROM core.module_states WHERE household_id=p_household AND module_id=schema_name AND state='enabled' AND data_generation=(p_row->>'module_generation')::bigint);
END $$;
REVOKE ALL ON FUNCTION core.finance_nonfinancial_parent(uuid,jsonb) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_artifact_scope() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE row_value jsonb;before_value jsonb;kind text;key uuid;household uuid;module_name text;generation bigint;row_key integer=0;version_now bigint;
 prior core.financial_artifact_scope_versions%ROWTYPE;head core.finance_origin_resources%ROWTYPE;operation core.finance_origin_operations%ROWTYPE;
 parent_scope core.financial_artifact_scope_versions%ROWTYPE;set_key uuid;refs jsonb;financial boolean=false;owner_key uuid;scope_key uuid;actor uuid;parent_row jsonb;
BEGIN
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 row_value=CASE WHEN TG_OP='DELETE' THEN to_jsonb(OLD) ELSE to_jsonb(NEW) END;before_value=CASE WHEN TG_OP='INSERT' THEN NULL ELSE to_jsonb(OLD) END;
 kind=CASE TG_TABLE_NAME WHEN 'attachments' THEN 'attachment' WHEN 'attachment_derivatives' THEN 'attachment_derivative' WHEN 'capture_jobs' THEN 'capture' WHEN 'import_batches' THEN 'import_batch' ELSE 'import_row' END;
 IF kind='import_row' THEN
  key=(row_value->>'batch_id')::uuid;row_key=(row_value->>'row_number')::integer;
  SELECT to_jsonb(b) INTO STRICT parent_row FROM core.import_batches b WHERE id=key;
  household=(parent_row->>'household_id')::uuid;module_name=parent_row->>'module_id';generation=(parent_row->>'module_generation')::bigint;
 ELSE
  key=(row_value->>'id')::uuid;
  IF kind='attachment_derivative' THEN
   SELECT to_jsonb(a) INTO STRICT parent_row FROM core.attachments a WHERE id=(row_value->>'attachment_id')::uuid;
   household=(parent_row->>'household_id')::uuid;module_name=parent_row->>'module_id';generation=(parent_row->>'module_generation')::bigint;
  ELSE household=(row_value->>'household_id')::uuid;module_name=row_value->>'module_id';generation=(row_value->>'module_generation')::bigint;END IF;
 END IF;
 SELECT * INTO prior FROM core.financial_artifact_scope_versions WHERE household_id=household AND artifact_kind=kind AND object_id=key
  AND row_number IS NOT DISTINCT FROM nullif(row_key,0) ORDER BY revision DESC LIMIT 1;
 SELECT * INTO head FROM core.finance_origin_resources WHERE household_id=household AND resource_type=kind AND resource_id=key AND row_number=row_key ORDER BY module_generation DESC LIMIT 1;
 IF TG_OP='INSERT' AND prior.id IS NOT NULL THEN RAISE EXCEPTION 'retained artifact identity cannot be reused' USING ERRCODE='23514';END IF;
 refs=CASE WHEN kind='import_row' THEN jsonb_build_array(jsonb_build_object('kind','import_batch','id',key)) ELSE core.finance_artifact_sources(kind,row_value) END;
 IF kind='import_row' AND module_name='expenses' AND row_value->>'result_resource_id' IS NOT NULL THEN
  refs=refs||jsonb_build_array(jsonb_build_object('kind',CASE WHEN parent_row->>'entity'='bank' THEN
   CASE WHEN(row_value->'normalized_values'->>'amount_minor')::bigint>0 THEN 'cash_events' ELSE 'entries' END ELSE parent_row->>'entity' END,'id',row_value->>'result_resource_id'));
 END IF;
 financial=module_name='expenses' AND(kind='import_batch' OR kind='import_row' OR kind='capture' OR kind='attachment' AND core.finance_domain_kind(row_value->>'resource_type'));
 financial=financial OR head.id IS NOT NULL OR EXISTS(SELECT 1 FROM jsonb_array_elements(refs) r JOIN core.finance_origin_resources o
  ON o.household_id=household AND o.resource_type=r->>'kind' AND o.resource_id=(r->>'id')::uuid AND o.row_number=0);
 IF TG_OP='DELETE' AND kind IN('capture','import_batch','import_row') AND nullif(current_setting('household.audit_kind',true),'')='worker'
  AND EXISTS(SELECT 1 FROM core.finance_artifact_job_authorities a JOIN core.jobs j ON j.id=a.job_id AND j.household_id=a.household_id
   AND j.kind='housekeeping' AND j.state='running' AND j.lease_token=a.lease_token AND j.lease_until>clock_timestamp()
   WHERE a.household_id=household AND a.authority_kind='retention' AND a.transaction_id=pg_current_xact_id_if_assigned() AND a.transaction_started_at=transaction_timestamp()) THEN
  IF head.id IS NOT NULL THEN
   IF head.row_hash<>sha256(convert_to(before_value::text,'UTF8')) THEN RAISE EXCEPTION 'retention OLD image mismatch' USING ERRCODE='42501';END IF;
   UPDATE core.finance_origin_resources SET retired_at=coalesce(retired_at,clock_timestamp()) WHERE id=head.id;
   UPDATE core.finance_private_owners SET retired_at=coalesce(retired_at,clock_timestamp()) WHERE id=head.private_owner_id;
  END IF;
  RETURN NULL;
 END IF;
 IF TG_OP='UPDATE' AND kind='attachment' AND row_value->>'state' IN('orphaned','purged')
  AND(row_value-'state')=(before_value-'state') AND NOT core.finance_domain_exists(household,row_value->>'resource_type',(row_value->>'resource_id')::uuid)
  AND(head.id IS NULL OR head.row_hash=sha256(convert_to(before_value::text,'UTF8'))) THEN
  UPDATE core.finance_origin_resources SET retired_at=coalesce(retired_at,clock_timestamp()),row_hash=sha256(convert_to(row_value::text,'UTF8')) WHERE id=head.id;
  RETURN NULL;
 END IF;
 IF TG_OP='DELETE' AND nullif(current_setting('household.audit_kind',true),'')='worker' AND EXISTS(
  SELECT 1 FROM core.file_deletions d WHERE d.household_id=household AND d.available_at<=clock_timestamp()
   AND((kind='attachment' AND d.attachment_id=key AND d.storage_key=row_value->>'storage_key' AND row_value->>'state' IN('orphaned','purged'))
    OR(kind IN('capture','attachment_derivative') AND EXISTS(SELECT 1 FROM core.attachments a WHERE a.id=d.attachment_id AND a.household_id=household
     AND a.state IN('orphaned','purged') AND a.id=CASE kind WHEN 'capture' THEN(row_value->>'source_attachment_id')::uuid
      WHEN 'attachment_derivative' THEN(row_value->>'attachment_id')::uuid END)))) THEN
  IF head.id IS NOT NULL AND head.row_hash<>sha256(convert_to(before_value::text,'UTF8')) THEN RAISE EXCEPTION 'physical retirement OLD image mismatch' USING ERRCODE='42501';END IF;
  UPDATE core.finance_origin_resources SET retired_at=coalesce(retired_at,clock_timestamp()) WHERE id=head.id;
  RETURN NULL;
 END IF;
 IF NOT financial THEN
  IF prior.id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM core.financial_requirement_sets s WHERE s.id=prior.set_id AND s.classification IN('nonfinancial','private_import')) THEN
   RAISE EXCEPTION 'unresolved artifact cannot be reclassified' USING ERRCODE='42501';END IF;
  IF kind='attachment' AND NOT core.finance_nonfinancial_parent(household,row_value) AND TG_OP<>'DELETE' THEN RAISE EXCEPTION 'nonfinancial attachment parent unavailable' USING ERRCODE='42501';END IF;
  IF kind IN('attachment_derivative','capture','import_row') THEN
   SELECT * INTO parent_scope FROM core.financial_artifact_scope_versions WHERE household_id=household
    AND artifact_kind=CASE WHEN kind='import_row' THEN 'import_batch' ELSE 'attachment' END
    AND object_id=CASE WHEN kind='import_row' THEN key WHEN kind='capture' THEN(row_value->>'source_attachment_id')::uuid ELSE(row_value->>'attachment_id')::uuid END ORDER BY revision DESC LIMIT 1;
   IF NOT FOUND THEN RAISE EXCEPTION 'artifact source classification missing' USING ERRCODE='42501';END IF;
   set_key=parent_scope.set_id;
  ELSIF kind='import_batch' THEN
   actor=(row_value->>'created_by')::uuid;
   IF actor IS NULL THEN actor=nullif(current_setting('household.audit_user',true),'')::uuid;END IF;
   IF actor IS NULL THEN RAISE EXCEPTION 'import creator required' USING ERRCODE='42501';END IF;
   INSERT INTO core.financial_requirement_sets(household_id,classification,required_count,creator_user_id,reason_code) VALUES(household,'private_import',0,actor,'private_import_preview') RETURNING id INTO set_key;
  ELSE
   INSERT INTO core.financial_requirement_sets(household_id,classification,required_count,reason_code) VALUES(household,'nonfinancial',0,'nonfinancial_explicit') RETURNING id INTO set_key;
  END IF;
 ELSE
  operation=core.finance_pending(household);
  IF operation.target_kind<>(CASE WHEN kind='import_row' THEN 'import_batch' ELSE kind END) OR operation.target_id<>key THEN
   RAISE EXCEPTION 'artifact requires its exact prepared root' USING ERRCODE='42501';END IF;
  IF TG_OP<>'INSERT' AND(head.id IS NULL OR head.retired_at IS NOT NULL OR head.row_hash<>sha256(convert_to(before_value::text,'UTF8'))) THEN
   RAISE EXCEPTION 'artifact OLD image is not proven' USING ERRCODE='42501';END IF;
  IF TG_OP='INSERT' AND head.id IS NOT NULL THEN RAISE EXCEPTION 'artifact identity cannot be reused' USING ERRCODE='23514';END IF;
  set_key=core.finance_requirements(household,operation.actor_user_id,operation.membership_id,operation.session_id,operation.auth_version,operation.expenses_generation,refs,coalesce(head.set_id,operation.inherited_set_id));
  owner_key=head.private_owner_id;
  IF TG_OP='INSERT' AND kind IN('capture','import_batch','import_row') THEN
   INSERT INTO core.finance_private_owners(household_id,resource_type,resource_id,row_number,module_generation,owner_user_id)
   VALUES(household,kind,key,row_key,operation.expenses_generation,operation.actor_user_id) RETURNING id INTO owner_key;
  END IF;
 END IF;
 INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,row_number,revision,previous_id,set_id,source_module,source_generation)
 VALUES(household,kind,key,nullif(row_key,0),coalesce(prior.revision+1,1),prior.id,set_key,module_name,generation) RETURNING id INTO scope_key;
 IF financial THEN
  version_now=coalesce((row_value->>'version')::bigint,coalesce(head.resource_version,0)+1);
  INSERT INTO core.finance_origin_resources(household_id,resource_type,resource_id,row_number,module_generation,private_owner_id,root_id,set_id,audit_set_id,scope_id,resource_version,row_hash,retired_at)
  VALUES(household,kind,key,row_key,operation.expenses_generation,owner_key,operation.id,set_key,set_key,scope_key,version_now,
   sha256(convert_to(row_value::text,'UTF8')),CASE WHEN TG_OP='DELETE' THEN clock_timestamp() END)
  ON CONFLICT DO NOTHING;
  IF head.id IS NOT NULL THEN UPDATE core.finance_origin_resources SET root_id=operation.id,set_id=set_key,audit_set_id=set_key,scope_id=scope_key,
   resource_version=version_now,row_hash=sha256(convert_to(row_value::text,'UTF8')),retired_at=CASE WHEN TG_OP='DELETE' THEN clock_timestamp() END WHERE id=head.id;END IF;
  INSERT INTO core.finance_origin_effects(household_id,root_id,resource_type,resource_id,row_number,effect,before_hash,after_hash,resource_version)
  VALUES(household,operation.id,kind,key,row_key,lower(TG_OP),CASE WHEN before_value IS NOT NULL THEN sha256(convert_to(before_value::text,'UTF8')) END,
   CASE WHEN TG_OP<>'DELETE' THEN sha256(convert_to(row_value::text,'UTF8')) END,version_now);
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.finance_artifact_scope() FROM PUBLIC,household_app;
DO $artifacts$ DECLARE relation text;BEGIN
 FOREACH relation IN ARRAY ARRAY['attachments','attachment_derivatives','capture_jobs','import_batches','import_rows'] LOOP
  EXECUTE format('CREATE TRIGGER financial_visibility_scope AFTER INSERT OR UPDATE OR DELETE ON core.%I FOR EACH ROW EXECUTE FUNCTION core.finance_artifact_scope()',relation);
 END LOOP;
END $artifacts$;

CREATE FUNCTION core.finance_artifact_live(p_household uuid,p_kind text,p_id uuid) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE valid boolean;
BEGIN
 CASE p_kind
 WHEN 'attachment' THEN SELECT EXISTS(SELECT 1 FROM core.attachments a JOIN core.module_states s ON s.household_id=a.household_id AND s.module_id=a.module_id
  AND s.state='enabled' AND s.data_generation=a.module_generation WHERE a.household_id=p_household AND a.id=p_id AND a.state='ready') INTO valid;
 WHEN 'attachment_derivative' THEN SELECT EXISTS(SELECT 1 FROM core.attachment_derivatives d JOIN core.attachments a ON a.id=d.attachment_id
  JOIN core.module_states s ON s.household_id=a.household_id AND s.module_id=a.module_id AND s.state='enabled' AND s.data_generation=a.module_generation
  WHERE a.household_id=p_household AND d.id=p_id AND a.state='ready') INTO valid;
 WHEN 'capture' THEN SELECT EXISTS(SELECT 1 FROM core.capture_jobs c JOIN core.attachments a ON a.id=c.source_attachment_id AND a.household_id=c.household_id AND a.state='ready'
  JOIN core.module_states s ON s.household_id=c.household_id AND s.module_id=c.module_id AND s.state='enabled' AND s.data_generation=c.module_generation
  WHERE c.household_id=p_household AND c.id=p_id AND c.expires_at>transaction_timestamp()) INTO valid;
 WHEN 'import_batch' THEN SELECT EXISTS(SELECT 1 FROM core.import_batches b JOIN core.module_states s ON s.household_id=b.household_id AND s.module_id=b.module_id
  AND s.state='enabled' AND s.data_generation=b.module_generation WHERE b.household_id=p_household AND b.id=p_id
  AND(b.expires_at IS NULL OR b.expires_at>transaction_timestamp() OR b.state='confirmed')) INTO valid;
 ELSE RETURN false;
 END CASE;
 RETURN coalesce(valid,false);
END $$;
REVOKE ALL ON FUNCTION core.finance_artifact_live(uuid,text,uuid) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_prepare_artifact_job(p_job uuid,p_lease uuid,p_kind text,p_target uuid) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE job core.jobs%ROWTYPE;attachment uuid;head core.finance_origin_resources%ROWTYPE;original core.finance_origin_operations%ROWTYPE;generation bigint;
BEGIN
 IF nullif(current_setting('household.audit_kind',true),'') IS DISTINCT FROM 'worker' OR nullif(current_setting('household.audit_user',true),'') IS NOT NULL THEN
  RAISE EXCEPTION 'actual artifact worker required' USING ERRCODE='42501';END IF;
 SELECT * INTO job FROM core.jobs WHERE id=p_job AND lease_token=p_lease AND state='running' AND lease_until>clock_timestamp() AND kind IN('thumbnail','capture_ocr') FOR UPDATE;
 IF NOT FOUND OR(job.kind='thumbnail')<>(p_kind='attachment_derivative') OR p_kind NOT IN('attachment_derivative','capture') THEN RAISE EXCEPTION 'artifact job mismatch' USING ERRCODE='42501';END IF;
 IF job.kind='thumbnail' THEN attachment=(job.payload->>'attachment_id')::uuid;
 ELSE
  IF job.payload->>'capture_id' IS DISTINCT FROM p_target::text THEN RAISE EXCEPTION 'capture job target mismatch' USING ERRCODE='42501';END IF;
  SELECT source_attachment_id INTO attachment FROM core.capture_jobs WHERE household_id=job.household_id AND id=p_target AND module_id=job.module_id AND module_generation=job.module_generation;
 END IF;
 SELECT data_generation INTO generation FROM core.module_states WHERE household_id=job.household_id AND module_id='expenses' AND state='enabled';
 SELECT * INTO head FROM core.finance_origin_resources WHERE household_id=job.household_id AND module_generation=generation AND resource_type='attachment' AND resource_id=attachment AND retired_at IS NULL;
 IF head.id IS NULL THEN RETURN NULL;END IF;
 SELECT * INTO STRICT original FROM core.finance_origin_operations WHERE household_id=job.household_id AND id=head.root_id AND state='complete';
 PERFORM set_config('household.audit_household',job.household_id::text,true);
 INSERT INTO core.finance_artifact_job_authorities(household_id,job_id,lease_token,source_attachment_id,actor_user_id,membership_id,auth_version,expenses_generation,transaction_id,transaction_started_at)
 VALUES(job.household_id,p_job,p_lease,attachment,original.actor_user_id,original.membership_id,original.auth_version,generation,pg_current_xact_id(),transaction_timestamp());
 RETURN core.finance_origin_begin(job.household_id,original.actor_user_id,original.membership_id,NULL,original.auth_version,generation,p_kind,p_target,'capture',
  jsonb_build_array(jsonb_build_object('kind','attachment','id',attachment)),NULL);
END $$;
REVOKE ALL ON FUNCTION core.finance_prepare_artifact_job(uuid,uuid,text,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_prepare_artifact_job(uuid,uuid,text,uuid) TO household_app;

CREATE FUNCTION core.finance_finish_artifact_job(p_operation uuid) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE operation core.finance_origin_operations%ROWTYPE;
BEGIN
 IF p_operation IS NULL THEN RETURN;END IF;
 SELECT * INTO STRICT operation FROM core.finance_origin_operations WHERE id=p_operation;
 IF operation.target_kind NOT IN('attachment_derivative','capture') OR operation.session_id IS NOT NULL OR operation.authority_id IS NULL THEN
  RAISE EXCEPTION 'artifact authority required' USING ERRCODE='42501';END IF;
 PERFORM core.finance_origin_finish(operation.id,operation.household_id,operation.actor_user_id,operation.membership_id,NULL,operation.auth_version,operation.expenses_generation);
END $$;
REVOKE ALL ON FUNCTION core.finance_finish_artifact_job(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_finish_artifact_job(uuid) TO household_app;

CREATE FUNCTION core.finance_artifact_row(p_household uuid,p_kind text,p_id uuid,p_number integer DEFAULT 0) RETURNS jsonb
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE item jsonb;
BEGIN
 CASE p_kind
 WHEN 'attachment' THEN SELECT to_jsonb(t) INTO item FROM core.attachments t WHERE household_id=p_household AND id=p_id;
 WHEN 'attachment_derivative' THEN SELECT to_jsonb(t) INTO item FROM core.attachment_derivatives t JOIN core.attachments a ON a.id=t.attachment_id WHERE a.household_id=p_household AND t.id=p_id;
 WHEN 'capture' THEN SELECT to_jsonb(t) INTO item FROM core.capture_jobs t WHERE household_id=p_household AND id=p_id;
 WHEN 'import_batch' THEN SELECT to_jsonb(t) INTO item FROM core.import_batches t WHERE household_id=p_household AND id=p_id;
 WHEN 'import_row' THEN SELECT to_jsonb(t) INTO item FROM core.import_rows t JOIN core.import_batches b ON b.id=t.batch_id WHERE b.household_id=p_household AND t.batch_id=p_id AND t.row_number=p_number;
 ELSE RETURN NULL;END CASE;
 RETURN item;
END $$;
REVOKE ALL ON FUNCTION core.finance_artifact_row(uuid,text,uuid,integer) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_artifact_job_allowed(p_job uuid,p_lease uuid) RETURNS boolean LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE job core.jobs%ROWTYPE;attachment uuid;head core.finance_origin_resources%ROWTYPE;original core.finance_origin_operations%ROWTYPE;generation bigint;scope core.financial_artifact_scope_versions%ROWTYPE;
BEGIN
 IF nullif(current_setting('household.audit_kind',true),'') IS DISTINCT FROM 'worker' OR nullif(current_setting('household.audit_user',true),'') IS NOT NULL THEN RETURN false;END IF;
 SELECT * INTO job FROM core.jobs WHERE id=p_job AND lease_token=p_lease AND state='running' AND lease_until>clock_timestamp() AND kind IN('thumbnail','capture_ocr');
 IF NOT FOUND THEN RETURN false;END IF;
 IF job.kind='thumbnail' THEN attachment=(job.payload->>'attachment_id')::uuid;
 ELSE SELECT source_attachment_id INTO attachment FROM core.capture_jobs WHERE id=(job.payload->>'capture_id')::uuid AND household_id=job.household_id AND module_id=job.module_id AND module_generation=job.module_generation;END IF;
 IF NOT core.finance_artifact_live(job.household_id,'attachment',attachment) THEN RETURN false;END IF;
 SELECT * INTO head FROM core.finance_origin_resources WHERE household_id=job.household_id AND resource_type='attachment' AND resource_id=attachment ORDER BY module_generation DESC LIMIT 1;
 IF head.id IS NULL THEN
  SELECT * INTO scope FROM core.financial_artifact_scope_versions WHERE household_id=job.household_id AND artifact_kind='attachment' AND object_id=attachment ORDER BY revision DESC LIMIT 1;
  RETURN EXISTS(SELECT 1 FROM core.financial_requirement_sets WHERE household_id=job.household_id AND id=scope.set_id AND classification='nonfinancial' AND required_count=0);
 END IF;
 SELECT data_generation INTO generation FROM core.module_states WHERE household_id=job.household_id AND module_id='expenses' AND state='enabled';
 IF generation IS DISTINCT FROM head.module_generation OR head.retired_at IS NOT NULL THEN RETURN false;END IF;
 SELECT * INTO original FROM core.finance_origin_operations WHERE household_id=job.household_id AND id=head.root_id AND state='complete';
 RETURN EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=job.household_id AND m.id=original.membership_id
  AND m.user_id=original.actor_user_id AND m.inactive_at IS NULL AND m.role IN('admin','editor') AND u.disabled_at IS NULL AND NOT u.must_change_password AND u.auth_version=original.auth_version)
  AND core.finance_set_visible(job.household_id,head.set_id,original.actor_user_id,original.membership_id,generation,true);
END $$;
REVOKE ALL ON FUNCTION core.finance_artifact_job_allowed(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_artifact_job_allowed(uuid,uuid) TO household_app;

CREATE FUNCTION core.finance_prune_artifacts(p_job uuid,p_lease uuid) RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE job core.jobs%ROWTYPE;total integer=0;affected integer;batch_ids uuid[];
BEGIN
 IF nullif(current_setting('household.audit_kind',true),'') IS DISTINCT FROM 'worker' OR nullif(current_setting('household.audit_user',true),'') IS NOT NULL THEN
  RAISE EXCEPTION 'actual retention worker required' USING ERRCODE='42501';END IF;
 SELECT * INTO job FROM core.jobs WHERE id=p_job AND lease_token=p_lease AND state='running' AND lease_until>clock_timestamp() AND kind='housekeeping' AND module_id IS NULL FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'current retention lease required' USING ERRCODE='42501';END IF;
 PERFORM set_config('household.audit_household',job.household_id::text,true);
 INSERT INTO core.finance_artifact_job_authorities(household_id,job_id,lease_token,authority_kind,transaction_id,transaction_started_at)
 VALUES(job.household_id,p_job,p_lease,'retention',pg_current_xact_id(),transaction_timestamp());
 DELETE FROM core.capture_jobs WHERE id IN(SELECT c.id FROM core.capture_jobs c WHERE c.household_id=job.household_id AND c.expires_at<=clock_timestamp() AND c.state<>'confirmed'
  ORDER BY c.expires_at,c.id LIMIT 100 FOR UPDATE SKIP LOCKED);
 GET DIAGNOSTICS affected=ROW_COUNT;total=total+affected;
 SELECT coalesce(array_agg(id),ARRAY[]::uuid[]) INTO batch_ids FROM(SELECT b.id FROM core.import_batches b
  WHERE b.household_id=job.household_id AND b.purpose IN('finance_bank','recipe_review') AND b.expires_at<=clock_timestamp()
  ORDER BY b.expires_at,b.id LIMIT 100 FOR UPDATE SKIP LOCKED) selected;
 -- Retire typed children while the exact parent still exists. The artifact
 -- trigger does not gain an absent-parent fallback or broader worker authority.
 DELETE FROM core.import_rows WHERE batch_id=ANY(batch_ids);
 DELETE FROM core.import_batches WHERE id=ANY(batch_ids);
 GET DIAGNOSTICS affected=ROW_COUNT;total=total+affected;
 SELECT coalesce(array_agg(id),ARRAY[]::uuid[]) INTO batch_ids FROM(SELECT b.id FROM core.import_batches b
  WHERE b.household_id=job.household_id AND b.purpose='flat_csv' AND b.created_at<clock_timestamp()-interval '30 days' AND b.state='preview'
  ORDER BY b.created_at,b.id LIMIT 100 FOR UPDATE SKIP LOCKED) selected;
 DELETE FROM core.import_rows WHERE batch_id=ANY(batch_ids);
 DELETE FROM core.import_batches WHERE id=ANY(batch_ids);
 GET DIAGNOSTICS affected=ROW_COUNT;RETURN total+affected;
END $$;
REVOKE ALL ON FUNCTION core.finance_prune_artifacts(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_prune_artifacts(uuid,uuid) TO household_app;

CREATE FUNCTION core.finance_lock_artifact(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_kind text,p_id uuid,p_intent text)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE set_key uuid;
BEGIN
 IF NOT core.finance_artifact_visible(p_household,p_user,p_membership,p_session,p_auth,p_kind,p_id,p_intent) THEN RETURN false;END IF;
 PERFORM id FROM core.households WHERE id=p_household FOR SHARE;
 PERFORM id FROM core.users WHERE id=p_user AND auth_version=p_auth AND disabled_at IS NULL FOR SHARE;
 PERFORM id FROM core.memberships WHERE household_id=p_household AND id=p_membership AND user_id=p_user AND inactive_at IS NULL FOR SHARE;
 PERFORM id FROM core.sessions WHERE id=p_session AND user_id=p_user AND auth_version=p_auth AND revoked_at IS NULL FOR SHARE;
 PERFORM module_id FROM core.module_states WHERE household_id=p_household ORDER BY module_id FOR SHARE;
 SELECT a.set_id INTO set_key FROM core.financial_artifact_scope_versions a WHERE a.household_id=p_household AND a.artifact_kind=p_kind AND a.object_id=p_id ORDER BY revision DESC LIMIT 1;
 PERFORM p.id FROM core.financial_policies p WHERE p.household_id=p_household AND p.id IN(
  SELECT policy_id FROM core.financial_requirement_members WHERE household_id=p_household AND set_id=set_key) ORDER BY p.id FOR SHARE;
 RETURN core.finance_artifact_visible(p_household,p_user,p_membership,p_session,p_auth,p_kind,p_id,p_intent);
END $$;
REVOKE ALL ON FUNCTION core.finance_lock_artifact(uuid,uuid,uuid,uuid,bigint,text,uuid,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_lock_artifact(uuid,uuid,uuid,uuid,bigint,text,uuid,text) TO household_app;

DO $old_guards$
DECLARE name text;body text;
BEGIN
 FOREACH name IN ARRAY ARRAY['expenses.protect_posted()','expenses.touch_entry_after_line()'] LOOP
  SELECT p.prosrc INTO body FROM pg_proc p WHERE p.oid=to_regprocedure(name);
  IF position('IF core.finance_real_maintenance() THEN' IN body)=0 OR position('IF current_user=''household_owner'' THEN' IN body)>0 THEN
   RAISE EXCEPTION 'Finance Expense guard must precede catalog capture';END IF;
 END LOOP;
END $old_guards$;

INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('expenses','accounts',ARRAY['id'],ARRAY['account_type','currency_code','currency_exponent','archived_at','version'],NULL),
 ('expenses','entry_finance',ARRAY['id'],ARRAY['entry_id','original_state','original_currency','original_exponent','original_minor','fx_numerator','fx_denominator','fx_effective_on','fx_source','fx_rounding','fx_reviewed_by','fx_reviewed_at','account_id','payment_currency','payment_exponent','payment_minor','payment_fx_numerator','payment_fx_denominator','payment_fx_on','payment_base_minor','payment_difference_reviewed','version'],NULL),
 ('expenses','entry_original_lines',ARRAY['id'],ARRAY['entry_id','line_id','original_minor','allocation_rule','version'],NULL),
 ('expenses','cash_events',ARRAY['id'],ARRAY['event_kind','state','occurred_on','payee','entry_id','account_id','original_currency','original_exponent','original_minor','base_minor','fx_numerator','fx_denominator','fx_effective_on','reverses_event_id','posted_at','version'],NULL),
 ('expenses','account_movements',ARRAY['id'],ARRAY['event_id','account_id','leg','currency_code','currency_exponent','amount_minor','base_minor','fx_numerator','fx_denominator','fx_effective_on','version'],NULL),
 ('expenses','entry_relations',ARRAY['id'],ARRAY['entry_id','original_entry_id','relation','reviewed_original_version','reversal_entry_id','version'],NULL),
 ('expenses','entry_split_sets',ARRAY['id'],ARRAY['entry_id','method','currency_code','currency_exponent','total_minor','payer_membership_id','algorithm','reviewed_at','reviewed_by','active','version'],NULL),
 ('expenses','entry_person_shares',ARRAY['id'],ARRAY['split_set_id','entry_id','membership_id','share_minor','base_minor','position','version'],NULL),
 ('expenses','settlement_details',ARRAY['id'],ARRAY['event_id','from_membership_id','to_membership_id','currency_code','currency_exponent','amount_minor','version'],NULL),
 ('expenses','saved_filters',ARRAY['id'],ARRAY['owner_user_id','criteria_version','version'],NULL),
 ('expenses','category_budgets',ARRAY['id'],ARRAY['category_id','starts_on','ends_on','monthly_minor','carry_rule','initial_carry_minor','archived_at','version'],NULL),
 ('expenses','savings_goals',ARRAY['id'],ARRAY['currency_code','currency_exponent','target_minor','target_on','account_id','planned_period_minor','planned_period','state','version'],NULL),
 ('expenses','goal_contributions',ARRAY['id'],ARRAY['goal_id','occurred_on','amount_minor','contribution_kind','event_id','movement_id','reverses_contribution_id','version'],NULL),
 ('expenses','recurring_templates',ARRAY['id'],ARRAY['anchor_on','ends_on','repeat_unit','repeat_interval','monthday_policy','template_kind','account_id','active','version'],NULL),
 ('expenses','recurring_occurrences',ARRAY['id'],ARRAY['template_id','scheduled_on','template_version','entry_id','event_id','state','version'],NULL),
 ('expenses','categorization_rules',ARRAY['id'],ARRAY['priority','active','match_kind','currency_code','minimum_minor','maximum_minor','category_id','account_id','version'],NULL),
 ('expenses','bank_profiles',ARRAY['id'],ARRAY['owner_user_id','account_id','encoding','date_layout','decimal_separator','amount_mode','profile_format','archived_at','version'],NULL),
 ('expenses','reconciliations',ARRAY['id'],ARRAY['account_id','starts_on','opening_minor','statement_on','statement_minor','currency_code','currency_exponent','computed_minor','difference_minor','account_version','posting_count','state','confirmed_by','confirmed_at','reopens_id','version'],NULL),
 ('expenses','reconciliation_items',ARRAY['id'],ARRAY['reconciliation_id','movement_id','reviewed_event_version','reviewed_movement_version','amount_minor','version'],NULL),
 ('expenses','receipt_reviews',ARRAY['id'],ARRAY['entry_id','capture_job_id','attachment_id','source_version','currency_code','currency_exponent','receipt_total_minor','line_total_minor','difference_minor','difference_acknowledged','reviewed_entry_version','reviewed_entry_original_minor','reviewed_entry_currency','reviewed_entry_exponent','entry_difference_minor','state','confirmed_by','confirmed_at','version'],NULL),
 ('expenses','receipt_review_lines',ARRAY['id'],ARRAY['review_id','position','amount_minor','source_kind','page_number','source_line','confidence','entry_line_id','reviewed','version'],NULL);

CREATE OR REPLACE FUNCTION expenses.financial_bindings_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT core.financial_foundation_ready() AND NOT EXISTS(SELECT 1 FROM(VALUES('entries'),('entry_lines'),('accounts'),('entry_finance'),('entry_original_lines'),('cash_events'),
 ('account_movements'),('entry_relations'),('entry_split_sets'),('entry_person_shares'),('settlement_details'),('saved_filters'),('category_budgets'),('savings_goals'),
 ('goal_contributions'),('recurring_templates'),('recurring_occurrences'),('categorization_rules'),('bank_profiles'),('reconciliations'),('reconciliation_items'),('receipt_reviews'),('receipt_review_lines')) x(name)
 WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass('expenses.'||x.name) AND t.tgname='financial_subject' AND t.tgfoid='core.finance_bind_subject()'::regprocedure
  AND t.tgtype=13 AND t.tgenabled IN('O','A')) OR NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass('expenses.'||x.name) AND t.tgname='financial_visibility_scope'
  AND t.tgfoid='core.finance_domain_scope()'::regprocedure AND t.tgtype=29 AND t.tgenabled IN('O','A')))
$$;

CREATE FUNCTION core.finance_producers_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT core.finance_authority_ready() AND expenses.finance_ready() AND expenses.financial_bindings_ready()
 AND NOT EXISTS(SELECT 1 FROM(VALUES('attachments'),('attachment_derivatives'),('capture_jobs'),('import_batches'),('import_rows')) x(name)
 WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass('core.'||x.name) AND t.tgname='financial_visibility_scope' AND t.tgfoid='core.finance_artifact_scope()'::regprocedure
  AND t.tgtype=29 AND t.tgenabled IN('O','A')))
 AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid='core.audit_log'::regclass AND t.tgname='finance_audit_binding' AND t.tgfoid='core.finance_bind_audit()'::regprocedure AND t.tgenabled IN('O','A'))
 AND(SELECT count(*) FROM core.audit_policies)=121
$$;
REVOKE ALL ON FUNCTION core.finance_producers_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_producers_ready() TO household_app;
COMMIT;
