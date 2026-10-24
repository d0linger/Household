BEGIN;
ALTER TABLE shopping.lists ADD COLUMN is_template boolean NOT NULL DEFAULT false;
ALTER TABLE shopping.products ADD COLUMN favorite boolean NOT NULL DEFAULT false;
CREATE INDEX products_favorites ON shopping.products(household_id,created_at DESC,id DESC) WHERE favorite;
COMMIT;
