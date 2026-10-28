BEGIN;
ALTER TABLE fuel.vehicles
 ADD COLUMN next_service_on date,
 ADD COLUMN next_inspection_on date,
 ADD COLUMN insurance_renewal_on date,
 ADD CONSTRAINT fuel_vehicle_service_date CHECK(next_service_on IS NULL OR next_service_on BETWEEN date '1900-01-01' AND date '2199-12-31'),
 ADD CONSTRAINT fuel_vehicle_inspection_date CHECK(next_inspection_on IS NULL OR next_inspection_on BETWEEN date '1900-01-01' AND date '2199-12-31'),
 ADD CONSTRAINT fuel_vehicle_insurance_date CHECK(insurance_renewal_on IS NULL OR insurance_renewal_on BETWEEN date '1900-01-01' AND date '2199-12-31');
CREATE INDEX vehicles_overview ON fuel.vehicles(household_id,created_at DESC,id DESC);
CREATE INDEX fillups_overview_series ON fuel.fillups(household_id,odometer_series_id,occurred_on,odometer,id);
DO $install$
DECLARE cols jsonb;rules jsonb;idx jsonb;
BEGIN
 SELECT jsonb_agg(jsonb_build_array(a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attgenerated,a.attidentity,
  coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum) INTO cols
 FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE a.attrelid='fuel.vehicles'::regclass AND a.attnum>0 AND NOT a.attisdropped
  AND a.attname IN('next_service_on','next_inspection_on','insurance_renewal_on');
 SELECT jsonb_agg(jsonb_build_array(c.conname,pg_get_constraintdef(c.oid),c.convalidated) ORDER BY c.conname) INTO rules
 FROM pg_constraint c WHERE c.conrelid='fuel.vehicles'::regclass
 AND c.conname IN('fuel_vehicle_service_date','fuel_vehicle_inspection_date','fuel_vehicle_insurance_date');
 SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY pg_get_indexdef(i.indexrelid)) INTO idx
 FROM pg_index i WHERE i.indexrelid IN('fuel.vehicles_overview'::regclass,'fuel.fillups_overview_series'::regclass);
 EXECUTE replace(replace(replace($definition$
 CREATE FUNCTION fuel.overview_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT (SELECT count(*)=1 AND bool_and(version=26 AND NOT dirty) FROM core.schema_migrations)
 AND (SELECT jsonb_agg(jsonb_build_array(a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attgenerated,a.attidentity,
  coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum)
  FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
  WHERE a.attrelid='fuel.vehicles'::regclass AND a.attnum>0 AND NOT a.attisdropped
   AND a.attname IN('next_service_on','next_inspection_on','insurance_renewal_on'))=#COLS#::jsonb
 AND (SELECT jsonb_agg(jsonb_build_array(c.conname,pg_get_constraintdef(c.oid),c.convalidated) ORDER BY c.conname)
  FROM pg_constraint c WHERE c.conrelid='fuel.vehicles'::regclass
   AND c.conname IN('fuel_vehicle_service_date','fuel_vehicle_inspection_date','fuel_vehicle_insurance_date'))=#RULES#::jsonb
 AND (SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY pg_get_indexdef(i.indexrelid))
  FROM pg_index i WHERE i.indexrelid IN(to_regclass('fuel.vehicles_overview'),to_regclass('fuel.fillups_overview_series')))=#IDX#::jsonb
 AND has_table_privilege('household_app','fuel.vehicles','SELECT')
 AND has_table_privilege('household_app','fuel.vehicles','INSERT')
 AND has_table_privilege('household_app','fuel.vehicles','UPDATE')
 $body$;
 $definition$,'#COLS#',quote_literal(cols::text)),'#RULES#',quote_literal(rules::text)),'#IDX#',quote_literal(idx::text));
END $install$;
REVOKE ALL ON FUNCTION fuel.overview_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION fuel.overview_ready() TO household_app;
COMMIT;
