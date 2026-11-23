BEGIN;
ALTER TABLE core.users
 DROP CONSTRAINT users_username_check,
 ADD CONSTRAINT users_username_check CHECK (
  username ~ '^[A-Za-z0-9_.-]{3,64}$' OR (
   octet_length(username) <= 254
   AND octet_length(split_part(username, '@', 1)) <= 64
   AND username ~ '^[A-Za-z0-9_%+-]+([.][A-Za-z0-9_%+-]+)*@[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?([.][A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$'
  )
 );

-- Advance the exact Fuel pins on the Core version.
DO $compatibility$
DECLARE authority text;overview text;ready text;needle text='bool_and(version=44 AND NOT dirty)';old_hash text;
BEGIN
 authority=pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure);
 overview=pg_get_functiondef('fuel.overview_ready()'::regprocedure);
 ready=pg_get_functiondef('fuel.module_ready()'::regprocedure);
 old_hash=md5(authority);
 IF (length(authority)-length(replace(authority,needle,'')))/length(needle)<>1
 OR (length(overview)-length(replace(overview,needle,'')))/length(needle)<>1
 OR (length(ready)-length(replace(ready,old_hash,'')))/length(old_hash)<>1 THEN
  RAISE EXCEPTION 'Unreviewed Core44 Fuel predecessor contract';END IF;
 EXECUTE replace(authority,needle,'bool_and(version=45 AND NOT dirty)');
 EXECUTE replace(overview,needle,'bool_and(version=45 AND NOT dirty)');
 EXECUTE replace(ready,old_hash,md5(pg_get_functiondef('fuel.module_authorize(bigint,jsonb,uuid,bigint,uuid)'::regprocedure)));
END $compatibility$;

COMMIT;
