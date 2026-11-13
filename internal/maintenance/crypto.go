package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"household/internal/audit"
	"household/internal/config"
	"household/internal/database"
	"household/internal/secretbox"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const cryptoMaintenanceLock = 721607138

// CryptoReport contains counts only, never protected row identifiers or contents.
type CryptoReport struct {
	Examined   int64            `json:"examined"`
	Changed    int64            `json:"changed"`
	References map[string]int64 `json:"key_references"`
}

// KeyRetirementReport proves only the inspected live database state. Historical
// backup and rollback horizons still require an explicit operator decision.
type KeyRetirementReport struct {
	KeyID                         string     `json:"key_id"`
	Active                        bool       `json:"active"`
	LiveReferences                int64      `json:"live_references"`
	VerifiedRows                  int64      `json:"verified_rows"`
	LastVerifiedBackup            *time.Time `json:"last_verified_backup"`
	BackupRetentionReviewRequired bool       `json:"backup_retention_review_required"`
	RetirementApproved            bool       `json:"retirement_approved"`
}

type protectedTable struct {
	name, key, purpose, body, predicate string
	limit                               int
}

var protectedTables = []protectedTable{
	{"totp_credentials", "user_id", "totp", "encrypted_secret", "true", 4096},
	{"passkey_credentials", "id", "passkey-credential", "encrypted_credential", "true", 131072},
	{"auth_challenges", "id", "", "payload", "purpose IN('passkey-login','passkey-register','passkey-reauth')", 131072},
	{"push_subscriptions", "id", "push-subscription", "encrypted_subscription", "true", 16384},
}

type protectedRow struct {
	binding  secretbox.Binding
	stored   secretbox.Stored
	original []byte
}

type protectedReader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func rollbackCrypto(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func lockCrypto(ctx context.Context, pool *pgxpool.Pool) (*pgxpool.Conn, func(), error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	var owner bool
	if err = conn.QueryRow(ctx, "SELECT current_user='household_owner' AND session_user='household_owner'").Scan(&owner); err != nil || !owner {
		conn.Release()
		return nil, nil, errors.New("crypto maintenance requires the actual household owner session")
	}
	var locked bool
	if err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", cryptoMaintenanceLock).Scan(&locked); err != nil {
		// Cancellation can race successful server-side acquisition. Never return
		// a potentially locked session to the pool.
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = conn.Hijack().Close(cleanup)
		return nil, nil, errors.New("crypto maintenance lock could not be acquired")
	}
	if !locked {
		conn.Release()
		return nil, nil, errors.New("crypto maintenance is already running or unavailable")
	}
	unlock := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var released bool
		if err := conn.QueryRow(cleanup, "SELECT pg_advisory_unlock($1)", cryptoMaintenanceLock).Scan(&released); err != nil || !released {
			_ = conn.Hijack().Close(cleanup)
			return
		}
		conn.Release()
	}
	if err = database.Ready(ctx, pool); err != nil {
		unlock()
		return nil, nil, err
	}
	return conn, unlock, nil
}

func readProtected(ctx context.Context, query protectedReader, table protectedTable, after string, limit int, rotate bool, active string) ([]protectedRow, error) {
	user, household, purpose := "coalesce(user_id::text,'')", "''", "'"+table.purpose+"'"
	body := table.body
	if table.name == "push_subscriptions" {
		household = "household_id::text"
	}
	if table.name == "auth_challenges" {
		purpose, body = "purpose", "convert_to(payload::text,'UTF8')"
	}
	// Refuse oversized legacy/corrupt values without transferring them to Go.
	body = "CASE WHEN octet_length(" + body + ")<=" + fmt.Sprint(table.limit*2) + " THEN " + body + " ELSE NULL END"
	// Every identifier/expression is a closed internal constant; values remain parameters.
	sql := "SELECT " + table.key + "::text," + user + "," + household + "," + purpose + ",encryption_format,encryption_key_id," + body +
		" FROM core." + table.name + " WHERE " + table.predicate + " AND ($1::uuid IS NULL OR " + table.key + ">$1::uuid)"
	// The initial page has no lower bound. Legacy schemas permit the zero UUID;
	// it must be inspected and rejected by binding validation, never skipped.
	var cursor any
	if after != "" {
		cursor = after
	}
	args := []any{cursor, limit}
	if rotate {
		sql += " AND NOT(encryption_format=2 AND encryption_key_id=$3)"
		args = append(args, active)
	}
	sql += " ORDER BY " + table.key + " LIMIT $2"
	if rotate {
		sql += " FOR UPDATE"
	}
	rows, err := query.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]protectedRow, 0, limit)
	for rows.Next() {
		var row protectedRow
		if err = rows.Scan(&row.binding.RecordID, &row.binding.UserID, &row.binding.HouseholdID, &row.binding.Purpose,
			&row.stored.Format, &row.stored.KeyID, &row.original); err != nil {
			return nil, err
		}
		if len(row.original) == 0 {
			return nil, errors.New("protected value is empty or exceeds storage bounds")
		}
		if table.name == "auth_challenges" {
			if len(row.original) > 180000 {
				return nil, errors.New("protected challenge exceeds storage bounds")
			}
			var payload map[string]json.RawMessage
			if err = strictPrivateJSON(row.original, &payload); err != nil || len(payload) != 1 || payload["sealed"] == nil {
				return nil, errors.New("invalid protected challenge container")
			}
			// Decode into an independent buffer: the original JSON is the exact
			// compare-and-swap value, and decoding may reuse a destination slice.
			if err = json.Unmarshal(payload["sealed"], &row.stored.Ciphertext); err != nil {
				return nil, errors.New("invalid protected challenge bytes")
			}
		} else {
			row.stored.Ciphertext = row.original
		}
		if len(row.stored.Ciphertext) > table.limit {
			return nil, errors.New("protected value exceeds storage bounds")
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func strictPrivateJSON(raw []byte, target any) error {
	// Reject duplicate object keys as well as unknown fields and trailing values.
	depth := 0
	var walk func(*json.Decoder) error
	walk = func(d *json.Decoder) error {
		depth++
		defer func() { depth-- }()
		if depth > 32 {
			return errors.New("private bundle nesting exceeds bounds")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate private bundle field")
				}
				seen[name] = true
				if err = walk(d); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := walk(d); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid private bundle delimiter")
		}
		_, err = d.Token()
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := walk(d); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing private bundle data")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return errors.New("invalid private bundle fields")
	}
	return nil
}

func verifyProtected(ctx context.Context, query protectedReader, ring *secretbox.Keyring) (CryptoReport, error) {
	report := CryptoReport{References: map[string]int64{}}
	for _, table := range protectedTables {
		after := ""
		for {
			rows, err := readProtected(ctx, query, table, after, 64, false, "")
			if err != nil {
				return report, err
			}
			for _, row := range rows {
				clear, err := ring.Open(row.binding, row.stored)
				if err != nil {
					return report, fmt.Errorf("protected %s verification failed: %w", table.name, err)
				}
				for i := range clear {
					clear[i] = 0
				}
				key := row.stored.KeyID
				if row.stored.Format == 1 && key == "v1" {
					key = ring.LegacyID()
				}
				report.References[key]++
				report.Examined++
				after = row.binding.RecordID
			}
			if len(rows) < 64 {
				break
			}
		}
	}
	return report, nil
}

// Reencrypt rotates at most batch protected records in one atomic, resumable
// owner transaction. Repeated calls select only records not yet on the active key.
func Reencrypt(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, batch int) (CryptoReport, error) {
	report := CryptoReport{References: map[string]int64{}}
	if batch < 1 || batch > 100 {
		return report, errors.New("rotation batch must be between 1 and 100")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ring, err := cfg.Keyring()
	if err != nil {
		return report, err
	}
	conn, unlock, err := lockCrypto(ctx, pool)
	if err != nil {
		return report, err
	}
	defer unlock()
	ctx = audit.WithRequest(audit.WithSystem(ctx, "operator"), value.UUID())
	tx, err := conn.Begin(ctx)
	if err != nil {
		return report, err
	}
	defer rollbackCrypto(tx)
	if err = audit.Bind(ctx, tx); err != nil {
		return report, err
	}
	for _, table := range protectedTables {
		remaining := batch - int(report.Changed)
		if remaining == 0 {
			break
		}
		rows, err := readProtected(ctx, tx, table, "", remaining, true, ring.ActiveID())
		if err != nil {
			return CryptoReport{}, err
		}
		for _, row := range rows {
			clear, err := ring.Open(row.binding, row.stored)
			if err != nil {
				return CryptoReport{}, fmt.Errorf("protected %s rotation failed: %w", table.name, err)
			}
			next, sealErr := ring.Seal(row.binding, clear)
			for i := range clear {
				clear[i] = 0
			}
			if sealErr != nil {
				return CryptoReport{}, sealErr
			}
			if len(next.Ciphertext) > table.limit {
				return CryptoReport{}, errors.New("rotated value exceeds storage bounds")
			}
			body := next.Ciphertext
			original := row.original
			cast := ""
			if table.name == "auth_challenges" {
				body, err = json.Marshal(struct {
					Sealed []byte `json:"sealed"`
				}{next.Ciphertext})
				if err != nil {
					return CryptoReport{}, err
				}
				cast = "::jsonb"
			}
			sql := "UPDATE core." + table.name + " SET encryption_format=$2,encryption_key_id=$3," + table.body + "=$4" + cast +
				" WHERE " + table.key + "=$1::uuid AND encryption_format=$5 AND encryption_key_id=$6 AND " + table.body + "=$7" + cast
			tag, err := tx.Exec(ctx, sql, row.binding.RecordID, next.Format, next.KeyID, body, row.stored.Format, row.stored.KeyID, original)
			if err != nil {
				return CryptoReport{}, err
			}
			if tag.RowsAffected() != 1 {
				return CryptoReport{}, errors.New("protected row changed during rotation")
			}
			if err = audit.Event(ctx, tx, "core", "crypto_rotation", row.binding.RecordID, "record_write", nil); err != nil {
				return CryptoReport{}, err
			}
			report.Examined++
			report.Changed++
			report.References[next.KeyID]++
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return CryptoReport{}, err
	}
	return report, nil
}

// CheckKeyRetirement verifies every live protected value without changing keys.
// A zero count is not permission to discard keys required by historical backups.
func CheckKeyRetirement(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, keyID string) (KeyRetirementReport, error) {
	report := KeyRetirementReport{KeyID: keyID, BackupRetentionReviewRequired: true}
	ring, err := cfg.Keyring()
	if err != nil {
		return report, err
	}
	found := false
	for _, id := range ring.ReadableIDs() {
		if id == keyID {
			found = true
		}
	}
	if !found {
		return report, errors.New("retirement requires a configured readable key identifier")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	conn, unlock, err := lockCrypto(ctx, pool)
	if err != nil {
		return report, err
	}
	defer unlock()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return report, err
	}
	defer rollbackCrypto(tx)
	inventory, err := verifyProtected(ctx, tx, ring)
	if err != nil {
		return report, err
	}
	report.Active = keyID == ring.ActiveID()
	report.LiveReferences = inventory.References[keyID]
	report.VerifiedRows = inventory.Examined
	err = tx.QueryRow(ctx, "SELECT max(verified_at) FROM core.backup_runs WHERE state='complete'").Scan(&report.LastVerifiedBackup)
	if err != nil {
		return report, err
	}
	return report, tx.Commit(ctx)
}
