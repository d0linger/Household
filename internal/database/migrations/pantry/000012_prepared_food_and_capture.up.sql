BEGIN;

-- Core26 is an intentional compatibility ABI for the accepted Shopping/Fuel
-- migrations. Extend only its reviewed audit census, never its version.
DO $predecessor$
DECLARE old_source text; expected_source text:=$old$
 SELECT core.financial_requirements_ready() AND count(*)=(88+(SELECT count(*) FROM core.audit_policies WHERE schema_name='shopping' AND table_name='barcodes')) AND bool_and(
 EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
 AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure))
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
 AND c.relname<>'schema_migrations' AND NOT(n.nspname='fuel' AND c.relname IN('fuel_types','household_state','segment_state','consumption_segments','segment_jobs','module_operations','module_effects','capture_transitions','legacy_operation_fences'))
 AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
 AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states',
 'financial_requirement_sets','financial_requirement_members','financial_artifact_scope_versions'))
 AND NOT EXISTS(SELECT 1 FROM core.audit_policies p WHERE p.schema_name=n.nspname AND p.table_name=c.relname)) FROM core.audit_policies p
$old$;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=26 AND NOT dirty) FROM core.schema_migrations) THEN
  RAISE EXCEPTION 'prepared food requires the reviewed core26 contract';
 END IF;
 SELECT replace(p.prosrc,chr(13),'') INTO old_source FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
 WHERE p.oid='core.audit_ready()'::regprocedure AND p.prokind='f' AND p.prosecdef AND p.provolatile='s'
 AND p.prorettype='boolean'::regtype AND p.proowner='household_owner'::regrole AND l.lanname='sql'
 AND p.proconfig=ARRAY['search_path=pg_catalog'];
 IF old_source IS DISTINCT FROM expected_source THEN RAISE EXCEPTION 'unrecognized prepared food audit predecessor'; END IF;
 IF EXISTS(SELECT 1 FROM core.units WHERE code='portion' AND(dimension<>'serving' OR label<>'Portionen' OR factor_to_base<>1)) THEN
  RAISE EXCEPTION 'unrecognized portion catalog predecessor';
 END IF;
END $predecessor$;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
INSERT INTO core.units(code,dimension,label,factor_to_base) VALUES('portion','serving','Portionen',1) ON CONFLICT(code) DO NOTHING;
DO $pack_preimage$
DECLARE expected text; actual text; valid boolean;
BEGIN
 CREATE TEMP TABLE household_known_pack_unit(unit_code text CHECK(unit_code IN('piece','g','kg','ml','l'))) ON COMMIT DROP;
 SELECT pg_get_constraintdef(oid) INTO expected FROM pg_constraint WHERE conrelid='pg_temp.household_known_pack_unit'::regclass AND contype='c';
 SELECT pg_get_constraintdef(k.oid),k.contype='c' AND k.convalidated AND k.conislocal AND k.coninhcount=0 AND NOT k.connoinherit
 AND NOT k.condeferrable AND NOT k.condeferred AND k.conkey=ARRAY[a.attnum]::smallint[]
 INTO actual,valid FROM pg_constraint k JOIN pg_attribute a ON a.attrelid=k.conrelid AND a.attname='unit_code' AND NOT a.attisdropped
 WHERE k.conrelid='pantry.packs'::regclass AND k.conname='packs_unit_code_check';
 IF valid IS DISTINCT FROM true OR actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'unrecognized existing pack unit predicate'; END IF;
END $pack_preimage$;
ALTER TABLE pantry.packs DROP CONSTRAINT packs_unit_code_check;
ALTER TABLE pantry.packs ADD CONSTRAINT packs_unit_code_check CHECK(unit_code IN('piece','g','kg','ml','l','portion'));

CREATE TABLE pantry.leftover_operations(
 id uuid PRIMARY KEY,household_id uuid NOT NULL REFERENCES core.households(id),user_id uuid NOT NULL REFERENCES core.users(id),
 generation bigint NOT NULL CHECK(generation>0),request_key uuid NOT NULL,request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 state text NOT NULL CHECK(state IN('pending','complete','refused')),
 source_kind text NOT NULL CHECK(source_kind IN('manual','recipe_version','cooking','cancel')),
 source_id uuid,recipes_generation bigint,source_version_id uuid,source_cap numeric(18,6),source_prepared_on date,
 transaction_id xid8 NOT NULL DEFAULT pg_current_xact_id(),
 result jsonb CHECK(result IS NULL OR(jsonb_typeof(result)='object' AND octet_length(result::text)<=262144)),
 result_sha256 bytea CHECK(result_sha256 IS NULL OR octet_length(result_sha256)=32),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),finished_at timestamptz,
 UNIQUE(household_id,id),UNIQUE(household_id,user_id,generation,request_key),
 CHECK((result IS NULL)=(result_sha256 IS NULL)),
 CHECK((source_kind IN('manual','cancel') AND source_id IS NULL AND recipes_generation IS NULL AND source_version_id IS NULL AND source_cap IS NULL AND source_prepared_on IS NULL)
  OR(source_kind IN('recipe_version','cooking') AND source_id IS NOT NULL AND recipes_generation>0 AND source_version_id IS NOT NULL
   AND(source_kind='recipe_version' AND source_cap IS NULL AND source_prepared_on IS NULL OR source_kind='cooking'
   AND(source_cap IS NOT NULL AND source_cap>0 AND source_prepared_on IS NOT NULL OR state='complete' AND result IS NULL AND result_sha256 IS NULL)))),
 CHECK(state='pending' AND finished_at IS NULL OR state IN('complete','refused') AND finished_at IS NOT NULL)
);
CREATE UNIQUE INDEX leftover_once_cooking ON pantry.leftover_operations(household_id,recipes_generation,source_id)
 WHERE source_kind='cooking' AND state IN('pending','complete');
CREATE INDEX leftover_actor ON pantry.leftover_operations(household_id,user_id,created_at DESC,id DESC);
REVOKE ALL ON pantry.leftover_operations FROM PUBLIC,household_app;

CREATE TABLE pantry.prepared_lots(
 household_id uuid NOT NULL REFERENCES core.households(id),lot_id uuid NOT NULL,operation_id uuid NOT NULL,line_id uuid NOT NULL,movement_id uuid NOT NULL,
 prepared_on date NOT NULL,initial_portions numeric(18,6) NOT NULL CHECK(initial_portions>0),
 prepared_portions numeric(18,6),portions_unknown boolean NOT NULL,
 source_kind text NOT NULL CHECK(source_kind IN('manual','recipe_version','cooking')),
 dish_name text NOT NULL CHECK(length(dish_name)>0 AND length(dish_name)<=200),
 label text NOT NULL CHECK(length(label)>0 AND length(label)<=200),location_id uuid NOT NULL,
 location_name text NOT NULL CHECK(length(location_name)>0 AND length(location_name)<=100),received_on date NOT NULL,best_before_on date,use_by_on date,
 PRIMARY KEY(household_id,lot_id),UNIQUE(household_id,operation_id,line_id),UNIQUE(household_id,movement_id),
 FOREIGN KEY(household_id,lot_id) REFERENCES pantry.lots(household_id,id),
 FOREIGN KEY(household_id,operation_id) REFERENCES pantry.leftover_operations(household_id,id),
 FOREIGN KEY(household_id,movement_id) REFERENCES pantry.stock_movements(household_id,id),
 CHECK(portions_unknown AND prepared_portions IS NULL OR NOT portions_unknown AND prepared_portions IS NOT NULL AND prepared_portions>0)
);
REVOKE ALL ON pantry.prepared_lots FROM PUBLIC,household_app;
GRANT SELECT,INSERT ON pantry.prepared_lots TO household_app;
CREATE TRIGGER prepared_lot_immutable BEFORE UPDATE OR DELETE ON pantry.prepared_lots FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();

-- A transfer is a descendant of one original preparation, never another
-- preparation receipt or another allocation against its original portion cap.
CREATE TABLE pantry.prepared_transfers(
 household_id uuid NOT NULL REFERENCES core.households(id),lot_id uuid NOT NULL,root_lot_id uuid NOT NULL,source_lot_id uuid NOT NULL,
 movement_out_id uuid NOT NULL,movement_in_id uuid NOT NULL,correlation_id uuid NOT NULL,
 quantity numeric(18,6) NOT NULL CHECK(quantity>0),created_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
 PRIMARY KEY(household_id,lot_id),UNIQUE(household_id,movement_out_id),UNIQUE(household_id,movement_in_id),UNIQUE(household_id,correlation_id),
 FOREIGN KEY(household_id,lot_id) REFERENCES pantry.lots(household_id,id),
 FOREIGN KEY(household_id,source_lot_id) REFERENCES pantry.lots(household_id,id),
 FOREIGN KEY(household_id,root_lot_id) REFERENCES pantry.prepared_lots(household_id,lot_id),
 FOREIGN KEY(household_id,movement_out_id) REFERENCES pantry.stock_movements(household_id,id),
 FOREIGN KEY(household_id,movement_in_id) REFERENCES pantry.stock_movements(household_id,id),
 CHECK(lot_id<>source_lot_id AND lot_id<>root_lot_id AND movement_out_id<>movement_in_id)
);
CREATE INDEX prepared_transfer_root ON pantry.prepared_transfers(household_id,root_lot_id,lot_id);
REVOKE ALL ON pantry.prepared_transfers FROM PUBLIC,household_app;
GRANT SELECT ON pantry.prepared_transfers TO household_app;
CREATE TRIGGER prepared_transfer_immutable BEFORE UPDATE OR DELETE ON pantry.prepared_transfers FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();

-- Private Core-owned source boundary. No optional business FK blocks a source
-- purge. Source identity is a retained fence, not a copied private recipe.
CREATE FUNCTION core.pantry_leftover_source(p_household uuid,p_kind text,p_source uuid,p_generation bigint,p_version uuid)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE cap numeric; found_version uuid; prepared date;
BEGIN
 IF p_kind='manual' THEN RETURN jsonb_build_object('cap',NULL,'prepared_on',NULL); END IF;
 PERFORM 1 FROM core.module_states WHERE household_id=p_household AND module_id='recipes' AND state='enabled' AND data_generation=p_generation FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'recipe source generation unavailable' USING ERRCODE='42501'; END IF;
 IF p_kind='cooking' THEN
  SELECT c.actual_servings,c.source_version_id,c.cooked_on INTO cap,found_version,prepared FROM recipes.cooking_records c
  JOIN recipes.cooking_sessions s ON s.household_id=c.household_id AND s.id=c.session_id AND s.state='completed'
  WHERE c.household_id=p_household AND c.id=p_source;
 ELSE
  SELECT NULL::numeric,v.id INTO cap,found_version FROM recipes.versions v WHERE v.household_id=p_household AND v.id=p_source;
 END IF;
 IF found_version IS NULL OR found_version IS DISTINCT FROM p_version THEN
  RAISE EXCEPTION 'reviewed recipe source unavailable' USING ERRCODE='23514';
 END IF;
 RETURN jsonb_build_object('cap',cap,'prepared_on',prepared);
END $$;
REVOKE ALL ON FUNCTION core.pantry_leftover_source(uuid,text,uuid,bigint,uuid) FROM PUBLIC,household_app;

CREATE FUNCTION pantry.authorize_leftovers(p_generation bigint,p_session uuid,p_auth bigint,p_membership uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE h uuid:=nullif(current_setting('household.audit_household',true),'')::uuid;
 u uuid:=nullif(current_setting('household.audit_user',true),'')::uuid;
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' OR current_setting('household.audit_kind',true) IS DISTINCT FROM 'user' OR h IS NULL OR u IS NULL THEN
  RAISE EXCEPTION 'current leftover actor transaction required' USING ERRCODE='42501';
 END IF;
 PERFORM 1 FROM core.households WHERE id=h FOR SHARE;
 PERFORM 1 FROM core.memberships m JOIN core.users a ON a.id=m.user_id
 JOIN core.sessions s ON s.id=p_session AND s.user_id=a.id AND s.auth_version=a.auth_version
 WHERE m.id=p_membership AND m.household_id=h AND m.user_id=u AND m.role IN('admin','editor') AND m.inactive_at IS NULL
 AND a.disabled_at IS NULL AND NOT a.must_change_password AND a.auth_version=p_auth AND s.revoked_at IS NULL
 AND s.idle_expires_at>clock_timestamp() AND s.absolute_expires_at>clock_timestamp() FOR SHARE OF m,a,s;
 IF NOT FOUND THEN RAISE EXCEPTION 'leftover current actor unavailable' USING ERRCODE='42501'; END IF;
 PERFORM 1 FROM core.module_states WHERE household_id=h AND module_id='pantry' AND state='enabled' AND data_generation=p_generation FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'leftover generation unavailable' USING ERRCODE='42501'; END IF;
END $$;
REVOKE ALL ON FUNCTION pantry.authorize_leftovers(bigint,uuid,bigint,uuid) FROM PUBLIC,household_app;

CREATE FUNCTION pantry.prepared_transfer_evidence(p_household uuid,p_lot uuid,p_root uuid,p_source uuid,
 p_out uuid,p_in uuid,p_correlation uuid,p_quantity numeric,p_actor uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT p_quantity>0 AND EXISTS(SELECT 1 FROM pantry.prepared_lots original
 JOIN pantry.lots source ON source.household_id=original.household_id AND source.id=p_source
 JOIN pantry.lots target ON target.household_id=source.household_id AND target.id=p_lot AND target.product_id=source.product_id AND target.location_id<>source.location_id
 JOIN pantry.lots root ON root.household_id=original.household_id AND root.id=original.lot_id AND root.product_id=source.product_id
 JOIN pantry.products product ON product.household_id=root.household_id AND product.id=root.product_id AND product.unit_code='portion'
 JOIN pantry.stock_movements outgoing ON outgoing.household_id=source.household_id AND outgoing.id=p_out AND outgoing.lot_id=source.id
 JOIN pantry.stock_movements incoming ON incoming.household_id=target.household_id AND incoming.id=p_in AND incoming.lot_id=target.id
 WHERE original.household_id=p_household AND original.lot_id=p_root AND target.id<>source.id AND target.id<>root.id
 AND target.created_by=p_actor AND outgoing.created_by=p_actor AND incoming.created_by=p_actor
 AND outgoing.reason='transfer' AND incoming.reason='transfer' AND outgoing.quantity_delta=-p_quantity AND incoming.quantity_delta=p_quantity
 AND outgoing.correlation_id=p_correlation AND incoming.correlation_id=p_correlation AND outgoing.id<>incoming.id
 AND NOT EXISTS(SELECT 1 FROM pantry.prepared_lots other WHERE other.household_id=p_household AND other.lot_id=target.id)
 AND(source.id=root.id OR EXISTS(SELECT 1 FROM pantry.prepared_transfers prior
 WHERE prior.household_id=p_household AND prior.lot_id=source.id AND prior.root_lot_id=root.id)))
$$;
REVOKE ALL ON FUNCTION pantry.prepared_transfer_evidence(uuid,uuid,uuid,uuid,uuid,uuid,uuid,numeric,uuid) FROM PUBLIC,household_app;

CREATE FUNCTION pantry.guard_prepared_transfer() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' OR current_setting('household.audit_kind',true) IS DISTINCT FROM 'user'
 OR NEW.household_id IS DISTINCT FROM nullif(current_setting('household.audit_household',true),'')::uuid
 OR NEW.created_by IS DISTINCT FROM nullif(current_setting('household.audit_user',true),'')::uuid
 OR pantry.prepared_transfer_evidence(NEW.household_id,NEW.lot_id,NEW.root_lot_id,NEW.source_lot_id,
 NEW.movement_out_id,NEW.movement_in_id,NEW.correlation_id,NEW.quantity,NEW.created_by) IS DISTINCT FROM true THEN
  RAISE EXCEPTION 'prepared transfer requires actual same-household paired journal evidence' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_prepared_transfer() FROM PUBLIC,household_app;
CREATE TRIGGER prepared_transfer_guard BEFORE INSERT ON pantry.prepared_transfers FOR EACH ROW EXECUTE FUNCTION pantry.guard_prepared_transfer();

CREATE FUNCTION pantry.record_prepared_transfer(p_source uuid,p_target uuid,p_correlation uuid,p_quantity numeric,
 p_generation bigint,p_session uuid,p_auth bigint,p_membership uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE h uuid:=nullif(current_setting('household.audit_household',true),'')::uuid;
 u uuid:=nullif(current_setting('household.audit_user',true),'')::uuid; root uuid; outgoing uuid; incoming uuid;
BEGIN
 PERFORM pantry.authorize_leftovers(p_generation,p_session,p_auth,p_membership);
 SELECT lot_id INTO root FROM pantry.prepared_lots WHERE household_id=h AND lot_id=p_source;
 IF root IS NULL THEN SELECT root_lot_id INTO root FROM pantry.prepared_transfers WHERE household_id=h AND lot_id=p_source; END IF;
 IF root IS NULL THEN RETURN false; END IF;
 IF p_quantity IS NULL OR p_quantity<=0 OR p_quantity<>round(p_quantity,6) OR p_quantity>=1000000000000 THEN
  RAISE EXCEPTION 'exact positive prepared transfer quantity required' USING ERRCODE='23514'; END IF;
 SELECT id INTO outgoing FROM pantry.stock_movements WHERE household_id=h AND lot_id=p_source
 AND reason='transfer' AND correlation_id=p_correlation AND quantity_delta=-p_quantity AND created_by=u ORDER BY id LIMIT 1;
 SELECT id INTO incoming FROM pantry.stock_movements WHERE household_id=h AND lot_id=p_target
 AND reason='transfer' AND correlation_id=p_correlation AND quantity_delta=p_quantity AND created_by=u ORDER BY id LIMIT 1;
 IF outgoing IS NULL OR incoming IS NULL THEN RAISE EXCEPTION 'actual prepared transfer journal pair required' USING ERRCODE='23514'; END IF;
 INSERT INTO pantry.prepared_transfers(household_id,lot_id,root_lot_id,source_lot_id,movement_out_id,movement_in_id,correlation_id,quantity,created_by)
 VALUES(h,p_target,root,p_source,outgoing,incoming,p_correlation,p_quantity,u);
 RETURN true;
END $$;
REVOKE ALL ON FUNCTION pantry.record_prepared_transfer(uuid,uuid,uuid,numeric,bigint,uuid,bigint,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.record_prepared_transfer(uuid,uuid,uuid,numeric,bigint,uuid,bigint,uuid) TO household_app;

CREATE FUNCTION pantry.check_prepared_transfer() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pantry.prepared_transfers p WHERE p.household_id=NEW.household_id AND p.lot_id=NEW.lot_id
 AND pantry.prepared_transfer_evidence(p.household_id,p.lot_id,p.root_lot_id,p.source_lot_id,
 p.movement_out_id,p.movement_in_id,p.correlation_id,p.quantity,p.created_by)) THEN
  RAISE EXCEPTION 'prepared transfer final journal evidence changed' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION pantry.check_prepared_transfer() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER prepared_transfer_complete AFTER INSERT ON pantry.prepared_transfers
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pantry.check_prepared_transfer();

CREATE FUNCTION pantry.leftover_source_used(p_household uuid,p_generation bigint,p_source uuid)
RETURNS boolean LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF p_household IS DISTINCT FROM nullif(current_setting('household.audit_household',true),'')::uuid
 OR current_setting('household.audit_kind',true) IS DISTINCT FROM 'user' THEN RAISE EXCEPTION 'current household required' USING ERRCODE='42501'; END IF;
 RETURN EXISTS(SELECT 1 FROM pantry.leftover_operations WHERE household_id=p_household AND recipes_generation=p_generation
 AND source_kind='cooking' AND source_id=p_source AND state='complete');
END $$;
REVOKE ALL ON FUNCTION pantry.leftover_source_used(uuid,bigint,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.leftover_source_used(uuid,bigint,uuid) TO household_app;

CREATE FUNCTION pantry.begin_leftovers(p_id uuid,p_key uuid,p_generation bigint,p_hash bytea,p_kind text,p_source text,p_recipes_generation bigint,p_version text,
 p_session uuid,p_auth bigint,p_membership uuid) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE h uuid:=nullif(current_setting('household.audit_household',true),'')::uuid;
 u uuid:=nullif(current_setting('household.audit_user',true),'')::uuid; prior pantry.leftover_operations%ROWTYPE; source jsonb;
BEGIN
 PERFORM pantry.authorize_leftovers(p_generation,p_session,p_auth,p_membership);
 IF p_id IS NULL OR p_key IS NULL OR p_hash IS NULL OR octet_length(p_hash)<>32 OR p_kind NOT IN('manual','recipe_version','cooking') THEN
  RAISE EXCEPTION 'invalid leftover operation' USING ERRCODE='23514'; END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('pantry-leftovers-key/'||h||'/'||u||'/'||p_generation||'/'||p_key,0)) THEN
  RAISE EXCEPTION 'leftover operation in progress' USING ERRCODE='55P03'; END IF;
 SELECT * INTO prior FROM pantry.leftover_operations WHERE household_id=h AND user_id=u AND generation=p_generation AND request_key=p_key;
 IF FOUND THEN
  IF prior.state<>'complete' OR prior.request_hash IS DISTINCT FROM p_hash OR prior.result IS NULL
   OR sha256(convert_to(prior.result::text,'UTF8')) IS DISTINCT FROM prior.result_sha256 OR NOT pantry.leftover_completion_valid(prior.id) THEN
   RAISE EXCEPTION 'leftover key unavailable; inspect original operation' USING ERRCODE='23514'; END IF;
  RETURN jsonb_build_object('replay',true,'result',prior.result);
 END IF;
 IF p_kind='manual' AND(p_source<>'' OR p_version<>'' OR p_recipes_generation<>0) THEN RAISE EXCEPTION 'manual source mismatch' USING ERRCODE='23514'; END IF;
 IF p_kind='cooking' THEN
  IF NOT pg_try_advisory_xact_lock(hashtextextended('pantry-leftovers-source/'||h||'/'||p_recipes_generation||'/'||p_source,0)) THEN
   RAISE EXCEPTION 'cooking source in progress' USING ERRCODE='55P03'; END IF;
  IF EXISTS(SELECT 1 FROM pantry.leftover_operations WHERE household_id=h AND source_kind='cooking' AND recipes_generation=p_recipes_generation AND source_id=p_source::uuid AND state IN('pending','complete')) THEN
   RAISE EXCEPTION 'cooking source already recorded' USING ERRCODE='23514'; END IF;
 END IF;
 source:=core.pantry_leftover_source(h,p_kind,nullif(p_source,'')::uuid,p_recipes_generation,nullif(p_version,'')::uuid);
 INSERT INTO pantry.leftover_operations(id,household_id,user_id,generation,request_key,request_hash,state,source_kind,source_id,recipes_generation,source_version_id,source_cap,source_prepared_on)
 VALUES(p_id,h,u,p_generation,p_key,p_hash,'pending',p_kind,nullif(p_source,'')::uuid,nullif(p_recipes_generation,0),nullif(p_version,'')::uuid,
 (source->>'cap')::numeric,(source->>'prepared_on')::date);
 RETURN jsonb_build_object('replay',false,'operation_id',p_id);
END $$;
REVOKE ALL ON FUNCTION pantry.begin_leftovers(uuid,uuid,bigint,bytea,text,text,bigint,text,uuid,bigint,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.begin_leftovers(uuid,uuid,bigint,bytea,text,text,bigint,text,uuid,bigint,uuid) TO household_app;

CREATE FUNCTION pantry.guard_prepared_lot() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 -- Read through a private checker: runtime roles cannot inspect operation data.
 IF NOT pantry.prepared_lot_pending(NEW.household_id,NEW.operation_id,NEW.lot_id,NEW.movement_id,NEW.initial_portions,NEW.prepared_on,NEW.prepared_portions,NEW.portions_unknown) THEN
  RAISE EXCEPTION 'prepared lot source or actual movement mismatch' USING ERRCODE='23514'; END IF;
 IF NOT pantry.prepared_lot_source_kind(NEW.household_id,NEW.operation_id,NEW.source_kind) THEN
  RAISE EXCEPTION 'prepared source kind mismatch' USING ERRCODE='23514'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pantry.lots l WHERE l.household_id=NEW.household_id AND l.id=NEW.lot_id AND l.notes=NEW.label
 AND l.location_id=NEW.location_id AND l.received_on=NEW.received_on
 AND l.best_before_on IS NOT DISTINCT FROM NEW.best_before_on AND l.use_by_on IS NOT DISTINCT FROM NEW.use_by_on) THEN
  RAISE EXCEPTION 'prepared metadata must match its actual created lot' USING ERRCODE='23514'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pantry.locations WHERE household_id=NEW.household_id AND id=NEW.location_id AND name=NEW.location_name) THEN
  RAISE EXCEPTION 'prepared location label mismatch' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_prepared_lot() FROM PUBLIC,household_app;
CREATE TRIGGER prepared_lot_guard BEFORE INSERT ON pantry.prepared_lots FOR EACH ROW EXECUTE FUNCTION pantry.guard_prepared_lot();

CREATE FUNCTION pantry.prepared_lot_pending(p_household uuid,p_op uuid,p_lot uuid,p_movement uuid,p_quantity numeric,p_prepared date,p_cap numeric,p_unknown boolean)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT EXISTS(SELECT 1 FROM pantry.leftover_operations o
 JOIN pantry.lots l ON l.household_id=o.household_id AND l.id=p_lot JOIN pantry.products p ON p.household_id=l.household_id AND p.id=l.product_id
 JOIN pantry.locations loc ON loc.household_id=l.household_id AND loc.id=l.location_id
 JOIN pantry.stock_movements m ON m.household_id=l.household_id AND m.lot_id=l.id AND m.id=p_movement
 WHERE o.household_id=p_household AND o.id=p_op AND o.state='pending' AND o.transaction_id=pg_current_xact_id()
 AND o.user_id=nullif(current_setting('household.audit_user',true),'')::uuid
 AND o.household_id=nullif(current_setting('household.audit_household',true),'')::uuid
 AND l.created_by=o.user_id AND m.created_by=o.user_id AND p.unit_code='portion' AND p.archived_at IS NULL AND loc.archived_at IS NULL
 AND m.reason='receive' AND m.quantity_delta=p_quantity AND m.correlation_id=o.id AND p_prepared<=l.received_on
 AND(o.source_cap IS NULL OR NOT p_unknown AND p_cap=o.source_cap AND p_prepared=o.source_prepared_on))
$$;
REVOKE ALL ON FUNCTION pantry.prepared_lot_pending(uuid,uuid,uuid,uuid,numeric,date,numeric,boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.prepared_lot_pending(uuid,uuid,uuid,uuid,numeric,date,numeric,boolean) TO household_app;

CREATE FUNCTION pantry.prepared_lot_source_kind(p_household uuid,p_id uuid,p_kind text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT EXISTS(SELECT 1 FROM pantry.leftover_operations WHERE household_id=p_household AND id=p_id AND state='pending'
 AND transaction_id=pg_current_xact_id() AND source_kind=p_kind
 AND user_id=nullif(current_setting('household.audit_user',true),'')::uuid
 AND household_id=nullif(current_setting('household.audit_household',true),'')::uuid)
$$;
REVOKE ALL ON FUNCTION pantry.prepared_lot_source_kind(uuid,uuid,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.prepared_lot_source_kind(uuid,uuid,text) TO household_app;

CREATE FUNCTION core.guard_pantry_leftover_link() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF NEW.relation<>'prepared_leftovers' AND(TG_OP='INSERT' OR OLD.relation<>'prepared_leftovers') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' OR NEW.source_module<>'recipes' OR NEW.target_module<>'pantry' OR NEW.target_type<>'lots'
 OR NOT EXISTS(SELECT 1 FROM pantry.prepared_lots p JOIN pantry.leftover_operations o ON o.household_id=p.household_id AND o.id=p.operation_id
 WHERE p.household_id=NEW.household_id AND p.lot_id=NEW.target_id AND o.state='pending' AND o.transaction_id=pg_current_xact_id()
 AND o.user_id=nullif(current_setting('household.audit_user',true),'')::uuid
 AND o.household_id=nullif(current_setting('household.audit_household',true),'')::uuid
 AND o.source_id=NEW.source_id AND(o.source_kind='cooking' AND NEW.source_type='cooking_records'
 OR o.source_kind='recipe_version' AND NEW.source_type='versions')) THEN
  RAISE EXCEPTION 'prepared source link requires its actual pending operation' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION core.guard_pantry_leftover_link() FROM PUBLIC,household_app;
CREATE TRIGGER pantry_leftover_link BEFORE INSERT OR UPDATE ON core.resource_links FOR EACH ROW EXECUTE FUNCTION core.guard_pantry_leftover_link();

CREATE FUNCTION pantry.leftover_completion_valid(p_id uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT coalesce((SELECT o.state='complete' AND o.result IS NOT NULL AND sha256(convert_to(o.result::text,'UTF8'))=o.result_sha256
 AND jsonb_typeof(o.result->'lines')='array' AND jsonb_array_length(o.result->'lines')>=1 AND jsonb_array_length(o.result->'lines')<=20
 AND o.result->>'operation_id'=o.id::text AND(o.result->>'generation')::bigint=o.generation
 AND o.result->>'id'=o.id::text AND o.result->>'request_key'=o.request_key::text AND o.result->>'unit'='portion'
 AND jsonb_typeof(o.result->'name')='string'
 AND(SELECT count(*) FROM pantry.prepared_lots r WHERE r.household_id=o.household_id AND r.operation_id=o.id)=jsonb_array_length(o.result->'lines')
 AND(SELECT count(*) FROM pantry.stock_movements m WHERE m.household_id=o.household_id AND m.correlation_id=o.id)=jsonb_array_length(o.result->'lines')
 AND NOT EXISTS(SELECT 1 FROM pantry.prepared_lots r LEFT JOIN pantry.stock_movements m ON m.household_id=r.household_id AND m.id=r.movement_id
 LEFT JOIN pantry.lots l ON l.household_id=r.household_id AND l.id=r.lot_id
 WHERE r.household_id=o.household_id AND r.operation_id=o.id AND(m.id IS NULL OR l.id IS NULL OR m.reason<>'receive' OR m.quantity_delta<>r.initial_portions
 OR m.correlation_id<>o.id OR m.lot_id<>r.lot_id OR m.created_by<>o.user_id OR l.created_by<>o.user_id
 OR o.result->>'name' IS DISTINCT FROM r.dish_name
 OR(o.result->>'prepared_on')::date IS DISTINCT FROM r.prepared_on
 OR(o.transaction_id=pg_current_xact_id() AND(l.notes<>r.label OR l.location_id<>r.location_id OR l.received_on<>r.received_on
 OR l.best_before_on IS DISTINCT FROM r.best_before_on OR l.use_by_on IS DISTINCT FROM r.use_by_on))
 OR(o.source_cap IS NOT NULL AND(r.prepared_portions IS DISTINCT FROM o.source_cap OR r.portions_unknown))
 OR NOT EXISTS(SELECT 1 FROM jsonb_array_elements(o.result->'lines') x WHERE x->>'line_id'=r.line_id::text AND x->>'lot_id'=r.lot_id::text
 AND x->>'movement_id'=m.id::text AND x->>'location_id'=r.location_id::text AND x->>'location'=r.location_name
 AND x->>'label'=r.label AND(x->>'quantity')::numeric=r.initial_portions)))
 AND(SELECT count(DISTINCT x->>'line_id') FROM jsonb_array_elements(o.result->'lines') x)=jsonb_array_length(o.result->'lines')
 AND(SELECT sum(initial_portions) FROM pantry.prepared_lots WHERE household_id=o.household_id AND operation_id=o.id)=(o.result->>'total_portions')::numeric
 AND(SELECT sum(initial_portions) FROM pantry.prepared_lots WHERE household_id=o.household_id AND operation_id=o.id)<1000000000000
 AND NOT EXISTS(SELECT 1 FROM pantry.prepared_lots WHERE household_id=o.household_id AND operation_id=o.id
 HAVING count(DISTINCT prepared_portions)>1 OR count(DISTINCT portions_unknown)>1 OR
 sum(initial_portions)>min(prepared_portions))
 FROM pantry.leftover_operations o WHERE o.id=p_id),false)
$$;
REVOKE ALL ON FUNCTION pantry.leftover_completion_valid(uuid) FROM PUBLIC,household_app;

CREATE FUNCTION pantry.finish_leftovers(p_id uuid,p_result jsonb) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE op pantry.leftover_operations%ROWTYPE; accepted timestamptz;
BEGIN
 SELECT * INTO op FROM pantry.leftover_operations WHERE id=p_id FOR UPDATE;
 IF NOT FOUND OR op.state<>'pending' OR op.transaction_id<>pg_current_xact_id()
 OR op.user_id IS DISTINCT FROM nullif(current_setting('household.audit_user',true),'')::uuid
 OR op.household_id IS DISTINCT FROM nullif(current_setting('household.audit_household',true),'')::uuid
 OR jsonb_typeof(p_result)<>'object' OR octet_length(p_result::text)>262144 THEN
  RAISE EXCEPTION 'invalid leftover completion' USING ERRCODE='23514'; END IF;
 IF NOT(p_result ?& ARRAY['id','operation_id','request_key','generation','name','prepared_on','total_portions','unit','created_at','lines','details_available'])
 OR EXISTS(SELECT 1 FROM jsonb_object_keys(p_result) k WHERE k NOT IN('id','operation_id','request_key','generation','name','prepared_on','total_portions','unit','created_at','lines','details_available'))
 OR jsonb_typeof(p_result->'lines') IS DISTINCT FROM 'array'
 OR EXISTS(SELECT 1 FROM unnest(ARRAY['id','operation_id','request_key','name','prepared_on','total_portions','unit','created_at']) k
 WHERE jsonb_typeof(p_result->k) IS DISTINCT FROM 'string')
 OR jsonb_typeof(p_result->'generation') IS DISTINCT FROM 'number'
 OR p_result->'details_available' IS DISTINCT FROM 'true'::jsonb THEN RAISE EXCEPTION 'closed typed leftover receipt required' USING ERRCODE='23514'; END IF;
 accepted:=(p_result->>'created_at')::timestamptz;
 IF NOT isfinite(accepted) OR accepted<op.created_at OR accepted>clock_timestamp() THEN
  RAISE EXCEPTION 'actual leftover completion timestamp required' USING ERRCODE='23514'; END IF;
 IF EXISTS(SELECT 1 FROM jsonb_array_elements(p_result->'lines') x WHERE jsonb_typeof(x)<>'object'
 OR NOT(x ?& ARRAY['line_id','lot_id','movement_id','label','quantity','location_id','location'])
 OR EXISTS(SELECT 1 FROM jsonb_object_keys(x) k WHERE k NOT IN('line_id','lot_id','movement_id','label','quantity','location_id','location'))
 OR EXISTS(SELECT 1 FROM unnest(ARRAY['line_id','lot_id','movement_id','label','quantity','location_id','location']) k
 WHERE jsonb_typeof(x->k) IS DISTINCT FROM 'string')) THEN RAISE EXCEPTION 'closed exact leftover line required' USING ERRCODE='23514'; END IF;
 UPDATE pantry.leftover_operations SET state='complete',result=p_result,result_sha256=sha256(convert_to(p_result::text,'UTF8')),finished_at=clock_timestamp() WHERE id=p_id;
 IF NOT pantry.leftover_completion_valid(p_id) THEN RAISE EXCEPTION 'incomplete leftover effects' USING ERRCODE='23514'; END IF;
END $$;
REVOKE ALL ON FUNCTION pantry.finish_leftovers(uuid,jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.finish_leftovers(uuid,jsonb) TO household_app;

CREATE FUNCTION pantry.leftover_operation_status(p_key uuid,p_generation bigint,p_abandon boolean,p_session uuid,p_auth bigint,p_membership uuid)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE h uuid:=nullif(current_setting('household.audit_household',true),'')::uuid;
 u uuid:=nullif(current_setting('household.audit_user',true),'')::uuid; prior pantry.leftover_operations%ROWTYPE;
BEGIN
 PERFORM pantry.authorize_leftovers(p_generation,p_session,p_auth,p_membership);
 IF p_key IS NULL THEN RAISE EXCEPTION 'operation key required' USING ERRCODE='23514'; END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('pantry-leftovers-key/'||h||'/'||u||'/'||p_generation||'/'||p_key,0)) THEN
  RAISE EXCEPTION 'leftover operation in progress' USING ERRCODE='55P03'; END IF;
 SELECT * INTO prior FROM pantry.leftover_operations WHERE household_id=h AND user_id=u AND generation=p_generation AND request_key=p_key;
 IF NOT FOUND AND p_abandon THEN
  INSERT INTO pantry.leftover_operations(id,household_id,user_id,generation,request_key,request_hash,state,source_kind,finished_at)
  VALUES(gen_random_uuid(),h,u,p_generation,p_key,decode(repeat('00',32),'hex'),'refused','cancel',clock_timestamp()) RETURNING * INTO prior;
 END IF;
 IF prior.id IS NULL THEN RETURN jsonb_build_object('state','unknown','message','Kein bestätigter Ausgang; die ursprüngliche Kennung bleibt gesperrt.'); END IF;
 IF prior.state='refused' THEN RETURN jsonb_build_object('state','refused','result',jsonb_build_object('operation_id',prior.id,'details_available',false)); END IF;
 IF prior.state='complete' AND prior.result IS NOT NULL AND pantry.leftover_completion_valid(prior.id) THEN RETURN jsonb_build_object('state','complete','result',prior.result); END IF;
 RETURN jsonb_build_object('state','unknown','message','Der frühere Vorgang ist erhalten, sein Ergebnis derzeit nicht überprüfbar. Keine neue Kennung verwenden.');
END $$;
REVOKE ALL ON FUNCTION pantry.leftover_operation_status(uuid,bigint,boolean,uuid,bigint,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.leftover_operation_status(uuid,bigint,boolean,uuid,bigint,uuid) TO household_app;

CREATE FUNCTION pantry.check_leftover_completion() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE op_id uuid;
BEGIN
 IF TG_TABLE_NAME='leftover_operations' THEN op_id=NEW.id; ELSE op_id=NEW.operation_id; END IF;
 IF session_user='household_owner' AND current_setting('household.audit_kind',true)='operator' AND EXISTS(
 SELECT 1 FROM pantry.leftover_operations o JOIN core.module_states s ON s.household_id=o.household_id AND s.module_id='pantry'
 WHERE o.id=op_id AND o.state='complete' AND o.result IS NULL AND o.result_sha256 IS NULL AND s.state='disabled' AND s.data_generation>o.generation) THEN RETURN NULL; END IF;
 IF EXISTS(SELECT 1 FROM pantry.leftover_operations WHERE id=op_id AND state='refused') THEN RETURN NULL; END IF;
 IF NOT pantry.leftover_completion_valid(op_id) THEN RAISE EXCEPTION 'unfinished or damaged leftover operation' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION pantry.check_leftover_completion() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER leftover_operation_complete AFTER INSERT OR UPDATE ON pantry.leftover_operations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pantry.check_leftover_completion();
CREATE CONSTRAINT TRIGGER leftover_lot_complete AFTER INSERT ON pantry.prepared_lots
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pantry.check_leftover_completion();

-- PA13 metadata never creates stock: adoption receipts and source snapshots
-- complete independently from the normal reviewed intake journal.

CREATE TABLE pantry.foodfacts_cache (
 household_id uuid NOT NULL REFERENCES core.households(id), generation bigint NOT NULL CHECK(generation>0),
 provider text NOT NULL CHECK(provider IN('off-production','off-staging')),
 code text COLLATE "C" NOT NULL CHECK(code ~ '^([0-9]{8}|[0-9]{12}|[0-9]{13}|[0-9]{14})$'),
 state text NOT NULL CHECK(state IN('found','not_found','error','limited')),
 name text NOT NULL DEFAULT '' CHECK(length(name)<=200), brand text NOT NULL DEFAULT '' CHECK(length(brand)<=300),
 pack_description text NOT NULL DEFAULT '' CHECK(length(pack_description)<=500),
 error_code text NOT NULL DEFAULT '' CHECK(length(error_code)<=40),
 payload_sha256 bytea CHECK(payload_sha256 IS NULL OR octet_length(payload_sha256)=32),
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 fetched_at timestamptz NOT NULL, expires_at timestamptz NOT NULL, retry_at timestamptz,
 PRIMARY KEY(household_id,generation,provider,code), CHECK(expires_at>fetched_at),
 CHECK(state='found' OR(name='' AND brand='' AND pack_description=''))
);
CREATE INDEX foodfacts_cache_expiry ON pantry.foodfacts_cache(expires_at);

CREATE TABLE pantry.foodfacts_operations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 generation bigint NOT NULL CHECK(generation>0), created_by uuid NOT NULL REFERENCES core.users(id), request_key uuid NOT NULL,
 state text NOT NULL CHECK(state IN('complete','refused')),
 request_hash bytea CHECK(request_hash IS NULL OR octet_length(request_hash)=32),
 result jsonb CHECK(result IS NULL OR(jsonb_typeof(result)='object' AND octet_length(result::text)<=16384)),
 result_hash bytea CHECK(result_hash IS NULL OR octet_length(result_hash)=32),
 created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
 UNIQUE(household_id,id), UNIQUE(household_id,created_by,request_key),
 CHECK((state='complete' AND request_hash IS NOT NULL) OR(state='refused' AND request_hash IS NULL AND result IS NULL AND result_hash IS NULL)),
 CHECK((result IS NULL)=(result_hash IS NULL))
);
CREATE INDEX foodfacts_operation_actor ON pantry.foodfacts_operations(household_id,created_by,created_at DESC,id DESC);

CREATE TABLE pantry.foodfacts_provenance (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 generation bigint NOT NULL CHECK(generation>0), created_by uuid NOT NULL REFERENCES core.users(id),
 operation_id uuid NOT NULL, product_id uuid NOT NULL, pack_id uuid, barcode_id uuid NOT NULL,
 provider text NOT NULL CHECK(provider IN('off-production','off-staging')), code text COLLATE "C" NOT NULL,
 source_snapshot jsonb NOT NULL CHECK(jsonb_typeof(source_snapshot)='object' AND octet_length(source_snapshot::text)<=8192),
 selected_fields text[] NOT NULL CHECK(cardinality(selected_fields)>=1 AND cardinality(selected_fields)<=3 AND selected_fields <@ ARRAY['name','brand','pack_description']::text[]),
 created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
 UNIQUE(household_id,id), UNIQUE(household_id,operation_id),
 FOREIGN KEY(household_id,operation_id) REFERENCES pantry.foodfacts_operations(household_id,id),
 FOREIGN KEY(household_id,product_id) REFERENCES pantry.products(household_id,id) ON DELETE CASCADE
);
CREATE INDEX foodfacts_provenance_product ON pantry.foodfacts_provenance(household_id,product_id,created_at DESC,id DESC);

REVOKE ALL ON pantry.foodfacts_cache,pantry.foodfacts_operations,pantry.foodfacts_provenance FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON pantry.foodfacts_cache TO household_app;
GRANT SELECT,INSERT ON pantry.foodfacts_operations,pantry.foodfacts_provenance TO household_app;
CREATE TRIGGER foodfacts_operation_immutable BEFORE UPDATE OR DELETE ON pantry.foodfacts_operations FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();
CREATE TRIGGER foodfacts_provenance_immutable BEFORE UPDATE ON pantry.foodfacts_provenance FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();

CREATE FUNCTION pantry.guard_foodfacts_operation() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' OR current_setting('household.audit_kind',true) IS DISTINCT FROM 'user'
 OR NEW.household_id IS DISTINCT FROM nullif(current_setting('household.audit_household',true),'')::uuid
 OR NEW.created_by IS DISTINCT FROM nullif(current_setting('household.audit_user',true),'')::uuid
 OR NOT EXISTS(SELECT 1 FROM core.module_states WHERE household_id=NEW.household_id AND module_id='pantry' AND state='enabled' AND data_generation=NEW.generation) THEN
  RAISE EXCEPTION 'current food facts actor and generation required' USING ERRCODE='42501'; END IF;
 IF NEW.state='complete' AND(NEW.result IS NULL OR NEW.result_hash IS DISTINCT FROM sha256(convert_to(NEW.result::text,'UTF8'))
 OR NEW.result->>'operation_id' IS DISTINCT FROM NEW.id::text) THEN RAISE EXCEPTION 'food facts receipt hash mismatch' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_foodfacts_operation() FROM PUBLIC,household_app;
CREATE TRIGGER foodfacts_operation_guard BEFORE INSERT ON pantry.foodfacts_operations FOR EACH ROW EXECUTE FUNCTION pantry.guard_foodfacts_operation();

CREATE FUNCTION pantry.guard_foodfacts_provenance() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF array_position(NEW.selected_fields,NULL) IS NOT NULL OR
 (SELECT count(DISTINCT f) FROM unnest(NEW.selected_fields) f)<>cardinality(NEW.selected_fields) THEN
  RAISE EXCEPTION 'selected source fields must be distinct supported values' USING ERRCODE='23514'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pantry.foodfacts_operations o JOIN pantry.products p ON p.household_id=o.household_id AND p.id=NEW.product_id
 JOIN pantry.barcodes b ON b.household_id=p.household_id AND b.product_id=p.id AND b.id=NEW.barcode_id AND b.code=NEW.code
 WHERE o.household_id=NEW.household_id AND o.id=NEW.operation_id AND o.generation=NEW.generation AND o.created_by=NEW.created_by
 AND o.state='complete' AND p.archived_at IS NULL AND b.pack_id IS NOT DISTINCT FROM NEW.pack_id
 AND o.result->>'product_id'=p.id::text AND o.result->>'barcode_id'=b.id::text AND o.result->>'provenance_id'=NEW.id::text
 AND o.result->'selected_fields'=to_jsonb(NEW.selected_fields)
 AND(o.result->>'pack_id' IS NULL AND NEW.pack_id IS NULL OR o.result->>'pack_id'=NEW.pack_id::text))
 OR NEW.household_id IS DISTINCT FROM nullif(current_setting('household.audit_household',true),'')::uuid
 OR NEW.created_by IS DISTINCT FROM nullif(current_setting('household.audit_user',true),'')::uuid
 OR jsonb_typeof(NEW.source_snapshot->'source') IS DISTINCT FROM 'object' OR jsonb_typeof(NEW.source_snapshot->'adopted') IS DISTINCT FROM 'object'
 OR NEW.source_snapshot->'source'->>'provider' IS DISTINCT FROM NEW.provider OR NEW.source_snapshot->'source'->>'code' IS DISTINCT FROM NEW.code
 OR NEW.source_snapshot->'source'->>'database_license' IS DISTINCT FROM 'ODbL-1.0' OR NEW.source_snapshot->'source'->>'contents_license' IS DISTINCT FROM 'DbCL-1.0'
 OR NEW.source_snapshot->'source'->>'url' IS DISTINCT FROM
 (CASE NEW.provider WHEN 'off-production' THEN 'https://world.openfoodfacts.org/product/' ELSE 'https://world.openfoodfacts.net/product/' END||NEW.code) THEN
  RAISE EXCEPTION 'food facts provenance identity mismatch' USING ERRCODE='23514'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pantry.foodfacts_operations o JOIN pantry.products p ON p.household_id=o.household_id AND p.id=NEW.product_id
 WHERE o.id=NEW.operation_id AND o.household_id=NEW.household_id AND o.result->'source'=NEW.source_snapshot->'source'
 AND NEW.source_snapshot->'adopted'->>'product_name'=p.name AND NEW.source_snapshot->'adopted'->>'unit'=p.unit_code
 AND(NEW.source_snapshot->'adopted'->>'integer_quantities')::boolean=p.integer_quantities) THEN
  RAISE EXCEPTION 'adopted local metadata differs from actual product or receipt' USING ERRCODE='23514'; END IF;
 IF NEW.pack_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM pantry.packs WHERE household_id=NEW.household_id AND product_id=NEW.product_id AND id=NEW.pack_id AND NOT disabled) THEN
  RAISE EXCEPTION 'food facts pack identity mismatch' USING ERRCODE='23514'; END IF;
 IF NEW.pack_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM pantry.packs p WHERE p.household_id=NEW.household_id AND p.id=NEW.pack_id
 AND NEW.source_snapshot->'adopted'->>'pack_label'=p.label AND NEW.source_snapshot->'adopted'->>'pack_unit'=p.unit_code
 AND jsonb_typeof(NEW.source_snapshot->'adopted'->'pack_quantity')='string'
 AND(NEW.source_snapshot->'adopted'->>'pack_quantity')::numeric=p.quantity) THEN
  RAISE EXCEPTION 'adopted pack metadata differs from actual pack' USING ERRCODE='23514'; END IF;
 IF NEW.pack_id IS NULL AND(NEW.source_snapshot->'adopted'->>'pack_label' IS DISTINCT FROM ''
 OR NEW.source_snapshot->'adopted'->>'pack_quantity' IS DISTINCT FROM '' OR NEW.source_snapshot->'adopted'->>'pack_unit' IS DISTINCT FROM '') THEN
  RAISE EXCEPTION 'absent pack cannot have adopted pack metadata' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION pantry.guard_foodfacts_provenance() FROM PUBLIC,household_app;
CREATE TRIGGER foodfacts_provenance_guard BEFORE INSERT ON pantry.foodfacts_provenance FOR EACH ROW EXECUTE FUNCTION pantry.guard_foodfacts_provenance();

CREATE FUNCTION pantry.check_foodfacts_completion() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE op_id uuid;
BEGIN
 IF TG_TABLE_NAME='foodfacts_operations' THEN op_id=NEW.id; ELSE op_id=NEW.operation_id; END IF;
 IF NOT EXISTS(SELECT 1 FROM pantry.foodfacts_operations o WHERE o.id=op_id AND(o.state='refused' AND NOT EXISTS(
 SELECT 1 FROM pantry.foodfacts_provenance p WHERE p.household_id=o.household_id AND p.operation_id=o.id)
 OR o.state='complete' AND EXISTS(SELECT 1 FROM pantry.foodfacts_provenance p
 JOIN pantry.barcodes b ON b.household_id=p.household_id AND b.id=p.barcode_id AND b.product_id=p.product_id AND b.code=p.code
 WHERE p.household_id=o.household_id AND p.operation_id=o.id AND o.result->>'provenance_id'=p.id::text)
 AND o.result_hash=sha256(convert_to(o.result::text,'UTF8')))) THEN
  RAISE EXCEPTION 'incomplete food facts adoption' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION pantry.check_foodfacts_completion() FROM PUBLIC,household_app;
CREATE CONSTRAINT TRIGGER foodfacts_operation_complete AFTER INSERT ON pantry.foodfacts_operations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pantry.check_foodfacts_completion();
CREATE CONSTRAINT TRIGGER foodfacts_provenance_complete AFTER INSERT ON pantry.foodfacts_provenance
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pantry.check_foodfacts_completion();

CREATE FUNCTION pantry.clear_extension_responses(p_household uuid) RETURNS void LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF session_user<>'household_owner' OR current_user<>'household_owner'
 OR current_setting('household.audit_kind',true) IS DISTINCT FROM 'operator'
 OR p_household IS DISTINCT FROM nullif(current_setting('household.audit_household',true),'')::uuid
 OR NOT EXISTS(SELECT 1 FROM core.module_purge_requests p JOIN core.module_states s ON s.household_id=p.household_id AND s.module_id='pantry'
 WHERE p.id=nullif(current_setting('household.audit_request',true),'')::uuid AND p.household_id=p_household AND p.module_id='pantry'
 AND p.state='pending' AND p.expected_generation=s.data_generation AND s.state='disabled') THEN
  RAISE EXCEPTION 'exact owner pantry purge context required' USING ERRCODE='42501'; END IF;
 UPDATE pantry.leftover_operations SET result=NULL,result_sha256=NULL,source_cap=NULL,source_prepared_on=NULL WHERE household_id=p_household AND state='complete';
 UPDATE pantry.foodfacts_operations SET result=NULL,result_hash=NULL WHERE household_id=p_household AND state='complete';
END $$;
REVOKE ALL ON FUNCTION pantry.clear_extension_responses(uuid) FROM PUBLIC,household_app;

INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('pantry','leftover_operations',ARRAY['id'],ARRAY['generation','state','source_kind'],NULL),
 ('pantry','prepared_lots',ARRAY['lot_id'],ARRAY['operation_id','line_id','prepared_on','initial_portions','portions_unknown'],NULL),
 ('pantry','prepared_transfers',ARRAY['lot_id'],ARRAY['root_lot_id','source_lot_id','movement_out_id','movement_in_id','correlation_id','quantity'],NULL),
 ('pantry','foodfacts_cache',ARRAY['fingerprint'],ARRAY['generation','provider','state'],NULL),
 ('pantry','foodfacts_operations',ARRAY['id'],ARRAY['generation','state'],NULL),
 ('pantry','foodfacts_provenance',ARRAY['id'],ARRAY['generation','operation_id','product_id','pack_id','barcode_id','provider'],NULL);
DO $audit_triggers$
DECLARE name text;
BEGIN
 FOREACH name IN ARRAY ARRAY['leftover_operations','prepared_lots','prepared_transfers','foodfacts_cache','foodfacts_operations','foodfacts_provenance'] LOOP
  EXECUTE format('CREATE TRIGGER household_audit_change AFTER INSERT OR UPDATE OR DELETE ON pantry.%I FOR EACH ROW EXECUTE FUNCTION core.capture_audit_change()',name);
  EXECUTE format('CREATE TRIGGER household_audit_truncate BEFORE TRUNCATE ON pantry.%I FOR EACH STATEMENT EXECUTE FUNCTION core.capture_audit_truncate()',name);
 END LOOP;
END $audit_triggers$;

CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT core.financial_requirements_ready() AND count(*)=(94+(SELECT count(*) FROM core.audit_policies WHERE schema_name='shopping' AND table_name='barcodes')) AND bool_and(
 EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
 AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure))
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
 AND c.relname<>'schema_migrations' AND NOT(n.nspname='fuel' AND c.relname IN('fuel_types','household_state','segment_state','consumption_segments','segment_jobs','module_operations','module_effects','capture_transitions','legacy_operation_fences'))
 AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
 AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states',
 'financial_requirement_sets','financial_requirement_members','financial_artifact_scope_versions'))
 AND NOT EXISTS(SELECT 1 FROM core.audit_policies p WHERE p.schema_name=n.nspname AND p.table_name=c.relname)) FROM core.audit_policies p
$$;

-- Canonicalize new CHECK expressions through the exact dump representation
-- before pinning. This avoids BETWEEN/flattened-AND drift on pg_dump roundtrip;
-- it never normalizes an existing production predicate or readiness capture.
DO $canonical_checks$
DECLARE r record;
BEGIN
 FOR r IN SELECT c.relname,k.conname,pg_get_constraintdef(k.oid) AS definition
 FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='pantry' AND c.relname IN('leftover_operations','prepared_lots','prepared_transfers','foodfacts_cache','foodfacts_operations','foodfacts_provenance')
 AND k.contype='c' ORDER BY c.relname,k.conname LOOP
  EXECUTE format('ALTER TABLE pantry.%I DROP CONSTRAINT %I',r.relname,r.conname);
  EXECUTE format('ALTER TABLE pantry.%I ADD CONSTRAINT %I %s',r.relname,r.conname,r.definition);
 END LOOP;
END $canonical_checks$;

CREATE FUNCTION pantry.prepared_capture_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
 'columns',(SELECT jsonb_agg(jsonb_build_array(c.relname,a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attidentity,a.attgenerated,
 (SELECT quote_ident(cn.nspname)||'.'||quote_ident(co.collname) FROM pg_collation co JOIN pg_namespace cn ON cn.oid=co.collnamespace WHERE co.oid=a.attcollation),
 coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY c.relname,a.attnum)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
 LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE n.nspname='pantry'
 AND c.relname IN('leftover_operations','prepared_lots','prepared_transfers','foodfacts_cache','foodfacts_operations','foodfacts_provenance')),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(c.relname,k.conname,pg_get_constraintdef(k.oid),k.convalidated,k.conislocal,k.coninhcount,k.connoinherit) ORDER BY c.relname,k.conname)
 FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='pantry'
 AND c.relname IN('leftover_operations','prepared_lots','prepared_transfers','foodfacts_cache','foodfacts_operations','foodfacts_provenance')),
 'indexes',(SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready,i.indislive) ORDER BY pg_get_indexdef(i.indexrelid))
 FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='pantry'
 AND c.relname IN('leftover_operations','prepared_lots','prepared_transfers','foodfacts_cache','foodfacts_operations','foodfacts_provenance')),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(c.relname,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled::text,t.tgdeferrable,t.tginitdeferred) ORDER BY c.relname,t.tgname)
 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='pantry' AND NOT t.tgisinternal
 AND c.relname IN('leftover_operations','prepared_lots','prepared_transfers','foodfacts_cache','foodfacts_operations','foodfacts_provenance')),
 'tables',(SELECT jsonb_agg(jsonb_build_array(c.relname,c.relkind::text,c.relpersistence::text,c.relispartition,c.relrowsecurity,c.relforcerowsecurity,
 EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid),pg_get_userbyid(c.relowner),
 coalesce(c.relacl,pg_catalog.acldefault('r',c.relowner))::text,(SELECT coalesce(jsonb_agg(jsonb_build_array(a.attname,coalesce(a.attacl,'{}'::aclitem[])::text) ORDER BY a.attnum),'[]') FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped)) ORDER BY c.relname)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='pantry'
 AND c.relname IN('leftover_operations','prepared_lots','prepared_transfers','foodfacts_cache','foodfacts_operations','foodfacts_provenance')),
 'functions',(SELECT jsonb_agg(jsonb_build_array(n.nspname,p.proname,pg_get_function_identity_arguments(p.oid),md5(pg_get_functiondef(p.oid)),pg_get_userbyid(p.proowner),p.prosecdef,p.provolatile::text,p.proconfig,coalesce(p.proacl,'{}'::aclitem[])::text)
 ORDER BY n.nspname,p.proname) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname='core' AND p.proname IN('pantry_leftover_source','guard_pantry_leftover_link','audit_ready') OR n.nspname='pantry' AND p.proname IN(
 'authorize_leftovers','leftover_source_used','begin_leftovers','guard_prepared_lot','prepared_lot_pending','prepared_lot_source_kind',
 'leftover_completion_valid','finish_leftovers','leftover_operation_status','check_leftover_completion',
 'guard_foodfacts_operation','guard_foodfacts_provenance','check_foodfacts_completion','clear_extension_responses',
 'prepared_transfer_evidence','guard_prepared_transfer','record_prepared_transfer','check_prepared_transfer')),
 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY p.table_name) FROM core.audit_policies p WHERE p.schema_name='pantry'
 AND p.table_name IN('leftover_operations','prepared_lots','prepared_transfers','foodfacts_cache','foodfacts_operations','foodfacts_provenance')),
 'portion',(SELECT to_jsonb(u) FROM core.units u WHERE code='portion'),
 'portion_pack_check',(SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='pantry.packs'::regclass AND conname='packs_unit_code_check'),
 'source_link_trigger',(SELECT jsonb_build_array(pg_get_triggerdef(oid),tgenabled::text) FROM pg_trigger
 WHERE tgrelid='core.resource_links'::regclass AND tgname='pantry_leftover_link' AND NOT tgisinternal))
$$;
REVOKE ALL ON FUNCTION pantry.prepared_capture_catalog() FROM PUBLIC,household_app;
DO $ready_install$
DECLARE expected jsonb;catalog_hash text;
BEGIN
 expected:=pantry.prepared_capture_catalog();
 SELECT md5(pg_get_functiondef('pantry.prepared_capture_catalog()'::regprocedure)) INTO catalog_hash;
 EXECUTE replace(replace($definition$
 CREATE FUNCTION pantry.prepared_capture_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT pantry.prepared_capture_catalog()=#EXPECTED#::jsonb
 AND md5(pg_get_functiondef('pantry.prepared_capture_catalog()'::regprocedure))=#HASH#
 AND core.audit_ready() AND pantry.projection_ready() AND pantry.stock_batch_ready() AND pantry.waste_ready()
 AND NOT pg_has_role('household_app','household_owner','MEMBER')
 AND NOT has_table_privilege('household_app','pantry.leftover_operations','SELECT,INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT has_table_privilege('household_app','pantry.prepared_transfers','INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
 AND NOT has_function_privilege('household_app','core.pantry_leftover_source(uuid,text,uuid,bigint,uuid)','EXECUTE')
 AND NOT has_function_privilege('household_app','pantry.prepared_capture_catalog()','EXECUTE')
 $body$;
 $definition$,'#EXPECTED#',quote_literal(expected::text)),'#HASH#',quote_literal(catalog_hash));
END $ready_install$;
REVOKE ALL ON FUNCTION pantry.prepared_capture_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pantry.prepared_capture_ready() TO household_app;
COMMIT;
