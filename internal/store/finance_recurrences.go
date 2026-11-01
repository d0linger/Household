package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const FinanceRecurrenceJobKind = "finance_recurrence"

func (s *Store) financeRecurrenceCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, c FinanceCommand) (Record, error) {
	if c.Action == "generate" {
		var in FinanceGenerateInput
		if err := decodeFinancePayload(c.Payload, &in); err != nil {
			return nil, err
		}
		if !finance.CivilDate(in.Through) {
			return nil, Invalid{"Gültigen letzten Fälligkeitstag auswählen"}
		}
		template, err := financeSupportRecord(ctx, tx, id, scope, "recurring_templates", c.ID, true)
		if err != nil {
			return nil, err
		}
		if Int(template["version"]) != c.Version {
			return nil, ErrConflict
		}
		result, err := s.financeGenerateRecurrence(ctx, tx, id, scope, template, in.Through)
		if err != nil {
			return nil, err
		}
		if result["resource_type"] != "recurring_occurrences" {
			return nil, Invalid{"Bis zum gewählten Tag sind keine weiteren Entwürfe fällig"}
		}
		return result, nil
	}
	if c.Action == "pause" {
		if err := decodeFinancePayload(c.Payload, &struct{}{}); err != nil {
			return nil, err
		}
		if err := setFinanceAutomation(ctx, tx, id, scope, c.ID, false); err != nil {
			return nil, err
		}
		return financeArchiveSupport(ctx, tx, id, scope, c, "recurring_templates", "active", "false")
	}
	var in FinanceRecurrenceInput
	if err := decodeFinancePayload(c.Payload, &in); err != nil {
		return nil, err
	}
	if in.TemplateKind == "" {
		in.TemplateKind = "expense"
	}
	if !financeText(in.Name, 1, 100) || !finance.CivilDate(in.AnchorOn) || in.EndsOn != nil && (!finance.CivilDate(*in.EndsOn) || *in.EndsOn < in.AnchorOn) ||
		!slices.Contains([]string{"day", "week", "month", "year"}, in.RepeatUnit) || in.RepeatInterval < 1 || in.RepeatInterval > 365 ||
		!slices.Contains([]string{"clamp", "skip"}, in.MonthdayPolicy) || len(in.Template) > 65536 || !slices.Contains([]string{"expense", "income"}, in.TemplateKind) {
		return nil, Invalid{"Vorlage, feste Kalenderregel und begrenzten Entwurf prüfen"}
	}
	refs, err := financeValidateRecurrenceTemplate(ctx, tx, id, scope, in)
	if err != nil {
		return nil, err
	}
	if c.Version > 0 {
		old, readErr := financeSupportRecord(ctx, tx, id, scope, "recurring_templates", c.ID, true)
		if readErr != nil {
			return nil, readErr
		}
		var materialized bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expenses.recurring_occurrences WHERE household_id=$1 AND template_id=$2)`, id.HouseholdID, c.ID).Scan(&materialized); err != nil {
			return nil, err
		}
		if materialized && (String(old["anchor_on"]) != in.AnchorOn || String(old["repeat_unit"]) != in.RepeatUnit || Int(old["repeat_interval"]) != int64(in.RepeatInterval) || String(old["monthday_policy"]) != in.MonthdayPolicy) {
			return nil, Invalid{"Für einen geänderten Kalender eine neue Vorlage anlegen; bereits erzeugte Fälligkeiten bleiben unverändert"}
		}
	}
	if c.Version == 0 && in.Automatic && (c.InitialAccess == nil || c.InitialAccess.ResponsibleMembershipID == nil) {
		return nil, Invalid{"Für automatische Entwürfe zuerst ausdrücklich eine verantwortliche schreibfähige Person zuordnen"}
	}
	result, err := financeSaveSupport(ctx, tx, id, scope, c, "recurring_templates",
		[]string{"name", "anchor_on", "ends_on", "repeat_unit", "repeat_interval", "monthday_policy", "template_kind", "template", "account_id", "active"},
		[]any{in.Name, in.AnchorOn, in.EndsOn, in.RepeatUnit, in.RepeatInterval, in.MonthdayPolicy, in.TemplateKind, []byte(in.Template), in.AccountID, in.Active}, refs, in)
	if err != nil {
		return nil, err
	}
	if err = setFinanceAutomation(ctx, tx, id, scope, c.ID, in.Automatic && in.Active); err != nil {
		return nil, err
	}
	return result, nil
}

func financeValidateRecurrenceTemplate(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, in FinanceRecurrenceInput) ([]FinanceResourceRef, error) {
	refs := []FinanceResourceRef{}
	var payment *FinancePaymentInput
	if in.TemplateKind == "income" {
		var draft FinanceIncomeInput
		if err := decodeFinancePayload(in.Template, &draft); err != nil {
			return nil, err
		}
		money, err := finance.NormalizeMoney(draft.Money, false, true)
		if err != nil || money.Minor <= 0 || !financeText(draft.Description, 1, 300) || !financeText(draft.Payee, 0, 200) || draft.State != "" && draft.State != "draft" {
			return nil, Invalid{"Wiederkehrende Einnahme muss ein positiver prüfbarer Entwurf sein"}
		}
		payment = draft.Payment
	} else {
		var draft FinanceEntryInput
		if err := decodeFinancePayload(in.Template, &draft); err != nil {
			return nil, err
		}
		money, err := finance.NormalizeMoney(draft.Money, false, true)
		if err != nil || draft.Kind != "expense" && draft.Kind != "refund" || draft.Kind == "expense" && money.Minor <= 0 || draft.Kind == "refund" && money.Minor >= 0 ||
			!financeText(draft.Description, 1, 300) || !financeText(draft.Payee, 0, 200) || len(draft.Lines) > 100 || len(draft.Baseline) != 0 || len(draft.Removed) != 0 {
			return nil, Invalid{"Vorlage muss einen neuen vollständigen begrenzten Buchungsentwurf enthalten"}
		}
		seen := map[string]bool{}
		for _, line := range draft.Lines {
			amount, amountErr := finance.ParseMinor(line.OriginalMinor)
			if amountErr != nil || amount == 0 || !value.ValidUUID(line.ID) || seen[line.ID] || line.Version != 0 || !financeText(line.Note, 0, 300) {
				return nil, Invalid{"Vorlagenzeilen brauchen eindeutige neue Identitäten und exakte Beträge"}
			}
			seen[line.ID] = true
			if err = financeCategory(ctx, tx, id.HouseholdID, line.CategoryID); err != nil {
				return nil, err
			}
		}
		if draft.PaidByMembershipID != nil {
			if err = financeMemberSelection(ctx, tx, id, *draft.PaidByMembershipID, false); err != nil {
				return nil, err
			}
		}
		if draft.Split != nil {
			if _, err = finance.Split(*draft.Split, money.Minor); err != nil {
				return nil, err
			}
			for _, person := range draft.Split.Participants {
				if err = financeMemberSelection(ctx, tx, id, person.MembershipID, false); err != nil {
					return nil, err
				}
			}
		}
		payment = draft.Payment
	}
	if in.AccountID != nil && (payment == nil || *in.AccountID != payment.AccountID) {
		return nil, Invalid{"Vorlagenkonto muss der ausdrücklich erfassten tatsächlichen Zahlung entsprechen"}
	}
	if payment != nil {
		if _, _, err := financeAccountForPayment(ctx, tx, id, scope, payment, true); err != nil {
			return nil, err
		}
		refs = append(refs, FinanceResourceRef{Kind: "accounts", ID: payment.AccountID})
	}
	return refs, nil
}

// financeNextRecurrence always derives from the original anchor, so clamping
// 31January to February never turns March's schedule into the28th.
func financeNextRecurrence(anchor, after, through, unit string, interval int, policy string) (string, error) {
	start, err := time.Parse(time.DateOnly, anchor)
	if err != nil || !finance.CivilDate(anchor) || !finance.CivilDate(through) || interval < 1 || interval > 365 ||
		!slices.Contains([]string{"day", "week", "month", "year"}, unit) || !slices.Contains([]string{"clamp", "skip"}, policy) {
		return "", Invalid{"Gespeicherte Kalenderregel ist ungültig"}
	}
	for index := 0; index < 120000; index++ {
		var date time.Time
		switch unit {
		case "day":
			date = start.AddDate(0, 0, index*interval)
		case "week":
			date = start.AddDate(0, 0, index*interval*7)
		case "month", "year":
			months := index * interval
			if unit == "year" {
				months *= 12
			}
			first := time.Date(start.Year(), start.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
			day := start.Day()
			last := first.AddDate(0, 1, -1).Day()
			if day > last {
				if policy == "skip" {
					if first.Format(time.DateOnly) > through {
						return "", nil
					}
					continue
				}
				day = last
			}
			date = first.AddDate(0, 0, day-1)
		}
		if date.Year() > 2200 || date.Format(time.DateOnly) > through {
			return "", nil
		}
		civil := date.Format(time.DateOnly)
		if after == "" || civil > after {
			return civil, nil
		}
	}
	return "", Invalid{"Kalenderregel überschreitet die begrenzte Zeitspanne"}
}

func financeRecurrenceState(ctx context.Context, tx pgx.Tx, household, template string) (string, error) {
	var last string
	err := tx.QueryRow(ctx, `SELECT coalesce(max(scheduled_on)::text,'') FROM expenses.recurring_occurrences WHERE household_id=$1 AND template_id=$2`, household, template).Scan(&last)
	return last, err
}

func financeRecurrenceValues(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, row Record) error {
	info, err := financeAutomationInfo(ctx, tx, id, scope, String(row["id"]))
	if err != nil {
		return err
	}
	row["automatic"], row["automation_reason"] = info["automatic"], info["reason"]
	last, err := financeRecurrenceState(ctx, tx, id.HouseholdID, String(row["id"]))
	if err != nil {
		return err
	}
	next, err := financeNextRecurrence(String(row["anchor_on"]), last, "2200-12-31", String(row["repeat_unit"]), int(Int(row["repeat_interval"])), String(row["monthday_policy"]))
	if err != nil {
		return err
	}
	if row["ends_on"] != nil && next > String(row["ends_on"]) {
		next = ""
	}
	row["next_due"] = nil
	if next != "" {
		row["next_due"] = next
	}
	return nil
}

func (s *Store) financeGenerateRecurrence(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, row Record, through string) (Record, error) {
	active, _ := row["active"].(bool)
	if !active {
		return nil, Invalid{"Vorlage ist pausiert"}
	}
	if row["ends_on"] != nil && through > String(row["ends_on"]) {
		through = String(row["ends_on"])
	}
	templateID := String(row["id"])
	last, err := financeRecurrenceState(ctx, tx, id.HouseholdID, templateID)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(row["template"])
	if err != nil {
		return nil, err
	}
	hash := financeValueHash(row["template"])
	created := []Record{}
	proofID := ""
	var proofVersion int64
	for range 12 {
		on, nextErr := financeNextRecurrence(String(row["anchor_on"]), last, through, String(row["repeat_unit"]), int(Int(row["repeat_interval"])), String(row["monthday_policy"]))
		if nextErr != nil {
			return nil, nextErr
		}
		if on == "" {
			break
		}
		occurrence := finance.StableID(templateID, "occurrence/"+on)
		target := finance.StableID(occurrence, "draft")
		kind := "entries"
		if row["template_kind"] == "income" {
			kind = "cash_events"
		}
		sources := []FinanceResourceRef{{Kind: "recurring_templates", ID: templateID}}
		if row["account_id"] != nil {
			sources = append(sources, FinanceResourceRef{Kind: "accounts", ID: String(row["account_id"])})
		}
		op, prepareErr := prepareFinanceOrigin(ctx, tx, id, scope, "recurring_occurrences", occurrence, "recurrence", sources)
		if prepareErr != nil {
			return nil, prepareErr
		}
		fingerprint := hex.EncodeToString(financeValueHash([]any{"recurrence-v1", templateID, on}))
		if err = financeClaimIngest(ctx, tx, id, scope, "recurrence", fingerprint, kind, target); err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO expenses.recurring_occurrences(id,household_id,template_id,scheduled_on,template_version,template_hash,state,created_by)
 VALUES($1,$2,$3,$4,$5,$6,'proposed',$7)`, occurrence, id.HouseholdID, templateID, on, Int(row["version"]), hash, id.UserID)
		if err != nil {
			return nil, err
		}
		producerCtx := withFinanceOrigin(ctx, "recurrence", sources)
		if kind == "entries" {
			var draft FinanceEntryInput
			if err = decodeFinancePayload(raw, &draft); err != nil {
				return nil, err
			}
			draft.OccurredOn = on
			draft.Baseline = nil
			draft.Removed = nil
			for index := range draft.Lines {
				draft.Lines[index].ID = finance.StableID(target, "line/"+strconv.Itoa(index))
				draft.Lines[index].Version = 0
			}
			if draft.Payment != nil {
				var version int64
				if err = tx.QueryRow(ctx, `SELECT version FROM expenses.accounts WHERE household_id=$1 AND id=$2`, id.HouseholdID, draft.Payment.AccountID).Scan(&version); err != nil {
					return nil, err
				}
				draft.Payment.AccountVersion = version
			}
			_, err = financeWriteEntry(producerCtx, tx, id, scope, target, 0, "save", draft)
		} else {
			var draft FinanceIncomeInput
			if err = decodeFinancePayload(raw, &draft); err != nil {
				return nil, err
			}
			draft.OccurredOn = on
			draft.State = "draft"
			if draft.Payment != nil {
				var version int64
				if err = tx.QueryRow(ctx, `SELECT version FROM expenses.accounts WHERE household_id=$1 AND id=$2`, id.HouseholdID, draft.Payment.AccountID).Scan(&version); err != nil {
					return nil, err
				}
				draft.Payment.AccountVersion = version
			}
			_, err = financeWriteIncome(producerCtx, tx, id, scope, target, 0, draft)
		}
		if err != nil {
			return nil, err
		}
		column := "entry_id"
		if kind == "cash_events" {
			column = "event_id"
		}
		var occurrenceVersion int64
		if err = tx.QueryRow(ctx, "UPDATE expenses.recurring_occurrences SET "+column+"=$3,state='draft_created' WHERE household_id=$1 AND id=$2 RETURNING version", id.HouseholdID, occurrence, target).Scan(&occurrenceVersion); err != nil {
			return nil, err
		}
		if err = financeProjectSupport(ctx, tx, id, scope, "recurring_occurrences", Record{"id": occurrence, "version": occurrenceVersion}); err != nil {
			return nil, err
		}
		if err = completeFinanceOrigin(ctx, tx, id, scope, op); err != nil {
			return nil, err
		}
		if proofID == "" {
			proofID, proofVersion = occurrence, occurrenceVersion
		}
		created = append(created, Record{"id": target, "scheduled_on": on, "resource_type": kind})
		last = on
	}
	next, err := financeNextRecurrence(String(row["anchor_on"]), last, through, String(row["repeat_unit"]), int(Int(row["repeat_interval"])), String(row["monthday_policy"]))
	if err != nil {
		return nil, err
	}
	result := Record{"id": templateID, "resource_type": "recurring_templates", "version": Int(row["version"]), "impact": Record{"action": "recurrences.generate", "template_id": templateID, "template_version": row["version"], "through": through, "drafts": created, "backlog": next != "", "posted": false}}
	if proofID != "" {
		result["id"], result["resource_type"], result["version"] = proofID, "recurring_occurrences", proofVersion
	}
	return result, nil
}

func (s *Store) ScheduleFinanceRecurrences(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := audit.Exec(audit.WithSystem(ctx, "worker"), s.Pool, "SELECT core.finance_schedule_recurrences()")
	return err
}

func (s *Store) CompleteFinanceRecurrenceJob(ctx context.Context, job, lease string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ctx = audit.WithSystem(ctx, "worker")
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var template string
	err = tx.QueryRow(ctx, `SELECT payload->>'template_id' FROM core.jobs WHERE id=$1 AND lease_token=$2 AND state='running' AND lease_until>now() AND kind=$3`, job, lease, FinanceRecurrenceJobKind).Scan(&template)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	id, scope, err := financeAutomationScope(ctx, tx, job, lease, template)
	if err != nil {
		return err
	}
	ctx = audit.WithHousehold(ctx, id.HouseholdID)
	if err = audit.Bind(ctx, tx); err != nil {
		return err
	}
	row, err := financeSupportRecord(ctx, tx, id, scope, "recurring_templates", template, true)
	if err != nil {
		return err
	}
	zone, err := time.LoadLocation(scope.timezone)
	if err != nil {
		return err
	}
	through := scope.readAt.In(zone).Format(time.DateOnly)
	result, err := s.financeGenerateRecurrence(ctx, tx, id, scope, row, through)
	if err != nil {
		return err
	}
	impact, _ := result["impact"].(Record)
	backlog, _ := impact["backlog"].(bool)
	state := "done"
	if backlog {
		state = "pending"
	}
	tag, err := tx.Exec(ctx, `UPDATE core.jobs SET state=$3,finished_at=CASE WHEN $3='done' THEN now() ELSE NULL END,
 available_at=now()+interval '30 seconds',lease_token=NULL,lease_until=NULL,attempts=0
 WHERE id=$1 AND lease_token=$2 AND state='running' AND lease_until>now()`, job, lease, state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
