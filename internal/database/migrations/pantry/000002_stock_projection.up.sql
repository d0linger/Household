BEGIN;
SELECT id FROM core.households FOR UPDATE;
LOCK TABLE pantry.stock_movements IN SHARE MODE;
CREATE TABLE pantry.lot_balances (
 household_id uuid NOT NULL,
 lot_id uuid NOT NULL,
 quantity numeric(30,6) NOT NULL CHECK(quantity>=0),
 movement_count bigint NOT NULL CHECK(movement_count>0),
 last_recorded_at timestamptz NOT NULL,
 PRIMARY KEY(household_id,lot_id),
 FOREIGN KEY(household_id,lot_id) REFERENCES pantry.lots(household_id,id)
);
INSERT INTO pantry.lot_balances(household_id,lot_id,quantity,movement_count,last_recorded_at)
 SELECT household_id,lot_id,sum(quantity_delta),count(*),max(created_at)
 FROM pantry.stock_movements GROUP BY household_id,lot_id;
REVOKE ALL ON pantry.lot_balances FROM PUBLIC,household_app;
GRANT SELECT ON pantry.lot_balances TO household_app;

CREATE FUNCTION pantry.project_stock_movement() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF TG_TABLE_SCHEMA<>'pantry' OR TG_TABLE_NAME<>'stock_movements' OR TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'invalid stock projection source';
 END IF;
 UPDATE pantry.lot_balances SET quantity=quantity+NEW.quantity_delta,
 movement_count=movement_count+1,last_recorded_at=greatest(last_recorded_at,NEW.created_at)
 WHERE household_id=NEW.household_id AND lot_id=NEW.lot_id;
 IF FOUND THEN RETURN NEW; END IF;
 INSERT INTO pantry.lot_balances AS b(household_id,lot_id,quantity,movement_count,last_recorded_at)
 VALUES(NEW.household_id,NEW.lot_id,NEW.quantity_delta,1,NEW.created_at)
 ON CONFLICT(household_id,lot_id) DO UPDATE
 SET quantity=b.quantity+EXCLUDED.quantity,movement_count=b.movement_count+1,
     last_recorded_at=greatest(b.last_recorded_at,EXCLUDED.last_recorded_at);
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.project_stock_movement() FROM PUBLIC,household_app;
CREATE TRIGGER pantry_project_stock AFTER INSERT ON pantry.stock_movements
 FOR EACH ROW EXECUTE FUNCTION pantry.project_stock_movement();

CREATE FUNCTION pantry.projection_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
  WHERE t.tgrelid='pantry.stock_movements'::regclass AND t.tgname='pantry_project_stock'
  AND t.tgenabled IN('O','A') AND t.tgtype=5 AND t.tgfoid='pantry.project_stock_movement()'::regprocedure
  AND p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog'])
 AND has_table_privilege('household_app','pantry.lot_balances','SELECT')
 AND NOT has_table_privilege('household_app','pantry.lot_balances','INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
$$;
REVOKE ALL ON FUNCTION pantry.projection_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.projection_ready() TO household_app;
CREATE INDEX pantry_product_page ON pantry.products(household_id,created_at DESC,id DESC);
CREATE INDEX pantry_lot_page ON pantry.lots(household_id,product_id,created_at DESC,id DESC);
CREATE INDEX pantry_movement_history ON pantry.stock_movements(household_id,created_at,lot_id);
COMMIT;
