package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type PantryBatchPostInput struct {
	Generation  int64  `json:"generation"`
	Version     int64  `json:"version"`
	Fingerprint string `json:"fingerprint"`
}

func (s *Store) PostPantryBatch(ctx context.Context, id auth.Identity, draft, key, requestID string, in PantryBatchPostInput) (Record, error) {
	fingerprint, parseErr := hex.DecodeString(in.Fingerprint)
	if !value.ValidUUID(draft) || !value.ValidUUID(key) || in.Generation < 1 || in.Version < 1 || parseErr != nil || len(fingerprint) != sha256.Size {
		return nil, Invalid{"Gespeicherten Entwurf mit geprüftem Stand und gültiger Vorgangskennung bestätigen"}
	}
	draft, key, in.Fingerprint = strings.ToLower(draft), strings.ToLower(key), strings.ToLower(in.Fingerprint)
	ctx, cancel := context.WithTimeout(audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID), 20*time.Second)
	defer cancel()
	tx, err := s.BeginMutation(ctx, id, "pantry", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	raw, err := json.Marshal(struct {
		Draft string               `json:"draft"`
		Input PantryBatchPostInput `json:"input"`
	}{draft, in})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "pantry-batch-post/"+id.HouseholdID+id.UserID+key); err != nil {
		return nil, err
	}
	// A request key cannot be silently reused for a different draft. This
	// permanent local receipt is independent of generic replay-cache pruning.
	var usedDraft string
	err = tx.QueryRow(ctx, "SELECT draft_id FROM pantry.stock_batch_receipts WHERE household_id=$1 AND created_by=$2 AND request_key=$3", id.HouseholdID, id.UserID, key).Scan(&usedDraft)
	if err == nil && usedDraft != draft {
		return nil, ErrConflict
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	current, err := readPantryBatchDraft(ctx, tx, id, draft, true)
	if err != nil {
		return nil, err
	}
	var priorHash, prior []byte
	err = tx.QueryRow(ctx, "SELECT request_hash,snapshot FROM pantry.stock_batch_receipts WHERE household_id=$1 AND draft_id=$2 AND created_by=$3", id.HouseholdID, draft, id.UserID).Scan(&priorHash, &prior)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return nil, ErrConflict
		}
		return decode(prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if current["state"] != "active" || current["expired"] == true || Int(current["version"]) != in.Version || current["fingerprint"] != in.Fingerprint {
		return nil, ErrConflict
	}
	input, err := pantryBatchDraftInput(current)
	if err != nil {
		return nil, err
	}
	effects, err := s.validatePantryBatch(ctx, tx, id, input)
	if err != nil {
		return nil, err
	}
	posting := value.UUID()
	var at time.Time
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return nil, err
	}
	lines := make([]Record, 0, len(effects))
	movements := 0
	for i := range effects {
		effect := &effects[i]
		lotID := effect.Input.LotID
		if input.Kind == "intake" {
			lotID = value.UUID()
			effect.Lot["label"] = effect.Data["notes"]
		}
		movement := ""
		if effect.Delta != "0.000000" {
			movement = value.UUID()
			movements++
		}
		lines = append(lines, Record{"line_id": effect.Input.ID, "lot_id": lotID, "movement_id": movement,
			"product_id": effect.Lot["product_id"], "product_name": effect.Lot["product_name"], "unit": effect.Lot["unit"],
			"location_id": effect.Lot["location_id"], "location": effect.Lot["location"], "label": effect.Lot["label"],
			"product_archived": effect.Lot["product_archived"], "location_archived": effect.Lot["location_archived"],
			"before": effect.Before, "counted": effect.Count, "delta": effect.Delta, "reason": effect.Input.Reason, "pack": effect.Pack,
			"received_on": effect.Input.ReceivedOn, "best_before_on": effect.Input.BestBefore, "use_by_on": effect.Input.UseBy,
			"opened_on": effect.Input.OpenedOn, "after_open_days": effect.Input.AfterOpenDays, "after_open_source": effect.Input.AfterOpenSource, "after_open_note": effect.Input.AfterOpenNote})
	}
	result := Record{"id": posting, "draft_id": draft, "generation": in.Generation, "draft_version": in.Version, "kind": input.Kind, "title": input.Title,
		"created_by": id.UserID, "actor": id.DisplayName, "created_at": at.UTC().Format(time.RFC3339Nano), "line_count": len(lines), "movement_count": movements, "lines": lines}
	snapshot, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO pantry.stock_batch_receipts(id,household_id,draft_id,generation,draft_version,kind,created_by,request_key,request_hash,line_count,movement_count,snapshot)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, posting, id.HouseholdID, draft, in.Generation, in.Version, input.Kind, id.UserID, key, hash[:], len(lines), movements, snapshot); err != nil {
		return nil, shoppingContention(err)
	}
	for i, effect := range effects {
		if err = s.writePantryBatchEffect(ctx, tx, id, posting, in.Generation, input.Kind, effect, lines[i], requestID); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE pantry.stock_batch_drafts SET state='posted' WHERE household_id=$1 AND id=$2", id.HouseholdID, draft); err != nil {
		return nil, shoppingContention(err)
	}
	if err = Audit(ctx, tx, id, "pantry", "stock_batch_receipts", posting, "post", requestID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return result, nil
}

func (s *Store) writePantryBatchEffect(ctx context.Context, tx pgx.Tx, id auth.Identity, posting string, generation int64, kind string, effect pantryBatchEffect, line Record, requestID string) error {
	m, _ := s.Module("pantry")
	lots, _ := m.Entity("lots")
	lid := String(line["lot_id"])
	if kind == "intake" {
		lot, err := save(ctx, tx, id, m, lots, lid, effect.Data, true, 0)
		if err != nil {
			return shoppingContention(err)
		}
		if err = s.afterWrite(ctx, tx, id, m, lots, lot, generation); err != nil {
			return err
		}
		if err = Audit(ctx, tx, id, "pantry", "lots", lid, "record_write", requestID); err != nil {
			return err
		}
	}
	if movement := String(line["movement_id"]); movement != "" {
		reason := "adjust"
		if kind == "intake" {
			reason = "receive"
		}
		var raw []byte
		err := tx.QueryRow(ctx, `INSERT INTO pantry.stock_movements AS m(id,household_id,lot_id,quantity_delta,reason,note,created_by,correlation_id)
 VALUES($1,$2,$3,$4::numeric,$5,$6,$7,$8) RETURNING to_jsonb(m)`, movement, id.HouseholdID, lid, effect.Delta, reason, effect.Input.Reason, id.UserID, posting).Scan(&raw)
		if err != nil {
			return shoppingContention(err)
		}
		row, err := decode(raw)
		if err != nil {
			return err
		}
		e, _ := m.Entity("stock_movements")
		if err = s.afterWrite(ctx, tx, id, m, e, row, generation); err != nil {
			return err
		}
		if err = Audit(ctx, tx, id, "pantry", "stock_movements", movement, "record_write", requestID); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO pantry.stock_batch_lines(household_id,receipt_id,line_id,lot_id,movement_id,before_quantity,counted_quantity,quantity_delta,unit_code)
 VALUES($1,$2,$3,$4,nullif($5,'')::uuid,$6::numeric,$7::numeric,$8::numeric,$9)`, id.HouseholdID, posting, effect.Input.ID, lid, line["movement_id"], effect.Before, effect.Count, effect.Delta, effect.Lot["unit"])
	return shoppingContention(err)
}
