BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE FUNCTION pantry.guard_reservation_allocation() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE product uuid; unit text; integers boolean; product_archived boolean; location_archived boolean; physical numeric; claimed numeric; root core.meal_reservations%ROWTYPE;
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'reservation allocation requires read committed isolation' USING ERRCODE='25000';
 END IF;
 SELECT p.id,p.unit_code,p.integer_quantities,p.archived_at IS NOT NULL,loc.archived_at IS NOT NULL
  INTO product,unit,integers,product_archived,location_archived FROM pantry.lots l
  JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id
  JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id
  WHERE l.household_id=NEW.household_id AND l.id=NEW.lot_id FOR UPDATE OF l NOWAIT FOR SHARE OF p,loc NOWAIT;
 IF NOT FOUND OR product<>NEW.product_id OR unit<>NEW.unit_code OR product_archived OR location_archived
  OR (integers AND NEW.quantity<>trunc(NEW.quantity)) THEN
  RAISE EXCEPTION 'reservation requires an active matching lot and exact base amount' USING ERRCODE='23514';
 END IF;
 -- Separate statements after the parent lock see committed stock and claims.
 SELECT * INTO root FROM core.meal_reservations WHERE household_id=NEW.household_id AND id=NEW.reservation_id;
 IF NOT FOUND OR root.state<>'active' OR root.expires_at<=clock_timestamp() THEN
  RAISE EXCEPTION 'reservation is not active' USING ERRCODE='23514';
 END IF;
 SELECT coalesce(sum(a.quantity),0) INTO claimed FROM core.meal_reservation_allocations a
  JOIN core.meal_reservations r ON r.household_id=a.household_id AND r.id=a.reservation_id
  WHERE a.household_id=NEW.household_id AND a.lot_id=NEW.lot_id AND r.state='active' AND r.expires_at>clock_timestamp()
  AND r.pantry_generation=root.pantry_generation;
 SELECT coalesce((SELECT quantity FROM pantry.lot_balances WHERE household_id=NEW.household_id AND lot_id=NEW.lot_id),0) INTO physical;
 IF claimed+NEW.quantity>physical THEN RAISE EXCEPTION 'insufficient free lot stock' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_reservation_allocation() FROM PUBLIC,household_app;
CREATE TRIGGER meal_reservation_allocation_guard BEFORE INSERT ON core.meal_reservation_allocations FOR EACH ROW EXECUTE FUNCTION pantry.guard_reservation_allocation();
COMMIT;
