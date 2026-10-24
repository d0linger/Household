BEGIN;
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA core;
CREATE INDEX search_title_trigram ON core.search_documents USING gin(title core.gin_trgm_ops);
CREATE TABLE core.alert_states(household_id uuid NOT NULL REFERENCES core.households(id),module_id text NOT NULL,resource_id uuid NOT NULL,kind text NOT NULL,active boolean NOT NULL,episode bigint NOT NULL DEFAULT 0,changed_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(household_id,module_id,resource_id,kind));
GRANT SELECT,INSERT,UPDATE,DELETE ON core.alert_states TO household_app;
COMMIT;
