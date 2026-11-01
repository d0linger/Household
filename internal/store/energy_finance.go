package store

import (
	"context"
	"errors"
	"slices"
	"strings"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) energyFinancialCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string, in EnergyCommand, data Record, requestID string) (Record, error) {
	var result Record
	var err error
	switch in.Action {
	case "tariff.save":
		if in.ID != "" || in.Version != 0 {
			return nil, Invalid{"Tarifpreise bleiben unverändert; einen neuen datierten Tarifstand anlegen"}
		}
		if data["close_previous"] == true {
			_, err = tx.Exec(ctx, `UPDATE meters.tariff_versions SET ends_before=$3,version=version+1
 WHERE household_id=$1 AND register_id=$2 AND ends_before IS NULL AND starts_on<$3::date`,
				id.HouseholdID, data["register_id"], data["starts_on"])
			if err != nil {
				return nil, err
			}
		}
		result, err = energyObject(ctx, tx, `INSERT INTO meters.tariff_versions AS x(id,household_id,register_id,starts_on,ends_before,
 price_minor_per_unit,base_minor_per_day,label,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
 RETURNING jsonb_build_object('id',x.id,'version',x.version,'costs_use_current_tariff_versions',true)`,
			rid, id.HouseholdID, data["register_id"], data["starts_on"], energyNullable(data, "ends_before"),
			data["price_minor_per_unit"], data["base_minor_per_day"], data["label"], id.UserID)
	case "payment.save":
		if in.Version == 0 {
			result, err = energyObject(ctx, tx, `INSERT INTO meters.payments AS x(id,household_id,register_id,paid_on,amount_minor,note,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING jsonb_build_object('id',x.id,'version',x.version)`,
				rid, id.HouseholdID, data["register_id"], data["paid_on"], data["amount_minor"], String(data["note"]), id.UserID)
		} else {
			result, err = energyObject(ctx, tx, `UPDATE meters.payments AS x SET paid_on=$4,amount_minor=$5,note=$6,
 version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2 AND version=$3 AND register_id=$7
 RETURNING jsonb_build_object('id',x.id,'version',x.version)`,
				id.HouseholdID, rid, in.Version, data["paid_on"], data["amount_minor"], String(data["note"]), data["register_id"])
		}
	case "bill.save":
		if !slices.Contains([]string{"invoice", "advance"}, String(data["kind"])) {
			return nil, Invalid{"Rechnung oder bestätigten Abschlag auswählen"}
		}
		if in.Version == 0 {
			result, err = energyObject(ctx, tx, `INSERT INTO meters.bills AS x(id,household_id,register_id,occurred_on,title,amount_minor,kind,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING jsonb_build_object('id',x.id,'version',x.version,'confirmed',false)`,
				rid, id.HouseholdID, data["register_id"], data["occurred_on"], data["title"], data["amount_minor"], data["kind"], id.UserID)
		} else {
			result, err = energyObject(ctx, tx, `UPDATE meters.bills AS x SET occurred_on=$4,title=$5,amount_minor=$6,kind=$7,
 updated_at=clock_timestamp(),version=version+1 WHERE household_id=$1 AND id=$2 AND version=$3 AND register_id=$8 AND confirmed_at IS NULL
 RETURNING jsonb_build_object('id',x.id,'version',x.version,'confirmed',false)`,
				id.HouseholdID, rid, in.Version, data["occurred_on"], data["title"], data["amount_minor"], data["kind"], data["register_id"])
		}
	case "bill.confirm":
		result, err = energyObject(ctx, tx, `UPDATE meters.bills AS x SET confirmed_at=clock_timestamp(),updated_at=clock_timestamp(),version=version+1
 WHERE household_id=$1 AND id=$2 AND version=$3 AND confirmed_at IS NULL
 RETURNING jsonb_build_object('id',x.id,'version',x.version,'confirmed',true)`, id.HouseholdID, rid, in.Version)
	case "bill.handoff":
		return s.energyBillToExpense(ctx, tx, id, in, data, requestID)
	}
	if errors.Is(err, ErrNotFound) {
		return nil, ErrConflict
	}
	return result, err
}

// This is an explicit user-reviewed manual financial snapshot. It does not grant
// a telemetry adapter authority to post money or invent an external payment.
func (s *Store) energyBillToExpense(ctx context.Context, tx pgx.Tx, id auth.Identity, in EnergyCommand, data Record, requestID string) (Record, error) {
	bill, err := energyObject(ctx, tx, `SELECT to_jsonb(b)||jsonb_build_object('amount_minor',b.amount_minor::text)
 FROM meters.bills b WHERE household_id=$1 AND id=$2 AND version=$3 AND confirmed_at IS NOT NULL FOR UPDATE`,
		id.HouseholdID, in.ID, in.Version)
	if err != nil {
		return nil, energyError(err)
	}
	if entry := String(bill["expense_id"]); entry != "" {
		return Record{"id": in.ID, "version": bill["version"], "expense_id": entry, "already_transferred": true}, nil
	}
	category := String(data["category_id"])
	var exists bool
	err = tx.QueryRow(ctx, `SELECT true FROM expenses.categories WHERE household_id=$1 AND id=$2 AND archived_at IS NULL FOR SHARE NOWAIT`,
		id.HouseholdID, category).Scan(&exists)
	if err != nil || !exists {
		return nil, energyError(err)
	}
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil || scope.generation != in.TargetGenerations["expenses"] {
		if err != nil {
			return nil, err
		}
		return nil, ErrConflict
	}
	entryID := value.UUID()
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, "entries", entryID, "manual", []FinanceResourceRef{})
	if err != nil {
		return nil, err
	}
	module, _ := s.Module("expenses")
	entity, _ := module.Entity("entries")
	title := strings.TrimSpace("Energie: " + String(bill["title"]))
	entry, err := save(ctx, tx, id, module, entity, entryID, Record{"occurred_on": bill["occurred_on"], "description": title,
		"payee": "", "amount_minor": Int(bill["amount_minor"]), "kind": "expense"}, true, 0)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO expenses.entry_lines(household_id,entry_id,category_id,amount_minor,created_by)
 VALUES($1,$2,$3,$4,$5)`, id.HouseholdID, entryID, category, Int(bill["amount_minor"]), id.UserID)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE expenses.entries SET state='posted',posted_at=clock_timestamp(),updated_at=clock_timestamp(),version=version+1
 WHERE household_id=$1 AND id=$2 AND state='draft'`, id.HouseholdID, entryID)
	}
	if err == nil {
		err = s.afterWrite(ctx, tx, id, module, entity, entry, scope.generation)
	}
	if err == nil {
		err = completeFinanceOrigin(ctx, tx, id, scope, op)
	}
	if err != nil {
		return nil, err
	}
	result, err := energyObject(ctx, tx, `UPDATE meters.bills AS b SET expense_id=$4,expense_generation=$5,updated_at=clock_timestamp(),version=version+1
 WHERE household_id=$1 AND id=$2 AND version=$3 AND expense_id IS NULL
 RETURNING jsonb_build_object('id',b.id,'version',b.version,'expense_id',b.expense_id,'confirmed',true)`,
		id.HouseholdID, in.ID, in.Version, entryID, scope.generation)
	return result, err
}
