BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
LOCK TABLE pantry.stock_movements IN SHARE MODE;

ALTER TABLE pantry.lots ADD COLUMN after_open_days integer CHECK(after_open_days BETWEEN 0 AND 3660);
ALTER TABLE pantry.lots ADD COLUMN after_open_source text NOT NULL DEFAULT '' CHECK(after_open_source IN('','user','manufacturer'));
ALTER TABLE pantry.lots ADD COLUMN after_open_note text NOT NULL DEFAULT '' CHECK(length(after_open_note)<=500);
ALTER TABLE pantry.lots ADD CONSTRAINT pantry_opening_source CHECK(
 (after_open_days IS NULL AND after_open_source='' AND after_open_note='') OR
 (after_open_days IS NOT NULL AND after_open_source IN('user','manufacturer')));
CREATE INDEX pantry_priority_date ON pantry.lots(household_id,(least(use_by_on,opened_on+after_open_days,best_before_on)),id);

CREATE TABLE pantry.packs(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 product_id uuid NOT NULL, label text NOT NULL CHECK(length(label) BETWEEN 1 AND 100),
 quantity numeric(18,6) NOT NULL CHECK(quantity>0), unit_code text NOT NULL REFERENCES core.units(code) CHECK(unit_code IN('piece','g','kg','ml','l')),
 disabled boolean NOT NULL DEFAULT false, created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), version bigint NOT NULL DEFAULT 1,
 UNIQUE(household_id,id), UNIQUE(household_id,product_id,id),
 FOREIGN KEY(household_id,product_id) REFERENCES pantry.products(household_id,id) ON DELETE CASCADE
);
CREATE INDEX pantry_pack_product ON pantry.packs(household_id,product_id,created_at,id);
CREATE TABLE pantry.barcodes(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id), product_id uuid NOT NULL,
 pack_id uuid, code text NOT NULL CHECK(code ~ '^[!-~]{1,64}$'), created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), version bigint NOT NULL DEFAULT 1,
 UNIQUE(household_id,id), CONSTRAINT pantry_barcode_identity UNIQUE(household_id,code),
 FOREIGN KEY(household_id,product_id) REFERENCES pantry.products(household_id,id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,product_id,pack_id) REFERENCES pantry.packs(household_id,product_id,id)
);
CREATE INDEX pantry_barcode_product ON pantry.barcodes(household_id,product_id,created_at,id);
-- Preserve every legacy field. Only unique supported exact identities become active aliases.
WITH candidates AS (
 SELECT p.*,count(*) OVER(PARTITION BY household_id,btrim(barcode)) occurrences FROM pantry.products p WHERE btrim(barcode) ~ '^[!-~]{1,64}$'
)
INSERT INTO pantry.barcodes(household_id,product_id,code,created_by)
 SELECT household_id,id,btrim(barcode),created_by FROM candidates WHERE occurrences=1;

CREATE TABLE pantry.stock_operations(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 lot_id uuid NOT NULL, movement_id uuid NOT NULL, kind text NOT NULL CHECK(kind IN('consume','undo')),
 request_key uuid NOT NULL, request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=65536),
 created_by uuid NOT NULL REFERENCES core.users(id), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id), UNIQUE(household_id,created_by,request_key), UNIQUE(household_id,movement_id),
 FOREIGN KEY(household_id,lot_id) REFERENCES pantry.lots(household_id,id),
 FOREIGN KEY(household_id,movement_id) REFERENCES pantry.stock_movements(household_id,id)
);
CREATE INDEX pantry_operation_lot ON pantry.stock_operations(household_id,lot_id,created_at DESC,id DESC);
REVOKE ALL ON pantry.packs,pantry.barcodes,pantry.stock_operations FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON pantry.packs,pantry.barcodes TO household_app;
GRANT SELECT,INSERT ON pantry.stock_operations TO household_app;
CREATE TRIGGER pantry_operation_immutable BEFORE UPDATE OR DELETE ON pantry.stock_operations FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();

CREATE FUNCTION pantry.guard_pack() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE h uuid; p uuid; base_dimension text; pack_dimension text; base_factor numeric; pack_factor numeric; base_amount numeric;
BEGIN
 IF TG_OP='DELETE' THEN h=OLD.household_id; p=OLD.product_id; ELSE h=NEW.household_id; p=NEW.product_id; END IF;
 SELECT u.dimension,u.factor_to_base INTO base_dimension,base_factor FROM pantry.products product JOIN core.units u ON u.code=product.unit_code
  WHERE product.household_id=h AND product.id=p FOR UPDATE OF product NOWAIT;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 IF NOT FOUND THEN RAISE EXCEPTION 'pack product missing' USING ERRCODE='23503'; END IF;
 IF TG_OP='UPDATE' AND (NEW.id,NEW.household_id,NEW.product_id,NEW.created_by) IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.product_id,OLD.created_by) THEN
  RAISE EXCEPTION 'pack identity immutable' USING ERRCODE='23514';
 END IF;
 SELECT dimension,factor_to_base INTO pack_dimension,pack_factor FROM core.units WHERE code=NEW.unit_code;
 IF base_dimension IS DISTINCT FROM pack_dimension THEN RAISE EXCEPTION 'pack dimension mismatch' USING ERRCODE='23514'; END IF;
 base_amount=NEW.quantity*pack_factor/base_factor;
 IF base_amount<=0 OR base_amount>999999999999.999999 OR base_amount<>round(base_amount,6) THEN
  RAISE EXCEPTION 'pack base amount unsupported' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' AND (SELECT count(*) FROM pantry.packs WHERE household_id=h AND product_id=p)>=50 THEN
  RAISE EXCEPTION 'pack limit exceeded' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' THEN NEW.version=OLD.version+1; NEW.updated_at=clock_timestamp(); END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_pack() FROM PUBLIC,household_app;
CREATE TRIGGER pantry_pack_change BEFORE INSERT OR UPDATE OR DELETE ON pantry.packs FOR EACH ROW EXECUTE FUNCTION pantry.guard_pack();

CREATE FUNCTION pantry.guard_barcode() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 PERFORM id FROM pantry.products WHERE household_id=NEW.household_id AND id=NEW.product_id FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'barcode product missing' USING ERRCODE='23503'; END IF;
 IF TG_OP='UPDATE' AND (NEW.id,NEW.household_id,NEW.created_by) IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.created_by) THEN
  RAISE EXCEPTION 'barcode identity immutable' USING ERRCODE='23514';
 END IF;
 IF (TG_OP='INSERT' OR NEW.product_id<>OLD.product_id) AND (SELECT count(*) FROM pantry.barcodes WHERE household_id=NEW.household_id AND product_id=NEW.product_id AND id<>NEW.id)>=100 THEN
  RAISE EXCEPTION 'barcode limit exceeded' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' THEN NEW.version=OLD.version+1; NEW.updated_at=clock_timestamp(); END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_barcode() FROM PUBLIC,household_app;
CREATE TRIGGER pantry_barcode_change BEFORE INSERT OR UPDATE ON pantry.barcodes FOR EACH ROW EXECUTE FUNCTION pantry.guard_barcode();

CREATE FUNCTION pantry.guard_product_stock_identity() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.barcode<>'' THEN RAISE EXCEPTION 'use barcode aliases' USING ERRCODE='23514'; END IF;
  RETURN NEW;
 END IF;
 IF (NEW.id,NEW.household_id,NEW.created_by) IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.created_by) THEN
  RAISE EXCEPTION 'product identity immutable' USING ERRCODE='23514';
 END IF;
 IF NEW.barcode IS DISTINCT FROM OLD.barcode THEN RAISE EXCEPTION 'legacy barcode read only; use aliases' USING ERRCODE='23514'; END IF;
 IF NEW.unit_code<>OLD.unit_code AND (EXISTS(SELECT 1 FROM pantry.packs WHERE household_id=OLD.household_id AND product_id=OLD.id) OR
  EXISTS(SELECT 1 FROM pantry.lots l JOIN pantry.stock_movements m ON m.household_id=l.household_id AND m.lot_id=l.id WHERE l.household_id=OLD.household_id AND l.product_id=OLD.id)) THEN
  RAISE EXCEPTION 'used product unit immutable' USING ERRCODE='23514';
 END IF;
 IF NEW.integer_quantities AND NOT OLD.integer_quantities AND EXISTS(
  SELECT 1 FROM pantry.lots l JOIN pantry.stock_movements m ON m.household_id=l.household_id AND m.lot_id=l.id WHERE l.household_id=OLD.household_id AND l.product_id=OLD.id AND m.quantity_delta<>trunc(m.quantity_delta)) THEN
  RAISE EXCEPTION 'fractional stock history exists' USING ERRCODE='23514';
 END IF;
 NEW.version=OLD.version+1; NEW.updated_at=clock_timestamp();
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_product_stock_identity() FROM PUBLIC,household_app;
CREATE TRIGGER pantry_product_stock_identity BEFORE INSERT OR UPDATE ON pantry.products FOR EACH ROW EXECUTE FUNCTION pantry.guard_product_stock_identity();

CREATE FUNCTION pantry.guard_lot_identity() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF (NEW.id,NEW.household_id,NEW.created_by) IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.created_by) OR
  ((NEW.product_id,NEW.location_id) IS DISTINCT FROM (OLD.product_id,OLD.location_id) AND EXISTS(SELECT 1 FROM pantry.stock_movements WHERE household_id=OLD.household_id AND lot_id=OLD.id)) THEN
  RAISE EXCEPTION 'used lot identity immutable' USING ERRCODE='23514';
 END IF;
 NEW.version=OLD.version+1; NEW.updated_at=clock_timestamp();
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_lot_identity() FROM PUBLIC,household_app;
CREATE TRIGGER pantry_lot_identity BEFORE UPDATE ON pantry.lots FOR EACH ROW EXECUTE FUNCTION pantry.guard_lot_identity();

CREATE FUNCTION pantry.guard_stock_movement() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE integers boolean; original pantry.stock_movements%ROWTYPE;
BEGIN
 SELECT p.integer_quantities INTO integers FROM pantry.lots l JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id
  WHERE l.household_id=NEW.household_id AND l.id=NEW.lot_id FOR UPDATE OF l NOWAIT FOR SHARE OF p NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'stock lot missing' USING ERRCODE='23503'; END IF;
 IF integers AND NEW.quantity_delta<>trunc(NEW.quantity_delta) THEN RAISE EXCEPTION 'integer stock required' USING ERRCODE='23514'; END IF;
 IF (NEW.reason='receive' AND NEW.quantity_delta<0) OR (NEW.reason IN('consume','waste') AND NEW.quantity_delta>0) OR
  ((NEW.reason='reversal') IS DISTINCT FROM (NEW.reverses_movement_id IS NOT NULL)) THEN
  RAISE EXCEPTION 'stock movement reason mismatch' USING ERRCODE='23514';
 END IF;
 IF NEW.reason='reversal' THEN
  SELECT * INTO original FROM pantry.stock_movements WHERE household_id=NEW.household_id AND id=NEW.reverses_movement_id;
  IF NOT FOUND OR original.lot_id<>NEW.lot_id OR original.quantity_delta<>-NEW.quantity_delta OR original.reason IN('reversal','transfer') THEN
   RAISE EXCEPTION 'invalid reversal source' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_stock_movement() FROM PUBLIC,household_app;
CREATE TRIGGER pantry_movement_guard BEFORE INSERT ON pantry.stock_movements FOR EACH ROW EXECUTE FUNCTION pantry.guard_stock_movement();

CREATE FUNCTION pantry.guard_stock_operation() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE movement pantry.stock_movements%ROWTYPE;
BEGIN
 SELECT * INTO movement FROM pantry.stock_movements WHERE household_id=NEW.household_id AND id=NEW.movement_id;
 IF NOT FOUND OR movement.lot_id<>NEW.lot_id OR movement.created_by<>NEW.created_by OR
  (NEW.kind='consume' AND movement.reason<>'consume') OR (NEW.kind='undo' AND movement.reason<>'reversal') THEN
  RAISE EXCEPTION 'operation movement mismatch' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_stock_operation() FROM PUBLIC,household_app;
CREATE TRIGGER pantry_operation_guard BEFORE INSERT ON pantry.stock_operations FOR EACH ROW EXECUTE FUNCTION pantry.guard_stock_operation();

CREATE OR REPLACE FUNCTION pantry.projection_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
  WHERE t.tgrelid='pantry.stock_movements'::regclass AND t.tgname='pantry_project_stock'
  AND t.tgenabled IN('O','A') AND t.tgtype=5 AND t.tgfoid='pantry.project_stock_movement()'::regprocedure
  AND p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog'])
 AND NOT EXISTS(
  SELECT 1 FROM (VALUES
   ('pantry.packs'::regclass,'pantry_pack_change','pantry.guard_pack()'::regprocedure,31),
   ('pantry.barcodes'::regclass,'pantry_barcode_change','pantry.guard_barcode()'::regprocedure,23),
   ('pantry.products'::regclass,'pantry_product_stock_identity','pantry.guard_product_stock_identity()'::regprocedure,23),
   ('pantry.lots'::regclass,'pantry_lot_identity','pantry.guard_lot_identity()'::regprocedure,19),
   ('pantry.stock_movements'::regclass,'pantry_movement_guard','pantry.guard_stock_movement()'::regprocedure,7),
   ('pantry.stock_operations'::regclass,'pantry_operation_guard','pantry.guard_stock_operation()'::regprocedure,7)
  ) required(relation,name,function_id,trigger_type)
  WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgrelid=required.relation
   AND t.tgname=required.name AND t.tgfoid=required.function_id AND t.tgtype=required.trigger_type AND t.tgenabled IN('O','A')
   AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog'])
 )
 AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid='pantry.stock_operations'::regclass AND t.tgname='pantry_operation_immutable'
  AND t.tgfoid='core.reject_mutation()'::regprocedure AND t.tgtype=27 AND t.tgenabled IN('O','A'))
 AND has_table_privilege('household_app','pantry.lot_balances','SELECT')
 AND NOT has_table_privilege('household_app','pantry.lot_balances','INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND has_table_privilege('household_app','pantry.stock_operations','SELECT')
 AND has_table_privilege('household_app','pantry.stock_operations','INSERT')
 AND NOT has_table_privilege('household_app','pantry.stock_operations','UPDATE,DELETE,TRUNCATE,TRIGGER')
$$;
COMMIT;
