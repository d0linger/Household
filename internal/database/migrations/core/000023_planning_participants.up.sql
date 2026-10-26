BEGIN;
INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column) VALUES
 ('mealplan','participants',ARRAY['id'],ARRAY['meal_id'],NULL);
UPDATE core.audit_policies SET safe_columns=array_append(safe_columns,'servings_mode')
 WHERE schema_name='mealplan' AND table_name IN('meals','components');
UPDATE core.audit_policies SET safe_columns=array_append(safe_columns,'guest_count')
 WHERE schema_name='mealplan' AND table_name='meals';
CREATE FUNCTION core.planning_participants_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT NOT EXISTS(SELECT 1 FROM (VALUES
 ('mealplan.participants','planning_participant_change','mealplan.guard_participant()',31,false),
 ('mealplan.meals','planning_meal_portions','mealplan.check_participant_portions()',21,true),
 ('mealplan.components','planning_component_portions','mealplan.check_participant_portions()',29,true),
 ('mealplan.participants','planning_participant_portions','mealplan.check_participant_portions()',29,true)
 ) required(relation,name,function_id,trigger_type,deferred) WHERE NOT EXISTS(
 SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
 WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=required.name
 AND t.tgfoid=to_regprocedure(required.function_id) AND t.tgtype=required.trigger_type
 AND t.tgenabled IN('O','A') AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']
 AND t.tgdeferrable=required.deferred AND t.tginitdeferred=required.deferred))
 AND NOT EXISTS(SELECT 1 FROM (VALUES
 ('mealplan.meals','servings_mode','text'),('mealplan.meals','guest_count','integer'),
 ('mealplan.components','servings_mode','text')
 ) required(relation,column_name,type_name) WHERE NOT EXISTS(SELECT 1 FROM pg_attribute a
 WHERE a.attrelid=to_regclass(required.relation) AND a.attname=required.column_name
 AND NOT a.attisdropped AND a.attnotnull AND a.atttypid=to_regtype(required.type_name)))
 AND NOT EXISTS(SELECT 1 FROM (VALUES
 ('mealplan.meals','meal_servings_mode','servings_mode','(servings_mode = ANY (ARRAY[''manual''::text, ''participants''::text]))'),
 ('mealplan.meals','meal_guest_count','guest_count','((guest_count >= 0) AND (guest_count <= 100))'),
 ('mealplan.components','component_servings_mode','servings_mode','(servings_mode = ANY (ARRAY[''independent''::text, ''meal''::text]))')
 ) required(relation,name,column_name,expression) WHERE NOT EXISTS(SELECT 1 FROM pg_constraint c
 WHERE c.conrelid=to_regclass(required.relation) AND c.conname=required.name AND c.contype='c' AND c.convalidated
 AND pg_get_expr(c.conbin,c.conrelid)=required.expression
 AND ARRAY(SELECT a.attname::text FROM unnest(c.conkey) WITH ORDINALITY k(attnum,position)
 JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum ORDER BY k.position)=ARRAY[required.column_name]))
 AND NOT EXISTS(SELECT 1 FROM (VALUES('SELECT'),('INSERT'),('DELETE')) required(privilege)
 WHERE NOT has_table_privilege('household_app','mealplan.participants',required.privilege))
 AND NOT has_table_privilege('household_app','mealplan.participants','UPDATE,TRUNCATE,TRIGGER')
 AND NOT has_function_privilege('household_app','mealplan.guard_participant()','EXECUTE')
 AND NOT has_function_privilege('household_app','mealplan.check_participant_portions()','EXECUTE')
 AND EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_index i ON i.indexrelid=c.conindid
 WHERE c.conrelid=to_regclass('mealplan.participants') AND c.conname='participant_meal_member'
 AND c.contype='u' AND c.convalidated AND i.indisvalid AND i.indisready AND i.indisunique
 AND ARRAY(SELECT a.attname::text FROM unnest(c.conkey) WITH ORDINALITY k(attnum,position)
 JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum ORDER BY k.position)
 =ARRAY['household_id','meal_id','membership_id'])
 AND NOT EXISTS(SELECT 1 FROM (VALUES
 ('participant_meal_household','mealplan.meals','c',ARRAY['household_id','meal_id']),
 ('participant_member_household','core.memberships','a',ARRAY['household_id','membership_id'])
 ) required(name,target,on_delete,source_columns) WHERE NOT EXISTS(SELECT 1 FROM pg_constraint c
 WHERE c.conrelid=to_regclass('mealplan.participants') AND c.conname=required.name
 AND c.contype='f' AND c.convalidated AND c.confrelid=to_regclass(required.target)
 AND c.confdeltype::text=required.on_delete
 AND ARRAY(SELECT a.attname::text FROM unnest(c.conkey) WITH ORDINALITY k(attnum,position)
 JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum ORDER BY k.position)=required.source_columns
 AND ARRAY(SELECT a.attname::text FROM unnest(c.confkey) WITH ORDINALITY k(attnum,position)
 JOIN pg_attribute a ON a.attrelid=c.confrelid AND a.attnum=k.attnum ORDER BY k.position)=ARRAY['household_id','id']))
$$;
REVOKE ALL ON FUNCTION core.planning_participants_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION core.planning_participants_ready() TO household_app;
CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT count(*)=74 AND bool_and(
  EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
  AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure)
 ) AND NOT EXISTS(
  SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
  AND c.relname<>'schema_migrations'
  AND NOT(n.nspname='fuel' AND c.relname='fuel_types')
  AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
  AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states'))
  AND NOT EXISTS(SELECT 1 FROM core.audit_policies policy WHERE policy.schema_name=n.nspname AND policy.table_name=c.relname)
 ) FROM core.audit_policies p
$$;

COMMIT;
