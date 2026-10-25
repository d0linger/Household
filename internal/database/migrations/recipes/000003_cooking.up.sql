BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

CREATE TABLE recipes.cooking_sessions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 household_id uuid NOT NULL REFERENCES core.households(id),
 user_id uuid NOT NULL,
 source_version_id uuid NOT NULL,
 servings numeric(18,6) NOT NULL CHECK(servings>0),
 current_step integer NOT NULL DEFAULT 0 CHECK(current_step BETWEEN 0 AND 499),
 checked_ids uuid[] NOT NULL DEFAULT '{}' CHECK(cardinality(checked_ids)<=500 AND array_position(checked_ids,NULL) IS NULL),
 state text NOT NULL DEFAULT 'active' CHECK(state IN('active','completed','abandoned')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id), UNIQUE(household_id,id,user_id,source_version_id),
 FOREIGN KEY(household_id,user_id) REFERENCES core.memberships(household_id,user_id),
 FOREIGN KEY(household_id,source_version_id) REFERENCES recipes.versions(household_id,id)
);
CREATE INDEX cooking_sessions_active ON recipes.cooking_sessions(household_id,user_id,created_at DESC,id DESC) WHERE state='active';
CREATE TABLE recipes.cooking_timers(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id), session_id uuid NOT NULL,
 name text NOT NULL CHECK(length(btrim(name)) BETWEEN 1 AND 100),
 duration_seconds integer NOT NULL CHECK(duration_seconds BETWEEN 1 AND 86400),
 state text NOT NULL CHECK(state IN('running','paused','done')),
 deadline timestamptz, remaining_millis bigint,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id), FOREIGN KEY(household_id,session_id) REFERENCES recipes.cooking_sessions(household_id,id),
 CHECK((state='running' AND deadline IS NOT NULL AND remaining_millis IS NULL)
 OR (state='paused' AND deadline IS NULL AND remaining_millis IS NOT NULL AND remaining_millis BETWEEN 1 AND duration_seconds::bigint*1000)
 OR (state='done' AND deadline IS NULL AND remaining_millis IS NULL))
);
CREATE INDEX cooking_timers_session ON recipes.cooking_timers(household_id,session_id,created_at,id);
CREATE TABLE recipes.cooking_records(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 session_id uuid NOT NULL, user_id uuid NOT NULL, source_version_id uuid NOT NULL,
 actual_servings numeric(18,6) NOT NULL CHECK(actual_servings>0),
 rating smallint CHECK(rating BETWEEN 1 AND 5), notes text NOT NULL DEFAULT '' CHECK(octet_length(notes)<=20000),
 cooked_on date NOT NULL CHECK(cooked_on BETWEEN DATE '1900-01-01' AND DATE '2200-12-31'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id), UNIQUE(household_id,session_id),
 FOREIGN KEY(household_id,session_id,user_id,source_version_id) REFERENCES recipes.cooking_sessions(household_id,id,user_id,source_version_id)
);
CREATE INDEX cooking_records_history ON recipes.cooking_records(household_id,created_at DESC,id DESC);
CREATE INDEX cooking_records_source ON recipes.cooking_records(household_id,source_version_id,created_at DESC,id DESC);
REVOKE ALL ON recipes.cooking_sessions,recipes.cooking_timers,recipes.cooking_records FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE ON recipes.cooking_sessions TO household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON recipes.cooking_timers TO household_app;
GRANT SELECT,INSERT ON recipes.cooking_records TO household_app;
CREATE TRIGGER cooking_records_immutable BEFORE UPDATE OR DELETE ON recipes.cooking_records FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();

CREATE FUNCTION recipes.protect_cooking_session() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF (NEW.id,NEW.household_id,NEW.user_id,NEW.source_version_id) IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.user_id,OLD.source_version_id) OR OLD.state<>'active' THEN
  RAISE EXCEPTION 'cooking session identity or terminal state is immutable' USING ERRCODE='23514';
 END IF;
 NEW.version:=OLD.version+1; NEW.updated_at:=clock_timestamp(); RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION recipes.protect_cooking_session() FROM PUBLIC,household_app;
CREATE TRIGGER cooking_session_update BEFORE UPDATE ON recipes.cooking_sessions FOR EACH ROW EXECUTE FUNCTION recipes.protect_cooking_session();

CREATE FUNCTION recipes.protect_cooking_timer() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE h uuid; s uuid; current_state text;
BEGIN
 IF TG_OP='DELETE' THEN h:=OLD.household_id; s:=OLD.session_id; ELSE h:=NEW.household_id; s:=NEW.session_id; END IF;
 IF TG_OP='UPDATE' AND (NEW.id,NEW.household_id,NEW.session_id) IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.session_id) THEN
  RAISE EXCEPTION 'timer identity is immutable' USING ERRCODE='23514';
 END IF;
 -- NOWAIT prevents a direct timer writer inverting the session-first order.
 SELECT state INTO current_state FROM recipes.cooking_sessions WHERE household_id=h AND id=s FOR UPDATE NOWAIT;
 IF current_state IS DISTINCT FROM 'active' THEN RAISE EXCEPTION 'cooking session is not active' USING ERRCODE='23514'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 IF TG_OP='UPDATE' THEN NEW.version:=OLD.version+1; END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION recipes.protect_cooking_timer() FROM PUBLIC,household_app;
CREATE TRIGGER cooking_timer_change BEFORE INSERT OR UPDATE OR DELETE ON recipes.cooking_timers FOR EACH ROW EXECUTE FUNCTION recipes.protect_cooking_timer();
COMMIT;
