package store

import (
	"context"
	"fmt"
	"household/internal/auth"

	"github.com/jackc/pgx/v5"
)

func scheduleReminder(ctx context.Context, tx pgx.Tx, id auth.Identity, mid, entity string, r Record, generation int64) error {
	if mid == "fuel" && entity == "vehicles" && r["archived_at"] != nil && String(r["archived_at"]) != "" {
		_, err := tx.Exec(ctx, `UPDATE core.reminders SET state='cancelled',cancelled_at=clock_timestamp() WHERE household_id=$1
 AND module_id='fuel' AND resource_type='vehicles' AND resource_id=$2 AND state='pending'`, id.HouseholdID, r["id"])
		return err
	}
	date, kind, recipient := "", "", ""
	days := int64(0)
	switch mid + "/" + entity {
	case "documents/documents":
		date = String(r["valid_until"])
		kind = "document_expiry"
		if String(r["warranty_until"]) != "" && (date == "" || String(r["warranty_until"]) < date) {
			date = String(r["warranty_until"])
		}
		days = Int(r["remind_days"])
	case "pantry/lots":
		date = String(r["use_by_on"])
		kind = "stock_expiry"
		if date == "" {
			date = String(r["best_before_on"])
		}
	case "chores/task_occurrences":
		date = String(r["due_at"])
		kind = "task_due"
		var managed bool
		if err := tx.QueryRow(ctx, `SELECT profile.managed FROM chores.task_occurrences occurrence
 JOIN chores.series_profiles profile ON profile.household_id=occurrence.household_id AND profile.template_id=occurrence.template_id
 WHERE occurrence.household_id=$1 AND occurrence.id=$2`, id.HouseholdID, r["id"]).Scan(&managed); err != nil {
			return err
		}
		if managed {
			date = ""
		}
		if String(r["state"]) != "open" {
			date = ""
		}
		if membership := String(r["assignee_id"]); membership != "" {
			if err := tx.QueryRow(ctx, "SELECT user_id FROM core.memberships WHERE household_id=$1 AND id=$2", id.HouseholdID, membership).Scan(&recipient); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	if _, err := tx.Exec(ctx, "UPDATE core.reminders SET state='cancelled',cancelled_at=now() WHERE household_id=$1 AND module_id=$2 AND resource_type=$3 AND resource_id=$4 AND state='pending'", id.HouseholdID, mid, entity, r["id"]); err != nil {
		return err
	}
	if date == "" {
		return nil
	}
	if days < 0 || days > 3660 {
		return Invalid{"Erinnerungstage müssen zwischen 0 und 3660 liegen"}
	}
	key := fmt.Sprintf("%s/%s/%v/%s/%s/%d", mid, entity, r["id"], kind, date, days)
	expression := "($7::date::timestamp+interval '9 hours'-make_interval(days=>$8)) AT TIME ZONE h.timezone"
	if mid == "chores" {
		expression = "$7::timestamptz-make_interval(days=>$8)"
	}
	_, err := tx.Exec(ctx, `INSERT INTO core.reminders(household_id,module_id,module_generation,resource_type,resource_id,recipient_user_id,kind,scheduled_at,timezone,dedupe_key)
 SELECT h.id,$2,$3,$4,$5,nullif($6,'')::uuid,$9,`+expression+`,h.timezone,$10 FROM core.households h WHERE h.id=$1
 ON CONFLICT(dedupe_key) DO UPDATE SET scheduled_at=excluded.scheduled_at,recipient_user_id=excluded.recipient_user_id,state=CASE WHEN core.reminders.state='cancelled' THEN 'pending' ELSE core.reminders.state END,cancelled_at=NULL`, id.HouseholdID, mid, generation, entity, r["id"], recipient, date, int(days), kind, key)
	return err
}
