BEGIN;

ALTER TABLE pantry.stock_movements
 ADD COLUMN waste_reason text,
 ADD COLUMN waste_operation_key uuid,
 ADD COLUMN waste_generation bigint,
 ADD COLUMN waste_request_hash bytea,
 ADD COLUMN waste_result_hash bytea,
 ADD CONSTRAINT stock_waste_reason CHECK(waste_reason IS NULL OR
  (reason='waste' AND waste_reason IN('expired','spoiled','damaged','overbought','preparation','other'))),
 ADD CONSTRAINT stock_waste_proof CHECK(
  (waste_operation_key IS NULL AND waste_generation IS NULL AND waste_request_hash IS NULL AND waste_result_hash IS NULL)
  OR(waste_operation_key IS NOT NULL AND waste_generation IS NOT NULL AND waste_generation>0
   AND waste_request_hash IS NOT NULL AND octet_length(waste_request_hash)=32
   AND waste_result_hash IS NOT NULL AND octet_length(waste_result_hash)=32 AND reason IN('waste','reversal')));
CREATE INDEX movements_waste_date ON pantry.stock_movements(household_id,created_at DESC,id DESC)
 WHERE reason IN('waste','reversal');
CREATE UNIQUE INDEX movements_waste_operation ON pantry.stock_movements(household_id,created_by,waste_generation,waste_operation_key)
 WHERE waste_operation_key IS NOT NULL;

CREATE FUNCTION pantry.guard_waste_proof() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF NEW.waste_operation_key IS NOT NULL AND NEW.reason='reversal' AND NOT EXISTS(
  SELECT 1 FROM pantry.stock_movements m WHERE m.household_id=NEW.household_id AND m.lot_id=NEW.lot_id
   AND m.id=NEW.reverses_movement_id AND m.reason='waste') THEN
  RAISE EXCEPTION 'waste operation must reverse an actual same-lot waste movement' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_waste_proof() FROM PUBLIC,household_app;
CREATE TRIGGER pantry_waste_proof BEFORE INSERT ON pantry.stock_movements FOR EACH ROW EXECUTE FUNCTION pantry.guard_waste_proof();

DO $ready_install$
DECLARE columns_spec jsonb;constraints_spec jsonb;indexes_spec jsonb;trigger_spec text;guard_hash text;
BEGIN
 SELECT jsonb_agg(jsonb_build_array(a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attgenerated,a.attidentity,
  coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum) INTO columns_spec
 FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE a.attrelid='pantry.stock_movements'::regclass AND a.attnum>0 AND NOT a.attisdropped
  AND a.attname IN('waste_reason','waste_operation_key','waste_generation','waste_request_hash','waste_result_hash');
 SELECT jsonb_agg(jsonb_build_array(c.conname,pg_get_constraintdef(c.oid),c.convalidated) ORDER BY c.conname) INTO constraints_spec
 FROM pg_constraint c WHERE c.conrelid='pantry.stock_movements'::regclass AND c.conname IN('stock_waste_reason','stock_waste_proof');
 SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY pg_get_indexdef(i.indexrelid)) INTO indexes_spec
 FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='pantry.stock_movements'::regclass
  AND c.relname IN('movements_waste_date','movements_waste_operation');
 SELECT pg_get_triggerdef(t.oid) INTO trigger_spec FROM pg_trigger t
 WHERE t.tgrelid='pantry.stock_movements'::regclass AND t.tgname='pantry_waste_proof' AND NOT t.tgisinternal;
 SELECT md5(pg_get_functiondef('pantry.guard_waste_proof()'::regprocedure)) INTO guard_hash;
 EXECUTE replace(replace(replace(replace(replace($definition$
 CREATE FUNCTION pantry.waste_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $function$
 SELECT (SELECT jsonb_agg(jsonb_build_array(a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attgenerated,a.attidentity,
  coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum)
  FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
  WHERE a.attrelid='pantry.stock_movements'::regclass AND a.attnum>0 AND NOT a.attisdropped
   AND a.attname IN('waste_reason','waste_operation_key','waste_generation','waste_request_hash','waste_result_hash'))=#COLUMNS#::jsonb
 AND (SELECT jsonb_agg(jsonb_build_array(c.conname,pg_get_constraintdef(c.oid),c.convalidated) ORDER BY c.conname)
  FROM pg_constraint c WHERE c.conrelid='pantry.stock_movements'::regclass AND c.conname IN('stock_waste_reason','stock_waste_proof'))=#CONSTRAINTS#::jsonb
 AND (SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY pg_get_indexdef(i.indexrelid))
  FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='pantry.stock_movements'::regclass
   AND c.relname IN('movements_waste_date','movements_waste_operation'))=#INDEXES#::jsonb
 AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid='pantry.stock_movements'::regclass AND t.tgname='pantry_waste_proof'
  AND NOT t.tgisinternal AND t.tgenabled IN('O','A') AND t.tgtype=7 AND pg_get_triggerdef(t.oid)=#TRIGGER#
  AND t.tgfoid='pantry.guard_waste_proof()'::regprocedure)
 AND md5(pg_get_functiondef('pantry.guard_waste_proof()'::regprocedure))=#GUARD#
 AND EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='pantry.guard_waste_proof()'::regprocedure AND NOT p.prosecdef
  AND p.provolatile='v' AND p.proowner='household_owner'::regrole AND p.proconfig=ARRAY['search_path=pg_catalog'])
 AND NOT has_function_privilege('household_app','pantry.guard_waste_proof()','EXECUTE')
 AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid='pantry.stock_movements'::regclass AND t.tgname='movements_immutable'
  AND t.tgtype=27 AND t.tgenabled IN('O','A') AND t.tgfoid='core.reject_mutation()'::regprocedure)
 AND has_table_privilege('household_app','pantry.stock_movements','SELECT')
 AND has_table_privilege('household_app','pantry.stock_movements','INSERT')
 AND NOT has_any_column_privilege('household_app','pantry.stock_movements','UPDATE')
 AND NOT has_table_privilege('household_app','pantry.stock_movements','DELETE,TRUNCATE')
 $function$;
 $definition$,'#COLUMNS#',quote_literal(columns_spec::text)),'#CONSTRAINTS#',quote_literal(constraints_spec::text)),
 '#INDEXES#',quote_literal(indexes_spec::text)),'#TRIGGER#',quote_literal(trigger_spec)),'#GUARD#',quote_literal(guard_hash));
END $ready_install$;
REVOKE ALL ON FUNCTION pantry.waste_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.waste_ready() TO household_app;

COMMIT;
