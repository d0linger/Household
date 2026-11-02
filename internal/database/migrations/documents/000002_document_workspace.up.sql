BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
DO $predecessor$
DECLARE snapshot jsonb;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=33 AND NOT dirty) FROM core.schema_migrations)
  OR NOT core.audit_ready() OR NOT core.finance_ready() OR NOT meters.expansion_ready()
  OR NOT chores.expansion_ready() OR NOT fuel.module_ready() OR NOT pantry.prepared_capture_ready()
  OR NOT mealplan.expansion_ready() OR (SELECT count(*) FROM core.audit_policies)<>152 THEN
  RAISE EXCEPTION 'Documents requires the complete accepted Core33 predecessor';END IF;
 SELECT jsonb_build_object('finance_catalog',core.finance_catalog(),
  'audit_policies',(SELECT jsonb_agg(to_jsonb(policy) ORDER BY schema_name,table_name) FROM core.audit_policies policy),
  'functions',jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
   'owner',p.proowner::regrole::text,'acl',p.proacl::text))) INTO snapshot
 FROM pg_proc p WHERE p.oid IN('core.audit_ready()'::regprocedure,'core.finance_ready()'::regprocedure,
  'core.finance_producers_ready()'::regprocedure,'core.planning_extensions_ready()'::regprocedure,
  'fuel.overview_ready()'::regprocedure,'fuel.module_ready()'::regprocedure,
  'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure,'pantry.prepared_capture_ready()'::regprocedure);
 EXECUTE format('CREATE FUNCTION documents.predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION documents.predecessor_catalog() FROM PUBLIC,household_app;
CREATE TABLE documents.profiles(
 document_id uuid PRIMARY KEY,household_id uuid NOT NULL REFERENCES core.households(id),
 document_type text NOT NULL DEFAULT 'other' CHECK(document_type IN('other','invoice','warranty','contract','identity','manual')),
 review_state text NOT NULL DEFAULT 'unreviewed' CHECK(review_state IN('unreviewed','reviewed','needs_attention')),
 fields jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(fields)='object' AND octet_length(fields::text)<=16000),
 manual_keys text[] NOT NULL DEFAULT '{}',current_version_id uuid,reviewed_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now(),UNIQUE(household_id,document_id),
 FOREIGN KEY(household_id,document_id) REFERENCES documents.documents(household_id,id)
);
CREATE TABLE documents.file_versions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),document_id uuid NOT NULL,
 attachment_id uuid NOT NULL,ordinal integer NOT NULL CHECK(ordinal>0),source text NOT NULL CHECK(source IN('upload','legacy','watch','imap','paperless','derived')),
 original_sha256 bytea NOT NULL CHECK(octet_length(original_sha256)=32),original_size bigint NOT NULL CHECK(original_size BETWEEN 1 AND 10485760),
 extracted_text text NOT NULL DEFAULT '' CHECK(octet_length(extracted_text)<=80000),text_provider text NOT NULL DEFAULT '' CHECK(length(text_provider)<=40),
 page_count integer NOT NULL DEFAULT 1 CHECK(page_count BETWEEN 1 AND 100),created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(household_id,id),UNIQUE(household_id,document_id,id),UNIQUE(document_id,ordinal),UNIQUE(attachment_id),
 FOREIGN KEY(household_id,document_id) REFERENCES documents.documents(household_id,id),
 FOREIGN KEY(household_id,attachment_id) REFERENCES core.attachments(household_id,id)
);
ALTER TABLE documents.profiles ADD FOREIGN KEY(household_id,document_id,current_version_id) REFERENCES documents.file_versions(household_id,document_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE documents.scan_sets(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),document_id uuid NOT NULL,
 label text NOT NULL CHECK(length(btrim(label)) BETWEEN 1 AND 100),version bigint NOT NULL DEFAULT 1,created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(household_id,id),FOREIGN KEY(household_id,document_id) REFERENCES documents.documents(household_id,id)
);
CREATE TABLE documents.scan_pages(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),set_id uuid NOT NULL,version_id uuid NOT NULL,
 position integer NOT NULL CHECK(position BETWEEN 1 AND 100),page_number integer NOT NULL CHECK(page_number BETWEEN 1 AND 100),
 UNIQUE(set_id,position),FOREIGN KEY(household_id,set_id) REFERENCES documents.scan_sets(household_id,id),
 FOREIGN KEY(household_id,version_id) REFERENCES documents.file_versions(household_id,id)
);
CREATE TABLE documents.deadlines(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),document_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN('return','warranty','expiry','cancellation','payment','custom')),due_on date NOT NULL,
 label text NOT NULL CHECK(length(btrim(label)) BETWEEN 1 AND 200),remind_days integer NOT NULL DEFAULT 7 CHECK(remind_days BETWEEN 0 AND 366),
 source text NOT NULL CHECK(length(btrim(source)) BETWEEN 1 AND 200),confirmed_by uuid NOT NULL REFERENCES core.users(id),
 cancelled_at timestamptz,version bigint NOT NULL DEFAULT 1,created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,id),FOREIGN KEY(household_id,document_id) REFERENCES documents.documents(household_id,id)
);
CREATE INDEX document_deadline_agenda ON documents.deadlines(household_id,due_on,id) WHERE cancelled_at IS NULL;
CREATE TABLE documents.saved_views(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),user_id uuid NOT NULL REFERENCES core.users(id),
 name text NOT NULL CHECK(length(btrim(name)) BETWEEN 1 AND 100),filters jsonb NOT NULL CHECK(jsonb_typeof(filters)='object' AND octet_length(filters::text)<=4000),
 version bigint NOT NULL DEFAULT 1,UNIQUE(household_id,user_id,name)
);
CREATE TABLE documents.rules(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),name text NOT NULL CHECK(length(btrim(name)) BETWEEN 1 AND 100),
 contains_text text NOT NULL CHECK(length(btrim(contains_text)) BETWEEN 2 AND 200),document_type text NOT NULL CHECK(document_type IN('other','invoice','warranty','contract','identity','manual')),
 issuer text NOT NULL DEFAULT '' CHECK(length(issuer)<=200),category text NOT NULL DEFAULT '' CHECK(length(category)<=200),enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL DEFAULT 1,created_by uuid NOT NULL REFERENCES core.users(id)
);
CREATE TABLE documents.processing(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),document_id uuid NOT NULL,version_id uuid,
 operation text NOT NULL CHECK(operation IN('extract','scan')),input jsonb NOT NULL DEFAULT '{}' CHECK(octet_length(input::text)<=16000),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','running','done','failed','cancelled')),progress integer NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100),
 status_code text NOT NULL DEFAULT 'queued' CHECK(length(status_code)<=50),job_id uuid NOT NULL UNIQUE,module_generation bigint NOT NULL,
 requested_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,id),FOREIGN KEY(household_id,document_id) REFERENCES documents.documents(household_id,id),
 FOREIGN KEY(household_id,version_id) REFERENCES documents.file_versions(household_id,id)
);
CREATE INDEX document_processing_pending ON documents.processing(household_id,state,created_at,id);
CREATE TABLE documents.integration_sources(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),adapter text NOT NULL CHECK(adapter IN('watch','imap','paperless')),
 enabled boolean NOT NULL DEFAULT false,approved_by uuid NOT NULL REFERENCES core.users(id),module_generation bigint NOT NULL,
 status_code text NOT NULL DEFAULT 'idle' CHECK(length(status_code)<=50),cursor jsonb NOT NULL DEFAULT '{}' CHECK(octet_length(cursor::text)<=4000),
 failures integer NOT NULL DEFAULT 0 CHECK(failures BETWEEN 0 AND 1000),next_poll_at timestamptz NOT NULL DEFAULT now(),version bigint NOT NULL DEFAULT 1,
 UNIQUE(household_id,id),UNIQUE(household_id,adapter)
);
CREATE TABLE documents.import_receipts(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),source_id uuid NOT NULL,
 remote_key text NOT NULL CHECK(length(remote_key) BETWEEN 1 AND 300),content_sha256 bytea NOT NULL CHECK(octet_length(content_sha256)=32),document_id uuid NOT NULL,
 imported_at timestamptz NOT NULL DEFAULT now(),UNIQUE(source_id,remote_key,content_sha256),
 FOREIGN KEY(household_id,source_id) REFERENCES documents.integration_sources(household_id,id),
 FOREIGN KEY(household_id,document_id) REFERENCES documents.documents(household_id,id)
);
CREATE TABLE documents.command_receipts(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),user_id uuid NOT NULL REFERENCES core.users(id),command_key uuid NOT NULL,
 operation text NOT NULL CHECK(length(operation)<=50),module_generation bigint NOT NULL,request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 response_payload jsonb NOT NULL CHECK(octet_length(response_payload::text)<=131072),created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(household_id,user_id,command_key)
);
CREATE INDEX document_version_hash ON documents.file_versions(household_id,original_sha256,document_id);
CREATE INDEX document_probable_duplicate ON documents.documents(household_id,lower(title),lower(issuer),id) WHERE archived_at IS NULL;
CREATE INDEX document_version_fts ON documents.file_versions USING gin(to_tsvector('german',extracted_text));
GRANT SELECT,INSERT,UPDATE ON ALL TABLES IN SCHEMA documents TO household_app;
REVOKE DELETE ON documents.documents FROM household_app;
GRANT DELETE ON documents.saved_views,documents.scan_pages TO household_app;
REVOKE UPDATE,DELETE ON documents.command_receipts,documents.import_receipts,documents.scan_sets,documents.scan_pages FROM household_app;
INSERT INTO documents.profiles(document_id,household_id,review_state)
 SELECT id,household_id,'unreviewed' FROM documents.documents;
INSERT INTO documents.file_versions(household_id,document_id,attachment_id,ordinal,source,original_sha256,original_size,created_by)
 SELECT a.household_id,d.id,a.id,row_number() OVER(PARTITION BY d.id ORDER BY a.created_at,a.id)::integer,'legacy',a.sha256,a.size_bytes,a.uploaded_by
 FROM core.attachments a JOIN documents.documents d ON d.household_id=a.household_id AND d.id=a.resource_id
 WHERE a.module_id='documents' AND a.resource_type='documents' AND a.state='ready' AND a.size_bytes<=10485760;
UPDATE documents.profiles p SET current_version_id=(SELECT id FROM documents.file_versions v WHERE v.document_id=p.document_id ORDER BY ordinal DESC LIMIT 1);
INSERT INTO documents.deadlines(household_id,document_id,kind,due_on,label,remind_days,source,confirmed_by)
 SELECT household_id,id,'expiry',valid_until,title,remind_days,'Bestehendes geprüftes Ablaufdatum',created_by FROM documents.documents WHERE valid_until IS NOT NULL
 UNION ALL SELECT household_id,id,'warranty',warranty_until,title,remind_days,'Bestehendes geprüftes Garantiedatum',created_by FROM documents.documents WHERE warranty_until IS NOT NULL;
UPDATE core.reminders SET state='cancelled',cancelled_at=clock_timestamp() WHERE module_id='documents' AND state='pending';
INSERT INTO core.reminders(household_id,module_id,module_generation,resource_type,resource_id,kind,scheduled_at,timezone,dedupe_key)
 SELECT x.household_id,'documents',s.data_generation,'documents',x.document_id,'document_expiry',
 (x.due_on::timestamp+interval '9 hours'-make_interval(days=>x.remind_days)) AT TIME ZONE h.timezone,h.timezone,'document-deadline/'||x.id::text||'/legacy'
 FROM documents.deadlines x JOIN core.households h ON h.id=x.household_id JOIN core.module_states s ON s.household_id=x.household_id AND s.module_id='documents'
 JOIN documents.documents d ON d.id=x.document_id AND d.archived_at IS NULL;
CREATE FUNCTION documents.guard_original() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF(to_jsonb(NEW)-ARRAY['extracted_text','text_provider','page_count']) IS DISTINCT FROM(to_jsonb(OLD)-ARRAY['extracted_text','text_provider','page_count']) THEN
  RAISE EXCEPTION 'document original identity is immutable' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION documents.guard_original() FROM PUBLIC,household_app;
CREATE TRIGGER document_original_immutable BEFORE UPDATE ON documents.file_versions FOR EACH ROW EXECUTE FUNCTION documents.guard_original();
CREATE FUNCTION documents.validate_current_original() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM core.attachments a WHERE a.id=NEW.attachment_id AND a.household_id=NEW.household_id
  AND a.module_id='documents' AND a.resource_type='documents' AND a.resource_id=NEW.document_id AND a.state='ready'
  AND a.sha256=NEW.original_sha256 AND a.size_bytes=NEW.original_size) THEN
  RAISE EXCEPTION 'document version requires its exact ready Core original' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION documents.validate_current_original() FROM PUBLIC,household_app;
CREATE TRIGGER document_original_binding BEFORE INSERT ON documents.file_versions FOR EACH ROW EXECUTE FUNCTION documents.validate_current_original();

CREATE FUNCTION documents.expansion_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 WITH relations AS(SELECT t.oid,t.relname FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace
  WHERE n.nspname='documents' AND t.relkind IN('r','p') AND t.relname<>'schema_migrations')
 SELECT jsonb_build_object(
 'relations',(SELECT jsonb_agg(jsonb_build_array(r.relname,c.relowner::regrole::text,c.relkind,c.relpersistence,c.relrowsecurity,c.relforcerowsecurity,c.relacl::text) ORDER BY r.relname)
  FROM relations r JOIN pg_class c ON c.oid=r.oid),
 'columns',(SELECT jsonb_agg(jsonb_build_array(r.relname,a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attidentity,a.attgenerated,coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY r.relname,a.attnum)
  FROM relations r JOIN pg_attribute a ON a.attrelid=r.oid LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attnum>0 AND NOT a.attisdropped),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(r.relname,c.conname,pg_get_constraintdef(c.oid),c.convalidated) ORDER BY r.relname,c.conname)
  FROM relations r JOIN pg_constraint c ON c.conrelid=r.oid),
 'indexes',(SELECT jsonb_agg(jsonb_build_array(r.relname,pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY r.relname,i.indexrelid::regclass::text)
  FROM relations r JOIN pg_index i ON i.indrelid=r.oid),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(r.relname,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled) ORDER BY r.relname,t.tgname)
  FROM relations r JOIN pg_trigger t ON t.tgrelid=r.oid WHERE NOT t.tgisinternal AND t.tgname NOT IN('household_audit_change','household_audit_truncate')),
 'functions',(SELECT jsonb_agg(jsonb_build_array(p.oid::regprocedure::text,encode(sha256(convert_to(pg_get_functiondef(p.oid),'UTF8')),'hex'),p.proacl::text,
  p.proowner::regrole::text,p.prosecdef,p.provolatile,p.proconfig) ORDER BY p.oid::regprocedure::text)
  FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='documents' AND p.proname NOT IN('expansion_catalog','expansion_ready')))
$$;
REVOKE ALL ON FUNCTION documents.expansion_catalog() FROM PUBLIC,household_app;
DO $readiness$
DECLARE expected jsonb;catalog_hash text;catalog_owner text;catalog_acl text;ready_query text;
BEGIN
 expected=documents.expansion_catalog();
 SELECT encode(sha256(convert_to(pg_get_functiondef(p.oid),'UTF8')),'hex'),p.proowner::regrole::text,p.proacl::text
 INTO catalog_hash,catalog_owner,catalog_acl FROM pg_proc p WHERE p.oid='documents.expansion_catalog()'::regprocedure;
 ready_query=format('SELECT documents.expansion_catalog()=%L::jsonb AND (SELECT count(*) FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname=''documents'' AND t.relkind IN(''r'',''p'') AND t.relname<>''schema_migrations'')=12 AND EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid=''documents.expansion_catalog()''::regprocedure AND encode(sha256(convert_to(pg_get_functiondef(p.oid),''UTF8'')),''hex'')=%L AND p.proowner::regrole::text=%L AND p.proacl::text IS NOT DISTINCT FROM %L)',expected::text,catalog_hash,catalog_owner,catalog_acl);
 EXECUTE format('CREATE FUNCTION documents.expansion_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  ready_query);
END $readiness$;
REVOKE ALL ON FUNCTION documents.expansion_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION documents.expansion_ready() TO household_app;
COMMIT;
