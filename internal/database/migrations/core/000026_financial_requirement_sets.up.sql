BEGIN;
SET LOCAL search_path=pg_catalog;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

-- Security infrastructure only: no producer, historical inference or audience
-- cutover is installed here. App can inspect metadata but cannot create it yet.
ALTER TABLE core.financial_subjects ADD CONSTRAINT financial_subject_policy_pair UNIQUE(household_id,id,policy_id);
CREATE TABLE core.financial_requirement_sets(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 classification text NOT NULL CHECK(classification IN('financial','private_import','nonfinancial','unresolved')),
 required_count integer NOT NULL CHECK(required_count BETWEEN 0 AND 1024), creator_user_id uuid,
 reason_code text NOT NULL CHECK(reason_code IN('financial_sources','private_import_preview','nonfinancial_explicit','unresolved_legacy','unresolved_missing_origin')),
 classifier_version integer NOT NULL DEFAULT 1 CHECK(classifier_version=1),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), UNIQUE(household_id,id),
 FOREIGN KEY(household_id,creator_user_id) REFERENCES core.memberships(household_id,user_id),
 CONSTRAINT financial_requirement_class CHECK(
  (classification='financial' AND required_count>0 AND reason_code='financial_sources') OR
  (classification='private_import' AND creator_user_id IS NOT NULL AND reason_code='private_import_preview') OR
  (classification='nonfinancial' AND required_count=0 AND reason_code='nonfinancial_explicit') OR
  (classification='unresolved' AND reason_code IN('unresolved_legacy','unresolved_missing_origin')))
);
CREATE TABLE core.financial_requirement_members(
 household_id uuid NOT NULL, set_id uuid NOT NULL, subject_id uuid NOT NULL, policy_id uuid NOT NULL,
 PRIMARY KEY(set_id,subject_id),
 FOREIGN KEY(household_id,set_id) REFERENCES core.financial_requirement_sets(household_id,id),
 FOREIGN KEY(household_id,subject_id,policy_id) REFERENCES core.financial_subjects(household_id,id,policy_id),
 FOREIGN KEY(household_id,policy_id) REFERENCES core.financial_policies(household_id,id)
);
CREATE INDEX financial_requirement_policy ON core.financial_requirement_members(household_id,policy_id,set_id);
CREATE INDEX financial_requirement_subject ON core.financial_requirement_members(household_id,subject_id,set_id);
CREATE TABLE core.financial_artifact_scope_versions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 artifact_kind text NOT NULL CHECK(artifact_kind IN('attachment','attachment_derivative','capture','import_batch','import_row','audit','financial_subject')),
 object_id uuid, audit_id bigint, row_number integer,
 revision bigint NOT NULL CHECK(revision>0), previous_id uuid, set_id uuid NOT NULL,
 source_module text CHECK(source_module IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')),
 source_generation bigint, created_at timestamptz NOT NULL DEFAULT clock_timestamp(), UNIQUE(household_id,id),
 FOREIGN KEY(household_id,set_id) REFERENCES core.financial_requirement_sets(household_id,id),
 FOREIGN KEY(household_id,previous_id) REFERENCES core.financial_artifact_scope_versions(household_id,id) DEFERRABLE INITIALLY DEFERRED,
 CONSTRAINT financial_artifact_typed_key CHECK(
  (artifact_kind IN('attachment','attachment_derivative','capture','import_batch','financial_subject') AND object_id IS NOT NULL AND audit_id IS NULL AND row_number IS NULL) OR
  (artifact_kind='import_row' AND object_id IS NOT NULL AND audit_id IS NULL AND row_number IS NOT NULL AND row_number>0) OR
  (artifact_kind='audit' AND object_id IS NULL AND audit_id IS NOT NULL AND audit_id>0 AND row_number IS NULL)),
 CONSTRAINT financial_artifact_revision CHECK((revision=1 AND previous_id IS NULL) OR (revision>1 AND previous_id IS NOT NULL)),
 CONSTRAINT financial_artifact_audit_once CHECK(artifact_kind<>'audit' OR revision=1),
 CONSTRAINT financial_artifact_subject_generation CHECK(artifact_kind<>'financial_subject' OR (source_module IS NOT NULL AND source_module='expenses' AND source_generation IS NOT NULL AND source_generation>0)),
 CONSTRAINT financial_artifact_source_pair CHECK((source_module IS NULL AND source_generation IS NULL) OR (source_module IS NOT NULL AND source_generation IS NOT NULL AND source_generation>0))
);
CREATE UNIQUE INDEX financial_artifact_uuid_revision ON core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,revision) WHERE artifact_kind IN('attachment','attachment_derivative','capture','import_batch','financial_subject');
CREATE UNIQUE INDEX financial_artifact_row_revision ON core.financial_artifact_scope_versions(household_id,object_id,row_number,revision) WHERE artifact_kind='import_row';
CREATE UNIQUE INDEX financial_artifact_audit_revision ON core.financial_artifact_scope_versions(household_id,audit_id,revision) WHERE artifact_kind='audit';
CREATE INDEX financial_artifact_uuid_latest ON core.financial_artifact_scope_versions(household_id,artifact_kind,object_id,revision DESC) WHERE artifact_kind IN('attachment','attachment_derivative','capture','import_batch','financial_subject');
CREATE INDEX financial_artifact_row_latest ON core.financial_artifact_scope_versions(household_id,object_id,row_number,revision DESC) WHERE artifact_kind='import_row';
CREATE INDEX financial_artifact_audit_latest ON core.financial_artifact_scope_versions(household_id,audit_id,revision DESC) WHERE artifact_kind='audit';
CREATE INDEX financial_artifact_set ON core.financial_artifact_scope_versions(household_id,set_id,id);
REVOKE ALL ON core.financial_requirement_sets,core.financial_requirement_members,core.financial_artifact_scope_versions FROM PUBLIC,household_app;
GRANT SELECT ON core.financial_requirement_sets,core.financial_requirement_members,core.financial_artifact_scope_versions TO household_app;

CREATE FUNCTION core.guard_financial_requirement_infrastructure() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 -- SECURITY DEFINER audit code elevates current_user while retaining the App
 -- session/role. Only an actual owner login or explicitly authorized SET ROLE
 -- is maintenance. A spoofed household.* custom setting confers no authority.
 IF NOT(session_user='household_owner' OR
  (current_user='household_owner' AND current_setting('role',true)='household_owner'
   AND session_user<>'household_app' AND pg_has_role(session_user,'household_owner','MEMBER'))) THEN
  RAISE EXCEPTION 'financial security metadata is immutable; producers are not enabled' USING ERRCODE='42501';
 END IF;
 IF TG_OP='TRUNCATE' THEN RETURN NULL; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.guard_financial_requirement_infrastructure() FROM PUBLIC,household_app;

CREATE FUNCTION core.check_financial_requirement_complete() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE set_key uuid; set_row core.financial_requirement_sets%ROWTYPE; item core.financial_artifact_scope_versions%ROWTYPE;
 previous core.financial_artifact_scope_versions%ROWTYPE; actual_count bigint;
BEGIN
 IF TG_TABLE_NAME='financial_requirement_sets' THEN
  set_key=CASE WHEN TG_OP='DELETE' THEN OLD.id ELSE NEW.id END;
 ELSE
  set_key=CASE WHEN TG_OP='DELETE' THEN OLD.set_id ELSE NEW.set_id END;
 END IF;
 SELECT * INTO set_row FROM core.financial_requirement_sets WHERE id=set_key;
 IF FOUND THEN
  SELECT count(*) INTO actual_count FROM core.financial_requirement_members WHERE set_id=set_key;
  IF actual_count<>set_row.required_count THEN RAISE EXCEPTION 'financial requirement member count mismatch' USING ERRCODE='23514'; END IF;
  IF EXISTS(SELECT 1 FROM core.financial_artifact_scope_versions a WHERE a.set_id=set_key AND (
   (a.artifact_kind IN('attachment','attachment_derivative','capture') AND set_row.required_count>16) OR
   (a.artifact_kind IN('import_batch','import_row') AND set_row.required_count>1000) OR
   (set_row.classification='private_import' AND a.artifact_kind NOT IN('import_batch','import_row')))) THEN
   RAISE EXCEPTION 'financial artifact requirement bound mismatch' USING ERRCODE='23514';
  END IF;
 END IF;
 IF TG_TABLE_NAME='financial_artifact_scope_versions' AND TG_OP<>'DELETE' THEN
  SELECT * INTO item FROM core.financial_artifact_scope_versions WHERE id=NEW.id;
  IF NOT FOUND THEN RETURN NULL; END IF;
  IF set_row.household_id IS DISTINCT FROM item.household_id OR
   (item.artifact_kind IN('attachment','attachment_derivative','capture') AND set_row.required_count>16) OR
   (item.artifact_kind IN('import_batch','import_row') AND set_row.required_count>1000) OR
   (set_row.classification='private_import' AND item.artifact_kind NOT IN('import_batch','import_row')) THEN
   RAISE EXCEPTION 'financial artifact requirement scope mismatch' USING ERRCODE='23514';
  END IF;
  IF item.previous_id IS NOT NULL THEN
   SELECT * INTO previous FROM core.financial_artifact_scope_versions WHERE household_id=item.household_id AND id=item.previous_id;
   IF NOT FOUND OR previous.artifact_kind<>item.artifact_kind OR previous.object_id IS DISTINCT FROM item.object_id
    OR previous.audit_id IS DISTINCT FROM item.audit_id OR previous.row_number IS DISTINCT FROM item.row_number
    OR previous.revision<>item.revision-1 THEN
    RAISE EXCEPTION 'financial artifact predecessor mismatch' USING ERRCODE='23514';
   END IF;
  END IF;
  IF item.artifact_kind='financial_subject' AND (
   NOT EXISTS(SELECT 1 FROM core.financial_subjects s WHERE s.household_id=item.household_id AND s.id=item.object_id
    AND s.module_id=item.source_module AND s.module_generation=item.source_generation) OR
   EXISTS(SELECT 1 FROM core.financial_requirement_members m WHERE m.set_id=item.set_id AND m.subject_id=item.object_id)) THEN
   RAISE EXCEPTION 'financial inherited scope identity or own-anchor mismatch' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.check_financial_requirement_complete() FROM PUBLIC,household_app;
DO $install$
DECLARE relation text;
BEGIN
 FOREACH relation IN ARRAY ARRAY['financial_requirement_sets','financial_requirement_members','financial_artifact_scope_versions'] LOOP
  EXECUTE format('CREATE TRIGGER financial_infrastructure_immutable BEFORE INSERT OR UPDATE OR DELETE ON core.%I FOR EACH ROW EXECUTE FUNCTION core.guard_financial_requirement_infrastructure()',relation);
  EXECUTE format('CREATE TRIGGER financial_infrastructure_truncate BEFORE TRUNCATE ON core.%I FOR EACH STATEMENT EXECUTE FUNCTION core.guard_financial_requirement_infrastructure()',relation);
  EXECUTE format('CREATE CONSTRAINT TRIGGER financial_infrastructure_complete AFTER INSERT OR UPDATE OR DELETE ON core.%I DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.check_financial_requirement_complete()',relation);
 END LOOP;
END $install$;

-- Pin the exact constraints/indexes created above, including typed CHECKs and
-- partial keys. Captured literals avoid guessing pg_get_constraintdef spelling.
DO $install$
DECLARE constraint_values text; index_values text; column_values text;
BEGIN
 SELECT string_agg(format('(%L,%L,%L)',n.nspname||'.'||r.relname,c.conname,pg_get_constraintdef(c.oid)),',' ORDER BY r.relname,c.conname)
 INTO constraint_values FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace
 WHERE n.nspname='core' AND (r.relname IN('financial_requirement_sets','financial_requirement_members','financial_artifact_scope_versions') OR
  (r.relname='financial_subjects' AND c.conname='financial_subject_policy_pair'));
 SELECT string_agg(format('(%L,%L)',n.nspname||'.'||r.relname,pg_get_indexdef(i.indexrelid)),',' ORDER BY r.relname)
 INTO index_values FROM pg_index i JOIN pg_class r ON r.oid=i.indexrelid JOIN pg_namespace n ON n.oid=r.relnamespace
 WHERE i.indrelid IN('core.financial_requirement_sets'::regclass,'core.financial_requirement_members'::regclass,'core.financial_artifact_scope_versions'::regclass)
 OR i.indexrelid='core.financial_subject_policy_pair'::regclass;
 SELECT string_agg(format('(%L,%L)',relation,definition),',' ORDER BY relation) INTO column_values FROM (
 SELECT n.nspname||'.'||r.relname relation,jsonb_agg(jsonb_build_array(a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,
 a.attgenerated,a.attidentity,a.attnum,coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum)::text definition
 FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace JOIN pg_attribute a ON a.attrelid=r.oid
 LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE r.oid IN('core.financial_requirement_sets'::regclass,'core.financial_requirement_members'::regclass,'core.financial_artifact_scope_versions'::regclass)
 AND a.attnum>0 AND NOT a.attisdropped GROUP BY n.nspname,r.relname) captured_columns;
 EXECUTE replace(replace(replace($ready$
 CREATE FUNCTION core.financial_requirements_ready() RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $function$
 SELECT core.financial_foundation_ready() AND NOT pg_has_role('household_app','household_owner','MEMBER')
 AND NOT EXISTS(SELECT 1 FROM (VALUES('core.financial_requirement_sets'),('core.financial_requirement_members'),('core.financial_artifact_scope_versions')) required(relation)
 WHERE NOT has_table_privilege('household_app',required.relation,'SELECT') OR has_table_privilege('household_app',required.relation,'INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER,REFERENCES')
 OR EXISTS(SELECT 1 FROM core.audit_policies p WHERE p.schema_name='core' AND 'core.'||p.table_name=required.relation)
 OR EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname IN('household_audit_change','household_audit_truncate'))
 OR EXISTS(SELECT 1 FROM (VALUES('financial_infrastructure_immutable','core.guard_financial_requirement_infrastructure()',31,false),
 ('financial_infrastructure_truncate','core.guard_financial_requirement_infrastructure()',34,false),
 ('financial_infrastructure_complete','core.check_financial_requirement_complete()',29,true)) guard(name,function_id,kind,deferred)
 WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
 WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=guard.name AND t.tgfoid=to_regprocedure(guard.function_id)
 AND NOT t.tgisinternal AND t.tgtype=guard.kind AND t.tgenabled IN('O','A') AND t.tgqual IS NULL AND t.tgattr=''::int2vector AND t.tgnargs=0
 AND t.tgdeferrable=guard.deferred AND t.tginitdeferred=guard.deferred AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog'])))
 AND NOT has_function_privilege('household_app','core.guard_financial_requirement_infrastructure()','EXECUTE')
 AND NOT has_function_privilege('household_app','core.check_financial_requirement_complete()','EXECUTE')
 AND NOT EXISTS(SELECT 1 FROM (VALUES #CONSTRAINTS#) required(relation,name,definition) WHERE NOT EXISTS(
 SELECT 1 FROM pg_constraint c WHERE c.conrelid=to_regclass(required.relation) AND c.conname=required.name AND c.convalidated AND pg_get_constraintdef(c.oid)=required.definition))
 AND NOT EXISTS(SELECT 1 FROM (VALUES #INDEXES#) required(relation,definition) WHERE NOT EXISTS(
 SELECT 1 FROM pg_index i WHERE i.indexrelid=to_regclass(required.relation) AND i.indisvalid AND i.indisready AND i.indislive AND pg_get_indexdef(i.indexrelid)=required.definition))
 AND NOT EXISTS(SELECT 1 FROM (VALUES #COLUMNS#) required(relation,definition) WHERE
 (SELECT jsonb_agg(jsonb_build_array(a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attgenerated,a.attidentity,a.attnum,
 coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum) FROM pg_attribute a
 LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE a.attrelid=to_regclass(required.relation) AND a.attnum>0 AND NOT a.attisdropped) IS DISTINCT FROM required.definition::jsonb)
 $function$;
 $ready$,'#CONSTRAINTS#',constraint_values),'#INDEXES#',index_values),'#COLUMNS#',column_values);
END $install$;
REVOKE ALL ON FUNCTION core.financial_requirements_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.financial_requirements_ready() TO household_app;

CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT core.financial_requirements_ready() AND count(*)=77 AND bool_and(
  EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
  AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure)
 ) AND NOT EXISTS(
  SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
  AND c.relname<>'schema_migrations'
  AND NOT(n.nspname='fuel' AND c.relname='fuel_types') AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
  AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states',
   'financial_requirement_sets','financial_requirement_members','financial_artifact_scope_versions'))
  AND NOT EXISTS(SELECT 1 FROM core.audit_policies policy WHERE policy.schema_name=n.nspname AND policy.table_name=c.relname)
 ) FROM core.audit_policies p
$$;
COMMIT;
