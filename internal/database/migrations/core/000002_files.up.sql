BEGIN;
ALTER TABLE core.capture_jobs ADD COLUMN source_text text NOT NULL DEFAULT '' CHECK(length(source_text)<=20000);
CREATE TABLE core.file_deletions(storage_key text PRIMARY KEY,household_id uuid NOT NULL REFERENCES core.households(id),attachment_id uuid NOT NULL,available_at timestamptz NOT NULL DEFAULT now(),created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX file_deletions_due ON core.file_deletions(available_at);
GRANT SELECT,INSERT,UPDATE,DELETE ON core.file_deletions TO household_app;
REVOKE INSERT,UPDATE,DELETE ON core.backup_runs FROM household_app;
COMMIT;
