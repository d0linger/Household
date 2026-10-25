BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
UPDATE core.audit_policies SET safe_columns=array_append(safe_columns,'target_stock')
 WHERE schema_name='pantry' AND table_name='products';
COMMIT;
