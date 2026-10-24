BEGIN;
CREATE OR REPLACE FUNCTION expenses.protect_posted() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent_id uuid; parent_state text;
BEGIN
 IF current_user='household_owner' THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
 END IF;
 IF TG_TABLE_NAME='entries' THEN
  IF OLD.state='posted' THEN RAISE EXCEPTION 'posted entry is immutable' USING ERRCODE='23514'; END IF;
 ELSE
  IF TG_OP='UPDATE' AND NEW.entry_id IS DISTINCT FROM OLD.entry_id THEN
   RAISE EXCEPTION 'allocation parent is immutable' USING ERRCODE='23514';
  END IF;
  IF TG_OP='DELETE' THEN parent_id=OLD.entry_id; ELSE parent_id=NEW.entry_id; END IF;
  SELECT state INTO parent_state FROM expenses.entries WHERE id=parent_id FOR UPDATE;
  IF parent_state='posted' THEN RAISE EXCEPTION 'posted allocation is immutable' USING ERRCODE='23514'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;
COMMIT;
