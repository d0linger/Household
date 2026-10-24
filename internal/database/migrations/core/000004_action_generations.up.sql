ALTER TABLE core.idempotency_records ADD COLUMN target_generations jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(target_generations)='object');
-- Pre-release action receipts did not bind destination generations and cannot certify replay safety.
DELETE FROM core.idempotency_records WHERE operation LIKE 'action/%';
CREATE INDEX idempotency_targets ON core.idempotency_records USING gin(target_generations);
