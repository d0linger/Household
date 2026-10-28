BEGIN;

DO $predecessor$
DECLARE generation integer;dirty boolean;
BEGIN
 SELECT version,s.dirty INTO generation,dirty FROM core.schema_migrations s;
 IF dirty IS DISTINCT FROM false THEN RAISE EXCEPTION 'barcode audit requires a clean core schema';END IF;
 IF generation IS NULL OR (generation<>26 AND generation<32) THEN
  RAISE EXCEPTION 'local barcode upgrade requires reviewed core26 or approved versioned audit support; intermediate producer schemas are unsupported';
 END IF;
END $predecessor$;

CREATE TABLE shopping.barcodes(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),household_id uuid NOT NULL REFERENCES core.households(id),
 product_id uuid NOT NULL,code text COLLATE "C" NOT NULL,
 created_by uuid NOT NULL REFERENCES core.users(id),created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 UNIQUE(household_id,id),CONSTRAINT barcodes_household_code_key UNIQUE(household_id,code),
 CONSTRAINT barcodes_code_valid CHECK(length(code) BETWEEN 1 AND 64 AND code~'^[!-~]+$'),
 FOREIGN KEY(household_id,product_id) REFERENCES shopping.products(household_id,id)
);
CREATE INDEX barcodes_product ON shopping.barcodes(household_id,product_id);
REVOKE ALL ON shopping.barcodes FROM PUBLIC,household_app;
GRANT SELECT,INSERT,DELETE ON shopping.barcodes TO household_app;
-- PostgreSQL row locking requires UPDATE on at least one column. The immutable
-- trigger rejects every actual UPDATE, including an id=id no-op.
GRANT UPDATE(id) ON shopping.barcodes TO household_app;

CREATE FUNCTION shopping.guard_barcode() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE aliases integer;
BEGIN
 IF TG_OP='UPDATE' THEN RAISE EXCEPTION 'barcode assignment is immutable; review deletion and new assignment' USING ERRCODE='23514';END IF;
 IF current_setting('transaction_isolation')<>'read committed' THEN
  RAISE EXCEPTION 'barcode assignment requires read committed isolation' USING ERRCODE='23514';
 END IF;
 PERFORM id FROM shopping.products WHERE household_id=NEW.household_id AND id=NEW.product_id FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'barcode product is not in this household' USING ERRCODE='23503';END IF;
 SELECT count(*) INTO aliases FROM(SELECT id FROM shopping.barcodes WHERE household_id=NEW.household_id AND product_id=NEW.product_id LIMIT 100) bounded;
 IF aliases>=100 THEN RAISE EXCEPTION 'product barcode capacity exceeded' USING ERRCODE='23514';END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION shopping.guard_barcode() FROM PUBLIC,household_app;
CREATE TRIGGER barcode_identity BEFORE INSERT OR UPDATE ON shopping.barcodes FOR EACH ROW EXECUTE FUNCTION shopping.guard_barcode();

CREATE FUNCTION shopping.guard_barcode_pack() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF (NEW.pack_quantity,NEW.pack_unit) IS DISTINCT FROM (OLD.pack_quantity,OLD.pack_unit) THEN
  IF current_setting('transaction_isolation')<>'read committed' THEN
   RAISE EXCEPTION 'barcode product pack changes require read committed isolation' USING ERRCODE='23514';
  END IF;
  IF EXISTS(SELECT 1 FROM shopping.barcodes b WHERE b.household_id=OLD.household_id AND b.product_id=OLD.id) THEN
   RAISE EXCEPTION 'mapped product pack is in use; create another product variant' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION shopping.guard_barcode_pack() FROM PUBLIC,household_app;
CREATE TRIGGER barcode_product_pack BEFORE UPDATE OF pack_quantity,pack_unit ON shopping.products FOR EACH ROW EXECUTE FUNCTION shopping.guard_barcode_pack();

INSERT INTO core.audit_policies(schema_name,table_name,key_columns,safe_columns,label_column)
 VALUES('shopping','barcodes',ARRAY['id'],ARRAY['product_id','version'],NULL);

DO $audit$
DECLARE generation integer;dirty boolean;old_source text;
BEGIN
 SELECT version,s.dirty INTO generation,dirty FROM core.schema_migrations s;
 IF dirty IS DISTINCT FROM false THEN RAISE EXCEPTION 'barcode audit requires a clean core schema';END IF;
 IF generation=26 THEN
  SELECT replace(p.prosrc,chr(13),'') INTO old_source FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
  WHERE p.oid='core.audit_ready()'::regprocedure AND p.prosecdef AND p.provolatile='s' AND p.prorettype='boolean'::regtype
  AND p.proowner='household_owner'::regrole AND l.lanname='sql' AND p.proconfig=ARRAY['search_path=pg_catalog'];
  IF old_source IS DISTINCT FROM $expected$
 SELECT core.financial_requirements_ready() AND count(*)=77 AND bool_and(
  EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
  AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure)
 ) AND NOT EXISTS(
  SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
  AND c.relname<>'schema_migrations'
  AND NOT(n.nspname='fuel' AND c.relname='fuel_types') AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
  AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states',
   'financial_requirement_sets','financial_requirement_members','financial_artifact_scope_versions'))
  AND NOT EXISTS(SELECT 1 FROM core.audit_policies policy WHERE policy.schema_name=n.nspname AND policy.table_name=c.relname)
 ) FROM core.audit_policies p
$expected$ THEN RAISE EXCEPTION 'unsupported core26 audit definition; preserve its security checks';END IF;
  EXECUTE $install$CREATE OR REPLACE FUNCTION core.audit_ready() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $function$
 SELECT core.financial_requirements_ready() AND count(*)=78 AND bool_and(
  EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_change' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_change()'::regprocedure)
  AND EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgrelid=to_regclass(format('%I.%I',p.schema_name,p.table_name)) AND t.tgname='household_audit_truncate' AND t.tgenabled IN('O','A') AND t.tgfoid='core.capture_audit_truncate()'::regprocedure)
 ) AND NOT EXISTS(
  SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE c.relkind IN('r','p') AND n.nspname IN('core','shopping','fuel','recipes','pantry','expenses','chores','meters','documents','mealplan')
  AND c.relname<>'schema_migrations'
  AND NOT(n.nspname='fuel' AND c.relname='fuel_types') AND NOT(n.nspname='pantry' AND c.relname='lot_balances')
  AND NOT(n.nspname='core' AND c.relname IN('audit_log','audit_policies','units','module_installations','auth_challenges','rate_limit_buckets','jobs','outbox_events','event_deliveries','idempotency_records','search_documents','file_deletions','alert_states',
   'financial_requirement_sets','financial_requirement_members','financial_artifact_scope_versions'))
  AND NOT EXISTS(SELECT 1 FROM core.audit_policies policy WHERE policy.schema_name=n.nspname AND policy.table_name=c.relname)
 ) FROM core.audit_policies p
$function$;$install$;
 ELSIF generation>=32 THEN
  IF NOT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
   WHERE p.oid=to_regprocedure('core.shopping_barcode_audit_supported()') AND p.pronargs=0 AND p.prorettype='boolean'::regtype
   AND p.prosecdef AND p.provolatile='s' AND p.prokind='f' AND p.proowner='household_owner'::regrole
   AND l.lanname='sql' AND p.proconfig=ARRAY['search_path=pg_catalog']
   AND replace(p.prosrc,chr(13),'')=' SELECT core.shopping_barcode_audit_shape_v1() '
   AND NOT has_function_privilege('household_app',p.oid,'EXECUTE')) THEN
   RAISE EXCEPTION 'core lacks approved local barcode audit support';END IF;
  IF core.shopping_barcode_audit_supported() IS DISTINCT FROM true THEN RAISE EXCEPTION 'core barcode audit catalog mismatch';END IF;
 ELSE
  RAISE EXCEPTION 'local barcode upgrade requires reviewed core26 or approved versioned audit support; intermediate producer schemas are unsupported';
 END IF;
END $audit$;

DO $ready_install$
DECLARE column_spec jsonb;constraint_spec jsonb;index_spec jsonb;trigger_spec jsonb;guard_hash text;pack_hash text;
BEGIN
 SELECT jsonb_agg(jsonb_build_array(a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attgenerated,a.attidentity,a.attcollation::text,coalesce(a.attacl,'{}'::aclitem[])::text,
 coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum) INTO column_spec
 FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE a.attrelid='shopping.barcodes'::regclass AND a.attnum>0 AND NOT a.attisdropped;
 SELECT jsonb_agg(jsonb_build_array(c.conname,pg_get_constraintdef(c.oid),c.convalidated) ORDER BY c.conname) INTO constraint_spec FROM pg_constraint c WHERE c.conrelid='shopping.barcodes'::regclass;
 SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY pg_get_indexdef(i.indexrelid)) INTO index_spec FROM pg_index i WHERE i.indrelid='shopping.barcodes'::regclass;
 SELECT jsonb_agg(jsonb_build_array(t.tgname,pg_get_triggerdef(t.oid),t.tgenabled) ORDER BY t.tgname) INTO trigger_spec FROM pg_trigger t WHERE NOT t.tgisinternal AND t.tgname IN('barcode_identity','barcode_product_pack') AND t.tgrelid IN('shopping.barcodes'::regclass,'shopping.products'::regclass);
 SELECT md5(pg_get_functiondef('shopping.guard_barcode()'::regprocedure)),md5(pg_get_functiondef('shopping.guard_barcode_pack()'::regprocedure)) INTO guard_hash,pack_hash;
 EXECUTE replace(replace(replace(replace(replace(replace($definition$
 CREATE FUNCTION shopping.barcode_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $function$
 SELECT (SELECT jsonb_agg(jsonb_build_array(a.attname,a.atttypid::text,a.atttypmod,a.attnotnull,a.attgenerated,a.attidentity,a.attcollation::text,coalesce(a.attacl,'{}'::aclitem[])::text,
 coalesce(pg_get_expr(d.adbin,d.adrelid),'')) ORDER BY a.attnum) FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE a.attrelid='shopping.barcodes'::regclass AND a.attnum>0 AND NOT a.attisdropped)=#COLUMNS#::jsonb
 AND (SELECT jsonb_agg(jsonb_build_array(c.conname,pg_get_constraintdef(c.oid),c.convalidated) ORDER BY c.conname) FROM pg_constraint c WHERE c.conrelid='shopping.barcodes'::regclass)=#CONSTRAINTS#::jsonb
 AND (SELECT jsonb_agg(jsonb_build_array(pg_get_indexdef(i.indexrelid),i.indisvalid,i.indisready) ORDER BY pg_get_indexdef(i.indexrelid)) FROM pg_index i WHERE i.indrelid='shopping.barcodes'::regclass)=#INDEXES#::jsonb
 AND (SELECT jsonb_agg(jsonb_build_array(t.tgname,pg_get_triggerdef(t.oid),t.tgenabled) ORDER BY t.tgname) FROM pg_trigger t WHERE NOT t.tgisinternal AND t.tgname IN('barcode_identity','barcode_product_pack') AND t.tgrelid IN('shopping.barcodes'::regclass,'shopping.products'::regclass))=#TRIGGERS#::jsonb
 AND md5(pg_get_functiondef('shopping.guard_barcode()'::regprocedure))=#GUARD# AND md5(pg_get_functiondef('shopping.guard_barcode_pack()'::regprocedure))=#PACK#
 AND NOT EXISTS(SELECT 1 FROM pg_proc p WHERE p.oid IN('shopping.guard_barcode()'::regprocedure,'shopping.guard_barcode_pack()'::regprocedure)
  AND (p.provolatile<>'v' OR p.prosecdef OR p.proowner<>'household_owner'::regrole OR p.proconfig IS DISTINCT FROM ARRAY['search_path=pg_catalog']))
 AND NOT has_function_privilege('household_app','shopping.guard_barcode()','EXECUTE')
 AND NOT has_function_privilege('household_app','shopping.guard_barcode_pack()','EXECUTE')
 AND NOT EXISTS(SELECT 1 FROM pg_proc p,LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
  WHERE p.oid IN('shopping.guard_barcode()'::regprocedure,'shopping.guard_barcode_pack()'::regprocedure) AND a.grantee=0)
 AND EXISTS(SELECT 1 FROM core.audit_policies WHERE schema_name='shopping' AND table_name='barcodes' AND key_columns=ARRAY['id'] AND safe_columns=ARRAY['product_id','version'] AND label_column IS NULL AND ignored_columns=ARRAY['created_at','updated_at'])
 AND has_table_privilege('household_app','shopping.barcodes','SELECT') AND has_table_privilege('household_app','shopping.barcodes','INSERT') AND has_table_privilege('household_app','shopping.barcodes','DELETE')
 AND has_column_privilege('household_app','shopping.barcodes','id','UPDATE')
 AND NOT EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid='shopping.barcodes'::regclass AND a.attnum>0 AND NOT a.attisdropped
  AND a.attname<>'id' AND has_column_privilege('household_app','shopping.barcodes',a.attname,'UPDATE'))
 AND (SELECT count(*)=1 AND bool_and(a.attname='id' AND x.grantee='household_app'::regrole AND x.grantor='household_owner'::regrole
  AND x.privilege_type='UPDATE' AND NOT x.is_grantable)
  FROM pg_attribute a CROSS JOIN LATERAL aclexplode(CASE WHEN cardinality(a.attacl)>0 THEN a.attacl END) x
  WHERE a.attrelid='shopping.barcodes'::regclass AND a.attnum>0 AND NOT a.attisdropped)
 AND NOT has_table_privilege('household_app','shopping.barcodes','UPDATE') AND NOT has_table_privilege('household_app','shopping.barcodes','TRUNCATE')
 AND NOT EXISTS(SELECT 1 FROM pg_class c,LATERAL aclexplode(coalesce(c.relacl,acldefault('r',c.relowner))) a WHERE c.oid='shopping.barcodes'::regclass AND a.grantee=0)
 AND EXISTS(SELECT 1 FROM pg_class c WHERE c.oid='shopping.barcodes'::regclass AND c.relowner='household_owner'::regrole)
 $function$;
 $definition$,'#COLUMNS#',quote_literal(column_spec::text)),'#CONSTRAINTS#',quote_literal(constraint_spec::text)),'#INDEXES#',quote_literal(index_spec::text)),'#TRIGGERS#',quote_literal(trigger_spec::text)),'#GUARD#',quote_literal(guard_hash)),'#PACK#',quote_literal(pack_hash));
END $ready_install$;
REVOKE ALL ON FUNCTION shopping.barcode_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION shopping.barcode_ready() TO household_app;

COMMIT;
