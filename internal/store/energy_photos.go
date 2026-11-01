package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// PrepareEnergyPhoto runs inside the upload's authorized mutation transaction,
// after LockResource and before any physical file is created.
func (s *Store) PrepareEnergyPhoto(ctx context.Context, tx pgx.Tx, id auth.Identity, key, reading string,
	generation, revision int64, digest []byte,
) (Record, bool, error) {
	if !energyIdentityValid(id) || !id.CanWrite() || !value.ValidUUID(key) || !value.ValidUUID(reading) || revision < 1 || len(digest) != 32 {
		return nil, false, ErrForbidden
	}
	if err := checkSession(ctx, tx, id); err != nil {
		return nil, false, err
	}
	current, err := requireModule(ctx, tx, id, "meters")
	if err != nil || current != generation {
		if err != nil {
			return nil, false, err
		}
		return nil, false, ErrConflict
	}
	var previousHash, raw []byte
	var operation string
	var previousGeneration int64
	err = tx.QueryRow(ctx, `SELECT request_hash,response_payload,operation,module_generation FROM meters.command_receipts
 WHERE household_id=$1 AND user_id=$2 AND command_key=$3`, id.HouseholdID, id.UserID, key).
		Scan(&previousHash, &raw, &operation, &previousGeneration)
	if err == nil {
		if !bytes.Equal(previousHash, digest) || operation != "photo/"+reading || previousGeneration != generation {
			return nil, false, ErrConflict
		}
		result, err := decode(raw)
		return result, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	var found bool
	err = tx.QueryRow(ctx, `SELECT true FROM meters.readings WHERE household_id=$1 AND id=$2 AND version=$3 FOR SHARE`,
		id.HouseholdID, reading, revision).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrConflict
	}
	return nil, false, err
}

func SaveEnergyPhotoReceipt(ctx context.Context, tx pgx.Tx, id auth.Identity, key, reading, attachment string,
	generation, revision int64, digest []byte,
) (Record, error) {
	result := Record{"attachment_id": attachment, "reading_id": reading, "reading_revision": revision, "generation": generation}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO meters.command_receipts(household_id,user_id,command_key,operation,module_generation,request_hash,response_payload)
 VALUES($1,$2,$3,$4,$5,$6,$7)`, id.HouseholdID, id.UserID, key, "photo/"+reading, generation, digest, raw)
	return result, err
}
