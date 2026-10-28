package maintenance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const restoreBarcodeOriginal = "CHECK ((((length(code) >= 1) AND (length(code) <= 64)) AND (code ~ '^[!-~]+$'::text)))"
const restoreBarcodeFlat = "CHECK (((length(code) >= 1) AND (length(code) <= 64) AND (code ~ '^[!-~]+$'::text)))"

// normalizeRestoreBarcodeConstraint is restricted to the verified restore
// target and Shopping8 by VerifyRestore. PostgreSQL's dump deparser flattens
// this one AND expression; preserve its migration-pinned representation without
// accepting an unfamiliar predicate or recapturing a readiness fingerprint.
func normalizeRestoreBarcodeConstraint(ctx context.Context, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin restored barcode normalization: %w", err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	var owner bool
	if err = tx.QueryRow(ctx, "SELECT session_user='household_owner' AND current_user='household_owner'").Scan(&owner); err != nil {
		return fmt.Errorf("inspect restore normalization authority: %w", err)
	}
	if !owner {
		return errors.New("restored barcode normalization requires the maintenance owner")
	}
	if _, err = tx.Exec(ctx, "SET LOCAL search_path=pg_catalog"); err != nil {
		return fmt.Errorf("set restored barcode schema resolution: %w", err)
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE ONLY shopping.barcodes IN ACCESS EXCLUSIVE MODE NOWAIT"); err != nil {
		return fmt.Errorf("lock restored barcode constraint: %w", err)
	}
	var definition string
	var knownShape bool
	err = tx.QueryRow(ctx, `SELECT pg_get_constraintdef(k.oid),
 c.relkind='r' AND c.relpersistence='p' AND NOT c.relispartition AND c.relowner='household_owner'::regrole
 AND NOT EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)
 AND(SELECT count(*)=1 AND bool_and(version=8 AND NOT dirty) FROM shopping.schema_migrations)
 AND k.contype='c' AND k.convalidated AND k.conislocal AND k.coninhcount=0 AND NOT k.connoinherit
 AND NOT k.condeferrable AND NOT k.condeferred AND k.conkey=ARRAY[a.attnum]::smallint[]
 AND a.attnum=4 AND a.atttypid='text'::regtype AND a.atttypmod=-1 AND a.attnotnull
 AND a.attcollation='pg_catalog."C"'::regcollation AND a.attgenerated='' AND a.attidentity=''
 AND(SELECT count(*)=1 AND bool_and(n.convalidated AND n.conislocal AND n.coninhcount=0
  AND NOT n.connoinherit AND NOT n.condeferrable AND NOT n.condeferred)
  FROM pg_constraint n WHERE n.conrelid=c.oid AND n.contype='n' AND n.conkey=ARRAY[a.attnum]::smallint[])
 FROM pg_class c JOIN pg_namespace ns ON ns.oid=c.relnamespace
 JOIN pg_attribute a ON a.attrelid=c.oid AND a.attname='code' AND NOT a.attisdropped
 JOIN pg_constraint k ON k.conrelid=c.oid AND k.conname='barcodes_code_valid'
 WHERE ns.nspname='shopping' AND c.relname='barcodes'`).Scan(&definition, &knownShape)
	if err != nil {
		return fmt.Errorf("inspect restored barcode constraint: %w", err)
	}
	if !knownShape || definition != restoreBarcodeOriginal && definition != restoreBarcodeFlat {
		return errors.New("unrecognized restored barcode constraint")
	}
	if definition == restoreBarcodeFlat {
		if _, err = tx.Exec(ctx, "ALTER TABLE shopping.barcodes DROP CONSTRAINT barcodes_code_valid"); err != nil {
			return fmt.Errorf("replace restored barcode constraint: %w", err)
		}
		if _, err = tx.Exec(ctx, `ALTER TABLE shopping.barcodes ADD CONSTRAINT barcodes_code_valid
 CHECK(length(code) BETWEEN 1 AND 64 AND code ~ '^[!-~]+$')`); err != nil {
			return fmt.Errorf("restore original barcode constraint: %w", err)
		}
	}
	if err = tx.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
 WHERE conrelid='shopping.barcodes'::regclass AND conname='barcodes_code_valid'`).Scan(&definition); err != nil {
		return fmt.Errorf("verify restored barcode definition: %w", err)
	}
	if definition != restoreBarcodeOriginal {
		return errors.New("restored barcode definition differs from the reviewed original")
	}
	var ready bool
	if err = tx.QueryRow(ctx, "SELECT shopping.barcode_ready()").Scan(&ready); err != nil {
		return fmt.Errorf("verify normalized barcode contract: %w", err)
	}
	if !ready {
		return errors.New("normalized barcode contract has unrelated drift")
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit restored barcode normalization: %w", err)
	}
	return nil
}
