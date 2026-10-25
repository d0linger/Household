BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE TABLE core.meal_reservations(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 mealplan_generation bigint NOT NULL CHECK(mealplan_generation>0), plan_id uuid NOT NULL, meal_id uuid NOT NULL,
 pantry_generation bigint NOT NULL CHECK(pantry_generation>0), source_basis bytea NOT NULL CHECK(octet_length(source_basis)=32),
 created_by uuid NOT NULL REFERENCES core.users(id), request_key uuid NOT NULL, request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 allocation_count integer NOT NULL CHECK(allocation_count BETWEEN 1 AND 100),
 expires_at timestamptz NOT NULL CHECK(isfinite(expires_at)), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 state text NOT NULL DEFAULT 'active' CHECK(state IN('active','released','expired')),
 release_reason text NOT NULL DEFAULT '' CHECK(release_reason IN('','manual','replaced','source_changed','module_disabled','module_purged','expired')),
 closed_at timestamptz, revision bigint NOT NULL DEFAULT 1 CHECK(revision>0), version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 CHECK((state='active' AND release_reason='' AND closed_at IS NULL) OR
  (state='released' AND release_reason IN('manual','replaced','source_changed','module_disabled','module_purged') AND closed_at IS NOT NULL) OR
  (state='expired' AND release_reason='expired' AND closed_at IS NOT NULL)),
 UNIQUE(household_id,id), CONSTRAINT meal_reservation_request UNIQUE(household_id,created_by,request_key), CONSTRAINT meal_reservation_revision UNIQUE(household_id,mealplan_generation,meal_id,revision)
);
CREATE UNIQUE INDEX meal_reservation_active ON core.meal_reservations(household_id,mealplan_generation,meal_id) WHERE state='active';
CREATE INDEX meal_reservation_expiry ON core.meal_reservations(expires_at,id) WHERE state='active';
CREATE INDEX meal_reservation_source ON core.meal_reservations(household_id,mealplan_generation,meal_id,revision DESC);
CREATE INDEX meal_reservation_plan ON core.meal_reservations(household_id,plan_id) WHERE state='active';
CREATE TABLE core.meal_reservation_allocations(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id), reservation_id uuid NOT NULL,
 product_id uuid NOT NULL, lot_id uuid NOT NULL, unit_code text NOT NULL REFERENCES core.units(code),
 quantity numeric(18,6) NOT NULL CHECK(quantity>0 AND quantity<>'NaN'::numeric),
 UNIQUE(household_id,id), UNIQUE(household_id,reservation_id,lot_id),
 FOREIGN KEY(household_id,reservation_id) REFERENCES core.meal_reservations(household_id,id)
);
CREATE INDEX meal_reservation_lot ON core.meal_reservation_allocations(household_id,lot_id,reservation_id);
REVOKE ALL ON core.meal_reservations,core.meal_reservation_allocations FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE ON core.meal_reservations TO household_app;
GRANT SELECT,INSERT ON core.meal_reservation_allocations TO household_app;

CREATE FUNCTION core.guard_meal_reservation() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  NEW.created_at=clock_timestamp();NEW.version=1;
  IF NEW.state<>'active' OR NEW.release_reason<>'' OR NEW.closed_at IS NOT NULL
   OR NEW.expires_at<=clock_timestamp() OR NEW.expires_at>clock_timestamp()+interval '31 days' THEN
   RAISE EXCEPTION 'reservation requires a future lease of at most 31 days' USING ERRCODE='23514';
  END IF;
 ELSE
  IF (to_jsonb(NEW)-ARRAY['state','release_reason','closed_at','version']) IS DISTINCT FROM
   (to_jsonb(OLD)-ARRAY['state','release_reason','closed_at','version']) OR OLD.state<>'active' OR NEW.state='active' THEN
   RAISE EXCEPTION 'reservation identity is immutable and terminal leases cannot reopen' USING ERRCODE='23514';
  END IF;
  IF NEW.state='expired' AND OLD.expires_at>clock_timestamp() THEN
   RAISE EXCEPTION 'reservation has not expired' USING ERRCODE='23514';
  END IF;
  NEW.version=OLD.version+1;NEW.closed_at=clock_timestamp();
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.guard_meal_reservation() FROM PUBLIC,household_app;
CREATE TRIGGER meal_reservation_identity BEFORE INSERT OR UPDATE ON core.meal_reservations FOR EACH ROW EXECUTE FUNCTION core.guard_meal_reservation();
CREATE TRIGGER meal_reservation_delete BEFORE DELETE ON core.meal_reservations FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();
CREATE TRIGGER meal_reservation_allocation_immutable BEFORE UPDATE OR DELETE ON core.meal_reservation_allocations FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();

CREATE FUNCTION core.check_meal_reservation_complete() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE root_id uuid; expected integer;
BEGIN
 IF TG_TABLE_NAME='meal_reservations' THEN root_id=NEW.id;ELSE root_id=NEW.reservation_id;END IF;
 SELECT allocation_count INTO expected FROM core.meal_reservations WHERE household_id=NEW.household_id AND id=root_id;
 IF expected IS NULL OR expected<>(SELECT count(*) FROM core.meal_reservation_allocations WHERE household_id=NEW.household_id AND reservation_id=root_id) THEN
  RAISE EXCEPTION 'incomplete reservation bundle' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.check_meal_reservation_complete() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER meal_reservation_complete AFTER INSERT ON core.meal_reservations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.check_meal_reservation_complete();
CREATE CONSTRAINT TRIGGER meal_reservation_allocation_complete AFTER INSERT ON core.meal_reservation_allocations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core.check_meal_reservation_complete();

CREATE FUNCTION core.release_module_reservations() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF NEW.module_id IN('mealplan','pantry') AND
  ((NEW.state<>'enabled' AND OLD.state='enabled') OR NEW.data_generation<>OLD.data_generation) THEN
  -- Direct SQL must not invert the household -> state order by waiting here.
  PERFORM id FROM core.households WHERE id=NEW.household_id FOR UPDATE NOWAIT;
  UPDATE core.meal_reservations SET state='released',release_reason='module_disabled'
   WHERE household_id=NEW.household_id AND state='active';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.release_module_reservations() FROM PUBLIC,household_app;
CREATE TRIGGER meal_reservation_module_release AFTER UPDATE ON core.module_states FOR EACH ROW EXECUTE FUNCTION core.release_module_reservations();
INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('core','meal_reservations',ARRAY['id'],ARRAY['mealplan_generation','plan_id','meal_id','pantry_generation','allocation_count','state','release_reason','revision','version'],NULL),
 ('core','meal_reservation_allocations',ARRAY['id'],ARRAY['reservation_id','product_id','lot_id'],NULL);
CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT count(*)=69 AND bool_and(
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
CREATE FUNCTION core.meal_reservations_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT NOT EXISTS(SELECT 1 FROM (VALUES
 ('core.meal_reservations','meal_reservation_identity','core.guard_meal_reservation()',23),
 ('core.meal_reservations','meal_reservation_complete','core.check_meal_reservation_complete()',5),
 ('core.meal_reservations','meal_reservation_source','mealplan.guard_reservation_source()',7),
 ('core.meal_reservation_allocations','meal_reservation_allocation_complete','core.check_meal_reservation_complete()',5),
 ('core.meal_reservation_allocations','meal_reservation_allocation_guard','pantry.guard_reservation_allocation()',7),
 ('core.module_states','meal_reservation_module_release','core.release_module_reservations()',17),
 ('mealplan.plans','planning_reservation_plan','mealplan.release_changed_reservations()',25),
 ('mealplan.meals','planning_reservation_meal','mealplan.release_changed_reservations()',25),
 ('mealplan.components','planning_reservation_component','mealplan.release_changed_reservations()',29)
 ) required(relation,name,function_id,trigger_type) WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
 WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=required.name AND t.tgfoid=to_regprocedure(required.function_id)
 AND t.tgtype=required.trigger_type AND t.tgenabled IN('O','A') AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM (VALUES('core.meal_reservations','meal_reservation_complete'),('core.meal_reservation_allocations','meal_reservation_allocation_complete')) required(relation,name)
 WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=required.name AND t.tgdeferrable AND t.tginitdeferred))
 AND NOT EXISTS(SELECT 1 FROM (VALUES('core.meal_reservations','meal_reservation_delete',11),('core.meal_reservation_allocations','meal_reservation_allocation_immutable',27)) required(relation,name,trigger_type)
 WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=required.name AND t.tgtype=required.trigger_type
 AND t.tgfoid='core.reject_mutation()'::regprocedure AND t.tgenabled IN('O','A')))
 AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='core.meal_reservations'::regclass AND attname='revision' AND atttypid='bigint'::regtype AND attnotnull AND NOT attisdropped)
 AND EXISTS(SELECT 1 FROM pg_index WHERE indexrelid=to_regclass('core.meal_reservation_active') AND indrelid='core.meal_reservations'::regclass AND indisunique AND indisvalid AND indnkeyatts=3 AND pg_get_expr(indpred,indrelid)='(state = ''active''::text)')
 AND NOT EXISTS(SELECT 1 FROM (VALUES('meal_reservation_request'),('meal_reservation_revision')) required(name)
 WHERE NOT EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_index i ON i.indexrelid=c.conindid WHERE c.conrelid='core.meal_reservations'::regclass AND c.conname=required.name AND c.contype='u' AND c.convalidated AND i.indisvalid AND i.indisunique))
 AND has_table_privilege('household_app','core.meal_reservations','SELECT')
 AND has_table_privilege('household_app','core.meal_reservations','INSERT')
 AND has_table_privilege('household_app','core.meal_reservations','UPDATE')
 AND NOT has_table_privilege('household_app','core.meal_reservations','DELETE,TRUNCATE,TRIGGER')
 AND has_table_privilege('household_app','core.meal_reservation_allocations','SELECT')
 AND has_table_privilege('household_app','core.meal_reservation_allocations','INSERT')
 AND NOT has_table_privilege('household_app','core.meal_reservation_allocations','UPDATE,DELETE,TRUNCATE,TRIGGER')
$$;
REVOKE ALL ON FUNCTION core.meal_reservations_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.meal_reservations_ready() TO household_app;
COMMIT;
