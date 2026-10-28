BEGIN;
ALTER TABLE shopping.checkouts ADD COLUMN history_bytes integer
 GENERATED ALWAYS AS (octet_length(snapshot::text)) STORED;
CREATE INDEX checkouts_history ON shopping.checkouts(household_id,created_at DESC,id DESC);
CREATE INDEX items_repurchase_product ON shopping.items(household_id,list_id,(coalesce(replacement_product_id,product_id)));
CREATE FUNCTION shopping.repurchase_ready() RETURNS boolean
LANGUAGE sql STABLE SET search_path=pg_catalog AS $$
 SELECT EXISTS(SELECT 1 FROM pg_attribute a JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE a.attrelid='shopping.checkouts'::regclass AND a.attname='history_bytes' AND a.atttypid='integer'::regtype
 AND NOT a.attisdropped AND a.attgenerated='s' AND pg_get_expr(d.adbin,d.adrelid)='octet_length((snapshot)::text)')
 AND EXISTS(SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid
 WHERE i.indrelid='shopping.checkouts'::regclass AND c.relname='checkouts_history' AND i.indisvalid AND i.indisready
 AND NOT i.indisunique AND i.indpred IS NULL AND i.indexprs IS NULL
 AND pg_get_indexdef(i.indexrelid)='CREATE INDEX checkouts_history ON shopping.checkouts USING btree (household_id, created_at DESC, id DESC)')
 AND EXISTS(SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_am am ON am.oid=c.relam
 WHERE i.indrelid='shopping.items'::regclass AND c.relname='items_repurchase_product' AND i.indisvalid AND i.indisready
 AND NOT i.indisunique AND i.indpred IS NULL AND i.indnkeyatts=3 AND i.indnatts=3 AND am.amname='btree'
 AND pg_get_indexdef(i.indexrelid,1,true)='household_id' AND pg_get_indexdef(i.indexrelid,2,true)='list_id'
 AND regexp_replace(pg_get_expr(i.indexprs,i.indrelid),'[[:space:]()]','','g')='COALESCEreplacement_product_id,product_id');
$$;
REVOKE ALL ON FUNCTION shopping.repurchase_ready() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION shopping.repurchase_ready() TO household_app;
COMMIT;
