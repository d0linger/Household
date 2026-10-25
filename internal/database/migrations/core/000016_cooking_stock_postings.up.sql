BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE TABLE core.cooking_stock_postings(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 recipes_generation bigint NOT NULL CHECK(recipes_generation>0), cooking_record_id uuid NOT NULL, source_version_id uuid NOT NULL,
 pantry_generation bigint NOT NULL CHECK(pantry_generation>0), created_by uuid NOT NULL REFERENCES core.users(id),
 request_key uuid NOT NULL, request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 decision_count integer NOT NULL CHECK(decision_count BETWEEN 0 AND 500), movement_count integer NOT NULL CHECK(movement_count BETWEEN 0 AND 500),
 mode text NOT NULL CHECK(mode IN('stock','no_stock')), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 version bigint NOT NULL DEFAULT 1 CHECK(version=1),
 CHECK((mode='stock' AND movement_count>0) OR (mode='no_stock' AND movement_count=0)),
 UNIQUE(household_id,id), UNIQUE(household_id,recipes_generation,cooking_record_id), UNIQUE(household_id,created_by,request_key)
);
CREATE INDEX cooking_stock_source ON core.cooking_stock_postings(household_id,cooking_record_id,recipes_generation);
REVOKE ALL ON core.cooking_stock_postings FROM PUBLIC,household_app;
GRANT SELECT,INSERT ON core.cooking_stock_postings TO household_app;
CREATE FUNCTION core.guard_cooking_stock_posting() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 NEW.created_at=clock_timestamp();
 NEW.version=1;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.guard_cooking_stock_posting() FROM PUBLIC,household_app;
CREATE TRIGGER cooking_stock_posting_insert BEFORE INSERT ON core.cooking_stock_postings FOR EACH ROW EXECUTE FUNCTION core.guard_cooking_stock_posting();
CREATE TRIGGER cooking_stock_posting_immutable BEFORE UPDATE OR DELETE ON core.cooking_stock_postings FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();
INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('core','cooking_stock_postings',ARRAY['id'],ARRAY['recipes_generation','cooking_record_id','source_version_id','pantry_generation','mode','decision_count','movement_count'],NULL),
 ('pantry','cooking_stock_receipts',ARRAY['id'],ARRAY['id'],NULL),
 ('pantry','cooking_stock_movements',ARRAY['id'],ARRAY['posting_id','lot_id','operation_id'],NULL);
CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT count(*)=64 AND bool_and(
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
