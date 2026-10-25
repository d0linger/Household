BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
LOCK TABLE recipes.ingredients IN SHARE MODE;
CREATE FUNCTION recipes.ingredient_key(raw text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE SET search_path=pg_catalog AS $$
 SELECT lower(normalize(btrim(regexp_replace(raw,'[[:space:]]+',' ','g')),NFC))
$$;
REVOKE ALL ON FUNCTION recipes.ingredient_key(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION recipes.ingredient_key(text) TO household_app;
CREATE TABLE recipes.ingredient_catalog(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 200 AND name !~ '[[:cntrl:]]'),
 name_key text COLLATE "C" GENERATED ALWAYS AS(recipes.ingredient_key(name)) STORED,
 archived boolean NOT NULL DEFAULT false, notes text NOT NULL DEFAULT '' CHECK(length(notes)<=20000),
 created_by uuid NOT NULL REFERENCES core.users(id), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(), version bigint NOT NULL DEFAULT 1,
 UNIQUE(household_id,id), UNIQUE(household_id,name_key)
);
CREATE TABLE recipes.ingredient_aliases(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id), catalog_id uuid NOT NULL,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 200 AND name !~ '[[:cntrl:]]'),
 name_key text COLLATE "C" GENERATED ALWAYS AS(recipes.ingredient_key(name)) STORED,
 created_by uuid NOT NULL REFERENCES core.users(id), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(), version bigint NOT NULL DEFAULT 1,
 UNIQUE(household_id,id), UNIQUE(household_id,name_key),
 FOREIGN KEY(household_id,catalog_id) REFERENCES recipes.ingredient_catalog(household_id,id) ON DELETE CASCADE
);
CREATE INDEX ingredient_alias_catalog ON recipes.ingredient_aliases(household_id,catalog_id,created_at,id);
ALTER TABLE recipes.ingredients ADD COLUMN catalog_id uuid;
ALTER TABLE recipes.ingredients ADD COLUMN catalog_label text NOT NULL DEFAULT '' CHECK(length(catalog_label)<=200);
ALTER TABLE recipes.ingredients ADD CONSTRAINT ingredient_catalog_owner FOREIGN KEY(household_id,catalog_id) REFERENCES recipes.ingredient_catalog(household_id,id);
ALTER TABLE recipes.ingredients ADD CONSTRAINT ingredient_catalog_label CHECK(catalog_id IS NOT NULL OR catalog_label='');
CREATE INDEX ingredient_catalog_reference ON recipes.ingredients(household_id,catalog_id) WHERE catalog_id IS NOT NULL;
REVOKE ALL ON recipes.ingredient_catalog,recipes.ingredient_aliases FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON recipes.ingredient_catalog,recipes.ingredient_aliases TO household_app;

CREATE FUNCTION recipes.guard_ingredient_catalog() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE parent_archived boolean;
BEGIN
 NEW.name=normalize(btrim(regexp_replace(NEW.name,'[[:space:]]+',' ','g')),NFC);
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.household_id,NEW.created_by,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.household_id,OLD.created_by,OLD.created_at) THEN
   RAISE EXCEPTION 'ingredient catalogue identity is immutable' USING ERRCODE='23514';
  END IF;
  NEW.version=OLD.version+1;
 ELSE
  NEW.version=1;
 END IF;
 IF TG_TABLE_NAME='ingredient_aliases' THEN
  IF TG_OP='UPDATE' AND NEW.catalog_id<>OLD.catalog_id THEN
   RAISE EXCEPTION 'ingredient alias parent is immutable' USING ERRCODE='23514';
  END IF;
  SELECT archived INTO parent_archived FROM recipes.ingredient_catalog WHERE household_id=NEW.household_id AND id=NEW.catalog_id FOR UPDATE NOWAIT;
  IF NOT FOUND OR parent_archived THEN
   RAISE EXCEPTION 'active owned ingredient catalogue required' USING ERRCODE='23514';
  END IF;
  IF (SELECT count(*) FROM recipes.ingredient_aliases WHERE household_id=NEW.household_id AND catalog_id=NEW.catalog_id AND id<>NEW.id)>=50 THEN
   RAISE EXCEPTION 'at most 50 aliases per ingredient catalogue' USING ERRCODE='23514';
  END IF;
 END IF;
 NEW.updated_at=clock_timestamp();
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION recipes.guard_ingredient_catalog() FROM PUBLIC,household_app;
CREATE TRIGGER ingredient_catalog_identity BEFORE INSERT OR UPDATE ON recipes.ingredient_catalog FOR EACH ROW EXECUTE FUNCTION recipes.guard_ingredient_catalog();
CREATE TRIGGER ingredient_alias_identity BEFORE INSERT OR UPDATE ON recipes.ingredient_aliases FOR EACH ROW EXECUTE FUNCTION recipes.guard_ingredient_catalog();

CREATE FUNCTION recipes.guard_catalog_selection() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE label text; inactive boolean;
BEGIN
 IF NEW.catalog_id IS NULL THEN
  NEW.catalog_label='';
 ELSIF TG_OP='INSERT' OR NEW.catalog_id IS DISTINCT FROM OLD.catalog_id THEN
  SELECT name,archived INTO label,inactive FROM recipes.ingredient_catalog WHERE household_id=NEW.household_id AND id=NEW.catalog_id FOR SHARE NOWAIT;
  IF NOT FOUND OR inactive THEN
   RAISE EXCEPTION 'active owned ingredient catalogue required' USING ERRCODE='23514';
  END IF;
  NEW.catalog_label=label;
 ELSE
  NEW.catalog_label=OLD.catalog_label;
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION recipes.guard_catalog_selection() FROM PUBLIC,household_app;
CREATE TRIGGER ingredient_catalog_selection BEFORE INSERT OR UPDATE ON recipes.ingredients FOR EACH ROW EXECUTE FUNCTION recipes.guard_catalog_selection();

CREATE FUNCTION recipes.catalog_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT NOT EXISTS(
  SELECT 1 FROM (VALUES
   ('recipes.ingredient_catalog'::regclass,'ingredient_catalog_identity','recipes.guard_ingredient_catalog()'::regprocedure),
   ('recipes.ingredient_aliases'::regclass,'ingredient_alias_identity','recipes.guard_ingredient_catalog()'::regprocedure),
   ('recipes.ingredients'::regclass,'ingredient_catalog_selection','recipes.guard_catalog_selection()'::regprocedure),
   ('core.ingredient_product_bindings'::regclass,'ingredient_binding_identity','core.guard_ingredient_binding()'::regprocedure)
  ) required(relation,name,function_id)
  WHERE NOT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
   WHERE t.tgrelid=required.relation AND t.tgname=required.name AND t.tgfoid=required.function_id AND t.tgtype=23
   AND t.tgenabled IN('O','A') AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog'])
 )
 AND NOT EXISTS(SELECT 1 FROM (VALUES('recipes.ingredient_catalog'),('recipes.ingredient_aliases'),('core.ingredient_product_bindings')) relation(name)
 CROSS JOIN (VALUES('SELECT'),('INSERT'),('UPDATE'),('DELETE')) permission(name)
 WHERE NOT has_table_privilege('household_app',relation.name,permission.name))
 AND NOT has_table_privilege('household_app','recipes.ingredient_catalog','TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','recipes.ingredient_aliases','TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','core.ingredient_product_bindings','TRUNCATE,TRIGGER')
$$;
REVOKE ALL ON FUNCTION recipes.catalog_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION recipes.catalog_ready() TO household_app;
COMMIT;
