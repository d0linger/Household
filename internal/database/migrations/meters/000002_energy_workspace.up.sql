BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
DO $predecessor$
DECLARE snapshot jsonb;
BEGIN
 IF NOT (SELECT count(*)=1 AND bool_and(version=32 AND NOT dirty) FROM core.schema_migrations)
  OR NOT core.audit_ready() OR NOT core.finance_ready() OR NOT chores.expansion_ready()
  OR NOT fuel.module_ready() OR NOT pantry.prepared_capture_ready() OR NOT mealplan.expansion_ready()
  OR (SELECT count(*) FROM core.audit_policies)<>135 THEN
  RAISE EXCEPTION 'Energy workspace requires the accepted complete Core32 predecessor';
 END IF;
 SELECT jsonb_build_object('finance_catalog',core.finance_catalog(),
  'audit_policies',(SELECT jsonb_agg(to_jsonb(policy) ORDER BY schema_name,table_name) FROM core.audit_policies policy),
  'functions',jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
   'owner',p.proowner::regrole::text,'acl',p.proacl::text))) INTO snapshot
 FROM pg_proc p WHERE p.oid IN('core.audit_ready()'::regprocedure,'core.finance_ready()'::regprocedure,
  'core.finance_producers_ready()'::regprocedure,'core.planning_extensions_ready()'::regprocedure,
  'fuel.overview_ready()'::regprocedure,'fuel.module_ready()'::regprocedure,
  'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure,'pantry.prepared_capture_ready()'::regprocedure);
 EXECUTE format('CREATE FUNCTION meters.predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION meters.predecessor_catalog() FROM PUBLIC,household_app;

CREATE TABLE meters.registers(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),meter_id uuid NOT NULL,
 name text NOT NULL CHECK(length(btrim(name)) BETWEEN 1 AND 100),
 measurement_kind text NOT NULL CHECK(measurement_kind IN('cumulative','interval','power')),
 unit_code text NOT NULL CHECK(unit_code IN('kWh','m3','l','kW','W')),
 direction text NOT NULL DEFAULT 'import' CHECK(direction IN('import','export','generation')),
 timezone text NOT NULL CHECK(length(timezone) BETWEEN 1 AND 100),input_mode text NOT NULL DEFAULT 'manual' CHECK(input_mode IN('manual','telemetry')),
 is_default boolean NOT NULL DEFAULT false,max_gap_seconds integer NOT NULL DEFAULT 2678400 CHECK(max_gap_seconds BETWEEN 1 AND 31622400),
 daily_warning numeric(18,6) CHECK(daily_warning>0),base_load_warning numeric(18,6) CHECK(base_load_warning>0),
 min_history integer NOT NULL DEFAULT 7 CHECK(min_history BETWEEN 7 AND 100),retention_days integer CHECK(retention_days BETWEEN 7 AND 3650),
 archived_at timestamptz,retained_from timestamptz,data_revision bigint NOT NULL DEFAULT 1 CHECK(data_revision>0),
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,id),UNIQUE(household_id,meter_id,id),
 FOREIGN KEY(household_id,meter_id) REFERENCES meters.meters(household_id,id),
 CHECK((measurement_kind='power')=(unit_code IN('kW','W')))
);
CREATE UNIQUE INDEX energy_default_register ON meters.registers(meter_id) WHERE is_default;
CREATE INDEX energy_register_list ON meters.registers(household_id,meter_id,archived_at,id);
CREATE TABLE meters.device_registers(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),register_id uuid NOT NULL,device_id uuid NOT NULL,
 started_at timestamptz NOT NULL,ended_at timestamptz,initial_value numeric(18,6) NOT NULL CHECK(initial_value>=0),
 final_value numeric(18,6) CHECK(final_value>=0),rollover_value numeric(18,6) CHECK(rollover_value>0),legacy_boundary boolean NOT NULL DEFAULT false,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,id),UNIQUE(register_id,device_id),
 FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id),
 FOREIGN KEY(household_id,device_id) REFERENCES meters.devices(household_id,id),CHECK(ended_at IS NULL OR ended_at>=started_at),
 CHECK(final_value IS NULL OR ended_at IS NOT NULL),CHECK(rollover_value IS NULL OR initial_value<rollover_value AND (final_value IS NULL OR final_value<rollover_value))
);
CREATE INDEX energy_device_period ON meters.device_registers(household_id,register_id,started_at,ended_at);
ALTER TABLE meters.readings ADD COLUMN register_id uuid,ADD COLUMN interval_started_at timestamptz,
 ADD COLUMN rollover_confirmed boolean NOT NULL DEFAULT false,ADD COLUMN source_key text CHECK(length(source_key)<=200);
ALTER TABLE meters.readings ADD FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id);
ALTER TABLE meters.readings DROP CONSTRAINT readings_device_id_measured_at_key;
CREATE UNIQUE INDEX energy_reading_instant ON meters.readings(register_id,device_id,measured_at);
CREATE UNIQUE INDEX energy_reading_source ON meters.readings(household_id,register_id,source_key) WHERE source_key IS NOT NULL;
CREATE INDEX energy_reading_register ON meters.readings(household_id,register_id,measured_at,id) INCLUDE(value,kind,device_id);

CREATE TABLE meters.reading_revisions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),reading_id uuid NOT NULL,
 register_id uuid NOT NULL,device_id uuid NOT NULL,revision bigint NOT NULL CHECK(revision>0),
 measured_at timestamptz NOT NULL,value numeric(18,6) NOT NULL,kind text NOT NULL,interval_started_at timestamptz,
 rollover_confirmed boolean NOT NULL,note text NOT NULL,source_key text,deleted boolean NOT NULL DEFAULT false,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,id),UNIQUE(reading_id,revision),FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id),
 FOREIGN KEY(household_id,device_id) REFERENCES meters.devices(household_id,id)
);
CREATE TABLE meters.device_changes(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),register_id uuid NOT NULL,
 old_mapping_id uuid NOT NULL,new_mapping_id uuid NOT NULL,changed_at timestamptz NOT NULL,
 old_final numeric(18,6),new_initial numeric(18,6) NOT NULL CHECK(new_initial>=0),gap_acknowledged boolean NOT NULL DEFAULT false,
 reason text NOT NULL CHECK(length(btrim(reason)) BETWEEN 1 AND 1000),created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,id),UNIQUE(old_mapping_id),UNIQUE(new_mapping_id),
 FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id),
 FOREIGN KEY(household_id,old_mapping_id) REFERENCES meters.device_registers(household_id,id),
 FOREIGN KEY(household_id,new_mapping_id) REFERENCES meters.device_registers(household_id,id),
 CHECK(old_mapping_id<>new_mapping_id),CHECK(old_final IS NOT NULL OR gap_acknowledged)
);
CREATE TABLE meters.tariff_versions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),register_id uuid NOT NULL,
 starts_on date NOT NULL,ends_before date,price_minor_per_unit numeric(18,6) NOT NULL CHECK(price_minor_per_unit>=0),
 base_minor_per_day numeric(18,6) NOT NULL DEFAULT 0 CHECK(base_minor_per_day>=0),currency_code text NOT NULL DEFAULT 'EUR' CHECK(currency_code='EUR'),
 label text NOT NULL CHECK(length(btrim(label)) BETWEEN 1 AND 100),created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,id),UNIQUE(register_id,starts_on),
 FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id),CHECK(ends_before IS NULL OR ends_before>starts_on)
);
CREATE TABLE meters.payments(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),register_id uuid NOT NULL,
 paid_on date NOT NULL,amount_minor bigint NOT NULL CHECK(amount_minor BETWEEN 1 AND 9000000000000000),
 note text NOT NULL DEFAULT '' CHECK(length(note)<=1000),created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,id),
 FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id)
);
CREATE TABLE meters.bills(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),register_id uuid NOT NULL,
 occurred_on date NOT NULL,title text NOT NULL CHECK(length(btrim(title)) BETWEEN 1 AND 200),
 amount_minor bigint NOT NULL CHECK(amount_minor BETWEEN 1 AND 9000000000000000),
 kind text NOT NULL CHECK(kind IN('invoice','advance')),confirmed_at timestamptz,expense_id uuid,expense_generation bigint,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,id),
 FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id),
 CHECK((expense_id IS NULL)=(expense_generation IS NULL)),CHECK(expense_id IS NULL OR confirmed_at IS NOT NULL)
);
CREATE TABLE meters.reading_plans(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),register_id uuid NOT NULL,
 anchor_on date NOT NULL,interval_days integer NOT NULL CHECK(interval_days BETWEEN 1 AND 366),
 actual_only boolean NOT NULL DEFAULT true,active boolean NOT NULL DEFAULT true,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,id),
 FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id)
);
CREATE UNIQUE INDEX energy_active_reading_plan ON meters.reading_plans(register_id) WHERE active;
CREATE TABLE meters.plan_completions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),plan_id uuid NOT NULL,
 due_on date NOT NULL,plan_version bigint NOT NULL CHECK(plan_version>0),reading_id uuid NOT NULL,reading_revision bigint NOT NULL,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(household_id,id),UNIQUE(plan_id,plan_version,due_on,reading_id,reading_revision),
 FOREIGN KEY(household_id,plan_id) REFERENCES meters.reading_plans(household_id,id),
 FOREIGN KEY(reading_id,reading_revision) REFERENCES meters.reading_revisions(reading_id,revision)
);
CREATE TABLE meters.import_batches(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),register_id uuid NOT NULL,
 content_sha256 text NOT NULL CHECK(content_sha256~'^[0-9a-f]{64}$'),timezone text NOT NULL,unit_code text NOT NULL,
 state text NOT NULL DEFAULT 'preview' CHECK(state IN('preview','applied','cancelled')),row_count integer NOT NULL CHECK(row_count BETWEEN 1 AND 1000),
 module_generation bigint NOT NULL CHECK(module_generation>0),source_revision bigint NOT NULL CHECK(source_revision>0),
 register_version bigint NOT NULL CHECK(register_version>0),device_mapping_id uuid NOT NULL,mapping_version bigint NOT NULL CHECK(mapping_version>0),
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,id),
 FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id),
 FOREIGN KEY(household_id,device_mapping_id) REFERENCES meters.device_registers(household_id,id)
);
CREATE TABLE meters.import_rows(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),batch_id uuid NOT NULL,
 row_number integer NOT NULL CHECK(row_number BETWEEN 1 AND 1000),device_id uuid NOT NULL,measured_at timestamptz NOT NULL,
 value numeric(18,6) NOT NULL CHECK(value>=0),kind text NOT NULL CHECK(kind IN('actual','estimated')),interval_started_at timestamptz,
 reading_id uuid,created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,id),UNIQUE(batch_id,row_number),FOREIGN KEY(household_id,batch_id) REFERENCES meters.import_batches(household_id,id),
 FOREIGN KEY(household_id,device_id) REFERENCES meters.devices(household_id,id),FOREIGN KEY(household_id,reading_id) REFERENCES meters.readings(household_id,id)
);
CREATE TABLE meters.integration_sources(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),register_id uuid NOT NULL,device_id uuid NOT NULL,
 adapter text NOT NULL CHECK(adapter IN('home_assistant','mqtt')),source_name text NOT NULL CHECK(length(btrim(source_name)) BETWEEN 1 AND 200),
 measurement_kind text NOT NULL CHECK(measurement_kind IN('cumulative','interval','power')),unit_code text NOT NULL,
 enabled boolean NOT NULL DEFAULT false,module_generation bigint NOT NULL CHECK(module_generation>0),
 last_success_at timestamptz,last_attempt_at timestamptz,next_attempt_at timestamptz,consecutive_failures integer NOT NULL DEFAULT 0 CHECK(consecutive_failures BETWEEN 0 AND 20),
 status_code text NOT NULL DEFAULT 'not_run' CHECK(status_code IN('not_run','ready','disabled','configuration_missing','approval_required','unavailable','invalid_message','backoff')),
 authorized_by uuid NOT NULL REFERENCES core.users(id),last_rejected_sha256 text CHECK(last_rejected_sha256~'^[0-9a-f]{64}$'),
 rejected_messages bigint NOT NULL DEFAULT 0 CHECK(rejected_messages>=0),negotiated_qos integer CHECK(negotiated_qos IN(0,1)),
 minute_started_at timestamptz,minute_samples integer NOT NULL DEFAULT 0 CHECK(minute_samples BETWEEN 0 AND 120),
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,id),UNIQUE(household_id,adapter,source_name),
 FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id),
 FOREIGN KEY(household_id,device_id) REFERENCES meters.devices(household_id,id),
 CHECK(adapter<>'mqtt' OR source_name !~ '[+#\x00-\x1f]'),CHECK(adapter<>'home_assistant' OR source_name~'^sensor\.[a-z0-9_]{1,150}$')
);
CREATE TABLE meters.telemetry(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),
 register_id uuid NOT NULL,device_id uuid NOT NULL,source_id uuid NOT NULL,source_key text NOT NULL CHECK(length(source_key) BETWEEN 1 AND 200),
 measured_at timestamptz NOT NULL,interval_started_at timestamptz,value numeric(18,6) NOT NULL CHECK(value>=0),source_version bigint NOT NULL CHECK(source_version>0),
 kind text NOT NULL CHECK(kind IN('actual','estimated')),rollover_confirmed boolean NOT NULL DEFAULT false,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(source_id,source_key),FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id),
 FOREIGN KEY(household_id,device_id) REFERENCES meters.devices(household_id,id),
 FOREIGN KEY(household_id,source_id) REFERENCES meters.integration_sources(household_id,id)
);
CREATE INDEX energy_telemetry_window ON meters.telemetry(household_id,register_id,measured_at,id) INCLUDE(value,kind,device_id);
CREATE UNIQUE INDEX energy_telemetry_instant ON meters.telemetry(register_id,device_id,measured_at);
CREATE TABLE meters.intervals(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),register_id uuid NOT NULL,
 from_at timestamptz NOT NULL,through_at timestamptz NOT NULL,device_id uuid NOT NULL,quantity numeric(24,9),
 unit_code text NOT NULL,quality text NOT NULL CHECK(quality IN('actual','estimated','gap','reset_unresolved')),
 observation_kind text NOT NULL CHECK(observation_kind IN('manual','telemetry')),source_revision bigint NOT NULL CHECK(source_revision>0),
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,id),UNIQUE(register_id,from_at,through_at,observation_kind),
 FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id),
 FOREIGN KEY(household_id,device_id) REFERENCES meters.devices(household_id,id),CHECK(through_at>from_at),CHECK(quantity IS NULL OR quantity>=0),
 CHECK((quality IN('gap','reset_unresolved'))=(quantity IS NULL))
);
CREATE INDEX energy_interval_window ON meters.intervals(household_id,register_id,from_at,through_at);
CREATE TABLE meters.aggregates(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),register_id uuid NOT NULL,
 bucket_kind text NOT NULL CHECK(bucket_kind IN('hour','day','month')),from_at timestamptz NOT NULL,through_at timestamptz NOT NULL,
 quantity numeric(24,9),covered_seconds bigint NOT NULL CHECK(covered_seconds>=0),estimated_seconds bigint NOT NULL CHECK(estimated_seconds>=0),
 source_revision bigint NOT NULL CHECK(source_revision>0),created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,id),UNIQUE(register_id,bucket_kind,from_at),FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id),
 CHECK(through_at>from_at),CHECK(quantity IS NULL OR quantity>=0),CHECK(estimated_seconds<=covered_seconds)
);
CREATE INDEX energy_aggregate_window ON meters.aggregates(household_id,register_id,bucket_kind,from_at);
CREATE TABLE meters.dirty_ranges(
 register_id uuid PRIMARY KEY,household_id uuid NOT NULL REFERENCES core.households(id),from_at timestamptz NOT NULL,through_at timestamptz NOT NULL,
 cursor_at timestamptz,cursor_id text NOT NULL DEFAULT '',source_revision bigint NOT NULL CHECK(source_revision>0),
 phase text NOT NULL DEFAULT 'intervals' CHECK(phase IN('intervals','aggregates')),created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(household_id,register_id) REFERENCES meters.registers(household_id,id),CHECK(through_at>from_at)
);
CREATE TABLE meters.command_receipts(
 household_id uuid NOT NULL REFERENCES core.households(id),user_id uuid NOT NULL REFERENCES core.users(id),
 command_key uuid NOT NULL,operation text NOT NULL CHECK(length(operation) BETWEEN 1 AND 200),
 module_generation bigint NOT NULL CHECK(module_generation>0),request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 response_payload bytea NOT NULL CHECK(octet_length(response_payload) BETWEEN 2 AND 131072),
 created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(household_id,user_id,command_key)
);

INSERT INTO meters.registers(household_id,meter_id,name,measurement_kind,unit_code,timezone,is_default,created_by)
 SELECT m.household_id,m.id,m.name,'cumulative',m.unit_code,h.timezone,true,m.created_by FROM meters.meters m JOIN core.households h ON h.id=m.household_id;
INSERT INTO meters.device_registers(household_id,register_id,device_id,started_at,ended_at,initial_value,rollover_value,legacy_boundary,created_by)
 SELECT d.household_id,r.id,d.id,d.installed_on::timestamp AT TIME ZONE r.timezone,
  CASE WHEN d.removed_on IS NOT NULL THEN (d.removed_on+1)::timestamp AT TIME ZONE r.timezone END,
  d.initial_reading,d.rollover_value,true,d.created_by FROM meters.devices d JOIN meters.registers r ON r.meter_id=d.meter_id AND r.is_default;
UPDATE meters.readings x SET register_id=r.id FROM meters.devices d JOIN meters.registers r ON r.meter_id=d.meter_id AND r.is_default WHERE x.device_id=d.id;
ALTER TABLE meters.readings ALTER COLUMN register_id SET NOT NULL;
INSERT INTO meters.reading_revisions(household_id,reading_id,register_id,device_id,revision,measured_at,value,kind,interval_started_at,rollover_confirmed,note,source_key,created_by)
 SELECT household_id,id,register_id,device_id,version,measured_at,value,kind,interval_started_at,rollover_confirmed,note,source_key,created_by FROM meters.readings;

GRANT SELECT,INSERT,UPDATE,DELETE ON meters.registers,meters.device_registers,meters.tariff_versions,meters.payments,meters.bills,
 meters.reading_plans,meters.import_batches,meters.import_rows,meters.integration_sources,meters.intervals,meters.aggregates,meters.dirty_ranges TO household_app;
GRANT SELECT,INSERT ON meters.reading_revisions,meters.device_changes,meters.plan_completions,meters.telemetry TO household_app;
GRANT SELECT,INSERT ON meters.command_receipts TO household_app;
REVOKE TRUNCATE ON ALL TABLES IN SCHEMA meters FROM household_app;

CREATE FUNCTION meters.invalidate(p_household uuid,p_register uuid,p_from timestamptz,p_through timestamptz,p_actor uuid)
 RETURNS void LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE revision bigint;floor_at timestamptz;
BEGIN
 UPDATE meters.registers SET data_revision=data_revision+1 WHERE household_id=p_household AND id=p_register RETURNING data_revision,retained_from INTO revision,floor_at;
 IF floor_at IS NOT NULL THEN p_from=greatest(p_from,floor_at);END IF;
 IF revision IS NULL OR p_through<=p_from THEN RAISE EXCEPTION 'invalid energy invalidation' USING ERRCODE='23514';END IF;
 INSERT INTO meters.dirty_ranges AS old(register_id,household_id,from_at,through_at,source_revision,created_by)
 VALUES(p_register,p_household,p_from,p_through,revision,p_actor)
 ON CONFLICT(register_id) DO UPDATE SET from_at=least(old.from_at,EXCLUDED.from_at),through_at=greatest(old.through_at,EXCLUDED.through_at),
  cursor_at=CASE WHEN old.phase='intervals' AND EXCLUDED.from_at>old.cursor_at THEN old.cursor_at ELSE NULL END,
  cursor_id=CASE WHEN old.phase='intervals' AND EXCLUDED.from_at>old.cursor_at THEN old.cursor_id ELSE '' END,
  phase='intervals',source_revision=EXCLUDED.source_revision,updated_at=clock_timestamp();
END $$;
REVOKE ALL ON FUNCTION meters.invalidate(uuid,uuid,timestamptz,timestamptz,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION meters.invalidate(uuid,uuid,timestamptz,timestamptz,uuid) TO household_app;

CREATE FUNCTION meters.capture_legacy_meter() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 INSERT INTO meters.registers(household_id,meter_id,name,measurement_kind,unit_code,timezone,is_default,created_by)
 SELECT NEW.household_id,NEW.id,NEW.name,'cumulative',NEW.unit_code,h.timezone,true,NEW.created_by FROM core.households h WHERE h.id=NEW.household_id;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION meters.capture_legacy_meter() FROM PUBLIC,household_app;
CREATE TRIGGER energy_default_register AFTER INSERT ON meters.meters FOR EACH ROW EXECUTE FUNCTION meters.capture_legacy_meter();
CREATE FUNCTION meters.capture_legacy_device() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 INSERT INTO meters.device_registers(household_id,register_id,device_id,started_at,ended_at,initial_value,rollover_value,legacy_boundary,created_by)
 SELECT NEW.household_id,r.id,NEW.id,NEW.installed_on::timestamp AT TIME ZONE r.timezone,
  CASE WHEN NEW.removed_on IS NOT NULL THEN (NEW.removed_on+1)::timestamp AT TIME ZONE r.timezone END,
  NEW.initial_reading,NEW.rollover_value,true,NEW.created_by FROM meters.registers r WHERE r.meter_id=NEW.meter_id AND r.is_default;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION meters.capture_legacy_device() FROM PUBLIC,household_app;
CREATE TRIGGER energy_default_device AFTER INSERT ON meters.devices FOR EACH ROW EXECUTE FUNCTION meters.capture_legacy_device();

CREATE FUNCTION meters.guard_measurement() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE r meters.registers%ROWTYPE;mapping meters.device_registers%ROWTYPE;previous numeric;following numeric;source meters.integration_sources%ROWTYPE;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('household/meters/'||NEW.household_id::text,0));
 IF NEW.register_id IS NULL THEN
  SELECT reg.id INTO NEW.register_id FROM meters.devices d JOIN meters.registers reg ON reg.meter_id=d.meter_id AND reg.is_default
   WHERE d.household_id=NEW.household_id AND d.id=NEW.device_id;
 END IF;
 SELECT * INTO r FROM meters.registers WHERE household_id=NEW.household_id AND id=NEW.register_id FOR SHARE;
 SELECT * INTO mapping FROM meters.device_registers WHERE household_id=NEW.household_id AND register_id=NEW.register_id AND device_id=NEW.device_id FOR SHARE;
 IF r.id IS NULL OR mapping.id IS NULL OR r.archived_at IS NOT NULL
  OR NOT EXISTS(SELECT 1 FROM meters.meters m WHERE m.household_id=NEW.household_id AND m.id=r.meter_id AND m.archived_at IS NULL)
  OR NEW.measured_at<mapping.started_at
  OR mapping.ended_at IS NOT NULL AND NEW.measured_at>mapping.ended_at OR NEW.measured_at>clock_timestamp() THEN
  RAISE EXCEPTION 'measurement must match its active energy register/device period' USING ERRCODE='23514';
 END IF;
 IF r.measurement_kind='interval' AND (NEW.interval_started_at IS NULL OR NEW.interval_started_at>=NEW.measured_at OR NEW.interval_started_at<mapping.started_at)
  OR r.measurement_kind<>'interval' AND NEW.interval_started_at IS NOT NULL THEN
  RAISE EXCEPTION 'interval quantities require an explicit matching interval' USING ERRCODE='23514';
 END IF;
 IF TG_TABLE_NAME='telemetry' THEN
  SELECT * INTO source FROM meters.integration_sources WHERE household_id=NEW.household_id AND id=NEW.source_id;
  IF source.id IS NULL OR NOT source.enabled OR source.register_id<>NEW.register_id OR source.device_id<>NEW.device_id
   OR source.measurement_kind<>r.measurement_kind OR source.unit_code<>r.unit_code OR source.version<>NEW.source_version OR r.input_mode<>'telemetry'
   OR r.retained_from IS NOT NULL AND NEW.measured_at<r.retained_from
   OR NOT EXISTS(SELECT 1 FROM core.module_states s WHERE s.household_id=NEW.household_id AND s.module_id='meters' AND s.state='enabled' AND s.data_generation=source.module_generation) THEN
   RAISE EXCEPTION 'current mapped telemetry source required' USING ERRCODE='23514';
  END IF;
  IF NEW.rollover_confirmed AND(mapping.rollover_value IS NULL OR r.measurement_kind<>'cumulative')
   OR mapping.rollover_value IS NOT NULL AND r.measurement_kind='cumulative' AND NEW.value>=mapping.rollover_value THEN
   RAISE EXCEPTION 'telemetry contradicts its explicit counter range' USING ERRCODE='23514';END IF;
  IF r.measurement_kind='interval' AND EXISTS(SELECT 1 FROM meters.telemetry x WHERE x.register_id=NEW.register_id
   AND tstzrange(x.interval_started_at,x.measured_at,'[)') && tstzrange(NEW.interval_started_at,NEW.measured_at,'[)')) THEN
   RAISE EXCEPTION 'raw interval quantities cannot overlap' USING ERRCODE='23514';END IF;
 ELSE
  IF r.input_mode<>'manual' THEN RAISE EXCEPTION 'manual readings and raw telemetry use separate registers' USING ERRCODE='23514';END IF;
  IF TG_OP='UPDATE' AND (NEW.household_id,NEW.register_id,NEW.device_id,NEW.created_by) IS DISTINCT FROM(OLD.household_id,OLD.register_id,OLD.device_id,OLD.created_by) THEN
   RAISE EXCEPTION 'measurement identity is immutable' USING ERRCODE='23514';
  END IF;
  IF TG_OP='UPDATE' AND (NEW.version<>OLD.version+1 OR NEW.source_key IS DISTINCT FROM OLD.source_key) THEN
   RAISE EXCEPTION 'corrections retain source identity and advance exactly one revision' USING ERRCODE='23514';END IF;
  IF r.measurement_kind='cumulative' THEN
   SELECT value INTO previous FROM meters.readings WHERE register_id=NEW.register_id AND device_id=NEW.device_id AND measured_at<NEW.measured_at AND id<>NEW.id ORDER BY measured_at DESC LIMIT 1;
   SELECT value INTO following FROM meters.readings WHERE register_id=NEW.register_id AND device_id=NEW.device_id AND measured_at>NEW.measured_at AND id<>NEW.id ORDER BY measured_at LIMIT 1;
   IF mapping.rollover_value IS NULL AND (NEW.value<coalesce(previous,mapping.initial_value) OR following IS NOT NULL AND NEW.value>following)
    OR mapping.rollover_value IS NOT NULL AND NEW.value>=mapping.rollover_value
    OR NEW.rollover_confirmed AND mapping.rollover_value IS NULL THEN
    RAISE EXCEPTION 'measurement contradicts its device range or adjacent values' USING ERRCODE='23514';
   END IF;
  ELSIF NEW.rollover_confirmed THEN RAISE EXCEPTION 'only cumulative registers can roll over' USING ERRCODE='23514';END IF;
  IF r.measurement_kind='interval' AND EXISTS(SELECT 1 FROM meters.readings x WHERE x.register_id=NEW.register_id AND x.id<>NEW.id
   AND tstzrange(x.interval_started_at,x.measured_at,'[)') && tstzrange(NEW.interval_started_at,NEW.measured_at,'[)')) THEN
   RAISE EXCEPTION 'interval quantities cannot overlap' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION meters.guard_measurement() FROM PUBLIC,household_app;
CREATE TRIGGER energy_reading_guard BEFORE INSERT OR UPDATE ON meters.readings FOR EACH ROW EXECUTE FUNCTION meters.guard_measurement();
CREATE TRIGGER energy_telemetry_guard BEFORE INSERT ON meters.telemetry FOR EACH ROW EXECUTE FUNCTION meters.guard_measurement();

CREATE FUNCTION meters.capture_measurement() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE first_at timestamptz;last_at timestamptz;actor uuid;hh uuid;reg uuid;point_at timestamptz;
BEGIN
 IF TG_OP='DELETE' THEN hh=OLD.household_id;reg=OLD.register_id;point_at=OLD.measured_at;actor=OLD.created_by;
 ELSE hh=NEW.household_id;reg=NEW.register_id;point_at=NEW.measured_at;actor=NEW.created_by;END IF;
 actor=coalesce(nullif(current_setting('household.audit_user',true),'')::uuid,actor);
 IF TG_TABLE_NAME='readings' AND TG_OP<>'DELETE' THEN
  INSERT INTO meters.reading_revisions(household_id,reading_id,register_id,device_id,revision,measured_at,value,kind,interval_started_at,rollover_confirmed,note,source_key,created_by)
  VALUES(NEW.household_id,NEW.id,NEW.register_id,NEW.device_id,NEW.version,NEW.measured_at,NEW.value,NEW.kind,NEW.interval_started_at,NEW.rollover_confirmed,NEW.note,NEW.source_key,actor);
 ELSIF TG_TABLE_NAME='readings' THEN
  INSERT INTO meters.reading_revisions(household_id,reading_id,register_id,device_id,revision,measured_at,value,kind,interval_started_at,rollover_confirmed,note,source_key,deleted,created_by)
  VALUES(OLD.household_id,OLD.id,OLD.register_id,OLD.device_id,OLD.version+1,OLD.measured_at,OLD.value,OLD.kind,OLD.interval_started_at,OLD.rollover_confirmed,OLD.note,OLD.source_key,true,actor);
 END IF;
 SELECT min(started_at) INTO first_at FROM meters.device_registers WHERE household_id=hh AND register_id=reg;
 first_at=coalesce((SELECT max(measured_at) FROM meters.readings WHERE register_id=reg AND measured_at<point_at),first_at,point_at-interval '1 second');
 last_at=coalesce((SELECT min(measured_at) FROM meters.readings WHERE register_id=reg AND measured_at>point_at),point_at+interval '1 second');
 IF TG_TABLE_NAME='telemetry' THEN
  first_at=coalesce((SELECT max(measured_at) FROM meters.telemetry WHERE register_id=reg AND measured_at<point_at),first_at);
  last_at=coalesce((SELECT min(measured_at) FROM meters.telemetry WHERE register_id=reg AND measured_at>point_at),point_at+interval '1 second');
 END IF;
 IF TG_OP='UPDATE' THEN
  first_at=least(first_at,OLD.measured_at,(SELECT max(measured_at) FROM meters.readings WHERE register_id=reg AND measured_at<OLD.measured_at));
  last_at=greatest(last_at,OLD.measured_at+interval '1 second',(SELECT min(measured_at) FROM meters.readings WHERE register_id=reg AND measured_at>OLD.measured_at));
 END IF;
 PERFORM meters.invalidate(hh,reg,first_at,last_at,actor);
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION meters.capture_measurement() FROM PUBLIC,household_app;
CREATE TRIGGER energy_reading_revision AFTER INSERT OR UPDATE OR DELETE ON meters.readings FOR EACH ROW EXECUTE FUNCTION meters.capture_measurement();
CREATE TRIGGER energy_telemetry_invalidation AFTER INSERT ON meters.telemetry FOR EACH ROW EXECUTE FUNCTION meters.capture_measurement();

CREATE FUNCTION meters.guard_immutable_fact() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN RAISE EXCEPTION 'energy history is immutable; use a new correction or separate Owner purge' USING ERRCODE='23514';END $$;
REVOKE ALL ON FUNCTION meters.guard_immutable_fact() FROM PUBLIC,household_app;
CREATE TRIGGER energy_revision_immutable BEFORE UPDATE OR DELETE ON meters.reading_revisions FOR EACH ROW EXECUTE FUNCTION meters.guard_immutable_fact();
CREATE TRIGGER energy_change_immutable BEFORE UPDATE OR DELETE ON meters.device_changes FOR EACH ROW EXECUTE FUNCTION meters.guard_immutable_fact();
CREATE TRIGGER energy_plan_fact_immutable BEFORE UPDATE OR DELETE ON meters.plan_completions FOR EACH ROW EXECUTE FUNCTION meters.guard_immutable_fact();
CREATE TRIGGER energy_receipt_immutable BEFORE UPDATE OR DELETE ON meters.command_receipts FOR EACH ROW EXECUTE FUNCTION meters.guard_immutable_fact();

-- All existing values are preserved. Legacy boundaries remain labelled rather than inventing end readings.
INSERT INTO meters.dirty_ranges(register_id,household_id,from_at,through_at,source_revision,created_by)
 SELECT r.id,r.household_id,min(d.started_at),greatest(max(coalesce(d.ended_at,clock_timestamp())),min(d.started_at)+interval '1 second'),r.data_revision,r.created_by
 FROM meters.registers r JOIN meters.device_registers d ON d.register_id=r.id GROUP BY r.id;

CREATE FUNCTION meters.guard_mapping() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE meter uuid;device_meter uuid;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('household/meters/'||NEW.household_id::text,0));
 SELECT meter_id INTO meter FROM meters.registers WHERE household_id=NEW.household_id AND id=NEW.register_id;
 SELECT meter_id INTO device_meter FROM meters.devices WHERE household_id=NEW.household_id AND id=NEW.device_id;
 IF meter IS NULL OR device_meter IS DISTINCT FROM meter THEN RAISE EXCEPTION 'device and register must belong to the same meter' USING ERRCODE='23514';END IF;
 IF TG_OP='UPDATE' AND (NEW.household_id,NEW.register_id,NEW.device_id,NEW.started_at,NEW.initial_value)
  IS DISTINCT FROM(OLD.household_id,OLD.register_id,OLD.device_id,OLD.started_at,OLD.initial_value)
  AND(EXISTS(SELECT 1 FROM meters.readings WHERE register_id=OLD.register_id AND device_id=OLD.device_id)
   OR EXISTS(SELECT 1 FROM meters.telemetry WHERE register_id=OLD.register_id AND device_id=OLD.device_id)) THEN
  RAISE EXCEPTION 'device history boundaries require an explicit replacement' USING ERRCODE='23514';
 END IF;
 IF NOT NEW.legacy_boundary AND EXISTS(SELECT 1 FROM meters.device_registers x
  WHERE x.household_id=NEW.household_id AND x.register_id=NEW.register_id AND x.id<>NEW.id
   AND tstzrange(x.started_at,x.ended_at,'[)') && tstzrange(NEW.started_at,NEW.ended_at,'[)')) THEN
  RAISE EXCEPTION 'reviewed energy device periods cannot overlap' USING ERRCODE='23514';
 END IF;
 IF EXISTS(SELECT 1 FROM meters.readings WHERE register_id=NEW.register_id AND device_id=NEW.device_id
  AND (measured_at<NEW.started_at OR NEW.ended_at IS NOT NULL AND measured_at>NEW.ended_at))
 OR EXISTS(SELECT 1 FROM meters.telemetry WHERE register_id=NEW.register_id AND device_id=NEW.device_id
  AND (measured_at<NEW.started_at OR NEW.ended_at IS NOT NULL AND measured_at>NEW.ended_at)) THEN
  RAISE EXCEPTION 'device period cannot exclude its existing readings' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION meters.guard_mapping() FROM PUBLIC,household_app;
CREATE TRIGGER energy_mapping_guard BEFORE INSERT OR UPDATE ON meters.device_registers FOR EACH ROW EXECUTE FUNCTION meters.guard_mapping();

CREATE FUNCTION meters.guard_tariff() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('household/meters/'||NEW.household_id::text,0));
 IF EXISTS(SELECT 1 FROM meters.tariff_versions x WHERE x.household_id=NEW.household_id AND x.register_id=NEW.register_id
  AND x.id<>NEW.id AND daterange(x.starts_on,x.ends_before,'[)') && daterange(NEW.starts_on,NEW.ends_before,'[)')) THEN
  RAISE EXCEPTION 'energy tariff periods cannot overlap' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND (to_jsonb(NEW)-ARRAY['ends_before','version']) IS DISTINCT FROM(to_jsonb(OLD)-ARRAY['ends_before','version']) THEN
  RAISE EXCEPTION 'retain tariff prices and add a new dated version' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION meters.guard_tariff() FROM PUBLIC,household_app;
CREATE TRIGGER energy_tariff_guard BEFORE INSERT OR UPDATE ON meters.tariff_versions FOR EACH ROW EXECUTE FUNCTION meters.guard_tariff();

CREATE FUNCTION meters.guard_register() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('household/meters/'||NEW.household_id::text,0));
  IF(SELECT count(*) FROM meters.registers WHERE household_id=NEW.household_id)>=250 THEN
   RAISE EXCEPTION 'bounded energy register inventory exceeded' USING ERRCODE='23514';END IF;
 END IF;
 IF(TG_OP='INSERT' OR NEW.timezone IS DISTINCT FROM OLD.timezone)
  AND NOT EXISTS(SELECT 1 FROM pg_timezone_names WHERE name=NEW.timezone) THEN RAISE EXCEPTION 'valid energy timezone required' USING ERRCODE='23514';END IF;
 IF TG_OP='UPDATE' AND (NEW.household_id,NEW.meter_id,NEW.measurement_kind,NEW.unit_code,NEW.direction,NEW.timezone,NEW.input_mode)
  IS DISTINCT FROM(OLD.household_id,OLD.meter_id,OLD.measurement_kind,OLD.unit_code,OLD.direction,OLD.timezone,OLD.input_mode)
  AND (EXISTS(SELECT 1 FROM meters.readings WHERE register_id=OLD.id) OR EXISTS(SELECT 1 FROM meters.telemetry WHERE register_id=OLD.id)
   OR EXISTS(SELECT 1 FROM meters.device_registers WHERE register_id=OLD.id) OR EXISTS(SELECT 1 FROM meters.tariff_versions WHERE register_id=OLD.id)
   OR EXISTS(SELECT 1 FROM meters.reading_plans WHERE register_id=OLD.id) OR EXISTS(SELECT 1 FROM meters.bills WHERE register_id=OLD.id)
   OR EXISTS(SELECT 1 FROM meters.payments WHERE register_id=OLD.id)) THEN
  RAISE EXCEPTION 'register history retains its type, unit, direction, timezone and source stream' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION meters.guard_register() FROM PUBLIC,household_app;
CREATE TRIGGER energy_register_guard BEFORE INSERT OR UPDATE ON meters.registers FOR EACH ROW EXECUTE FUNCTION meters.guard_register();

CREATE FUNCTION meters.capture_mapping() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE boundary timestamptz;first_at timestamptz;last_at timestamptz;floor_at timestamptz;
BEGIN
 IF TG_OP='UPDATE' AND(NEW.started_at,NEW.ended_at,NEW.initial_value,NEW.final_value,NEW.rollover_value,NEW.legacy_boundary)
  IS NOT DISTINCT FROM(OLD.started_at,OLD.ended_at,OLD.initial_value,OLD.final_value,OLD.rollover_value,OLD.legacy_boundary) THEN RETURN NULL;END IF;
 boundary=CASE WHEN TG_OP='UPDATE' THEN coalesce(NEW.ended_at,OLD.ended_at,NEW.started_at) ELSE NEW.started_at END;
 SELECT retained_from INTO floor_at FROM meters.registers WHERE id=NEW.register_id;
 IF floor_at IS NOT NULL AND boundary<floor_at THEN RETURN NULL;END IF;
 SELECT max(at) INTO first_at FROM(
  SELECT measured_at at FROM meters.readings WHERE register_id=NEW.register_id AND measured_at<boundary
  UNION ALL SELECT measured_at FROM meters.telemetry WHERE register_id=NEW.register_id AND measured_at<boundary) prior;
 SELECT min(at) INTO last_at FROM(
  SELECT measured_at at FROM meters.readings WHERE register_id=NEW.register_id AND measured_at>boundary
  UNION ALL SELECT measured_at FROM meters.telemetry WHERE register_id=NEW.register_id AND measured_at>boundary) following;
 PERFORM meters.invalidate(NEW.household_id,NEW.register_id,coalesce(first_at,boundary),
  greatest(coalesce(last_at,boundary+interval '1 second'),boundary+interval '1 second'),NEW.created_by);
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION meters.capture_mapping() FROM PUBLIC,household_app;
CREATE TRIGGER energy_mapping_invalidation AFTER INSERT OR UPDATE ON meters.device_registers FOR EACH ROW EXECUTE FUNCTION meters.capture_mapping();

CREATE FUNCTION meters.guard_plan_completion() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM meters.reading_plans p JOIN meters.readings r ON r.household_id=p.household_id AND r.register_id=p.register_id
  JOIN meters.registers reg ON reg.id=p.register_id
  WHERE p.household_id=NEW.household_id AND p.id=NEW.plan_id AND p.version=NEW.plan_version AND p.active AND r.id=NEW.reading_id AND r.version=NEW.reading_revision
   AND (NOT p.actual_only OR r.kind='actual') AND (r.measured_at AT TIME ZONE reg.timezone)::date>=NEW.due_on
   AND (r.measured_at AT TIME ZONE reg.timezone)::date<NEW.due_on+p.interval_days
   AND NEW.due_on>=p.anchor_on AND (NEW.due_on-p.anchor_on)%p.interval_days=0) THEN
  RAISE EXCEPTION 'plan completion requires its actual matching current reading revision' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION meters.guard_plan_completion() FROM PUBLIC,household_app;
CREATE TRIGGER energy_plan_completion_guard BEFORE INSERT ON meters.plan_completions FOR EACH ROW EXECUTE FUNCTION meters.guard_plan_completion();

CREATE FUNCTION meters.guard_bill() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF OLD.confirmed_at IS NOT NULL AND (to_jsonb(NEW)-ARRAY['expense_id','expense_generation','updated_at','version'])
  IS DISTINCT FROM(to_jsonb(OLD)-ARRAY['expense_id','expense_generation','updated_at','version'])
  OR OLD.expense_id IS NOT NULL AND (NEW.expense_id,NEW.expense_generation) IS DISTINCT FROM(OLD.expense_id,OLD.expense_generation) THEN
  RAISE EXCEPTION 'confirmed energy bills retain their facts and financial identity' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION meters.guard_bill() FROM PUBLIC,household_app;
CREATE TRIGGER energy_bill_guard BEFORE UPDATE ON meters.bills FOR EACH ROW EXECUTE FUNCTION meters.guard_bill();

-- The application has no direct DELETE grant on raw telemetry. This narrowly
-- scoped owner function requires a real live energy job, current generation,
-- explicit retention, completed projections and a preserved predecessor anchor.
CREATE FUNCTION meters.prune_telemetry(p_household uuid,p_register uuid,p_job uuid,p_lease uuid,p_limit integer)
 RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE reg meters.registers%ROWTYPE;cutoff timestamptz;removed integer;
BEGIN
 IF p_limit NOT BETWEEN 1 AND 500 THEN RAISE EXCEPTION 'bounded telemetry retention required' USING ERRCODE='22023';END IF;
 PERFORM id FROM core.households WHERE id=p_household FOR SHARE;
 PERFORM pg_advisory_xact_lock(hashtextextended('household/meters/'||p_household::text,0));
 PERFORM module_id FROM core.module_states WHERE household_id=p_household AND module_id='meters' FOR SHARE;
 PERFORM id FROM core.jobs WHERE id=p_job AND lease_token=p_lease FOR UPDATE;
 IF NOT EXISTS(SELECT 1 FROM core.jobs j JOIN core.module_states s ON s.household_id=j.household_id AND s.module_id=j.module_id
  WHERE j.id=p_job AND j.lease_token=p_lease AND j.household_id=p_household AND j.kind='energy_workspace' AND j.module_id='meters'
   AND j.state='running' AND j.lease_until>clock_timestamp() AND s.state='enabled' AND s.data_generation=j.module_generation) THEN
  RAISE EXCEPTION 'current energy retention lease required' USING ERRCODE='42501';END IF;
 SELECT * INTO reg FROM meters.registers WHERE household_id=p_household AND id=p_register FOR UPDATE;
 IF reg.id IS NULL OR reg.input_mode<>'telemetry' OR reg.retention_days IS NULL
  OR EXISTS(SELECT 1 FROM meters.dirty_ranges WHERE register_id=p_register) THEN RETURN 0;END IF;
 cutoff=clock_timestamp()-make_interval(days=>reg.retention_days);
 WITH candidates AS(SELECT t.id FROM meters.telemetry t WHERE t.household_id=p_household AND t.register_id=p_register AND t.measured_at<cutoff
  AND EXISTS(SELECT 1 FROM meters.telemetry later WHERE later.register_id=p_register AND later.device_id=t.device_id AND later.measured_at<cutoff
   AND (later.measured_at,later.id)>(t.measured_at,t.id))
  AND EXISTS(SELECT 1 FROM meters.aggregates a WHERE a.register_id=p_register AND a.bucket_kind='hour'
   AND a.from_at<=t.measured_at AND a.through_at>t.measured_at AND a.quantity IS NOT NULL AND a.covered_seconds>0)
  ORDER BY t.measured_at,t.id LIMIT p_limit),deleted AS(DELETE FROM meters.telemetry t USING candidates c WHERE t.id=c.id RETURNING t.id)
 SELECT count(*) INTO removed FROM deleted;
 IF removed>0 THEN UPDATE meters.registers SET retained_from=greatest(coalesce(retained_from,cutoff),cutoff),updated_at=clock_timestamp() WHERE id=p_register;END IF;
 IF NOT EXISTS(SELECT 1 FROM core.jobs j JOIN core.module_states s ON s.household_id=j.household_id AND s.module_id=j.module_id
  WHERE j.id=p_job AND j.lease_token=p_lease AND j.household_id=p_household AND j.kind='energy_workspace'
  AND j.state='running' AND j.lease_until>clock_timestamp() AND s.state='enabled' AND s.data_generation=j.module_generation) THEN
  RAISE EXCEPTION 'energy retention lease expired before completion' USING ERRCODE='42501';END IF;
 RETURN removed;
END $$;
REVOKE ALL ON FUNCTION meters.prune_telemetry(uuid,uuid,uuid,uuid,integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION meters.prune_telemetry(uuid,uuid,uuid,uuid,integer) TO household_app;

-- Canonical metadata is captured only after the module's complete SQL source.
DO $canonical$
DECLARE item record;
BEGIN
 FOR item IN SELECT c.conrelid::regclass relation,c.conname,pg_get_constraintdef(c.oid) definition
  FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace
  WHERE n.nspname='meters' AND c.contype='c' ORDER BY c.conrelid,c.conname LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',item.relation,item.conname);
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s',item.relation,item.conname,item.definition);
 END LOOP;
END $canonical$;
CREATE FUNCTION meters.guard_device_identity() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('household/meters/'||NEW.household_id::text,0));
 IF(NEW.household_id,NEW.meter_id,NEW.installed_on,NEW.removed_on,NEW.initial_reading,NEW.rollover_value)
  IS DISTINCT FROM(OLD.household_id,OLD.meter_id,OLD.installed_on,OLD.removed_on,OLD.initial_reading,OLD.rollover_value)
  AND EXISTS(SELECT 1 FROM meters.device_registers WHERE household_id=OLD.household_id AND device_id=OLD.id) THEN
  RAISE EXCEPTION 'mapped device boundaries require a reviewed register replacement' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION meters.guard_device_identity() FROM PUBLIC,household_app;
CREATE TRIGGER energy_device_identity BEFORE UPDATE ON meters.devices FOR EACH ROW EXECUTE FUNCTION meters.guard_device_identity();

CREATE FUNCTION meters.expansion_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 WITH relations AS(SELECT t.oid,t.relname FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace
  WHERE n.nspname='meters' AND t.relkind IN('r','p') AND t.relname<>'schema_migrations')
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
  FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='meters' AND p.proname NOT IN('expansion_catalog','expansion_ready')))
$$;
REVOKE ALL ON FUNCTION meters.expansion_catalog() FROM PUBLIC,household_app;
DO $readiness$
DECLARE expected jsonb;catalog_hash text;catalog_owner text;catalog_acl text;ready_query text;
BEGIN
 expected=meters.expansion_catalog();
 SELECT encode(sha256(convert_to(pg_get_functiondef(p.oid),'UTF8')),'hex'),p.proowner::regrole::text,p.proacl::text
 INTO catalog_hash,catalog_owner,catalog_acl FROM pg_proc p WHERE p.oid='meters.expansion_catalog()'::regprocedure;
 ready_query=format('SELECT meters.expansion_catalog()=%L::jsonb AND (SELECT count(*) FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname=''meters'' AND t.relkind IN(''r'',''p'') AND t.relname<>''schema_migrations'')=20 AND EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid=''meters.expansion_catalog()''::regprocedure AND encode(sha256(convert_to(pg_get_functiondef(p.oid),''UTF8'')),''hex'')=%L AND p.proowner::regrole::text=%L AND p.proacl::text IS NOT DISTINCT FROM %L)',expected::text,catalog_hash,catalog_owner,catalog_acl);
 EXECUTE format('CREATE FUNCTION meters.expansion_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  ready_query);
END $readiness$;
REVOKE ALL ON FUNCTION meters.expansion_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION meters.expansion_ready() TO household_app;
COMMIT;
