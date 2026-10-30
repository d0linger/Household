BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
DROP FUNCTION core.recipe_expansion_ready();
DROP TRIGGER recipe_import_batch_guard ON core.import_batches;
DROP FUNCTION core.guard_recipe_import_batch();
DROP INDEX core.recipe_import_batch_expiry;
ALTER TABLE core.import_batches DROP CONSTRAINT recipe_import_batch_purpose;
ALTER TABLE core.import_batches DROP COLUMN purpose,DROP COLUMN owner_user_id,
 DROP COLUMN expires_at,DROP COLUMN preview_sha256;
DROP TRIGGER ingredient_binding_shopping ON core.ingredient_product_bindings;
DROP FUNCTION core.guard_ingredient_shopping_binding();
DROP INDEX core.ingredient_shopping_binding_target;
ALTER TABLE core.ingredient_product_bindings DROP CONSTRAINT ingredient_shopping_binding_pair;
ALTER TABLE core.ingredient_product_bindings DROP COLUMN shopping_product_id,DROP COLUMN shopping_generation;
-- Existing recipe ingredients referencing these units prevent this downgrade.
DELETE FROM core.units WHERE code IN('tsp','tbsp');
COMMIT;
