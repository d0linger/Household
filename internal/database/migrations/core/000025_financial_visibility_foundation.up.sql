BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

-- Rollout remains household-only until every indirect reader is protected.
-- A later forward migration must deliberately remove this database barrier.
CREATE TABLE core.financial_policies(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 module_id text NOT NULL CHECK(module_id='expenses'), module_generation bigint NOT NULL CHECK(module_generation>0),
 resource_type text NOT NULL CHECK(resource_type='entries'), resource_id uuid NOT NULL,
 access_mode text NOT NULL DEFAULT 'household', responsible_membership_id uuid,
 retired_at timestamptz, created_by uuid NOT NULL REFERENCES core.users(id), changed_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0), UNIQUE(household_id,id),
 CONSTRAINT financial_policy_identity UNIQUE(household_id,module_id,module_generation,resource_type,resource_id),
 CONSTRAINT financial_rollout_shared_only CHECK(access_mode='household'),
 FOREIGN KEY(household_id,responsible_membership_id) REFERENCES core.memberships(household_id,id)
);
CREATE TABLE core.financial_subjects(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 module_id text NOT NULL CHECK(module_id='expenses'), module_generation bigint NOT NULL CHECK(module_generation>0),
 resource_type text NOT NULL CHECK(resource_type IN('entries','entry_lines')), resource_id uuid NOT NULL,
 policy_id uuid NOT NULL, retired_at timestamptz,
 created_by uuid NOT NULL REFERENCES core.users(id), changed_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0), UNIQUE(household_id,id),
 CONSTRAINT financial_subject_identity UNIQUE(household_id,module_id,module_generation,resource_type,resource_id),
 FOREIGN KEY(household_id,policy_id) REFERENCES core.financial_policies(household_id,id)
);
CREATE INDEX financial_subject_policy ON core.financial_subjects(household_id,policy_id,id);
CREATE INDEX financial_policy_responsible ON core.financial_policies(household_id,responsible_membership_id,id)
 WHERE responsible_membership_id IS NOT NULL AND retired_at IS NULL;
REVOKE ALL ON core.financial_policies,core.financial_subjects FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE ON core.financial_policies,core.financial_subjects TO household_app;

CREATE FUNCTION core.guard_financial_identity() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE actor uuid; policy core.financial_policies%ROWTYPE;
BEGIN
 IF current_user='household_owner' THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'financial identity requires a tombstone' USING ERRCODE='23514'; END IF;
 actor=nullif(current_setting('household.audit_user',true),'')::uuid;
 IF nullif(current_setting('household.audit_kind',true),'') IS DISTINCT FROM 'user'
  OR nullif(current_setting('household.audit_household',true),'') IS DISTINCT FROM NEW.household_id::text
  OR actor IS NULL THEN RAISE EXCEPTION 'financial identity actor required' USING ERRCODE='42501'; END IF;
 PERFORM id FROM core.households WHERE id=NEW.household_id FOR SHARE;
 IF NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
  WHERE m.household_id=NEW.household_id AND m.user_id=actor AND m.inactive_at IS NULL
  AND u.disabled_at IS NULL AND m.role IN('admin','editor'))
  OR NOT EXISTS(SELECT 1 FROM core.module_states s WHERE s.household_id=NEW.household_id
   AND s.module_id=NEW.module_id AND s.data_generation=NEW.module_generation AND s.state='enabled') THEN
  RAISE EXCEPTION 'financial identity scope mismatch' USING ERRCODE='42501';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.created_by<>actor OR NEW.changed_by<>actor OR NEW.retired_at IS NOT NULL THEN
   RAISE EXCEPTION 'financial identity author mismatch' USING ERRCODE='23514';
  END IF;
  NEW.version=1; NEW.created_at=clock_timestamp();
 ELSE
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.household_id IS DISTINCT FROM OLD.household_id
   OR NEW.module_id IS DISTINCT FROM OLD.module_id OR NEW.module_generation IS DISTINCT FROM OLD.module_generation
   OR NEW.resource_type IS DISTINCT FROM OLD.resource_type OR NEW.resource_id IS DISTINCT FROM OLD.resource_id
   OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at
   OR (OLD.retired_at IS NOT NULL AND NEW.retired_at IS DISTINCT FROM OLD.retired_at) THEN
   RAISE EXCEPTION 'financial identity is immutable' USING ERRCODE='23514';
  END IF;
  IF TG_TABLE_NAME='financial_subjects' THEN
   IF NEW.policy_id IS DISTINCT FROM OLD.policy_id THEN
    RAISE EXCEPTION 'financial subject policy is immutable' USING ERRCODE='23514';
   END IF;
  END IF;
  NEW.version=OLD.version+1;
  IF OLD.retired_at IS NULL AND NEW.retired_at IS NOT NULL THEN NEW.retired_at=clock_timestamp(); END IF;
 END IF;
 NEW.changed_by=actor; NEW.updated_at=clock_timestamp();
 IF TG_TABLE_NAME='financial_subjects' THEN
  SELECT * INTO STRICT policy FROM core.financial_policies WHERE household_id=NEW.household_id AND id=NEW.policy_id FOR SHARE NOWAIT;
  IF policy.module_id<>NEW.module_id OR policy.module_generation<>NEW.module_generation
   OR (NEW.resource_type='entries' AND policy.resource_id<>NEW.resource_id)
   OR (NEW.retired_at IS NULL AND policy.retired_at IS NOT NULL) THEN
   RAISE EXCEPTION 'financial subject scope mismatch' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.guard_financial_identity() FROM PUBLIC,household_app;
CREATE TRIGGER financial_policy_identity BEFORE INSERT OR UPDATE OR DELETE ON core.financial_policies
 FOR EACH ROW EXECUTE FUNCTION core.guard_financial_identity();
CREATE TRIGGER financial_subject_identity BEFORE INSERT OR UPDATE OR DELETE ON core.financial_subjects
 FOR EACH ROW EXECUTE FUNCTION core.guard_financial_identity();

CREATE FUNCTION core.check_financial_identity_complete() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE policy core.financial_policies%ROWTYPE; source_entry uuid; source_line uuid; source_parent uuid; subject core.financial_subjects%ROWTYPE;
BEGIN
 -- Operator restore/purge preserves historical source-independent tombstones.
 IF current_user='household_owner' THEN RETURN NULL; END IF;
 IF TG_TABLE_NAME='financial_policies' THEN
  SELECT * INTO STRICT policy FROM core.financial_policies WHERE id=NEW.id FOR SHARE NOWAIT;
 ELSE
  SELECT * INTO STRICT subject FROM core.financial_subjects WHERE id=NEW.id;
  SELECT * INTO STRICT policy FROM core.financial_policies WHERE household_id=subject.household_id AND id=subject.policy_id FOR SHARE NOWAIT;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM core.module_states s WHERE s.household_id=policy.household_id AND s.module_id=policy.module_id
  AND s.data_generation=policy.module_generation AND s.state='enabled') THEN
  RAISE EXCEPTION 'financial final generation mismatch' USING ERRCODE='23514';
 END IF;
 SELECT id INTO source_entry FROM expenses.entries
  WHERE household_id=policy.household_id AND id=policy.resource_id FOR KEY SHARE NOWAIT;
 IF TG_TABLE_NAME='financial_policies' THEN
  IF (source_entry IS NULL) IS DISTINCT FROM (policy.retired_at IS NOT NULL) THEN
   RAISE EXCEPTION 'financial policy source liveness mismatch' USING ERRCODE='23514';
  END IF;
  IF source_entry IS NOT NULL THEN
   IF NOT EXISTS(SELECT 1 FROM core.financial_subjects s WHERE s.household_id=policy.household_id
    AND s.module_id=policy.module_id AND s.module_generation=policy.module_generation AND s.resource_type='entries'
    AND s.resource_id=source_entry AND s.policy_id=policy.id AND s.retired_at IS NULL)
    OR EXISTS(SELECT 1 FROM expenses.entry_lines l WHERE l.household_id=policy.household_id AND l.entry_id=source_entry
     AND NOT EXISTS(SELECT 1 FROM core.financial_subjects s WHERE s.household_id=l.household_id
      AND s.module_id=policy.module_id AND s.module_generation=policy.module_generation AND s.resource_type='entry_lines'
      AND s.resource_id=l.id AND s.policy_id=policy.id AND s.retired_at IS NULL)) THEN
    RAISE EXCEPTION 'financial policy bindings are incomplete' USING ERRCODE='23514';
   END IF;
  ELSIF EXISTS(SELECT 1 FROM core.financial_subjects s WHERE s.household_id=policy.household_id AND s.policy_id=policy.id AND s.retired_at IS NULL) THEN
   RAISE EXCEPTION 'retired financial policy has active subjects' USING ERRCODE='23514';
  END IF;
 ELSE
  IF subject.resource_type='entries' THEN
   IF subject.resource_id<>policy.resource_id OR (source_entry IS NULL) IS DISTINCT FROM (subject.retired_at IS NOT NULL) THEN
    RAISE EXCEPTION 'financial entry subject liveness mismatch' USING ERRCODE='23514';
   END IF;
  ELSE
   SELECT id,entry_id INTO source_line,source_parent FROM expenses.entry_lines WHERE household_id=subject.household_id
    AND id=subject.resource_id FOR KEY SHARE NOWAIT;
   IF (source_line IS NULL) IS DISTINCT FROM (subject.retired_at IS NOT NULL)
    OR (source_line IS NOT NULL AND source_parent<>policy.resource_id) THEN
    RAISE EXCEPTION 'financial line subject source mismatch' USING ERRCODE='23514';
   END IF;
  END IF;
  IF subject.retired_at IS NULL AND policy.retired_at IS NOT NULL THEN
   RAISE EXCEPTION 'live subject has retired financial policy' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.check_financial_identity_complete() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER financial_policy_complete AFTER INSERT OR UPDATE ON core.financial_policies
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.check_financial_identity_complete();
CREATE CONSTRAINT TRIGGER financial_subject_complete AFTER INSERT OR UPDATE ON core.financial_subjects
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.check_financial_identity_complete();
INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('core','financial_policies',ARRAY['id'],ARRAY['module_id','module_generation','resource_type','resource_id','access_mode','responsible_membership_id','retired_at','changed_by'],NULL),
 ('core','financial_subjects',ARRAY['id'],ARRAY['module_id','module_generation','resource_type','resource_id','policy_id','retired_at','changed_by'],NULL);

CREATE FUNCTION core.financial_foundation_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT NOT EXISTS(SELECT 1 FROM (VALUES
 ('core.financial_policies','financial_policy_identity'),('core.financial_subjects','financial_subject_identity')
 ) required(relation,name) WHERE NOT EXISTS(
 SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
 WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=required.name AND NOT t.tgisinternal
 AND t.tgfoid=to_regprocedure('core.guard_financial_identity()') AND t.tgtype=31
 AND t.tgenabled IN('O','A') AND t.tgqual IS NULL AND t.tgattr=''::int2vector AND t.tgnargs=0
 AND NOT t.tgdeferrable AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM (VALUES('core.financial_policies'),('core.financial_subjects')) required(relation)
 CROSS JOIN (VALUES('SELECT'),('INSERT'),('UPDATE')) permission(privilege)
 WHERE NOT has_table_privilege('household_app',required.relation,permission.privilege))
 AND NOT has_table_privilege('household_app','core.financial_policies','DELETE,TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','core.financial_subjects','DELETE,TRUNCATE,TRIGGER')
 AND NOT has_function_privilege('household_app','core.guard_financial_identity()','EXECUTE')
 AND NOT has_function_privilege('household_app','core.check_financial_identity_complete()','EXECUTE')
 AND NOT EXISTS(SELECT 1 FROM (VALUES
 ('core.financial_policies','financial_policy_complete'),('core.financial_subjects','financial_subject_complete')
 ) required(relation,name) WHERE NOT EXISTS(
 SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
 WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=required.name AND NOT t.tgisinternal
 AND t.tgfoid=to_regprocedure('core.check_financial_identity_complete()') AND t.tgtype=21
 AND t.tgenabled IN('O','A') AND t.tgqual IS NULL AND t.tgattr=''::int2vector AND t.tgnargs=0
 AND t.tgdeferrable AND t.tginitdeferred AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']))
 AND EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid=to_regclass('core.financial_policies')
  AND c.conname='financial_rollout_shared_only' AND c.contype='c' AND c.convalidated
  AND pg_get_constraintdef(c.oid)='CHECK ((access_mode = ''household''::text))')
 AND NOT EXISTS(SELECT 1 FROM (VALUES
 ('core.financial_policies','financial_policy_identity'),('core.financial_subjects','financial_subject_identity')
 ) required(relation,name) WHERE NOT EXISTS(SELECT 1 FROM pg_constraint c
 WHERE c.conrelid=to_regclass(required.relation) AND c.conname=required.name AND c.contype='u'
 AND c.convalidated AND NOT c.condeferrable
 AND pg_get_constraintdef(c.oid)='UNIQUE (household_id, module_id, module_generation, resource_type, resource_id)'))
 AND EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid=to_regclass('core.financial_subjects') AND c.contype='f'
  AND c.convalidated AND NOT c.condeferrable
  AND pg_get_constraintdef(c.oid)='FOREIGN KEY (household_id, policy_id) REFERENCES core.financial_policies(household_id, id)')
$$;
REVOKE ALL ON FUNCTION core.financial_foundation_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.financial_foundation_ready() TO household_app;
CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT count(*)=77 AND bool_and(
  EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
  AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure)
 ) AND NOT EXISTS(
  SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
  AND c.relname<>'schema_migrations'
  AND NOT(n.nspname='fuel' AND c.relname='fuel_types')
  AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
  AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states'))
  AND NOT EXISTS(SELECT 1 FROM core.audit_policies policy WHERE policy.schema_name=n.nspname AND policy.table_name=c.relname)
 ) FROM core.audit_policies p
$$;
COMMIT;
