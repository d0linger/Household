BEGIN;
CREATE TABLE core.households (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), singleton smallint NOT NULL UNIQUE DEFAULT 1 CHECK(singleton=1),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100), locale text NOT NULL DEFAULT 'de-AT',
 timezone text NOT NULL DEFAULT 'Europe/Vienna', currency_code text NOT NULL DEFAULT 'EUR' CHECK(currency_code='EUR'),
 module_revision bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE core.users (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), username text NOT NULL CHECK(username ~ '^[A-Za-z0-9_.-]{3,64}$'),
 username_key text GENERATED ALWAYS AS(lower(username)) STORED UNIQUE, display_name text NOT NULL CHECK(length(display_name) BETWEEN 1 AND 100),
 email text, password_hash text NOT NULL, must_change_password boolean NOT NULL DEFAULT true,
 auth_version bigint NOT NULL DEFAULT 1, password_changed_at timestamptz NOT NULL DEFAULT now(), disabled_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE core.memberships (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id), user_id uuid NOT NULL REFERENCES core.users(id),
 role text NOT NULL CHECK(role IN('admin','editor','reader')), inactive_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), version bigint NOT NULL DEFAULT 1,
 UNIQUE(household_id,user_id), UNIQUE(household_id,id)
);
CREATE TABLE core.user_preferences (
 household_id uuid NOT NULL REFERENCES core.households(id), user_id uuid NOT NULL REFERENCES core.users(id),
 theme text NOT NULL DEFAULT 'system' CHECK(theme IN('system','light','dark')), locale text,
 default_module_id text, offline_storage_enabled boolean NOT NULL DEFAULT false, updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(household_id,user_id)
);
CREATE TABLE core.module_installations(module_id text PRIMARY KEY CHECK(module_id ~ '^[a-z][a-z0-9_]{1,30}$'),schema_name text NOT NULL UNIQUE,first_installed_at timestamptz NOT NULL DEFAULT now(),last_seen_build text NOT NULL);
CREATE TABLE core.module_states (
 household_id uuid NOT NULL REFERENCES core.households(id), module_id text NOT NULL REFERENCES core.module_installations(module_id),
 state text NOT NULL CHECK(state IN('enabled','disabled','purging','error')), data_generation bigint NOT NULL DEFAULT 1,
 state_version bigint NOT NULL DEFAULT 1,settings jsonb NOT NULL DEFAULT '{}',changed_by uuid REFERENCES core.users(id),changed_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(household_id,module_id)
);
CREATE TABLE core.units(code text PRIMARY KEY,dimension text NOT NULL, label text NOT NULL, factor_to_base numeric(24,12) NOT NULL CHECK(factor_to_base>0));
INSERT INTO core.units VALUES ('piece','count','Stück',1),('g','mass','g',1),('kg','mass','kg',1000),('ml','volume','ml',1),('l','volume','l',1000),('kWh','energy','kWh',1),('m3','volume','m³',1000000),('km','distance','km',1);
CREATE TABLE core.sessions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
 token_hash bytea NOT NULL UNIQUE CHECK(octet_length(token_hash)=32), auth_version bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),last_seen_at timestamptz NOT NULL DEFAULT now(),
 idle_expires_at timestamptz NOT NULL,absolute_expires_at timestamptz NOT NULL,revoked_at timestamptz,
 user_agent text NOT NULL CHECK(length(user_agent)<=512),ip_prefix inet,
 CHECK(idle_expires_at<=absolute_expires_at)
);
CREATE INDEX sessions_user ON core.sessions(user_id,created_at DESC);
CREATE INDEX sessions_expiry ON core.sessions(idle_expires_at);
CREATE TABLE core.auth_challenges(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid REFERENCES core.users(id),purpose text NOT NULL,token_hash bytea NOT NULL UNIQUE CHECK(octet_length(token_hash)=32),payload jsonb NOT NULL DEFAULT '{}',attempts integer NOT NULL DEFAULT 0,created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL,consumed_at timestamptz);
CREATE INDEX auth_challenges_expiry ON core.auth_challenges(expires_at);
CREATE TABLE core.rate_limit_buckets(scope text NOT NULL,subject_hash bytea NOT NULL CHECK(octet_length(subject_hash)=32),window_start timestamptz NOT NULL,window_end timestamptz NOT NULL,attempt_count integer NOT NULL DEFAULT 1 CHECK(attempt_count>0),updated_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(scope,subject_hash,window_start));
CREATE INDEX rate_limit_expiry ON core.rate_limit_buckets(window_end);
CREATE TABLE core.totp_credentials(user_id uuid PRIMARY KEY REFERENCES core.users(id) ON DELETE CASCADE,encrypted_secret bytea NOT NULL,encryption_key_id text NOT NULL,last_accepted_counter bigint,enabled_at timestamptz,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE core.recovery_codes(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),user_id uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,code_hash bytea NOT NULL UNIQUE,created_at timestamptz NOT NULL DEFAULT now(),consumed_at timestamptz);
CREATE TABLE core.audit_log(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,event_id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),household_id uuid REFERENCES core.households(id),actor_user_id uuid,module_id text,resource_type text,resource_id uuid,action text NOT NULL,outcome text NOT NULL,request_id uuid NOT NULL,details jsonb NOT NULL DEFAULT '{}',created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX audit_household_date ON core.audit_log(household_id,created_at DESC);
CREATE INDEX audit_module_date ON core.audit_log(module_id,created_at DESC);
CREATE FUNCTION core.reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF current_user <> 'household_owner' THEN RAISE EXCEPTION 'immutable record' USING ERRCODE='23514'; END IF; IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW; END $$;
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON core.audit_log FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();
CREATE TABLE core.jobs(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),module_id text,module_generation bigint,kind text NOT NULL,payload_version integer NOT NULL DEFAULT 1,payload jsonb NOT NULL DEFAULT '{}',dedupe_key text NOT NULL UNIQUE,state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','running','done','failed','paused')),available_at timestamptz NOT NULL DEFAULT now(),attempts integer NOT NULL DEFAULT 0,max_attempts integer NOT NULL DEFAULT 5,lease_token uuid,lease_until timestamptz,finished_at timestamptz,last_error text,created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX jobs_claim ON core.jobs(state,available_at);
CREATE INDEX jobs_lease ON core.jobs(lease_until) WHERE state='running';
CREATE TABLE core.outbox_events(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),module_id text NOT NULL,module_generation bigint NOT NULL,event_type text NOT NULL,event_version integer NOT NULL DEFAULT 1,actor_user_id uuid,payload jsonb NOT NULL,dedupe_key text NOT NULL UNIQUE,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE core.event_deliveries(event_id uuid NOT NULL REFERENCES core.outbox_events(id) ON DELETE CASCADE,consumer_id text NOT NULL,state text NOT NULL DEFAULT 'pending',attempts integer NOT NULL DEFAULT 0,delivered_at timestamptz,last_error text,PRIMARY KEY(event_id,consumer_id));
CREATE TABLE core.idempotency_records(household_id uuid NOT NULL REFERENCES core.households(id),user_id uuid NOT NULL REFERENCES core.users(id),operation text NOT NULL,key uuid NOT NULL,request_hash bytea NOT NULL,module_id text NOT NULL,module_generation bigint NOT NULL,response_status integer NOT NULL,response_payload jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL DEFAULT now()+interval '30 days',PRIMARY KEY(household_id,user_id,operation,key));
CREATE INDEX idempotency_expiry ON core.idempotency_records(expires_at);
CREATE TABLE core.resource_links(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),source_module text NOT NULL,source_type text NOT NULL,source_id uuid NOT NULL,target_module text NOT NULL,target_type text NOT NULL,target_id uuid NOT NULL,relation text NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation));
CREATE TABLE core.module_purge_requests(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),module_id text NOT NULL,expected_generation bigint NOT NULL,requested_by uuid NOT NULL REFERENCES core.users(id),state text NOT NULL DEFAULT 'pending',confirmed_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL DEFAULT now()+interval '30 minutes',finished_at timestamptz,backup_reference text NOT NULL);
CREATE TABLE core.attachments(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),module_id text NOT NULL,module_generation bigint NOT NULL,resource_type text NOT NULL,resource_id uuid NOT NULL,storage_key text NOT NULL UNIQUE,original_name text NOT NULL,media_type text NOT NULL,size_bytes bigint NOT NULL CHECK(size_bytes>0),sha256 bytea NOT NULL CHECK(octet_length(sha256)=32),state text NOT NULL DEFAULT 'ready',uploaded_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(household_id,id));
CREATE INDEX attachments_resource ON core.attachments(household_id,module_id,resource_type,resource_id);
CREATE TABLE core.attachment_derivatives(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),attachment_id uuid NOT NULL REFERENCES core.attachments(id) ON DELETE CASCADE,variant text NOT NULL,storage_key text NOT NULL UNIQUE,media_type text NOT NULL,width integer,height integer,size_bytes bigint NOT NULL,sha256 bytea NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(attachment_id,variant));
CREATE TABLE core.capture_jobs(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),module_id text NOT NULL,module_generation bigint NOT NULL,source_attachment_id uuid NOT NULL REFERENCES core.attachments(id),target_schema text NOT NULL,schema_version integer NOT NULL DEFAULT 1,provider text NOT NULL DEFAULT 'manual',state text NOT NULL DEFAULT 'review',extracted_values jsonb,confidence jsonb,confirmed_values jsonb,confirmed_by uuid REFERENCES core.users(id),confirmed_at timestamptz,result_resource_id uuid,created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL DEFAULT now()+interval '7 days');
CREATE TABLE core.reminders(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),module_id text NOT NULL,module_generation bigint NOT NULL,resource_type text NOT NULL,resource_id uuid NOT NULL,recipient_user_id uuid REFERENCES core.users(id),kind text NOT NULL,scheduled_at timestamptz NOT NULL,timezone text NOT NULL,dedupe_key text NOT NULL UNIQUE,state text NOT NULL DEFAULT 'pending',delivered_at timestamptz,cancelled_at timestamptz,created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX reminders_due ON core.reminders(state,scheduled_at);
CREATE TABLE core.notifications(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),user_id uuid NOT NULL REFERENCES core.users(id),module_id text,kind text NOT NULL,title text NOT NULL,target_path text,read_at timestamptz,dismissed_at timestamptz,dedupe_key text NOT NULL UNIQUE,created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX notifications_user ON core.notifications(user_id,read_at,created_at DESC);
CREATE TABLE core.push_subscriptions(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),user_id uuid NOT NULL REFERENCES core.users(id),endpoint_hash bytea NOT NULL UNIQUE,encrypted_subscription bytea NOT NULL,encryption_key_id text NOT NULL,disabled_at timestamptz,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE core.search_documents(household_id uuid NOT NULL REFERENCES core.households(id),module_id text NOT NULL,module_generation bigint NOT NULL,resource_type text NOT NULL,resource_id uuid NOT NULL,title text NOT NULL,body text NOT NULL,search_vector tsvector GENERATED ALWAYS AS(to_tsvector('german',coalesce(title,'')||' '||coalesce(body,''))) STORED,updated_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(household_id,module_id,resource_type,resource_id));
CREATE INDEX search_vector ON core.search_documents USING gin(search_vector);
CREATE TABLE core.import_batches(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),module_id text NOT NULL,module_generation bigint NOT NULL,entity text NOT NULL,state text NOT NULL DEFAULT 'preview',row_count integer NOT NULL,valid_count integer NOT NULL,error_count integer NOT NULL,confirmed_by uuid REFERENCES core.users(id),confirmed_at timestamptz,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE core.import_rows(batch_id uuid NOT NULL REFERENCES core.import_batches(id) ON DELETE CASCADE,row_number integer NOT NULL,normalized_values jsonb NOT NULL,validation_errors jsonb NOT NULL,state text NOT NULL DEFAULT 'pending',result_resource_id uuid,PRIMARY KEY(batch_id,row_number));
CREATE TABLE core.backup_runs(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),started_at timestamptz NOT NULL DEFAULT now(),finished_at timestamptz,state text NOT NULL,application_version text NOT NULL,schema_manifest jsonb NOT NULL DEFAULT '{}',repository_reference text NOT NULL,snapshot_reference text,verified_at timestamptz,failure_reason text);
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION core.reject_mutation() FROM PUBLIC;
GRANT USAGE ON SCHEMA core TO household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA core TO household_app;
REVOKE UPDATE,DELETE ON core.audit_log FROM household_app;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA core TO household_app;
COMMIT;
