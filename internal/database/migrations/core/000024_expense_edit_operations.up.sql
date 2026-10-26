BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
-- Minimal deduplication survives module purge and 30-day response pruning.
-- There is intentionally no FK into an independently purgeable module schema.
CREATE TABLE core.expense_edit_operations(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 created_by uuid NOT NULL REFERENCES core.users(id), request_key uuid NOT NULL,
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 expenses_generation bigint NOT NULL CHECK(expenses_generation>0),
 operation text NOT NULL CHECK(operation IN('save','post')), entry_id uuid NOT NULL,
 result_version bigint NOT NULL CHECK(result_version>0), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 version bigint NOT NULL DEFAULT 1 CHECK(version=1), UNIQUE(household_id,id),
 CONSTRAINT expense_edit_request UNIQUE(household_id,created_by,request_key)
);
REVOKE ALL ON core.expense_edit_operations FROM PUBLIC,household_app;
GRANT SELECT,INSERT ON core.expense_edit_operations TO household_app;
CREATE FUNCTION core.guard_expense_edit_operation() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  NEW.created_at=clock_timestamp();
  NEW.version=1;
 ELSIF current_user<>'household_owner' THEN
  RAISE EXCEPTION 'expense operation is immutable' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.guard_expense_edit_operation() FROM PUBLIC,household_app;
CREATE TRIGGER expense_edit_insert BEFORE INSERT ON core.expense_edit_operations
 FOR EACH ROW EXECUTE FUNCTION core.guard_expense_edit_operation();
CREATE TRIGGER expense_edit_immutable BEFORE UPDATE OR DELETE ON core.expense_edit_operations
 FOR EACH ROW EXECUTE FUNCTION core.guard_expense_edit_operation();
CREATE FUNCTION core.check_expense_edit_complete() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE entry_version bigint; entry_state text;
BEGIN
 -- Owner restore preserves minimal historical proofs even after source purge.
 IF current_user='household_owner' THEN RETURN NULL; END IF;
 IF nullif(current_setting('household.audit_kind',true),'') IS DISTINCT FROM 'user'
  OR nullif(current_setting('household.audit_user',true),'') IS DISTINCT FROM NEW.created_by::text
  OR nullif(current_setting('household.audit_household',true),'') IS DISTINCT FROM NEW.household_id::text
  OR NOT EXISTS(SELECT 1 FROM core.module_states WHERE household_id=NEW.household_id AND module_id='expenses'
   AND state='enabled' AND data_generation=NEW.expenses_generation) THEN
  RAISE EXCEPTION 'expense operation scope mismatch' USING ERRCODE='23514';
 END IF;
 SELECT version,state INTO entry_version,entry_state FROM expenses.entries WHERE household_id=NEW.household_id AND id=NEW.entry_id;
 IF NOT FOUND OR entry_version<>NEW.result_version
  OR entry_state<>(CASE WHEN NEW.operation='post' THEN 'posted' ELSE 'draft' END)
  OR NOT EXISTS(SELECT 1 FROM core.audit_log a WHERE a.household_id=NEW.household_id AND a.module_id='expenses'
   AND a.resource_type='entries' AND a.resource_id=NEW.entry_id AND a.action IN('CREATE','UPDATE')
   AND a.actor_kind='user' AND a.actor_user_id=NEW.created_by AND a.db_role='household_app'
   AND a.xmin=pg_current_xact_id()::xid AND a.created_at>=transaction_timestamp()
   AND a.changes->'version'->>'after'=NEW.result_version::text) THEN
  RAISE EXCEPTION 'expense operation has no matching same-transaction effect' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.check_expense_edit_complete() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER expense_edit_complete AFTER INSERT ON core.expense_edit_operations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.check_expense_edit_complete();
INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('core','expense_edit_operations',ARRAY['id'],ARRAY['expenses_generation','operation','entry_id','result_version'],NULL);

CREATE FUNCTION core.expense_editing_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT NOT EXISTS(SELECT 1 FROM (VALUES
 ('expense_edit_insert','core.guard_expense_edit_operation()',7,false),
 ('expense_edit_immutable','core.guard_expense_edit_operation()',27,false),
 ('expense_edit_complete','core.check_expense_edit_complete()',5,true)
 ) required(name,function_id,trigger_type,deferred) WHERE NOT EXISTS(
 SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
 WHERE t.tgrelid=to_regclass('core.expense_edit_operations') AND t.tgname=required.name
 AND NOT t.tgisinternal AND t.tgfoid=to_regprocedure(required.function_id) AND t.tgtype=required.trigger_type
 AND t.tgqual IS NULL AND t.tgattr=''::int2vector AND t.tgnargs=0 AND t.tgenabled IN('O','A')
 AND t.tgdeferrable=required.deferred AND t.tginitdeferred=required.deferred AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM (VALUES('SELECT'),('INSERT')) required(privilege)
 WHERE NOT has_table_privilege('household_app','core.expense_edit_operations',required.privilege))
 AND NOT has_table_privilege('household_app','core.expense_edit_operations','UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT has_function_privilege('household_app','core.guard_expense_edit_operation()','EXECUTE')
 AND NOT has_function_privilege('household_app','core.check_expense_edit_complete()','EXECUTE')
 AND EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_index i ON i.indexrelid=c.conindid
 WHERE c.conrelid=to_regclass('core.expense_edit_operations') AND c.conname='expense_edit_request' AND c.contype='u'
 AND c.convalidated AND NOT c.condeferrable AND i.indisunique AND i.indisvalid AND i.indisready AND i.indislive
 AND ARRAY(SELECT a.attname::text FROM unnest(c.conkey) WITH ORDINALITY k(attnum,position)
 JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum ORDER BY k.position)
 =ARRAY['household_id','created_by','request_key'])
$$;
REVOKE ALL ON FUNCTION core.expense_editing_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.expense_editing_ready() TO household_app;

CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT count(*)=75 AND bool_and(
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
