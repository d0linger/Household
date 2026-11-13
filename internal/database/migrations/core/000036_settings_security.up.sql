BEGIN;
-- Core's version is also part of Fuel's fail-closed authority contract.
-- Reconstruct the reviewed Fuel11 predecessor from the frozen Documents
-- catalog; never accept arbitrary live function definitions as a baseline.
DO $compatibility$
DECLARE baseline jsonb;item text;expected text;actual text;authority text;overview text;ready text;
 old_hash text;needle text;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=11 AND NOT dirty) FROM fuel.schema_migrations) THEN
  RAISE EXCEPTION 'Settings security require reviewed Fuel11';END IF;
 baseline=documents.predecessor_catalog()->'functions';
 authority=baseline->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 overview=baseline->'fuel.overview_ready()'->>'definition';
 ready=baseline->'fuel.module_ready()'->>'definition';
 needle='bool_and(version=33 AND NOT dirty)';old_hash=md5(authority);
 IF authority IS NULL OR overview IS NULL OR ready IS NULL
  OR (length(authority)-length(replace(authority,needle,'')))/length(needle)<>1
  OR (length(overview)-length(replace(overview,needle,'')))/length(needle)<>1
  OR (length(ready)-length(replace(ready,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'Settings security Fuel catalog differs from its frozen predecessor';END IF;
 authority=replace(authority,needle,'bool_and(version=35 AND NOT dirty)');
 overview=replace(overview,needle,'bool_and(version=35 AND NOT dirty)');
 ready=replace(ready,old_hash,md5(authority));
 IF position(quote_literal(fuel.module_catalog()::text)||'::jsonb' in ready)=0
  OR position(quote_literal(md5(pg_get_functiondef('fuel.module_catalog()'::regprocedure))) in ready)=0 THEN
  RAISE EXCEPTION 'Settings security Fuel11 catalog differs from its frozen predecessor';END IF;
 FOREACH item IN ARRAY ARRAY['fuel.module_ready()','fuel.overview_ready()','fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'] LOOP
  expected=CASE item WHEN 'fuel.module_ready()' THEN ready WHEN 'fuel.overview_ready()' THEN overview ELSE authority END;
  actual=NULL;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=baseline->item->>'owner' AND p.proacl::text IS NOT DISTINCT FROM baseline->item->>'acl';
  IF actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'unreviewed Settings security Fuel predecessor: %',item;END IF;
 END LOOP;
 old_hash=md5(authority);needle='bool_and(version=35 AND NOT dirty)';
 EXECUTE replace(overview,needle,'bool_and(version=36 AND NOT dirty)');
 EXECUTE replace(authority,needle,'bool_and(version=36 AND NOT dirty)');
 EXECUTE replace(ready,old_hash,md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)));
END $compatibility$;
ALTER TABLE core.user_preferences
 DROP CONSTRAINT user_preferences_theme_check,
 ADD CONSTRAINT user_preferences_theme_check
 CHECK (theme IN ('system', 'light', 'dark', 'pink-light', 'pink-dark', 'pink-system'));
-- Separate freshness from session age: upgrading does not grant fresh authority.
ALTER TABLE core.sessions ADD COLUMN reauthenticated_at timestamptz;
UPDATE core.sessions SET reauthenticated_at=created_at;
ALTER TABLE core.sessions ALTER COLUMN reauthenticated_at SET NOT NULL,
 ALTER COLUMN reauthenticated_at SET DEFAULT now();
-- Credential objects are encrypted; no public key, credential, challenge or device
-- label enters audit history. Lifecycle changes use explicit semantic core events.
CREATE TABLE core.passkey_credentials(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
 credential_id bytea NOT NULL UNIQUE CHECK(octet_length(credential_id) BETWEEN 1 AND 1024),
 encrypted_credential bytea NOT NULL CHECK(octet_length(encrypted_credential) BETWEEN 1 AND 131072),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 created_at timestamptz NOT NULL DEFAULT now(), last_used_at timestamptz,
 UNIQUE(user_id,id)
);
CREATE INDEX passkey_credentials_user ON core.passkey_credentials(user_id,created_at);
GRANT SELECT,INSERT,UPDATE,DELETE ON core.passkey_credentials TO household_app;
CREATE INDEX auth_challenges_active_binding ON core.auth_challenges(purpose,expires_at) WHERE consumed_at IS NULL;
-- Classify only encrypted passkey protocol storage as semantic-audit-only.
-- Keep all 163 business policies and reject every other unclassified table.
-- Pantry pins the exact audit function hash, so advance that one reviewed pin.
DO $audit_compatibility$
DECLARE baseline jsonb;old_audit text;reviewed_audit text;new_audit text;
 reviewed_ready text;actual text;needle text;old_hash text;new_hash text;
BEGIN
 baseline=documents.predecessor_catalog()->'functions';
 old_audit=baseline->'core.audit_ready()'->>'definition';
 reviewed_audit=replace(old_audit,
  'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND count(*)=152',
  'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND documents.expansion_ready() AND count(*)=163');
 SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid='core.audit_ready()'::regprocedure
  AND p.proowner::regrole::text=baseline->'core.audit_ready()'->>'owner'
  AND p.proacl::text IS NOT DISTINCT FROM baseline->'core.audit_ready()'->>'acl';
 IF actual IS DISTINCT FROM reviewed_audit OR reviewed_audit IS NOT DISTINCT FROM old_audit THEN
  RAISE EXCEPTION 'unreviewed Settings security audit predecessor';END IF;
 old_hash=md5(old_audit);new_hash=md5(reviewed_audit);
 reviewed_ready=baseline->'pantry.prepared_capture_ready()'->>'definition';
 IF reviewed_ready IS NULL OR (length(reviewed_ready)-length(replace(reviewed_ready,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'Settings security Pantry predecessor catalog differs';END IF;
 reviewed_ready=replace(reviewed_ready,old_hash,new_hash);
 SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid='pantry.prepared_capture_ready()'::regprocedure
  AND p.proowner::regrole::text=baseline->'pantry.prepared_capture_ready()'->>'owner'
  AND p.proacl::text IS NOT DISTINCT FROM baseline->'pantry.prepared_capture_ready()'->>'acl';
 IF actual IS DISTINCT FROM reviewed_ready
  OR position(quote_literal(pantry.prepared_capture_catalog()::text)||'::jsonb' in reviewed_ready)=0
  OR position(quote_literal(md5(pg_get_functiondef('pantry.prepared_capture_catalog()'::regprocedure))) in reviewed_ready)=0 THEN
  RAISE EXCEPTION 'unreviewed Settings security Pantry predecessor';END IF;
 needle='''auth_challenges'',''rate_limit_buckets''';
 IF (length(reviewed_audit)-length(replace(reviewed_audit,needle,'')))/length(needle)<>1 THEN
  RAISE EXCEPTION 'Settings security audit protocol allowlist differs';END IF;
 new_audit=replace(reviewed_audit,needle,'''auth_challenges'',''passkey_credentials'',''rate_limit_buckets''');
 EXECUTE new_audit;
 EXECUTE replace(reviewed_ready,new_hash,md5(new_audit));
 IF NOT core.audit_ready() OR NOT pantry.prepared_capture_ready()
  OR (SELECT count(*) FROM core.audit_policies)<>163 THEN
  RAISE EXCEPTION 'Settings security audit compatibility failed';END IF;
END $audit_compatibility$;
COMMIT;
