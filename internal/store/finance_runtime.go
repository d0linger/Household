package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/finance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

const financePurpose = "expense-finance-v1"

func financeResourceKind(resource string) string {
	switch resource {
	case "cash":
		return "cash_events"
	case "filters":
		return "saved_filters"
	case "budgets":
		return "category_budgets"
	case "goals":
		return "savings_goals"
	case "contributions":
		return "goal_contributions"
	case "recurrences":
		return "recurring_templates"
	case "rules":
		return "categorization_rules"
	case "receipts":
		return "receipt_reviews"
	case "accounts", "entries", "bank_profiles", "reconciliations":
		return resource
	default:
		return ""
	}
}

// FinanceView holds one current-authorized snapshot for rows and full totals.
func (s *Store) FinanceView(ctx context.Context, id auth.Identity, query FinanceQuery) (Record, error) {
	if err := validateFinanceQuery(query); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	stamp, err := financeProspectiveAccess(ctx, tx, id, scope, nil, "view")
	if err != nil {
		return nil, err
	}
	zone, err := time.LoadLocation(scope.timezone)
	if err != nil {
		return nil, err
	}
	today := scope.readAt.In(zone).Format(time.DateOnly)
	if query.Month == "" {
		query.Month = today[:7]
	}
	var result Record
	if query.Pane == "accounts" || query.Pane == "people" || query.Pane == "entries" || query.Pane == "reports" && query.Section == "ledger" {
		result, err = financeLedgerRead(ctx, tx, id, scope, query)
	} else {
		result, err = s.financeSupportRead(ctx, tx, id, scope, query)
	}
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("finance view returned no projection")
	}
	refs, err := financeReferences(ctx, tx, id, scope)
	if err != nil {
		return nil, err
	}
	result["pane"], result["section"] = query.Pane, query.Section
	result["generation"], result["access_version"] = scope.generation, stamp
	result["read_at"], result["today"], result["timezone"] = scope.readAt, today, scope.timezone
	result["base_currency"], result["can_write"], result["references"] = scope.currency, id.CanWrite(), refs
	result["month"] = query.Month
	result["capabilities"] = Record{"write": id.CanWrite(), "save_personal_filter": true, "offline_finance": false}
	for _, name := range []string{"rows", "totals", "warnings"} {
		if result[name] == nil {
			result[name] = []Record{}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func financeReferences(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope) (Record, error) {
	refs := Record{"accounts": []Record{}, "members": []Record{}, "categories": []Record{}, "currencies": finance.Currencies()}
	accounts, err := financeLedgerRead(ctx, tx, id, scope, FinanceQuery{Pane: "accounts"})
	if err != nil {
		return nil, err
	}
	refs["accounts"] = accounts["rows"]
	more := Record{"accounts": String(accounts["next_cursor"]) != "", "members": false, "categories": false}
	for _, kind := range []string{"members", "categories"} {
		query := `SELECT m.id,u.display_name,m.inactive_at IS NULL AND u.disabled_at IS NULL
 FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=$1 ORDER BY u.display_name,m.id LIMIT 51`
		if kind == "categories" {
			query = `SELECT id,name,archived_at IS NULL FROM expenses.categories WHERE household_id=$1 ORDER BY sort_order,name,id LIMIT 51`
		}
		rows, queryErr := tx.Query(ctx, query, id.HouseholdID)
		if queryErr != nil {
			return nil, queryErr
		}
		items := []Record{}
		for rows.Next() {
			var rid, label string
			var active bool
			if err = rows.Scan(&rid, &label, &active); err != nil {
				rows.Close()
				return nil, err
			}
			items = append(items, Record{"id": rid, "label": label, "active": active})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(items) > 50 {
			more[kind] = true
			items = items[:50]
		}
		refs[kind] = items
	}
	refs["more"] = more
	refs["search_url"] = "/api/v1/expenses/finance/references/"
	return refs, nil
}

// FinancePreview exercises the complete writer in a transaction that is always
// rolled back, including deferred integrity guards. It never schedules external
// work or persists a financial draft, proof, audit row or source claim.
func (s *Store) FinancePreview(ctx context.Context, id auth.Identity, key, requestID string, command FinanceCommand) (Record, error) {
	return s.financeTransaction(ctx, id, key, requestID, command, true)
}

// FinanceApply accepts only a reviewed impact hash supplied by the authenticated
// HTTP token verifier; current authority and domain invariants are still checked.
func (s *Store) FinanceApply(ctx context.Context, id auth.Identity, key, requestID string, command FinanceCommand) (Record, error) {
	if command.Resource == "rules" && command.Action == "explain" {
		return nil, Invalid{"Die Regelerklärung ist eine schreibfreie Vorschau; sie erzeugt keine Buchung"}
	}
	if len(command.ReviewHash) != 64 {
		return nil, Invalid{"Finanzvorgang zuerst vollständig prüfen"}
	}
	return s.financeTransaction(ctx, id, key, requestID, command, false)
}

func FinanceCommandHash(command FinanceCommand) (string, error) {
	command.ReviewToken, command.ReviewHash = "", ""
	var checked json.RawMessage
	if err := decodeFinancePayload(command.Payload, &checked); err != nil {
		return "", err
	}
	var payload any
	d := json.NewDecoder(bytes.NewReader(command.Payload))
	d.UseNumber()
	if err := d.Decode(&payload); err != nil {
		return "", err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return "", Invalid{"Finanzvorgang enthält zusätzliche JSON-Daten"}
	}
	var err error
	command.Payload, err = json.Marshal(payload)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Purpose string         `json:"purpose"`
		Command FinanceCommand `json:"command"`
	}{financePurpose, command})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func (s *Store) financeBegin(ctx context.Context, id auth.Identity, command FinanceCommand) (pgx.Tx, error) {
	if command.Resource != "filters" && (command.Resource != "rules" || command.Action != "explain") {
		return s.BeginMutation(ctx, id, "expenses", command.Generation)
	}
	// Personal preferences are available to a current Reader. This exception does
	// not grant edit authority on any financial fact or referenced account.
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", id.HouseholdID); err == nil {
		err = checkSession(ctx, tx, id)
	}
	var generation int64
	if err == nil {
		err = tx.QueryRow(ctx, `SELECT data_generation FROM core.module_states
 WHERE household_id=$1 AND module_id='expenses' AND state='enabled' FOR SHARE`, id.HouseholdID).Scan(&generation)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	} else if err == nil && generation != command.Generation {
		err = ErrConflict
	}
	if err != nil {
		rollback(tx)
		return nil, err
	}
	return tx, nil
}

func (s *Store) financeTransaction(ctx context.Context, id auth.Identity, key, requestID string, command FinanceCommand, preview bool) (Record, error) {
	if err := validateFinanceCommand(command); err != nil {
		return nil, err
	}
	if !value.ValidUUID(key) || !value.ValidUUID(requestID) {
		return nil, Invalid{"Gültige Vorgangskennung erforderlich"}
	}
	var checkedPayload json.RawMessage
	if err := decodeFinancePayload(command.Payload, &checkedPayload); err != nil {
		return nil, err
	}
	encodedHash, err := FinanceCommandHash(command)
	if err != nil {
		return nil, err
	}
	hash, err := hex.DecodeString(encodedHash)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.financeBegin(ctx, id, command)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "finance-key-v1/"+id.HouseholdID+id.UserID+key); err != nil {
		return nil, err
	}
	prior, priorHash, err := financeReadOperation(ctx, tx, id, scope, key)
	if err == nil {
		if !bytes.Equal(hash, priorHash) {
			return nil, ErrConflict
		}
		prior["replayed"] = true
		if preview {
			return Record{"command": command, "impact": Record{"already_accepted": true}, "impact_hash": financeImpactHash(Record{"already_accepted": true})}, nil
		}
		return prior, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	stamp, err := financeProspectiveAccess(ctx, tx, id, scope, nil, "view")
	if command.Version > 0 && command.Resource != "access" {
		intent := "edit"
		if command.Resource == "rules" && command.Action == "explain" {
			intent = "view"
		}
		if command.Resource == "filters" {
			intent = "preference"
		}
		stamp, err = financeResourceAccess(ctx, tx, id, scope, financeResourceKind(command.Resource), command.ID, intent)
	}
	if err != nil {
		return nil, err
	}
	if command.Resource != "access" && stamp != command.AccessVersion {
		return nil, ErrConflict
	}
	var result Record
	if command.InitialAccess != nil {
		ctx = withFinanceInitialAccess(ctx, financeResourceKind(command.Resource), command.ID, *command.InitialAccess)
	}
	switch command.Resource {
	case "accounts", "cash", "entries", "reconciliations":
		result, err = financeLedgerCommand(ctx, tx, id, scope, command)
	case "access":
		result, err = financeAccessCommand(ctx, tx, id, scope, command)
	default:
		result, err = s.financeSupportCommand(ctx, tx, id, scope, command)
	}
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	if result == nil || result["impact"] == nil {
		return nil, errors.New("finance command returned no reviewed impact")
	}
	// Execute deferred guards before accepting a preview; a rollback alone would
	// otherwise omit constraints that PostgreSQL normally checks only at commit.
	if _, err = tx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
		return nil, financeAuthorityError(err)
	}
	impactHash := financeImpactHash(result["impact"])
	if preview {
		command.ReviewToken, command.ReviewHash = "", ""
		return Record{"command": command, "impact": result["impact"], "impact_hash": impactHash, "read_only": result["read_only"] == true}, nil
	}
	if impactHash != command.ReviewHash {
		return nil, ErrConflict
	}
	kind, target := financeResourceKind(command.Resource), command.ID
	if resultKind := String(result["resource_type"]); resultKind != "" {
		kind = resultKind
	}
	if resultID := String(result["id"]); resultID != "" {
		target = resultID
	}
	if kind == "" || !value.ValidUUID(target) || Int(result["version"]) <= 0 {
		return nil, errors.New("finance command returned invalid proof identity")
	}
	operationID := value.UUID()
	var acceptedAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO core.finance_operations
 (id,household_id,created_by,membership_id,request_key,purpose,operation,request_hash,expenses_generation,resource_type,resource_id,result_version)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING created_at`, operationID, id.HouseholdID, id.UserID,
		id.MembershipID, key, financePurpose, command.Resource+"."+command.Action, hash, scope.generation, kind, target, Int(result["version"])).Scan(&acceptedAt)
	if err != nil {
		return nil, financeAuthorityError(err)
	}
	exists := command.Resource != "filters" || command.Action != "delete"
	receipt := Record{"id": operationID, "request_key": key, "generation": scope.generation, "resource": kind,
		"action": command.Resource + "." + command.Action, "resource_id": target, "saved_version": Int(result["version"]),
		"accepted_at": acceptedAt, "replayed": false, "current_exists": exists, "current_version": Int(result["version"]), "current_state": String(result["state"])}
	if command.Resource == "access" && command.Action == "recover" {
		receipt["metadata_only"] = true
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, financeAuthorityError(err)
	}
	return receipt, nil
}

func financeImpactHash(impact any) string {
	raw, err := json.Marshal(impact)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func financeReadOperation(ctx context.Context, tx pgx.Tx, id auth.Identity, scope financialScope, key string) (Record, []byte, error) {
	var opID, purpose, operation, kind, resource string
	var hash []byte
	var generation, savedVersion int64
	var accepted time.Time
	err := tx.QueryRow(ctx, `SELECT id,purpose,operation,request_hash,expenses_generation,resource_type,resource_id,result_version,created_at
 FROM core.finance_operations WHERE household_id=$1 AND created_by=$2 AND request_key=$3`, id.HouseholdID, id.UserID, key).
		Scan(&opID, &purpose, &operation, &hash, &generation, &kind, &resource, &savedVersion, &accepted)
	if err != nil {
		return nil, nil, err
	}
	if generation != scope.generation || purpose != financePurpose {
		return nil, nil, ErrNotFound
	}
	if operation == "access.recover" {
		metadata, metadataErr := financeAccessInfoTx(ctx, tx, id, scope, kind, resource)
		if metadataErr != nil {
			return nil, nil, metadataErr
		}
		return Record{"id": opID, "request_key": key, "generation": generation, "resource": kind,
			"action": operation, "resource_id": resource, "saved_version": savedVersion, "accepted_at": accepted,
			"replayed": false, "metadata_only": true, "current_exists": true, "current_version": metadata["policy_version"], "current_state": ""}, hash, nil
	}
	if _, err = financeResourceAccess(ctx, tx, id, scope, kind, resource, "view"); err != nil {
		return nil, nil, err
	}
	var version int64
	var state string
	// security: kind is still checked against the compiled domain allowlist.
	if !financeKnownKind(kind) {
		return nil, nil, ErrNotFound
	}
	schema, table := "expenses", kind
	if kind == "import_batch" {
		schema, table = "core", "import_batches"
	}
	if !financeDomainKind(kind) && kind != "import_batch" {
		return nil, nil, ErrNotFound
	}
	err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT version,coalesce(to_jsonb(t)->>'state','') FROM %s t WHERE household_id=$1 AND id=$2`,
		pgx.Identifier{schema, table}.Sanitize()), id.HouseholdID, resource).Scan(&version, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return Record{"id": opID, "request_key": key, "generation": generation, "resource": kind,
		"action": operation, "resource_id": resource, "saved_version": savedVersion, "accepted_at": accepted,
		"replayed": false, "current_exists": true, "current_version": version, "current_state": state}, hash, nil
}

func financeKnownKind(kind string) bool {
	return financeDomainKind(kind) || kind == "attachment" || kind == "attachment_derivative" ||
		kind == "capture" || kind == "import_batch" || kind == "import_row" || kind == "audit"
}

func financeDomainKind(kind string) bool {
	switch kind {
	case "entries", "entry_lines", "accounts", "entry_finance", "entry_original_lines", "cash_events", "account_movements", "entry_relations",
		"entry_split_sets", "entry_person_shares", "settlement_details", "saved_filters", "category_budgets", "savings_goals", "goal_contributions",
		"recurring_templates", "recurring_occurrences", "categorization_rules", "bank_profiles", "reconciliations", "reconciliation_items", "receipt_reviews", "receipt_review_lines":
		return true
	default:
		return false
	}
}

func (s *Store) FinanceOperation(ctx context.Context, id auth.Identity, key string) (Record, error) {
	return s.financeOperation(ctx, id, key, nil)
}

// FinanceReplay can only read an already accepted, currently authorized proof.
// It never executes a fresh command when the authentic review has expired.
func (s *Store) FinanceReplay(ctx context.Context, id auth.Identity, key string, command FinanceCommand) (Record, error) {
	if err := validateFinanceCommand(command); err != nil {
		return nil, err
	}
	hash, err := FinanceCommandHash(command)
	if err != nil {
		return nil, err
	}
	bytes, err := hex.DecodeString(hash)
	if err != nil {
		return nil, err
	}
	return s.financeOperation(ctx, id, key, bytes)
}

func (s *Store) financeOperation(ctx context.Context, id auth.Identity, key string, expectedHash []byte) (Record, error) {
	if !value.ValidUUID(key) {
		return nil, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	scope, err := readFinancialScope(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	receipt, actualHash, err := financeReadOperation(ctx, tx, id, scope, key)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if expectedHash != nil {
		if !bytes.Equal(expectedHash, actualHash) {
			return nil, ErrConflict
		}
		receipt["replayed"] = true
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return receipt, nil
}
