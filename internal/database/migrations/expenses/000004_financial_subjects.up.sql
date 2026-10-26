BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM expenses.entries e WHERE NOT EXISTS(SELECT 1 FROM core.module_states s
  WHERE s.household_id=e.household_id AND s.module_id='expenses')) THEN
  RAISE EXCEPTION 'financial source generation is missing';
 END IF;
END $$;
-- Historical data stays explicitly shared; never infer responsibility from payer or creator.
INSERT INTO core.financial_policies(household_id,module_id,module_generation,resource_type,resource_id,created_by,changed_by)
 SELECT e.household_id,'expenses',s.data_generation,'entries',e.id,e.created_by,e.created_by
 FROM expenses.entries e JOIN core.module_states s ON s.household_id=e.household_id AND s.module_id='expenses';
INSERT INTO core.financial_subjects(household_id,module_id,module_generation,resource_type,resource_id,policy_id,created_by,changed_by)
 SELECT e.household_id,'expenses',p.module_generation,'entries',e.id,p.id,e.created_by,e.created_by
 FROM expenses.entries e JOIN core.financial_policies p ON p.household_id=e.household_id AND p.module_id='expenses'
  AND p.resource_type='entries' AND p.resource_id=e.id AND p.retired_at IS NULL;
INSERT INTO core.financial_subjects(household_id,module_id,module_generation,resource_type,resource_id,policy_id,created_by,changed_by)
 SELECT l.household_id,'expenses',p.module_generation,'entry_lines',l.id,p.id,l.created_by,l.created_by
 FROM expenses.entry_lines l JOIN core.financial_policies p ON p.household_id=l.household_id AND p.module_id='expenses'
  AND p.resource_type='entries' AND p.resource_id=l.entry_id AND p.retired_at IS NULL;

CREATE FUNCTION expenses.bind_financial_subject() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE source_household uuid; source_id uuid; parent_id uuid; generation bigint; policy_id uuid; actor uuid;
BEGIN
 IF TG_OP='DELETE' THEN
  source_household=OLD.household_id; source_id=OLD.id; actor=OLD.created_by;
 ELSE
  source_household=NEW.household_id; source_id=NEW.id; actor=NEW.created_by;
 END IF;
 SELECT data_generation INTO STRICT generation FROM core.module_states WHERE household_id=source_household AND module_id='expenses';
 IF current_user<>'household_owner' THEN actor=nullif(current_setting('household.audit_user',true),'')::uuid; END IF;
 IF TG_OP='DELETE' THEN
  UPDATE core.financial_subjects SET retired_at=clock_timestamp()
   WHERE household_id=source_household AND module_id='expenses' AND module_generation=generation
    AND resource_type=TG_TABLE_NAME AND resource_id=source_id AND retired_at IS NULL;
  IF TG_TABLE_NAME='entries' THEN
   UPDATE core.financial_policies SET retired_at=clock_timestamp()
    WHERE household_id=source_household AND module_id='expenses' AND module_generation=generation
     AND resource_type='entries' AND resource_id=source_id AND retired_at IS NULL;
  END IF;
  RETURN NULL;
 END IF;
 IF TG_TABLE_NAME='entries' THEN
  INSERT INTO core.financial_policies(household_id,module_id,module_generation,resource_type,resource_id,created_by,changed_by)
   VALUES(source_household,'expenses',generation,'entries',source_id,actor,actor) RETURNING id INTO policy_id;
 ELSE
  parent_id=NEW.entry_id;
  SELECT p.id INTO STRICT policy_id FROM core.financial_policies p
   WHERE p.household_id=source_household AND p.module_id='expenses' AND p.module_generation=generation
    AND p.resource_type='entries' AND p.resource_id=parent_id AND p.retired_at IS NULL FOR KEY SHARE NOWAIT;
 END IF;
 INSERT INTO core.financial_subjects(household_id,module_id,module_generation,resource_type,resource_id,policy_id,created_by,changed_by)
  VALUES(source_household,'expenses',generation,TG_TABLE_NAME,source_id,policy_id,actor,actor);
 RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION expenses.bind_financial_subject() FROM PUBLIC,household_app;
-- Run before household_audit_change so later audit bindings see this identity.
CREATE TRIGGER financial_subject AFTER INSERT OR DELETE ON expenses.entries
 FOR EACH ROW EXECUTE FUNCTION expenses.bind_financial_subject();
CREATE TRIGGER financial_subject AFTER INSERT OR DELETE ON expenses.entry_lines
 FOR EACH ROW EXECUTE FUNCTION expenses.bind_financial_subject();

CREATE FUNCTION expenses.financial_bindings_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT core.financial_foundation_ready() AND NOT EXISTS(SELECT 1 FROM (VALUES
 ('expenses.entries','financial_subject'),('expenses.entry_lines','financial_subject')
 ) required(relation,name) WHERE NOT EXISTS(
 SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
 WHERE t.tgrelid=to_regclass(required.relation) AND t.tgname=required.name AND NOT t.tgisinternal
 AND t.tgfoid=to_regprocedure('expenses.bind_financial_subject()') AND t.tgtype=13
 AND t.tgenabled IN('O','A') AND t.tgqual IS NULL AND t.tgattr=''::int2vector AND t.tgnargs=0
 AND NOT t.tgdeferrable AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path=pg_catalog']))
 AND NOT has_function_privilege('household_app','expenses.bind_financial_subject()','EXECUTE')
$$;
REVOKE ALL ON FUNCTION expenses.financial_bindings_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION expenses.financial_bindings_ready() TO household_app;
COMMIT;
