package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"household/internal/auth"
	"household/internal/finance"

	"github.com/jackc/pgx/v5"
)

type financeRelationKey struct{}
type financeRelation struct {
	OriginalID      string
	OriginalVersion int64
	Kind            string
	ReversalID      string
}

func financeEntryCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	if c.Action == "save" || c.Action == "post" {
		var in FinanceEntryInput
		if err := decodeFinancePayload(c.Payload, &in); err != nil {
			return nil, err
		}
		return financeWriteEntry(ctx, tx, id, scope, c.ID, c.Version, c.Action, in)
	}
	if c.Version != 0 {
		return nil, ErrConflict
	}
	if c.Action == "reverse" {
		var in struct {
			OriginalEntryID string `json:"original_entry_id"`
			OriginalVersion int64  `json:"original_version"`
			OccurredOn      string `json:"occurred_on"`
			Reason          string `json:"reason"`
		}
		if err := decodeFinancePayload(c.Payload, &in); err != nil {
			return nil, err
		}
		return financeReverseEntry(ctx, tx, id, scope, c.ID, in.OriginalEntryID, in.OriginalVersion, in.OccurredOn, in.Reason)
	}
	var in struct {
		OriginalEntryID string            `json:"original_entry_id"`
		OriginalVersion int64             `json:"original_version"`
		ReversalID      string            `json:"reversal_id,omitempty"`
		Entry           FinanceEntryInput `json:"entry"`
	}
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	original, err := financeOriginalForAction(ctx, tx, id, scope, in.OriginalEntryID, in.OriginalVersion)
	if err != nil {
		return nil, fmt.Errorf("review relation original: %w", err)
	}
	refs := financeActionSources(original, in.OriginalEntryID)
	_, priorReviewed := financeOriginFor(ctx, refs)
	relation := financeRelation{OriginalID: in.OriginalEntryID, OriginalVersion: in.OriginalVersion}
	switch c.Action {
	case "refund":
		if in.ReversalID != "" || in.Entry.Kind != "refund" || original["kind"] != "expense" {
			return nil, Invalid{"Erstattung mit ursprünglicher Ausgabe prüfen"}
		}
		var currency string
		var originalMinor string
		err = tx.QueryRow(ctx, `SELECT coalesce(f.original_currency,'EUR'),coalesce(f.original_minor,e.amount_minor)::text FROM expenses.entries e LEFT JOIN expenses.entry_finance f ON f.household_id=e.household_id AND f.entry_id=e.id WHERE e.household_id=$1 AND e.id=$2`, id.HouseholdID, in.OriginalEntryID).Scan(&currency, &originalMinor)
		if err != nil {
			return nil, err
		}
		amount, parseErr := finance.ParseMinor(in.Entry.Money.Minor)
		capacity, capacityErr := finance.ParseMinor(originalMinor)
		if parseErr != nil || capacityErr != nil || amount >= 0 || in.Entry.Money.Currency != currency {
			return nil, Invalid{"Erstattungsbetrag in der ursprünglichen Währung prüfen"}
		}
		var refunded string
		err = tx.QueryRow(ctx, `SELECT coalesce(sum(-coalesce(f.original_minor,e.amount_minor)),0)::text FROM expenses.entry_relations r JOIN expenses.entries e ON e.household_id=r.household_id AND e.id=r.entry_id LEFT JOIN expenses.entry_finance f ON f.household_id=e.household_id AND f.entry_id=e.id WHERE r.household_id=$1 AND r.original_entry_id=$2 AND r.relation='refund_of' AND e.state='posted' AND NOT EXISTS(SELECT 1 FROM expenses.entries reversal WHERE reversal.household_id=e.household_id AND reversal.reverses_entry_id=e.id AND reversal.state='posted')`, id.HouseholdID, in.OriginalEntryID).Scan(&refunded)
		if err != nil {
			return nil, err
		}
		used, parseErr := strconv.ParseInt(refunded, 10, 64)
		if parseErr != nil || used-amount > capacity {
			return nil, Invalid{"Erstattung überschreitet den noch offenen Originalbetrag"}
		}
		relation.Kind = "refund_of"
	case "correct":
		if in.ReversalID == "" || in.ReversalID == c.ID {
			return nil, Invalid{"Stabile eigene Stornokennung für die Korrektur erforderlich"}
		}
		if _, err = financeReverseEntry(ctx, tx, id, scope, in.ReversalID, in.OriginalEntryID, in.OriginalVersion, in.Entry.OccurredOn, "Korrektur"); err != nil {
			return nil, fmt.Errorf("append correction reversal: %w", err)
		}
		refs = append(refs, FinanceResourceRef{Kind: "entries", ID: in.ReversalID})
		relation.Kind, relation.ReversalID = "correction_of", in.ReversalID
	default:
		return nil, ErrNotFound
	}
	origin := c.Action
	if origin == "correct" {
		origin = "correction"
	}
	ctx = withFinanceOrigin(ctx, origin, refs)
	ctx = context.WithValue(ctx, financeReviewedSourcesKey{}, priorReviewed)
	ctx = context.WithValue(ctx, financeRelationKey{}, relation)
	result, err := financeWriteEntry(ctx, tx, id, scope, c.ID, 0, "post", in.Entry)
	if err != nil {
		return nil, fmt.Errorf("append relation replacement: %w", err)
	}
	impact := result["impact"].(Record)
	impact["relation"] = Record{"original_entry_id": in.OriginalEntryID, "original_version": in.OriginalVersion, "kind": relation.Kind, "reversal_id": relation.ReversalID}
	return result, nil
}

func financeOriginalForAction(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, rid string, version int64) (Record, error) {
	if _, err := financeResourceAccess(ctx, tx, id, scope, "entries", rid, "edit"); err != nil {
		return nil, err
	}
	refs, err := financeRelatedActionSources(ctx, tx, id, scope, rid)
	if err != nil {
		return nil, err
	}
	if _, err = financeProspectiveAccess(ctx, tx, id, scope, refs, "edit"); err != nil {
		return nil, err
	}
	var account *string
	if err := tx.QueryRow(ctx, `SELECT account_id FROM expenses.entry_finance WHERE household_id=$1 AND entry_id=$2`, id.HouseholdID, rid).Scan(&account); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if account != nil {
		if err := financeLockAccounts(ctx, tx, id, []string{*account}); err != nil {
			return nil, err
		}
	}
	record, err := financeLedgerRecord(ctx, tx, id.HouseholdID, "entries", rid, true)
	if err != nil {
		return nil, err
	}
	if record["state"] != "posted" || Int(record["version"]) != version {
		return nil, ErrConflict
	}
	var reversed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses.entries WHERE household_id=$1 AND reverses_entry_id=$2)`, id.HouseholdID, rid).Scan(&reversed); err != nil {
		return nil, err
	}
	if reversed {
		return nil, Invalid{"Die Buchung wurde bereits storniert"}
	}
	record["_action_sources"] = refs
	return record, nil
}

func financeActionSources(original Record, rid string) []FinanceResourceRef {
	if refs, ok := original["_action_sources"].([]FinanceResourceRef); ok {
		return refs
	}
	return []FinanceResourceRef{{Kind: "entries", ID: rid}}
}

func financeRelatedActionSources(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, rid string) ([]FinanceResourceRef, error) {
	predicate, auth, err := financeResourcePredicate(scope, "entries", "edit", "related", 3)
	if err != nil {
		return nil, err
	}
	const selected = `WITH direct AS(SELECT e.id FROM expenses.entries e WHERE e.household_id=$1 AND(e.reverses_entry_id=$2 OR EXISTS(SELECT 1 FROM expenses.entry_relations r WHERE r.household_id=e.household_id AND r.entry_id=e.id AND r.original_entry_id=$2))),affected AS(SELECT id FROM direct UNION SELECT e.id FROM expenses.entries e WHERE e.household_id=$1 AND e.reverses_entry_id IN(SELECT id FROM direct)) `
	const from = `FROM expenses.entries related WHERE related.household_id=$1 AND related.state='posted' AND related.id IN(SELECT id FROM affected)`
	var denied bool
	var count int64
	if err = tx.QueryRow(ctx, selected+`SELECT count(*),coalesce(bool_or((`+predicate+`) IS NOT TRUE),false) `+from, append([]any{id.HouseholdID, rid}, auth...)...).Scan(&count, &denied); err != nil {
		return nil, err
	}
	if denied {
		return nil, ErrForbidden
	}
	if count > 1000 {
		return nil, Invalid{"Vollständige Korrektur- und Erstattungsgrundlage überschreitet die Quellgrenze"}
	}
	rows, err := tx.Query(ctx, selected+`SELECT related.id `+from+` ORDER BY related.id LIMIT 1001`, id.HouseholdID, rid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []FinanceResourceRef{{Kind: "entries", ID: rid}}
	for rows.Next() {
		var source string
		if err = rows.Scan(&source); err != nil {
			return nil, err
		}
		refs = append(refs, FinanceResourceRef{Kind: "entries", ID: source})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(refs) > 1001 {
		return nil, Invalid{"Vollständige Korrekturgrundlage hat sich geändert"}
	}
	return refs, nil
}

func financeSaveRelation(ctx context.Context, tx pgx.Tx, id auth.Identity, entryID string) error {
	relation, ok := ctx.Value(financeRelationKey{}).(financeRelation)
	if !ok {
		return nil
	}
	var reversal any
	if relation.ReversalID != "" {
		reversal = relation.ReversalID
	}
	_, err := tx.Exec(ctx, `INSERT INTO expenses.entry_relations(id,household_id,entry_id,original_entry_id,relation,reviewed_original_version,reversal_entry_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, finance.StableID(entryID, "relation/"+relation.Kind), id.HouseholdID, entryID, relation.OriginalID, relation.Kind, relation.OriginalVersion, reversal, id.UserID)
	return err
}

func financeReverseEntry(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, newID, originalID string, version int64, on, reason string) (Record, error) {
	if !finance.CivilDate(on) || !financeText(reason, 1, 200) {
		return nil, Invalid{"Stornodatum und Grund erforderlich"}
	}
	original, err := financeOriginalForAction(ctx, tx, id, scope, originalID, version)
	if err != nil {
		return nil, err
	}
	lines, err := financeEntryLines(ctx, tx, id.HouseholdID, originalID, true)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 || len(lines) > 100 {
		return nil, Invalid{"Vollständige begrenzte Originalaufteilung erforderlich"}
	}
	if err = financeLockDebts(ctx, tx, id.HouseholdID); err != nil {
		return nil, err
	}
	ctx = withFinanceOrigin(ctx, "reversal", financeActionSources(original, originalID))
	origin, sources := financeOriginFor(ctx, nil)
	sourceStamp, err := financeSourceReviewStamp(ctx, tx, id, scope, nil)
	if err != nil {
		return nil, err
	}
	op, err := prepareFinanceOrigin(ctx, tx, id, scope, "entries", newID, origin, sources)
	if err != nil {
		return nil, err
	}
	description := []rune("Storno: " + reason + " · " + String(original["description"]))
	if len(description) > 300 {
		description = description[:300]
	}
	_, err = tx.Exec(ctx, `INSERT INTO expenses.entries(id,household_id,occurred_on,payee,description,amount_minor,kind,paid_by_membership_id,money_mode,reverses_entry_id,created_by) SELECT $1,household_id,$3,payee,$4,-amount_minor,'reversal',paid_by_membership_id,money_mode,id,$5 FROM expenses.entries WHERE household_id=$2 AND id=$6`, newID, id.HouseholdID, on, string(description), id.UserID, originalID)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO expenses.entry_finance(id,household_id,entry_id,original_state,original_currency,original_exponent,original_minor,fx_numerator,fx_denominator,fx_effective_on,fx_source,fx_rounding,fx_reviewed_by,fx_reviewed_at,account_id,payment_currency,payment_exponent,payment_minor,payment_fx_numerator,payment_fx_denominator,payment_fx_on,payment_fx_source,payment_base_minor,payment_difference_reviewed,created_by) SELECT $1,e.household_id,$1,'known',coalesce(f.original_currency,'EUR'),coalesce(f.original_exponent,2),-coalesce(f.original_minor,e.amount_minor),f.fx_numerator,f.fx_denominator,f.fx_effective_on,f.fx_source,coalesce(f.fx_rounding,'half_away_from_zero_v1'),f.fx_reviewed_by,f.fx_reviewed_at,f.account_id,f.payment_currency,f.payment_exponent,-f.payment_minor,f.payment_fx_numerator,f.payment_fx_denominator,f.payment_fx_on,f.payment_fx_source,-f.payment_base_minor,coalesce(f.payment_difference_reviewed,false),$4 FROM expenses.entries e LEFT JOIN expenses.entry_finance f ON f.household_id=e.household_id AND f.entry_id=e.id WHERE e.household_id=$2 AND e.id=$3`, newID, id.HouseholdID, originalID, id.UserID)
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		lineID := finance.StableID(newID, "reverse/"+String(line["id"]))
		base, parseErr := finance.ParseMinor(String(line["base_minor"]))
		if parseErr != nil {
			return nil, parseErr
		}
		originalMinor, parseErr := finance.ParseMinor(String(line["original_minor"]))
		if parseErr != nil {
			return nil, parseErr
		}
		_, err = tx.Exec(ctx, `INSERT INTO expenses.entry_lines(id,household_id,entry_id,category_id,amount_minor,note,position,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, lineID, id.HouseholdID, newID, line["category_id"], -base, line["note"], line["position"], id.UserID)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO expenses.entry_original_lines(id,household_id,line_id,entry_id,original_minor,created_by) VALUES($1,$2,$1,$3,$4,$5)`, lineID, id.HouseholdID, newID, -originalMinor, id.UserID)
		if err != nil {
			return nil, err
		}
	}
	if err = financeReverseShares(ctx, tx, id, originalID, newID); err != nil {
		return nil, err
	}
	if err = financeReverseCash(ctx, tx, id, originalID, newID, on, string(description)); err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, financeRelationKey{}, financeRelation{OriginalID: originalID, OriginalVersion: version, Kind: "reversal_of"})
	if err = financeSaveRelation(ctx, tx, id, newID); err != nil {
		return nil, err
	}
	var savedVersion int64
	if err = tx.QueryRow(ctx, `UPDATE expenses.entries SET state='posted',posted_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2 RETURNING version`, id.HouseholdID, newID).Scan(&savedVersion); err != nil {
		return nil, err
	}
	if err = financeProjectLedger(ctx, tx, id, scope, "entries", newID); err != nil {
		return nil, err
	}
	if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
		return nil, err
	}
	return financeLedgerResult("entries", newID, savedVersion, Record{"relation": "reversal_of", "source_access_version": sourceStamp, "original_entry_id": originalID, "original_version": version, "date": on, "reason": reason, "original_lines": lines, "original_base_minor": original["amount_minor"]}), nil
}

func financeReverseCash(ctx context.Context, tx pgx.Tx, id auth.Identity, originalID, newID, on, description string) error {
	eventID := finance.StableID(newID, "cash")
	_, err := tx.Exec(ctx, `INSERT INTO expenses.cash_events(id,household_id,event_kind,occurred_on,description,entry_id,original_currency,original_exponent,original_minor,base_minor,fx_numerator,fx_denominator,fx_effective_on,fx_source,reverses_event_id,account_id,created_by) SELECT $1,e.household_id,'reversal',$4,$5,$3,coalesce(f.original_currency,'EUR'),coalesce(f.original_exponent,2),-coalesce(f.original_minor,e.amount_minor),-e.amount_minor,f.fx_numerator,f.fx_denominator,f.fx_effective_on,f.fx_source,c.id,f.account_id,$6 FROM expenses.entries e LEFT JOIN expenses.entry_finance f ON f.household_id=e.household_id AND f.entry_id=e.id LEFT JOIN expenses.cash_events c ON c.household_id=e.household_id AND c.entry_id=e.id WHERE e.household_id=$2 AND e.id=$7`, eventID, id.HouseholdID, newID, on, description, id.UserID, originalID)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT m.id FROM expenses.account_movements m JOIN expenses.cash_events c ON c.household_id=m.household_id AND c.id=m.event_id WHERE c.household_id=$1 AND c.entry_id=$2 ORDER BY m.leg LIMIT 3`, id.HouseholdID, originalID)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var rid string
		if err = rows.Scan(&rid); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, rid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, rid := range ids {
		_, err = tx.Exec(ctx, `INSERT INTO expenses.account_movements(id,household_id,event_id,account_id,leg,currency_code,currency_exponent,amount_minor,base_minor,fx_numerator,fx_denominator,fx_effective_on,fx_source,created_by) SELECT $1,household_id,$2,account_id,leg,currency_code,currency_exponent,-amount_minor,-base_minor,fx_numerator,fx_denominator,fx_effective_on,fx_source,$3 FROM expenses.account_movements WHERE household_id=$4 AND id=$5`, finance.StableID(newID, "reverse-movement/"+rid), eventID, id.UserID, id.HouseholdID, rid)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE expenses.cash_events SET state='posted',posted_at=clock_timestamp() WHERE household_id=$1 AND id=$2`, id.HouseholdID, eventID)
	return err
}

func financeReverseShares(ctx context.Context, tx pgx.Tx, id auth.Identity, originalID, newID string) error {
	var oldSet string
	err := tx.QueryRow(ctx, `SELECT id FROM expenses.entry_split_sets WHERE household_id=$1 AND entry_id=$2 AND active`, id.HouseholdID, originalID).Scan(&oldSet)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	newSet := finance.StableID(newID, "split")
	_, err = tx.Exec(ctx, `INSERT INTO expenses.entry_split_sets(id,household_id,entry_id,method,currency_code,currency_exponent,total_minor,payer_membership_id,basis,reviewed_by,created_by) SELECT $1,s.household_id,$2,'fixed',s.currency_code,s.currency_exponent,-s.total_minor,s.payer_membership_id,(SELECT jsonb_agg(jsonb_build_object('membership_id',p.membership_id,'basis',(-p.share_minor)::text) ORDER BY p.position,p.id) FROM expenses.entry_person_shares p WHERE p.household_id=s.household_id AND p.split_set_id=s.id),$3,$3 FROM expenses.entry_split_sets s WHERE s.household_id=$4 AND s.id=$5`, newSet, newID, id.UserID, id.HouseholdID, oldSet)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM expenses.entry_person_shares WHERE household_id=$1 AND split_set_id=$2 ORDER BY id LIMIT 51`, id.HouseholdID, oldSet)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var rid string
		if err = rows.Scan(&rid); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, rid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ids) > 50 {
		return Invalid{"Originalaufteilung überschreitet Personengrenze"}
	}
	for _, rid := range ids {
		_, err = tx.Exec(ctx, `INSERT INTO expenses.entry_person_shares(id,household_id,split_set_id,entry_id,membership_id,share_minor,base_minor,position,created_by) SELECT $1,household_id,$2,$3,membership_id,-share_minor,-base_minor,position,$4 FROM expenses.entry_person_shares WHERE household_id=$5 AND id=$6`, finance.StableID(newID, "reverse-share/"+rid), newSet, newID, id.UserID, id.HouseholdID, rid)
		if err != nil {
			return err
		}
	}
	return nil
}
