BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

DO $predecessor$
DECLARE snapshot jsonb;
BEGIN
 IF NOT (SELECT count(*)=1 AND bool_and(version=31 AND NOT dirty) FROM core.schema_migrations)
 OR NOT core.audit_ready() OR NOT core.finance_ready() OR NOT core.planning_extensions_ready()
 OR NOT fuel.module_ready() OR NOT pantry.prepared_capture_ready() OR NOT mealplan.expansion_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>121 THEN
  RAISE EXCEPTION 'Chores workspace requires the accepted complete Core31 predecessor';
 END IF;
 SELECT jsonb_build_object('finance_catalog',core.finance_catalog(),
 'audit_policies',(SELECT jsonb_agg(to_jsonb(policy) ORDER BY schema_name,table_name) FROM core.audit_policies policy),
 'relations',(SELECT jsonb_agg(jsonb_build_array(n.nspname,t.relname,t.relkind,t.relowner::regrole::text) ORDER BY n.nspname,t.relname)
 FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace WHERE t.relkind IN('r','p')
 AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')),
 'audit_triggers',(SELECT jsonb_agg(jsonb_build_array(policy.schema_name,policy.table_name,tr.tgname,pg_get_triggerdef(tr.oid),tr.tgenabled)
 ORDER BY policy.schema_name,policy.table_name,tr.tgname) FROM core.audit_policies policy
 JOIN pg_trigger tr ON tr.tgrelid=to_regclass(format('%I.%I',policy.schema_name,policy.table_name))
 AND tr.tgname IN('household_audit_change','household_audit_truncate')),
 'functions',jsonb_object_agg(p.oid::regprocedure::text,
  jsonb_build_object('definition',pg_get_functiondef(p.oid),'owner',p.proowner::regrole::text,'acl',p.proacl::text)))
 INTO snapshot FROM pg_proc p WHERE p.oid IN('core.audit_ready()'::regprocedure,'core.finance_ready()'::regprocedure,
  'core.finance_producers_ready()'::regprocedure,'core.planning_extensions_ready()'::regprocedure,
  'fuel.overview_ready()'::regprocedure,'fuel.module_ready()'::regprocedure,
  'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure,'pantry.prepared_capture_ready()'::regprocedure);
 EXECUTE format('CREATE FUNCTION chores.predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION chores.predecessor_catalog() FROM PUBLIC,household_app;

ALTER TABLE chores.task_templates DROP CONSTRAINT task_templates_repeat_days_check;
ALTER TABLE chores.task_templates ADD CONSTRAINT task_templates_repeat_days_check CHECK(repeat_days BETWEEN 0 AND 366);

CREATE TABLE chores.areas(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),
 name text NOT NULL CHECK(length(btrim(name)) BETWEEN 1 AND 100),position integer NOT NULL DEFAULT 0 CHECK(position BETWEEN 0 AND 10000),
 archived_at timestamptz,created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,id)
);
CREATE UNIQUE INDEX chore_active_area_name ON chores.areas(household_id,lower(name)) WHERE archived_at IS NULL;

CREATE TABLE chores.template_versions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),template_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),title text NOT NULL,
 description text NOT NULL DEFAULT '',materials text[] NOT NULL DEFAULT '{}' CHECK(cardinality(materials)<=20 AND octet_length(materials::text)<=8000),
 area_id uuid,area_name text CHECK(length(area_name)<=100),estimated_minutes integer CHECK(estimated_minutes BETWEEN 1 AND 1440),
 difficulty integer CHECK(difficulty BETWEEN 1 AND 5),priority integer NOT NULL DEFAULT 1 CHECK(priority BETWEEN 0 AND 3),
 rule_kind text NOT NULL CHECK(rule_kind IN('once','days','weekly','monthly')),interval_value integer NOT NULL DEFAULT 1 CHECK(interval_value BETWEEN 1 AND 366),
 anchor_kind text NOT NULL DEFAULT 'scheduled' CHECK(anchor_kind IN('scheduled','completion')),
 starts_on date CHECK(starts_on BETWEEN DATE '1900-01-01' AND DATE '2200-12-31'),local_time time NOT NULL DEFAULT '09:00',
 timezone text NOT NULL,weekdays integer[] NOT NULL DEFAULT '{}',months integer[] NOT NULL DEFAULT '{}',
 month_day integer CHECK(month_day BETWEEN 1 AND 31),ends_on date,effective_at timestamptz,
 assignment_kind text NOT NULL DEFAULT 'fixed' CHECK(assignment_kind IN('fixed','rotation','unassigned')),
 assignee_id uuid,legacy_capture boolean NOT NULL DEFAULT false,dst_policy text NOT NULL DEFAULT 'civil-v1/forward-gap/early-fold',
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,id),UNIQUE(household_id,template_id,id),UNIQUE(template_id,revision),
 FOREIGN KEY(household_id,template_id) REFERENCES chores.task_templates(household_id,id),
 FOREIGN KEY(household_id,area_id) REFERENCES chores.areas(household_id,id),
 FOREIGN KEY(household_id,assignee_id) REFERENCES core.memberships(household_id,id),
 CHECK(legacy_capture OR length(btrim(title)) BETWEEN 1 AND 200),CHECK(legacy_capture OR length(description)<=10000),
 CHECK(legacy_capture OR length(timezone) BETWEEN 1 AND 100),
 CHECK(legacy_capture OR dst_policy='civil-v1/forward-gap/early-fold'),
 CHECK(anchor_kind<>'completion' OR rule_kind='days'),CHECK(ends_on IS NULL OR starts_on IS NOT NULL AND ends_on>=starts_on),
 CHECK(cardinality(weekdays)<=7 AND weekdays<@ARRAY[1,2,3,4,5,6,7]),
 CHECK(cardinality(months)<=12 AND months<@ARRAY[1,2,3,4,5,6,7,8,9,10,11,12]),
 CHECK(rule_kind<>'weekly' OR cardinality(weekdays)>0),CHECK(rule_kind<>'monthly' OR month_day IS NOT NULL)
);
CREATE INDEX chore_version_effective ON chores.template_versions(template_id,revision DESC) INCLUDE(effective_at);

CREATE TABLE chores.series_profiles(
 household_id uuid NOT NULL REFERENCES core.households(id),template_id uuid PRIMARY KEY,current_version_id uuid NOT NULL,
 managed boolean NOT NULL DEFAULT false,paused boolean NOT NULL DEFAULT false,paused_until date,paused_at timestamptz,resume_at timestamptz,
 materialize_from timestamptz,resume_anchor_at timestamptz,
 review_required boolean NOT NULL DEFAULT false,review_reason text NOT NULL DEFAULT '' CHECK(length(review_reason)<=200),
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,template_id),
 FOREIGN KEY(household_id,template_id,current_version_id) REFERENCES chores.template_versions(household_id,template_id,id),
 CHECK(paused=(paused_at IS NOT NULL)),CHECK((paused_until IS NULL)=(resume_at IS NULL)),
 CHECK(paused OR resume_at IS NULL),CHECK(resume_at IS NULL OR resume_at>paused_at)
);

CREATE TABLE chores.version_checklist_items(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL,version_id uuid NOT NULL,item_key uuid NOT NULL DEFAULT gen_random_uuid(),
 title text NOT NULL CHECK(length(btrim(title)) BETWEEN 1 AND 300),position integer NOT NULL CHECK(position BETWEEN 0 AND 49),required boolean NOT NULL DEFAULT true,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(version_id,item_key),UNIQUE(version_id,position),UNIQUE(household_id,id),
 FOREIGN KEY(household_id,version_id) REFERENCES chores.template_versions(household_id,id)
);

CREATE TABLE chores.version_dependencies(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL,version_id uuid NOT NULL,template_id uuid NOT NULL,prerequisite_id uuid NOT NULL,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(version_id,prerequisite_id),CHECK(template_id<>prerequisite_id),
 FOREIGN KEY(household_id,template_id,version_id) REFERENCES chores.template_versions(household_id,template_id,id),
 FOREIGN KEY(household_id,prerequisite_id) REFERENCES chores.task_templates(household_id,id)
);

CREATE TABLE chores.rotation_members(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL,version_id uuid NOT NULL,membership_id uuid NOT NULL,
 position integer NOT NULL CHECK(position BETWEEN 0 AND 31),eligible boolean NOT NULL DEFAULT true,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(version_id,membership_id),UNIQUE(version_id,position),
 FOREIGN KEY(household_id,version_id) REFERENCES chores.template_versions(household_id,id),
 FOREIGN KEY(household_id,membership_id) REFERENCES core.memberships(household_id,id)
);

CREATE TABLE chores.absences(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),membership_id uuid NOT NULL,
 starts_on date NOT NULL,ends_on date NOT NULL,substitute_id uuid,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,id),
 FOREIGN KEY(household_id,membership_id) REFERENCES core.memberships(household_id,id),
 FOREIGN KEY(household_id,substitute_id) REFERENCES core.memberships(household_id,id),
 CHECK(ends_on>=starts_on AND ends_on-starts_on<=366),CHECK(substitute_id IS NULL OR substitute_id<>membership_id)
);
CREATE INDEX chore_absence_window ON chores.absences(household_id,membership_id,starts_on,ends_on);

CREATE TABLE chores.occurrence_details(
 occurrence_id uuid PRIMARY KEY,household_id uuid NOT NULL,template_id uuid NOT NULL,template_version_id uuid NOT NULL,
 logical_key text NOT NULL CHECK(length(logical_key) BETWEEN 1 AND 240),original_due_at timestamptz NOT NULL,
 scheduled_on date,local_time time,timezone text NOT NULL,
 anchor_kind text NOT NULL CHECK(anchor_kind IN('scheduled','completion')),managed boolean NOT NULL DEFAULT false,
 lifecycle_state text NOT NULL DEFAULT 'open' CHECK(lifecycle_state IN('open','completed','skipped')),
 assignment_reason text NOT NULL DEFAULT 'legacy' CHECK(length(assignment_reason)<=300),assignment_snapshot jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(assignment_snapshot)='object'),
 overrides jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(overrides)='object' AND octet_length(overrides::text)<=16000
 AND overrides-ARRAY['title','description']='{}'::jsonb
 AND (NOT(overrides?'title') OR jsonb_typeof(overrides->'title')='string' AND length(btrim(overrides->>'title')) BETWEEN 1 AND 200)
 AND (NOT(overrides?'description') OR jsonb_typeof(overrides->'description')='string' AND length(overrides->>'description')<=10000)),
 claimed_by uuid,claim_started_at timestamptz,claim_expires_at timestamptz,
 skipped_reason text NOT NULL DEFAULT '' CHECK(length(skipped_reason)<=200),skipped_at timestamptz,skipped_by uuid REFERENCES core.users(id),skip_anchor_at timestamptz,
 generated_by text NOT NULL DEFAULT 'legacy' CHECK(generated_by IN('legacy','user','worker')),
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,occurrence_id),UNIQUE(template_id,logical_key),
 FOREIGN KEY(household_id,occurrence_id) REFERENCES chores.task_occurrences(household_id,id),
 FOREIGN KEY(household_id,template_id,template_version_id) REFERENCES chores.template_versions(household_id,template_id,id),
 FOREIGN KEY(household_id,claimed_by) REFERENCES core.memberships(household_id,id),
 CHECK(generated_by='legacy' OR length(timezone) BETWEEN 1 AND 100),
 CHECK((claimed_by IS NULL)=(claim_expires_at IS NULL) AND (claimed_by IS NULL)=(claim_started_at IS NULL)),
 CHECK(claim_expires_at IS NULL OR claim_expires_at>claim_started_at AND claim_expires_at-claim_started_at<=INTERVAL '24 hours')
);
CREATE UNIQUE INDEX chore_one_completion_successor ON chores.occurrence_details(household_id,template_id)
 WHERE managed AND anchor_kind='completion' AND lifecycle_state='open';
CREATE INDEX chore_template_timeline ON chores.task_occurrences(household_id,template_id,due_at,id);
CREATE INDEX chore_person_due ON chores.task_occurrences(household_id,assignee_id,state,due_at,id);
CREATE INDEX chore_claims ON chores.occurrence_details(household_id,claimed_by,claim_expires_at) WHERE claimed_by IS NOT NULL;

CREATE TABLE chores.occurrence_checklist_items(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL,occurrence_id uuid NOT NULL,item_key uuid NOT NULL,
 title text NOT NULL CHECK(length(btrim(title)) BETWEEN 1 AND 300),position integer NOT NULL CHECK(position BETWEEN 0 AND 49),required boolean NOT NULL,
 done boolean NOT NULL DEFAULT false,done_at timestamptz,done_by uuid REFERENCES core.users(id),
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(occurrence_id,item_key),UNIQUE(occurrence_id,position),
 FOREIGN KEY(household_id,occurrence_id) REFERENCES chores.task_occurrences(household_id,id),
 CHECK(done=(done_at IS NOT NULL) AND done=(done_by IS NOT NULL))
);

CREATE TABLE chores.completions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL,occurrence_id uuid NOT NULL UNIQUE,template_id uuid NOT NULL,
 template_version_id uuid NOT NULL,completed_at timestamptz NOT NULL,actor_user_id uuid REFERENCES core.users(id),actor_membership_id uuid,
 actual_minutes integer CHECK(actual_minutes BETWEEN 0 AND 1440),snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=65536),
 legacy_capture boolean NOT NULL DEFAULT false,created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,id),FOREIGN KEY(household_id,occurrence_id) REFERENCES chores.task_occurrences(household_id,id),
 FOREIGN KEY(household_id,template_id,template_version_id) REFERENCES chores.template_versions(household_id,template_id,id),
 FOREIGN KEY(household_id,actor_membership_id) REFERENCES core.memberships(household_id,id),
 CHECK(legacy_capture OR actor_user_id IS NOT NULL AND actor_membership_id IS NOT NULL)
);
CREATE INDEX chore_completion_history ON chores.completions(household_id,completed_at DESC,id DESC);

CREATE TABLE chores.member_preferences(
 household_id uuid NOT NULL REFERENCES core.households(id),user_id uuid NOT NULL REFERENCES core.users(id),
 daily_minutes integer NOT NULL DEFAULT 60 CHECK(daily_minutes BETWEEN 0 AND 1440),max_difficulty integer CHECK(max_difficulty BETWEEN 1 AND 5),
 notification_mode text NOT NULL DEFAULT 'legacy' CHECK(notification_mode IN('off','legacy','digest')),
 digest_enabled boolean NOT NULL DEFAULT false,digest_minute integer NOT NULL DEFAULT 540 CHECK(digest_minute BETWEEN 0 AND 1439),
 quiet_start integer CHECK(quiet_start BETWEEN 0 AND 1439),quiet_end integer CHECK(quiet_end BETWEEN 0 AND 1439),
 timezone text NOT NULL CHECK(length(timezone) BETWEEN 1 AND 100),escalation_enabled boolean NOT NULL DEFAULT false,
 escalation_after_days integer NOT NULL DEFAULT 3 CHECK(escalation_after_days BETWEEN 1 AND 30),escalation_recipient_id uuid,
 allow_escalations boolean NOT NULL DEFAULT false,created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),version bigint NOT NULL DEFAULT 1 CHECK(version>0),PRIMARY KEY(household_id,user_id),
 FOREIGN KEY(household_id,escalation_recipient_id) REFERENCES core.memberships(household_id,id),
 CHECK((quiet_start IS NULL)=(quiet_end IS NULL)),CHECK(quiet_start IS NULL OR quiet_start<>quiet_end),
 CHECK(NOT escalation_enabled OR escalation_recipient_id IS NOT NULL),CHECK(digest_enabled=(notification_mode='digest'))
);

CREATE TABLE chores.calendar_feeds(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),user_id uuid NOT NULL REFERENCES core.users(id),
 issuer_membership_id uuid NOT NULL,issuer_role text NOT NULL CHECK(issuer_role IN('admin','editor','reader')),
 token_hash bytea NOT NULL UNIQUE CHECK(octet_length(token_hash)=32),auth_version bigint NOT NULL CHECK(auth_version>0),module_generation bigint NOT NULL CHECK(module_generation>0),
 scope text NOT NULL DEFAULT 'mine' CHECK(scope IN('mine','household')),days_back integer NOT NULL DEFAULT 7 CHECK(days_back BETWEEN 0 AND 31),
 days_ahead integer NOT NULL DEFAULT 90 CHECK(days_ahead BETWEEN 1 AND 90),expires_at timestamptz NOT NULL,revoked_at timestamptz,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),CHECK(expires_at>created_at AND expires_at-created_at<=INTERVAL '366 days'),
 FOREIGN KEY(household_id,issuer_membership_id) REFERENCES core.memberships(household_id,id)
);

CREATE TABLE chores.reminder_deliveries(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),module_generation bigint NOT NULL CHECK(module_generation>0),
 recipient_user_id uuid NOT NULL REFERENCES core.users(id),local_day date NOT NULL,stage text NOT NULL CHECK(stage IN('digest','escalation')),
 source_key text NOT NULL CHECK(length(source_key) BETWEEN 1 AND 200),notification_id uuid,delivered_at timestamptz NOT NULL DEFAULT now(),
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,module_generation,recipient_user_id,local_day,stage,source_key)
);

CREATE TABLE chores.schedule_state(
 household_id uuid NOT NULL REFERENCES core.households(id),template_id uuid PRIMARY KEY,template_version_id uuid NOT NULL,module_generation bigint NOT NULL CHECK(module_generation>0),
 generated_through timestamptz,next_check_at timestamptz NOT NULL DEFAULT now(),last_run_at timestamptz,
 backlog boolean NOT NULL DEFAULT false,last_error text NOT NULL DEFAULT '' CHECK(length(last_error)<=500),
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 FOREIGN KEY(household_id,template_id,template_version_id) REFERENCES chores.template_versions(household_id,template_id,id)
);
CREATE INDEX chore_schedule_due ON chores.schedule_state(next_check_at,household_id,template_id);

GRANT SELECT,INSERT,UPDATE,DELETE ON chores.areas,chores.series_profiles,chores.absences,chores.occurrence_details,
 chores.occurrence_checklist_items,chores.member_preferences,chores.calendar_feeds,chores.schedule_state TO household_app;
GRANT SELECT,INSERT ON chores.template_versions,chores.version_checklist_items,chores.version_dependencies,
 chores.rotation_members,chores.completions,chores.reminder_deliveries TO household_app;
REVOKE TRUNCATE ON ALL TABLES IN SCHEMA chores FROM household_app;

-- Upgrade captures what is actually known now. It does not assert that the
-- current title or membership display name was already present at completion.
INSERT INTO chores.template_versions(household_id,template_id,revision,title,description,rule_kind,interval_value,
 starts_on,local_time,timezone,assignment_kind,assignee_id,legacy_capture,dst_policy,created_by)
 SELECT t.household_id,t.id,1,t.title,t.description,CASE WHEN coalesce(t.repeat_days,0)>0 THEN 'days' ELSE 'once' END,
 greatest(coalesce(t.repeat_days,1),1),
 CASE WHEN zone.valid AND first_due.due_at IS NOT NULL AND (first_due.due_at AT TIME ZONE t.timezone)::date BETWEEN DATE '1900-01-01' AND DATE '2200-12-31'
 THEN (first_due.due_at AT TIME ZONE t.timezone)::date END,
 CASE WHEN zone.valid AND first_due.due_at IS NOT NULL THEN (first_due.due_at AT TIME ZONE t.timezone)::time ELSE TIME '09:00' END,
 t.timezone,CASE WHEN t.assignee_id IS NULL THEN 'unassigned' ELSE 'fixed' END,t.assignee_id,true,'legacy-postgresql',t.created_by
 FROM chores.task_templates t
 CROSS JOIN LATERAL(SELECT EXISTS(SELECT 1 FROM pg_timezone_names WHERE name=t.timezone) valid) zone
 LEFT JOIN LATERAL(SELECT min(o.due_at) due_at FROM chores.task_occurrences o WHERE o.household_id=t.household_id AND o.template_id=t.id) first_due ON true;

INSERT INTO chores.series_profiles(household_id,template_id,current_version_id,review_required,review_reason,created_by)
 SELECT v.household_id,v.template_id,v.id,v.starts_on IS NULL OR v.rule_kind<>'once',
 CASE WHEN v.starts_on IS NULL THEN 'legacy_start_or_timezone_unknown' WHEN v.rule_kind<>'once' THEN 'review_before_rolling_schedule' ELSE '' END,v.created_by
 FROM chores.template_versions v;

INSERT INTO chores.occurrence_details(occurrence_id,household_id,template_id,template_version_id,logical_key,original_due_at,
 scheduled_on,local_time,timezone,anchor_kind,lifecycle_state,assignment_snapshot,created_by)
 SELECT o.id,o.household_id,o.template_id,p.current_version_id,'legacy/'||o.id::text,o.due_at,
 CASE WHEN zone.valid THEN (o.due_at AT TIME ZONE t.timezone)::date END,
 CASE WHEN zone.valid THEN (o.due_at AT TIME ZONE t.timezone)::time END,t.timezone,'scheduled',o.state,
 jsonb_build_object('membership_id',o.assignee_id,'captured_at_upgrade',true),o.created_by
 FROM chores.task_occurrences o JOIN chores.task_templates t ON t.household_id=o.household_id AND t.id=o.template_id
 JOIN chores.series_profiles p ON p.household_id=t.household_id AND p.template_id=t.id
 CROSS JOIN LATERAL(SELECT EXISTS(SELECT 1 FROM pg_timezone_names WHERE name=t.timezone) valid) zone;

INSERT INTO chores.completions(household_id,occurrence_id,template_id,template_version_id,completed_at,actor_user_id,
 actor_membership_id,snapshot,legacy_capture,created_by)
 SELECT o.household_id,o.id,o.template_id,d.template_version_id,o.completed_at,o.completed_by,m.id,
 jsonb_build_object('captured_at_upgrade',true,'original_template_history_unknown',true,'duration_unknown',true),true,o.created_by
 FROM chores.task_occurrences o JOIN chores.occurrence_details d ON d.occurrence_id=o.id
 LEFT JOIN core.memberships m ON m.household_id=o.household_id AND m.user_id=o.completed_by
 WHERE o.state='completed';

CREATE FUNCTION chores.lock_write() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE household uuid;
BEGIN
 IF current_setting('transaction_isolation') NOT IN('read committed','serializable') THEN
  RAISE EXCEPTION 'chores writes require the serialized supported transaction mode' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN household=OLD.household_id;ELSE household=NEW.household_id;END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('household/chores/'||household::text,0));
 IF TG_OP='UPDATE' AND NEW.household_id IS DISTINCT FROM OLD.household_id THEN
  RAISE EXCEPTION 'chore household identity is immutable' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION chores.lock_write() FROM PUBLIC,household_app;

CREATE FUNCTION chores.guard_history() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 RAISE EXCEPTION 'chore history is immutable; use a future revision or separate maintenance purge' USING ERRCODE='23514';
END $$;
REVOKE ALL ON FUNCTION chores.guard_history() FROM PUBLIC,household_app;

CREATE FUNCTION chores.capture_template() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE profile chores.series_profiles%ROWTYPE;captured chores.template_versions%ROWTYPE;next_revision bigint;new_version uuid;start_day date;clock_time time;valid_zone boolean;
BEGIN
 SELECT * INTO profile FROM chores.series_profiles WHERE household_id=NEW.household_id AND template_id=NEW.id;
 IF TG_OP='INSERT' AND NEW.active AND(SELECT count(*) FROM chores.task_templates WHERE household_id=NEW.household_id AND active)>500 THEN
  RAISE EXCEPTION 'at most 500 active chore series; archive unused series first' USING ERRCODE='23514';
 END IF;
 IF FOUND AND profile.managed THEN
  SELECT * INTO captured FROM chores.template_versions WHERE id=profile.current_version_id;
  IF captured.title IS DISTINCT FROM NEW.title OR captured.description IS DISTINCT FROM NEW.description
   OR captured.timezone IS DISTINCT FROM NEW.timezone OR captured.assignee_id IS DISTINCT FROM NEW.assignee_id
   OR captured.rule_kind='days' AND captured.interval_value IS DISTINCT FROM NEW.repeat_days
   OR captured.rule_kind='once' AND coalesce(NEW.repeat_days,0)<>0
   OR captured.rule_kind IN('weekly','monthly') AND NEW.repeat_days IS NOT NULL THEN
   RAISE EXCEPTION 'edit the managed chore through its reviewed workspace version' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='UPDATE' AND (NEW.title,NEW.description,NEW.assignee_id,NEW.repeat_days,NEW.timezone)
  IS NOT DISTINCT FROM (OLD.title,OLD.description,OLD.assignee_id,OLD.repeat_days,OLD.timezone) THEN RETURN NEW;END IF;
 SELECT EXISTS(SELECT 1 FROM pg_timezone_names WHERE name=NEW.timezone) INTO valid_zone;
 IF valid_zone THEN
  SELECT (min(due_at) AT TIME ZONE NEW.timezone)::date,(min(due_at) AT TIME ZONE NEW.timezone)::time
   INTO start_day,clock_time FROM chores.task_occurrences WHERE household_id=NEW.household_id AND template_id=NEW.id;
 END IF;
 IF start_day NOT BETWEEN DATE '1900-01-01' AND DATE '2200-12-31' THEN start_day=NULL;END IF;
 SELECT coalesce(max(revision),0)+1 INTO next_revision FROM chores.template_versions WHERE template_id=NEW.id;
 INSERT INTO chores.template_versions(household_id,template_id,revision,title,description,rule_kind,interval_value,
  starts_on,local_time,timezone,assignment_kind,assignee_id,legacy_capture,dst_policy,created_by)
 VALUES(NEW.household_id,NEW.id,next_revision,NEW.title,NEW.description,CASE WHEN coalesce(NEW.repeat_days,0)>0 THEN 'days' ELSE 'once' END,
  greatest(coalesce(NEW.repeat_days,1),1),start_day,coalesce(clock_time,TIME '09:00'),NEW.timezone,
  CASE WHEN NEW.assignee_id IS NULL THEN 'unassigned' ELSE 'fixed' END,NEW.assignee_id,true,'legacy-postgresql',NEW.created_by) RETURNING id INTO new_version;
 INSERT INTO chores.series_profiles(household_id,template_id,current_version_id,review_required,review_reason,created_by)
 VALUES(NEW.household_id,NEW.id,new_version,start_day IS NULL OR coalesce(NEW.repeat_days,0)>0,
  CASE WHEN start_day IS NULL THEN 'legacy_start_or_timezone_unknown' WHEN coalesce(NEW.repeat_days,0)>0 THEN 'review_before_rolling_schedule' ELSE '' END,NEW.created_by)
 ON CONFLICT(template_id) DO UPDATE SET current_version_id=EXCLUDED.current_version_id,review_required=EXCLUDED.review_required,
  review_reason=EXCLUDED.review_reason,updated_at=now(),version=chores.series_profiles.version+1;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION chores.capture_template() FROM PUBLIC,household_app;

CREATE FUNCTION chores.capture_occurrence() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE profile chores.series_profiles%ROWTYPE;captured chores.template_versions%ROWTYPE;valid_zone boolean;new_version uuid;
BEGIN
 SELECT * INTO profile FROM chores.series_profiles WHERE household_id=NEW.household_id AND template_id=NEW.template_id;
 SELECT * INTO captured FROM chores.template_versions WHERE id=profile.current_version_id;
 IF profile.managed THEN
  IF profile.paused OR NOT(SELECT active FROM chores.task_templates WHERE household_id=NEW.household_id AND id=NEW.template_id) THEN
   RAISE EXCEPTION 'paused or archived chore series cannot create occurrences' USING ERRCODE='23514';
  END IF;
  SELECT * INTO captured FROM chores.template_versions WHERE household_id=NEW.household_id AND template_id=NEW.template_id
   AND NOT legacy_capture AND effective_at<=NEW.due_at ORDER BY revision DESC LIMIT 1;
  IF captured.id IS NULL OR profile.materialize_from IS NULL OR NEW.due_at<profile.materialize_from
   OR position('v/'||captured.id::text||'/' in NEW.occurrence_key)<>1 THEN
   RAISE EXCEPTION 'managed occurrence requires its exact effective reviewed version' USING ERRCODE='23514';
  END IF;
 END IF;
 IF captured.id IS NULL THEN RAISE EXCEPTION 'chore template snapshot missing' USING ERRCODE='23514';END IF;
 SELECT EXISTS(SELECT 1 FROM pg_timezone_names WHERE name=captured.timezone) INTO valid_zone;
 IF NOT profile.managed AND captured.starts_on IS NULL AND valid_zone
  AND (NEW.due_at AT TIME ZONE captured.timezone)::date BETWEEN DATE '1900-01-01' AND DATE '2200-12-31'
  AND NOT EXISTS(SELECT 1 FROM chores.occurrence_details WHERE household_id=NEW.household_id AND template_id=NEW.template_id) THEN
  INSERT INTO chores.template_versions(household_id,template_id,revision,title,description,rule_kind,interval_value,starts_on,
   local_time,timezone,assignment_kind,assignee_id,legacy_capture,dst_policy,created_by)
  VALUES(captured.household_id,captured.template_id,captured.revision+1,captured.title,captured.description,captured.rule_kind,
   captured.interval_value,(NEW.due_at AT TIME ZONE captured.timezone)::date,(NEW.due_at AT TIME ZONE captured.timezone)::time,
   captured.timezone,captured.assignment_kind,captured.assignee_id,true,'legacy-postgresql',captured.created_by) RETURNING id INTO new_version;
  UPDATE chores.series_profiles SET current_version_id=new_version,review_required=captured.rule_kind<>'once',
   review_reason=CASE WHEN captured.rule_kind<>'once' THEN 'review_before_rolling_schedule' ELSE '' END,updated_at=now(),version=version+1
   WHERE template_id=NEW.template_id;
  profile.current_version_id=new_version;
  SELECT * INTO captured FROM chores.template_versions WHERE id=new_version;
 END IF;
 INSERT INTO chores.occurrence_details(occurrence_id,household_id,template_id,template_version_id,logical_key,original_due_at,
  scheduled_on,local_time,timezone,anchor_kind,managed,lifecycle_state,assignment_snapshot,generated_by,created_by)
 VALUES(NEW.id,NEW.household_id,NEW.template_id,captured.id,CASE WHEN profile.managed THEN NEW.occurrence_key ELSE 'legacy/'||NEW.id::text END,
  NEW.due_at,CASE WHEN valid_zone THEN (NEW.due_at AT TIME ZONE captured.timezone)::date END,
  CASE WHEN valid_zone THEN (NEW.due_at AT TIME ZONE captured.timezone)::time END,captured.timezone,captured.anchor_kind,
  profile.managed,NEW.state,jsonb_build_object('membership_id',NEW.assignee_id),CASE WHEN profile.managed THEN 'user' ELSE 'legacy' END,NEW.created_by);
 INSERT INTO chores.occurrence_checklist_items(household_id,occurrence_id,item_key,title,position,required,created_by)
 SELECT NEW.household_id,NEW.id,item_key,title,position,required,NEW.created_by FROM chores.version_checklist_items WHERE version_id=captured.id;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION chores.capture_occurrence() FROM PUBLIC,household_app;

CREATE FUNCTION chores.sync_occurrence_state() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF (NEW.state,NEW.due_at,NEW.assignee_id) IS DISTINCT FROM (OLD.state,OLD.due_at,OLD.assignee_id) THEN
  UPDATE chores.occurrence_details SET lifecycle_state=NEW.state,updated_at=now(),version=version+1 WHERE occurrence_id=NEW.id;
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION chores.sync_occurrence_state() FROM PUBLIC,household_app;

CREATE FUNCTION chores.guard_terminal_occurrence() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  RAISE EXCEPTION 'retain chore identity; cancel the occurrence or use maintenance purge' USING ERRCODE='23514';
 END IF;
 IF NEW.id IS DISTINCT FROM OLD.id OR NEW.household_id IS DISTINCT FROM OLD.household_id OR NEW.template_id IS DISTINCT FROM OLD.template_id
  OR NEW.occurrence_key IS DISTINCT FROM OLD.occurrence_key THEN
  RAISE EXCEPTION 'chore occurrence identity is immutable' USING ERRCODE='23514';
 END IF;
 IF OLD.state IN('completed','skipped') AND NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'terminal chore occurrence is immutable' USING ERRCODE='23514';
 END IF;
 IF NEW.state='completed' AND NOT EXISTS(SELECT 1 FROM chores.task_templates t JOIN chores.series_profiles p ON p.template_id=t.id
  WHERE t.household_id=NEW.household_id AND t.id=NEW.template_id AND t.active AND(NOT p.paused OR NEW.due_at<=p.paused_at)) THEN
  RAISE EXCEPTION 'archived or suspended chore cannot be completed' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION chores.guard_terminal_occurrence() FROM PUBLIC,household_app;

CREATE TRIGGER chore_template_capture AFTER INSERT OR UPDATE ON chores.task_templates FOR EACH ROW EXECUTE FUNCTION chores.capture_template();
CREATE TRIGGER chore_occurrence_capture AFTER INSERT ON chores.task_occurrences FOR EACH ROW EXECUTE FUNCTION chores.capture_occurrence();
CREATE TRIGGER chore_occurrence_terminal BEFORE UPDATE OR DELETE ON chores.task_occurrences FOR EACH ROW EXECUTE FUNCTION chores.guard_terminal_occurrence();
CREATE TRIGGER chore_occurrence_sync AFTER UPDATE ON chores.task_occurrences FOR EACH ROW EXECUTE FUNCTION chores.sync_occurrence_state();

DO $guards$
DECLARE table_name text;
BEGIN
 FOREACH table_name IN ARRAY ARRAY['task_templates','task_occurrences','areas','series_profiles','template_versions','version_checklist_items',
  'version_dependencies','rotation_members','absences','occurrence_details','occurrence_checklist_items','completions','member_preferences','calendar_feeds','reminder_deliveries','schedule_state'] LOOP
  EXECUTE format('CREATE TRIGGER chore_00_write_lock BEFORE INSERT OR UPDATE OR DELETE ON chores.%I FOR EACH ROW EXECUTE FUNCTION chores.lock_write()',table_name);
 END LOOP;
 FOREACH table_name IN ARRAY ARRAY['template_versions','version_checklist_items','version_dependencies','rotation_members','completions','reminder_deliveries'] LOOP
  EXECUTE format('CREATE TRIGGER chore_history_immutable BEFORE UPDATE OR DELETE ON chores.%I FOR EACH ROW EXECUTE FUNCTION chores.guard_history()',table_name);
 END LOOP;
END $guards$;

CREATE FUNCTION chores.dependencies_met(household uuid,occurrence uuid) RETURNS boolean
LANGUAGE sql STABLE SET search_path=pg_catalog AS $$
 SELECT NOT EXISTS(
  SELECT 1 FROM chores.task_occurrences target JOIN chores.occurrence_details detail ON detail.occurrence_id=target.id
  JOIN chores.version_dependencies dependency ON dependency.version_id=detail.template_version_id
  LEFT JOIN LATERAL(
   SELECT source.state,completion.id completion_id FROM chores.task_occurrences source
   JOIN chores.occurrence_details source_detail ON source_detail.occurrence_id=source.id
   LEFT JOIN chores.completions completion ON completion.occurrence_id=source.id
   WHERE source.household_id=household AND source.template_id=dependency.prerequisite_id AND source.due_at<=target.due_at
   AND source_detail.skipped_reason<>'series_changed' ORDER BY source.due_at DESC,source.id DESC LIMIT 1
  ) prerequisite ON true
  WHERE target.household_id=household AND target.id=occurrence AND(prerequisite.state IS DISTINCT FROM 'completed' OR prerequisite.completion_id IS NULL)
 ) AND EXISTS(SELECT 1 FROM chores.task_occurrences WHERE household_id=household AND id=occurrence)
$$;
REVOKE ALL ON FUNCTION chores.dependencies_met(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION chores.dependencies_met(uuid,uuid) TO household_app;

CREATE FUNCTION chores.check_dependency_graph() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE source uuid;household uuid;active_version uuid;
BEGIN
 source=NEW.template_id;household=NEW.household_id;
 SELECT current_version_id INTO active_version FROM chores.series_profiles WHERE household_id=household AND template_id=source;
 IF active_version IS NULL THEN RETURN NULL;END IF;
 IF (SELECT count(*) FROM chores.version_dependencies WHERE version_id=active_version)>16 THEN
  RAISE EXCEPTION 'at most sixteen chore prerequisites are supported' USING ERRCODE='23514';
 END IF;
 IF EXISTS(WITH RECURSIVE reachable(id) AS(
  SELECT prerequisite_id FROM chores.version_dependencies WHERE version_id=active_version
  UNION SELECT dependency.prerequisite_id FROM reachable r
   JOIN chores.series_profiles profile ON profile.household_id=household AND profile.template_id=r.id
   JOIN chores.version_dependencies dependency ON dependency.version_id=profile.current_version_id
 ) SELECT 1 FROM reachable WHERE id=source) THEN
  RAISE EXCEPTION 'chore prerequisite cycle' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION chores.check_dependency_graph() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER chore_profile_graph AFTER INSERT OR UPDATE ON chores.series_profiles
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION chores.check_dependency_graph();
CREATE CONSTRAINT TRIGGER chore_dependency_graph AFTER INSERT ON chores.version_dependencies
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION chores.check_dependency_graph();

CREATE FUNCTION chores.guard_occurrence_details() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE current_state text;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'retain chore history until maintenance purge' USING ERRCODE='23514';END IF;
 IF (NEW.occurrence_id,NEW.household_id,NEW.template_id,NEW.template_version_id,NEW.logical_key,NEW.original_due_at,NEW.scheduled_on,NEW.local_time,NEW.timezone,NEW.managed,NEW.anchor_kind)
  IS DISTINCT FROM(OLD.occurrence_id,OLD.household_id,OLD.template_id,OLD.template_version_id,OLD.logical_key,OLD.original_due_at,OLD.scheduled_on,OLD.local_time,OLD.timezone,OLD.managed,OLD.anchor_kind) THEN
  RAISE EXCEPTION 'original chore identity and deadline are immutable' USING ERRCODE='23514';
 END IF;
 SELECT state INTO current_state FROM chores.task_occurrences WHERE household_id=NEW.household_id AND id=NEW.occurrence_id;
 IF OLD.lifecycle_state IN('completed','skipped') AND NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'terminal chore details are immutable' USING ERRCODE='23514';
 END IF;
 IF current_state IN('completed','skipped') AND
  ((to_jsonb(NEW)-ARRAY['lifecycle_state','updated_at','version']) IS DISTINCT FROM(to_jsonb(OLD)-ARRAY['lifecycle_state','updated_at','version'])
   OR NEW.lifecycle_state IS DISTINCT FROM current_state) THEN
  RAISE EXCEPTION 'only the matching final chore transition may change terminal details' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION chores.guard_occurrence_details() FROM PUBLIC,household_app;
CREATE TRIGGER chore_details_guard BEFORE UPDATE OR DELETE ON chores.occurrence_details FOR EACH ROW EXECUTE FUNCTION chores.guard_occurrence_details();

CREATE FUNCTION chores.guard_feed() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'revoke calendar feeds explicitly' USING ERRCODE='23514';END IF;
 IF (to_jsonb(NEW)-ARRAY['revoked_at','updated_at','version']) IS DISTINCT FROM
  (to_jsonb(OLD)-ARRAY['revoked_at','updated_at','version']) OR OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN
  RAISE EXCEPTION 'calendar feed identity and revocation are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION chores.guard_feed() FROM PUBLIC,household_app;
CREATE TRIGGER chore_feed_guard BEFORE UPDATE OR DELETE ON chores.calendar_feeds FOR EACH ROW EXECUTE FUNCTION chores.guard_feed();

CREATE FUNCTION chores.guard_checklist() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE occurrence uuid;household uuid;current_state text;
BEGIN
 IF TG_OP='DELETE' THEN occurrence=OLD.occurrence_id;household=OLD.household_id;ELSE occurrence=NEW.occurrence_id;household=NEW.household_id;END IF;
 SELECT state INTO current_state FROM chores.task_occurrences WHERE household_id=household AND id=occurrence;
 IF current_state IS DISTINCT FROM 'open' THEN
  RAISE EXCEPTION 'checklist belongs to an open chore occurrence' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'use a reviewed future checklist revision' USING ERRCODE='23514';END IF;
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.household_id,NEW.occurrence_id,NEW.item_key,NEW.title,NEW.position,NEW.required)
   IS DISTINCT FROM(OLD.id,OLD.household_id,OLD.occurrence_id,OLD.item_key,OLD.title,OLD.position,OLD.required) THEN
   RAISE EXCEPTION 'captured chore checklist structure is immutable' USING ERRCODE='23514';
  END IF;
 END IF;
 IF NEW.done AND NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
  WHERE m.household_id=household AND m.user_id=NEW.done_by AND m.role IN('admin','editor') AND m.inactive_at IS NULL
  AND u.disabled_at IS NULL AND NOT u.must_change_password) THEN
  RAISE EXCEPTION 'current chore checklist editor required' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION chores.guard_checklist() FROM PUBLIC,household_app;
CREATE TRIGGER chore_checklist_guard BEFORE INSERT OR UPDATE OR DELETE ON chores.occurrence_checklist_items FOR EACH ROW EXECUTE FUNCTION chores.guard_checklist();

CREATE FUNCTION chores.guard_completion() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE occurrence chores.task_occurrences%ROWTYPE;detail chores.occurrence_details%ROWTYPE;
BEGIN
 SELECT * INTO occurrence FROM chores.task_occurrences WHERE household_id=NEW.household_id AND id=NEW.occurrence_id FOR UPDATE;
 SELECT * INTO detail FROM chores.occurrence_details WHERE occurrence_id=NEW.occurrence_id;
 IF occurrence.id IS NULL OR detail.occurrence_id IS NULL OR occurrence.state<>'completed'
  OR occurrence.template_id IS DISTINCT FROM NEW.template_id OR detail.template_version_id IS DISTINCT FROM NEW.template_version_id
  OR occurrence.completed_at IS DISTINCT FROM NEW.completed_at OR occurrence.completed_by IS DISTINCT FROM NEW.actor_user_id THEN
  RAISE EXCEPTION 'completion must describe its exact completed chore occurrence' USING ERRCODE='23514';
 END IF;
 IF NEW.legacy_capture THEN
  IF session_user<>'household_owner' THEN RAISE EXCEPTION 'only the separate owner may capture legacy chore facts' USING ERRCODE='42501';END IF;
  RETURN NEW;
 END IF;
 PERFORM 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
  WHERE m.household_id=NEW.household_id AND m.id=NEW.actor_membership_id AND m.user_id=NEW.actor_user_id
  AND m.role IN('admin','editor') AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password FOR SHARE OF m,u;
 IF NOT FOUND THEN RAISE EXCEPTION 'current chore completion editor required' USING ERRCODE='23514';END IF;
 IF NEW.completed_at<transaction_timestamp() OR NEW.completed_at>clock_timestamp() THEN
  RAISE EXCEPTION 'new chore completion uses its real transaction time' USING ERRCODE='23514';
 END IF;
 IF EXISTS(SELECT 1 FROM chores.occurrence_checklist_items WHERE occurrence_id=NEW.occurrence_id AND required AND NOT done)
  OR NOT chores.dependencies_met(NEW.household_id,NEW.occurrence_id) THEN
  RAISE EXCEPTION 'complete mandatory chore steps and prerequisites first' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION chores.guard_completion() FROM PUBLIC,household_app;
CREATE TRIGGER chore_completion_guard BEFORE INSERT ON chores.completions FOR EACH ROW EXECUTE FUNCTION chores.guard_completion();

CREATE FUNCTION chores.check_occurrence_consistency() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE occurrence uuid;household uuid;task chores.task_occurrences%ROWTYPE;detail chores.occurrence_details%ROWTYPE;fact chores.completions%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='task_occurrences' THEN occurrence=NEW.id;household=NEW.household_id;
 ELSE occurrence=NEW.occurrence_id;household=NEW.household_id;END IF;
 SELECT * INTO task FROM chores.task_occurrences WHERE household_id=household AND id=occurrence;
 SELECT * INTO detail FROM chores.occurrence_details WHERE household_id=household AND occurrence_id=occurrence;
 SELECT * INTO fact FROM chores.completions WHERE household_id=household AND occurrence_id=occurrence;
 IF task.id IS NULL OR detail.occurrence_id IS NULL OR task.template_id IS DISTINCT FROM detail.template_id
  OR task.state IS DISTINCT FROM detail.lifecycle_state THEN
  RAISE EXCEPTION 'complete matching chore occurrence detail required' USING ERRCODE='23514';
 END IF;
 IF task.state='completed' THEN
  IF fact.id IS NULL OR fact.template_id IS DISTINCT FROM task.template_id OR fact.template_version_id IS DISTINCT FROM detail.template_version_id
   OR fact.completed_at IS DISTINCT FROM task.completed_at OR fact.actor_user_id IS DISTINCT FROM task.completed_by THEN
   RAISE EXCEPTION 'exactly one matching immutable chore completion is required' USING ERRCODE='23514';
  END IF;
 ELSIF fact.id IS NOT NULL THEN RAISE EXCEPTION 'an open or skipped chore cannot own a completion fact' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION chores.check_occurrence_consistency() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER chore_occurrence_complete AFTER INSERT OR UPDATE ON chores.task_occurrences
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION chores.check_occurrence_consistency();
CREATE CONSTRAINT TRIGGER chore_detail_complete AFTER INSERT OR UPDATE ON chores.occurrence_details
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION chores.check_occurrence_consistency();
CREATE CONSTRAINT TRIGGER chore_fact_complete AFTER INSERT ON chores.completions
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION chores.check_occurrence_consistency();

CREATE FUNCTION chores.guard_profile() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'archive the chore series instead of deleting its profile' USING ERRCODE='23514';END IF;
 IF NEW.template_id IS DISTINCT FROM OLD.template_id THEN RAISE EXCEPTION 'chore series identity is immutable' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION chores.guard_profile() FROM PUBLIC,household_app;
CREATE TRIGGER chore_profile_identity BEFORE UPDATE OR DELETE ON chores.series_profiles FOR EACH ROW EXECUTE FUNCTION chores.guard_profile();

CREATE FUNCTION chores.check_profile_consistency() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE profile chores.series_profiles%ROWTYPE;captured chores.template_versions%ROWTYPE;template chores.task_templates%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='task_templates' THEN
  SELECT * INTO profile FROM chores.series_profiles WHERE household_id=NEW.household_id AND template_id=NEW.id;
 ELSE
  SELECT * INTO profile FROM chores.series_profiles WHERE household_id=NEW.household_id AND template_id=NEW.template_id;
 END IF;
 IF profile.template_id IS NULL THEN RAISE EXCEPTION 'chore series profile required' USING ERRCODE='23514';END IF;
 IF NOT profile.managed THEN RETURN NULL;END IF;
 SELECT * INTO captured FROM chores.template_versions WHERE id=profile.current_version_id;
 SELECT * INTO template FROM chores.task_templates WHERE household_id=profile.household_id AND id=profile.template_id;
 IF captured.id IS NULL OR template.id IS NULL OR captured.title IS DISTINCT FROM template.title
  OR captured.description IS DISTINCT FROM template.description OR captured.timezone IS DISTINCT FROM template.timezone
  OR captured.assignee_id IS DISTINCT FROM template.assignee_id
  OR captured.rule_kind='days' AND captured.interval_value IS DISTINCT FROM template.repeat_days
  OR captured.rule_kind='once' AND coalesce(template.repeat_days,0)<>0
  OR captured.rule_kind IN('weekly','monthly') AND template.repeat_days IS NOT NULL THEN
  RAISE EXCEPTION 'managed chore header and current snapshot must agree' USING ERRCODE='23514';
 END IF;
 IF captured.legacy_capture OR captured.starts_on IS NULL OR captured.effective_at IS NULL OR profile.materialize_from IS NULL THEN
  RAISE EXCEPTION 'managed chore requires its reviewed schedule snapshot' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION chores.check_profile_consistency() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER chore_template_profile_complete AFTER INSERT OR UPDATE ON chores.task_templates
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION chores.check_profile_consistency();
CREATE CONSTRAINT TRIGGER chore_profile_complete AFTER INSERT OR UPDATE ON chores.series_profiles
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION chores.check_profile_consistency();

-- Canonical metadata must survive pg_dump/restore without changing readiness.
SET CONSTRAINTS ALL IMMEDIATE;
DO $canonical$
DECLARE item record;
BEGIN
 FOR item IN SELECT c.conrelid::regclass relation,c.conname,pg_get_constraintdef(c.oid) definition
  FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace
  WHERE n.nspname='chores' AND c.contype='c' ORDER BY c.conrelid,c.conname LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',item.relation,item.conname);
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s',item.relation,item.conname,item.definition);
 END LOOP;
END $canonical$;

CREATE FUNCTION chores.expansion_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 WITH relations AS(SELECT t.oid,t.relname FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace
  WHERE n.nspname='chores' AND t.relkind IN('r','p') AND t.relname<>'schema_migrations')
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
  FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='chores' AND p.proname NOT IN('expansion_catalog','expansion_ready')))
$$;
REVOKE ALL ON FUNCTION chores.expansion_catalog() FROM PUBLIC,household_app;
DO $readiness$
DECLARE expected jsonb;catalog_hash text;catalog_owner text;catalog_acl text;ready_query text;
BEGIN
 expected=chores.expansion_catalog();
 SELECT encode(sha256(convert_to(pg_get_functiondef(p.oid),'UTF8')),'hex'),p.proowner::regrole::text,p.proacl::text
 INTO catalog_hash,catalog_owner,catalog_acl FROM pg_proc p WHERE p.oid='chores.expansion_catalog()'::regprocedure;
 ready_query=format('SELECT chores.expansion_catalog()=%L::jsonb AND (SELECT count(*) FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname=''chores'' AND t.relkind IN(''r'',''p'') AND t.relname<>''schema_migrations'')=16 AND EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid=''chores.expansion_catalog()''::regprocedure AND encode(sha256(convert_to(pg_get_functiondef(p.oid),''UTF8'')),''hex'')=%L AND p.proowner::regrole::text=%L AND p.proacl::text IS NOT DISTINCT FROM %L)',expected::text,catalog_hash,catalog_owner,catalog_acl);
 EXECUTE format('CREATE FUNCTION chores.expansion_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  ready_query);
END $readiness$;
REVOKE ALL ON FUNCTION chores.expansion_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION chores.expansion_ready() TO household_app;
COMMIT;
