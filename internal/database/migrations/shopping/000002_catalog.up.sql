BEGIN;

CREATE TABLE shopping.stores (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 200), note text NOT NULL DEFAULT '',
 created_by uuid NOT NULL REFERENCES core.users(id), created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), version bigint NOT NULL DEFAULT 1, UNIQUE(household_id,id)
);
CREATE TABLE shopping.aisles (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 store_id uuid NOT NULL, name text NOT NULL CHECK(length(name) BETWEEN 1 AND 200), sort_order bigint NOT NULL DEFAULT 0 CHECK(sort_order BETWEEN 0 AND 1000000000),
 created_by uuid NOT NULL REFERENCES core.users(id), created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), version bigint NOT NULL DEFAULT 1, UNIQUE(household_id,id),
 FOREIGN KEY(household_id,store_id) REFERENCES shopping.stores(household_id,id)
);
CREATE UNIQUE INDEX aisles_unique_name ON shopping.aisles(household_id,store_id,lower(name));
CREATE TABLE shopping.products (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 200), brand text NOT NULL DEFAULT '' CHECK(length(brand)<=300),
 pack_quantity numeric(18,6) NOT NULL DEFAULT 1 CHECK(pack_quantity>0),
 pack_unit text NOT NULL DEFAULT 'piece' REFERENCES core.units(code) CHECK(pack_unit IN('piece','g','kg','ml','l')),
 note text NOT NULL DEFAULT '', created_by uuid NOT NULL REFERENCES core.users(id), created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), version bigint NOT NULL DEFAULT 1, UNIQUE(household_id,id)
);
CREATE TABLE shopping.price_observations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), household_id uuid NOT NULL REFERENCES core.households(id),
 product_id uuid NOT NULL, store_id uuid NOT NULL, observed_on date NOT NULL,
 quantity numeric(18,6) NOT NULL CHECK(quantity>0), unit_code text NOT NULL REFERENCES core.units(code) CHECK(unit_code IN('piece','g','kg','ml','l')),
 price_minor bigint NOT NULL CHECK(price_minor BETWEEN 1 AND 1000000000000), note text NOT NULL DEFAULT '',
 created_by uuid NOT NULL REFERENCES core.users(id), created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), version bigint NOT NULL DEFAULT 1, UNIQUE(household_id,id),
 FOREIGN KEY(household_id,product_id) REFERENCES shopping.products(household_id,id),
 FOREIGN KEY(household_id,store_id) REFERENCES shopping.stores(household_id,id)
);
CREATE INDEX price_observations_latest ON shopping.price_observations(household_id,product_id,store_id,observed_on DESC,created_at DESC,id DESC);
ALTER TABLE shopping.lists ADD COLUMN store_id uuid,
 ADD COLUMN shopper_id uuid, ADD COLUMN shopper_until timestamptz,
 ADD FOREIGN KEY(household_id,store_id) REFERENCES shopping.stores(household_id,id),
 ADD FOREIGN KEY(household_id,shopper_id) REFERENCES core.memberships(household_id,id),
 ADD CHECK((shopper_id IS NULL)=(shopper_until IS NULL));
ALTER TABLE shopping.items ADD COLUMN product_id uuid, ADD COLUMN replacement_product_id uuid,
 ADD COLUMN availability text NOT NULL DEFAULT 'available' CHECK(availability IN('available','unavailable','substituted')),
 ADD COLUMN assignee_id uuid,
 ADD FOREIGN KEY(household_id,product_id) REFERENCES shopping.products(household_id,id),
 ADD FOREIGN KEY(household_id,replacement_product_id) REFERENCES shopping.products(household_id,id),
 ADD FOREIGN KEY(household_id,assignee_id) REFERENCES core.memberships(household_id,id),
 ADD CHECK((availability='substituted')=(replacement_product_id IS NOT NULL)),
 ADD CHECK(replacement_product_id IS NULL OR product_id IS NOT NULL AND replacement_product_id<>product_id);
CREATE INDEX items_product ON shopping.items(household_id,product_id);
GRANT SELECT,INSERT,UPDATE,DELETE ON shopping.stores,shopping.aisles,shopping.products,shopping.price_observations TO household_app;
COMMIT;
