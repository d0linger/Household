BEGIN;
-- Forward only the accepted Core fence; preserve the complete Fuel fingerprints.
DO $compatibility$
DECLARE overview_source text;expected_overview text:=$accepted_overview$
 SELECT (SELECT count(*)=1 AND bool_and(version=28 AND NOT dirty) FROM core.schema_migrations)
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
 $accepted_overview$;cols jsonb;rules jsonb;idx jsonb;authority_definition text;ready_definition text;old_hash text;new_hash text;
 required text:='bool_and(version=28 AND NOT dirty)';
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=31 AND NOT dirty) FROM core.schema_migrations)
 OR NOT core.finance_ready() OR NOT core.audit_ready() THEN RAISE EXCEPTION 'Fuel compatibility requires reviewed Core31 Finance';END IF;
 SELECT jsonb_agg(jsonb_build_array(a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attgenerated,a.attidentity,coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum)
 INTO cols FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid='fuel.vehicles'::regclass
 AND a.attnum>0 AND NOT a.attisdropped AND a.attname IN('next_service_on','next_inspection_on','insurance_renewal_on');
 SELECT jsonb_agg(jsonb_build_array(c.conname,pg_get_constraintdef(c.oid),c.convalidated) ORDER BY c.conname) INTO rules FROM pg_constraint c
 WHERE c.conrelid='fuel.vehicles'::regclass AND c.conname IN('fuel_vehicle_service_date','fuel_vehicle_inspection_date','fuel_vehicle_insurance_date');
 SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY pg_get_indexdef(i.indexrelid)) INTO idx FROM pg_index i
 WHERE i.indexrelid IN('fuel.vehicles_overview'::regclass,'fuel.fillups_overview_series'::regclass);
 expected_overview=replace(replace(replace(expected_overview,'#COLS#',quote_literal(cols::text)),'#RULES#',quote_literal(rules::text)),'#IDX#',quote_literal(idx::text));
 SELECT replace(p.prosrc,chr(13),'') INTO overview_source FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='fuel.overview_ready()'::regprocedure AND p.prokind='f' AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.provolatile='s'
 AND p.prorettype='boolean'::regtype AND p.proconfig=ARRAY['search_path=pg_catalog'] AND l.lanname='sql';
 IF overview_source IS DISTINCT FROM expected_overview THEN RAISE EXCEPTION 'unreviewed Fuel overview predecessor';END IF;
 SELECT pg_get_functiondef(p.oid) INTO authority_definition FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure AND p.prokind='f' AND p.proowner='household_owner'::regrole
 AND p.prosecdef AND p.provolatile='v' AND p.prorettype='void'::regtype AND p.proconfig=ARRAY['search_path=pg_catalog'] AND l.lanname='plpgsql';
 SELECT pg_get_functiondef(p.oid) INTO ready_definition FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='fuel.module_ready()'::regprocedure AND p.prokind='f' AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.provolatile='s'
 AND p.prorettype='boolean'::regtype AND p.proconfig=ARRAY['search_path=pg_catalog'] AND l.lanname='sql';
 IF authority_definition IS NULL OR ready_definition IS NULL OR
 (length(authority_definition)-length(replace(authority_definition,required,'')))/length(required)<>1 THEN RAISE EXCEPTION 'unreviewed Fuel authority predecessor';END IF;
 old_hash=md5(authority_definition);
 IF (length(ready_definition)-length(replace(ready_definition,old_hash,'')))/length(old_hash)<>1
 OR position(quote_literal(fuel.module_catalog()::text)||'::jsonb' in ready_definition)=0
 OR position(quote_literal(md5(pg_get_functiondef('fuel.module_catalog()'::regprocedure))) in ready_definition)=0 THEN
 RAISE EXCEPTION 'Fuel catalog differs from the accepted readiness baseline';END IF;
 EXECUTE format('CREATE OR REPLACE FUNCTION fuel.overview_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',replace(overview_source,required,'bool_and(version=31 AND NOT dirty)'));
 EXECUTE replace(authority_definition,required,'bool_and(version=31 AND NOT dirty)');
 new_hash=md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure));
 IF old_hash=new_hash THEN RAISE EXCEPTION 'Fuel authority compatibility was not applied';END IF;
 EXECUTE replace(ready_definition,old_hash,new_hash);
 IF NOT fuel.overview_ready() OR NOT fuel.module_ready() THEN RAISE EXCEPTION 'Fuel Finance compatibility failed';END IF;
END $compatibility$;
COMMIT;