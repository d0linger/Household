BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
CREATE TABLE pantry.stock_batch_drafts(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id), owner_id uuid NOT NULL,
 generation bigint NOT NULL CHECK(generation>0), kind text NOT NULL CHECK(kind IN('intake','inventory')),
 title text NOT NULL DEFAULT '' CHECK(octet_length(title)<=100), location_id uuid,
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object' AND payload ? 'lines' AND jsonb_typeof(payload->'lines')='array'
  AND jsonb_array_length(payload->'lines')<=100 AND octet_length(payload::text)<=1048576),
 state text NOT NULL DEFAULT 'active' CHECK(state IN('active','posted','discarded')), version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL DEFAULT(clock_timestamp()+interval '30 days'),
 CHECK(kind='inventory' OR location_id IS NULL), UNIQUE(household_id,id),
 FOREIGN KEY(household_id,owner_id) REFERENCES core.memberships(household_id,user_id)
);
CREATE INDEX pantry_batch_owner ON pantry.stock_batch_drafts(household_id,owner_id,created_at DESC,id DESC) WHERE state='active';
CREATE INDEX pantry_batch_expiry ON pantry.stock_batch_drafts(expires_at,id) WHERE state<>'posted';
CREATE INDEX pantry_inventory_page ON pantry.lots(household_id,location_id,created_at DESC,id DESC);
CREATE TABLE pantry.stock_batch_receipts(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id), draft_id uuid NOT NULL,
 generation bigint NOT NULL CHECK(generation>0), draft_version bigint NOT NULL CHECK(draft_version>0),
 kind text NOT NULL CHECK(kind IN('intake','inventory')), created_by uuid NOT NULL REFERENCES core.users(id),
 request_key uuid NOT NULL, request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 line_count integer NOT NULL CHECK(line_count BETWEEN 1 AND 100), movement_count integer NOT NULL CHECK(movement_count BETWEEN 0 AND 100),
 CHECK(movement_count<=line_count AND(kind='inventory' OR movement_count=line_count)),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND snapshot ? 'lines' AND jsonb_typeof(snapshot->'lines')='array'
  AND jsonb_array_length(snapshot->'lines')<=100 AND octet_length(snapshot::text)<=2097152),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), UNIQUE(household_id,id), UNIQUE(household_id,draft_id), UNIQUE(household_id,created_by,request_key),
 FOREIGN KEY(household_id,draft_id) REFERENCES pantry.stock_batch_drafts(household_id,id)
);
CREATE TABLE pantry.stock_batch_lines(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id), receipt_id uuid NOT NULL,
 line_id uuid NOT NULL, lot_id uuid NOT NULL, movement_id uuid,
 before_quantity numeric(30,6) NOT NULL CHECK(before_quantity>=0), counted_quantity numeric(30,6) NOT NULL CHECK(counted_quantity>=0),
 quantity_delta numeric(18,6) NOT NULL, unit_code text NOT NULL REFERENCES core.units(code),
 CHECK(counted_quantity=before_quantity+quantity_delta), CHECK((quantity_delta=0)=(movement_id IS NULL)),
 UNIQUE(household_id,id), UNIQUE(household_id,receipt_id,line_id), UNIQUE(household_id,receipt_id,lot_id), UNIQUE(household_id,movement_id),
 FOREIGN KEY(household_id,receipt_id) REFERENCES pantry.stock_batch_receipts(household_id,id),
 FOREIGN KEY(household_id,lot_id) REFERENCES pantry.lots(household_id,id),
 FOREIGN KEY(household_id,movement_id) REFERENCES pantry.stock_movements(household_id,id)
);
CREATE INDEX pantry_batch_lot_history ON pantry.stock_batch_lines(household_id,lot_id,receipt_id);
REVOKE ALL ON pantry.stock_batch_drafts,pantry.stock_batch_receipts,pantry.stock_batch_lines FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON pantry.stock_batch_drafts TO household_app;
GRANT SELECT,INSERT ON pantry.stock_batch_receipts,pantry.stock_batch_lines TO household_app;
CREATE TRIGGER pantry_batch_receipt_immutable BEFORE UPDATE OR DELETE ON pantry.stock_batch_receipts FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();
CREATE TRIGGER pantry_batch_line_immutable BEFORE UPDATE OR DELETE ON pantry.stock_batch_lines FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();

CREATE FUNCTION pantry.guard_stock_batch_draft() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE current_generation bigint; current_state text;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.state='posted' OR (OLD.state<>'discarded' AND OLD.expires_at>clock_timestamp()) THEN
   RAISE EXCEPTION 'only discarded or expired unposted drafts may be pruned' USING ERRCODE='23514';
  END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' THEN
  IF OLD.state<>'active' OR (NEW.id,NEW.household_id,NEW.owner_id,NEW.generation,NEW.kind,NEW.created_at) IS DISTINCT FROM
   (OLD.id,OLD.household_id,OLD.owner_id,OLD.generation,OLD.kind,OLD.created_at) OR (OLD.location_id IS NOT NULL AND NEW.location_id IS DISTINCT FROM OLD.location_id) THEN
   RAISE EXCEPTION 'draft identity or terminal state immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.state<>'active' AND (NEW.title,NEW.payload,NEW.location_id) IS DISTINCT FROM (OLD.title,OLD.payload,OLD.location_id) THEN
   RAISE EXCEPTION 'finalizing cannot change draft input' USING ERRCODE='23514';
  END IF;
  IF NEW.state='posted' AND NOT EXISTS(SELECT 1 FROM pantry.stock_batch_receipts r WHERE r.household_id=OLD.household_id AND r.draft_id=OLD.id
   AND r.created_by=OLD.owner_id AND r.draft_version=OLD.version AND r.generation=OLD.generation) THEN
   RAISE EXCEPTION 'posted draft requires matching receipt' USING ERRCODE='23514';
  END IF;
  IF NEW.state<>'posted' AND EXISTS(SELECT 1 FROM pantry.stock_batch_receipts r WHERE r.household_id=OLD.household_id AND r.draft_id=OLD.id) THEN
   RAISE EXCEPTION 'draft already has a completion' USING ERRCODE='23514';
  END IF;
  NEW.version=OLD.version+1;
 ELSE
  IF NEW.state<>'active' THEN RAISE EXCEPTION 'new draft must be active' USING ERRCODE='23514'; END IF;
  NEW.version=1; NEW.created_at=clock_timestamp();
 END IF;
 SELECT state,data_generation INTO current_state,current_generation FROM core.module_states WHERE household_id=NEW.household_id AND module_id='pantry' FOR SHARE;
 IF current_state IS DISTINCT FROM 'enabled' OR current_generation IS DISTINCT FROM NEW.generation THEN
  RAISE EXCEPTION 'draft module generation unavailable' USING ERRCODE='23514';
 END IF;
 IF EXISTS(SELECT 1 FROM jsonb_array_elements(NEW.payload->'lines') x WHERE jsonb_typeof(x)<>'object' OR
  coalesce(x->>'id','') !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') OR
  EXISTS(SELECT 1 FROM jsonb_array_elements(NEW.payload->'lines') x GROUP BY lower(x->>'id') HAVING count(*)>1) THEN
  RAISE EXCEPTION 'draft line identities invalid' USING ERRCODE='23514';
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('pantry-batch-drafts/'||NEW.household_id::text||'/'||NEW.owner_id::text,0));
 IF NEW.state='active' AND (SELECT count(*) FROM pantry.stock_batch_drafts WHERE household_id=NEW.household_id AND owner_id=NEW.owner_id
  AND id<>NEW.id AND state='active' AND expires_at>clock_timestamp())>=10 THEN
  RAISE EXCEPTION 'active draft limit exceeded' USING ERRCODE='23514';
 END IF;
 NEW.updated_at=clock_timestamp();
 IF NEW.state='active' THEN NEW.expires_at=clock_timestamp()+interval '30 days'; ELSE NEW.expires_at=OLD.expires_at; END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_stock_batch_draft() FROM PUBLIC,household_app;
CREATE TRIGGER pantry_batch_draft_guard BEFORE INSERT OR UPDATE OR DELETE ON pantry.stock_batch_drafts FOR EACH ROW EXECUTE FUNCTION pantry.guard_stock_batch_draft();

CREATE FUNCTION pantry.guard_stock_batch_receipt() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE draft pantry.stock_batch_drafts%ROWTYPE;
BEGIN
 SELECT * INTO draft FROM pantry.stock_batch_drafts WHERE household_id=NEW.household_id AND id=NEW.draft_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR draft.state<>'active' OR draft.expires_at<=clock_timestamp() OR draft.owner_id<>NEW.created_by OR draft.generation<>NEW.generation
  OR draft.version<>NEW.draft_version OR draft.kind<>NEW.kind OR jsonb_array_length(draft.payload->'lines')<>NEW.line_count
  OR jsonb_array_length(NEW.snapshot->'lines')<>NEW.line_count THEN
  RAISE EXCEPTION 'batch receipt draft mismatch' USING ERRCODE='23514';
 END IF;
 NEW.created_at=clock_timestamp();
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_stock_batch_receipt() FROM PUBLIC,household_app;
CREATE TRIGGER pantry_batch_receipt_guard BEFORE INSERT ON pantry.stock_batch_receipts FOR EACH ROW EXECUTE FUNCTION pantry.guard_stock_batch_receipt();

CREATE FUNCTION pantry.guard_stock_batch_line() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE receipt pantry.stock_batch_receipts%ROWTYPE; draft pantry.stock_batch_drafts%ROWTYPE;
 actual_unit text; integers boolean; actual_location uuid; actual_stock numeric;
BEGIN
 SELECT * INTO receipt FROM pantry.stock_batch_receipts WHERE household_id=NEW.household_id AND id=NEW.receipt_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'batch receipt missing' USING ERRCODE='23503'; END IF;
 SELECT * INTO draft FROM pantry.stock_batch_drafts WHERE household_id=NEW.household_id AND id=receipt.draft_id;
 IF NOT EXISTS(SELECT 1 FROM jsonb_array_elements(draft.payload->'lines') x WHERE lower(x->>'id')=NEW.line_id::text
  AND(receipt.kind='intake' OR lower(x->>'lot_id')=NEW.lot_id::text)) THEN
  RAISE EXCEPTION 'batch line not selected in draft' USING ERRCODE='23514';
 END IF;
 SELECT p.unit_code,p.integer_quantities,l.location_id
 INTO actual_unit,integers,actual_location FROM pantry.lots l
 JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id
 WHERE l.household_id=NEW.household_id AND l.id=NEW.lot_id FOR UPDATE OF l NOWAIT FOR SHARE OF p NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'batch lot missing' USING ERRCODE='23503'; END IF;
 SELECT coalesce((SELECT quantity FROM pantry.lot_balances WHERE household_id=NEW.household_id AND lot_id=NEW.lot_id),0) INTO actual_stock;
 IF NEW.unit_code<>actual_unit OR NEW.counted_quantity<>actual_stock
  OR integers AND(NEW.counted_quantity<>trunc(NEW.counted_quantity) OR NEW.quantity_delta<>trunc(NEW.quantity_delta))
  OR receipt.kind='inventory' AND(draft.location_id IS NULL OR actual_location<>draft.location_id)
  OR receipt.kind='intake' AND(NEW.before_quantity<>0 OR NEW.quantity_delta<=0) THEN
  RAISE EXCEPTION 'batch line stock or identity mismatch' USING ERRCODE='23514';
 END IF;
 IF NEW.movement_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM pantry.stock_movements m WHERE m.household_id=NEW.household_id
  AND m.id=NEW.movement_id AND m.lot_id=NEW.lot_id AND m.correlation_id=receipt.id AND m.created_by=receipt.created_by
  AND m.quantity_delta=NEW.quantity_delta AND m.reason=CASE receipt.kind WHEN 'intake' THEN 'receive' ELSE 'adjust' END) THEN
  RAISE EXCEPTION 'batch journal amount or correlation mismatch' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_stock_batch_line() FROM PUBLIC,household_app;
CREATE TRIGGER pantry_batch_line_guard BEFORE INSERT ON pantry.stock_batch_lines FOR EACH ROW EXECUTE FUNCTION pantry.guard_stock_batch_line();

CREATE FUNCTION pantry.check_stock_batch_completion() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE rid uuid; expected_lines integer; expected_movements integer; actual_lines integer; actual_movements integer;
BEGIN
 IF TG_TABLE_NAME='stock_batch_receipts' THEN rid=NEW.id; ELSE rid=NEW.receipt_id; END IF;
 SELECT r.line_count,r.movement_count INTO expected_lines,expected_movements FROM pantry.stock_batch_receipts r
 JOIN pantry.stock_batch_drafts d ON d.household_id=r.household_id AND d.id=r.draft_id
  AND d.state='posted' AND d.version=r.draft_version+1
 WHERE r.household_id=NEW.household_id AND r.id=rid;
 SELECT count(*),count(movement_id) INTO actual_lines,actual_movements FROM pantry.stock_batch_lines WHERE household_id=NEW.household_id AND receipt_id=rid;
 IF expected_lines IS NULL OR expected_lines<>actual_lines OR expected_movements<>actual_movements THEN
  RAISE EXCEPTION 'incomplete stock batch bundle or draft transition' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION pantry.check_stock_batch_completion() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER pantry_batch_receipt_complete AFTER INSERT ON pantry.stock_batch_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pantry.check_stock_batch_completion();
CREATE CONSTRAINT TRIGGER pantry_batch_line_complete AFTER INSERT ON pantry.stock_batch_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pantry.check_stock_batch_completion();

CREATE FUNCTION pantry.stock_batch_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT NOT EXISTS(SELECT 1 FROM(VALUES
  ('pantry.stock_batch_drafts'::regclass,'pantry_batch_draft_guard','pantry.guard_stock_batch_draft()'::regprocedure,31),
  ('pantry.stock_batch_receipts'::regclass,'pantry_batch_receipt_guard','pantry.guard_stock_batch_receipt()'::regprocedure,7),
  ('pantry.stock_batch_lines'::regclass,'pantry_batch_line_guard','pantry.guard_stock_batch_line()'::regprocedure,7),
  ('pantry.stock_batch_receipts'::regclass,'pantry_batch_receipt_complete','pantry.check_stock_batch_completion()'::regprocedure,5),
  ('pantry.stock_batch_lines'::regclass,'pantry_batch_line_complete','pantry.check_stock_batch_completion()'::regprocedure,5)
 ) required(relation,name,function_id,trigger_type) WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
  WHERE t.tgrelid=required.relation AND t.tgname=required.name AND t.tgfoid=required.function_id AND t.tgtype=required.trigger_type
  AND t.tgenabled IN('O','A') AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM(VALUES('pantry.stock_batch_receipts'::regclass,'pantry_batch_receipt_immutable'),
  ('pantry.stock_batch_lines'::regclass,'pantry_batch_line_immutable')) required(relation,name)
  WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=required.relation AND t.tgname=required.name
   AND t.tgfoid='core.reject_mutation()'::regprocedure AND t.tgtype=27 AND t.tgenabled IN('O','A')))
 AND NOT EXISTS(SELECT 1 FROM(VALUES('pantry.stock_batch_drafts'),('pantry.stock_batch_receipts'),('pantry.stock_batch_lines')) required(relation)
  WHERE NOT has_table_privilege('household_app',required.relation,'SELECT') OR NOT has_table_privilege('household_app',required.relation,'INSERT')
  OR has_table_privilege('household_app',required.relation,'TRUNCATE,TRIGGER'))
 AND has_table_privilege('household_app','pantry.stock_batch_drafts','UPDATE') AND has_table_privilege('household_app','pantry.stock_batch_drafts','DELETE')
 AND NOT has_table_privilege('household_app','pantry.stock_batch_receipts','UPDATE,DELETE')
 AND NOT has_table_privilege('household_app','pantry.stock_batch_lines','UPDATE,DELETE')
 AND NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgname IN('pantry_batch_receipt_complete','pantry_batch_line_complete')
  AND t.tgrelid IN('pantry.stock_batch_receipts'::regclass,'pantry.stock_batch_lines'::regclass) AND(NOT t.tgdeferrable OR NOT t.tginitdeferred))
$$;
REVOKE ALL ON FUNCTION pantry.stock_batch_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.stock_batch_ready() TO household_app;
COMMIT;
