BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE TABLE pantry.cooking_stock_receipts(
 id uuid PRIMARY KEY, household_id uuid NOT NULL REFERENCES core.households(id),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND snapshot ?& ARRAY['source','decisions']
  AND jsonb_typeof(snapshot->'source')='object' AND jsonb_typeof(snapshot->'decisions')='array'
  AND jsonb_array_length(snapshot->'decisions')<=500 AND octet_length(snapshot::text)<=2097152),
 UNIQUE(household_id,id), FOREIGN KEY(household_id,id) REFERENCES core.cooking_stock_postings(household_id,id)
);
CREATE TABLE pantry.cooking_stock_movements(
 id uuid PRIMARY KEY, household_id uuid NOT NULL REFERENCES core.households(id), posting_id uuid NOT NULL,
 lot_id uuid NOT NULL, operation_id uuid NOT NULL, quantity numeric(18,6) NOT NULL CHECK(quantity>0), unit_code text NOT NULL REFERENCES core.units(code),
 UNIQUE(household_id,id), UNIQUE(household_id,posting_id,lot_id), UNIQUE(household_id,operation_id),
 FOREIGN KEY(household_id,posting_id) REFERENCES pantry.cooking_stock_receipts(household_id,id),
 FOREIGN KEY(household_id,id) REFERENCES pantry.stock_movements(household_id,id),
 FOREIGN KEY(household_id,lot_id) REFERENCES pantry.lots(household_id,id),
 FOREIGN KEY(household_id,operation_id) REFERENCES pantry.stock_operations(household_id,id)
);
REVOKE ALL ON pantry.cooking_stock_receipts,pantry.cooking_stock_movements FROM PUBLIC,household_app;
GRANT SELECT,INSERT ON pantry.cooking_stock_receipts,pantry.cooking_stock_movements TO household_app;
CREATE TRIGGER cooking_receipt_immutable BEFORE UPDATE OR DELETE ON pantry.cooking_stock_receipts FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();
CREATE TRIGGER cooking_movement_immutable BEFORE UPDATE OR DELETE ON pantry.cooking_stock_movements FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();

CREATE FUNCTION pantry.guard_cooking_receipt() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE posting core.cooking_stock_postings%ROWTYPE;
BEGIN
 SELECT * INTO posting FROM core.cooking_stock_postings WHERE household_id=NEW.household_id AND id=NEW.id;
 IF NOT FOUND OR jsonb_array_length(NEW.snapshot->'decisions')<>posting.decision_count
  OR NEW.snapshot->'source'->>'source_version_id' IS DISTINCT FROM posting.source_version_id::text
  OR NEW.snapshot->'source'->>'cooking_record_id' IS DISTINCT FROM posting.cooking_record_id::text THEN
  RAISE EXCEPTION 'cooking receipt source mismatch' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_cooking_receipt() FROM PUBLIC,household_app;
CREATE TRIGGER cooking_receipt_guard BEFORE INSERT ON pantry.cooking_stock_receipts FOR EACH ROW EXECUTE FUNCTION pantry.guard_cooking_receipt();

CREATE FUNCTION pantry.guard_cooking_movement() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM core.cooking_stock_postings posting
  JOIN pantry.stock_movements movement ON movement.household_id=posting.household_id AND movement.correlation_id=posting.id
  JOIN pantry.lots lot ON lot.household_id=movement.household_id AND lot.id=movement.lot_id
  JOIN pantry.products product ON product.household_id=lot.household_id AND product.id=lot.product_id
  JOIN pantry.stock_operations operation ON operation.household_id=movement.household_id AND operation.movement_id=movement.id
  WHERE posting.household_id=NEW.household_id AND posting.id=NEW.posting_id AND posting.mode='stock'
  AND movement.id=NEW.id AND movement.lot_id=NEW.lot_id AND movement.reason='consume' AND movement.quantity_delta=-NEW.quantity
  AND product.unit_code=NEW.unit_code AND movement.created_by=posting.created_by
  AND operation.id=NEW.operation_id AND operation.kind='consume' AND operation.lot_id=NEW.lot_id AND operation.created_by=posting.created_by) THEN
  RAISE EXCEPTION 'cooking movement identity or amount mismatch' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_cooking_movement() FROM PUBLIC,household_app;
CREATE TRIGGER cooking_movement_guard BEFORE INSERT ON pantry.cooking_stock_movements FOR EACH ROW EXECUTE FUNCTION pantry.guard_cooking_movement();

CREATE FUNCTION pantry.check_cooking_completion() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE v_posting_id uuid; expected integer; actual integer;
BEGIN
 IF TG_TABLE_NAME IN('cooking_stock_postings','cooking_stock_receipts') THEN v_posting_id=NEW.id; ELSE v_posting_id=NEW.posting_id; END IF;
 SELECT movement_count INTO expected FROM core.cooking_stock_postings WHERE household_id=NEW.household_id AND id=v_posting_id;
 SELECT count(*) INTO actual FROM pantry.cooking_stock_movements m WHERE m.household_id=NEW.household_id AND m.posting_id=v_posting_id;
 IF expected IS NULL OR actual<>expected OR NOT EXISTS(
  SELECT 1 FROM pantry.cooking_stock_receipts WHERE household_id=NEW.household_id AND id=v_posting_id) THEN
  RAISE EXCEPTION 'incomplete cooking movement bundle' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION pantry.check_cooking_completion() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER cooking_posting_complete AFTER INSERT ON core.cooking_stock_postings DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pantry.check_cooking_completion();
CREATE CONSTRAINT TRIGGER cooking_receipt_complete AFTER INSERT ON pantry.cooking_stock_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pantry.check_cooking_completion();
CREATE CONSTRAINT TRIGGER cooking_movement_complete AFTER INSERT ON pantry.cooking_stock_movements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pantry.check_cooking_completion();

CREATE FUNCTION pantry.cooking_stock_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT NOT EXISTS(SELECT 1 FROM (VALUES
  ('core.cooking_stock_postings'::regclass,'cooking_stock_posting_insert','core.guard_cooking_stock_posting()'::regprocedure,7),
  ('core.cooking_stock_postings'::regclass,'cooking_posting_complete','pantry.check_cooking_completion()'::regprocedure,5),
  ('pantry.cooking_stock_receipts'::regclass,'cooking_receipt_guard','pantry.guard_cooking_receipt()'::regprocedure,7),
  ('pantry.cooking_stock_movements'::regclass,'cooking_movement_guard','pantry.guard_cooking_movement()'::regprocedure,7),
  ('pantry.cooking_stock_receipts'::regclass,'cooking_receipt_complete','pantry.check_cooking_completion()'::regprocedure,5),
  ('pantry.cooking_stock_movements'::regclass,'cooking_movement_complete','pantry.check_cooking_completion()'::regprocedure,5)
 ) required(relation,name,function_id,trigger_type)
 WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgrelid=required.relation AND t.tgname=required.name
  AND t.tgfoid=required.function_id AND t.tgtype=required.trigger_type AND t.tgenabled IN('O','A') AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM (VALUES
  ('core.cooking_stock_postings'::regclass,'cooking_stock_posting_immutable'),
  ('pantry.cooking_stock_receipts'::regclass,'cooking_receipt_immutable'),
  ('pantry.cooking_stock_movements'::regclass,'cooking_movement_immutable')
 ) required(relation,name) WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=required.relation AND t.tgname=required.name
  AND t.tgfoid='core.reject_mutation()'::regprocedure AND t.tgtype=27 AND t.tgenabled IN('O','A')))
 AND NOT EXISTS(SELECT 1 FROM (VALUES('core.cooking_stock_postings'),('pantry.cooking_stock_receipts'),('pantry.cooking_stock_movements')) required(relation)
  WHERE NOT has_table_privilege('household_app',required.relation,'SELECT') OR NOT has_table_privilege('household_app',required.relation,'INSERT')
  OR has_table_privilege('household_app',required.relation,'UPDATE,DELETE,TRUNCATE,TRIGGER'))
 AND NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgname IN('cooking_posting_complete','cooking_receipt_complete','cooking_movement_complete')
  AND t.tgrelid IN('core.cooking_stock_postings'::regclass,'pantry.cooking_stock_receipts'::regclass,'pantry.cooking_stock_movements'::regclass) AND (NOT t.tgdeferrable OR NOT t.tginitdeferred))
$$;
REVOKE ALL ON FUNCTION pantry.cooking_stock_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.cooking_stock_ready() TO household_app;
COMMIT;
