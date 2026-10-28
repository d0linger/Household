BEGIN;

CREATE FUNCTION fuel.valid_cost_map(p_value jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE SET search_path=pg_catalog AS $$
 SELECT CASE WHEN jsonb_typeof(p_value) IS DISTINCT FROM 'object' THEN false ELSE
 (SELECT count(*)<=64 FROM jsonb_each(p_value)) AND NOT EXISTS(SELECT 1 FROM jsonb_each(p_value) e
 WHERE e.key !~ '^[A-Z]{3}$' OR jsonb_typeof(e.value)<>'string' OR e.value#>>'{}' !~ '^[0-9]{1,48}$') END
$$;
REVOKE ALL ON FUNCTION fuel.valid_cost_map(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION fuel.valid_cost_map(jsonb) TO household_app;

INSERT INTO fuel.fuel_types(code,label,unit_code) VALUES('electric','Elektrisch','kWh'),('hydrogen','Wasserstoff','kg');
ALTER TABLE fuel.vehicles
 ADD COLUMN tank_capacity numeric(18,6) CHECK(tank_capacity>0),
 ADD COLUMN battery_capacity numeric(18,6) CHECK(battery_capacity>0),
 ADD COLUMN current_series_id uuid;
ALTER TABLE fuel.odometer_series
 ADD COLUMN final_reading numeric(18,6) CHECK(final_reading>=initial_reading),
 ADD COLUMN previous_series_id uuid,
 ADD COLUMN distance_offset numeric(18,6) CHECK(distance_offset>=0),
 ADD CONSTRAINT series_previous FOREIGN KEY(household_id,vehicle_id,previous_series_id) REFERENCES fuel.odometer_series(household_id,vehicle_id,id),
 ADD CONSTRAINT series_previous_distinct CHECK(previous_series_id IS NULL OR previous_series_id<>id);
ALTER TABLE fuel.vehicles ADD CONSTRAINT vehicles_current_series FOREIGN KEY(household_id,id,current_series_id)
 REFERENCES fuel.odometer_series(household_id,vehicle_id,id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE fuel.fillups ADD COLUMN quantity_unit text REFERENCES core.units(code),
 ADD COLUMN currency_code text NOT NULL DEFAULT 'EUR' CHECK(currency_code ~ '^[A-Z]{3}$'),
 ADD COLUMN source_kind text NOT NULL DEFAULT 'manual' CHECK(source_kind IN('manual','import','receipt')),
 ADD COLUMN source_job_id uuid,ADD COLUMN source_row integer CHECK(source_row>0),
 ADD COLUMN source_fingerprint bytea CHECK(octet_length(source_fingerprint)=32);
UPDATE fuel.fillups f SET quantity_unit=t.unit_code FROM fuel.fuel_types t WHERE t.code=f.fuel_type;
ALTER TABLE fuel.fillups ALTER COLUMN quantity_unit SET NOT NULL;
CREATE UNIQUE INDEX fillups_import_source ON fuel.fillups(household_id,source_job_id,source_row) WHERE source_job_id IS NOT NULL AND source_row IS NOT NULL;

CREATE TABLE fuel.odometer_observations(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),vehicle_id uuid NOT NULL,
 odometer_series_id uuid NOT NULL,observed_on date NOT NULL,reading numeric(18,6) NOT NULL CHECK(reading>=0),
 source_kind text NOT NULL CHECK(source_kind IN('manual','photo')),capture_job_id uuid UNIQUE,
 notes text NOT NULL DEFAULT '' CHECK(length(notes)<=10000),created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 UNIQUE(household_id,id),FOREIGN KEY(household_id,vehicle_id,odometer_series_id) REFERENCES fuel.odometer_series(household_id,vehicle_id,id));
CREATE INDEX observations_latest ON fuel.odometer_observations(household_id,vehicle_id,odometer_series_id,observed_on DESC,reading DESC,id DESC);
CREATE TABLE fuel.charging_sessions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),vehicle_id uuid NOT NULL,
 odometer_series_id uuid,occurred_on date NOT NULL,odometer numeric(18,6) CHECK(odometer>=0),
 energy_kwh numeric(18,6) NOT NULL CHECK(energy_kwh>0),measurement_basis text NOT NULL CHECK(measurement_basis IN('wall','battery','unknown')),
 total_minor bigint CHECK(total_minor BETWEEN 0 AND 1000000000000),currency_code text NOT NULL CHECK(currency_code ~ '^[A-Z]{3}$'),
 location text NOT NULL DEFAULT '' CHECK(length(location)<=200),charge_kind text NOT NULL CHECK(charge_kind IN('home','public','work','other')),
 soc_start numeric(5,2) CHECK(soc_start BETWEEN 0 AND 100),soc_end numeric(5,2) CHECK(soc_end BETWEEN 0 AND 100),
 notes text NOT NULL DEFAULT '' CHECK(length(notes)<=10000),created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 UNIQUE(household_id,id),FOREIGN KEY(household_id,vehicle_id) REFERENCES fuel.vehicles(household_id,id),
 FOREIGN KEY(household_id,vehicle_id,odometer_series_id) REFERENCES fuel.odometer_series(household_id,vehicle_id,id),
 CHECK((odometer_series_id IS NULL)=(odometer IS NULL)),CHECK(soc_start IS NULL OR soc_end IS NULL OR soc_end>=soc_start));
CREATE INDEX charging_window ON fuel.charging_sessions(household_id,vehicle_id,occurred_on DESC,id DESC);
CREATE TABLE fuel.trips(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),vehicle_id uuid NOT NULL,
 odometer_series_id uuid NOT NULL,started_on date NOT NULL,ended_on date NOT NULL CHECK(ended_on>=started_on),
 start_odometer numeric(18,6) NOT NULL CHECK(start_odometer>=0),end_odometer numeric(18,6) NOT NULL CHECK(end_odometer>start_odometer),
 purpose text NOT NULL DEFAULT '' CHECK(length(purpose)<=200),notes text NOT NULL DEFAULT '' CHECK(length(notes)<=10000),
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,id),
 FOREIGN KEY(household_id,vehicle_id,odometer_series_id) REFERENCES fuel.odometer_series(household_id,vehicle_id,id));
CREATE INDEX trips_window ON fuel.trips(household_id,vehicle_id,started_on DESC,id DESC);
CREATE TABLE fuel.vehicle_costs(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),vehicle_id uuid NOT NULL,
 occurred_on date NOT NULL,kind text NOT NULL CHECK(kind IN('insurance','tax','repair','maintenance','parking','toll','other')),
 title text NOT NULL CHECK(length(title) BETWEEN 1 AND 200),total_minor bigint NOT NULL CHECK(total_minor BETWEEN 1 AND 1000000000000),
 currency_code text NOT NULL CHECK(currency_code ~ '^[A-Z]{3}$'),notes text NOT NULL DEFAULT '' CHECK(length(notes)<=10000),corrects_id uuid,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),UNIQUE(household_id,id),UNIQUE(household_id,vehicle_id,id),
 UNIQUE(household_id,corrects_id),FOREIGN KEY(household_id,vehicle_id) REFERENCES fuel.vehicles(household_id,id),
 FOREIGN KEY(household_id,vehicle_id,corrects_id) REFERENCES fuel.vehicle_costs(household_id,vehicle_id,id),CHECK(corrects_id IS NULL OR corrects_id<>id));
CREATE INDEX costs_window ON fuel.vehicle_costs(household_id,vehicle_id,occurred_on DESC,id DESC);
CREATE TABLE fuel.maintenance_plans(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),vehicle_id uuid NOT NULL,
 title text NOT NULL CHECK(length(title) BETWEEN 1 AND 200),notes text NOT NULL DEFAULT '' CHECK(length(notes)<=10000),
 due_on date,due_odometer numeric(18,6) CHECK(due_odometer>=0),odometer_series_id uuid,
 repeat_days integer CHECK(repeat_days BETWEEN 1 AND 36500),repeat_distance numeric(18,6) CHECK(repeat_distance>0),
 active boolean NOT NULL DEFAULT true,cycle bigint NOT NULL DEFAULT 1 CHECK(cycle>0),task_opt_in boolean NOT NULL DEFAULT false,
 assignee_id uuid,created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 UNIQUE(household_id,id),UNIQUE(household_id,vehicle_id,id),FOREIGN KEY(household_id,vehicle_id) REFERENCES fuel.vehicles(household_id,id),
 FOREIGN KEY(household_id,vehicle_id,odometer_series_id) REFERENCES fuel.odometer_series(household_id,vehicle_id,id),
 FOREIGN KEY(household_id,assignee_id) REFERENCES core.memberships(household_id,id),
 CHECK(due_on IS NOT NULL OR due_odometer IS NOT NULL),CHECK(due_odometer IS NULL OR odometer_series_id IS NOT NULL));
CREATE INDEX maintenance_due ON fuel.maintenance_plans(household_id,vehicle_id,active,due_on,id);
CREATE TABLE fuel.maintenance_events(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),vehicle_id uuid NOT NULL,plan_id uuid NOT NULL,
 cycle bigint NOT NULL CHECK(cycle>0),completed_on date NOT NULL,odometer numeric(18,6) CHECK(odometer>=0),odometer_series_id uuid,
 notes text NOT NULL DEFAULT '' CHECK(length(notes)<=10000),created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 UNIQUE(household_id,id),UNIQUE(household_id,plan_id,cycle),FOREIGN KEY(household_id,vehicle_id,plan_id) REFERENCES fuel.maintenance_plans(household_id,vehicle_id,id),
 FOREIGN KEY(household_id,vehicle_id,odometer_series_id) REFERENCES fuel.odometer_series(household_id,vehicle_id,id),CHECK((odometer IS NULL)=(odometer_series_id IS NULL)));

CREATE TABLE fuel.user_settings(
 household_id uuid NOT NULL REFERENCES core.households(id),user_id uuid NOT NULL REFERENCES core.users(id),generation bigint NOT NULL CHECK(generation>0),
 default_vehicle_id uuid,default_series_id uuid,default_station text NOT NULL DEFAULT '' CHECK(length(default_station)<=200),
 minimum_consumption numeric(18,6) CHECK(minimum_consumption>0),maximum_consumption numeric(18,6) CHECK(maximum_consumption>0),
 maximum_fill_quantity numeric(18,6) CHECK(maximum_fill_quantity>0),maximum_daily_distance numeric(18,6) CHECK(maximum_daily_distance>0),
 maximum_unit_price numeric(18,6) CHECK(maximum_unit_price>0),plausibility_unit text CHECK(plausibility_unit IN('l','kg','kWh')),
 plausibility_currency_code text CHECK(plausibility_currency_code ~ '^[A-Z]{3}$'),version bigint NOT NULL DEFAULT 1 CHECK(version>0),updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(household_id,user_id),FOREIGN KEY(household_id,default_vehicle_id) REFERENCES fuel.vehicles(household_id,id),
 FOREIGN KEY(household_id,default_vehicle_id,default_series_id) REFERENCES fuel.odometer_series(household_id,vehicle_id,id),
 CHECK(default_series_id IS NULL OR default_vehicle_id IS NOT NULL),CHECK(minimum_consumption IS NULL OR maximum_consumption IS NULL OR minimum_consumption<=maximum_consumption));
CREATE TABLE fuel.import_jobs(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),vehicle_id uuid NOT NULL,odometer_series_id uuid,
 user_id uuid NOT NULL REFERENCES core.users(id),membership_id uuid NOT NULL REFERENCES core.memberships(id),generation bigint NOT NULL CHECK(generation>0),
 profile text NOT NULL,source_sha256 bytea NOT NULL CHECK(octet_length(source_sha256)=32),configuration jsonb NOT NULL,summary jsonb NOT NULL,
 state text NOT NULL DEFAULT 'staged' CHECK(state IN('staged','complete','expired')),version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 expires_at timestamptz NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,id),UNIQUE(household_id,user_id,generation,source_sha256,vehicle_id),
 FOREIGN KEY(household_id,vehicle_id) REFERENCES fuel.vehicles(household_id,id),
 FOREIGN KEY(household_id,vehicle_id,odometer_series_id) REFERENCES fuel.odometer_series(household_id,vehicle_id,id),
 CHECK(pg_column_size(configuration)<=65536 AND pg_column_size(summary)<=65536));
CREATE TABLE fuel.import_rows(
 job_id uuid NOT NULL,household_id uuid NOT NULL,row_number integer NOT NULL CHECK(row_number BETWEEN 1 AND 1000),
 source_fingerprint bytea NOT NULL CHECK(octet_length(source_fingerprint)=32),source_values jsonb NOT NULL DEFAULT '{}',normalized jsonb NOT NULL,errors jsonb NOT NULL DEFAULT '[]',warnings jsonb NOT NULL DEFAULT '[]',
 decision text NOT NULL DEFAULT 'pending' CHECK(decision IN('pending','import','skip')),record_id uuid,duplicate_of uuid,
 updated_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(job_id,row_number),FOREIGN KEY(household_id,job_id) REFERENCES fuel.import_jobs(household_id,id),
 CHECK(pg_column_size(source_values)<=32768 AND pg_column_size(normalized)<=32768 AND pg_column_size(errors)<=16384 AND pg_column_size(warnings)<=16384));
CREATE TABLE fuel.capture_jobs(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),vehicle_id uuid NOT NULL,odometer_series_id uuid,
 user_id uuid NOT NULL REFERENCES core.users(id),membership_id uuid NOT NULL REFERENCES core.memberships(id),session_id uuid NOT NULL,
 auth_version bigint NOT NULL CHECK(auth_version>0),generation bigint NOT NULL CHECK(generation>0),attachment_id uuid NOT NULL,
 attachment_sha256 bytea NOT NULL CHECK(octet_length(attachment_sha256)=32),source_text text NOT NULL DEFAULT '' CHECK(octet_length(source_text)<=65536),lease_token uuid,
 kind text NOT NULL CHECK(kind IN('receipt','odometer')),state text NOT NULL DEFAULT 'queued' CHECK(state IN('queued','running','review','complete','failed','expired')),
 suggestions jsonb NOT NULL DEFAULT '{}',warnings jsonb NOT NULL DEFAULT '[]',error_code text,version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),lease_until timestamptz,next_attempt_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL,record_id uuid,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,id),FOREIGN KEY(household_id,vehicle_id) REFERENCES fuel.vehicles(household_id,id),
 FOREIGN KEY(household_id,vehicle_id,odometer_series_id) REFERENCES fuel.odometer_series(household_id,vehicle_id,id),
 FOREIGN KEY(household_id,attachment_id) REFERENCES core.attachments(household_id,id),
 CHECK(pg_column_size(suggestions)<=131072 AND pg_column_size(warnings)<=65536));
CREATE INDEX capture_jobs_work ON fuel.capture_jobs(next_attempt_at,id) WHERE state IN('queued','running');
CREATE TABLE fuel.capture_transitions(
 transaction_id xid8 NOT NULL,job_id uuid NOT NULL,before_sha256 bytea NOT NULL CHECK(octet_length(before_sha256)=32),
 after_sha256 bytea NOT NULL CHECK(octet_length(after_sha256)=32),PRIMARY KEY(transaction_id,job_id));
REVOKE ALL ON fuel.capture_transitions FROM PUBLIC,household_app;
CREATE TABLE fuel.integration_bindings(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),vehicle_id uuid NOT NULL,
 source_entity text NOT NULL CHECK(source_entity IN('fillups','charging_sessions','vehicle_costs','maintenance_plans','vehicles')),
 source_id uuid NOT NULL,source_cycle bigint NOT NULL DEFAULT 0 CHECK(source_cycle>=0),
 target_module text NOT NULL CHECK(target_module IN('expenses','chores','documents')),target_entity text NOT NULL,target_id uuid NOT NULL,
 target_generation bigint NOT NULL CHECK(target_generation>0),relation text NOT NULL,state text NOT NULL DEFAULT 'linked' CHECK(state IN('linked','unlinked','target_unavailable')),
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 UNIQUE(household_id,id),UNIQUE(household_id,source_entity,source_id,source_cycle,target_module,relation),
 FOREIGN KEY(household_id,vehicle_id) REFERENCES fuel.vehicles(household_id,id),
 CHECK((target_module='expenses' AND target_entity='entries' AND relation='fuel_expense' AND source_entity IN('fillups','charging_sessions','vehicle_costs') AND source_cycle=0)
 OR(target_module='chores' AND target_entity='task_occurrences' AND relation='maintenance_task' AND source_entity='maintenance_plans' AND source_cycle>0)
 OR(target_module='documents' AND target_entity='documents' AND relation='vehicle_document/'||target_id::text AND source_entity='vehicles' AND source_cycle=0)));

CREATE TABLE fuel.household_state(household_id uuid PRIMARY KEY REFERENCES core.households(id),source_version bigint NOT NULL DEFAULT 0 CHECK(source_version>=0),updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE fuel.segment_state(
 household_id uuid NOT NULL,vehicle_id uuid NOT NULL,source_version bigint NOT NULL DEFAULT 0 CHECK(source_version>=0),built_version bigint NOT NULL DEFAULT -1,
 algorithm_version integer NOT NULL DEFAULT 1,checkpoint jsonb NOT NULL DEFAULT '{}' CHECK(pg_column_size(checkpoint)<=65536),
 cursor_series uuid,cursor_day date,cursor_odometer numeric(18,6),cursor_id uuid,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN('pending','building','ready','failed')),updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(household_id,vehicle_id),FOREIGN KEY(household_id,vehicle_id) REFERENCES fuel.vehicles(household_id,id));
CREATE TABLE fuel.consumption_segments(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL,vehicle_id uuid NOT NULL,odometer_series_id uuid NOT NULL,
 source_version bigint NOT NULL,algorithm_version integer NOT NULL,from_fillup_id uuid,to_fillup_id uuid,from_on date,to_on date NOT NULL,
 from_odometer numeric(18,6),to_odometer numeric(18,6),quantity numeric(48,6),distance numeric(48,6),
 quantity_unit text NOT NULL CHECK(quantity_unit IN('l','kg')),fuel_type text NOT NULL,
 state text NOT NULL CHECK(state IN('known','unknown')),reason text NOT NULL,below_precision boolean NOT NULL DEFAULT false,
 costs_minor jsonb NOT NULL DEFAULT '{}' CHECK(pg_column_size(costs_minor)<=65536 AND fuel.valid_cost_map(costs_minor)),
 FOREIGN KEY(household_id,vehicle_id,odometer_series_id) REFERENCES fuel.odometer_series(household_id,vehicle_id,id));
CREATE INDEX segments_window ON fuel.consumption_segments(household_id,vehicle_id,source_version,to_on,id);
CREATE TABLE fuel.segment_jobs(
 household_id uuid NOT NULL,vehicle_id uuid NOT NULL,target_version bigint NOT NULL,state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','running','failed')),
 attempts integer NOT NULL DEFAULT 0,next_attempt_at timestamptz NOT NULL DEFAULT now(),lease_until timestamptz,updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(household_id,vehicle_id),FOREIGN KEY(household_id,vehicle_id) REFERENCES fuel.vehicles(household_id,id));

CREATE TABLE fuel.module_operations(
 id uuid PRIMARY KEY,household_id uuid NOT NULL REFERENCES core.households(id),user_id uuid NOT NULL REFERENCES core.users(id),
 membership_id uuid NOT NULL REFERENCES core.memberships(id),session_id uuid NOT NULL,auth_version bigint NOT NULL,
 generation bigint NOT NULL CHECK(generation>0),operation_key uuid NOT NULL,action text NOT NULL,
 request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),dependencies jsonb NOT NULL,current_txid xid8 NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','complete')),effect_count integer NOT NULL DEFAULT 0 CHECK(effect_count BETWEEN 0 AND 4096),result jsonb,result_sha256 bytea CHECK(octet_length(result_sha256)=32),
 created_at timestamptz NOT NULL DEFAULT now(),completed_at timestamptz,
 UNIQUE(household_id,user_id,generation,operation_key),CHECK((state='complete')=(completed_at IS NOT NULL)),
 CHECK((result IS NULL)=(result_sha256 IS NULL)),CHECK(pg_column_size(dependencies)<=4096 AND (result IS NULL OR pg_column_size(result)<=1048576)));
CREATE TABLE fuel.module_effects(
 operation_id uuid NOT NULL REFERENCES fuel.module_operations(id),ordinal bigint GENERATED ALWAYS AS IDENTITY,
 relation text NOT NULL,record_id uuid NOT NULL,kind text NOT NULL CHECK(kind IN('INSERT','UPDATE','DELETE')),
 before_sha256 bytea CHECK(octet_length(before_sha256)=32),after_sha256 bytea CHECK(octet_length(after_sha256)=32),before_version bigint,after_version bigint,
 PRIMARY KEY(operation_id,ordinal));
CREATE TABLE fuel.legacy_operation_fences(
 household_id uuid NOT NULL REFERENCES core.households(id),user_id uuid NOT NULL REFERENCES core.users(id),generation bigint NOT NULL CHECK(generation>0),
 operation_key uuid NOT NULL,first_seen_at timestamptz NOT NULL,PRIMARY KEY(household_id,user_id,generation,operation_key));
INSERT INTO fuel.legacy_operation_fences(household_id,user_id,generation,operation_key,first_seen_at)
 SELECT household_id,user_id,module_generation,key,min(created_at) FROM core.idempotency_records
 WHERE module_id='fuel' AND module_generation>0 GROUP BY household_id,user_id,module_generation,key;

-- Private means dedicated authorized APIs, never a generic module descriptor.
GRANT SELECT,INSERT,UPDATE,DELETE ON fuel.odometer_observations,fuel.charging_sessions,fuel.trips,fuel.vehicle_costs,
 fuel.maintenance_plans,fuel.maintenance_events,fuel.user_settings,fuel.import_jobs,fuel.import_rows,fuel.capture_jobs,
 fuel.integration_bindings,fuel.household_state,fuel.segment_state,fuel.consumption_segments,fuel.segment_jobs TO household_app;
REVOKE ALL ON fuel.module_operations,fuel.module_effects FROM PUBLIC,household_app;
REVOKE ALL ON fuel.legacy_operation_fences FROM PUBLIC,household_app;
REVOKE INSERT,UPDATE,DELETE,TRUNCATE ON fuel.household_state,fuel.fuel_types FROM household_app;
REVOKE UPDATE,DELETE ON fuel.vehicle_costs,fuel.maintenance_events FROM household_app;

CREATE INDEX fillups_reading_guard ON fuel.fillups(household_id,vehicle_id,odometer_series_id,odometer DESC,occurred_on DESC,id DESC);
CREATE INDEX observations_reading_guard ON fuel.odometer_observations(household_id,vehicle_id,odometer_series_id,reading DESC,observed_on DESC,id DESC);
CREATE INDEX charging_reading_guard ON fuel.charging_sessions(household_id,vehicle_id,odometer_series_id,odometer DESC,occurred_on DESC,id DESC) WHERE odometer IS NOT NULL;
CREATE INDEX trips_reading_guard ON fuel.trips(household_id,vehicle_id,odometer_series_id,end_odometer DESC,ended_on DESC,id DESC);

COMMIT;
