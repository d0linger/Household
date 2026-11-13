BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

CREATE TEMP TABLE notification_financial_before(snapshot jsonb NOT NULL) ON COMMIT DROP;
REVOKE ALL ON notification_financial_before FROM PUBLIC,household_app;
DO $predecessor$
DECLARE baseline jsonb;fuel_baseline jsonb;original_catalog jsonb;reviewed_catalog jsonb;producer text;
 reviewed_finance text;reviewed_audit text;actual text;item text;expected text;snapshot jsonb;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=12 AND NOT dirty) FROM fuel.schema_migrations)
 OR NOT(SELECT count(*)=1 AND bool_and(version=5 AND NOT dirty) FROM expenses.schema_migrations)
 OR NOT core.finance_ready() OR NOT core.audit_ready() OR NOT expenses.finance_ready()
 OR NOT fuel.notification_ready() OR NOT pantry.notification_observations_ready()
 OR (SELECT count(*) FROM core.audit_policies)<>165 THEN
  RAISE EXCEPTION 'Financial notifications require reviewed Fuel12/Expenses5/Core38';END IF;
 baseline=documents.predecessor_catalog();fuel_baseline=fuel.notification_predecessor_catalog();
 IF NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='expenses.finance_catalog()'::regprocedure
  AND md5(replace(p.prosrc,E'\r\n',E'\n'))='4c74bc7ee11fcbe25288082a727ba125'
  AND p.proowner='household_owner'::regrole AND p.prosecdef AND p.provolatile='s'
  AND p.proconfig=ARRAY['search_path=pg_catalog'] AND NOT EXISTS(
   SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE')) THEN
  RAISE EXCEPTION 'Unreviewed Expenses5 catalog implementation';END IF;
 expected=format($body$
 SELECT expenses.editing_ready() AND expenses.finance_catalog()=%L::jsonb
 AND md5(pg_get_functiondef('expenses.finance_catalog()'::regprocedure))=%L
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='expenses' AND c.relkind='r' AND c.relname<>'schema_migrations' AND has_table_privilege('household_app',c.oid,'TRUNCATE,TRIGGER'))
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace CROSS JOIN(VALUES('SELECT'),('INSERT'),('UPDATE'),('DELETE')) required(privilege) WHERE n.nspname='expenses' AND c.relkind='r' AND c.relname<>'schema_migrations' AND NOT has_table_privilege('household_app',c.oid,required.privilege))
 AND NOT EXISTS(SELECT 1 FROM(VALUES('expenses.guard_finance_domain()'),('expenses.touch_finance_entry()'),('expenses.touch_receipt_review()'),('expenses.validate_total()'),('expenses.validate_cash_event()'),('expenses.finance_catalog()')) forbidden(fn) WHERE has_function_privilege('household_app',forbidden.fn,'EXECUTE'))
 $body$,expenses.finance_catalog()::text,md5(pg_get_functiondef('expenses.finance_catalog()'::regprocedure)));
 IF NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid='expenses.finance_ready()'::regprocedure
  AND replace(p.prosrc,E'\r\n',E'\n')=expected AND p.proowner='household_owner'::regrole
  AND p.prosecdef AND p.provolatile='s' AND p.proconfig=ARRAY['search_path=pg_catalog']) THEN
  RAISE EXCEPTION 'Unreviewed Expenses5 readiness implementation';END IF;
 -- Core's migration marker is already dirty39 here, so the released Fuel
 -- runtime readiness (correctly requiring clean Core38) cannot be executed.
 -- Verify its exact frozen definitions and catalog instead.
 FOREACH item IN ARRAY ARRAY['fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)','fuel.overview_ready()','fuel.module_ready()'] LOOP
  expected=fuel_baseline->'functions'->item->>'definition';
  IF item='fuel.module_ready()' THEN
   expected=replace(expected,quote_literal((fuel_baseline->'fuel_catalog')::text)||'::jsonb',quote_literal(fuel.module_catalog()::text)||'::jsonb');
  END IF;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner::regrole::text=fuel_baseline->'functions'->item->>'owner'
   AND p.proacl::text IS NOT DISTINCT FROM fuel_baseline->'functions'->item->>'acl';
  IF expected IS NULL OR actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'Unreviewed Fuel12 predecessor: %',item;END IF;
 END LOOP;
 original_catalog=baseline->'finance_catalog';
 producer=replace(baseline->'functions'->'core.finance_producers_ready()'->>'definition',
  '(SELECT count(*) FROM core.audit_policies)=152','(SELECT count(*) FROM core.audit_policies)=165');
 SELECT jsonb_set(original_catalog,'{functions}',jsonb_agg(CASE WHEN value->>0='core.finance_producers_ready()'
  THEN jsonb_set(value,'{1}',to_jsonb(producer)) ELSE value END ORDER BY ordinal)) INTO reviewed_catalog
 FROM jsonb_array_elements(original_catalog->'functions') WITH ORDINALITY x(value,ordinal);
 reviewed_finance=replace(baseline->'functions'->'core.finance_ready()'->>'definition',
  quote_literal(original_catalog::text)||'::jsonb',quote_literal(reviewed_catalog::text)||'::jsonb');
 reviewed_audit=replace(fuel_baseline->'functions'->'core.audit_ready()'->>'definition',
  '''capture_transitions'',''legacy_operation_fences''','''capture_transitions'',''legacy_operation_fences'',''notification_maintenance_observations''');
 IF reviewed_catalog IS NULL OR reviewed_catalog IS DISTINCT FROM core.finance_catalog()
  OR reviewed_audit IS NULL OR reviewed_finance IS NULL THEN RAISE EXCEPTION 'Unreviewed financial notification predecessor catalog';END IF;
 FOREACH item IN ARRAY ARRAY['core.finance_ready()','core.finance_producers_ready()','core.audit_ready()'] LOOP
  expected=CASE item WHEN 'core.finance_ready()' THEN reviewed_finance WHEN 'core.finance_producers_ready()' THEN producer ELSE reviewed_audit END;
  SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid=to_regprocedure(item)
   AND p.proowner='household_owner'::regrole AND p.proacl::text IS NOT DISTINCT FROM baseline->'functions'->item->>'acl';
  IF actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'Unreviewed financial notification predecessor: %',item;END IF;
 END LOOP;
 SELECT jsonb_build_object('finance_catalog',core.finance_catalog(),'fuel_catalog',fuel.module_catalog(),
  'pantry_catalog',pantry.notification_observations_catalog(),'expenses_catalog',expenses.finance_catalog(),
  'functions',jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
   'owner',p.proowner::regrole::text,'acl',p.proacl::text))) INTO snapshot FROM pg_proc p WHERE p.oid IN(
  'core.finance_ready()'::regprocedure,'core.finance_catalog()'::regprocedure,'core.audit_ready()'::regprocedure,
  'fuel.module_ready()'::regprocedure,'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure,
  'fuel.overview_ready()'::regprocedure,'pantry.prepared_capture_ready()'::regprocedure,
  'pantry.notification_observations_ready()'::regprocedure,'expenses.finance_ready()'::regprocedure);
 INSERT INTO notification_financial_before VALUES(snapshot);
 EXECUTE format('CREATE FUNCTION core.notification_financial_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION core.notification_financial_predecessor_catalog() FROM PUBLIC,household_app;

-- Deliberately survives Expenses purge. This is an authority epoch, not a
-- ledger row count: creating an unrelated initial policy must not reset it.
CREATE TABLE core.notification_financial_epochs(
 household_id uuid PRIMARY KEY REFERENCES core.households(id) ON DELETE CASCADE,
 epoch bigint NOT NULL DEFAULT 0 CHECK(epoch BETWEEN 0 AND 9007199254740991),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(updated_at)
  AND updated_at>='2000-01-01'::timestamptz AND updated_at<'10000-01-01'::timestamptz)
);
INSERT INTO core.notification_financial_epochs(household_id) SELECT id FROM core.households;
REVOKE ALL ON core.notification_financial_epochs FROM PUBLIC,household_app;
GRANT SELECT ON core.notification_financial_epochs TO household_app;

CREATE FUNCTION core.notification_financial_epoch_change() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF TG_TABLE_SCHEMA<>'core' OR TG_TABLE_NAME<>'financial_policies' OR TG_OP NOT IN('UPDATE','DELETE') THEN
  RAISE EXCEPTION 'invalid financial epoch trigger' USING ERRCODE='42501';END IF;
 INSERT INTO core.notification_financial_epochs(household_id,epoch,updated_at) VALUES(OLD.household_id,1,clock_timestamp())
 ON CONFLICT(household_id) DO UPDATE SET epoch=core.notification_financial_epochs.epoch+1,updated_at=clock_timestamp();
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION core.notification_financial_epoch_change() FROM PUBLIC,household_app;
CREATE TRIGGER notification_financial_epoch AFTER UPDATE OR DELETE ON core.financial_policies
 FOR EACH ROW EXECUTE FUNCTION core.notification_financial_epoch_change();

-- Assignment checks do not invent a recipient login. The real caller must
-- first edit the entry; only a Boolean view-eligibility result is returned.
CREATE FUNCTION core.finance_notification_recipient_allowed(p_household uuid,p_user uuid,p_membership uuid,p_session uuid,
 p_auth bigint,p_generation bigint,p_entry uuid,p_recipient uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET timezone='UTC' AS $$
DECLARE head core.finance_origin_resources%ROWTYPE;target_user uuid;subject core.financial_subjects%ROWTYPE;
BEGIN
 PERFORM core.finance_authority(p_household,p_user,p_membership,p_session,p_auth,p_generation,
  jsonb_build_array(jsonb_build_object('kind','entries','id',p_entry)),'edit');
 SELECT u.id INTO target_user FROM core.memberships m JOIN core.users u ON u.id=m.user_id
  WHERE m.household_id=p_household AND m.id=p_recipient AND m.inactive_at IS NULL
   AND u.disabled_at IS NULL AND NOT u.must_change_password;
 IF target_user IS NULL THEN RETURN false;END IF;
 SELECT * INTO head FROM core.finance_origin_resources WHERE household_id=p_household AND module_generation=p_generation
  AND resource_type='entries' AND resource_id=p_entry AND row_number=0 AND audit_id IS NULL AND retired_at IS NULL;
 IF head.id IS NULL OR head.subject_id IS NULL OR head.private_owner_id IS NOT NULL
  OR NOT core.finance_root_live(head.root_id,target_user,p_recipient) THEN RETURN false;END IF;
 SELECT * INTO subject FROM core.financial_subjects WHERE household_id=p_household AND id=head.subject_id
  AND module_generation=p_generation AND resource_type='entries' AND resource_id=p_entry AND retired_at IS NULL;
 RETURN subject.id IS NOT NULL
  AND core.finance_policy_allowed(p_household,subject.policy_id,p_recipient,p_generation,false)
  AND core.finance_binding_policy(p_household,'entries',p_entry,subject.policy_id,p_generation)
  AND core.finance_set_visible(p_household,head.set_id,target_user,p_recipient,p_generation,false)
  AND core.finance_binding_complete(p_household,'entries',p_entry,p_generation,target_user,p_recipient);
END $$;
REVOKE ALL ON FUNCTION core.finance_notification_recipient_allowed(uuid,uuid,uuid,uuid,bigint,bigint,uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.finance_notification_recipient_allowed(uuid,uuid,uuid,uuid,bigint,bigint,uuid,uuid) TO household_app;

DO $compatibility$
DECLARE old jsonb;new_catalog jsonb;filtered jsonb;old_audit text;new_audit text;old_pantry text;needle text;authority text;overview text;ready text;old_hash text;
BEGIN
 SELECT snapshot INTO STRICT old FROM notification_financial_before;
 new_catalog=core.finance_catalog();
 SELECT jsonb_set(new_catalog,'{functions}',jsonb_agg(value ORDER BY ordinal)) INTO filtered
 FROM jsonb_array_elements(new_catalog->'functions') WITH ORDINALITY x(value,ordinal)
 WHERE value->>0<>'core.finance_notification_recipient_allowed(uuid,uuid,uuid,uuid,bigint,bigint,uuid,uuid)';
 IF filtered IS DISTINCT FROM old->'finance_catalog' THEN RAISE EXCEPTION 'Unrelated Finance authority metadata changed';END IF;
 EXECUTE replace(old->'functions'->'core.finance_ready()'->>'definition',quote_literal((old->'finance_catalog')::text)||'::jsonb',quote_literal(new_catalog::text)||'::jsonb');
 old_audit=old->'functions'->'core.audit_ready()'->>'definition';
 needle='''notification_intents'',''notification_deliveries''';
 IF (length(old_audit)-length(replace(old_audit,needle,'')))/length(needle)<>1 THEN RAISE EXCEPTION 'Financial operational census changed';END IF;
 new_audit=replace(old_audit,needle,needle||',''notification_financial_epochs''');EXECUTE new_audit;
 old_pantry=old->'functions'->'pantry.prepared_capture_ready()'->>'definition';
 EXECUTE replace(old_pantry,md5(old_audit),md5(new_audit));
 new_catalog=pantry.notification_observations_catalog();
 IF new_catalog-'compatibility' IS DISTINCT FROM(old->'pantry_catalog')-'compatibility' THEN RAISE EXCEPTION 'Unrelated Pantry observation metadata changed';END IF;
 EXECUTE replace(old->'functions'->'pantry.notification_observations_ready()'->>'definition',quote_literal((old->'pantry_catalog')::text)||'::jsonb',quote_literal(new_catalog::text)||'::jsonb');
 authority=old->'functions'->'fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'->>'definition';
 overview=old->'functions'->'fuel.overview_ready()'->>'definition';ready=old->'functions'->'fuel.module_ready()'->>'definition';
 needle='bool_and(version=38 AND NOT dirty)';old_hash=md5(authority);
 IF (length(authority)-length(replace(authority,needle,'')))/length(needle)<>1
  OR (length(overview)-length(replace(overview,needle,'')))/length(needle)<>1
  OR position(quote_literal((old->'fuel_catalog')::text)||'::jsonb' in ready)=0 THEN RAISE EXCEPTION 'Unreviewed Fuel authority barrier';END IF;
 EXECUTE replace(authority,needle,'bool_and(version=39 AND NOT dirty)');
 EXECUTE replace(overview,needle,'bool_and(version=39 AND NOT dirty)');
 EXECUTE replace(ready,old_hash,md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)));
 IF NOT core.finance_ready() OR NOT core.audit_ready() OR NOT pantry.notification_observations_ready() THEN
  RAISE EXCEPTION 'Incomplete financial notification authority compatibility';END IF;
END $compatibility$;

DO $canonical$
DECLARE item record;
BEGIN
 FOR item IN SELECT conname,pg_get_constraintdef(oid) definition FROM pg_constraint
 WHERE conrelid='core.notification_financial_epochs'::regclass AND contype='c' ORDER BY conname LOOP
  EXECUTE format('ALTER TABLE core.notification_financial_epochs DROP CONSTRAINT %I',item.conname);
  EXECUTE format('ALTER TABLE core.notification_financial_epochs ADD CONSTRAINT %I %s',item.conname,item.definition);
 END LOOP;
END $canonical$;

CREATE FUNCTION core.notification_financial_catalog() RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object(
 'table',(SELECT jsonb_build_array(c.relowner::regrole::text,c.relkind,c.relpersistence,c.relrowsecurity,c.relforcerowsecurity,c.relispartition,c.relacl::text,
  EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)) FROM pg_class c WHERE c.oid='core.notification_financial_epochs'::regclass),
 'columns',(SELECT jsonb_agg(jsonb_build_array(a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attidentity,a.attgenerated,a.attacl::text,coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum)
  FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid='core.notification_financial_epochs'::regclass AND a.attnum>0 AND NOT a.attisdropped),
 'constraints',(SELECT jsonb_agg(jsonb_build_array(conname,pg_get_constraintdef(oid),convalidated) ORDER BY conname) FROM pg_constraint WHERE conrelid='core.notification_financial_epochs'::regclass),
 'indexes',(SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(indexrelid),indisvalid,indisready,indislive) ORDER BY indexrelid::regclass::text) FROM pg_index WHERE indrelid='core.notification_financial_epochs'::regclass),
 'triggers',(SELECT jsonb_agg(jsonb_build_array(tgrelid::regclass::text,tgname,pg_get_triggerdef(oid),tgenabled,tgisinternal) ORDER BY tgrelid::regclass::text,tgname)
  FROM pg_trigger WHERE NOT tgisinternal AND(tgrelid='core.notification_financial_epochs'::regclass OR(tgrelid='core.financial_policies'::regclass AND tgname='notification_financial_epoch'))),
 -- Internal FK trigger names contain OIDs that change on restore. Pin both
 -- referencing checks and referenced actions by their full stable semantics.
 'constraint_triggers',(SELECT jsonb_agg(identity ORDER BY identity) FROM(
  SELECT jsonb_build_array(k.conrelid::regclass::text,k.conname,k.contype,pg_get_constraintdef(k.oid),k.convalidated,
   t.tgrelid::regclass::text,t.tgconstrrelid::regclass::text,t.tgconstrindid::regclass::text,
   t.tgfoid::regprocedure::text,t.tgtype,t.tgenabled,t.tgisinternal,t.tgdeferrable,t.tginitdeferred,
   t.tgnargs,encode(t.tgargs,'hex'),t.tgattr::text,pg_get_expr(t.tgqual,t.tgrelid)) identity
  FROM pg_trigger t LEFT JOIN pg_constraint k ON k.oid=t.tgconstraint
  WHERE t.tgisinternal AND(t.tgrelid IN('core.notification_financial_epochs'::regclass) OR
   k.conrelid IN('core.notification_financial_epochs'::regclass))
 ) fk_triggers),
 'functions',(SELECT jsonb_agg(jsonb_build_array(p.oid::regprocedure::text,pg_get_functiondef(p.oid),p.proowner::regrole::text,p.proacl::text) ORDER BY p.oid::regprocedure::text)
  FROM pg_proc p WHERE p.oid IN('core.notification_financial_epoch_change()'::regprocedure,'core.finance_notification_recipient_allowed(uuid,uuid,uuid,uuid,bigint,bigint,uuid,uuid)'::regprocedure)),
 'policies',(SELECT jsonb_agg(to_jsonb(p)) FROM core.audit_policies p WHERE schema_name='core' AND table_name='notification_financial_epochs'))
$$;
REVOKE ALL ON FUNCTION core.notification_financial_catalog() FROM PUBLIC,household_app;
DO $ready$
DECLARE expected jsonb;catalog_hash text;
BEGIN
 expected=core.notification_financial_catalog();catalog_hash=md5(pg_get_functiondef('core.notification_financial_catalog()'::regprocedure));
 EXECUTE format($definition$CREATE FUNCTION core.notification_financial_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $body$
 SELECT core.notification_financial_catalog()=%L::jsonb AND md5(pg_get_functiondef('core.notification_financial_catalog()'::regprocedure))=%L
 AND NOT has_table_privilege('household_app','core.notification_financial_epochs','INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER,REFERENCES')
 AND NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid IN('core.notification_financial_catalog()'::regprocedure,'core.notification_financial_predecessor_catalog()'::regprocedure)
  AND(p.proowner<>'household_owner'::regrole OR NOT p.prosecdef OR p.proconfig IS DISTINCT FROM ARRAY['search_path=pg_catalog']))
 AND NOT EXISTS(SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
  WHERE p.oid IN('core.notification_financial_catalog()'::regprocedure,'core.notification_financial_predecessor_catalog()'::regprocedure)
   AND(a.grantee<>p.proowner OR a.privilege_type<>'EXECUTE'))
 $body$$definition$,expected,catalog_hash);
END $ready$;
REVOKE ALL ON FUNCTION core.notification_financial_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.notification_financial_ready() TO household_app;
COMMIT;
