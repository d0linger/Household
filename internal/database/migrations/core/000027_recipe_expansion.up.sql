BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

DO $units$
BEGIN
 IF EXISTS(SELECT 1 FROM core.units WHERE code='tsp' AND (dimension<>'volume' OR label<>'TL' OR factor_to_base<>5))
 OR EXISTS(SELECT 1 FROM core.units WHERE code='tbsp' AND (dimension<>'volume' OR label<>'EL' OR factor_to_base<>15)) THEN
  RAISE EXCEPTION 'conflicting recipe volume unit' USING ERRCODE='23514';
 END IF;
END $units$;
INSERT INTO core.units(code,dimension,label,factor_to_base) VALUES
 ('tsp','volume','TL',5),('tbsp','volume','EL',15) ON CONFLICT(code) DO NOTHING;

ALTER TABLE core.ingredient_product_bindings
 ADD COLUMN shopping_product_id uuid,
 ADD COLUMN shopping_generation bigint,
 ADD CONSTRAINT ingredient_shopping_binding_pair CHECK
 ((shopping_product_id IS NULL AND shopping_generation IS NULL) OR
  (shopping_product_id IS NOT NULL AND shopping_generation IS NOT NULL AND shopping_generation>0));
CREATE INDEX ingredient_shopping_binding_target ON core.ingredient_product_bindings(household_id,shopping_product_id)
 WHERE shopping_product_id IS NOT NULL;

-- Core owns the optional-module link. No FK points from Core into Shopping.
-- Domain commands lock module state, catalogue, target, and binding before this
-- guard executes; this guard also refuses forged direct App-role updates.
CREATE FUNCTION core.guard_ingredient_shopping_binding() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE current_generation bigint; current_state text;
BEGIN
 IF NEW.shopping_product_id IS NULL THEN RETURN NEW; END IF;
 SELECT state,data_generation INTO current_state,current_generation FROM core.module_states
 WHERE household_id=NEW.household_id AND module_id='shopping';
 IF NOT FOUND OR current_state<>'enabled' OR current_generation<>NEW.shopping_generation
 OR NOT EXISTS(SELECT 1 FROM shopping.products p WHERE p.household_id=NEW.household_id AND p.id=NEW.shopping_product_id)
 OR NOT EXISTS(SELECT 1 FROM recipes.ingredient_catalog c JOIN core.module_states s
  ON s.household_id=c.household_id AND s.module_id='recipes' AND s.state='enabled'
  AND s.data_generation=NEW.source_generation
  WHERE c.household_id=NEW.household_id AND c.id=NEW.catalog_id AND NOT c.archived) THEN
  RAISE EXCEPTION 'current recipe and shopping endpoints required' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.guard_ingredient_shopping_binding() FROM PUBLIC,household_app;
CREATE TRIGGER ingredient_binding_shopping BEFORE INSERT OR UPDATE OF shopping_product_id,shopping_generation
 ON core.ingredient_product_bindings FOR EACH ROW EXECUTE FUNCTION core.guard_ingredient_shopping_binding();

-- Old generic CSV batches retain their exact behavior. A recipe review batch
-- has an explicit, non-interchangeable purpose and current actor/expiry/digest.
ALTER TABLE core.import_batches
 ADD COLUMN purpose text NOT NULL DEFAULT 'flat_csv',
 ADD COLUMN owner_user_id uuid REFERENCES core.users(id),
 ADD COLUMN expires_at timestamptz,
 ADD COLUMN preview_sha256 bytea,
 ADD CONSTRAINT recipe_import_batch_purpose CHECK
 ((purpose='flat_csv' AND owner_user_id IS NULL AND expires_at IS NULL AND preview_sha256 IS NULL) OR
  (purpose='recipe_review' AND module_id='recipes' AND entity='recipes' AND
   owner_user_id IS NOT NULL AND expires_at IS NOT NULL AND
   preview_sha256 IS NOT NULL AND octet_length(preview_sha256)=32 AND row_count BETWEEN 1 AND 20));
CREATE INDEX recipe_import_batch_expiry ON core.import_batches(household_id,expires_at,id)
 WHERE purpose='recipe_review';
CREATE FUNCTION core.guard_recipe_import_batch() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='UPDATE' AND
  (NEW.id,NEW.household_id,NEW.module_id,NEW.module_generation,NEW.entity,NEW.purpose,
   NEW.owner_user_id,NEW.expires_at,NEW.preview_sha256,NEW.row_count)
  IS DISTINCT FROM
  (OLD.id,OLD.household_id,OLD.module_id,OLD.module_generation,OLD.entity,OLD.purpose,
   OLD.owner_user_id,OLD.expires_at,OLD.preview_sha256,OLD.row_count) THEN
  RAISE EXCEPTION 'import batch identity is immutable' USING ERRCODE='23514';
 END IF;
 IF NEW.purpose='recipe_review' AND
  (NEW.expires_at<=clock_timestamp() OR NEW.expires_at>NEW.created_at+interval '30 minutes'
   OR NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
    WHERE m.household_id=NEW.household_id AND m.user_id=NEW.owner_user_id
    AND m.role IN('admin','editor') AND m.inactive_at IS NULL AND u.disabled_at IS NULL)) THEN
  RAISE EXCEPTION 'current recipe import owner and expiry required' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.guard_recipe_import_batch() FROM PUBLIC,household_app;
CREATE TRIGGER recipe_import_batch_guard BEFORE INSERT OR UPDATE ON core.import_batches
 FOR EACH ROW EXECUTE FUNCTION core.guard_recipe_import_batch();

CREATE FUNCTION core.recipe_expansion_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT EXISTS(SELECT 1 FROM core.units WHERE code='tsp' AND dimension='volume' AND factor_to_base=5)
 AND EXISTS(SELECT 1 FROM core.units WHERE code='tbsp' AND dimension='volume' AND factor_to_base=15)
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='core.ingredient_product_bindings'::regclass AND conname='ingredient_shopping_binding_pair' AND convalidated)
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='core.import_batches'::regclass AND conname='recipe_import_batch_purpose' AND convalidated)
 AND EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='core.ingredient_product_bindings'::regclass AND tgname='ingredient_binding_shopping' AND tgenabled IN('O','A') AND tgfoid='core.guard_ingredient_shopping_binding()'::regprocedure)
 AND EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='core.import_batches'::regclass AND tgname='recipe_import_batch_guard' AND tgenabled IN('O','A') AND tgfoid='core.guard_recipe_import_batch()'::regprocedure)
 AND NOT has_function_privilege('household_app','core.guard_ingredient_shopping_binding()','EXECUTE')
 AND NOT has_function_privilege('household_app','core.guard_recipe_import_batch()','EXECUTE')
 AND NOT has_table_privilege('household_app','core.import_batches','TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','core.ingredient_product_bindings','TRUNCATE,TRIGGER')
$$;
REVOKE ALL ON FUNCTION core.recipe_expansion_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.recipe_expansion_ready() TO household_app;
COMMIT;
