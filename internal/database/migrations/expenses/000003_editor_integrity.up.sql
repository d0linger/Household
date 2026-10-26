BEGIN;
SELECT id FROM core.households /* expense editor household barrier */ ORDER BY id FOR UPDATE;
CREATE INDEX lines_editor_order ON expenses.entry_lines(household_id,entry_id,position,id);

-- Generic child-first writes and the atomic parent-first editor must fail fast
-- on opposite lock order. Each application child write changes its parent
-- version, including direct application-role SQL outside the generic API.
CREATE OR REPLACE FUNCTION expenses.protect_posted() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE parent_id uuid; parent_household uuid; parent_state text;
BEGIN
 IF current_user='household_owner' THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
 END IF;
 IF TG_TABLE_NAME='entries' THEN
  IF OLD.state='posted' THEN RAISE EXCEPTION 'posted entry is immutable' USING ERRCODE='23514'; END IF;
  IF TG_OP='UPDATE' AND (NEW.id IS DISTINCT FROM OLD.id OR NEW.household_id IS DISTINCT FROM OLD.household_id) THEN
   RAISE EXCEPTION 'entry identity is immutable' USING ERRCODE='23514';
  END IF;
 ELSE
  IF TG_OP='UPDATE' AND (NEW.id IS DISTINCT FROM OLD.id OR NEW.household_id IS DISTINCT FROM OLD.household_id
   OR NEW.entry_id IS DISTINCT FROM OLD.entry_id) THEN
   RAISE EXCEPTION 'allocation identity and parent are immutable' USING ERRCODE='23514';
  END IF;
  IF TG_OP='DELETE' THEN parent_id=OLD.entry_id; parent_household=OLD.household_id;
  ELSE parent_id=NEW.entry_id; parent_household=NEW.household_id; END IF;
  SELECT state INTO parent_state FROM expenses.entries
   WHERE household_id=parent_household AND id=parent_id FOR UPDATE NOWAIT;
  IF NOT FOUND AND TG_OP<>'DELETE' THEN RAISE EXCEPTION 'allocation parent missing' USING ERRCODE='23503'; END IF;
  IF parent_state='posted' THEN RAISE EXCEPTION 'posted allocation is immutable' USING ERRCODE='23514'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION expenses.touch_entry_after_line() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE parent_id uuid; parent_household uuid;
BEGIN
 -- Restore and deliberate maintenance preserve historical versions. The
 -- existing owner bypass is not a grant to the runtime application role.
 IF current_user='household_owner' THEN RETURN NULL; END IF;
 IF TG_OP='DELETE' THEN parent_id=OLD.entry_id; parent_household=OLD.household_id;
 ELSE parent_id=NEW.entry_id; parent_household=NEW.household_id; END IF;
 UPDATE expenses.entries SET version=version+1,updated_at=clock_timestamp()
  WHERE household_id=parent_household AND id=parent_id;
 -- A draft parent deletion cascades after the parent is gone; no row is
 -- resurrected. The existing composite FK protects every other child path.
 RETURN NULL;
END $$;
CREATE TRIGGER lines_touch_entry AFTER INSERT OR UPDATE OR DELETE ON expenses.entry_lines
 FOR EACH ROW EXECUTE FUNCTION expenses.touch_entry_after_line();

ALTER FUNCTION expenses.validate_total() SET search_path=pg_catalog;
REVOKE EXECUTE ON FUNCTION expenses.protect_posted(),expenses.touch_entry_after_line(),expenses.validate_total() FROM PUBLIC;

CREATE FUNCTION expenses.editing_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT NOT EXISTS(SELECT 1 FROM (VALUES
 ('expenses.entries','entries_immutable','expenses.protect_posted()',27,false),
 ('expenses.entry_lines','lines_immutable','expenses.protect_posted()',31,false),
 ('expenses.entry_lines','lines_touch_entry','expenses.touch_entry_after_line()',29,false),
 ('expenses.entries','entries_total','expenses.validate_total()',21,true),
 ('expenses.entry_lines','lines_total','expenses.validate_total()',29,true)
 ) required(relation,name,function_id,trigger_type,deferred) WHERE NOT EXISTS(
 SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
 WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=required.name AND NOT t.tgisinternal
 AND t.tgfoid=to_regprocedure(required.function_id) AND t.tgtype=required.trigger_type
 AND t.tgqual IS NULL AND t.tgattr=''::int2vector AND t.tgnargs=0
 AND t.tgenabled IN('O','A') AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']
 AND t.tgdeferrable=required.deferred AND t.tginitdeferred=required.deferred))
 AND NOT EXISTS(SELECT 1 FROM (VALUES
 ('expenses.protect_posted()'),('expenses.touch_entry_after_line()'),('expenses.validate_total()')
 ) required(function_id) WHERE has_function_privilege('household_app',required.function_id,'EXECUTE'))
 AND NOT EXISTS(SELECT 1 FROM (VALUES('expenses.entries'),('expenses.entry_lines')) required(relation)
 CROSS JOIN (VALUES('SELECT'),('INSERT'),('UPDATE'),('DELETE')) permitted(privilege)
 WHERE NOT has_table_privilege('household_app',required.relation,permitted.privilege))
 AND NOT EXISTS(SELECT 1 FROM (VALUES('expenses.entries'),('expenses.entry_lines')) required(relation)
 WHERE has_table_privilege('household_app',required.relation,'TRUNCATE,TRIGGER'))
 AND EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_index i ON i.indexrelid=c.conindid
 WHERE c.conrelid=to_regclass('expenses.entry_lines') AND c.contype='f' AND c.convalidated
 AND c.confrelid=to_regclass('expenses.entries') AND c.confdeltype='c' AND NOT c.condeferrable
 AND i.indisunique AND i.indisvalid AND i.indisready
 AND ARRAY(SELECT a.attname::text FROM unnest(c.conkey) WITH ORDINALITY k(attnum,position)
 JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum ORDER BY k.position)=ARRAY['household_id','entry_id']
 AND ARRAY(SELECT a.attname::text FROM unnest(c.confkey) WITH ORDINALITY k(attnum,position)
 JOIN pg_attribute a ON a.attrelid=c.confrelid AND a.attnum=k.attnum ORDER BY k.position)=ARRAY['household_id','id'])
 AND EXISTS(SELECT 1 FROM pg_index i JOIN pg_class idx ON idx.oid=i.indexrelid JOIN pg_am am ON am.oid=idx.relam
 WHERE i.indexrelid=to_regclass('expenses.lines_editor_order') AND i.indrelid=to_regclass('expenses.entry_lines')
 AND i.indisvalid AND i.indisready AND i.indislive AND NOT i.indisunique AND i.indpred IS NULL AND i.indexprs IS NULL
 AND i.indoption='0 0 0 0'::int2vector
 AND i.indnkeyatts=4 AND i.indnatts=4 AND am.amname='btree'
 AND ARRAY(SELECT a.attname::text FROM unnest(i.indkey) WITH ORDINALITY k(attnum,position)
 JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.attnum ORDER BY k.position)
 =ARRAY['household_id','entry_id','position','id'])
$$;
REVOKE EXECUTE ON FUNCTION expenses.editing_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION expenses.editing_ready() TO household_app;

COMMIT;
