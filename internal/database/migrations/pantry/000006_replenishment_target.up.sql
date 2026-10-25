BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
ALTER TABLE pantry.products ADD COLUMN target_stock numeric(18,6);
ALTER TABLE pantry.products ADD CONSTRAINT pantry_target_stock_valid CHECK(
 target_stock IS NULL OR (target_stock<>'NaN'::numeric AND target_stock>=0
 AND low_stock_threshold IS NOT NULL AND target_stock>=low_stock_threshold
 AND (NOT integer_quantities OR target_stock=trunc(target_stock)))
);
CREATE FUNCTION pantry.replenishment_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='pantry.products'::regclass AND attname='target_stock'
 AND atttypid='numeric'::regtype AND atttypmod=1179658 AND NOT attisdropped)
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='pantry.products'::regclass AND conname='pantry_target_stock_valid' AND contype='c' AND convalidated)
 AND has_table_privilege('household_app','pantry.products','SELECT')
 AND has_table_privilege('household_app','pantry.products','INSERT')
 AND has_table_privilege('household_app','pantry.products','UPDATE')
 AND EXISTS(SELECT 1 FROM core.audit_policies WHERE schema_name='pantry' AND table_name='products' AND 'target_stock'=ANY(safe_columns))
$$;
REVOKE ALL ON FUNCTION pantry.replenishment_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.replenishment_ready() TO household_app;
COMMIT;
