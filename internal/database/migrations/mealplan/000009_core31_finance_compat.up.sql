BEGIN;
-- Preserve all five exact Mealplan policy classifications and domain snapshots.
DO $compatibility$
DECLARE accepted_prior text:=$prior_planning$
 SELECT NOT EXISTS(SELECT 1 FROM (VALUES
 ('leftover_sources',ARRAY['id'],ARRAY['plan_id','source_meal_id','source_component_id','reviewed_meal_version','reviewed_component_version','version']),
 ('leftover_allocations',ARRAY['id'],ARRAY['plan_id','source_id','target_meal_id','reviewed_source_version','reviewed_target_version','version']),
 ('weekly_settings',ARRAY['id'],ARRAY['plan_id','week_start','version']),
 ('calendar_feeds',ARRAY['id'],ARRAY['plan_id','week_start','module_generation','expires_at','revoked_at','version']),
 ('calendar_tombstones',ARRAY['household_id','module_generation','meal_id','planned_on'],ARRAY['plan_id','module_generation','sequence','deleted_at'])
 ) expected(name,keys,safe) WHERE NOT EXISTS(SELECT 1 FROM core.audit_policies p WHERE p.schema_name='mealplan' AND p.table_name=expected.name
 AND p.key_columns=expected.keys AND p.safe_columns=expected.safe AND p.label_column IS NULL))
 AND (SELECT count(*) FROM core.audit_policies)=100
$prior_planning$;source text;required text:='(SELECT count(*) FROM core.audit_policies)=100';
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=31 AND NOT dirty) FROM core.schema_migrations)
 OR NOT core.finance_ready() OR NOT core.audit_ready() OR NOT mealplan.expansion_ready() THEN
 RAISE EXCEPTION 'Mealplan compatibility requires reviewed Core31 and Mealplan8';END IF;
 SELECT replace(p.prosrc,chr(13),'') INTO source FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='core.planning_extensions_ready()'::regprocedure AND p.prokind='f' AND p.proowner='household_owner'::regrole
 AND p.prosecdef AND p.provolatile='s' AND p.prorettype='boolean'::regtype AND p.proconfig=ARRAY['search_path=pg_catalog'] AND l.lanname='sql';
 IF source IS DISTINCT FROM accepted_prior OR (length(source)-length(replace(source,required,'')))/length(required)<>1 THEN
 RAISE EXCEPTION 'unreviewed Mealplan Core policy predecessor';END IF;
 EXECUTE format('CREATE OR REPLACE FUNCTION core.planning_extensions_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',replace(source,required,'(SELECT count(*) FROM core.audit_policies)=121'));
 IF NOT core.planning_extensions_ready() OR NOT mealplan.expansion_ready() THEN RAISE EXCEPTION 'Mealplan Finance compatibility failed';END IF;
END $compatibility$;
COMMIT;