BEGIN;
SET LOCAL search_path=pg_catalog;
SET LOCAL timezone='UTC';
SELECT id FROM core.households ORDER BY id FOR UPDATE;
DO $predecessor$ BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=30 AND NOT dirty OR version=31 AND dirty) FROM core.schema_migrations) OR NOT core.finance_producers_ready() THEN
  RAISE EXCEPTION 'selected Finance requires complete reviewed producers';END IF;
END $predecessor$;

-- A proven old atomic editor payload can establish a manual source. Neither an
-- owner-created row nor a missing producer marker is such proof. Derived links,
-- captures, imports and old correction chains remain unresolved here.
DO $history$
DECLARE entry record;line record;proof record;root uuid;set_key uuid;audience uuid;subject_key uuid;scope_key uuid;member uuid;auth_version bigint;origin text;
BEGIN
 FOR entry IN SELECT e.*,s.data_generation FROM expenses.entries e JOIN core.module_states s ON s.household_id=e.household_id AND s.module_id='expenses' ORDER BY e.household_id,e.id LOOP
  SELECT o.id operation_id,a.id audit_id,o.created_by INTO proof FROM core.expense_edit_operations o
   JOIN core.audit_log receipt ON receipt.household_id=o.household_id AND receipt.module_id='core' AND receipt.resource_type='expense_edit_operations' AND receipt.resource_id=o.id
    AND receipt.action='CREATE' AND receipt.db_role='household_app' AND receipt.actor_kind='user' AND receipt.actor_user_id=o.created_by AND receipt.request_id IS NOT NULL
    AND receipt.changes->'entry_id'->>'after'=o.entry_id::text AND receipt.changes->'result_version'->>'after'=o.result_version::text
   JOIN core.audit_log a
   ON a.household_id=o.household_id AND a.module_id='expenses' AND a.resource_type='entries' AND a.resource_id=o.entry_id AND a.action IN('CREATE','UPDATE')
   AND a.actor_kind='user' AND a.actor_user_id=o.created_by AND a.db_role='household_app' AND a.request_id=receipt.request_id
   AND a.changes->'version'->>'after'=o.result_version::text
   WHERE o.household_id=entry.household_id AND o.entry_id=entry.id AND o.expenses_generation=entry.data_generation AND o.result_version=entry.version
    AND entry.reverses_entry_id IS NULL AND entry.replaces_entry_id IS NULL
    AND NOT EXISTS(SELECT 1 FROM core.resource_links l WHERE l.household_id=entry.household_id AND(l.source_module='expenses' AND l.source_id=entry.id OR l.target_module='expenses' AND l.target_id=entry.id))
    AND NOT EXISTS(SELECT 1 FROM core.capture_jobs c WHERE c.household_id=entry.household_id AND c.result_resource_id=entry.id)
    AND NOT EXISTS(SELECT 1 FROM core.import_rows r JOIN core.import_batches b ON b.id=r.batch_id WHERE b.household_id=entry.household_id AND r.result_resource_id=entry.id)
   ORDER BY o.created_at DESC,o.id,a.id DESC LIMIT 1;
  origin=CASE WHEN proof.operation_id IS NULL THEN 'legacy_unresolved' ELSE 'manual' END;
  SELECT m.id,u.auth_version INTO member,auth_version FROM core.users u LEFT JOIN core.memberships m ON m.household_id=entry.household_id AND m.user_id=u.id
   WHERE u.id=coalesce(proof.created_by,entry.created_by);
  IF member IS NULL THEN origin='legacy_unresolved';END IF;
  INSERT INTO core.financial_requirement_sets(household_id,classification,required_count,reason_code) VALUES(entry.household_id,
   CASE WHEN origin='legacy_unresolved' THEN 'unresolved' ELSE 'nonfinancial' END,0,CASE WHEN origin='legacy_unresolved' THEN 'unresolved_legacy' ELSE 'nonfinancial_explicit' END) RETURNING id INTO set_key;
  INSERT INTO core.finance_origin_operations(household_id,transaction_id,transaction_started_at,expenses_generation,actor_user_id,membership_id,auth_version,target_kind,target_id,origin,
   legacy_operation_id,legacy_audit_id,source_refs,inherited_set_id,state,depth,completed_at)
  VALUES(entry.household_id,pg_current_xact_id(),transaction_timestamp(),entry.data_generation,coalesce(proof.created_by,entry.created_by),member,auth_version,'entries',entry.id,origin,
   proof.operation_id,proof.audit_id,'[]',set_key,'complete',1,clock_timestamp()) RETURNING id INTO root;
  SELECT id INTO STRICT subject_key FROM core.financial_subjects WHERE household_id=entry.household_id AND module_generation=entry.data_generation AND resource_type='entries' AND resource_id=entry.id AND retired_at IS NULL;
  audience=core.finance_audience_set(entry.household_id,set_key,subject_key);
  INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,revision,set_id,source_module,source_generation)
   VALUES(entry.household_id,'financial_subject',subject_key,1,set_key,'expenses',entry.data_generation) RETURNING id INTO scope_key;
  INSERT INTO core.finance_origin_resources(household_id,resource_type,resource_id,module_generation,subject_id,root_id,set_id,audit_set_id,scope_id,resource_version,row_hash)
   VALUES(entry.household_id,'entries',entry.id,entry.data_generation,subject_key,root,set_key,audience,scope_key,entry.version,sha256(convert_to((to_jsonb(entry)-'data_generation')::text,'UTF8')));
  FOR line IN SELECT * FROM expenses.entry_lines WHERE household_id=entry.household_id AND entry_id=entry.id ORDER BY id LOOP
   SELECT id INTO STRICT subject_key FROM core.financial_subjects WHERE household_id=entry.household_id AND module_generation=entry.data_generation AND resource_type='entry_lines' AND resource_id=line.id AND retired_at IS NULL;
   audience=core.finance_audience_set(entry.household_id,set_key,subject_key);
   INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,revision,set_id,source_module,source_generation)
    VALUES(entry.household_id,'financial_subject',subject_key,1,set_key,'expenses',entry.data_generation) RETURNING id INTO scope_key;
   INSERT INTO core.finance_origin_resources(household_id,resource_type,resource_id,module_generation,subject_id,root_id,set_id,audit_set_id,scope_id,resource_version,row_hash)
    VALUES(entry.household_id,'entry_lines',line.id,entry.data_generation,subject_key,root,set_key,audience,scope_key,line.version,sha256(convert_to(to_jsonb(line)::text,'UTF8')));
  END LOOP;
 END LOOP;
END $history$;

-- Older Core derivatives are classified only from a complete existing typed
-- financial source. Other missing historical paths stay closed, including
-- attachments which might have been rebound before scope tracking existed.
DO $artifacts$
DECLARE item record;key uuid;known boolean;creator uuid;parent_key uuid;parent_set uuid;classification text;
BEGIN
 FOR item IN SELECT a.household_id,'attachment'::text kind,a.id,a.module_id,a.module_generation FROM core.attachments a
  UNION ALL SELECT a.household_id,'attachment_derivative',d.id,a.module_id,a.module_generation FROM core.attachment_derivatives d JOIN core.attachments a ON a.id=d.attachment_id
  UNION ALL SELECT c.household_id,'capture',c.id,c.module_id,c.module_generation FROM core.capture_jobs c
  UNION ALL SELECT b.household_id,'import_batch',b.id,b.module_id,b.module_generation FROM core.import_batches b LOOP
  IF NOT EXISTS(SELECT 1 FROM core.financial_artifact_scope_versions s WHERE s.household_id=item.household_id AND s.artifact_kind=item.kind AND s.object_id=item.id) THEN
   known=false;creator=NULL;parent_set=NULL;
   IF item.module_id<>'expenses' THEN
    IF item.kind='attachment' THEN
     known=EXISTS(SELECT 1 FROM core.audit_log a WHERE a.household_id=item.household_id AND a.module_id='core' AND a.resource_type='attachments' AND a.resource_id=item.id
      AND a.action='CREATE' AND a.db_role='household_app' AND a.actor_kind='user' AND a.changes->'module_id'->>'after'=item.module_id
      AND a.changes->'module_generation'->>'after'=item.module_generation::text)
      AND(SELECT count(*) FROM core.audit_log a WHERE a.household_id=item.household_id AND a.module_id='core' AND a.resource_type='attachments' AND a.resource_id=item.id AND a.action='CREATE')=1
      AND NOT EXISTS(SELECT 1 FROM core.audit_log a WHERE a.household_id=item.household_id AND a.module_id='core' AND a.resource_type='attachments' AND a.resource_id=item.id
       AND(a.db_role<>'household_app' OR a.actor_kind<>'user' OR a.action='DELETE' OR a.changes->'module_id'->>'before'='expenses' OR a.changes->'module_id'->>'after'='expenses'));
    ELSIF item.kind IN('attachment_derivative','capture') THEN
     IF item.kind='capture' THEN SELECT source_attachment_id INTO parent_key FROM core.capture_jobs WHERE id=item.id;
     ELSE SELECT attachment_id INTO parent_key FROM core.attachment_derivatives WHERE id=item.id;END IF;
     SELECT a.set_id INTO parent_set FROM core.financial_artifact_scope_versions a JOIN core.financial_requirement_sets s ON s.id=a.set_id AND s.household_id=a.household_id
      WHERE a.household_id=item.household_id AND a.artifact_kind='attachment' AND a.object_id=parent_key AND s.classification='nonfinancial' AND s.required_count=0 ORDER BY a.revision DESC LIMIT 1;
     known=parent_set IS NOT NULL AND EXISTS(SELECT 1 FROM core.audit_log a WHERE a.household_id=item.household_id AND a.module_id='core'
      AND a.resource_type=CASE item.kind WHEN 'capture' THEN 'capture_jobs' ELSE 'attachment_derivatives' END AND a.resource_id=item.id AND a.action='CREATE'
      AND a.db_role='household_app' AND a.actor_kind IN('user','worker') AND a.changes->CASE item.kind WHEN 'capture' THEN 'source_attachment_id' ELSE 'attachment_id' END->>'after'=parent_key::text)
      AND NOT EXISTS(SELECT 1 FROM core.audit_log a WHERE a.household_id=item.household_id AND a.module_id='core'
       AND a.resource_type=CASE item.kind WHEN 'capture' THEN 'capture_jobs' ELSE 'attachment_derivatives' END AND a.resource_id=item.id AND a.action<>'CREATE'
       AND(a.action='DELETE' OR a.changes ? CASE item.kind WHEN 'capture' THEN 'source_attachment_id' ELSE 'attachment_id' END));
    ELSIF item.kind='import_batch' THEN
     SELECT a.actor_user_id INTO creator FROM core.audit_log a WHERE a.household_id=item.household_id AND a.module_id='core' AND a.resource_type='import_batches' AND a.resource_id=item.id
      AND a.action='CREATE' AND a.db_role='household_app' AND a.actor_kind='user' AND a.changes->'module_id'->>'after'=item.module_id
      AND EXISTS(SELECT 1 FROM core.memberships m WHERE m.household_id=item.household_id AND m.user_id=a.actor_user_id)
      AND NOT EXISTS(SELECT 1 FROM core.audit_log b WHERE b.household_id=a.household_id AND b.module_id='core' AND b.resource_type='import_batches' AND b.resource_id=a.resource_id
       AND(b.db_role<>'household_app' OR b.actor_kind<>'user' OR b.changes->'module_id'->>'before'='expenses' OR b.changes->'module_id'->>'after'='expenses'))
      ORDER BY a.id LIMIT 1;
     known=creator IS NOT NULL;
    END IF;
   END IF;
   IF known AND parent_set IS NOT NULL THEN key=parent_set;
   ELSE
    classification=CASE WHEN NOT known THEN 'unresolved' WHEN creator IS NOT NULL THEN 'private_import' ELSE 'nonfinancial' END;
    INSERT INTO core.financial_requirement_sets(household_id,classification,required_count,creator_user_id,reason_code) VALUES(item.household_id,classification,0,creator,
     CASE classification WHEN 'unresolved' THEN 'unresolved_legacy' WHEN 'private_import' THEN 'private_import_preview' ELSE 'nonfinancial_explicit' END) RETURNING id INTO key;
   END IF;
   INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,revision,set_id,source_module,source_generation)
    VALUES(item.household_id,item.kind,item.id,1,key,item.module_id,item.module_generation);
  END IF;
 END LOOP;
 FOR item IN SELECT b.household_id,b.id batch_id,r.row_number,b.module_id,b.module_generation,a.set_id FROM core.import_batches b JOIN core.import_rows r ON r.batch_id=b.id
  JOIN LATERAL(SELECT v.set_id FROM core.financial_artifact_scope_versions v WHERE v.household_id=b.household_id AND v.artifact_kind='import_batch' AND v.object_id=b.id ORDER BY v.revision DESC LIMIT 1) a ON true
  WHERE NOT EXISTS(SELECT 1 FROM core.financial_artifact_scope_versions v WHERE v.household_id=b.household_id AND v.artifact_kind='import_row' AND v.object_id=b.id AND v.row_number=r.row_number) LOOP
  INSERT INTO core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,row_number,revision,set_id,source_module,source_generation)
   VALUES(item.household_id,'import_row',item.batch_id,item.row_number,1,item.set_id,item.module_id,item.module_generation);
 END LOOP;
END $artifacts$;

-- Validate the populated history before canonical DDL. Deferred FK/identity
-- events on these same tables must finish before PostgreSQL can alter them.
SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE core.financial_policies DROP CONSTRAINT financial_rollout_shared_only;
ALTER TABLE core.financial_policies ADD CONSTRAINT financial_access_mode CHECK(access_mode IN('household','selected')),
 ADD CONSTRAINT financial_selected_responsible CHECK(access_mode<>'selected' OR responsible_membership_id IS NOT NULL);

-- CHECK deparse round trips are canonical before catalog capture, matching the
-- existing Pantry/Mealplan restore convention; no predecessor SQL is changed.
DO $canonical$
DECLARE item record;definition text;
BEGIN
 FOR item IN SELECT c.conrelid::regclass relation,c.conname,pg_get_constraintdef(c.oid) definition FROM pg_constraint c
  WHERE c.contype='c' AND(c.conrelid IN('core.financial_policies'::regclass,'core.financial_subjects'::regclass)
   OR c.conrelid IN(SELECT t.oid FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname='core' AND t.relname IN(
    'financial_policy_grants','finance_private_owners','finance_key_tombstones','finance_operations','finance_origin_operations','finance_origin_resources','finance_origin_effects','finance_audit_bindings',
    'finance_ingest_claims','finance_background_authorities','finance_transaction_authorities','finance_artifact_job_authorities','finance_security_events','import_batches'))) ORDER BY c.conrelid,c.conname LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',item.relation,item.conname);
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s',item.relation,item.conname,item.definition);
 END LOOP;
END $canonical$;

CREATE FUNCTION core.finance_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 WITH relations AS(SELECT t.oid,n.nspname,t.relname FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname='core' AND t.relname IN(
 'financial_policies','financial_subjects','financial_policy_grants','finance_private_owners','finance_key_tombstones','finance_operations','finance_origin_operations','finance_origin_resources','finance_origin_effects',
 'finance_audit_bindings','finance_ingest_claims','finance_background_authorities','finance_transaction_authorities','finance_artifact_job_authorities','finance_security_events',
 'attachments','attachment_derivatives','capture_jobs','import_batches','import_rows'))
 SELECT jsonb_build_object(
 'relations',(SELECT jsonb_agg(jsonb_build_array(r.relname,c.relowner::regrole::text,c.relkind,c.relpersistence,c.relrowsecurity,c.relforcerowsecurity,c.relacl::text) ORDER BY r.relname)
  FROM relations r JOIN pg_class c ON c.oid=r.oid),
 'columns',(SELECT jsonb_agg(jsonb_build_array(r.relname,a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attidentity,a.attgenerated,coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY r.relname,a.attnum)
  FROM relations r JOIN pg_attribute a ON a.attrelid=r.oid LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attnum>0 AND NOT a.attisdropped),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(r.relname,c.conname,pg_get_constraintdef(c.oid),c.convalidated) ORDER BY r.relname,c.conname) FROM relations r JOIN pg_constraint c ON c.conrelid=r.oid),
 'indexes',(SELECT jsonb_agg(jsonb_build_array(r.relname,pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY r.relname,i.indexrelid::regclass::text) FROM relations r JOIN pg_index i ON i.indrelid=r.oid),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(t.tgrelid::regclass::text,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled) ORDER BY t.tgrelid::regclass::text,t.tgname)
  FROM pg_trigger t WHERE NOT t.tgisinternal AND(t.tgname LIKE 'finance_%' OR t.tgname IN('financial_subject','financial_visibility_scope','financial_policy_identity','financial_subject_identity','financial_policy_complete','financial_subject_complete','financial_infrastructure_immutable','financial_infrastructure_truncate','financial_infrastructure_complete'))),
 'functions',(SELECT jsonb_agg(jsonb_build_array(p.oid::regprocedure::text,pg_get_functiondef(p.oid),p.proacl::text,p.proowner::regrole::text,p.prosecdef,p.provolatile,p.proconfig) ORDER BY p.oid::regprocedure::text)
  FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='core' AND(p.proname LIKE 'finance_%' AND p.proname NOT IN('finance_catalog','finance_ready')
   OR p.proname IN('guard_financial_identity','check_financial_identity_complete','guard_financial_requirement_infrastructure'))),
 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY p.table_name) FROM core.audit_policies p WHERE p.schema_name='expenses'))
$$;
REVOKE ALL ON FUNCTION core.finance_catalog() FROM PUBLIC,household_app;

DO $ready$
DECLARE expected jsonb;
BEGIN
 expected=core.finance_catalog();
 EXECUTE format($definition$CREATE FUNCTION core.finance_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $finance_ready_body_31$
 SELECT core.finance_producers_ready() AND core.finance_catalog()=%L::jsonb AND EXISTS(SELECT 1 FROM pg_constraint c
  WHERE c.conrelid='core.financial_policies'::regclass AND c.conname='financial_access_mode' AND c.convalidated)
 AND NOT EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid='core.financial_policies'::regclass AND c.conname='financial_rollout_shared_only')
 $finance_ready_body_31$$definition$,expected);
END $ready$;
REVOKE ALL ON FUNCTION core.finance_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_ready() TO household_app;

CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT core.finance_ready() AND count(*)=121 AND bool_and(EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name))
 AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
 AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate'
 AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure))
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN('r','p')
 AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan') AND c.relname<>'schema_migrations'
 AND NOT(n.nspname='fuel' AND c.relname IN('fuel_types','household_state','segment_state','consumption_segments','segment_jobs','module_operations','module_effects','capture_transitions','legacy_operation_fences'))
 AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
 AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states',
 'financial_requirement_sets','financial_requirement_members','financial_artifact_scope_versions','financial_policy_grants','finance_private_owners','finance_key_tombstones','finance_operations','finance_origin_operations','finance_origin_resources','finance_origin_effects',
 'finance_audit_bindings','finance_ingest_claims','finance_background_authorities','finance_transaction_authorities','finance_artifact_job_authorities','finance_security_events'))
 AND NOT EXISTS(SELECT 1 FROM core.audit_policies a WHERE a.schema_name=n.nspname AND a.table_name=c.relname)) FROM core.audit_policies p
$$;
COMMIT;
