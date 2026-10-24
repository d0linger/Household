BEGIN;
CREATE TABLE shopping.checkouts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 list_id uuid NOT NULL, request_hash bytea NOT NULL CHECK(octet_length(request_hash)=32),
 item_count integer NOT NULL CHECK(item_count BETWEEN 1 AND 500),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=4194304),
 created_by uuid NOT NULL REFERENCES core.users(id), created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(household_id,id), UNIQUE(household_id,list_id),
 FOREIGN KEY(household_id,list_id) REFERENCES shopping.lists(household_id,id)
);
CREATE TRIGGER checkouts_immutable BEFORE UPDATE OR DELETE ON shopping.checkouts
 FOR EACH ROW EXECUTE FUNCTION core.reject_mutation();
REVOKE ALL ON shopping.checkouts FROM PUBLIC,household_app;
GRANT SELECT,INSERT ON shopping.checkouts TO household_app;
COMMIT;
