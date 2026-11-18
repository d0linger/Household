BEGIN;
SELECT id FROM core.households ORDER BY id FOR UPDATE;

-- A late sample must not restart completed projection work for the whole
-- accumulated range. meters.invalidate now moves the interval cursor back only
-- to the new range's own start and, in the aggregates phase, keeps every hour
-- bucket before the aggregate cursor. Nothing else in the schema changes.
CREATE TEMP TABLE meters_invalidation_before(catalog jsonb NOT NULL,ready text NOT NULL) ON COMMIT DROP;
REVOKE ALL ON meters_invalidation_before FROM PUBLIC,household_app;
DO $predecessor$
DECLARE snapshot jsonb;
BEGIN
 IF NOT meters.expansion_ready() OR NOT meters.notification_ready() THEN
  RAISE EXCEPTION 'Bounded energy invalidation requires an accepted Meters3 contract';END IF;
 INSERT INTO meters_invalidation_before(catalog,ready)
 VALUES(meters.expansion_catalog(),pg_get_functiondef('meters.expansion_ready()'::regprocedure));
 -- Freeze the exact Meters3 definitions, as Core41 does, so a reviewed
 -- predecessor can be reconstructed without trusting live functions.
 SELECT jsonb_object_agg(p.oid::regprocedure::text,jsonb_build_object('definition',pg_get_functiondef(p.oid),
  'owner',p.proowner::regrole::text,'acl',p.proacl::text)) INTO snapshot FROM pg_proc p WHERE p.oid IN(
  'meters.invalidate(uuid,uuid,timestamptz,timestamptz,uuid)'::regprocedure,'meters.expansion_ready()'::regprocedure);
 IF (SELECT count(*) FROM jsonb_object_keys(snapshot))<>2 THEN RAISE EXCEPTION 'Unreviewed Meters3 invalidation predecessor';END IF;
 EXECUTE format('CREATE FUNCTION meters.invalidation_predecessor_catalog() RETURNS jsonb LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS %L',
  'SELECT '||quote_literal(snapshot::text)||'::jsonb');
END $predecessor$;
REVOKE ALL ON FUNCTION meters.invalidation_predecessor_catalog() FROM PUBLIC,household_app;

CREATE OR REPLACE FUNCTION meters.invalidate(p_household uuid,p_register uuid,p_from timestamptz,p_through timestamptz,p_actor uuid)
 RETURNS void LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE revision bigint;floor_at timestamptz;
BEGIN
 UPDATE meters.registers SET data_revision=data_revision+1 WHERE household_id=p_household AND id=p_register RETURNING data_revision,retained_from INTO revision,floor_at;
 IF floor_at IS NOT NULL THEN p_from=greatest(p_from,floor_at);END IF;
 IF revision IS NULL OR p_through<=p_from THEN RAISE EXCEPTION 'invalid energy invalidation' USING ERRCODE='23514';END IF;
 -- p_from is the point before the change, so intervals before it stay valid.
 -- Intervals phase: keep the cursor unless the new range starts at or before
 -- it; then resume at the new start, never at the merged range start.
 -- Aggregates phase: every interval is built and every hour before the
 -- aggregate cursor is final, so only the new range's intervals and the hours
 -- from min(cursor, new start) are redone.
 INSERT INTO meters.dirty_ranges AS old(register_id,household_id,from_at,through_at,source_revision,created_by)
 VALUES(p_register,p_household,p_from,p_through,revision,p_actor)
 ON CONFLICT(register_id) DO UPDATE SET
  from_at=CASE WHEN old.phase='aggregates' THEN least(coalesce(old.cursor_at,old.from_at),EXCLUDED.from_at)
   ELSE least(old.from_at,EXCLUDED.from_at) END,
  through_at=greatest(old.through_at,EXCLUDED.through_at),
  cursor_at=CASE WHEN old.phase='intervals' AND EXCLUDED.from_at>coalesce(old.cursor_at,old.from_at) THEN old.cursor_at
   ELSE EXCLUDED.from_at END,
  cursor_id=CASE WHEN old.phase='intervals' AND EXCLUDED.from_at>coalesce(old.cursor_at,old.from_at) THEN old.cursor_id
   ELSE '' END,
  phase='intervals',source_revision=EXCLUDED.source_revision,updated_at=clock_timestamp();
END $$;

DO $compatibility$
DECLARE old_catalog jsonb;old_ready text;new_catalog jsonb;key text;needle text;changed text;added text;
BEGIN
 SELECT catalog,ready INTO STRICT old_catalog,old_ready FROM meters_invalidation_before;
 new_catalog=meters.expansion_catalog();
 changed='meters.invalidate(uuid,uuid,timestamp with time zone,timestamp with time zone,uuid)';
 added='meters.invalidation_predecessor_catalog()';
 FOREACH key IN ARRAY ARRAY['relations','columns','constraints','indexes','triggers'] LOOP
  IF new_catalog->key IS DISTINCT FROM old_catalog->key THEN RAISE EXCEPTION 'Unrelated Meters metadata changed: %',key;END IF;
 END LOOP;
 -- Only the replaced function's definition hash may differ (its ACL, owner,
 -- security and settings stay as reviewed), and only the frozen predecessor
 -- snapshot is new.
 IF (SELECT jsonb_agg(CASE WHEN value->>0=changed THEN value-1 ELSE value END ORDER BY ordinal)
   FROM jsonb_array_elements(new_catalog->'functions') WITH ORDINALITY x(value,ordinal) WHERE value->>0<>added)
  IS DISTINCT FROM (SELECT jsonb_agg(CASE WHEN value->>0=changed THEN value-1 ELSE value END ORDER BY ordinal)
   FROM jsonb_array_elements(old_catalog->'functions') WITH ORDINALITY x(value,ordinal))
 OR (SELECT count(*) FROM jsonb_array_elements(new_catalog->'functions') x(value) WHERE value->>0 IN(changed,added))<>2 THEN
  RAISE EXCEPTION 'Unrelated Meters functions changed';END IF;
 needle=quote_literal(old_catalog::text)||'::jsonb';
 IF (length(old_ready)-length(replace(old_ready,needle,'')))/length(needle)<>1 THEN
  RAISE EXCEPTION 'Unexpected Meters readiness snapshot';END IF;
 EXECUTE replace(old_ready,needle,quote_literal(new_catalog::text)||'::jsonb');
 IF NOT meters.expansion_ready() OR NOT meters.notification_ready() THEN
  RAISE EXCEPTION 'Incomplete bounded energy invalidation';END IF;
END $compatibility$;
COMMIT;
