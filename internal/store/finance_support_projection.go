package store

import (
	"context"
	"fmt"
	"strconv"

	"household/internal/auth"
	"household/internal/finance"

	"github.com/jackc/pgx/v5"
)

func financeProjectSupport(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, kind string, row Record) error {
	if kind == "saved_filters" || kind == "bank_profiles" {
		return nil
	}
	rid := String(row["id"])
	version := Int(row["version"])
	if kind == "category_budgets" || kind == "savings_goals" || kind == "recurring_templates" || kind == "categorization_rules" {
		title := String(row["name"])
		if !financeText(title, 1, 100) {
			return Invalid{"Finanzsuchtext ist ungültig"}
		}
		_, err := tx.Exec(ctx, `INSERT INTO core.search_documents(household_id,module_id,module_generation,resource_type,resource_id,title,body)
 VALUES($1,'expenses',$2,$3,$4,$5,'') ON CONFLICT(household_id,module_id,resource_type,resource_id)
 DO UPDATE SET title=excluded.title,module_generation=excluded.module_generation,updated_at=clock_timestamp()`, id.HouseholdID, scope.generation, kind, rid, title)
		if err != nil {
			return err
		}
	}
	event := finance.StableID(rid, "record-changed/"+kind+"/"+strconv.FormatInt(version, 10))
	payload, err := financeRaw(Record{"entity": kind, "id": rid})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.outbox_events(id,household_id,module_id,module_generation,event_type,event_version,actor_user_id,payload,dedupe_key)
 VALUES($1,$2,'expenses',$3,'record_changed',1,CASE WHEN current_setting('household.audit_kind',true)='worker' THEN NULL ELSE $4::uuid END,$5,$6)`,
		event, id.HouseholdID, scope.generation, id.UserID, payload, fmt.Sprintf("finance/%s/%s/%d", kind, rid, version))
	return err
}
