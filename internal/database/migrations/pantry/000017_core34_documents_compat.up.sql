BEGIN;
DO $compatibility$
DECLARE baseline jsonb;old_audit text;new_audit text;ready text;actual text;old_hash text;new_hash text;
BEGIN
 IF NOT(SELECT count(*)=1 AND bool_and(version=34 AND NOT dirty) FROM core.schema_migrations)
  OR NOT core.finance_ready() OR NOT core.audit_ready() OR NOT chores.expansion_ready() THEN RAISE EXCEPTION 'Pantry compatibility requires reviewed Core34';END IF;
 baseline=documents.predecessor_catalog()->'functions';old_audit=baseline->'core.audit_ready()'->>'definition';
 new_audit=replace(old_audit,'core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND count(*)=152','core.finance_ready() AND chores.expansion_ready() AND meters.expansion_ready() AND documents.expansion_ready() AND count(*)=163');
 SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid='core.audit_ready()'::regprocedure
  AND p.proowner::regrole::text=baseline->'core.audit_ready()'->>'owner'
  AND p.proacl::text IS NOT DISTINCT FROM baseline->'core.audit_ready()'->>'acl';
 IF actual IS DISTINCT FROM new_audit OR new_audit=old_audit THEN RAISE EXCEPTION 'unreviewed Documents audit definition';END IF;
 ready=baseline->'pantry.prepared_capture_ready()'->>'definition';
 SELECT pg_get_functiondef(p.oid) INTO actual FROM pg_proc p WHERE p.oid='pantry.prepared_capture_ready()'::regprocedure
  AND p.proowner::regrole::text=baseline->'pantry.prepared_capture_ready()'->>'owner'
  AND p.proacl::text IS NOT DISTINCT FROM baseline->'pantry.prepared_capture_ready()'->>'acl';
 old_hash=md5(old_audit);new_hash=md5(new_audit);
 IF actual IS DISTINCT FROM ready OR old_hash=new_hash
  OR (length(ready)-length(replace(ready,old_hash,'')))/length(old_hash)<>1
  OR position(quote_literal(replace(pantry.prepared_capture_catalog()::text,new_hash,old_hash))||'::jsonb' in ready)=0
  OR position(quote_literal(md5(pg_get_functiondef('pantry.prepared_capture_catalog()'::regprocedure))) in ready)=0 THEN
  RAISE EXCEPTION 'Pantry Documents catalog differs outside its reviewed audit hash';
 END IF;
 EXECUTE replace(ready,old_hash,new_hash);
 IF NOT pantry.prepared_capture_ready() THEN RAISE EXCEPTION 'Pantry Documents compatibility failed';END IF;
END $compatibility$;
COMMIT;
