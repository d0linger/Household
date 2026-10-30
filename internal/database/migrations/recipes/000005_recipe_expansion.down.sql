BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
DROP FUNCTION recipes.expansion_ready();
DROP INDEX recipes.recipe_expansion_cooked;
DROP INDEX recipes.recipe_expansion_catalog;
DROP INDEX recipes.recipe_expansion_tags;
DROP INDEX recipes.recipe_expansion_import_hash;
DROP INDEX recipes.recipe_expansion_portable_hash;
DROP INDEX recipes.recipe_expansion_url;
DROP INDEX recipes.recipe_expansion_active;
DROP INDEX recipes.recipe_expansion_title;
DROP TRIGGER ingredient_conversion_fence ON recipes.ingredients;
DROP TRIGGER ingredient_substitution_targets ON recipes.ingredient_catalog;
DROP TRIGGER recipe_expansion_metadata ON recipes.recipes;
DROP FUNCTION recipes.guard_expansion_metadata();
ALTER TABLE recipes.ingredient_catalog DROP CONSTRAINT ingredient_substitution_rules_valid;
ALTER TABLE recipes.recipes DROP CONSTRAINT recipe_nutrition_profile_valid;
ALTER TABLE recipes.ingredients DROP CONSTRAINT ingredient_conversion_provenance_valid;
DROP FUNCTION recipes.valid_substitution_rules(jsonb);
DROP FUNCTION recipes.valid_nutrition_profile(jsonb);
DROP FUNCTION recipes.valid_conversion_provenance(jsonb);
DROP FUNCTION recipes.ingredient_profile_fingerprint(uuid,uuid);
CREATE OR REPLACE FUNCTION recipes.capture_version() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE payload jsonb; actor uuid;
BEGIN
 IF EXISTS(SELECT 1 FROM recipes.versions WHERE household_id=NEW.household_id AND recipe_id=NEW.id
  AND transaction_id=txid_current() AND transaction_started_at=transaction_timestamp()) THEN RETURN NULL; END IF;
 payload:=recipes.content_snapshot(NEW.household_id,NEW.id);
 IF payload IS NULL THEN RETURN NULL; END IF;
 actor:=nullif(current_setting('household.audit_user',true),'')::uuid;
 INSERT INTO recipes.versions(household_id,recipe_id,revision,transaction_id,snapshot,created_by)
 SELECT NEW.household_id,NEW.id,coalesce(max(revision),0)+1,txid_current(),payload,actor
 FROM recipes.versions WHERE household_id=NEW.household_id AND recipe_id=NEW.id;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION recipes.capture_version() FROM PUBLIC,household_app;
ALTER TABLE recipes.versions DROP CONSTRAINT recipe_snapshot_sha256;
ALTER TABLE recipes.versions DROP COLUMN snapshot_sha256;
ALTER TABLE recipes.ingredients DROP CONSTRAINT ingredient_conversion_provenance_size;
ALTER TABLE recipes.ingredients DROP COLUMN conversion_provenance;
ALTER TABLE recipes.ingredient_catalog DROP CONSTRAINT ingredient_density_complete;
ALTER TABLE recipes.ingredient_catalog DROP CONSTRAINT ingredient_substitution_rules_size;
ALTER TABLE recipes.ingredient_catalog DROP COLUMN substitution_rules,DROP COLUMN density_g_per_ml,
 DROP COLUMN density_source,DROP COLUMN density_observed_on;
ALTER TABLE recipes.recipes DROP CONSTRAINT recipe_nutrition_profile_size;
ALTER TABLE recipes.recipes DROP CONSTRAINT recipe_import_provenance_size;
ALTER TABLE recipes.recipes DROP CONSTRAINT recipe_import_hash;
ALTER TABLE recipes.recipes DROP CONSTRAINT recipe_portable_hash;
ALTER TABLE recipes.recipes DROP CONSTRAINT recipe_source_url_safe;
ALTER TABLE recipes.recipes DROP COLUMN source_url,DROP COLUMN source_import_sha256,DROP COLUMN portable_sha256,
 DROP COLUMN import_provenance,DROP COLUMN nutrition_profile;
COMMIT;
