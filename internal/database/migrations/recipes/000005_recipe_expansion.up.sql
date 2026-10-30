BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

ALTER TABLE recipes.recipes
 ADD COLUMN source_url text,
 ADD COLUMN source_import_sha256 bytea,
 ADD COLUMN portable_sha256 bytea,
 ADD COLUMN import_provenance jsonb NOT NULL DEFAULT '{}'::jsonb,
 ADD COLUMN nutrition_profile jsonb NOT NULL DEFAULT '{}'::jsonb,
 ADD CONSTRAINT recipe_source_url_safe CHECK(source_url IS NULL OR
  (length(source_url) BETWEEN 9 AND 2048 AND source_url ~ '^https://[^/?#@]+'
   AND position('?' IN source_url)=0 AND position('#' IN source_url)=0
   AND source_url !~ '[[:cntrl:]]')),
 ADD CONSTRAINT recipe_import_hash CHECK(source_import_sha256 IS NULL OR octet_length(source_import_sha256)=32),
 ADD CONSTRAINT recipe_portable_hash CHECK(portable_sha256 IS NULL OR octet_length(portable_sha256)=32),
 ADD CONSTRAINT recipe_import_provenance_size CHECK(jsonb_typeof(import_provenance)='object' AND octet_length(import_provenance::text)<=2048),
 ADD CONSTRAINT recipe_nutrition_profile_size CHECK(jsonb_typeof(nutrition_profile)='object' AND octet_length(nutrition_profile::text)<=8192);

ALTER TABLE recipes.ingredient_catalog
 ADD COLUMN substitution_rules jsonb NOT NULL DEFAULT '[]'::jsonb,
 ADD COLUMN density_g_per_ml numeric(18,6),
 ADD COLUMN density_source text,
 ADD COLUMN density_observed_on date,
 ADD CONSTRAINT ingredient_substitution_rules_size CHECK(jsonb_typeof(substitution_rules)='array'
  AND jsonb_array_length(substitution_rules)<=20 AND octet_length(substitution_rules::text)<=8192),
 ADD CONSTRAINT ingredient_density_complete CHECK
 ((density_g_per_ml IS NULL AND density_source IS NULL AND density_observed_on IS NULL) OR
  (density_g_per_ml>0 AND density_g_per_ml<=1000 AND density_source IS NOT NULL
   AND length(btrim(density_source)) BETWEEN 1 AND 256 AND density_source !~ '[[:cntrl:]]'
   AND density_observed_on BETWEEN DATE '1900-01-01' AND DATE '2200-12-31'));

ALTER TABLE recipes.ingredients
 ADD COLUMN conversion_provenance jsonb NOT NULL DEFAULT '{}'::jsonb,
 ADD CONSTRAINT ingredient_conversion_provenance_size CHECK(jsonb_typeof(conversion_provenance)='object'
  AND octet_length(conversion_provenance::text)<=2048);

-- Only ingredient meaning and recipe yield invalidate a manually sourced
-- nutrition profile; title, tags, and cooking notes do not.
CREATE FUNCTION recipes.ingredient_profile_fingerprint(h uuid,r uuid) RETURNS text
LANGUAGE sql STABLE SET search_path=pg_catalog AS $$
 SELECT encode(sha256(convert_to(jsonb_build_object(
  'base_servings',p.base_servings::text,
  'ingredients',coalesce((SELECT jsonb_agg(jsonb_build_object(
   'id',i.id,'name',i.name,'quantity',i.quantity::text,'unit_code',i.unit_code,
   'catalog_id',i.catalog_id,'preparation_note',i.preparation_note)
   ORDER BY i.position,i.id) FROM recipes.ingredients i
   WHERE i.household_id=p.household_id AND i.recipe_id=p.id),'[]'::jsonb))::text,'UTF8')),'hex')
 FROM recipes.recipes p WHERE p.household_id=h AND p.id=r
$$;
REVOKE ALL ON FUNCTION recipes.ingredient_profile_fingerprint(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION recipes.ingredient_profile_fingerprint(uuid,uuid) TO household_app;

-- The immutable history gets a digest of its complete, canonical PG jsonb
-- snapshot. Backfill is an owner-only migration under the household barrier;
-- the old immutability and audit triggers are restored before commit. The
-- derived digest is metadata, so a per-version audit copy of up to 8 MiB is
-- avoided during upgrade without hiding a business-content change.
ALTER TABLE recipes.versions ADD COLUMN snapshot_sha256 bytea;
ALTER TABLE recipes.versions DISABLE TRIGGER versions_immutable;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='recipes.versions'::regclass
  AND tgname='household_audit_change') THEN
  ALTER TABLE recipes.versions DISABLE TRIGGER household_audit_change;
 END IF;
END $$;
UPDATE recipes.versions SET snapshot_sha256=sha256(convert_to(snapshot::text,'UTF8'));
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='recipes.versions'::regclass
  AND tgname='household_audit_change') THEN
  ALTER TABLE recipes.versions ENABLE TRIGGER household_audit_change;
 END IF;
END $$;
ALTER TABLE recipes.versions ENABLE TRIGGER versions_immutable;
ALTER TABLE recipes.versions ALTER COLUMN snapshot_sha256 SET NOT NULL;
ALTER TABLE recipes.versions ADD CONSTRAINT recipe_snapshot_sha256 CHECK(octet_length(snapshot_sha256)=32);

CREATE OR REPLACE FUNCTION recipes.capture_version() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE payload jsonb; actor uuid;
BEGIN
 IF EXISTS(SELECT 1 FROM recipes.versions WHERE household_id=NEW.household_id AND recipe_id=NEW.id
  AND transaction_id=txid_current() AND transaction_started_at=transaction_timestamp()) THEN RETURN NULL; END IF;
 payload:=recipes.content_snapshot(NEW.household_id,NEW.id);
 IF payload IS NULL THEN RETURN NULL; END IF;
 actor:=nullif(current_setting('household.audit_user',true),'')::uuid;
 INSERT INTO recipes.versions(household_id,recipe_id,revision,transaction_id,snapshot,snapshot_sha256,created_by)
 SELECT NEW.household_id,NEW.id,coalesce(max(revision),0)+1,txid_current(),payload,
  sha256(convert_to(payload::text,'UTF8')),actor
 FROM recipes.versions WHERE household_id=NEW.household_id AND recipe_id=NEW.id;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION recipes.capture_version() FROM PUBLIC,household_app;

CREATE FUNCTION recipes.valid_nutrition_profile(profile jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
DECLARE code text; amount text; status text;
BEGIN
 IF profile='{}'::jsonb THEN RETURN true; END IF;
 IF jsonb_typeof(profile)<>'object' OR
  NOT(profile ?& ARRAY['basis','basis_servings','source','source_date','ingredient_fingerprint','nutrients','allergens']) OR
  EXISTS(SELECT 1 FROM jsonb_object_keys(profile) k WHERE k NOT IN
   ('basis','basis_servings','source','source_date','ingredient_fingerprint','nutrients','allergens')) OR
  coalesce(profile->>'basis','') NOT IN('per_recipe','per_serving') OR
  coalesce(profile->>'basis_servings','') !~ '^[0-9]{1,12}(\.[0-9]{1,6})?$' OR
  (profile->>'basis_servings')::numeric<=0 OR
  length(btrim(coalesce(profile->>'source',''))) NOT BETWEEN 1 AND 256 OR
  coalesce(profile->>'source','') ~ '[[:cntrl:]]' OR
  coalesce(profile->>'source_date','') !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' OR
  (profile->>'source_date')::date NOT BETWEEN DATE '1900-01-01' AND DATE '2200-12-31' OR
  coalesce(profile->>'ingredient_fingerprint','') !~ '^[0-9a-f]{64}$' OR
  jsonb_typeof(profile->'nutrients')<>'object' OR
  jsonb_typeof(profile->'allergens')<>'object' OR
  (SELECT count(*) FROM jsonb_object_keys(profile->'nutrients'))>12 OR
  (SELECT count(*) FROM jsonb_object_keys(profile->'allergens'))>14 THEN RETURN false; END IF;
 FOR code,amount IN SELECT key,value FROM jsonb_each_text(profile->'nutrients') LOOP
  IF code NOT IN('energy_kcal','protein_g','fat_g','saturated_fat_g','carbohydrate_g','sugars_g','fiber_g','salt_g','sodium_mg')
   OR amount !~ '^[0-9]{1,9}(\.[0-9]{1,6})?$' THEN RETURN false; END IF;
 END LOOP;
 FOR code,status IN SELECT key,value FROM jsonb_each_text(profile->'allergens') LOOP
  IF code NOT IN('gluten','crustaceans','eggs','fish','peanuts','soy','milk','tree_nuts',
   'celery','mustard','sesame','sulphites','lupin','molluscs') OR
   status NOT IN('contains','may_contain','reviewed_absent') THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
EXCEPTION WHEN data_exception THEN RETURN false;
END $$;
REVOKE ALL ON FUNCTION recipes.valid_nutrition_profile(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION recipes.valid_nutrition_profile(jsonb) TO household_app;
ALTER TABLE recipes.recipes ADD CONSTRAINT recipe_nutrition_profile_valid
 CHECK(recipes.valid_nutrition_profile(nutrition_profile));

CREATE FUNCTION recipes.valid_substitution_rules(rules jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
DECLARE rule jsonb; target text; seen text[]:='{}';
BEGIN
 IF jsonb_typeof(rules)<>'array' OR jsonb_array_length(rules)>20 THEN RETURN false; END IF;
 FOR rule IN SELECT value FROM jsonb_array_elements(rules) LOOP
  IF jsonb_typeof(rule)<>'object' OR
   NOT(rule ?& ARRAY['target_catalog_id','ratio','source_unit_code','unit_code','source','source_date','note']) OR
   EXISTS(SELECT 1 FROM jsonb_object_keys(rule) k WHERE k NOT IN
    ('target_catalog_id','ratio','source_unit_code','unit_code','source','source_date','note')) THEN RETURN false; END IF;
  target:=rule->>'target_catalog_id';
  IF coalesce(target,'') !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   OR target=ANY(seen) OR coalesce(rule->>'ratio','') !~ '^[0-9]{1,6}(\.[0-9]{1,6})?$'
   OR (rule->>'ratio')::numeric<=0 OR (rule->>'ratio')::numeric>1000
   OR coalesce(rule->>'source_unit_code','') NOT IN('piece','g','kg','ml','l','tsp','tbsp')
   OR coalesce(rule->>'unit_code','') NOT IN('piece','g','kg','ml','l','tsp','tbsp')
   OR length(btrim(coalesce(rule->>'source',''))) NOT BETWEEN 1 AND 256
   OR coalesce(rule->>'source','') ~ '[[:cntrl:]]'
   OR coalesce(rule->>'source_date','') !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'
   OR (rule->>'source_date')::date NOT BETWEEN DATE '1900-01-01' AND DATE '2200-12-31'
   OR length(coalesce(rule->>'note',''))>500 THEN RETURN false; END IF;
  seen:=array_append(seen,target);
 END LOOP;
 RETURN true;
EXCEPTION WHEN data_exception THEN RETURN false;
END $$;
REVOKE ALL ON FUNCTION recipes.valid_substitution_rules(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION recipes.valid_substitution_rules(jsonb) TO household_app;
ALTER TABLE recipes.ingredient_catalog ADD CONSTRAINT ingredient_substitution_rules_valid
 CHECK(recipes.valid_substitution_rules(substitution_rules));

CREATE FUNCTION recipes.valid_conversion_provenance(provenance jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
DECLARE kind text;
BEGIN
 IF provenance='{}'::jsonb THEN RETURN true; END IF;
 IF jsonb_typeof(provenance)<>'object' OR octet_length(provenance::text)>2048 OR
  NOT(provenance ?& ARRAY['kind','source_catalog_id','target_catalog_id','source_name',
   'source_quantity','source_unit_code','source','source_date','recorded_at']) OR
  EXISTS(SELECT 1 FROM jsonb_object_keys(provenance) k WHERE k NOT IN
   ('kind','source_catalog_id','target_catalog_id','source_name','source_quantity',
    'source_unit_code','ratio','rule_version','density_g_per_ml','source','source_date','recorded_at')) THEN
  RETURN false;
 END IF;
 kind:=provenance->>'kind';
 IF coalesce(kind,'') NOT IN('substitution','density') OR
  coalesce(provenance->>'source_catalog_id','') !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' OR
  coalesce(provenance->>'target_catalog_id','') !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' OR
  length(btrim(coalesce(provenance->>'source_name',''))) NOT BETWEEN 1 AND 200 OR
  coalesce(provenance->>'source_name','') ~ '[[:cntrl:]]' OR
  coalesce(provenance->>'source_quantity','') !~ '^[0-9]{1,12}(\.[0-9]{1,6})?$' OR
  (provenance->>'source_quantity')::numeric<=0 OR
  coalesce(provenance->>'source_unit_code','') NOT IN('piece','g','kg','ml','l','tsp','tbsp') OR
  length(btrim(coalesce(provenance->>'source',''))) NOT BETWEEN 1 AND 256 OR
  coalesce(provenance->>'source','') ~ '[[:cntrl:]]' OR
  coalesce(provenance->>'source_date','') !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' OR
  (provenance->>'source_date')::date NOT BETWEEN DATE '1900-01-01' AND DATE '2200-12-31' OR
  coalesce(provenance->>'recorded_at','') !~ '^20[0-9]{2}-[0-9]{2}-[0-9]{2}T' THEN
  RETURN false;
 END IF;
 IF kind='substitution' THEN
  IF provenance ? 'density_g_per_ml' OR
   NOT(provenance ?& ARRAY['ratio','rule_version']) OR
   provenance->>'source_catalog_id'=provenance->>'target_catalog_id' OR
   coalesce(provenance->>'ratio','') !~ '^[0-9]{1,6}(\.[0-9]{1,6})?$' OR
   (provenance->>'ratio')::numeric<=0 OR (provenance->>'ratio')::numeric>1000 OR
   coalesce(provenance->>'rule_version','') !~ '^[1-9][0-9]{0,17}$' THEN RETURN false; END IF;
 ELSE
  IF provenance ? 'ratio' OR provenance ? 'rule_version' OR
   NOT(provenance ? 'density_g_per_ml') OR
   provenance->>'source_catalog_id'<>provenance->>'target_catalog_id' OR
   coalesce(provenance->>'density_g_per_ml','') !~ '^[0-9]{1,12}(\.[0-9]{1,6})?$' OR
   (provenance->>'density_g_per_ml')::numeric<=0 OR
   (provenance->>'density_g_per_ml')::numeric>1000 THEN RETURN false; END IF;
 END IF;
 RETURN true;
EXCEPTION WHEN data_exception THEN RETURN false;
END $$;
REVOKE ALL ON FUNCTION recipes.valid_conversion_provenance(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION recipes.valid_conversion_provenance(jsonb) TO household_app;
ALTER TABLE recipes.ingredients ADD CONSTRAINT ingredient_conversion_provenance_valid
 CHECK(recipes.valid_conversion_provenance(conversion_provenance));

CREATE FUNCTION recipes.guard_expansion_metadata() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE rule jsonb; provenance_changed boolean:=true;
 source_dimension text; target_dimension text; source_factor numeric; target_factor numeric;
BEGIN
 IF TG_TABLE_NAME='recipes' THEN
  IF TG_OP='UPDATE' AND OLD.source_import_sha256 IS NOT NULL AND
   (NEW.source_import_sha256,NEW.source_url,NEW.import_provenance)
   IS DISTINCT FROM (OLD.source_import_sha256,OLD.source_url,OLD.import_provenance) THEN
   RAISE EXCEPTION 'import source identity is immutable' USING ERRCODE='23514';
  END IF;
 ELSIF TG_TABLE_NAME='ingredient_catalog' THEN
  FOR rule IN SELECT value FROM jsonb_array_elements(NEW.substitution_rules) LOOP
   IF rule->>'target_catalog_id'=NEW.id::text OR
    NOT EXISTS(SELECT 1 FROM recipes.ingredient_catalog c WHERE c.household_id=NEW.household_id
     AND c.id=(rule->>'target_catalog_id')::uuid AND NOT c.archived) THEN
    RAISE EXCEPTION 'current same-household substitution target required' USING ERRCODE='23514';
   END IF;
  END LOOP;
 ELSIF TG_TABLE_NAME='ingredients' THEN
  IF TG_OP='INSERT' AND NEW.conversion_provenance<>'{}'::jsonb THEN
   RAISE EXCEPTION 'conversion provenance requires an existing source ingredient' USING ERRCODE='23514';
  END IF;
  IF TG_OP='UPDATE' THEN
   IF (NEW.name,NEW.quantity,NEW.unit_code,NEW.catalog_id) IS DISTINCT FROM
    (OLD.name,OLD.quantity,OLD.unit_code,OLD.catalog_id) AND
    NEW.conversion_provenance=OLD.conversion_provenance THEN
    NEW.conversion_provenance:='{}'::jsonb;
   ELSIF NEW.conversion_provenance IS DISTINCT FROM OLD.conversion_provenance AND
    (NEW.name,NEW.quantity,NEW.unit_code,NEW.catalog_id) IS NOT DISTINCT FROM
    (OLD.name,OLD.quantity,OLD.unit_code,OLD.catalog_id) THEN
    RAISE EXCEPTION 'conversion provenance requires changed ingredient meaning' USING ERRCODE='23514';
   END IF;
   provenance_changed:=NEW.conversion_provenance IS DISTINCT FROM OLD.conversion_provenance;
  END IF;
  IF NEW.conversion_provenance<>'{}'::jsonb AND provenance_changed THEN
   IF NOT recipes.valid_conversion_provenance(NEW.conversion_provenance) OR
    TG_OP<>'UPDATE' OR
    NEW.conversion_provenance->>'source_catalog_id' IS DISTINCT FROM OLD.catalog_id::text OR
    NEW.conversion_provenance->>'source_name' IS DISTINCT FROM OLD.name OR
    NEW.conversion_provenance->>'source_quantity' IS DISTINCT FROM OLD.quantity::text OR
    NEW.conversion_provenance->>'source_unit_code' IS DISTINCT FROM OLD.unit_code OR
    NEW.catalog_id IS NULL OR NEW.catalog_id::text<>NEW.conversion_provenance->>'target_catalog_id' OR
    NOT EXISTS(SELECT 1 FROM recipes.ingredient_catalog c WHERE c.household_id=NEW.household_id
     AND c.id=(NEW.conversion_provenance->>'source_catalog_id')::uuid AND NOT c.archived) OR
    NOT EXISTS(SELECT 1 FROM recipes.ingredient_catalog c WHERE c.household_id=NEW.household_id
     AND c.id=NEW.catalog_id AND NOT c.archived) OR
    (NEW.conversion_provenance->>'kind'='substitution' AND NOT EXISTS(
     SELECT 1 FROM recipes.ingredient_catalog source,
      jsonb_array_elements(source.substitution_rules) AS chosen(value)
     WHERE source.household_id=NEW.household_id
      AND source.id=(NEW.conversion_provenance->>'source_catalog_id')::uuid
      AND NOT source.archived
      AND source.version=(NEW.conversion_provenance->>'rule_version')::bigint
      AND chosen.value->>'target_catalog_id'=NEW.conversion_provenance->>'target_catalog_id'
      AND chosen.value->>'ratio'=NEW.conversion_provenance->>'ratio'
      AND chosen.value->>'source_unit_code'=NEW.conversion_provenance->>'source_unit_code'
      AND chosen.value->>'unit_code'=NEW.unit_code
      AND chosen.value->>'source'=NEW.conversion_provenance->>'source'
      AND chosen.value->>'source_date'=NEW.conversion_provenance->>'source_date')) OR
    (NEW.conversion_provenance->>'kind'='density' AND NOT EXISTS(
     SELECT 1 FROM recipes.ingredient_catalog source
     WHERE source.household_id=NEW.household_id
      AND source.id=(NEW.conversion_provenance->>'source_catalog_id')::uuid
      AND NOT source.archived
      AND source.density_g_per_ml=(NEW.conversion_provenance->>'density_g_per_ml')::numeric
      AND source.density_source=NEW.conversion_provenance->>'source'
      AND source.density_observed_on=(NEW.conversion_provenance->>'source_date')::date)) THEN
    RAISE EXCEPTION 'current same-household conversion provenance required' USING ERRCODE='23514';
   END IF;
   SELECT dimension,factor_to_base INTO source_dimension,source_factor FROM core.units
    WHERE code=OLD.unit_code;
   SELECT dimension,factor_to_base INTO target_dimension,target_factor FROM core.units
    WHERE code=NEW.unit_code;
   IF source_dimension IS NULL OR target_dimension IS NULL OR OLD.quantity IS NULL OR NEW.quantity IS NULL THEN
    RAISE EXCEPTION 'conversion requires known quantities and units' USING ERRCODE='23514';
   END IF;
   IF NEW.conversion_provenance->>'kind'='substitution' THEN
    IF NEW.quantity IS DISTINCT FROM OLD.quantity*(NEW.conversion_provenance->>'ratio')::numeric THEN
     RAISE EXCEPTION 'substitution quantity differs from exact ratio' USING ERRCODE='23514';
    END IF;
   ELSIF source_dimension='mass' AND target_dimension='volume' THEN
    -- Multiplication equality avoids rounding a repeating mass/volume quotient
    -- into the stored numeric(18,6) value.
    IF NEW.quantity*target_factor*(NEW.conversion_provenance->>'density_g_per_ml')::numeric
       IS DISTINCT FROM OLD.quantity*source_factor THEN
     RAISE EXCEPTION 'density quantity is not exact' USING ERRCODE='23514';
    END IF;
   ELSIF source_dimension='volume' AND target_dimension='mass' THEN
    IF NEW.quantity*target_factor IS DISTINCT FROM
       OLD.quantity*source_factor*(NEW.conversion_provenance->>'density_g_per_ml')::numeric THEN
     RAISE EXCEPTION 'density quantity is not exact' USING ERRCODE='23514';
    END IF;
   ELSE
    RAISE EXCEPTION 'density provenance requires mass and volume units' USING ERRCODE='23514';
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION recipes.guard_expansion_metadata() FROM PUBLIC,household_app;
CREATE TRIGGER recipe_expansion_metadata BEFORE INSERT OR UPDATE OF source_url,source_import_sha256,import_provenance
 ON recipes.recipes FOR EACH ROW EXECUTE FUNCTION recipes.guard_expansion_metadata();
CREATE TRIGGER ingredient_substitution_targets BEFORE INSERT OR UPDATE OF substitution_rules
 ON recipes.ingredient_catalog FOR EACH ROW EXECUTE FUNCTION recipes.guard_expansion_metadata();
CREATE TRIGGER ingredient_conversion_fence BEFORE INSERT OR UPDATE ON recipes.ingredients
 FOR EACH ROW EXECUTE FUNCTION recipes.guard_expansion_metadata();

CREATE INDEX recipe_expansion_active ON recipes.recipes(household_id,created_at DESC,id DESC)
 WHERE archived_at IS NULL;
CREATE INDEX recipe_expansion_title ON recipes.recipes(household_id,title) WHERE archived_at IS NULL;
CREATE INDEX recipe_expansion_url ON recipes.recipes(household_id,source_url) WHERE source_url IS NOT NULL;
CREATE INDEX recipe_expansion_import_hash ON recipes.recipes(household_id,source_import_sha256) WHERE source_import_sha256 IS NOT NULL;
CREATE INDEX recipe_expansion_portable_hash ON recipes.recipes(household_id,portable_sha256) WHERE portable_sha256 IS NOT NULL;
CREATE INDEX recipe_expansion_tags ON recipes.recipe_tags(household_id,tag_id,recipe_id);
CREATE INDEX recipe_expansion_catalog ON recipes.ingredients(household_id,catalog_id,recipe_id) WHERE catalog_id IS NOT NULL;
CREATE INDEX recipe_expansion_cooked ON recipes.cooking_records(household_id,source_version_id,cooked_on DESC,id);

CREATE FUNCTION recipes.expansion_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='recipes.recipes'::regclass AND conname='recipe_nutrition_profile_valid' AND convalidated)
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='recipes.ingredient_catalog'::regclass AND conname='ingredient_substitution_rules_valid' AND convalidated)
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='recipes.ingredients'::regclass AND conname='ingredient_conversion_provenance_valid' AND convalidated)
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='recipes.versions'::regclass AND conname='recipe_snapshot_sha256' AND convalidated)
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='recipes.recipes'::regclass AND conname='recipe_portable_hash' AND convalidated)
 AND EXISTS(SELECT 1 FROM pg_index WHERE indexrelid='recipes.recipe_expansion_portable_hash'::regclass AND indisvalid)
 AND EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='recipes.recipes'::regclass AND tgname='recipe_expansion_metadata' AND tgenabled IN('O','A'))
 AND EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='recipes.ingredient_catalog'::regclass AND tgname='ingredient_substitution_targets' AND tgenabled IN('O','A'))
 AND EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='recipes.ingredients'::regclass AND tgname='ingredient_conversion_fence' AND tgenabled IN('O','A'))
 AND has_function_privilege('household_app','recipes.ingredient_profile_fingerprint(uuid,uuid)','EXECUTE')
 AND NOT has_function_privilege('household_app','recipes.guard_expansion_metadata()','EXECUTE')
 AND NOT has_table_privilege('household_app','recipes.versions','UPDATE,DELETE,TRUNCATE,TRIGGER')
$$;
REVOKE ALL ON FUNCTION recipes.expansion_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION recipes.expansion_ready() TO household_app;
COMMIT;
