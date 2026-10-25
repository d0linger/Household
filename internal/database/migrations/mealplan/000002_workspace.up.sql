BEGIN;
-- Match the application's household barrier before changing parent/child locks.
SELECT id FROM core.households ORDER BY id FOR UPDATE;
ALTER TABLE mealplan.plans ADD COLUMN is_template boolean NOT NULL DEFAULT false;
ALTER TABLE mealplan.plans ADD COLUMN copy_source_plan uuid;
ALTER TABLE mealplan.plans ADD COLUMN copy_key uuid;
ALTER TABLE mealplan.plans ADD COLUMN copy_request_hash bytea;
ALTER TABLE mealplan.plans ADD CONSTRAINT planning_copy_identity UNIQUE(household_id,created_by,copy_source_plan,copy_key);
ALTER TABLE mealplan.plans ADD CONSTRAINT planning_copy_provenance CHECK(
 (copy_source_plan IS NULL AND copy_key IS NULL AND copy_request_hash IS NULL) OR
 (copy_source_plan IS NOT NULL AND copy_key IS NOT NULL AND copy_request_hash IS NOT NULL AND octet_length(copy_request_hash)=32));
ALTER TABLE mealplan.meals ADD COLUMN kind text NOT NULL DEFAULT 'free' CHECK(kind IN('recipe','free','leftovers','out','order'));
ALTER TABLE mealplan.meals ADD COLUMN status text NOT NULL DEFAULT 'planned' CHECK(status IN('open','planned','purchased','cooked','cancelled'));
-- An old live link is preserved as an unreviewed recipe draft, never fabricated history.
UPDATE mealplan.meals m SET kind='recipe' WHERE EXISTS(
 SELECT 1 FROM core.resource_links l WHERE l.household_id=m.household_id AND l.source_module='mealplan' AND l.source_type='meals' AND l.source_id=m.id AND l.relation='meal_recipe');

CREATE TABLE mealplan.components(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 meal_id uuid NOT NULL, position integer NOT NULL CHECK(position BETWEEN 0 AND 9),
 servings numeric(18,6) NOT NULL CHECK(servings>0), prep_minutes integer CHECK(prep_minutes BETWEEN 0 AND 525600), cook_minutes integer CHECK(cook_minutes BETWEEN 0 AND 525600),
 source_recipe_id uuid NOT NULL, source_version_id uuid NOT NULL,
 source_generation bigint NOT NULL CHECK(source_generation>0), source_revision bigint NOT NULL CHECK(source_revision>0),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND snapshot ?& ARRAY['recipe','ingredients','steps'] AND jsonb_typeof(snapshot->'recipe')='object' AND jsonb_typeof(snapshot->'ingredients')='array' AND jsonb_typeof(snapshot->'steps')='array' AND snapshot->'recipe'->>'id' IS NOT NULL AND snapshot->'recipe'->>'id'=source_recipe_id::text AND octet_length(snapshot::text)<=1048576),
 copied_at timestamptz NOT NULL DEFAULT clock_timestamp(), created_by uuid NOT NULL REFERENCES core.users(id),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 UNIQUE(household_id,id), UNIQUE(household_id,meal_id,position) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(household_id,meal_id) REFERENCES mealplan.meals(household_id,id) ON DELETE CASCADE
);
CREATE INDEX planning_week ON mealplan.meals(household_id,plan_id,planned_on,meal_slot,id);
CREATE INDEX planning_components ON mealplan.components(household_id,meal_id,position,id);
REVOKE ALL ON mealplan.components FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON mealplan.components TO household_app;

CREATE FUNCTION mealplan.guard_plan() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF (NEW.id,NEW.household_id,NEW.created_by,NEW.copy_source_plan,NEW.copy_key,NEW.copy_request_hash) IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.created_by,OLD.copy_source_plan,OLD.copy_key,OLD.copy_request_hash) THEN
  RAISE EXCEPTION 'plan identity is immutable' USING ERRCODE='23514';
 END IF;
 IF EXISTS(SELECT 1 FROM mealplan.meals WHERE household_id=OLD.household_id AND plan_id=OLD.id AND planned_on NOT BETWEEN NEW.starts_on AND NEW.ends_on) THEN
  RAISE EXCEPTION 'plan interval excludes a meal' USING ERRCODE='23514';
 END IF;
 NEW.version:=OLD.version+1; NEW.updated_at:=clock_timestamp(); RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.guard_plan() FROM PUBLIC,household_app;
CREATE TRIGGER planning_plan_update BEFORE UPDATE ON mealplan.plans FOR EACH ROW EXECUTE FUNCTION mealplan.guard_plan();

CREATE FUNCTION mealplan.guard_meal() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE h uuid; p uuid; first_day date; last_day date;
BEGIN
 IF TG_OP='DELETE' THEN h:=OLD.household_id;p:=OLD.plan_id;ELSE h:=NEW.household_id;p:=NEW.plan_id;END IF;
 IF TG_OP='UPDATE' AND (NEW.id,NEW.household_id,NEW.plan_id,NEW.created_by) IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.plan_id,OLD.created_by) THEN
  RAISE EXCEPTION 'meal identity and plan are immutable' USING ERRCODE='23514';
 END IF;
 SELECT starts_on,ends_on INTO first_day,last_day FROM mealplan.plans WHERE household_id=h AND id=p FOR UPDATE NOWAIT;
 IF NOT FOUND THEN
  IF TG_OP='DELETE' THEN RETURN OLD;END IF;
  RAISE EXCEPTION 'plan not found' USING ERRCODE='23503';
 END IF;
 IF TG_OP<>'DELETE' THEN
  IF NEW.planned_on NOT BETWEEN first_day AND last_day THEN RAISE EXCEPTION 'meal outside plan interval' USING ERRCODE='23514';END IF;
  IF TG_OP='UPDATE' AND OLD.status IN('cooked','cancelled') AND (NEW.planned_on,NEW.meal_slot) IS DISTINCT FROM (OLD.planned_on,OLD.meal_slot) THEN
   RAISE EXCEPTION 'reopen terminal meal before moving' USING ERRCODE='23514';
  END IF;
  IF NEW.kind<>'recipe' AND EXISTS(SELECT 1 FROM mealplan.components WHERE household_id=h AND meal_id=NEW.id) THEN
   RAISE EXCEPTION 'remove recipe components before changing meal kind' USING ERRCODE='23514';
  END IF;
 END IF;
 UPDATE mealplan.plans SET updated_at=clock_timestamp() WHERE household_id=h AND id=p;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;
 IF TG_OP='UPDATE' THEN NEW.version:=OLD.version+1;END IF;
 NEW.updated_at:=clock_timestamp();RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.guard_meal() FROM PUBLIC,household_app;
CREATE TRIGGER planning_meal_change BEFORE INSERT OR UPDATE OR DELETE ON mealplan.meals FOR EACH ROW EXECUTE FUNCTION mealplan.guard_meal();

-- Validate the committed window, allowing an atomic swap to cross a full window.
-- The parent lock acquired above serializes concurrent inserts and moves.
CREATE FUNCTION mealplan.guard_window_count() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='UPDATE' AND NEW.planned_on=OLD.planned_on THEN RETURN NULL;END IF;
 IF NOT EXISTS(SELECT 1 FROM mealplan.meals WHERE household_id=NEW.household_id AND id=NEW.id) THEN RETURN NULL;END IF;
 IF EXISTS(SELECT 1 FROM generate_series(0,6) g(offset_days) WHERE
  (SELECT count(*) FROM mealplan.meals m WHERE m.household_id=NEW.household_id AND m.plan_id=NEW.plan_id
   AND m.planned_on BETWEEN NEW.planned_on-g.offset_days AND NEW.planned_on-g.offset_days+6)>42) THEN
  RAISE EXCEPTION 'at most 42 meals in a seven-day window' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION mealplan.guard_window_count() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER planning_window_count AFTER INSERT OR UPDATE ON mealplan.meals
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION mealplan.guard_window_count();

CREATE FUNCTION mealplan.guard_component() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE h uuid; m uuid; meal_kind text; used_bytes bigint;
BEGIN
 IF TG_OP='DELETE' THEN h:=OLD.household_id;m:=OLD.meal_id;ELSE h:=NEW.household_id;m:=NEW.meal_id;END IF;
 IF TG_OP='UPDATE' AND (NEW.id,NEW.household_id,NEW.meal_id,NEW.source_recipe_id,NEW.source_version_id,NEW.source_generation,NEW.source_revision,NEW.snapshot,NEW.copied_at,NEW.created_by)
  IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.meal_id,OLD.source_recipe_id,OLD.source_version_id,OLD.source_generation,OLD.source_revision,OLD.snapshot,OLD.copied_at,OLD.created_by) THEN
  RAISE EXCEPTION 'component source is immutable; replace through reviewed selection' USING ERRCODE='23514';
 END IF;
 SELECT kind INTO meal_kind FROM mealplan.meals WHERE household_id=h AND id=m FOR UPDATE NOWAIT;
 IF NOT FOUND THEN
  IF TG_OP='DELETE' THEN RETURN OLD;END IF;
  RAISE EXCEPTION 'meal not found' USING ERRCODE='23503';
 END IF;
 IF TG_OP<>'DELETE' THEN
  IF meal_kind<>'recipe' THEN RAISE EXCEPTION 'recipe components require recipe meal kind' USING ERRCODE='23514';END IF;
  IF (SELECT count(*) FROM mealplan.components WHERE household_id=h AND meal_id=m AND id<>NEW.id)>=10 THEN
   RAISE EXCEPTION 'at most ten components per meal' USING ERRCODE='23514';
  END IF;
  SELECT coalesce(sum(octet_length(snapshot::text)),0) INTO used_bytes FROM mealplan.components WHERE household_id=h AND meal_id=m AND id<>NEW.id;
  IF used_bytes+octet_length(NEW.snapshot::text)>2097152 THEN RAISE EXCEPTION 'meal snapshots exceed two MiB' USING ERRCODE='23514';END IF;
 END IF;
 UPDATE mealplan.meals SET updated_at=clock_timestamp() WHERE household_id=h AND id=m;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;
 IF TG_OP='UPDATE' THEN NEW.version:=OLD.version+1;END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.guard_component() FROM PUBLIC,household_app;
CREATE TRIGGER planning_component_change BEFORE INSERT OR UPDATE OR DELETE ON mealplan.components FOR EACH ROW EXECUTE FUNCTION mealplan.guard_component();
COMMIT;
