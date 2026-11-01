package store

import (
	"context"
	"fmt"
	"strconv"

	"household/internal/auth"
	"household/internal/finance"

	"github.com/jackc/pgx/v5"
)

func financeProjectLedger(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, kind, rid string) error {
	var selectTitle, selectBody string
	switch kind {
	case "entries":
		selectTitle, selectBody = "description", "description||' '||payee"
	case "accounts":
		selectTitle, selectBody = "name", "notes"
	case "cash_events":
		selectTitle, selectBody = "description", "description||' '||payee"
	case "reconciliations":
		selectTitle, selectBody = "'Kontoabstimmung · '||statement_on::text", "''"
	default:
		return ErrNotFound
	}
	var title, body string
	var version int64
	query := "SELECT " + selectTitle + "," + selectBody + ",version FROM " + pgx.Identifier{"expenses", kind}.Sanitize() + " WHERE household_id=$1 AND id=$2"
	if err := tx.QueryRow(ctx, query, id.HouseholdID, rid).Scan(&title, &body, &version); err != nil {
		return err
	}
	if len(title)+len(body) > 1<<20 {
		return Invalid{"Finanzsuchtext überschreitet die sichere Größe"}
	}
	_, err := tx.Exec(ctx, `INSERT INTO core.search_documents(household_id,module_id,module_generation,resource_type,resource_id,title,body) VALUES($1,'expenses',$2,$3,$4,$5,$6) ON CONFLICT(household_id,module_id,resource_type,resource_id) DO UPDATE SET module_generation=excluded.module_generation,title=excluded.title,body=excluded.body,updated_at=clock_timestamp()`, id.HouseholdID, scope.generation, kind, rid, title, body)
	if err != nil {
		return err
	}
	if kind == "entries" {
		var bytes int64
		if err = tx.QueryRow(ctx, `SELECT coalesce(sum(octet_length(note)),0) FROM expenses.entry_lines WHERE household_id=$1 AND entry_id=$2`, id.HouseholdID, rid).Scan(&bytes); err != nil {
			return err
		}
		if bytes > 1<<20 {
			return Invalid{"Kategorie-Suchtext überschreitet die vollständige sichere Größe"}
		}
		_, err = tx.Exec(ctx, `INSERT INTO core.search_documents(household_id,module_id,module_generation,resource_type,resource_id,title,body) SELECT household_id,'expenses',$2,'entry_lines',id,CASE WHEN note='' THEN 'Kategorieaufteilung' ELSE note END,note FROM expenses.entry_lines WHERE household_id=$1 AND entry_id=$3 ON CONFLICT(household_id,module_id,resource_type,resource_id) DO UPDATE SET module_generation=excluded.module_generation,title=excluded.title,body=excluded.body,updated_at=clock_timestamp()`, id.HouseholdID, scope.generation, rid)
		if err != nil {
			return err
		}
	}
	event := finance.StableID(rid, "record-changed/"+kind+"/"+strconv.FormatInt(version, 10))
	payload, err := financeRaw(Record{"entity": kind, "id": rid})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.outbox_events(id,household_id,module_id,module_generation,event_type,event_version,actor_user_id,payload,dedupe_key) VALUES($1,$2,'expenses',$3,'record_changed',1,CASE WHEN current_setting('household.audit_kind',true)='worker' THEN NULL ELSE $4::uuid END,$5,$6)`, event, id.HouseholdID, scope.generation, id.UserID, payload, fmt.Sprintf("finance/%s/%s/%d", kind, rid, version))
	return err
}
