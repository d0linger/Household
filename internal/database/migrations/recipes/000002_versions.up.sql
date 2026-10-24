BEGIN;

-- Match BeginMutation's outer barrier before acquiring any schema/table lock.
SELECT id FROM core.households ORDER BY id FOR UPDATE;

ALTER TABLE recipes.recipes ADD COLUMN content_version bigint NOT NULL DEFAULT 1;
CREATE TABLE recipes.versions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 household_id uuid NOT NULL REFERENCES core.households(id),
 recipe_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 transaction_id bigint NOT NULL,
 transaction_started_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=8388608
  AND jsonb_array_length(snapshot->'ingredients')<=500 AND jsonb_array_length(snapshot->'steps')<=500 AND jsonb_array_length(snapshot->'tags')<=100),
 created_by uuid REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,recipe_id,revision),
 UNIQUE(household_id,recipe_id,transaction_id,transaction_started_at),
 UNIQUE(household_id,id)
);
CREATE INDEX versions_recipe_cursor ON recipes.versions(household_id,recipe_id,revision DESC);
ALTER TABLE recipes.recipes ADD COLUMN source_version_id uuid,
 ADD FOREIGN KEY(household_id,source_version_id) REFERENCES recipes.versions(household_id,id);
REVOKE ALL ON recipes.versions FROM PUBLIC,household_app;
GRANT SELECT ON recipes.versions TO household_app;
CREATE TRIGGER versions_immutable BEFORE UPDATE OR DELETE ON recipes.versions FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();

-- A single bundle uses bounded collections and retains decimal strings. Callers
-- reject sentinel rows before editing. History constraints reject oversize
-- recipes atomically, including migration-time preflight of existing records.
CREATE FUNCTION recipes.content_snapshot(h uuid,r uuid) RETURNS jsonb
LANGUAGE sql STABLE SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
  'recipe',to_jsonb(p)||jsonb_build_object('base_servings',p.base_servings::text),
  'ingredients',coalesce((SELECT jsonb_agg(to_jsonb(i)||jsonb_build_object('quantity',i.quantity::text) ORDER BY i.position,i.id) FROM (SELECT * FROM recipes.ingredients WHERE household_id=h AND recipe_id=r ORDER BY position,id LIMIT 501) i),'[]'::jsonb),
  'steps',coalesce((SELECT jsonb_agg(to_jsonb(s) ORDER BY s.position,s.id) FROM (SELECT * FROM recipes.steps WHERE household_id=h AND recipe_id=r ORDER BY position,id LIMIT 501) s),'[]'::jsonb),
  'tags',coalesce((SELECT jsonb_agg(to_jsonb(t) ORDER BY t.name,t.id) FROM (SELECT t.* FROM recipes.recipe_tags rt JOIN recipes.tags t ON t.household_id=rt.household_id AND t.id=rt.tag_id WHERE rt.household_id=h AND rt.recipe_id=r ORDER BY t.name,t.id LIMIT 101) t),'[]'::jsonb)
 ) FROM recipes.recipes p WHERE p.household_id=h AND p.id=r
$$;
REVOKE ALL ON FUNCTION recipes.content_snapshot(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION recipes.content_snapshot(uuid,uuid) TO household_app;

-- Deferred capture observes the final transaction, including generic writes,
-- imports and capture. No caller-controlled bypass flag or dynamic SQL is used.
CREATE FUNCTION recipes.capture_version() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE payload jsonb; actor uuid;
BEGIN
 -- XIDs are cluster-local and can repeat after restore. Bind deduplication to
 -- the transaction start instant as well; imported historical XIDs cannot
 -- suppress the first new edit in the restored installation.
 IF EXISTS(SELECT 1 FROM recipes.versions WHERE household_id=NEW.household_id AND recipe_id=NEW.id AND transaction_id=txid_current() AND transaction_started_at=transaction_timestamp()) THEN RETURN NULL; END IF;
 payload:=recipes.content_snapshot(NEW.household_id,NEW.id);
 IF payload IS NULL THEN RETURN NULL; END IF;
 actor:=nullif(current_setting('household.audit_user',true),'')::uuid;
 INSERT INTO recipes.versions(household_id,recipe_id,revision,transaction_id,snapshot,created_by)
 SELECT NEW.household_id,NEW.id,coalesce(max(revision),0)+1,txid_current(),payload,actor FROM recipes.versions WHERE household_id=NEW.household_id AND recipe_id=NEW.id;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION recipes.capture_version() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER recipe_version_capture AFTER INSERT OR UPDATE ON recipes.recipes
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION recipes.capture_version();

CREATE FUNCTION recipes.advance_content() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN NEW.content_version:=OLD.content_version+1; RETURN NEW; END $$;
REVOKE ALL ON FUNCTION recipes.advance_content() FROM PUBLIC,household_app;
CREATE TRIGGER recipe_content_version BEFORE UPDATE ON recipes.recipes FOR EACH ROW EXECUTE FUNCTION recipes.advance_content();

-- Every child mutation touches the parent. NOWAIT breaks child/parent lock
-- inversion with an editor holding the parent while replacing several children.
CREATE FUNCTION recipes.touch_parent() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE h uuid; old_id uuid; new_id uuid; parent uuid;
BEGIN
 IF TG_OP<>'INSERT' THEN h:=OLD.household_id; old_id:=OLD.recipe_id; END IF;
 IF TG_OP<>'DELETE' THEN h:=NEW.household_id; new_id:=NEW.recipe_id; END IF;
 FOR parent IN SELECT id FROM recipes.recipes WHERE household_id=h AND id IN(old_id,new_id) ORDER BY id FOR UPDATE NOWAIT LOOP
  UPDATE recipes.recipes SET updated_at=now() WHERE household_id=h AND id=parent;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION recipes.touch_parent() FROM PUBLIC,household_app;
CREATE TRIGGER ingredient_parent BEFORE INSERT OR UPDATE OR DELETE ON recipes.ingredients FOR EACH ROW EXECUTE FUNCTION recipes.touch_parent();
CREATE TRIGGER step_parent BEFORE INSERT OR UPDATE OR DELETE ON recipes.steps FOR EACH ROW EXECUTE FUNCTION recipes.touch_parent();
CREATE TRIGGER tag_parent BEFORE INSERT OR UPDATE OR DELETE ON recipes.recipe_tags FOR EACH ROW EXECUTE FUNCTION recipes.touch_parent();

CREATE FUNCTION recipes.touch_tag_recipes() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE parent uuid;
BEGIN
 FOR parent IN SELECT r.id FROM recipes.recipes r JOIN recipes.recipe_tags rt ON rt.household_id=r.household_id AND rt.recipe_id=r.id WHERE rt.household_id=OLD.household_id AND rt.tag_id=OLD.id ORDER BY r.id FOR UPDATE OF r NOWAIT LOOP
  UPDATE recipes.recipes SET updated_at=now() WHERE household_id=OLD.household_id AND id=parent;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION recipes.touch_tag_recipes() FROM PUBLIC,household_app;
CREATE TRIGGER tag_recipe_versions BEFORE UPDATE OR DELETE ON recipes.tags FOR EACH ROW EXECUTE FUNCTION recipes.touch_tag_recipes();

-- Baseline existing recipes honestly as migration-time snapshots, not invented
-- previous revisions. The migration lock prevents a concurrent backfill gap.
LOCK TABLE recipes.recipes,recipes.ingredients,recipes.steps,recipes.tags,recipes.recipe_tags IN ACCESS EXCLUSIVE MODE;
INSERT INTO recipes.versions(household_id,recipe_id,revision,transaction_id,snapshot)
 SELECT household_id,id,1,txid_current(),recipes.content_snapshot(household_id,id) FROM recipes.recipes;
COMMIT;
