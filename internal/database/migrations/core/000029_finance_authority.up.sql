BEGIN;
SET LOCAL search_path=pg_catalog;
SET LOCAL timezone='UTC';
SELECT id FROM core.households ORDER BY id FOR UPDATE;

DO $predecessor$
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=28 AND NOT dirty OR version=29 AND dirty) FROM core.schema_migrations)
 OR NOT core.planning_extensions_ready() OR (SELECT count(*) FROM core.audit_policies)<>100 THEN
  RAISE EXCEPTION 'Finance authority requires the exact released Core28 predecessor';
 END IF;
END $predecessor$;

ALTER TABLE core.financial_policies DROP CONSTRAINT financial_policies_resource_type_check;
ALTER TABLE core.financial_policies ADD CONSTRAINT financial_policies_resource_type_check CHECK(resource_type IN(
 'entries','accounts','cash_events','category_budgets','savings_goals','recurring_templates','categorization_rules','reconciliations','receipt_reviews'));
ALTER TABLE core.financial_subjects DROP CONSTRAINT financial_subjects_resource_type_check;
ALTER TABLE core.financial_subjects ADD CONSTRAINT financial_subjects_resource_type_check CHECK(resource_type IN(
 'entries','entry_lines','accounts','entry_finance','entry_original_lines','cash_events','account_movements','entry_relations','entry_split_sets',
 'entry_person_shares','settlement_details','category_budgets','savings_goals','goal_contributions','recurring_templates','recurring_occurrences',
 'categorization_rules','reconciliations','reconciliation_items','receipt_reviews','receipt_review_lines'));

CREATE TABLE core.financial_policy_grants(
 household_id uuid NOT NULL,policy_id uuid NOT NULL,membership_id uuid NOT NULL,
 permission text NOT NULL CHECK(permission IN('view','edit')),
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(policy_id,membership_id),
 FOREIGN KEY(household_id,policy_id) REFERENCES core.financial_policies(household_id,id),
 FOREIGN KEY(household_id,membership_id) REFERENCES core.memberships(household_id,id)
);
CREATE INDEX finance_grant_member ON core.financial_policy_grants(household_id,membership_id,policy_id);

CREATE TABLE core.finance_private_owners(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),
 resource_type text NOT NULL CHECK(resource_type IN('saved_filters','bank_profiles','import_batch','import_row','capture')),
 resource_id uuid NOT NULL,row_number integer NOT NULL DEFAULT 0 CHECK(row_number>=0),
 module_generation bigint NOT NULL CHECK(module_generation>0),owner_user_id uuid NOT NULL,
 retired_at timestamptz,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id),UNIQUE(household_id,module_generation,resource_type,resource_id,row_number),
 FOREIGN KEY(household_id,owner_user_id) REFERENCES core.memberships(household_id,user_id),
 CHECK((resource_type='import_row' AND row_number>0) OR(resource_type<>'import_row' AND row_number=0))
);

CREATE TABLE core.finance_operations(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),
 created_by uuid NOT NULL REFERENCES core.users(id),membership_id uuid NOT NULL,request_key uuid NOT NULL,
 purpose text NOT NULL CHECK(purpose='expense-finance-v1'),operation text NOT NULL CHECK(length(operation) BETWEEN 1 AND 100),
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),expenses_generation bigint NOT NULL CHECK(expenses_generation>0),
 resource_type text NOT NULL,resource_id uuid NOT NULL,result_version bigint NOT NULL CHECK(result_version>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),UNIQUE(household_id,created_by,request_key),
 FOREIGN KEY(household_id,membership_id) REFERENCES core.memberships(household_id,id)
);
CREATE INDEX finance_operation_resource ON core.finance_operations(household_id,expenses_generation,resource_type,resource_id,id);

CREATE TABLE core.finance_key_tombstones(
 household_id uuid NOT NULL REFERENCES core.households(id),created_by uuid NOT NULL REFERENCES core.users(id),request_key uuid NOT NULL,
 expenses_generation bigint NOT NULL CHECK(expenses_generation>0),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(household_id,created_by,request_key)
);

CREATE TABLE core.finance_origin_operations(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),
 parent_id uuid,provenance_root_id uuid,transaction_id xid8 NOT NULL,transaction_started_at timestamptz NOT NULL,
 expenses_generation bigint NOT NULL CHECK(expenses_generation>0),
 actor_user_id uuid NOT NULL REFERENCES core.users(id),membership_id uuid,session_id uuid,
 auth_version bigint NOT NULL CHECK(auth_version>0),authority_id uuid,
 target_kind text NOT NULL,target_id uuid NOT NULL,
 origin text NOT NULL CHECK(origin IN('manual','bank','recurrence','correction','refund','reversal','fuel','capture','import','preference','access','legacy_unresolved')),
 legacy_operation_id uuid REFERENCES core.expense_edit_operations(id),legacy_audit_id bigint REFERENCES core.audit_log(id),
 source_refs jsonb NOT NULL CHECK(jsonb_typeof(source_refs)='array' AND jsonb_array_length(source_refs)<=1024 AND octet_length(source_refs::text)<=131072),
 inherited_set_id uuid,initial_access jsonb,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','complete')),
 depth integer NOT NULL CHECK(depth BETWEEN 1 AND 16),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),completed_at timestamptz,
 UNIQUE(household_id,id),
 FOREIGN KEY(household_id,parent_id) REFERENCES core.finance_origin_operations(household_id,id),
 FOREIGN KEY(household_id,provenance_root_id) REFERENCES core.finance_origin_operations(household_id,id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(household_id,membership_id) REFERENCES core.memberships(household_id,id),
 FOREIGN KEY(household_id,inherited_set_id) REFERENCES core.financial_requirement_sets(household_id,id),
 CHECK((state='pending' AND completed_at IS NULL) OR(state='complete' AND completed_at IS NOT NULL)),
 CHECK(membership_id IS NOT NULL OR(origin='legacy_unresolved' AND session_id IS NULL AND state='complete')),
 CHECK(initial_access IS NULL OR(jsonb_typeof(initial_access)='object' AND octet_length(initial_access::text)<=32768))
);
CREATE INDEX finance_origin_transaction ON core.finance_origin_operations(transaction_id,transaction_started_at,id);
CREATE INDEX finance_origin_target ON core.finance_origin_operations(household_id,expenses_generation,target_kind,target_id,id);

CREATE TABLE core.finance_origin_resources(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),
 resource_type text NOT NULL,resource_id uuid,audit_id bigint,row_number integer NOT NULL DEFAULT 0,
 module_generation bigint NOT NULL CHECK(module_generation>0),subject_id uuid,private_owner_id uuid,
 root_id uuid NOT NULL,provenance_root_id uuid,set_id uuid NOT NULL,audit_set_id uuid,scope_id uuid,
 resource_version bigint NOT NULL CHECK(resource_version>0),row_hash bytea NOT NULL CHECK(octet_length(row_hash)=32),
 retired_at timestamptz,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id),
 FOREIGN KEY(household_id,subject_id) REFERENCES core.financial_subjects(household_id,id),
 FOREIGN KEY(household_id,private_owner_id) REFERENCES core.finance_private_owners(household_id,id),
 FOREIGN KEY(household_id,root_id) REFERENCES core.finance_origin_operations(household_id,id),
 FOREIGN KEY(household_id,set_id) REFERENCES core.financial_requirement_sets(household_id,id),
 FOREIGN KEY(household_id,audit_set_id) REFERENCES core.financial_requirement_sets(household_id,id),
 FOREIGN KEY(household_id,scope_id) REFERENCES core.financial_artifact_scope_versions(household_id,id),
 FOREIGN KEY(household_id,provenance_root_id) REFERENCES core.finance_origin_operations(household_id,id),
 CHECK((resource_type='audit' AND resource_id IS NULL AND audit_id>0 AND row_number=0)
 OR(resource_type='import_row' AND resource_id IS NOT NULL AND audit_id IS NULL AND row_number>0)
 OR(resource_type NOT IN('audit','import_row') AND resource_id IS NOT NULL AND audit_id IS NULL AND row_number=0))
);
CREATE UNIQUE INDEX finance_origin_uuid ON core.finance_origin_resources(household_id,module_generation,resource_type,resource_id)
 WHERE resource_type NOT IN('audit','import_row');
CREATE UNIQUE INDEX finance_origin_import_row ON core.finance_origin_resources(household_id,module_generation,resource_id,row_number) WHERE resource_type='import_row';
CREATE UNIQUE INDEX finance_origin_audit ON core.finance_origin_resources(household_id,audit_id) WHERE resource_type='audit';
CREATE UNIQUE INDEX finance_origin_subject ON core.finance_origin_resources(household_id,subject_id) WHERE subject_id IS NOT NULL;
CREATE INDEX finance_origin_requirements ON core.finance_origin_resources(household_id,set_id,id);

CREATE TABLE core.finance_origin_effects(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,household_id uuid NOT NULL,
 root_id uuid NOT NULL,resource_type text NOT NULL,resource_id uuid,audit_id bigint,row_number integer NOT NULL DEFAULT 0,
 effect text NOT NULL CHECK(effect IN('insert','update','delete','audit','scope')),
 before_hash bytea,after_hash bytea,resource_version bigint,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(household_id,root_id) REFERENCES core.finance_origin_operations(household_id,id),
 CHECK(before_hash IS NULL OR octet_length(before_hash)=32),CHECK(after_hash IS NULL OR octet_length(after_hash)=32)
);
CREATE INDEX finance_origin_effect_root ON core.finance_origin_effects(root_id,id);

CREATE TABLE core.finance_audit_bindings(
 audit_id bigint PRIMARY KEY,household_id uuid NOT NULL,root_id uuid,
 resource_type text NOT NULL,resource_id uuid,row_number integer NOT NULL DEFAULT 0 CHECK(row_number>=0),policy_id uuid,
 inherited_set_id uuid NOT NULL,set_id uuid,source_generation bigint NOT NULL CHECK(source_generation>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(household_id,root_id) REFERENCES core.finance_origin_operations(household_id,id),
 FOREIGN KEY(household_id,policy_id) REFERENCES core.financial_policies(household_id,id),
 FOREIGN KEY(household_id,inherited_set_id) REFERENCES core.financial_requirement_sets(household_id,id),
 FOREIGN KEY(household_id,set_id) REFERENCES core.financial_requirement_sets(household_id,id)
);
CREATE INDEX finance_audit_set ON core.finance_audit_bindings(household_id,set_id,audit_id DESC);

CREATE TABLE core.finance_ingest_claims(
 household_id uuid NOT NULL REFERENCES core.households(id),source_kind text NOT NULL CHECK(source_kind IN('bank_transaction','bank_file_row','recurrence')),
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 target_kind text NOT NULL CHECK(target_kind IN('entries','cash_events')),target_id uuid NOT NULL,
 expenses_generation bigint NOT NULL CHECK(expenses_generation>0),root_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(household_id,source_kind,fingerprint),
 FOREIGN KEY(household_id,root_id) REFERENCES core.finance_origin_operations(household_id,id)
);
CREATE INDEX finance_ingest_target ON core.finance_ingest_claims(household_id,expenses_generation,target_kind,target_id);

CREATE TABLE core.finance_background_authorities(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),
 template_id uuid NOT NULL,expenses_generation bigint NOT NULL CHECK(expenses_generation>0),
 responsible_membership_id uuid NOT NULL,user_id uuid NOT NULL REFERENCES core.users(id),
 auth_version bigint NOT NULL CHECK(auth_version>0),template_version bigint NOT NULL CHECK(template_version>0),
 source_stamp text NOT NULL CHECK(source_stamp~'^[0-9a-f]{64}$'),enabled boolean NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id),UNIQUE(household_id,expenses_generation,template_id),
 FOREIGN KEY(household_id,responsible_membership_id) REFERENCES core.memberships(household_id,id)
);
CREATE TABLE core.finance_transaction_authorities(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL,authority_id uuid NOT NULL,
 job_id uuid NOT NULL,lease_token uuid NOT NULL,template_id uuid NOT NULL,
 transaction_id xid8 NOT NULL,transaction_started_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),UNIQUE(transaction_id,transaction_started_at),
 FOREIGN KEY(household_id,authority_id) REFERENCES core.finance_background_authorities(household_id,id)
);

CREATE TABLE core.finance_security_events(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,household_id uuid NOT NULL REFERENCES core.households(id),
 actor_user_id uuid NOT NULL REFERENCES core.users(id),request_id uuid NOT NULL,
 action text NOT NULL CHECK(action IN('access_change','responsibility_assign','responsibility_recover','automation_consent')),
 policy_id uuid,policy_version bigint,reason_code text NOT NULL CHECK(reason_code IN('explicit_change','explicit_assignment','explicit_recovery','explicit_consent')),
 reason_hash bytea CHECK(reason_hash IS NULL OR octet_length(reason_hash)=32),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(household_id,policy_id) REFERENCES core.financial_policies(household_id,id)
);

CREATE TABLE core.finance_artifact_job_authorities(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),job_id uuid NOT NULL,lease_token uuid NOT NULL,
 authority_kind text NOT NULL DEFAULT 'artifact' CHECK(authority_kind IN('artifact','retention')),
 source_attachment_id uuid,actor_user_id uuid REFERENCES core.users(id),membership_id uuid,auth_version bigint CHECK(auth_version>0),
 expenses_generation bigint CHECK(expenses_generation>0),transaction_id xid8 NOT NULL,transaction_started_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),UNIQUE(transaction_id,transaction_started_at),
 FOREIGN KEY(household_id,membership_id) REFERENCES core.memberships(household_id,id),
 CHECK((authority_kind='artifact' AND source_attachment_id IS NOT NULL AND actor_user_id IS NOT NULL AND membership_id IS NOT NULL AND auth_version IS NOT NULL AND expenses_generation IS NOT NULL)
  OR(authority_kind='retention' AND source_attachment_id IS NULL AND actor_user_id IS NULL AND membership_id IS NULL AND auth_version IS NULL AND expenses_generation IS NULL))
);

CREATE FUNCTION core.finance_real_maintenance() RETURNS boolean
LANGUAGE sql STABLE SET search_path=pg_catalog AS $$
 SELECT session_user='household_owner' OR(current_user='household_owner' AND current_setting('role',true)='household_owner'
 AND session_user<>'household_app' AND pg_has_role(session_user,'household_owner','MEMBER'))
$$;
REVOKE ALL ON FUNCTION core.finance_real_maintenance() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_real_maintenance() TO household_app;

CREATE FUNCTION core.finance_actor(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE result jsonb; binding core.finance_transaction_authorities%ROWTYPE;
BEGIN
 IF p_session IS NOT NULL THEN
  SELECT jsonb_build_object('household_id',h.id,'user_id',u.id,'membership_id',m.id,'role',m.role,'auth_version',u.auth_version,
   'generation',s.data_generation,'state_version',s.state_version,'timezone',h.timezone,'currency',h.currency_code,
   'session_id',a.id,'session_created_at',a.created_at,'read_at',transaction_timestamp()) INTO result
  FROM core.households h JOIN core.memberships m ON m.household_id=h.id AND m.id=p_membership AND m.user_id=p_user AND m.inactive_at IS NULL
  JOIN core.users u ON u.id=m.user_id AND u.disabled_at IS NULL AND NOT u.must_change_password AND u.auth_version=p_auth
  JOIN core.sessions a ON a.id=p_session AND a.user_id=u.id AND a.revoked_at IS NULL AND a.auth_version=u.auth_version
   AND a.idle_expires_at>transaction_timestamp() AND a.absolute_expires_at>transaction_timestamp()
  JOIN core.module_states s ON s.household_id=h.id AND s.module_id='expenses' AND s.state='enabled' AND s.data_generation=p_generation
  WHERE h.id=p_household;
 ELSE
  SELECT * INTO binding FROM core.finance_transaction_authorities t WHERE t.household_id=p_household
   AND t.transaction_id=pg_current_xact_id_if_assigned() AND t.transaction_started_at=transaction_timestamp();
  IF FOUND THEN
   SELECT jsonb_build_object('household_id',h.id,'user_id',u.id,'membership_id',m.id,'role',m.role,'auth_version',u.auth_version,
    'generation',s.data_generation,'state_version',s.state_version,'timezone',h.timezone,'currency',h.currency_code,
    'session_id',NULL,'read_at',transaction_timestamp()) INTO result
   FROM core.households h JOIN core.finance_background_authorities c ON c.household_id=h.id AND c.id=binding.authority_id AND c.enabled
   JOIN core.memberships m ON m.household_id=h.id AND m.id=c.responsible_membership_id AND m.id=p_membership
    AND m.user_id=p_user AND m.inactive_at IS NULL AND m.role IN('admin','editor')
   JOIN core.users u ON u.id=m.user_id AND u.id=c.user_id AND u.disabled_at IS NULL AND NOT u.must_change_password
    AND u.auth_version=p_auth AND c.auth_version=u.auth_version
   JOIN core.module_states s ON s.household_id=h.id AND s.module_id='expenses' AND s.state='enabled'
    AND s.data_generation=p_generation AND c.expenses_generation=s.data_generation
   JOIN core.jobs j ON j.id=binding.job_id AND j.household_id=h.id AND j.module_id='expenses'
    AND j.module_generation=s.data_generation AND j.state='running' AND j.lease_token=binding.lease_token AND j.lease_until>clock_timestamp()
   WHERE h.id=p_household AND c.source_stamp=core.finance_automation_stamp(h.id,c.template_id,c.responsible_membership_id,s.data_generation);
  END IF;
 END IF;
 IF result IS NULL AND p_session IS NULL THEN
  SELECT jsonb_build_object('household_id',h.id,'user_id',u.id,'membership_id',m.id,'role',m.role,'auth_version',u.auth_version,
   'generation',s.data_generation,'state_version',s.state_version,'timezone',h.timezone,'currency',h.currency_code,'session_id',NULL,'read_at',transaction_timestamp()) INTO result
  FROM core.finance_artifact_job_authorities a JOIN core.households h ON h.id=a.household_id
  JOIN core.memberships m ON m.household_id=h.id AND m.id=a.membership_id AND m.user_id=a.actor_user_id AND m.inactive_at IS NULL AND m.role IN('admin','editor')
  JOIN core.users u ON u.id=m.user_id AND u.disabled_at IS NULL AND NOT u.must_change_password AND u.auth_version=a.auth_version
  JOIN core.module_states s ON s.household_id=h.id AND s.module_id='expenses' AND s.state='enabled' AND s.data_generation=a.expenses_generation
  JOIN core.attachments f ON f.id=a.source_attachment_id AND f.household_id=h.id AND f.state='ready'
  JOIN core.module_states fs ON fs.household_id=h.id AND fs.module_id=f.module_id AND fs.state='enabled' AND fs.data_generation=f.module_generation
  JOIN core.jobs j ON j.id=a.job_id AND j.household_id=h.id AND j.module_id=f.module_id AND j.module_generation=f.module_generation
   AND j.kind IN('thumbnail','capture_ocr') AND j.state='running' AND j.lease_token=a.lease_token AND j.lease_until>clock_timestamp()
  JOIN core.finance_origin_resources o ON o.household_id=h.id AND o.resource_type='attachment' AND o.resource_id=f.id AND o.module_generation=s.data_generation AND o.retired_at IS NULL
  WHERE h.id=p_household AND a.authority_kind='artifact' AND m.id=p_membership AND u.id=p_user AND u.auth_version=p_auth AND s.data_generation=p_generation
   AND a.transaction_id=pg_current_xact_id_if_assigned() AND a.transaction_started_at=transaction_timestamp()
   AND core.finance_set_visible(h.id,o.set_id,u.id,m.id,s.data_generation,true);
 END IF;
 IF result IS NULL THEN RAISE EXCEPTION 'current Finance authority required' USING ERRCODE='42501';END IF;
 RETURN result;
END $$;
REVOKE ALL ON FUNCTION core.finance_actor(uuid,uuid,uuid,uuid,bigint,bigint) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_policy_allowed(p_household uuid,p_policy uuid,p_membership uuid,p_generation bigint,p_edit boolean)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT EXISTS(SELECT 1 FROM core.financial_policies p WHERE p.household_id=p_household AND p.id=p_policy
 AND p.module_id='expenses' AND p.module_generation=p_generation AND p.retired_at IS NULL
 AND(p.access_mode='household' OR(p.access_mode='selected' AND EXISTS(SELECT 1 FROM core.financial_policy_grants g
 WHERE g.household_id=p.household_id AND g.policy_id=p.id AND g.membership_id=p_membership AND(NOT p_edit OR g.permission='edit')))))
$$;
REVOKE ALL ON FUNCTION core.finance_policy_allowed(uuid,uuid,uuid,bigint,boolean) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_domain_kind(p_kind text) RETURNS boolean
LANGUAGE sql IMMUTABLE SET search_path=pg_catalog AS $$
 SELECT p_kind=ANY(ARRAY['entries','entry_lines','accounts','entry_finance','entry_original_lines','cash_events','account_movements','entry_relations',
 'entry_split_sets','entry_person_shares','settlement_details','saved_filters','category_budgets','savings_goals','goal_contributions',
 'recurring_templates','recurring_occurrences','categorization_rules','bank_profiles','reconciliations','reconciliation_items','receipt_reviews','receipt_review_lines'])
$$;
REVOKE ALL ON FUNCTION core.finance_domain_kind(text) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_domain_exists(p_household uuid,p_kind text,p_id uuid) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE found boolean;
BEGIN
 IF NOT core.finance_domain_kind(p_kind) OR p_id IS NULL OR to_regclass(format('expenses.%I',p_kind)) IS NULL THEN RETURN false;END IF;
 EXECUTE format('SELECT EXISTS(SELECT 1 FROM expenses.%I WHERE household_id=$1 AND id=$2)',p_kind) INTO found USING p_household,p_id;
 RETURN coalesce(found,false);
END $$;
REVOKE ALL ON FUNCTION core.finance_domain_exists(uuid,text,uuid) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_parent(p_kind text,p_row jsonb) RETURNS jsonb LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
DECLARE kind text;key text;
BEGIN
 CASE p_kind
 WHEN 'entry_lines','entry_finance','entry_original_lines','entry_relations','entry_split_sets','entry_person_shares' THEN kind='entries';key='entry_id';
 WHEN 'account_movements','settlement_details' THEN kind='cash_events';key='event_id';
 WHEN 'goal_contributions' THEN kind='savings_goals';key='goal_id';
 WHEN 'recurring_occurrences' THEN kind='recurring_templates';key='template_id';
 WHEN 'reconciliation_items' THEN kind='reconciliations';key='reconciliation_id';
 WHEN 'receipt_review_lines' THEN kind='receipt_reviews';key='review_id';
 WHEN 'cash_events' THEN IF p_row->>'entry_id' IS NOT NULL THEN kind='entries';key='entry_id';END IF;
 WHEN 'entries','accounts','saved_filters','category_budgets','savings_goals','recurring_templates','categorization_rules','bank_profiles','reconciliations','receipt_reviews' THEN RETURN NULL;
 ELSE RAISE EXCEPTION 'unknown financial parent kind' USING ERRCODE='22023';
 END CASE;
 IF kind IS NULL THEN RETURN NULL;END IF;
 RETURN jsonb_build_object('kind',kind,'id',p_row->>key);
END $$;
REVOKE ALL ON FUNCTION core.finance_parent(text,jsonb) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_binding_policy(p_household uuid,p_kind text,p_id uuid,p_policy uuid,p_generation bigint) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE item jsonb;parent jsonb;
BEGIN
 IF NOT core.finance_domain_kind(p_kind) THEN RETURN false;END IF;
 EXECUTE format('SELECT to_jsonb(t) FROM expenses.%I t WHERE household_id=$1 AND id=$2',p_kind) INTO item USING p_household,p_id;
 IF item IS NULL THEN RETURN false;END IF;
 parent=core.finance_parent(p_kind,item);
 IF parent IS NULL THEN RETURN EXISTS(SELECT 1 FROM core.financial_policies WHERE household_id=p_household AND id=p_policy AND module_generation=p_generation
  AND resource_type=p_kind AND resource_id=p_id AND retired_at IS NULL);END IF;
 RETURN EXISTS(SELECT 1 FROM core.financial_subjects WHERE household_id=p_household AND module_generation=p_generation AND resource_type=parent->>'kind'
  AND resource_id=(parent->>'id')::uuid AND policy_id=p_policy AND retired_at IS NULL);
END $$;
REVOKE ALL ON FUNCTION core.finance_binding_policy(uuid,text,uuid,uuid,bigint) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_domain_matches(p_household uuid,p_kind text,p_id uuid,p_hash bytea,p_version bigint) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE item jsonb;
BEGIN
 IF NOT core.finance_domain_kind(p_kind) THEN RETURN false;END IF;
 EXECUTE format('SELECT to_jsonb(t) FROM expenses.%I t WHERE household_id=$1 AND id=$2',p_kind) INTO item USING p_household,p_id;
 RETURN item IS NOT NULL AND sha256(convert_to(item::text,'UTF8'))=p_hash AND(item->>'version')::bigint=p_version;
END $$;
REVOKE ALL ON FUNCTION core.finance_domain_matches(uuid,text,uuid,bytea,bigint) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_root_live(p_root uuid,p_user uuid,p_membership uuid) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE operation core.finance_origin_operations%ROWTYPE;cursor_key uuid;depth integer;
BEGIN
 SELECT * INTO operation FROM core.finance_origin_operations WHERE id=p_root;
 IF NOT FOUND THEN RETURN false;END IF;
 -- Committed roots never need an in-flight ancestor walk. Keep that uncommon
 -- path out of every ordinary source/child permission query.
 IF operation.state='complete' THEN RETURN true;END IF;
 IF operation.state<>'pending' OR p_user IS NULL OR p_membership IS NULL
  OR operation.transaction_id IS DISTINCT FROM pg_current_xact_id_if_assigned()
  OR operation.transaction_started_at IS DISTINCT FROM transaction_timestamp()
  OR operation.actor_user_id IS DISTINCT FROM p_user OR operation.membership_id IS DISTINCT FROM p_membership THEN RETURN false;END IF;
 cursor_key=nullif(current_setting('household.finance_origin',true),'')::uuid;
 FOR depth IN 1..16 LOOP
  IF cursor_key IS NULL THEN RETURN false;END IF;
  IF cursor_key=p_root THEN RETURN true;END IF;
  SELECT parent_id INTO cursor_key FROM core.finance_origin_operations WHERE id=cursor_key;
  IF NOT FOUND THEN RETURN false;END IF;
 END LOOP;
 RETURN false;
END
$$;
REVOKE ALL ON FUNCTION core.finance_root_live(uuid,uuid,uuid) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_set_visible(p_household uuid,p_set uuid,p_user uuid,p_membership uuid,p_generation bigint,p_edit boolean)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT EXISTS(SELECT 1 FROM core.financial_requirement_sets x WHERE x.household_id=p_household AND x.id=p_set
 AND(x.classification IN('financial','nonfinancial') OR(x.classification='private_import' AND x.creator_user_id=p_user))
 AND x.required_count=(SELECT count(*) FROM core.financial_requirement_members m WHERE m.household_id=x.household_id AND m.set_id=x.id)
 AND NOT EXISTS(SELECT 1 FROM core.financial_requirement_members m
 LEFT JOIN core.financial_subjects s ON s.household_id=m.household_id AND s.id=m.subject_id AND s.policy_id=m.policy_id
 LEFT JOIN core.finance_origin_resources o ON o.household_id=s.household_id AND o.subject_id=s.id
 LEFT JOIN core.financial_requirement_sets sx ON sx.household_id=o.household_id AND sx.id=o.set_id
 WHERE m.household_id=x.household_id AND m.set_id=x.id AND(s.id IS NULL OR s.retired_at IS NOT NULL
 OR s.module_id<>'expenses' OR s.module_generation<>p_generation OR o.id IS NULL OR o.retired_at IS NOT NULL
 OR o.resource_type<>s.resource_type OR o.resource_id<>s.resource_id OR o.module_generation<>s.module_generation
 OR sx.id IS NULL OR sx.classification NOT IN('financial','nonfinancial') OR NOT core.finance_root_live(o.root_id,p_user,p_membership)
 OR NOT core.finance_domain_exists(s.household_id,s.resource_type,s.resource_id)
 OR NOT core.finance_domain_matches(s.household_id,s.resource_type,s.resource_id,o.row_hash,o.resource_version)
 OR NOT core.finance_binding_policy(s.household_id,s.resource_type,s.resource_id,s.policy_id,p_generation)
 OR NOT core.finance_policy_allowed(m.household_id,m.policy_id,p_membership,p_generation,p_edit))))
$$;
REVOKE ALL ON FUNCTION core.finance_set_visible(uuid,uuid,uuid,uuid,bigint,boolean) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_child_binding_live(p_household uuid,p_kind text,p_row jsonb,p_generation bigint,p_user uuid,p_membership uuid)
RETURNS boolean LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE subject core.financial_subjects%ROWTYPE;head core.finance_origin_resources%ROWTYPE;classification text;
BEGIN
 SELECT * INTO subject FROM core.financial_subjects WHERE household_id=p_household AND module_generation=p_generation
  AND resource_type=p_kind AND resource_id=(p_row->>'id')::uuid AND retired_at IS NULL;
 IF NOT FOUND THEN RETURN false;END IF;
 SELECT * INTO head FROM core.finance_origin_resources WHERE household_id=p_household AND module_generation=p_generation
  AND resource_type=p_kind AND resource_id=(p_row->>'id')::uuid AND subject_id=subject.id AND row_number=0 AND audit_id IS NULL AND retired_at IS NULL;
 IF NOT FOUND OR head.resource_version IS DISTINCT FROM (p_row->>'version')::bigint
  OR head.row_hash IS DISTINCT FROM sha256(convert_to(p_row::text,'UTF8')) THEN RETURN false;END IF;
 SELECT x.classification INTO classification FROM core.financial_requirement_sets x WHERE x.household_id=p_household AND x.id=head.set_id;
 IF classification IS NULL OR classification NOT IN('financial','nonfinancial') THEN RETURN false;END IF;
 RETURN core.finance_binding_policy(p_household,p_kind,(p_row->>'id')::uuid,subject.policy_id,p_generation)
  AND core.finance_root_live(head.root_id,p_user,p_membership);
END $$;
REVOKE ALL ON FUNCTION core.finance_child_binding_live(uuid,text,jsonb,bigint,uuid,uuid) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_binding_complete(p_household uuid,p_kind text,p_id uuid,p_generation bigint,p_user uuid,p_membership uuid)
RETURNS boolean LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE child text;parent_column text;item text[];policy_key uuid;children text[][];child_row jsonb;inspected integer=0;dangling record;pending_row jsonb;
BEGIN
 SELECT policy_id INTO policy_key FROM core.financial_subjects WHERE household_id=p_household AND module_generation=p_generation
  AND resource_type=p_kind AND resource_id=p_id AND retired_at IS NULL;
 IF p_kind IN('entries','cash_events','savings_goals','recurring_templates','reconciliations','receipt_reviews') THEN
  -- One new append-only reversal subject precedes its own head inside the
  -- trigger statement. Only that exact validated current target may be pending.
  -- Another missing/retired head, an absent row or an unrelated root still deny.
  FOR dangling IN SELECT s.resource_type,s.resource_id,o.id IS NULL AS missing_head FROM core.financial_subjects s LEFT JOIN core.finance_origin_resources o ON o.household_id=s.household_id AND o.subject_id=s.id
  WHERE s.household_id=p_household AND s.module_generation=p_generation AND s.policy_id=policy_key AND s.retired_at IS NULL
   AND(o.id IS NULL OR o.retired_at IS NOT NULL OR NOT core.finance_domain_exists(s.household_id,s.resource_type,s.resource_id)) LIMIT 2 LOOP
   IF p_kind<>'savings_goals' OR dangling.resource_type<>'goal_contributions'
    OR NOT dangling.missing_head THEN RETURN false;END IF;
   EXECUTE 'SELECT to_jsonb(t) FROM expenses.goal_contributions t WHERE household_id=$1 AND id=$2'
    INTO pending_row USING p_household,dangling.resource_id;
   IF pending_row IS NULL OR pending_row->>'contribution_kind'<>'reversal' OR pending_row->>'reverses_contribution_id' IS NULL
    OR NOT EXISTS(SELECT 1 FROM core.financial_policies p WHERE p.household_id=p_household AND p.id=policy_key
     AND p.module_generation=p_generation AND p.resource_type='savings_goals' AND p.resource_id=(pending_row->>'goal_id')::uuid
     AND p.retired_at IS NULL)
    OR NOT EXISTS(SELECT 1 FROM core.finance_origin_operations operation WHERE operation.household_id=p_household
     AND operation.expenses_generation=p_generation AND operation.state='pending' AND operation.origin='reversal'
     AND operation.transaction_id=pg_current_xact_id_if_assigned()
     AND operation.transaction_started_at=transaction_timestamp() AND operation.target_kind='goal_contributions'
     AND operation.target_id=dangling.resource_id AND operation.actor_user_id=p_user AND operation.membership_id=p_membership
     AND operation.id::text=current_setting('household.finance_origin',true)
     AND core.finance_root_live(operation.id,p_user,p_membership)) THEN RETURN false;END IF;
  END LOOP;
 END IF;
 IF p_kind='entries' THEN
  children=ARRAY[
   ['entry_lines','entry_id'],['entry_finance','entry_id'],['entry_original_lines','entry_id'],
   ['entry_split_sets','entry_id'],['entry_person_shares','entry_id']];
 ELSIF p_kind='cash_events' THEN
  children=ARRAY[['account_movements','event_id']];
 ELSE RETURN true;
 END IF;
 -- Resolve one exact head per actual child. A flattened anti-join let the
 -- planner evaluate root liveness across a subjects/sets/heads cross product
 -- on a newly populated database, producing over a million security calls.
 FOREACH item SLICE 1 IN ARRAY children LOOP
  child=item[1];parent_column=item[2];
  IF to_regclass(format('expenses.%I',child)) IS NULL THEN RETURN false;END IF;
  FOR child_row IN EXECUTE format('SELECT to_jsonb(c) FROM expenses.%I c WHERE c.household_id=$1 AND c.%I=$2 ORDER BY c.id LIMIT 1025',child,parent_column)
   USING p_household,p_id LOOP
   inspected=inspected+1;
   IF inspected>1024 OR NOT core.finance_child_binding_live(p_household,child,child_row,p_generation,p_user,p_membership) THEN RETURN false;END IF;
  END LOOP;
 END LOOP;
 RETURN true;
END $$;
REVOKE ALL ON FUNCTION core.finance_binding_complete(uuid,text,uuid,bigint,uuid,uuid) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_resource_visible(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,
 p_kind text,p_id uuid,p_intent text) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE actor jsonb;item core.finance_origin_resources%ROWTYPE;subject core.financial_subjects%ROWTYPE;
 owner core.finance_private_owners%ROWTYPE;requires_edit boolean;row_value jsonb;parent jsonb;
BEGIN
 IF p_intent NOT IN('view','edit','manage','preference') OR p_kind IS NULL OR p_id IS NULL THEN RETURN false;END IF;
 actor=core.finance_actor(p_household,p_user,p_membership,p_session,p_auth,p_generation);
 requires_edit=p_intent IN('edit','manage');
 IF requires_edit AND actor->>'role' NOT IN('admin','editor') THEN RETURN false;END IF;
 IF p_intent='preference' AND p_kind<>'saved_filters' THEN RETURN false;END IF;
 SELECT * INTO item FROM core.finance_origin_resources o WHERE o.household_id=p_household AND o.module_generation=p_generation
 AND o.resource_type=p_kind AND o.resource_id=p_id AND o.row_number=0 AND o.audit_id IS NULL;
 IF NOT FOUND OR item.retired_at IS NOT NULL OR NOT core.finance_root_live(item.root_id,p_user,p_membership) THEN RETURN false;END IF;
 IF item.private_owner_id IS NOT NULL THEN
  SELECT * INTO owner FROM core.finance_private_owners o WHERE o.household_id=p_household AND o.id=item.private_owner_id;
  IF NOT FOUND OR owner.owner_user_id<>p_user OR owner.retired_at IS NOT NULL OR owner.resource_type<>p_kind OR owner.resource_id<>p_id OR owner.module_generation<>p_generation THEN RETURN false;END IF;
  IF p_kind='saved_filters' AND p_intent='preference' THEN RETURN core.finance_domain_exists(p_household,p_kind,p_id);END IF;
  IF p_intent='manage' THEN RETURN false;END IF;
 ELSIF item.subject_id IS NOT NULL THEN
  SELECT * INTO subject FROM core.financial_subjects s WHERE s.household_id=p_household AND s.id=item.subject_id;
  IF NOT FOUND OR subject.retired_at IS NOT NULL OR subject.module_generation<>p_generation OR subject.resource_type<>p_kind OR subject.resource_id<>p_id
   OR NOT core.finance_policy_allowed(p_household,subject.policy_id,p_membership,p_generation,requires_edit) THEN RETURN false;END IF;
  IF p_intent='manage' AND NOT EXISTS(SELECT 1 FROM core.financial_policies p WHERE p.household_id=p_household AND p.id=subject.policy_id
   AND p.responsible_membership_id=p_membership) THEN RETURN false;END IF;
 ELSE
  IF p_kind NOT IN('attachment','attachment_derivative','capture','import_batch') THEN RETURN false;END IF;
 END IF;
 IF core.finance_domain_kind(p_kind) THEN
  IF to_regclass(format('expenses.%I',p_kind)) IS NULL THEN RETURN false;END IF;
  EXECUTE format('SELECT to_jsonb(t) FROM expenses.%I t WHERE household_id=$1 AND id=$2',p_kind) INTO row_value USING p_household,p_id;
  IF row_value IS NULL OR sha256(convert_to(row_value::text,'UTF8')) IS DISTINCT FROM item.row_hash
   OR (row_value->>'version')::bigint IS DISTINCT FROM item.resource_version THEN RETURN false;END IF;
  parent=core.finance_parent(p_kind,row_value);
 END IF;
 IF item.subject_id IS NOT NULL AND NOT core.finance_binding_policy(p_household,p_kind,p_id,subject.policy_id,p_generation) THEN RETURN false;END IF;
 IF NOT core.finance_domain_kind(p_kind) AND NOT core.finance_artifact_live(p_household,p_kind,p_id) THEN RETURN false;END IF;
 IF NOT core.finance_set_visible(p_household,item.set_id,p_user,p_membership,p_generation,requires_edit) THEN RETURN false;END IF;
 -- The fixed typed-parent graph is acyclic. A child's intact own head cannot
 -- make a damaged or no-longer-authorized parent readable through child APIs.
 -- Parent completeness checks inspect exact child heads without recursing.
 IF parent IS NOT NULL AND NOT core.finance_resource_visible(p_household,p_user,p_membership,p_session,p_auth,p_generation,
  parent->>'kind',(parent->>'id')::uuid,p_intent) THEN RETURN false;END IF;
 RETURN core.finance_binding_complete(p_household,p_kind,p_id,p_generation,p_user,p_membership);
EXCEPTION WHEN insufficient_privilege THEN RETURN false;
END $$;
REVOKE ALL ON FUNCTION core.finance_resource_visible(uuid,uuid,uuid,uuid,bigint,bigint,text,uuid,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_resource_visible(uuid,uuid,uuid,uuid,bigint,bigint,text,uuid,text) TO household_app;

CREATE FUNCTION core.finance_shared_source(p_household uuid,p_kind text,p_id uuid) RETURNS jsonb
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE schema_name text;table_name text;current_generation bigint;value jsonb;source_zone text;
BEGIN
 CASE p_kind WHEN 'shared_fuel_fillups' THEN schema_name='fuel';table_name='fillups';
 WHEN 'shared_fuel_vehicle_costs' THEN schema_name='fuel';table_name='vehicle_costs';
 WHEN 'shared_shopping_checkout' THEN schema_name='shopping';table_name='checkouts';
 ELSE RAISE EXCEPTION 'unknown independent source' USING ERRCODE='22023';END CASE;
 SELECT data_generation INTO current_generation FROM core.module_states WHERE household_id=p_household AND module_id=schema_name AND state='enabled';
 IF current_generation IS NULL OR to_regclass(format('%I.%I',schema_name,table_name)) IS NULL THEN RETURN NULL;END IF;
 IF schema_name='fuel' THEN
  SELECT reset_val INTO source_zone FROM pg_settings WHERE name='TimeZone';
  IF source_zone IS NULL THEN RETURN NULL;END IF;
  PERFORM set_config('TimeZone',source_zone,true);
 END IF;
 EXECUTE format('SELECT to_jsonb(t) FROM %I.%I t WHERE household_id=$1 AND id=$2',schema_name,table_name) INTO value USING p_household,p_id;
 IF value IS NULL THEN RETURN NULL;END IF;
 IF schema_name='fuel' AND NOT EXISTS(SELECT 1 FROM fuel.module_effects e JOIN fuel.module_operations o ON o.id=e.operation_id
  WHERE o.household_id=p_household AND o.generation=current_generation AND e.relation='fuel.'||table_name AND e.record_id=p_id
   AND e.after_sha256=sha256(convert_to(value::text,'UTF8')) AND e.after_version=(value->>'version')::bigint
   AND(o.state='complete' OR o.state='pending' AND o.current_txid=pg_current_xact_id_if_assigned()
    AND o.id=nullif(current_setting('household.fuel_operation',true),'')::uuid)) THEN RETURN NULL;END IF;
 RETURN jsonb_build_object('kind',p_kind,'id',p_id,'generation',current_generation,'version',coalesce(value->>'version','1'));
END $$;
REVOKE ALL ON FUNCTION core.finance_shared_source(uuid,text,uuid) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_shared_protected_refs(p_household uuid,p_refs jsonb) RETURNS jsonb
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE refs jsonb=p_refs;ref jsonb;capture record;classification text;inspected integer=0;
BEGIN
 FOR ref IN SELECT value FROM jsonb_array_elements(p_refs) WHERE value->>'kind' IN('shared_fuel_fillups','shared_fuel_vehicle_costs') LOOP
  FOR capture IN SELECT c.source_attachment_id attachment_id FROM core.capture_jobs c WHERE c.household_id=p_household AND c.module_id='fuel' AND c.result_resource_id=(ref->>'id')::uuid
   UNION SELECT c.attachment_id FROM fuel.capture_jobs c WHERE c.household_id=p_household AND c.record_id=(ref->>'id')::uuid LIMIT 1025 LOOP
   inspected=inspected+1;IF inspected>1024 THEN RAISE EXCEPTION 'capture source closure exceeds bound' USING ERRCODE='22023';END IF;
   SELECT s.classification INTO classification FROM core.financial_artifact_scope_versions a JOIN core.financial_requirement_sets s ON s.household_id=a.household_id AND s.id=a.set_id
    WHERE a.household_id=p_household AND a.artifact_kind='attachment' AND a.object_id=capture.attachment_id ORDER BY a.revision DESC LIMIT 1;
   IF classification IS NULL OR classification='unresolved' THEN RAISE EXCEPTION 'original capture source unresolved' USING ERRCODE='P0002';END IF;
   IF classification='financial' THEN refs=refs||jsonb_build_array(jsonb_build_object('kind','attachment','id',capture.attachment_id));END IF;
   IF jsonb_array_length(refs)>1024 THEN RAISE EXCEPTION 'capture source closure exceeds bound' USING ERRCODE='22023';END IF;
  END LOOP;
 END LOOP;
 RETURN(SELECT coalesce(jsonb_agg(DISTINCT value ORDER BY value),'[]') FROM jsonb_array_elements(refs));
END $$;
REVOKE ALL ON FUNCTION core.finance_shared_protected_refs(uuid,jsonb) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_reference(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,
 p_ref jsonb,p_intent text) RETURNS core.finance_origin_resources
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE item core.finance_origin_resources%ROWTYPE;kind text=p_ref->>'kind';key uuid;row_key integer;history bigint;
BEGIN
 IF jsonb_typeof(p_ref)<>'object' OR EXISTS(SELECT 1 FROM jsonb_object_keys(p_ref) k WHERE k NOT IN('kind','id','row_number','audit_id')) THEN
  RAISE EXCEPTION 'invalid typed source' USING ERRCODE='22023';END IF;
 IF kind='audit' THEN
  history=(p_ref->>'audit_id')::bigint;
  IF history IS NULL OR history<=0 OR p_ref ? 'id' OR p_ref ? 'row_number' THEN RAISE EXCEPTION 'invalid audit source' USING ERRCODE='22023';END IF;
  SELECT * INTO item FROM core.finance_origin_resources WHERE household_id=p_household AND audit_id=history AND resource_type=kind;
 ELSE
  key=(p_ref->>'id')::uuid;row_key=coalesce((p_ref->>'row_number')::integer,0);
  IF key IS NULL OR p_ref ? 'audit_id' OR (kind='import_row')<>(row_key>0) THEN RAISE EXCEPTION 'invalid UUID source' USING ERRCODE='22023';END IF;
  SELECT * INTO item FROM core.finance_origin_resources WHERE household_id=p_household AND module_generation=p_generation
   AND resource_type=kind AND resource_id=key AND row_number=row_key AND audit_id IS NULL;
 END IF;
 IF item.id IS NULL AND kind IN('attachment','attachment_derivative','capture','import_batch') THEN
  IF NOT core.finance_artifact_visible(p_household,p_user,p_membership,p_session,p_auth,kind,key,CASE WHEN p_intent='view' THEN 'view' ELSE 'edit' END) THEN
   RAISE EXCEPTION 'source unavailable' USING ERRCODE='P0002';END IF;
  SELECT a.set_id,a.id,a.revision INTO item.set_id,item.scope_id,item.resource_version FROM core.financial_artifact_scope_versions a
   WHERE a.household_id=p_household AND a.artifact_kind=kind AND a.object_id=key ORDER BY a.revision DESC LIMIT 1;
  item.household_id=p_household;item.resource_type=kind;item.resource_id=key;RETURN item;
 END IF;
 IF item.id IS NULL OR item.retired_at IS NOT NULL OR NOT core.finance_root_live(item.root_id,p_user,p_membership) THEN
  RAISE EXCEPTION 'source unavailable' USING ERRCODE='P0002';END IF;
 IF kind NOT IN('audit','import_row') THEN
  IF NOT core.finance_resource_visible(p_household,p_user,p_membership,p_session,p_auth,p_generation,kind,key,p_intent) THEN
   RAISE EXCEPTION 'source unavailable' USING ERRCODE='P0002';END IF;
 ELSIF NOT core.finance_set_visible(p_household,item.set_id,p_user,p_membership,p_generation,p_intent IN('edit','manage')) THEN
  RAISE EXCEPTION 'source unavailable' USING ERRCODE='P0002';
 ELSIF item.private_owner_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM core.finance_private_owners o
  WHERE o.id=item.private_owner_id AND o.household_id=p_household AND o.owner_user_id=p_user AND o.retired_at IS NULL) THEN
  RAISE EXCEPTION 'source unavailable' USING ERRCODE='P0002';
 END IF;
 RETURN item;
END $$;
REVOKE ALL ON FUNCTION core.finance_reference(uuid,uuid,uuid,uuid,bigint,bigint,jsonb,text) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_authority(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,
 p_refs jsonb,p_intent text) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE actor jsonb;ref jsonb;item core.finance_origin_resources%ROWTYPE;proof jsonb='[]';members jsonb;shared jsonb;
BEGIN
 actor=core.finance_actor(p_household,p_user,p_membership,p_session,p_auth,p_generation);
 IF p_intent NOT IN('view','edit','manage','preference') OR jsonb_typeof(p_refs)<>'array' OR jsonb_array_length(p_refs)>1024
 OR octet_length(p_refs::text)>131072 THEN RAISE EXCEPTION 'bounded authority required' USING ERRCODE='22023';END IF;
 IF p_intent IN('edit','manage') AND actor->>'role' NOT IN('admin','editor') THEN RAISE EXCEPTION 'writer required' USING ERRCODE='42501';END IF;
 p_refs=core.finance_shared_protected_refs(p_household,p_refs);
 FOR ref IN SELECT DISTINCT value FROM jsonb_array_elements(p_refs) ORDER BY value LOOP
  IF ref->>'kind' IN('shared_fuel_fillups','shared_fuel_vehicle_costs','shared_shopping_checkout') THEN
   shared=core.finance_shared_source(p_household,ref->>'kind',(ref->>'id')::uuid);
   IF shared IS NULL THEN RAISE EXCEPTION 'source unavailable' USING ERRCODE='P0002';END IF;
   proof=proof||jsonb_build_array(shared);CONTINUE;
  END IF;
  item=core.finance_reference(p_household,p_user,p_membership,p_session,p_auth,p_generation,ref,p_intent);
  SELECT coalesce(jsonb_agg(jsonb_build_array(s.id,s.version,p.id,p.version) ORDER BY s.id),'[]') INTO members
   FROM core.financial_requirement_members m JOIN core.financial_subjects s ON s.id=m.subject_id AND s.household_id=m.household_id
   JOIN core.financial_policies p ON p.id=m.policy_id AND p.household_id=m.household_id WHERE m.set_id=item.set_id;
  IF item.subject_id IS NOT NULL THEN
   SELECT members||jsonb_build_array(jsonb_build_array(s.id,s.version,p.id,p.version)) INTO members FROM core.financial_subjects s
    JOIN core.financial_policies p ON p.id=s.policy_id AND p.household_id=s.household_id WHERE s.id=item.subject_id;
  END IF;
  proof=proof||jsonb_build_array(jsonb_build_object('ref',ref,'version',item.resource_version,'requirements',members));
 END LOOP;
 RETURN encode(sha256(convert_to(jsonb_build_object('household',p_household,'user',p_user,'membership',p_membership,'session',p_session,
  'auth',p_auth,'generation',p_generation,'state_version',actor->'state_version','role',actor->'role',
  'timezone',actor->'timezone','currency',actor->'currency','resources',proof)::text,'UTF8')),'hex');
END $$;
REVOKE ALL ON FUNCTION core.finance_authority(uuid,uuid,uuid,uuid,bigint,bigint,jsonb,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_authority(uuid,uuid,uuid,uuid,bigint,bigint,jsonb,text) TO household_app;

CREATE FUNCTION core.finance_pending(p_household uuid) RETURNS core.finance_origin_operations
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE item core.finance_origin_operations%ROWTYPE;selected uuid;
BEGIN
 selected=nullif(current_setting('household.finance_origin',true),'')::uuid;
 SELECT * INTO item FROM core.finance_origin_operations WHERE id=selected AND household_id=p_household AND state='pending'
  AND transaction_id=pg_current_xact_id_if_assigned() AND transaction_started_at=transaction_timestamp();
 IF NOT FOUND THEN RAISE EXCEPTION 'prepared financial operation required' USING ERRCODE='42501';END IF;
 PERFORM core.finance_actor(item.household_id,item.actor_user_id,item.membership_id,item.session_id,item.auth_version,item.expenses_generation);
 RETURN item;
END $$;
REVOKE ALL ON FUNCTION core.finance_pending(uuid) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_requirements(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,
 p_refs jsonb,p_prior uuid DEFAULT NULL) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE item core.finance_origin_resources%ROWTYPE;ref jsonb;subjects uuid[]='{}';source_ids uuid[]='{}';set_key uuid=gen_random_uuid();total integer;
BEGIN
 p_refs=core.finance_shared_protected_refs(p_household,p_refs);
 IF p_prior IS NOT NULL THEN SELECT coalesce(array_agg(subject_id),'{}') INTO subjects FROM core.financial_requirement_members WHERE household_id=p_household AND set_id=p_prior;END IF;
 FOR ref IN SELECT DISTINCT value FROM jsonb_array_elements(p_refs) ORDER BY value LOOP
  IF ref->>'kind' IN('shared_fuel_fillups','shared_fuel_vehicle_costs','shared_shopping_checkout') THEN
   IF core.finance_shared_source(p_household,ref->>'kind',(ref->>'id')::uuid) IS NULL THEN RAISE EXCEPTION 'source unavailable' USING ERRCODE='P0002';END IF;
   CONTINUE;
  END IF;
  item=core.finance_reference(p_household,p_user,p_membership,p_session,p_auth,p_generation,ref,'view');
  SELECT coalesce(array_agg(subject_id),'{}') INTO source_ids FROM core.financial_requirement_members WHERE household_id=p_household AND set_id=item.set_id;
  subjects=subjects||source_ids;
  IF item.subject_id IS NOT NULL THEN subjects=array_append(subjects,item.subject_id);END IF;
  IF item.resource_type='entries' THEN
   SELECT coalesce(array_agg(s.id),'{}') INTO source_ids FROM core.financial_subjects s JOIN expenses.entry_lines l ON l.household_id=s.household_id AND l.id=s.resource_id
    WHERE s.household_id=p_household AND s.module_generation=p_generation AND s.resource_type='entry_lines' AND s.retired_at IS NULL AND l.entry_id=item.resource_id;
   subjects=subjects||source_ids;
  ELSIF item.resource_type='cash_events' THEN
   SELECT coalesce(array_agg(s.id),'{}') INTO source_ids FROM core.financial_subjects s JOIN expenses.account_movements m ON m.household_id=s.household_id AND m.id=s.resource_id
    WHERE s.household_id=p_household AND s.module_generation=p_generation AND s.resource_type='account_movements' AND s.retired_at IS NULL AND m.event_id=item.resource_id;
   subjects=subjects||source_ids;
  END IF;
 END LOOP;
 SELECT coalesce(array_agg(DISTINCT v ORDER BY v),'{}') INTO subjects FROM unnest(subjects) v;
 total=cardinality(subjects);
 IF total>1024 THEN RAISE EXCEPTION 'financial closure exceeds bound' USING ERRCODE='22023';END IF;
 INSERT INTO core.financial_requirement_sets(id,household_id,classification,required_count,reason_code)
 VALUES(set_key,p_household,CASE WHEN total=0 THEN 'nonfinancial' ELSE 'financial' END,total,
  CASE WHEN total=0 THEN 'nonfinancial_explicit' ELSE 'financial_sources' END);
 INSERT INTO core.financial_requirement_members(household_id,set_id,subject_id,policy_id)
 SELECT p_household,set_key,s.id,s.policy_id FROM core.financial_subjects s WHERE s.household_id=p_household AND s.id=ANY(subjects);
 IF (SELECT count(*) FROM core.financial_requirement_members WHERE set_id=set_key)<>total THEN RAISE EXCEPTION 'missing financial anchor' USING ERRCODE='42501';END IF;
 RETURN set_key;
END $$;
REVOKE ALL ON FUNCTION core.finance_requirements(uuid,uuid,uuid,uuid,bigint,bigint,jsonb,uuid) FROM PUBLIC,household_app;

CREATE OR REPLACE FUNCTION core.guard_financial_requirement_infrastructure() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE household uuid;
BEGIN
 IF core.finance_real_maintenance() THEN IF TG_OP='TRUNCATE' THEN RETURN NULL;ELSIF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;END IF;
 IF current_user<>'household_owner' OR TG_OP<>'INSERT' THEN RAISE EXCEPTION 'immutable financial closure' USING ERRCODE='42501';END IF;
 -- Only private owner-defined factories have table INSERT privilege here.
 -- App stays an invoker and cannot turn a custom setting into a factory frame.
 household=NEW.household_id;
 -- Direct private factories require a real prepared transaction. Nested
 -- evidence is emitted only by installed owner-defined business/artifact
 -- triggers; an unrelated owner-definer call is not a producer capability.
 IF pg_trigger_depth()=1 THEN PERFORM core.finance_pending(household);END IF;
 IF NOT EXISTS(SELECT 1 FROM core.households WHERE id=household) THEN RAISE EXCEPTION 'unknown closure household' USING ERRCODE='42501';END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION core.finance_origin_begin(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,
 p_kind text,p_id uuid,p_origin text,p_sources jsonb,p_initial jsonb) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE actor jsonb;parent core.finance_origin_operations%ROWTYPE;prior core.finance_origin_resources%ROWTYPE;
 key uuid=gen_random_uuid();parent_key uuid;level integer=1;prior_set uuid;ignored text;
BEGIN
 actor=core.finance_actor(p_household,p_user,p_membership,p_session,p_auth,p_generation);
 IF p_origin NOT IN('manual','bank','recurrence','correction','refund','reversal','fuel','capture','import','preference','access') OR p_id IS NULL
 OR NOT(core.finance_domain_kind(p_kind) OR p_kind IN('attachment','attachment_derivative','capture','import_batch'))
 OR jsonb_typeof(p_sources)<>'array' OR jsonb_array_length(p_sources)>1024 OR octet_length(p_sources::text)>131072 THEN
  RAISE EXCEPTION 'invalid prepared financial target' USING ERRCODE='22023';END IF;
 IF actor->>'role' NOT IN('admin','editor') AND NOT(p_kind='saved_filters' AND p_origin='preference') THEN
  RAISE EXCEPTION 'writer required' USING ERRCODE='42501';END IF;
 IF p_session IS NULL AND p_origin NOT IN('recurrence','correction','reversal','refund','capture') THEN RAISE EXCEPTION 'bounded worker origin required' USING ERRCODE='42501';END IF;
 PERFORM id FROM core.households WHERE id=p_household FOR SHARE;
 PERFORM id FROM core.users WHERE id=p_user AND auth_version=p_auth AND disabled_at IS NULL AND NOT must_change_password FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'current financial actor changed' USING ERRCODE='42501';END IF;
 IF p_session IS NOT NULL THEN
  PERFORM id FROM core.sessions WHERE id=p_session AND user_id=p_user AND auth_version=p_auth AND revoked_at IS NULL
   AND idle_expires_at>transaction_timestamp() AND absolute_expires_at>transaction_timestamp() FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION 'current financial session changed' USING ERRCODE='42501';END IF;
 END IF;
 PERFORM module_id FROM core.module_states WHERE household_id=p_household AND module_id='expenses' AND state='enabled' AND data_generation=p_generation FOR SHARE;
 PERFORM id FROM core.memberships WHERE household_id=p_household AND id=p_membership AND inactive_at IS NULL FOR SHARE;
 IF nullif(current_setting('household.finance_origin',true),'') IS NOT NULL THEN
  parent=core.finance_pending(p_household);parent_key=parent.id;level=parent.depth+1;
  IF EXISTS(SELECT 1 FROM core.finance_origin_operations WHERE parent_id=parent.id AND state='pending') THEN RAISE EXCEPTION 'financial child must finish first' USING ERRCODE='23514';END IF;
 END IF;
 IF level>16 THEN RAISE EXCEPTION 'financial nesting bound' USING ERRCODE='22023';END IF;
 SELECT * INTO prior FROM core.finance_origin_resources WHERE household_id=p_household AND module_generation=p_generation
  AND resource_type=p_kind AND resource_id=p_id AND row_number=0 AND audit_id IS NULL FOR UPDATE;
 IF FOUND THEN
  IF prior.retired_at IS NOT NULL THEN RAISE EXCEPTION 'retired identity unavailable' USING ERRCODE='P0002';END IF;
  ignored=core.finance_authority(p_household,p_user,p_membership,p_session,p_auth,p_generation,
   jsonb_build_array(jsonb_build_object('kind',p_kind,'id',p_id)),CASE WHEN p_kind='saved_filters' THEN 'preference' ELSE 'edit' END);
  prior_set=CASE WHEN p_kind='saved_filters' THEN NULL ELSE prior.set_id END;
  IF p_initial IS NOT NULL AND p_initial<>'null'::jsonb THEN RAISE EXCEPTION 'initial policy belongs only to new target' USING ERRCODE='22023';END IF;
 ELSIF core.finance_domain_kind(p_kind) AND core.finance_domain_exists(p_household,p_kind,p_id) THEN
  RAISE EXCEPTION 'existing target has no proven origin' USING ERRCODE='P0002';
 END IF;
 ignored=core.finance_authority(p_household,p_user,p_membership,p_session,p_auth,p_generation,p_sources,CASE WHEN p_kind='saved_filters' THEN 'view' ELSE 'edit' END);
 IF p_initial='null'::jsonb THEN p_initial=NULL;END IF;
 IF p_initial IS NOT NULL AND(p_kind NOT IN('entries','accounts','cash_events','category_budgets','savings_goals','recurring_templates','categorization_rules','reconciliations','receipt_reviews')
  OR jsonb_typeof(p_initial)<>'object' OR octet_length(p_initial::text)>32768) THEN RAISE EXCEPTION 'invalid initial policy target' USING ERRCODE='22023';END IF;
 INSERT INTO core.finance_origin_operations(id,household_id,parent_id,provenance_root_id,transaction_id,transaction_started_at,expenses_generation,
  actor_user_id,membership_id,session_id,auth_version,authority_id,target_kind,target_id,origin,source_refs,initial_access,depth)
 VALUES(key,p_household,parent_key,coalesce((SELECT coalesce(provenance_root_id,id) FROM core.finance_origin_operations WHERE id=prior.root_id),key),pg_current_xact_id(),transaction_timestamp(),p_generation,p_user,p_membership,p_session,p_auth,
  coalesce((SELECT authority_id FROM core.finance_transaction_authorities WHERE transaction_id=pg_current_xact_id_if_assigned() AND transaction_started_at=transaction_timestamp()),
   (SELECT id FROM core.finance_artifact_job_authorities WHERE transaction_id=pg_current_xact_id_if_assigned() AND transaction_started_at=transaction_timestamp())),
  p_kind,p_id,p_origin,p_sources,p_initial,level);
 PERFORM set_config('household.finance_origin',key::text,true);
 UPDATE core.finance_origin_operations SET inherited_set_id=core.finance_requirements(p_household,p_user,p_membership,p_session,p_auth,p_generation,p_sources,prior_set) WHERE id=key;
 PERFORM p.id FROM core.financial_policies p WHERE p.household_id=p_household AND p.id IN(
  SELECT m.policy_id FROM core.financial_requirement_members m JOIN core.finance_origin_operations o ON o.inherited_set_id=m.set_id WHERE o.id=key
  UNION SELECT policy_id FROM core.financial_subjects WHERE id=prior.subject_id) ORDER BY p.id FOR SHARE;
 RETURN key;
END $$;
REVOKE ALL ON FUNCTION core.finance_origin_begin(uuid,uuid,uuid,uuid,bigint,bigint,text,uuid,text,jsonb,jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_origin_begin(uuid,uuid,uuid,uuid,bigint,bigint,text,uuid,text,jsonb,jsonb) TO household_app;

CREATE FUNCTION core.finance_business_actor(p_household uuid) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE operation core.finance_origin_operations%ROWTYPE;kind text;
BEGIN
 operation=core.finance_pending(p_household);kind=nullif(current_setting('household.audit_kind',true),'');
 IF nullif(current_setting('household.audit_household',true),'') IS DISTINCT FROM p_household::text THEN
  RAISE EXCEPTION 'financial business household mismatch' USING ERRCODE='42501';END IF;
 IF operation.session_id IS NOT NULL THEN
  IF kind IS DISTINCT FROM 'user' OR nullif(current_setting('household.audit_user',true),'') IS DISTINCT FROM operation.actor_user_id::text THEN
   RAISE EXCEPTION 'financial business actor mismatch' USING ERRCODE='42501';END IF;
 ELSE
  IF kind IS DISTINCT FROM 'worker' OR nullif(current_setting('household.audit_user',true),'') IS NOT NULL OR operation.authority_id IS NULL THEN
   RAISE EXCEPTION 'financial worker authority mismatch' USING ERRCODE='42501';END IF;
 END IF;
 RETURN operation.actor_user_id;
END $$;
REVOKE ALL ON FUNCTION core.finance_business_actor(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_business_actor(uuid) TO household_app;

CREATE OR REPLACE FUNCTION core.guard_financial_identity() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE actor uuid;household uuid;policy core.financial_policies%ROWTYPE;
BEGIN
 IF core.finance_real_maintenance() THEN IF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;END IF;
 IF TG_OP='DELETE' OR current_user<>'household_owner' THEN RAISE EXCEPTION 'financial identity uses a private factory' USING ERRCODE='42501';END IF;
 household=NEW.household_id;actor=core.finance_business_actor(household);
 IF TG_OP='INSERT' THEN
  IF NEW.created_by<>actor OR NEW.changed_by<>actor OR NEW.retired_at IS NOT NULL THEN RAISE EXCEPTION 'financial identity author mismatch' USING ERRCODE='23514';END IF;
  NEW.version=1;NEW.created_at=clock_timestamp();
 ELSE
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.household_id IS DISTINCT FROM OLD.household_id OR NEW.module_id IS DISTINCT FROM OLD.module_id
   OR NEW.module_generation IS DISTINCT FROM OLD.module_generation OR NEW.resource_type IS DISTINCT FROM OLD.resource_type
   OR NEW.resource_id IS DISTINCT FROM OLD.resource_id OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at
   OR(OLD.retired_at IS NOT NULL AND NEW.retired_at IS DISTINCT FROM OLD.retired_at) THEN RAISE EXCEPTION 'financial identity is immutable' USING ERRCODE='23514';END IF;
  IF TG_TABLE_NAME='financial_subjects' THEN
   IF NEW.policy_id IS DISTINCT FROM OLD.policy_id THEN RAISE EXCEPTION 'subject policy is immutable' USING ERRCODE='23514';END IF;
  END IF;
  NEW.version=OLD.version+1;
  IF OLD.retired_at IS NULL AND NEW.retired_at IS NOT NULL THEN NEW.retired_at=clock_timestamp();END IF;
 END IF;
 NEW.changed_by=actor;NEW.updated_at=clock_timestamp();
 IF TG_TABLE_NAME='financial_subjects' THEN
  SELECT * INTO STRICT policy FROM core.financial_policies WHERE household_id=household AND id=NEW.policy_id FOR SHARE;
  IF policy.module_generation<>NEW.module_generation OR(policy.retired_at IS NOT NULL AND NEW.retired_at IS NULL) THEN RAISE EXCEPTION 'subject scope mismatch' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION core.check_financial_identity_complete() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE item jsonb;policy core.financial_policies%ROWTYPE;subject core.financial_subjects%ROWTYPE;exists_now boolean;
BEGIN
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 IF TG_TABLE_NAME='financial_policies' THEN
  SELECT * INTO STRICT policy FROM core.financial_policies WHERE id=NEW.id;
  exists_now=core.finance_domain_exists(policy.household_id,policy.resource_type,policy.resource_id);
  IF exists_now IS DISTINCT FROM (policy.retired_at IS NULL) THEN RAISE EXCEPTION 'policy liveness mismatch' USING ERRCODE='23514';END IF;
  IF policy.retired_at IS NULL AND NOT EXISTS(SELECT 1 FROM core.financial_subjects s JOIN core.finance_origin_resources o ON o.subject_id=s.id AND o.household_id=s.household_id
   JOIN core.finance_origin_operations r ON r.household_id=o.household_id AND r.id=o.root_id AND r.state='complete'
   WHERE s.household_id=policy.household_id AND s.policy_id=policy.id AND s.resource_type=policy.resource_type AND s.resource_id=policy.resource_id
    AND s.module_generation=policy.module_generation AND s.retired_at IS NULL AND o.retired_at IS NULL) THEN RAISE EXCEPTION 'incomplete policy origin' USING ERRCODE='23514';END IF;
  IF policy.responsible_membership_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
   WHERE m.household_id=policy.household_id AND m.id=policy.responsible_membership_id AND m.inactive_at IS NULL AND m.role IN('admin','editor')
    AND u.disabled_at IS NULL AND(policy.access_mode='household' OR EXISTS(SELECT 1 FROM core.financial_policy_grants g
    WHERE g.policy_id=policy.id AND g.membership_id=m.id AND g.permission='edit'))) THEN RAISE EXCEPTION 'active responsible editor required' USING ERRCODE='23514';END IF;
 ELSE
  SELECT * INTO STRICT subject FROM core.financial_subjects WHERE id=NEW.id;
  SELECT * INTO STRICT policy FROM core.financial_policies WHERE household_id=subject.household_id AND id=subject.policy_id;
  exists_now=core.finance_domain_exists(subject.household_id,subject.resource_type,subject.resource_id);
  IF exists_now IS DISTINCT FROM(subject.retired_at IS NULL) OR(subject.retired_at IS NULL AND policy.retired_at IS NOT NULL)
   OR NOT EXISTS(SELECT 1 FROM core.finance_origin_resources o JOIN core.finance_origin_operations r ON r.id=o.root_id AND r.household_id=o.household_id
    WHERE o.household_id=subject.household_id AND o.subject_id=subject.id AND r.state='complete' AND(o.retired_at IS NULL)=(subject.retired_at IS NULL)) THEN
   RAISE EXCEPTION 'subject liveness or origin mismatch' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NULL;
END $$;

CREATE FUNCTION core.finance_metadata_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF core.finance_real_maintenance() THEN IF TG_OP='TRUNCATE' THEN RETURN NULL;ELSIF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;END IF;
 IF current_user<>'household_owner' OR TG_OP='TRUNCATE' THEN RAISE EXCEPTION 'private financial metadata factory required' USING ERRCODE='42501';END IF;
 IF TG_OP='DELETE' AND TG_TABLE_NAME NOT IN('financial_policy_grants') THEN RAISE EXCEPTION 'financial evidence is retained' USING ERRCODE='42501';END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;
END $$;
REVOKE ALL ON FUNCTION core.finance_metadata_guard() FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_origin_complete() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE operation core.finance_origin_operations%ROWTYPE;
BEGIN
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 SELECT * INTO STRICT operation FROM core.finance_origin_operations WHERE id=NEW.id;
 IF operation.state<>'complete' OR operation.transaction_id<>pg_current_xact_id_if_assigned() OR operation.transaction_started_at<>transaction_timestamp()
 OR NOT EXISTS(SELECT 1 FROM core.finance_origin_effects e WHERE e.root_id=operation.id AND e.resource_type=operation.target_kind AND e.resource_id=operation.target_id
  AND e.effect IN('insert','update','delete','scope')) THEN RAISE EXCEPTION 'unfinished financial origin' USING ERRCODE='23514';END IF;
 IF EXISTS(SELECT 1 FROM core.finance_audit_bindings a WHERE a.root_id=operation.id AND a.set_id IS NULL) THEN RAISE EXCEPTION 'unfinished causal financial audit' USING ERRCODE='23514';END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.finance_origin_complete() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER finance_origin_complete AFTER INSERT OR UPDATE ON core.finance_origin_operations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.finance_origin_complete();

CREATE FUNCTION core.finance_origin_finish(p_operation uuid,p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE operation core.finance_origin_operations%ROWTYPE;head core.finance_origin_resources%ROWTYPE;actual jsonb;
BEGIN
 operation=core.finance_pending(p_household);
 IF operation.id<>p_operation OR operation.actor_user_id<>p_user OR operation.membership_id<>p_membership OR operation.session_id IS DISTINCT FROM p_session
  OR operation.auth_version<>p_auth OR operation.expenses_generation<>p_generation THEN RAISE EXCEPTION 'origin completion scope mismatch' USING ERRCODE='42501';END IF;
 IF EXISTS(SELECT 1 FROM core.finance_origin_operations WHERE parent_id=operation.id AND state<>'complete') THEN RAISE EXCEPTION 'unfinished nested origin' USING ERRCODE='23514';END IF;
 PERFORM core.finance_finalize_target_sources(operation.id);
 SELECT * INTO head FROM core.finance_origin_resources WHERE household_id=p_household AND module_generation=p_generation
  AND resource_type=operation.target_kind AND resource_id=operation.target_id AND row_number=0;
 IF NOT FOUND OR head.root_id<>operation.id OR NOT EXISTS(SELECT 1 FROM core.finance_origin_effects e WHERE e.root_id=operation.id
  AND e.resource_type=operation.target_kind AND e.resource_id=operation.target_id) THEN RAISE EXCEPTION 'prepared operation has no exact target effect' USING ERRCODE='23514';END IF;
 IF core.finance_domain_kind(operation.target_kind) THEN
  EXECUTE format('SELECT to_jsonb(t) FROM expenses.%I t WHERE household_id=$1 AND id=$2',operation.target_kind) INTO actual USING p_household,operation.target_id;
  IF head.retired_at IS NULL AND(actual IS NULL OR head.row_hash<>sha256(convert_to(actual::text,'UTF8')) OR head.resource_version<>(actual->>'version')::bigint)
   OR head.retired_at IS NOT NULL AND(actual IS NOT NULL OR NOT EXISTS(SELECT 1 FROM core.finance_origin_effects e WHERE e.root_id=operation.id AND e.effect='delete'
     AND e.resource_type=operation.target_kind AND e.resource_id=operation.target_id AND e.before_hash=head.row_hash)) THEN RAISE EXCEPTION 'financial final image mismatch' USING ERRCODE='23514';END IF;
 ELSE
  actual=core.finance_artifact_row(p_household,operation.target_kind,operation.target_id,0);
  IF head.retired_at IS NULL AND(actual IS NULL OR head.row_hash<>sha256(convert_to(actual::text,'UTF8')))
   OR head.retired_at IS NOT NULL AND actual IS NOT NULL THEN RAISE EXCEPTION 'artifact final image mismatch' USING ERRCODE='23514';END IF;
 END IF;
 IF NOT(operation.target_kind='saved_filters' AND head.retired_at IS NOT NULL AND head.private_owner_id IS NOT NULL)
  AND NOT core.finance_set_visible(p_household,head.set_id,p_user,p_membership,p_generation,false) THEN RAISE EXCEPTION 'financial source changed' USING ERRCODE='42501';END IF;
 PERFORM core.finance_finalize_audits(operation.id);
 UPDATE core.finance_origin_operations SET state='complete',completed_at=clock_timestamp() WHERE id=operation.id;
 PERFORM set_config('household.finance_origin',coalesce(operation.parent_id::text,''),true);
END $$;
REVOKE ALL ON FUNCTION core.finance_origin_finish(uuid,uuid,uuid,uuid,uuid,bigint,bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_origin_finish(uuid,uuid,uuid,uuid,uuid,bigint,bigint) TO household_app;

CREATE FUNCTION core.finance_ingest_status(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,
 p_kind text,p_fingerprint bytea) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE claim core.finance_ingest_claims%ROWTYPE;
BEGIN
 PERFORM core.finance_actor(p_household,p_user,p_membership,p_session,p_auth,p_generation);
 IF p_kind NOT IN('bank_transaction','bank_file_row','recurrence') OR octet_length(p_fingerprint)<>32 THEN RAISE EXCEPTION 'invalid ingest identity' USING ERRCODE='22023';END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended(p_household::text||'/'||p_kind||'/'||encode(p_fingerprint,'hex'),711));
 SELECT * INTO claim FROM core.finance_ingest_claims WHERE household_id=p_household AND source_kind=p_kind AND fingerprint=p_fingerprint;
 IF NOT FOUND THEN RETURN 'available';END IF;
 IF claim.expenses_generation=p_generation AND core.finance_resource_visible(p_household,p_user,p_membership,p_session,p_auth,p_generation,claim.target_kind,claim.target_id,'view') THEN RETURN 'duplicate';END IF;
 RETURN 'unavailable';
END $$;
REVOKE ALL ON FUNCTION core.finance_ingest_status(uuid,uuid,uuid,uuid,bigint,bigint,text,bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_ingest_status(uuid,uuid,uuid,uuid,bigint,bigint,text,bytea) TO household_app;

CREATE FUNCTION core.finance_ingest_claim(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,
 p_kind text,p_fingerprint bytea,p_target_kind text,p_target uuid) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE operation core.finance_origin_operations%ROWTYPE;
BEGIN
 IF core.finance_ingest_status(p_household,p_user,p_membership,p_session,p_auth,p_generation,p_kind,p_fingerprint)<>'available'
  OR p_target_kind NOT IN('entries','cash_events') THEN RAISE EXCEPTION 'ingest identity unavailable' USING ERRCODE='40001';END IF;
 operation=core.finance_pending(p_household);
 IF operation.actor_user_id<>p_user OR operation.membership_id<>p_membership OR operation.expenses_generation<>p_generation
  OR NOT(operation.target_kind=p_target_kind AND operation.target_id=p_target AND operation.origin IN('bank','recurrence')
   OR p_kind IN('bank_transaction','bank_file_row') AND operation.target_kind='import_batch' AND operation.origin='import' AND EXISTS(
    SELECT 1 FROM core.import_rows r JOIN core.import_batches b ON b.id=r.batch_id WHERE b.household_id=p_household AND b.id=operation.target_id
     AND b.module_id='expenses' AND b.module_generation=p_generation AND b.created_by=p_user AND b.entity='bank' AND b.state='preview'
     AND r.normalized_values->>'fingerprint'=encode(p_fingerprint,'hex'))
   OR p_kind='recurrence' AND operation.target_kind='recurring_occurrences' AND operation.origin='recurrence'
    AND EXISTS(SELECT 1 FROM jsonb_array_elements(operation.source_refs) r WHERE r->>'kind'='recurring_templates')) THEN
   RAISE EXCEPTION 'ingest target not reserved' USING ERRCODE='42501';END IF;
 INSERT INTO core.finance_ingest_claims(household_id,source_kind,fingerprint,target_kind,target_id,expenses_generation,root_id)
 VALUES(p_household,p_kind,p_fingerprint,p_target_kind,p_target,p_generation,operation.id);
END $$;
REVOKE ALL ON FUNCTION core.finance_ingest_claim(uuid,uuid,uuid,uuid,bigint,bigint,text,bytea,text,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_ingest_claim(uuid,uuid,uuid,uuid,bigint,bigint,text,bytea,text,uuid) TO household_app;

CREATE FUNCTION core.finance_ingest_complete() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
BEGIN
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 IF NOT EXISTS(WITH RECURSIVE roots AS(
  SELECT id,parent_id,target_kind,target_id,state,transaction_id,transaction_started_at,expenses_generation,household_id,1 depth FROM core.finance_origin_operations WHERE id=NEW.root_id
  UNION ALL SELECT c.id,c.parent_id,c.target_kind,c.target_id,c.state,c.transaction_id,c.transaction_started_at,c.expenses_generation,c.household_id,r.depth+1
   FROM core.finance_origin_operations c JOIN roots r ON c.parent_id=r.id AND c.household_id=r.household_id WHERE r.depth<16)
  SELECT 1 FROM roots WHERE household_id=NEW.household_id AND expenses_generation=NEW.expenses_generation AND target_kind=NEW.target_kind AND target_id=NEW.target_id
   AND state='complete' AND transaction_id=pg_current_xact_id_if_assigned() AND transaction_started_at=transaction_timestamp()) THEN
  RAISE EXCEPTION 'ingest claim has no completed exact target' USING ERRCODE='23514';END IF;
 IF NEW.source_kind='recurrence' AND NOT EXISTS(SELECT 1 FROM core.finance_origin_operations r JOIN expenses.recurring_occurrences o
  ON o.household_id=r.household_id AND o.id=r.target_id WHERE r.id=NEW.root_id AND r.target_kind='recurring_occurrences'
   AND NEW.fingerprint=sha256(convert_to(replace(jsonb_build_array('recurrence-v1',o.template_id::text,o.scheduled_on::text)::text,', ',','),'UTF8'))
   AND o.id=core.finance_stable_id(o.template_id,'occurrence/'||o.scheduled_on::text) AND NEW.target_id=core.finance_stable_id(o.id,'draft')
   AND(o.entry_id=NEW.target_id AND NEW.target_kind='entries' OR o.event_id=NEW.target_id AND NEW.target_kind='cash_events')) THEN
  RAISE EXCEPTION 'recurrence fingerprint or result substitution' USING ERRCODE='23514';END IF;
 IF NEW.source_kind IN('bank_transaction','bank_file_row') AND NOT EXISTS(SELECT 1 FROM core.finance_origin_operations o JOIN core.import_batches b ON b.household_id=o.household_id AND b.id=o.target_id
  JOIN core.import_rows r ON r.batch_id=b.id WHERE o.id=NEW.root_id AND b.entity='bank' AND r.result_resource_id=NEW.target_id
   AND r.normalized_values->>'fingerprint'=encode(NEW.fingerprint,'hex') AND NEW.target_id=core.finance_stable_id(b.id,'bank-row/'||r.row_number)
   AND NEW.target_kind=CASE WHEN(r.normalized_values->>'amount_minor')::bigint>0 THEN 'cash_events' ELSE 'entries' END) THEN
  RAISE EXCEPTION 'bank fingerprint or result substitution' USING ERRCODE='23514';END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.finance_ingest_complete() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER finance_ingest_complete AFTER INSERT ON core.finance_ingest_claims DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.finance_ingest_complete();

CREATE FUNCTION core.finance_stable_id(p_parent uuid,p_purpose text) RETURNS uuid LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
DECLARE bytes bytea;encoded text;
BEGIN
 bytes=substring(sha256(convert_to(p_parent::text,'UTF8')||decode('00','hex')||convert_to(p_purpose,'UTF8')) FROM 1 FOR 16);
 bytes=set_byte(bytes,6,(get_byte(bytes,6)&15)|128);bytes=set_byte(bytes,8,(get_byte(bytes,8)&63)|128);encoded=encode(bytes,'hex');
 RETURN(substring(encoded,1,8)||'-'||substring(encoded,9,4)||'-'||substring(encoded,13,4)||'-'||substring(encoded,17,4)||'-'||substring(encoded,21,12))::uuid;
END $$;
REVOKE ALL ON FUNCTION core.finance_stable_id(uuid,text) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_set_automation(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,p_template uuid,p_enabled boolean)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE stamp text;responsible uuid;template jsonb;policy_key uuid;
BEGIN
 stamp=core.finance_authority(p_household,p_user,p_membership,p_session,p_auth,p_generation,
  jsonb_build_array(jsonb_build_object('kind','recurring_templates','id',p_template)),CASE WHEN p_enabled THEN 'manage' ELSE 'edit' END);
 SELECT p.id,p.responsible_membership_id INTO policy_key,responsible FROM core.financial_policies p
  WHERE household_id=p_household AND module_generation=p_generation AND resource_type='recurring_templates' AND resource_id=p_template AND retired_at IS NULL FOR UPDATE;
 IF p_enabled AND(responsible IS NULL OR responsible<>p_membership) THEN RAISE EXCEPTION 'explicit template responsibility required' USING ERRCODE='42501';END IF;
 EXECUTE 'SELECT to_jsonb(t) FROM expenses.recurring_templates t WHERE household_id=$1 AND id=$2 FOR SHARE' INTO template USING p_household,p_template;
 IF template IS NULL OR p_enabled AND NOT(template->>'active')::boolean THEN RAISE EXCEPTION 'active template required' USING ERRCODE='P0002';END IF;
 IF NOT p_enabled THEN
  UPDATE core.finance_background_authorities SET enabled=false,updated_at=clock_timestamp() WHERE household_id=p_household AND expenses_generation=p_generation AND template_id=p_template;
 ELSE
  stamp=core.finance_automation_stamp(p_household,p_template,responsible,p_generation);
  IF stamp IS NULL THEN RAISE EXCEPTION 'complete current automation authority required' USING ERRCODE='42501';END IF;
  INSERT INTO core.finance_background_authorities(household_id,template_id,expenses_generation,responsible_membership_id,user_id,auth_version,template_version,source_stamp,enabled)
  VALUES(p_household,p_template,p_generation,responsible,p_user,p_auth,(template->>'version')::bigint,stamp,true)
  ON CONFLICT(household_id,expenses_generation,template_id) DO UPDATE SET responsible_membership_id=EXCLUDED.responsible_membership_id,user_id=EXCLUDED.user_id,
   auth_version=EXCLUDED.auth_version,template_version=EXCLUDED.template_version,source_stamp=EXCLUDED.source_stamp,enabled=true,updated_at=clock_timestamp();
 END IF;
 INSERT INTO core.finance_security_events(household_id,actor_user_id,request_id,action,policy_id,reason_code)
 VALUES(p_household,p_user,coalesce(nullif(current_setting('household.audit_request',true),'')::uuid,gen_random_uuid()),'automation_consent',policy_key,'explicit_consent');
END $$;
REVOKE ALL ON FUNCTION core.finance_set_automation(uuid,uuid,uuid,uuid,bigint,bigint,uuid,boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_set_automation(uuid,uuid,uuid,uuid,bigint,bigint,uuid,boolean) TO household_app;

CREATE FUNCTION core.finance_automation_info(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,p_template uuid)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE consent core.finance_background_authorities%ROWTYPE;stamp text;version_now bigint;active_now boolean;
BEGIN
 stamp=core.finance_authority(p_household,p_user,p_membership,p_session,p_auth,p_generation,
  jsonb_build_array(jsonb_build_object('kind','recurring_templates','id',p_template)),'view');
 SELECT * INTO consent FROM core.finance_background_authorities WHERE household_id=p_household AND expenses_generation=p_generation AND template_id=p_template;
 IF NOT FOUND OR NOT consent.enabled THEN RETURN jsonb_build_object('automatic',false,'reason','not_enabled');END IF;
 EXECUTE 'SELECT version,active FROM expenses.recurring_templates WHERE household_id=$1 AND id=$2' INTO version_now,active_now USING p_household,p_template;
 IF version_now<>consent.template_version OR NOT coalesce(active_now,false) OR consent.source_stamp IS DISTINCT FROM core.finance_automation_stamp(p_household,p_template,consent.responsible_membership_id,p_generation)
  THEN RETURN jsonb_build_object('automatic',false,'reason','review_required');END IF;
 IF NOT EXISTS(SELECT 1 FROM core.financial_policies p JOIN core.memberships m ON m.household_id=p.household_id AND m.id=p.responsible_membership_id
  JOIN core.users u ON u.id=m.user_id WHERE p.household_id=p_household AND p.module_generation=p_generation AND p.resource_type='recurring_templates'
  AND p.resource_id=p_template AND p.retired_at IS NULL AND m.id=consent.responsible_membership_id AND m.user_id=consent.user_id
  AND m.inactive_at IS NULL AND m.role IN('admin','editor') AND u.disabled_at IS NULL AND NOT u.must_change_password AND u.auth_version=consent.auth_version) THEN
  RETURN jsonb_build_object('automatic',false,'reason','review_required');END IF;
 RETURN jsonb_build_object('automatic',true,'reason','enabled');
END $$;
REVOKE ALL ON FUNCTION core.finance_automation_info(uuid,uuid,uuid,uuid,bigint,bigint,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_automation_info(uuid,uuid,uuid,uuid,bigint,bigint,uuid) TO household_app;

CREATE FUNCTION core.finance_automation_scope(p_job uuid,p_lease uuid,p_template uuid) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE job core.jobs%ROWTYPE;consent core.finance_background_authorities%ROWTYPE;result jsonb;template jsonb;stamp text;
BEGIN
 IF nullif(current_setting('household.audit_kind',true),'') IS DISTINCT FROM 'worker' OR nullif(current_setting('household.audit_user',true),'') IS NOT NULL THEN
  RAISE EXCEPTION 'actual worker context required' USING ERRCODE='42501';END IF;
 SELECT * INTO job FROM core.jobs WHERE id=p_job AND kind='finance_recurrence' AND module_id='expenses' AND state='running'
  AND lease_token=p_lease AND lease_until>clock_timestamp() AND payload->>'template_id'=p_template::text FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'current recurrence lease required' USING ERRCODE='42501';END IF;
 SELECT * INTO consent FROM core.finance_background_authorities WHERE household_id=job.household_id AND template_id=p_template
  AND expenses_generation=job.module_generation AND enabled FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'current recurrence consent required' USING ERRCODE='42501';END IF;
 IF nullif(current_setting('household.audit_household',true),'') IS NOT NULL
  AND nullif(current_setting('household.audit_household',true),'') IS DISTINCT FROM job.household_id::text THEN RAISE EXCEPTION 'worker household mismatch' USING ERRCODE='42501';END IF;
 PERFORM set_config('household.audit_household',job.household_id::text,true);
 PERFORM id FROM core.households WHERE id=job.household_id FOR SHARE;
 PERFORM m.id FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=job.household_id AND m.id=consent.responsible_membership_id
  AND m.user_id=consent.user_id AND m.inactive_at IS NULL AND m.role IN('admin','editor') AND u.auth_version=consent.auth_version AND u.disabled_at IS NULL FOR SHARE OF m,u;
 IF NOT FOUND THEN RAISE EXCEPTION 'current automation responsible required' USING ERRCODE='42501';END IF;
 EXECUTE 'SELECT to_jsonb(t) FROM expenses.recurring_templates t WHERE household_id=$1 AND id=$2 FOR SHARE' INTO template USING job.household_id,p_template;
 IF template IS NULL OR NOT(template->>'active')::boolean OR(template->>'version')::bigint<>consent.template_version THEN
  RAISE EXCEPTION 'template requires renewed consent' USING ERRCODE='42501';END IF;
 IF NOT EXISTS(SELECT 1 FROM core.financial_policies p WHERE p.household_id=job.household_id AND p.resource_type='recurring_templates' AND p.resource_id=p_template
  AND p.module_generation=job.module_generation AND p.retired_at IS NULL AND p.responsible_membership_id=consent.responsible_membership_id) THEN
  RAISE EXCEPTION 'template responsibility changed' USING ERRCODE='42501';END IF;
 INSERT INTO core.finance_transaction_authorities(household_id,authority_id,job_id,lease_token,template_id,transaction_id,transaction_started_at)
 VALUES(job.household_id,consent.id,p_job,p_lease,p_template,pg_current_xact_id(),transaction_timestamp());
 result=core.finance_actor(job.household_id,consent.user_id,consent.responsible_membership_id,NULL,consent.auth_version,job.module_generation);
 stamp=core.finance_authority(job.household_id,consent.user_id,consent.responsible_membership_id,NULL,consent.auth_version,job.module_generation,
  jsonb_build_array(jsonb_build_object('kind','recurring_templates','id',p_template)),'edit');
 RETURN result;
END $$;
REVOKE ALL ON FUNCTION core.finance_automation_scope(uuid,uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_automation_scope(uuid,uuid,uuid) TO household_app;

CREATE FUNCTION core.finance_current_automation_scope(p_household uuid,p_user uuid,p_membership uuid,p_auth bigint) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE generation bigint;
BEGIN
 SELECT data_generation INTO generation FROM core.module_states WHERE household_id=p_household AND module_id='expenses' AND state='enabled';
 RETURN core.finance_actor(p_household,p_user,p_membership,NULL,p_auth,generation);
END $$;
REVOKE ALL ON FUNCTION core.finance_current_automation_scope(uuid,uuid,uuid,bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_current_automation_scope(uuid,uuid,uuid,bigint) TO household_app;

CREATE FUNCTION core.finance_schedule_recurrences() RETURNS integer
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
  WHERE c.enabled ORDER BY c.updated_at,c.id LIMIT 100 FOR SHARE OF c,s,m,u,p SKIP LOCKED LOOP
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
REVOKE ALL ON FUNCTION core.finance_schedule_recurrences() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_schedule_recurrences() TO household_app;

CREATE FUNCTION core.finance_automation_stamp(p_household uuid,p_template uuid,p_membership uuid,p_generation bigint) RETURNS text
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE head core.finance_origin_resources%ROWTYPE;policy core.financial_policies%ROWTYPE;proof jsonb;actor jsonb;
BEGIN
 SELECT * INTO head FROM core.finance_origin_resources WHERE household_id=p_household AND module_generation=p_generation AND resource_type='recurring_templates' AND resource_id=p_template AND retired_at IS NULL;
 SELECT p.* INTO policy FROM core.financial_policies p JOIN core.financial_subjects s ON s.policy_id=p.id AND s.household_id=p.household_id WHERE s.id=head.subject_id AND s.retired_at IS NULL AND p.retired_at IS NULL;
 SELECT jsonb_build_array(m.id,m.role,u.id,u.auth_version) INTO actor FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=p_household AND m.id=p_membership
  AND m.inactive_at IS NULL AND m.role IN('admin','editor') AND u.disabled_at IS NULL AND NOT u.must_change_password;
 IF actor IS NULL OR head.id IS NULL OR policy.responsible_membership_id IS DISTINCT FROM p_membership OR NOT core.finance_policy_allowed(p_household,policy.id,p_membership,p_generation,true)
  OR NOT core.finance_set_visible(p_household,head.set_id,(actor->>2)::uuid,p_membership,p_generation,true) THEN RETURN NULL;END IF;
 SELECT coalesce(jsonb_agg(jsonb_build_array(s.id,s.version,p.id,p.version,o.resource_version) ORDER BY s.id),'[]') INTO proof
 FROM core.financial_requirement_members m JOIN core.financial_subjects s ON s.id=m.subject_id AND s.household_id=m.household_id
 JOIN core.financial_policies p ON p.id=m.policy_id AND p.household_id=m.household_id
 JOIN core.finance_origin_resources o ON o.household_id=m.household_id AND o.subject_id=s.id WHERE m.set_id=head.set_id;
 RETURN encode(sha256(convert_to(jsonb_build_array(p_household,p_generation,actor,p_template,head.resource_version,policy.id,policy.version,proof)::text,'UTF8')),'hex');
END $$;
REVOKE ALL ON FUNCTION core.finance_automation_stamp(uuid,uuid,uuid,bigint) FROM PUBLIC,household_app;

CREATE FUNCTION core.finance_access_projection(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,
 p_kind text,p_id uuid,p_recovery boolean) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE actor jsonb;policy core.financial_policies%ROWTYPE;visible boolean;editable boolean;manager boolean;recoverable boolean;stamp text;grants jsonb='[]';
BEGIN
 actor=core.finance_actor(p_household,p_user,p_membership,p_session,p_auth,p_generation);
 SELECT p.* INTO policy FROM core.financial_policies p LEFT JOIN core.financial_subjects s ON s.household_id=p.household_id AND s.policy_id=p.id
 WHERE p.household_id=p_household AND p.module_generation=p_generation AND p.retired_at IS NULL
 AND((s.resource_type=p_kind AND s.resource_id=p_id AND s.retired_at IS NULL) OR(p.resource_type=p_kind AND p.resource_id=p_id)) ORDER BY p.id LIMIT 1;
 IF NOT FOUND THEN RAISE EXCEPTION 'policy unavailable' USING ERRCODE='P0002';END IF;
 visible=core.finance_resource_visible(p_household,p_user,p_membership,p_session,p_auth,p_generation,p_kind,p_id,'view');
 recoverable=p_session IS NOT NULL AND actor->>'role'='admin' AND(actor->>'session_created_at')::timestamptz>transaction_timestamp()-interval '10 minutes';
 IF p_recovery THEN
  IF NOT recoverable THEN RAISE EXCEPTION 'recent administrator required' USING ERRCODE='42501';END IF;
  stamp=encode(sha256(convert_to(jsonb_build_array('recovery',p_household,p_user,p_membership,p_session,p_auth,p_generation,policy.id,policy.version)::text,'UTF8')),'hex');
  RETURN jsonb_build_object('target_kind',policy.resource_type,'target_id',policy.resource_id,'policy_version',policy.version,
   'access_version',stamp,'can_recover',true,'metadata_only',true);
 END IF;
 IF NOT visible THEN RAISE EXCEPTION 'resource unavailable' USING ERRCODE='P0002';END IF;
 editable=core.finance_resource_visible(p_household,p_user,p_membership,p_session,p_auth,p_generation,p_kind,p_id,'edit');
 manager=editable AND policy.responsible_membership_id=p_membership;
 stamp=core.finance_authority(p_household,p_user,p_membership,p_session,p_auth,p_generation,jsonb_build_array(jsonb_build_object('kind',p_kind,'id',p_id)),'view');
 IF manager THEN SELECT coalesce(jsonb_agg(jsonb_build_object('membership_id',membership_id,'permission',permission) ORDER BY membership_id),'[]')
  INTO grants FROM core.financial_policy_grants WHERE household_id=p_household AND policy_id=policy.id;END IF;
 RETURN jsonb_build_object('target_kind',policy.resource_type,'target_id',policy.resource_id,'access_mode',policy.access_mode,'policy_version',policy.version,
  'access_version',stamp,'responsible_membership_id',policy.responsible_membership_id,'grants',grants,'can_view',true,'can_edit',editable,
  'can_manage',manager,'can_assign',editable AND policy.access_mode='household' AND policy.responsible_membership_id IS NULL,'can_recover',recoverable);
END $$;
REVOKE ALL ON FUNCTION core.finance_access_projection(uuid,uuid,uuid,uuid,bigint,bigint,text,uuid,boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_access_projection(uuid,uuid,uuid,uuid,bigint,bigint,text,uuid,boolean) TO household_app;

CREATE FUNCTION core.finance_access_command(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,
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
 UPDATE core.finance_background_authorities SET enabled=false,updated_at=clock_timestamp() WHERE household_id=p_household AND expenses_generation=p_generation
  AND(template_id=p_id OR id IN(SELECT t.authority_id FROM core.finance_transaction_authorities t WHERE t.household_id=p_household));
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
REVOKE ALL ON FUNCTION core.finance_access_command(uuid,uuid,uuid,uuid,bigint,bigint,text,text,uuid,bigint,bigint,text,text,uuid,jsonb,bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_access_command(uuid,uuid,uuid,uuid,bigint,bigint,text,text,uuid,bigint,bigint,text,text,uuid,jsonb,bytea) TO household_app;

CREATE FUNCTION core.finance_origin_projection(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,p_kind text,p_id uuid)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE head core.finance_origin_resources%ROWTYPE;operation core.finance_origin_operations%ROWTYPE;ref jsonb;available boolean=true;independent boolean=false;retained boolean;
BEGIN
 IF NOT core.finance_resource_visible(p_household,p_user,p_membership,p_session,p_auth,p_generation,p_kind,p_id,'view') THEN RAISE EXCEPTION 'resource unavailable' USING ERRCODE='P0002';END IF;
 SELECT * INTO STRICT head FROM core.finance_origin_resources WHERE household_id=p_household AND module_generation=p_generation AND resource_type=p_kind AND resource_id=p_id AND row_number=0;
 SELECT * INTO STRICT operation FROM core.finance_origin_operations WHERE id=coalesce(head.provenance_root_id,(SELECT provenance_root_id FROM core.finance_origin_operations WHERE id=head.root_id),head.root_id) AND household_id=p_household;
 FOR ref IN SELECT value FROM jsonb_array_elements(operation.source_refs) LOOP
  IF ref->>'kind' IN('shared_fuel_fillups','shared_fuel_vehicle_costs','shared_shopping_checkout') THEN
   independent=true;available=available AND core.finance_shared_source(p_household,ref->>'kind',(ref->>'id')::uuid) IS NOT NULL;
  ELSIF ref->>'kind' IN('attachment','attachment_derivative','capture','import_batch','import_row') THEN
   available=available AND core.finance_artifact_visible(p_household,p_user,p_membership,p_session,p_auth,
    CASE WHEN ref->>'kind'='import_row' THEN 'import_batch' ELSE ref->>'kind' END,(ref->>'id')::uuid,'view');
  END IF;
 END LOOP;
 SELECT required_count>0 INTO retained FROM core.financial_requirement_sets WHERE id=head.set_id;
 RETURN jsonb_build_object('status',CASE WHEN available THEN 'known' ELSE 'source_unavailable' END,'origin',operation.origin,
  'source_available',available,'independently_shared',independent,'financial_requirements_retained',retained);
END $$;
REVOKE ALL ON FUNCTION core.finance_origin_projection(uuid,uuid,uuid,uuid,bigint,bigint,text,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_origin_projection(uuid,uuid,uuid,uuid,bigint,bigint,text,uuid) TO household_app;

CREATE FUNCTION core.finance_operation_guard() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF core.finance_real_maintenance() THEN IF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;END IF;
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'financial operation proof is immutable' USING ERRCODE='42501';END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('finance-actor-key/'||NEW.household_id||'/'||NEW.created_by||'/'||NEW.request_key,0));
 IF EXISTS(SELECT 1 FROM core.finance_key_tombstones WHERE household_id=NEW.household_id AND created_by=NEW.created_by AND request_key=NEW.request_key)
  AND NOT EXISTS(SELECT 1 FROM core.finance_operations WHERE household_id=NEW.household_id AND created_by=NEW.created_by AND request_key=NEW.request_key)
  AND NOT EXISTS(SELECT 1 FROM core.expense_edit_operations WHERE household_id=NEW.household_id AND created_by=NEW.created_by AND request_key=NEW.request_key) THEN
  RAISE EXCEPTION 'operation key is sealed' USING ERRCODE='40001';END IF;
 IF TG_TABLE_NAME='finance_operations' THEN
  IF EXISTS(SELECT 1 FROM core.expense_edit_operations WHERE household_id=NEW.household_id AND created_by=NEW.created_by AND request_key=NEW.request_key) THEN
   RAISE EXCEPTION 'idempotency purpose collision' USING ERRCODE='40001';END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM core.finance_operations WHERE household_id=NEW.household_id AND created_by=NEW.created_by AND request_key=NEW.request_key) THEN
   RAISE EXCEPTION 'idempotency purpose collision' USING ERRCODE='40001';END IF;
 END IF;
 IF nullif(current_setting('household.audit_kind',true),'') IS DISTINCT FROM 'user' OR nullif(current_setting('household.audit_user',true),'') IS DISTINCT FROM NEW.created_by::text
  OR nullif(current_setting('household.audit_household',true),'') IS DISTINCT FROM NEW.household_id::text THEN RAISE EXCEPTION 'operation actor mismatch' USING ERRCODE='42501';END IF;
 NEW.created_at=clock_timestamp();RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.finance_operation_guard() FROM PUBLIC,household_app;
CREATE TRIGGER finance_operation_identity BEFORE INSERT OR UPDATE OR DELETE ON core.finance_operations FOR EACH ROW EXECUTE FUNCTION core.finance_operation_guard();
CREATE TRIGGER finance_actor_key BEFORE INSERT ON core.expense_edit_operations FOR EACH ROW EXECUTE FUNCTION core.finance_operation_guard();

CREATE FUNCTION core.finance_seal_key(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,p_auth bigint,p_generation bigint,p_key uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
BEGIN
 IF p_key IS NULL OR p_generation IS NULL OR p_generation<=0 OR NOT EXISTS(SELECT 1 FROM core.households h JOIN core.memberships m
  ON m.household_id=h.id AND m.id=p_membership AND m.user_id=p_user AND m.inactive_at IS NULL JOIN core.users u ON u.id=m.user_id
  AND u.disabled_at IS NULL AND NOT u.must_change_password AND u.auth_version=p_auth JOIN core.sessions s ON s.user_id=u.id AND s.id=p_session
  AND s.auth_version=p_auth AND s.revoked_at IS NULL AND s.idle_expires_at>transaction_timestamp() AND s.absolute_expires_at>transaction_timestamp()
  JOIN core.module_states ms ON ms.household_id=h.id AND ms.module_id='expenses' AND ms.data_generation>=p_generation WHERE h.id=p_household) THEN
  RAISE EXCEPTION 'current key owner required' USING ERRCODE='42501';END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('finance-actor-key/'||p_household||'/'||p_user||'/'||p_key,0));
 INSERT INTO core.finance_key_tombstones(household_id,created_by,request_key,expenses_generation) VALUES(p_household,p_user,p_key,p_generation)
 ON CONFLICT(household_id,created_by,request_key) DO NOTHING;
END $$;
REVOKE ALL ON FUNCTION core.finance_seal_key(uuid,uuid,uuid,uuid,bigint,bigint,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_seal_key(uuid,uuid,uuid,uuid,bigint,bigint,uuid) TO household_app;

CREATE FUNCTION core.finance_operation_complete() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
BEGIN
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 IF NOT EXISTS(SELECT 1 FROM core.finance_origin_operations r JOIN core.finance_origin_effects e ON e.root_id=r.id AND e.household_id=r.household_id
  WHERE r.household_id=NEW.household_id AND r.actor_user_id=NEW.created_by AND r.membership_id=NEW.membership_id AND r.expenses_generation=NEW.expenses_generation
  AND r.transaction_id=pg_current_xact_id_if_assigned() AND r.transaction_started_at=transaction_timestamp() AND r.state='complete'
  AND r.target_kind=NEW.resource_type AND r.target_id=NEW.resource_id
  AND e.resource_type=NEW.resource_type AND e.resource_id=NEW.resource_id
  AND(e.resource_version=NEW.result_version OR NEW.operation='access.recover')) THEN
  RAISE EXCEPTION 'operation has no exact completed financial effect' USING ERRCODE='23514';END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.finance_operation_complete() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER finance_operation_complete AFTER INSERT ON core.finance_operations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.finance_operation_complete();

CREATE FUNCTION core.finance_responsibility_guard() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
BEGIN
 IF core.finance_real_maintenance() THEN IF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;END IF;
 IF TG_TABLE_NAME='memberships' THEN
  IF(TG_OP='DELETE' OR NEW.inactive_at IS NOT NULL OR NEW.role NOT IN('admin','editor')) AND EXISTS(SELECT 1 FROM core.financial_policies p
   WHERE p.household_id=OLD.household_id AND p.responsible_membership_id=OLD.id AND p.retired_at IS NULL) THEN
   RAISE EXCEPTION 'reassign financial responsibility first' USING ERRCODE='23514';END IF;
 ELSE
  IF(TG_OP='DELETE' OR NEW.disabled_at IS NOT NULL) AND EXISTS(SELECT 1 FROM core.financial_policies p JOIN core.memberships m ON m.household_id=p.household_id AND m.id=p.responsible_membership_id
   WHERE m.user_id=OLD.id AND p.retired_at IS NULL) THEN RAISE EXCEPTION 'reassign financial responsibility first' USING ERRCODE='23514';END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;
END $$;
REVOKE ALL ON FUNCTION core.finance_responsibility_guard() FROM PUBLIC,household_app;
CREATE TRIGGER finance_responsibility BEFORE UPDATE OR DELETE ON core.memberships FOR EACH ROW EXECUTE FUNCTION core.finance_responsibility_guard();
CREATE TRIGGER finance_responsibility BEFORE UPDATE OR DELETE ON core.users FOR EACH ROW EXECUTE FUNCTION core.finance_responsibility_guard();

DO $metadata$
DECLARE relation text;
BEGIN
 FOREACH relation IN ARRAY ARRAY['financial_policy_grants','finance_private_owners','finance_key_tombstones','finance_origin_operations','finance_origin_resources','finance_origin_effects','finance_audit_bindings',
 'finance_ingest_claims','finance_background_authorities','finance_transaction_authorities','finance_artifact_job_authorities','finance_security_events'] LOOP
  EXECUTE format('REVOKE ALL ON core.%I FROM PUBLIC,household_app',relation);
  EXECUTE format('GRANT SELECT ON core.%I TO household_app',relation);
  EXECUTE format('CREATE TRIGGER finance_private_metadata BEFORE INSERT OR UPDATE OR DELETE ON core.%I FOR EACH ROW EXECUTE FUNCTION core.finance_metadata_guard()',relation);
  EXECUTE format('CREATE TRIGGER finance_private_truncate BEFORE TRUNCATE ON core.%I FOR EACH STATEMENT EXECUTE FUNCTION core.finance_metadata_guard()',relation);
 END LOOP;
END $metadata$;
REVOKE ALL ON core.finance_operations FROM PUBLIC,household_app;
GRANT SELECT,INSERT ON core.finance_operations TO household_app;

CREATE OR REPLACE FUNCTION core.financial_foundation_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT NOT pg_has_role('household_app','household_owner','MEMBER') AND NOT EXISTS(SELECT 1 FROM(VALUES('financial_policies'),('financial_subjects')) x(name)
 WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass('core.'||x.name) AND t.tgname=CASE x.name WHEN 'financial_policies' THEN 'financial_policy_identity' ELSE 'financial_subject_identity' END
  AND t.tgfoid='core.guard_financial_identity()'::regprocedure AND t.tgenabled IN('O','A') AND t.tgtype=31)
 OR NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass('core.'||x.name) AND t.tgname=CASE x.name WHEN 'financial_policies' THEN 'financial_policy_complete' ELSE 'financial_subject_complete' END
  AND t.tgfoid='core.check_financial_identity_complete()'::regprocedure AND t.tgenabled IN('O','A') AND t.tgdeferrable AND t.tginitdeferred)
 OR has_table_privilege('household_app','core.'||x.name,'DELETE,TRUNCATE,TRIGGER'))
 AND NOT has_function_privilege('household_app','core.guard_financial_identity()','EXECUTE') AND NOT has_function_privilege('household_app','core.check_financial_identity_complete()','EXECUTE')
$$;

CREATE FUNCTION core.finance_authority_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT core.financial_requirements_ready() AND NOT EXISTS(SELECT 1 FROM(VALUES('financial_policy_grants'),('finance_private_owners'),('finance_key_tombstones'),('finance_origin_operations'),('finance_origin_resources'),
 ('finance_origin_effects'),('finance_audit_bindings'),('finance_ingest_claims'),('finance_background_authorities'),('finance_transaction_authorities'),('finance_artifact_job_authorities'),('finance_security_events')) r(name)
 WHERE NOT has_table_privilege('household_app','core.'||r.name,'SELECT') OR has_table_privilege('household_app','core.'||r.name,'INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER,REFERENCES')
 OR NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass('core.'||r.name) AND t.tgname='finance_private_metadata' AND t.tgenabled IN('O','A') AND t.tgfoid='core.finance_metadata_guard()'::regprocedure))
 AND has_table_privilege('household_app','core.finance_operations','SELECT') AND has_table_privilege('household_app','core.finance_operations','INSERT')
 AND NOT has_table_privilege('household_app','core.finance_operations','UPDATE,DELETE,TRUNCATE,TRIGGER')
$$;
REVOKE ALL ON FUNCTION core.finance_authority_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_authority_ready() TO household_app;
COMMIT;
