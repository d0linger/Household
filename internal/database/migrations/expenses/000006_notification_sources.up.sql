BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

CREATE TEMP TABLE expenses_notification_before(snapshot jsonb NOT NULL) ON COMMIT DROP;
REVOKE ALL ON expenses_notification_before FROM PUBLIC,household_app;
DO $predecessor$
DECLARE snapshot jsonb;reviewed jsonb;actual text;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=39 AND NOT dirty) FROM core.schema_migrations)
 OR NOT core.notification_financial_ready() OR NOT core.finance_ready() OR NOT expenses.finance_ready()
 OR NOT core.audit_ready() OR NOT pantry.notification_observations_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN RAISE EXCEPTION 'Finance notification sources require accepted Core39/Expenses5';END IF;
 reviewed=core.notification_financial_predecessor_catalog();
 SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid='expenses.finance_ready()'::regprocedure
  AND p.proowner::regrole::text=reviewed->'functions'->'expenses.finance_ready()'->>'owner'
  AND p.proacl::text IS NOT DISTINCT FROM reviewed->'functions'->'expenses.finance_ready()'->>'acl';
 IF reviewed->'expenses_catalog' IS DISTINCT FROM expenses.finance_catalog()
  OR actual IS DISTINCT FROM reviewed->'functions'->'expenses.finance_ready()'->>'definition' THEN
  RAISE EXCEPTION 'Unreviewed Expenses5 metadata after authority barrier';END IF;
 SELECT jsonb_build_object('expenses_catalog',expenses.finance_catalog(),'pantry_catalog',pantry.notification_observations_catalog(),
  'functions',jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
   'owner',p.proowner::regrole::text,'acl',p.proacl::text))) INTO snapshot FROM pg_proc p WHERE p.oid IN(
  'core.audit_ready()'::regprocedure,'expenses.finance_ready()'::regprocedure,'expenses.finance_catalog()'::regprocedure,
  'pantry.prepared_capture_ready()'::regprocedure,'pantry.notification_observations_ready()'::regprocedure);
 INSERT INTO expenses_notification_before VALUES(snapshot);
 EXECUTE format('CREATE FUNCTION expenses.notification_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION expenses.notification_predecessor_catalog() FROM PUBLIC,household_app;

CREATE TABLE expenses.notification_payment_reminders(
 id uuid PRIMARY KEY,household_id uuid NOT NULL,
 due_on date NOT NULL CHECK(isfinite(due_on) AND due_on>='2000-01-01'::date AND due_on<'10000-01-01'::date),
 responsible_member_id uuid,
 state text NOT NULL DEFAULT 'draft' CHECK(state IN('draft','confirmed','completed','cancelled')),
 confirmed_entry_version bigint CHECK(confirmed_entry_version BETWEEN 1 AND 9007199254740991),
 confirmed_at timestamptz,responsible_since timestamptz,completed_at timestamptz,
 version bigint NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
 created_by uuid NOT NULL REFERENCES core.users(id),changed_by uuid NOT NULL REFERENCES core.users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(household_id,id),
 FOREIGN KEY(household_id,id) REFERENCES expenses.entries(household_id,id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,responsible_member_id) REFERENCES core.memberships(household_id,id),
 CHECK((responsible_member_id IS NULL)=(responsible_since IS NULL)),
 CHECK((confirmed_at IS NULL)=(confirmed_entry_version IS NULL)),
 CHECK((state='draft' AND confirmed_at IS NULL AND completed_at IS NULL)
  OR(state='confirmed' AND confirmed_at IS NOT NULL AND responsible_member_id IS NOT NULL AND completed_at IS NULL)
  OR(state='completed' AND confirmed_at IS NOT NULL AND completed_at IS NOT NULL)
  OR(state='cancelled' AND completed_at IS NULL)),
 CHECK(isfinite(created_at) AND created_at>='2000-01-01'::timestamptz AND isfinite(updated_at)
  AND created_at<=updated_at AND updated_at<'10000-01-01'::timestamptz
  AND(responsible_since IS NULL OR(isfinite(responsible_since) AND responsible_since>=created_at AND responsible_since<=updated_at))
  AND(confirmed_at IS NULL OR(isfinite(confirmed_at) AND confirmed_at>=created_at AND confirmed_at<=updated_at))
  AND(completed_at IS NULL OR(isfinite(completed_at) AND completed_at>=confirmed_at AND completed_at<=updated_at)))
);
CREATE INDEX notification_payment_reminders_due ON expenses.notification_payment_reminders(household_id,due_on,id) WHERE state='confirmed';

CREATE TABLE expenses.notification_budget_observations(
 household_id uuid NOT NULL,user_id uuid NOT NULL,budget_id uuid NOT NULL,threshold smallint NOT NULL CHECK(threshold IN(80,100)),
 module_generation bigint NOT NULL CHECK(module_generation BETWEEN 1 AND 9007199254740991),module_activated_at timestamptz NOT NULL,
 month date NOT NULL CHECK(isfinite(month) AND month>='2000-01-01'::date AND month<'10000-01-01'::date AND extract(day FROM month)=1),
 budget_version bigint NOT NULL CHECK(budget_version BETWEEN 1 AND 9007199254740991),
 permission_epoch bigint NOT NULL CHECK(permission_epoch BETWEEN 0 AND 9007199254740991),
 membership_version bigint NOT NULL CHECK(membership_version BETWEEN 1 AND 9007199254740991),
 auth_version bigint NOT NULL CHECK(auth_version BETWEEN 1 AND 9007199254740991),
 scope_stamp bytea NOT NULL CHECK(octet_length(scope_stamp)=32),
 state text NOT NULL CHECK(state IN('baseline','healthy','crossed','ineligible')),
 episode bigint NOT NULL DEFAULT 0 CHECK(episode BETWEEN 0 AND 9007199254740991),
 last_healthy_at timestamptz,observed_at timestamptz NOT NULL,detected_at timestamptz,consent_cutoff timestamptz,
 PRIMARY KEY(household_id,user_id,budget_id,threshold),
 FOREIGN KEY(household_id,user_id) REFERENCES core.memberships(household_id,user_id) ON DELETE CASCADE,
 FOREIGN KEY(household_id,budget_id) REFERENCES expenses.category_budgets(household_id,id) ON DELETE CASCADE,
 CHECK(isfinite(observed_at) AND observed_at>='2000-01-01'::timestamptz AND observed_at<'10000-01-01'::timestamptz
  AND isfinite(module_activated_at) AND module_activated_at>='2000-01-01'::timestamptz AND module_activated_at<=observed_at),
 CHECK((state='healthy' AND last_healthy_at IS NOT NULL AND last_healthy_at=observed_at AND detected_at IS NULL AND consent_cutoff IS NULL)
  OR(state='crossed' AND episode>0 AND last_healthy_at IS NOT NULL AND detected_at IS NOT NULL AND consent_cutoff IS NOT NULL
   AND module_activated_at<=consent_cutoff AND consent_cutoff=last_healthy_at AND consent_cutoff<=detected_at AND detected_at<=observed_at)
  OR(state IN('baseline','ineligible') AND last_healthy_at IS NULL AND detected_at IS NULL AND consent_cutoff IS NULL))
);
CREATE INDEX notification_budget_observations_crossed ON expenses.notification_budget_observations
 (household_id,user_id,module_generation,detected_at,budget_id,threshold) WHERE state='crossed';
REVOKE ALL ON expenses.notification_payment_reminders,expenses.notification_budget_observations FROM PUBLIC,household_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON expenses.notification_payment_reminders,expenses.notification_budget_observations TO household_app;

CREATE FUNCTION expenses.notification_payment_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE context_value jsonb;actor uuid;now_at timestamptz=clock_timestamp();source_version bigint;member_changed boolean;
BEGIN
 IF TG_TABLE_SCHEMA<>'expenses' OR TG_TABLE_NAME<>'notification_payment_reminders' OR TG_OP NOT IN('INSERT','UPDATE','DELETE') THEN
  RAISE EXCEPTION 'invalid payment reminder trigger' USING ERRCODE='42501';END IF;
 context_value=core.audit_context();actor=(context_value->>'actor')::uuid;
 IF session_user<>'household_owner' AND(context_value->>'kind'<>'user' OR actor IS NULL
  OR(context_value->>'household')::uuid IS DISTINCT FROM coalesce(NEW.household_id,OLD.household_id)
  OR NOT EXISTS(SELECT 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id
   WHERE m.household_id=coalesce(NEW.household_id,OLD.household_id) AND m.user_id=actor
   AND m.role IN('admin','editor') AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password)) THEN
  RAISE EXCEPTION 'current household writer required' USING ERRCODE='42501';END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;
 IF TG_OP='UPDATE' AND(NEW.household_id IS DISTINCT FROM OLD.household_id OR NEW.id IS DISTINCT FROM OLD.id
  OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at) THEN
  RAISE EXCEPTION 'immutable reminder identity' USING ERRCODE='23514';END IF;
 IF TG_OP='INSERT' AND NEW.state<>'draft' THEN RAISE EXCEPTION 'new reminder must be a draft' USING ERRCODE='23514';END IF;
 member_changed=TG_OP='INSERT' OR NEW.responsible_member_id IS DISTINCT FROM OLD.responsible_member_id;
 IF member_changed AND NEW.responsible_member_id IS NOT NULL THEN
  PERFORM 1 FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=NEW.household_id
   AND m.id=NEW.responsible_member_id AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password FOR SHARE OF m,u NOWAIT;
  IF NOT FOUND THEN RAISE EXCEPTION 'active household recipient required' USING ERRCODE='23514';END IF;
 END IF;
 IF TG_OP='INSERT' THEN NEW.version=1;NEW.created_at=now_at;NEW.created_by=coalesce(actor,NEW.created_by);
 ELSE NEW.version=OLD.version+1;END IF;
 NEW.changed_by=coalesce(actor,NEW.created_by);NEW.updated_at=now_at;
 IF member_changed THEN NEW.responsible_since=CASE WHEN NEW.responsible_member_id IS NULL THEN NULL ELSE now_at END;
 ELSE NEW.responsible_since=OLD.responsible_since;END IF;
 IF NEW.state='draft' THEN NEW.confirmed_entry_version=NULL;NEW.confirmed_at=NULL;NEW.completed_at=NULL;
 ELSIF NEW.state='confirmed' THEN
  SELECT version INTO source_version FROM expenses.entries WHERE household_id=NEW.household_id AND id=NEW.id FOR SHARE NOWAIT;
  IF source_version IS NULL OR NEW.confirmed_entry_version IS DISTINCT FROM source_version OR NEW.responsible_member_id IS NULL THEN
   RAISE EXCEPTION 'explicit current entry confirmation required' USING ERRCODE='23514';END IF;
  NEW.confirmed_at=now_at;NEW.completed_at=NULL;
 ELSIF NEW.state='completed' THEN
  IF TG_OP<>'UPDATE' OR OLD.state<>'confirmed' THEN RAISE EXCEPTION 'only confirmed reminders can be completed' USING ERRCODE='23514';END IF;
  NEW.confirmed_entry_version=OLD.confirmed_entry_version;NEW.confirmed_at=OLD.confirmed_at;NEW.completed_at=now_at;
 ELSE NEW.confirmed_entry_version=OLD.confirmed_entry_version;NEW.confirmed_at=OLD.confirmed_at;NEW.completed_at=NULL;
 END IF;
 IF TG_OP='UPDATE' AND NEW.state<>'draft' AND(NEW.due_on IS DISTINCT FROM OLD.due_on OR member_changed) THEN
  RAISE EXCEPTION 'save changed reminder as draft before confirmation' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION expenses.notification_payment_guard() FROM PUBLIC,household_app;
CREATE TRIGGER notification_payment_guard BEFORE INSERT OR UPDATE OR DELETE ON expenses.notification_payment_reminders
 FOR EACH ROW EXECUTE FUNCTION expenses.notification_payment_guard();

-- The audit identity is the existing protected entry, never an independent
-- financial kind. No due date, recipient, amount, label or payment detail is copied.
CREATE FUNCTION expenses.notification_payment_audit() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE context_value jsonb;entry_id uuid;household uuid;transition jsonb;
BEGIN
 IF TG_TABLE_SCHEMA<>'expenses' OR TG_TABLE_NAME<>'notification_payment_reminders' OR TG_OP NOT IN('INSERT','UPDATE','DELETE') THEN
  RAISE EXCEPTION 'invalid payment reminder audit trigger' USING ERRCODE='42501';END IF;
 IF core.finance_real_maintenance() THEN RETURN NULL;END IF;
 context_value=core.audit_context();entry_id=coalesce(NEW.id,OLD.id);household=coalesce(NEW.household_id,OLD.household_id);
 transition=jsonb_build_object('payment_reminder_state',jsonb_build_object('before',CASE WHEN TG_OP='INSERT' THEN NULL ELSE OLD.state END,
  'after',CASE WHEN TG_OP='DELETE' THEN NULL ELSE NEW.state END));
 INSERT INTO core.audit_log(household_id,actor_user_id,module_id,resource_type,resource_id,action,outcome,request_id,details,actor_kind,db_role,entity_key,entity_label,changes,created_at)
 VALUES(household,(context_value->>'actor')::uuid,'expenses','entries',entry_id,'payment_reminder_'||lower(TG_OP),'success',
  (context_value->>'request')::uuid,'{}',context_value->>'kind',session_user,jsonb_build_object('id',entry_id),'expenses.entries',transition,clock_timestamp());
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION expenses.notification_payment_audit() FROM PUBLIC,household_app;
CREATE TRIGGER notification_payment_audit AFTER INSERT OR UPDATE OR DELETE ON expenses.notification_payment_reminders
 FOR EACH ROW EXECUTE FUNCTION expenses.notification_payment_audit();

DO $canonical$
DECLARE item record;
BEGIN
 FOR item IN SELECT conrelid::regclass relation,conname,pg_get_constraintdef(oid) definition FROM pg_constraint
 WHERE contype='c' AND conrelid IN('expenses.notification_payment_reminders'::regclass,'expenses.notification_budget_observations'::regclass) ORDER BY conrelid,conname LOOP
  EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',item.relation,item.conname);
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s',item.relation,item.conname,item.definition);
 END LOOP;
END $canonical$;

DO $compatibility$
DECLARE old jsonb;old_audit text;new_audit text;new_catalog jsonb;filtered jsonb;key text;values_before jsonb;values_after jsonb;needle text;
BEGIN
 SELECT snapshot INTO STRICT old FROM expenses_notification_before;
 new_catalog=expenses.finance_catalog();
 -- Every pre-existing table, column, index, function and trigger must remain
 -- byte-for-byte unchanged. Only the two new sidecar tables enter this catalog.
 FOREACH key IN ARRAY ARRAY['columns','tables','constraints','indexes'] LOOP
  SELECT coalesce(jsonb_agg(value ORDER BY ordinal),'[]') INTO values_after
  FROM jsonb_array_elements(new_catalog->key) WITH ORDINALITY x(value,ordinal)
  WHERE CASE WHEN key='indexes' THEN value->>0 NOT IN('notification_payment_reminders_pkey',
   'notification_payment_reminders_household_id_id_key','notification_payment_reminders_due',
   'notification_budget_observations_pkey','notification_budget_observations_crossed')
   ELSE value->>0 NOT IN('notification_payment_reminders','notification_budget_observations') END;
  values_before=coalesce(old->'expenses_catalog'->key,'[]');
  IF values_after IS DISTINCT FROM values_before THEN RAISE EXCEPTION 'Unrelated Expenses metadata changed: %',key;END IF;
 END LOOP;
 IF new_catalog->'functions' IS DISTINCT FROM old->'expenses_catalog'->'functions'
  OR new_catalog->'triggers' IS DISTINCT FROM old->'expenses_catalog'->'triggers' THEN RAISE EXCEPTION 'Unrelated Expenses behavior changed';END IF;
 EXECUTE replace(old->'functions'->'expenses.finance_ready()'->>'definition',quote_literal((old->'expenses_catalog')::text)||'::jsonb',quote_literal(new_catalog::text)||'::jsonb');
 old_audit=old->'functions'->'core.audit_ready()'->>'definition';
 needle='AND NOT(n.nspname=''pantry'' AND c.relname IN(''lot_balances'',''notification_stock_observations''))';
 IF (length(old_audit)-length(replace(old_audit,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'Unexpected audit census predecessor';END IF;
 new_audit=replace(old_audit,needle,needle||E'\n AND NOT(n.nspname=''expenses'' AND c.relname IN(''notification_payment_reminders'',''notification_budget_observations''))');
 EXECUTE new_audit;
 EXECUTE replace(old->'functions'->'pantry.prepared_capture_ready()'->>'definition',md5(old_audit),md5(new_audit));
 new_catalog=pantry.notification_observations_catalog();
 IF new_catalog-'compatibility' IS DISTINCT FROM(old->'pantry_catalog')-'compatibility' THEN RAISE EXCEPTION 'Unrelated Pantry metadata changed';END IF;
 EXECUTE replace(old->'functions'->'pantry.notification_observations_ready()'->>'definition',quote_literal((old->'pantry_catalog')::text)||'::jsonb',quote_literal(new_catalog::text)||'::jsonb');
 IF NOT expenses.finance_ready() OR NOT core.finance_ready() OR NOT core.audit_ready()
 OR NOT pantry.notification_observations_ready() OR NOT core.notification_financial_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN RAISE EXCEPTION 'Incomplete Expenses notification compatibility';END IF;
END $compatibility$;

CREATE FUNCTION expenses.notification_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
 'tables',(SELECT jsonb_agg(jsonb_build_array(c.relname,c.relowner::regrole::text,c.relkind,c.relpersistence,c.relrowsecurity,c.relforcerowsecurity,c.relispartition,c.relacl::text,
  EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)) ORDER BY c.relname) FROM pg_class c WHERE c.oid IN('expenses.notification_payment_reminders'::regclass,'expenses.notification_budget_observations'::regclass)),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(tgrelid::regclass::text,tgname,pg_get_triggerdef(oid),tgenabled,tgisinternal) ORDER BY tgrelid::regclass::text,tgname)
  FROM pg_trigger WHERE NOT tgisinternal AND(tgrelid IN('expenses.notification_payment_reminders'::regclass,'expenses.notification_budget_observations'::regclass))),
 -- Internal FK trigger names contain OIDs that change on restore. Pin both
 -- referencing checks and referenced actions by their full stable semantics.
 'constraint_triggers',(SELECT jsonb_agg(identity ORDER BY identity) FROM(
  SELECT jsonb_build_array(k.conrelid::regclass::text,k.conname,k.contype,pg_get_constraintdef(k.oid),k.convalidated,
   t.tgrelid::regclass::text,t.tgconstrrelid::regclass::text,t.tgconstrindid::regclass::text,
   t.tgfoid::regprocedure::text,t.tgtype,t.tgenabled,t.tgisinternal,t.tgdeferrable,t.tginitdeferred,
   t.tgnargs,encode(t.tgargs,'hex'),t.tgattr::text,pg_get_expr(t.tgqual,t.tgrelid)) identity
  FROM pg_trigger t LEFT JOIN pg_constraint k ON k.oid=t.tgconstraint
  WHERE t.tgisinternal AND(t.tgrelid IN('expenses.notification_payment_reminders'::regclass,'expenses.notification_budget_observations'::regclass) OR
   k.conrelid IN('expenses.notification_payment_reminders'::regclass,'expenses.notification_budget_observations'::regclass))
 ) fk_triggers),
 'functions',(SELECT jsonb_agg(jsonb_build_array(p.oid::regprocedure::text,pg_get_functiondef(p.oid),p.proowner::regrole::text,p.proacl::text) ORDER BY p.oid::regprocedure::text)
  FROM pg_proc p WHERE p.oid IN('expenses.notification_payment_guard()'::regprocedure,'expenses.notification_payment_audit()'::regprocedure)),
 'policies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY table_name) FROM core.audit_policies p WHERE schema_name='expenses' AND table_name IN('notification_payment_reminders','notification_budget_observations')))
$$;
REVOKE ALL ON FUNCTION expenses.notification_catalog() FROM PUBLIC,household_app;
DO $ready$
DECLARE expected jsonb;catalog_hash text;
BEGIN
 expected=expenses.notification_catalog();catalog_hash=md5(pg_get_functiondef('expenses.notification_catalog()'::regprocedure));
 EXECUTE format($definition$CREATE FUNCTION expenses.notification_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT expenses.finance_ready() AND expenses.notification_catalog()=%L::jsonb
 AND md5(pg_get_functiondef('expenses.notification_catalog()'::regprocedure))=%L
 AND NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid IN('expenses.notification_catalog()'::regprocedure,'expenses.notification_predecessor_catalog()'::regprocedure)
  AND(p.proowner<>'household_owner'::regrole OR NOT p.prosecdef OR p.proconfig IS DISTINCT FROM ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
  WHERE p.oid IN('expenses.notification_catalog()'::regprocedure,'expenses.notification_predecessor_catalog()'::regprocedure)
   AND(a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE'))
 $body$$definition$,expected,catalog_hash);
END $ready$;
REVOKE ALL ON FUNCTION expenses.notification_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION expenses.notification_ready() TO household_app;
COMMIT;
