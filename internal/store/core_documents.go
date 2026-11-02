package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// Cross-module document integrations use this Core boundary exclusively.
type documentLinkInput struct {
	Module     string `json:"module"`
	Entity     string `json:"entity"`
	TargetID   string `json:"target_id"`
	Remove     bool   `json:"remove"`
	CreateTask bool   `json:"create_task"`
	DeadlineID string `json:"deadline_id"`
}

func documentCoreTarget(ctx context.Context, tx pgx.Tx, id auth.Identity, mid, entity, rid string, generation int64, lock bool) (string, error) {
	if !value.ValidUUID(rid) || generation < 1 {
		return "", ErrNotFound
	}
	var active bool
	stateQuery := `SELECT true FROM core.module_states WHERE household_id=$1 AND module_id=$2 AND state='enabled' AND data_generation=$3`
	if lock {
		stateQuery += " FOR SHARE"
	}
	err := tx.QueryRow(ctx, stateQuery, id.HouseholdID, mid, generation).Scan(&active)
	if err != nil {
		return "", energyError(err)
	}
	var query string
	switch mid + "/" + entity {
	case "fuel/vehicles":
		query = `SELECT name FROM fuel.vehicles WHERE household_id=$1 AND id=$2 AND archived_at IS NULL`
	case "meters/meters":
		query = `SELECT name FROM meters.meters WHERE household_id=$1 AND id=$2`
	case "meters/devices":
		query = `SELECT serial_number FROM meters.devices WHERE household_id=$1 AND id=$2`
	case "chores/task_occurrences":
		query = `SELECT t.title FROM chores.task_occurrences o JOIN chores.task_templates t ON t.id=o.template_id WHERE o.household_id=$1 AND o.id=$2`
	case "expenses/entries":
		if lock {
			err = lockFinancialReplay(ctx, tx, id, "entries", rid, financialView)
		} else {
			var scope financialScope
			scope, err = readFinancialScope(ctx, tx, id)
			if err == nil {
				_, err = readFinancialAccessVersion(ctx, tx, scope, rid)
			}
		}
		if err != nil {
			return "", err
		}
		query = `SELECT description FROM expenses.entries WHERE household_id=$1 AND id=$2`
	default:
		return "", ErrNotFound
	}
	if lock {
		query += " FOR SHARE NOWAIT"
	}
	var title string
	err = tx.QueryRow(ctx, query, id.HouseholdID, rid).Scan(&title)
	return title, energyError(err)
}

func (s *Store) documentAuthorizeLink(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand) error {
	var v documentLinkInput
	if err := documentDecode(in.Data, &v); err != nil {
		return err
	}
	gen := in.TargetGenerations[v.Module]
	if len(in.TargetGenerations) != 1 || gen < 1 {
		return Invalid{"Aktuellen Zielmodulstand bestätigen"}
	}
	if v.CreateTask {
		if v.Module != "chores" || v.Entity != "task_occurrences" || v.Remove || !value.ValidUUID(v.DeadlineID) {
			return Invalid{"Aufgabe ausdrücklich für eine bestätigte Frist anlegen"}
		}
		current, err := requireModule(ctx, tx, id, "chores")
		if err != nil {
			return err
		}
		if current != gen {
			return ErrConflict
		}
		return nil
	}
	_, err := documentCoreTarget(ctx, tx, id, v.Module, v.Entity, v.TargetID, gen, true)
	return err
}

func (s *Store) documentCoreSaveLink(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand) (Record, error) {
	var v documentLinkInput
	if err := documentDecode(in.Data, &v); err != nil {
		return nil, err
	}
	gen := in.TargetGenerations[v.Module]
	if err := s.documentAuthorizeLink(ctx, tx, id, in); err != nil {
		return nil, err
	}
	relation := "document/" + strconv.FormatInt(gen, 10)
	if v.CreateTask {
		relation += "/deadline/" + v.DeadlineID
		// One task per confirmed deadline and target generation, even with a new command key.
		var old string
		err := tx.QueryRow(ctx, `SELECT target_id FROM core.resource_links WHERE household_id=$1 AND source_module='documents' AND source_type='documents' AND source_id=$2 AND relation=$3`, id.HouseholdID, in.ID, relation).Scan(&old)
		if err == nil {
			_, err = documentCoreTarget(ctx, tx, id, "chores", "task_occurrences", old, gen, true)
			return Record{"id": in.ID, "target_id": old}, err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		var label, day, timezone string
		err = tx.QueryRow(ctx, `SELECT x.label,x.due_on::text,h.timezone FROM documents.deadlines x JOIN core.households h ON h.id=x.household_id WHERE x.household_id=$1 AND x.document_id=$2 AND x.id=$3 AND x.cancelled_at IS NULL FOR SHARE OF x`, id.HouseholdID, in.ID, v.DeadlineID).Scan(&label, &day, &timezone)
		if err != nil {
			return nil, energyError(err)
		}
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			return nil, err
		}
		date, err := time.ParseInLocation(time.DateOnly, day, loc)
		if err != nil {
			return nil, err
		}
		due := time.Date(date.Year(), date.Month(), date.Day(), 9, 0, 0, 0, loc)
		tid, oid := value.UUID(), value.UUID()
		_, err = tx.Exec(ctx, `INSERT INTO chores.task_templates(id,household_id,title,description,timezone,active,created_by) VALUES($1,$2,$3,$4,$5,true,$6)`, tid, id.HouseholdID, label, "Dokumentfrist: /m/documents/workspace/review?id="+in.ID, timezone, id.UserID)
		if err != nil {
			return nil, err
		}
		var raw []byte
		err = tx.QueryRow(ctx, `INSERT INTO chores.task_occurrences AS t(id,household_id,template_id,occurrence_key,due_at,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING to_jsonb(t)`, oid, id.HouseholdID, tid, "document/"+v.DeadlineID, due, id.UserID).Scan(&raw)
		if err != nil {
			return nil, err
		}
		occurrence, err := decode(raw)
		if err != nil {
			return nil, err
		}
		m, _ := s.Module("chores")
		e, _ := m.Entity("task_occurrences")
		if err = s.afterWrite(ctx, tx, id, m, e, occurrence, gen); err != nil {
			return nil, err
		}
		v.TargetID = oid
	}
	if v.Remove {
		_, err := tx.Exec(ctx, `DELETE FROM core.resource_links WHERE household_id=$1 AND source_module='documents' AND source_id=$2 AND target_module=$3 AND target_type=$4 AND target_id=$5`, id.HouseholdID, in.ID, v.Module, v.Entity, v.TargetID)
		return Record{"id": in.ID}, err
	}
	_, err := tx.Exec(ctx, `INSERT INTO core.resource_links(household_id,source_module,source_type,source_id,target_module,target_type,target_id,relation) VALUES($1,'documents','documents',$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, id.HouseholdID, in.ID, v.Module, v.Entity, v.TargetID, relation)
	return Record{"id": in.ID, "target_id": v.TargetID}, err
}

func (s *Store) documentCoreLinks(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string) ([]Record, error) {
	rows, err := energyRows(ctx, tx, 100, `SELECT jsonb_build_object('id',id,'module',target_module,'entity',target_type,'target_id',target_id,'relation',relation) FROM core.resource_links WHERE household_id=$1 AND source_module='documents' AND source_type='documents' AND source_id=$2 ORDER BY id LIMIT 101`, id.HouseholdID, rid)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		var generation int64
		if _, err = fmt.Sscanf(String(r["relation"]), "document/%d", &generation); err != nil {
			return nil, Invalid{"Gespeicherte Dokumentverknüpfung prüfen"}
		}
		// Missing or forbidden targets expose no cached title or private snapshot.
		title, targetErr := documentCoreTarget(ctx, tx, id, String(r["module"]), String(r["entity"]), String(r["target_id"]), generation, false)
		if targetErr != nil {
			if !errors.Is(targetErr, ErrNotFound) && !errors.Is(targetErr, ErrForbidden) {
				return nil, targetErr
			}
			r["available"] = false
			r["title"] = "Verknüpfung derzeit nicht verfügbar"
			delete(r, "target_id")
			delete(r, "relation")
			continue
		}
		r["available"] = true
		r["title"] = title
		r["generation"] = generation
		r["path"] = "/m/" + String(r["module"]) + "/" + String(r["entity"]) + "/" + String(r["target_id"])
	}
	return rows, nil
}

func (s *Store) DocumentTargets(ctx context.Context, id auth.Identity, mid, entity, search string) (Record, error) {
	if len(search) > 200 {
		return nil, Invalid{"Zielsuche begrenzen"}
	}
	q, err := s.documentRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer q.close()
	var generation int64
	err = q.tx.QueryRow(q.ctx, `SELECT data_generation FROM core.module_states WHERE household_id=$1 AND module_id=$2 AND state='enabled'`, id.HouseholdID, mid).Scan(&generation)
	if err != nil {
		return nil, energyError(err)
	}
	var query string
	args := []any{id.HouseholdID, search}
	switch mid + "/" + entity {
	case "fuel/vehicles":
		query = `SELECT jsonb_build_object('id',id,'title',name) FROM fuel.vehicles WHERE household_id=$1 AND archived_at IS NULL AND ($2='' OR name ILIKE '%'||$2||'%') ORDER BY name,id LIMIT 51`
	case "meters/meters":
		query = `SELECT jsonb_build_object('id',id,'title',name) FROM meters.meters WHERE household_id=$1 AND ($2='' OR name ILIKE '%'||$2||'%') ORDER BY name,id LIMIT 51`
	case "meters/devices":
		query = `SELECT jsonb_build_object('id',id,'title',serial_number) FROM meters.devices WHERE household_id=$1 AND ($2='' OR serial_number ILIKE '%'||$2||'%') ORDER BY serial_number,id LIMIT 51`
	case "chores/task_occurrences":
		query = `SELECT jsonb_build_object('id',o.id,'title',t.title) FROM chores.task_occurrences o JOIN chores.task_templates t ON t.id=o.template_id WHERE o.household_id=$1 AND o.state='open' AND ($2='' OR t.title ILIKE '%'||$2||'%') ORDER BY o.due_at,o.id LIMIT 51`
	case "expenses/entries":
		scope, e := readFinancialScope(q.ctx, q.tx, id)
		if e != nil {
			return nil, e
		}
		predicate, params, e := financialPredicate(scope, "entries", financialView, 3)
		if e != nil {
			return nil, e
		}
		args = append(args, params...)
		query = `SELECT jsonb_build_object('id',t.id,'title',t.description) FROM expenses.entries t WHERE t.household_id=$1 AND ($2='' OR t.description ILIKE '%'||$2||'%') AND ` + predicate + ` ORDER BY t.occurred_on DESC,t.id LIMIT 51`
	default:
		return nil, ErrNotFound
	}
	rows, err := energyRows(q.ctx, q.tx, 51, query, args...)
	if err != nil {
		return nil, err
	}
	more := len(rows) > 50
	if more {
		rows = rows[:50]
	}
	return Record{"items": rows, "generation": generation, "more": more}, nil
}
