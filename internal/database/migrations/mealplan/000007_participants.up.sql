BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

ALTER TABLE mealplan.meals ADD COLUMN servings_mode text NOT NULL DEFAULT 'manual'
 CONSTRAINT meal_servings_mode CHECK(servings_mode IN('manual','participants'));
ALTER TABLE mealplan.meals ADD COLUMN guest_count integer NOT NULL DEFAULT 0
 CONSTRAINT meal_guest_count CHECK(guest_count BETWEEN 0 AND 100);
ALTER TABLE mealplan.components ADD COLUMN servings_mode text NOT NULL DEFAULT 'independent'
 CONSTRAINT component_servings_mode CHECK(servings_mode IN('independent','meal'));

CREATE TABLE mealplan.participants(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 household_id uuid NOT NULL REFERENCES core.households(id),
 meal_id uuid NOT NULL,
 membership_id uuid NOT NULL,
 created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id),
 CONSTRAINT participant_meal_member UNIQUE(household_id,meal_id,membership_id),
 CONSTRAINT participant_meal_household FOREIGN KEY(household_id,meal_id)
  REFERENCES mealplan.meals(household_id,id) ON DELETE CASCADE,
 CONSTRAINT participant_member_household FOREIGN KEY(household_id,membership_id)
  REFERENCES core.memberships(household_id,id)
);
CREATE INDEX participants_member ON mealplan.participants(household_id,membership_id);
REVOKE ALL ON mealplan.participants FROM PUBLIC,household_app;
GRANT SELECT,INSERT,DELETE ON mealplan.participants TO household_app;

CREATE FUNCTION mealplan.guard_participant() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE h uuid; m uuid;
BEGIN
 IF TG_OP='UPDATE' THEN
  RAISE EXCEPTION 'replace a participant explicitly instead of updating its identity' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN h:=OLD.household_id;m:=OLD.meal_id;ELSE h:=NEW.household_id;m:=NEW.meal_id;END IF;
 PERFORM id FROM mealplan.meals WHERE household_id=h AND id=m FOR UPDATE NOWAIT;
 IF NOT FOUND THEN
  IF TG_OP='DELETE' THEN RETURN OLD;END IF;
  RAISE EXCEPTION 'participant meal missing' USING ERRCODE='23503';
 END IF;
 IF TG_OP='INSERT' AND (SELECT count(*) FROM mealplan.participants WHERE household_id=h AND meal_id=m)>=100 THEN
  RAISE EXCEPTION 'at most100 members per meal' USING ERRCODE='23514';
 END IF;
 -- Core membership ownership is a composite FK; service validation checks
 -- newly selected activity while retained/copied inactive members remain valid.
 UPDATE mealplan.meals SET updated_at=clock_timestamp() WHERE household_id=h AND id=m;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION mealplan.guard_participant() FROM PUBLIC,household_app;
CREATE TRIGGER planning_participant_change BEFORE INSERT OR UPDATE OR DELETE ON mealplan.participants
 FOR EACH ROW EXECUTE FUNCTION mealplan.guard_participant();

CREATE FUNCTION mealplan.check_participant_portions() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE h uuid;m uuid;mode text;guests integer;effective numeric;members bigint;
BEGIN
 IF TG_TABLE_SCHEMA<>'mealplan' OR TG_TABLE_NAME NOT IN('meals','components','participants') THEN
  RAISE EXCEPTION 'invalid portion-check context' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN
  h:=OLD.household_id;
  IF TG_TABLE_NAME='meals' THEN m:=OLD.id;ELSE m:=OLD.meal_id;END IF;
 ELSE
  h:=NEW.household_id;
  IF TG_TABLE_NAME='meals' THEN m:=NEW.id;ELSE m:=NEW.meal_id;END IF;
 END IF;
 SELECT servings_mode,guest_count,servings INTO mode,guests,effective
  FROM mealplan.meals WHERE household_id=h AND id=m;
 IF NOT FOUND THEN RETURN NULL;END IF;
 SELECT count(*) INTO members FROM mealplan.participants WHERE household_id=h AND meal_id=m;
 IF mode NOT IN('manual','participants') OR guests NOT BETWEEN 0 AND 100 OR members>100 OR (mode='participants' AND (members+guests=0 OR effective<>(members+guests)::numeric)) THEN
  RAISE EXCEPTION 'meal portions must match the reviewed participants and guests' USING ERRCODE='23514';
 END IF;
 IF EXISTS(SELECT 1 FROM mealplan.components WHERE household_id=h AND meal_id=m
 AND (servings_mode NOT IN('independent','meal') OR servings_mode='meal' AND servings<>effective)) THEN
  RAISE EXCEPTION 'linked component portions must match the meal' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION mealplan.check_participant_portions() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER planning_meal_portions AFTER INSERT OR UPDATE ON mealplan.meals
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION mealplan.check_participant_portions();
CREATE CONSTRAINT TRIGGER planning_component_portions AFTER INSERT OR UPDATE OR DELETE ON mealplan.components
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION mealplan.check_participant_portions();
CREATE CONSTRAINT TRIGGER planning_participant_portions AFTER INSERT OR UPDATE OR DELETE ON mealplan.participants
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION mealplan.check_participant_portions();
COMMIT;
