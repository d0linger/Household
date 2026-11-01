// Package store executes compiled, allowlisted SQL against normalized module tables.
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/foodfacts"
	"household/internal/module"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("nicht gefunden")
var ErrConflict = errors.New("inzwischen geändert; bitte neu laden und prüfen")
var ErrForbidden = errors.New("keine Berechtigung")

type Invalid struct{ Message string }

func (e Invalid) Error() string { return e.Message }

type State struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Generation int64  `json:"generation"`
	Version    int64  `json:"version"`
}
type Record = map[string]any
type Store struct {
	FuelOCREnabled bool
	FoodFacts      foodfacts.Provider
	Pool           *pgxpool.Pool
	Modules        []module.Descriptor
	snapshot       atomic.Value
	Gate           sync.RWMutex
}
type Mutation struct {
	Data              map[string]json.RawMessage `json:"data"`
	Version           int64                      `json:"version"`
	Generation        int64                      `json:"generation"`
	TargetGenerations map[string]int64           `json:"target_generations,omitempty"`
}

// LockResource serializes attachment metadata changes with resource deletion.
// Immutable records are protected from maintenance deletion by BeginMutation's household lock.
func (s *Store) LockResource(ctx context.Context, tx pgx.Tx, id auth.Identity, mid, entity, rid string) error {
	m, ok := s.Module(mid)
	e, exists := m.Entity(entity)
	if !ok || !exists || !value.ValidUUID(rid) {
		return ErrNotFound
	}
	if financialEntity(mid, entity) {
		if err := lockFinancialReplay(ctx, tx, id, entity, rid, financialEdit); err != nil {
			return err
		}
	}
	lock := " FOR KEY SHARE"
	if financialEntity(mid, entity) {
		lock += " NOWAIT"
	}
	if e.Immutable {
		lock = ""
	}
	var found string
	err := tx.QueryRow(ctx, "SELECT id FROM "+pgx.Identifier{mid, entity}.Sanitize()+" WHERE household_id=$1 AND id=$2"+lock, id.HouseholdID, rid).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func New(pool *pgxpool.Pool, modules []module.Descriptor) *Store {
	s := &Store{Pool: pool, Modules: modules}
	s.snapshot.Store(map[string]State{})
	return s
}
func (s *Store) Module(id string) (module.Descriptor, bool) {
	for _, m := range s.Modules {
		if m.ID == id {
			return m, true
		}
	}
	return module.Descriptor{}, false
}
func (s *Store) States() map[string]State {
	old := s.snapshot.Load().(map[string]State)
	out := make(map[string]State, len(old))
	for k, v := range old {
		out[k] = v
	}
	return out
}
func (s *Store) State(id string) (State, bool) {
	st, ok := s.snapshot.Load().(map[string]State)[id]
	return st, ok && st.State == "enabled"
}
func (s *Store) LoadStates(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, "SELECT module_id,state,data_generation,state_version FROM core.module_states")
	if err != nil {
		return err
	}
	defer rows.Close()
	all := map[string]State{}
	for rows.Next() {
		var st State
		if err = rows.Scan(&st.ID, &st.State, &st.Generation, &st.Version); err != nil {
			return err
		}
		all[st.ID] = st
	}
	if err = rows.Err(); err != nil {
		return err
	}
	s.snapshot.Store(all)
	return nil
}

func (s *Store) SetModule(ctx context.Context, id auth.Identity, mid string, enabled bool, version int64, requestID string) error {
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	if id.Role != "admin" {
		return ErrForbidden
	}
	if _, ok := s.Module(mid); !ok {
		return ErrNotFound
	}
	s.Gate.Lock()
	defer s.Gate.Unlock()
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR UPDATE", id.HouseholdID); err != nil {
		return err
	}
	if err = checkSession(ctx, tx, id); err != nil {
		return err
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	tag, err := tx.Exec(ctx, "UPDATE core.module_states SET state=$3,state_version=state_version+1,changed_at=now(),changed_by=$4 WHERE household_id=$1 AND module_id=$2 AND state_version=$5 AND state NOT IN('purging','error')", id.HouseholdID, mid, state, id.UserID, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, "UPDATE core.households SET module_revision=module_revision+1 WHERE id=$1", id.HouseholdID); err != nil {
		return err
	}
	if err = Audit(ctx, tx, id, mid, "module", "", "module_"+state, requestID); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return s.LoadStates(ctx)
}

func (s *Store) List(ctx context.Context, id auth.Identity, m module.Descriptor, e module.Entity, query string, limit, offset int) ([]Record, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 || offset > 100000 {
		offset = 0
	}
	if financialEntity(m.ID, e.ID) {
		return s.listFinancial(ctx, id, e, query, limit, offset)
	}
	table := pgx.Identifier{m.ID, e.ID}.Sanitize()
	primary := pgx.Identifier{e.Primary}.Sanitize()
	where := ""
	order := "created_at DESC,id DESC"
	if m.ID == "shopping" && e.ID == "lists" {
		where = " AND archived_at IS NULL AND NOT is_template"
		if query == "is:template" {
			where = " AND archived_at IS NULL AND is_template"
			query = ""
		}
		if query == "is:archived" {
			where = " AND archived_at IS NOT NULL"
			query = ""
		}
		order = "sort_order,name,id"
	}
	if m.ID == "shopping" && e.ID == "products" && query == "is:favorite" {
		where = " AND favorite"
		query = ""
	}
	if m.ID == "shopping" && e.ID == "items" {
		where = " AND EXISTS(SELECT 1 FROM shopping.lists l WHERE l.id=t.list_id AND l.household_id=t.household_id AND l.archived_at IS NULL)"
		order = "checked_at NULLS FIRST,group_name,sort_order,name,id"
	}
	if m.ID == "recipes" && e.ID == "recipes" && strings.HasPrefix(query, "#") {
		where = " AND EXISTS(SELECT 1 FROM recipes.recipe_tags rt JOIN recipes.tags tag ON tag.household_id=rt.household_id AND tag.id=rt.tag_id WHERE rt.household_id=t.household_id AND rt.recipe_id=t.id AND lower(tag.name)=lower($6))"
		tag := strings.TrimPrefix(query, "#")
		rows, err := s.Pool.Query(ctx, "SELECT "+pantryMovementRecordJSON(m.ID, e.ID)+" FROM "+table+" t WHERE household_id=$1 AND ($2='' OR "+primary+"::text ILIKE $3)"+where+" ORDER BY "+order+" LIMIT $4 OFFSET $5", id.HouseholdID, "", "", limit, offset, tag)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		list := []Record{}
		for rows.Next() {
			var raw []byte
			if err = rows.Scan(&raw); err != nil {
				return nil, err
			}
			record, err := decode(raw)
			if err != nil {
				return nil, err
			}
			list = append(list, record)
		}
		return list, rows.Err()
	}
	rows, err := s.Pool.Query(ctx, "SELECT "+pantryMovementRecordJSON(m.ID, e.ID)+" FROM "+table+" t WHERE household_id=$1 AND ($2='' OR "+primary+"::text ILIKE $3)"+where+" ORDER BY "+order+" LIMIT $4 OFFSET $5", id.HouseholdID, query, "%"+query+"%", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list %s.%s: %w", m.ID, e.ID, err)
	}
	defer rows.Close()
	list := []Record{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		record, err := decode(raw)
		if err != nil {
			return nil, err
		}
		list = append(list, record)
	}
	return list, rows.Err()
}

func (s *Store) Get(ctx context.Context, id auth.Identity, m module.Descriptor, e module.Entity, rid string) (Record, error) {
	if !value.ValidUUID(rid) {
		return nil, ErrNotFound
	}
	if financialEntity(m.ID, e.ID) {
		return s.getFinancial(ctx, id, e, rid)
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, "SELECT "+pantryMovementRecordJSON(m.ID, e.ID)+" FROM "+pgx.Identifier{m.ID, e.ID}.Sanitize()+" t WHERE household_id=$1 AND id=$2", id.HouseholdID, rid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(raw)
}

func (s *Store) BeginMutation(ctx context.Context, id auth.Identity, mid string, generation int64) (pgx.Tx, error) {
	ctx = audit.WithActor(ctx, id.UserID, id.HouseholdID)
	if !id.CanWrite() {
		return nil, ErrForbidden
	}
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM core.households WHERE id=$1 FOR SHARE", id.HouseholdID); err != nil {
		rollback(tx)
		return nil, err
	}
	if err = checkSession(ctx, tx, id); err != nil {
		rollback(tx)
		return nil, err
	}
	var state string
	var gen int64
	err = tx.QueryRow(ctx, "SELECT state,data_generation FROM core.module_states WHERE household_id=$1 AND module_id=$2 FOR SHARE", id.HouseholdID, mid).Scan(&state, &gen)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && state != "enabled" {
		rollback(tx)
		return nil, ErrNotFound
	}
	if err != nil {
		rollback(tx)
		return nil, err
	}
	if generation != gen {
		rollback(tx)
		return nil, ErrConflict
	}
	return tx, nil
}

func checkSession(ctx context.Context, tx pgx.Tx, id auth.Identity) error {
	err := auth.LockSession(ctx, tx, id)
	if errors.Is(err, auth.ErrSession) {
		return ErrForbidden
	}
	return err
}

func (s *Store) Write(ctx context.Context, id auth.Identity, m module.Descriptor, e module.Entity, rid, key, requestID string, in Mutation) (Record, error) {
	if m.ID == "fuel" {
		return s.fuelModuleLegacyWrite(ctx, id, e, rid, key, requestID, in)
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	creating := rid == ""
	if !creating && (!value.ValidUUID(rid) || e.Immutable) {
		return nil, ErrForbidden
	}
	data, err := e.Normalize(in.Data, !creating)
	if err != nil {
		return nil, Invalid{err.Error()}
	}
	tx, err := s.BeginMutation(ctx, id, m.ID, in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	op := "write/" + m.ID + "/" + e.ID + "/" + rid
	if !value.ValidUUID(key) {
		return nil, Invalid{"gültige Idempotenz-ID erforderlich"}
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.UserID+op+key); err != nil {
		return nil, err
	}
	var priorHash, prior []byte
	var durableFinance Record
	var durableFinanceFound bool
	financeProofOperation := "legacy." + op
	if financialEntity(m.ID, e.ID) {
		durableFinance, durableFinanceFound, err = financeLegacyReplay(ctx, tx, id, key, financeProofOperation, e.ID, rid, hash[:])
		if err != nil {
			return nil, err
		}
	}
	err = tx.QueryRow(ctx, "SELECT request_hash,response_payload FROM core.idempotency_records WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4", id.HouseholdID, id.UserID, op, key).Scan(&priorHash, &prior)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return nil, ErrConflict
		}
		result, decodeErr := decode(prior)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if financialEntity(m.ID, e.ID) {
			if !creating && !strings.EqualFold(String(result["id"]), rid) {
				return nil, ErrConflict
			}
			if err = lockFinancialReplay(ctx, tx, id, e.ID, String(result["id"]), financialEdit); err != nil {
				return nil, err
			}
		}
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if durableFinanceFound {
		return durableFinance, nil
	}
	if m.ID == "pantry" && e.ID == "stock_movements" && creating && String(data["reason"]) == "waste" {
		return nil, Invalid{"Abfall über Vorrat → Abfall buchen; bereits gebuchte Vorgänge im Verlauf prüfen"}
	}
	if financialEntity(m.ID, e.ID) {
		if err = financialWriteScope(ctx, tx, id, e.ID, rid, data, creating); err != nil {
			return nil, err
		}
	}
	current := Record{}
	if !creating {
		var raw []byte
		err = tx.QueryRow(ctx, "SELECT to_jsonb(t) FROM "+pgx.Identifier{m.ID, e.ID}.Sanitize()+" t WHERE household_id=$1 AND id=$2 FOR UPDATE", id.HouseholdID, rid).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		current, err = decode(raw)
		if err != nil {
			return nil, err
		}
		if Int(current["version"]) != in.Version {
			return nil, ErrConflict
		}
	}
	merged := Record{}
	for k, v := range current {
		merged[k] = v
	}
	for k, v := range data {
		merged[k] = v
	}
	if err = s.validateDomain(ctx, tx, id, m.ID, e.ID, rid, merged, data); err != nil {
		return nil, err
	}
	if creating {
		rid = value.UUID()
	}
	var financeOperation string
	var financeScope financialScope
	if financialEntity(m.ID, e.ID) {
		financeOperation, financeScope, err = prepareLegacyFinanceMutation(ctx, tx, id, e.ID, rid, merged)
		if err != nil {
			return nil, err
		}
	}
	result, err := save(ctx, tx, id, m, e, rid, data, creating, in.Version)
	if err != nil {
		return nil, err
	}
	if err = s.afterWrite(shoppingProducerBefore(ctx, m, e, current), tx, id, m, e, result, in.Generation); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, m.ID, e.ID, rid, "record_write", requestID); err != nil {
		return nil, err
	}
	if financeOperation != "" {
		if err = completeFinanceOrigin(ctx, tx, id, financeScope, financeOperation); err != nil {
			return nil, err
		}
		if err = financeStoreLegacyProof(ctx, tx, id, key, financeProofOperation, e.ID, rid, hash[:], in.Generation, Int(result["version"])); err != nil {
			return nil, err
		}
	}
	resultRaw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload) VALUES($1,$2,$3,$4,$5,$6,$7,200,$8)", id.HouseholdID, id.UserID, op, key, hash[:], m.ID, in.Generation, resultRaw)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, planningLeftoverMutationError(m.ID, err)
	}
	return result, nil
}

func save(ctx context.Context, tx pgx.Tx, id auth.Identity, m module.Descriptor, e module.Entity, rid string, data Record, creating bool, version int64) (Record, error) {
	columns, types, names := []string{}, []string{}, []string{}
	for _, f := range e.Fields {
		if _, exists := data[f.Name]; exists {
			quoted := pgx.Identifier{f.Name}.Sanitize()
			columns = append(columns, quoted)
			types = append(types, quoted+" "+f.SQLType())
			names = append(names, "x."+quoted)
		}
	}
	if len(columns) == 0 {
		return nil, Invalid{"mindestens ein Feld erforderlich"}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	table := pgx.Identifier{m.ID, e.ID}.Sanitize()
	var result []byte
	if creating {
		query := "INSERT INTO " + table + " AS t (id,household_id,created_by," + strings.Join(columns, ",") + ") SELECT $1::uuid,$2::uuid,$3::uuid," + strings.Join(names, ",") + " FROM jsonb_to_record($4::jsonb) AS x(" + strings.Join(types, ",") + ") RETURNING " + pantryMovementRecordJSON(m.ID, e.ID)
		err = tx.QueryRow(ctx, query, rid, id.HouseholdID, id.UserID, raw).Scan(&result)
	} else {
		query := "UPDATE " + table + " AS t SET (" + strings.Join(columns, ",") + ")=(SELECT " + strings.Join(names, ",") + " FROM jsonb_to_record($4::jsonb) AS x(" + strings.Join(types, ",") + ")),updated_at=now(),version=version+1 WHERE id=$1 AND household_id=$2 AND version=$3 RETURNING " + pantryMovementRecordJSON(m.ID, e.ID)
		err = tx.QueryRow(ctx, query, rid, id.HouseholdID, version, raw).Scan(&result)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, fmt.Errorf("save %s.%s: %w", m.ID, e.ID, err)
	}
	return decode(result)
}

func (s *Store) afterWrite(ctx context.Context, tx pgx.Tx, id auth.Identity, m module.Descriptor, e module.Entity, r Record, generation int64) error {
	title := String(r[e.Primary])
	if title == "" {
		title = e.Title
	}
	body := []string{}
	for _, f := range e.Fields {
		if f.Kind == "text" || f.Kind == "longtext" {
			body = append(body, String(r[f.Name]))
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO core.search_documents(household_id,module_id,module_generation,resource_type,resource_id,title,body) VALUES($1,$2,$3,$4,$5,$6,$7)
	 ON CONFLICT(household_id,module_id,resource_type,resource_id) DO UPDATE SET title=excluded.title,body=excluded.body,module_generation=excluded.module_generation,updated_at=now()`, id.HouseholdID, m.ID, generation, e.ID, r["id"], title, strings.Join(body, " "))
	if err != nil {
		return err
	}
	event := value.UUID()
	payload, eventVersion, err := shoppingEventPayload(ctx, m.ID, e.ID, r)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO core.outbox_events(id,household_id,module_id,module_generation,event_type,event_version,actor_user_id,payload,dedupe_key,created_at) VALUES($1::uuid,$2,$3,$4,'record_changed',$5,$6,$7,$1::uuid::text,CASE WHEN $3='shopping' THEN clock_timestamp() ELSE transaction_timestamp() END)", event, id.HouseholdID, m.ID, generation, eventVersion, id.UserID, payload)
	if err != nil {
		return err
	}
	return scheduleReminder(ctx, tx, id, m.ID, e.ID, r, generation)
}

func (s *Store) Delete(ctx context.Context, id auth.Identity, m module.Descriptor, e module.Entity, rid string, version, generation int64, requestID string) error {
	if m.ID == "fuel" {
		return s.fuelModuleLegacyDelete(ctx, id, e.ID, rid, version, generation, requestID)
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	if e.Immutable {
		return ErrForbidden
	}
	if !value.ValidUUID(rid) {
		return ErrNotFound
	}
	tx, err := s.BeginMutation(ctx, id, m.ID, generation)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var financeOperation string
	var financeScope financialScope
	if financialEntity(m.ID, e.ID) {
		if err = lockFinancialResource(ctx, tx, id, e.ID, rid); err != nil {
			return err
		}
		targetKind, targetID := e.ID, rid
		if e.ID == "entry_lines" {
			// The child disappears before its parent's revision trigger runs.
			// Reserve the locked stored parent so both effects retain one root.
			err = tx.QueryRow(ctx, `SELECT entry_id FROM expenses.entry_lines
 WHERE household_id=$1 AND id=$2 AND version=$3`, id.HouseholdID, rid, version).Scan(&targetID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
			targetKind = "entries"
		}
		financeOperation, financeScope, err = prepareLegacyFinanceMutation(ctx, tx, id, targetKind, targetID, nil)
		if err != nil {
			return err
		}
	}
	var shoppingBefore Record
	if m.ID == "shopping" && (e.ID == "items" || e.ID == "lists") {
		var raw []byte
		if err = tx.QueryRow(ctx, "SELECT to_jsonb(t) FROM "+pgx.Identifier{m.ID, e.ID}.Sanitize()+" t WHERE household_id=$1 AND id=$2 AND version=$3 FOR UPDATE", id.HouseholdID, rid, version).Scan(&raw); errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		} else if err != nil {
			return err
		}
		shoppingBefore, err = decode(raw)
		if err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, "DELETE FROM "+pgx.Identifier{m.ID, e.ID}.Sanitize()+" WHERE household_id=$1 AND id=$2 AND version=$3", id.HouseholdID, rid, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if shoppingBefore != nil {
		if err = emitShoppingDeletion(ctx, tx, id, e.ID, shoppingBefore, generation); err != nil {
			return err
		}
	}
	if err = cleanupDeletedIngredientBinding(ctx, tx, id, m.ID, e.ID, rid); err != nil {
		return err
	}
	if err = s.cleanupOrphans(ctx, tx, id, m); err != nil {
		return err
	}
	if err = Audit(ctx, tx, id, m.ID, e.ID, rid, "record_delete", requestID); err != nil {
		return err
	}
	if financeOperation != "" {
		if err = completeFinanceOrigin(ctx, tx, id, financeScope, financeOperation); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) cleanupOrphans(ctx context.Context, tx pgx.Tx, id auth.Identity, m module.Descriptor) error {
	for _, entity := range m.Entities {
		table := pgx.Identifier{m.ID, entity.ID}.Sanitize()
		exists := "EXISTS(SELECT 1 FROM " + table + " t WHERE t.household_id=d.household_id AND t.id=d.resource_id)"
		if _, err := tx.Exec(ctx, "DELETE FROM core.search_documents d WHERE household_id=$1 AND module_id=$2 AND resource_type=$3 AND NOT "+exists, id.HouseholdID, m.ID, entity.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE core.reminders d SET state='cancelled',cancelled_at=now() WHERE household_id=$1 AND module_id=$2 AND resource_type=$3 AND state='pending' AND NOT "+exists, id.HouseholdID, m.ID, entity.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE core.attachments d SET state='orphaned' WHERE household_id=$1 AND module_id=$2 AND resource_type=$3 AND state='ready' AND NOT "+exists, id.HouseholdID, m.ID, entity.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO core.file_deletions(storage_key,household_id,attachment_id,available_at) SELECT storage_key,household_id,id,now()+interval '7 days' FROM core.attachments WHERE household_id=$1 AND module_id=$2 AND resource_type=$3 AND state='orphaned' ON CONFLICT DO NOTHING", id.HouseholdID, m.ID, entity.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO core.file_deletions(storage_key,household_id,attachment_id,available_at) SELECT d.storage_key,a.household_id,a.id,now()+interval '7 days' FROM core.attachment_derivatives d JOIN core.attachments a ON a.id=d.attachment_id WHERE a.household_id=$1 AND a.module_id=$2 AND a.resource_type=$3 AND a.state='orphaned' ON CONFLICT DO NOTHING", id.HouseholdID, m.ID, entity.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM core.resource_links d WHERE household_id=$1 AND ((source_module=$2 AND source_type=$3 AND NOT EXISTS(SELECT 1 FROM "+table+" t WHERE t.household_id=d.household_id AND t.id=d.source_id)) OR (target_module=$2 AND target_type=$3 AND NOT EXISTS(SELECT 1 FROM "+table+" t WHERE t.household_id=d.household_id AND t.id=d.target_id)))", id.HouseholdID, m.ID, entity.ID); err != nil {
			return err
		}
	}
	return nil
}

func Audit(ctx context.Context, tx pgx.Tx, id auth.Identity, mid, entity, rid, action, requestID string) error {
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	return audit.Event(ctx, tx, mid, entity, rid, action, nil)
}

// DecodeRecord preserves PostgreSQL numeric values exactly for rendering and
// further domain calculations rather than coercing them through float64.
func DecodeRecord(raw []byte) (Record, error) {
	out := Record{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&out)
	return out, err
}

func decode(raw []byte) (Record, error) { return DecodeRecord(raw) }
func String(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func Int(v any) int64 { n, _ := strconv.ParseInt(String(v), 10, 64); return n }
func rollback(tx pgx.Tx) {
	err := tx.Rollback(context.Background())
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return
	}
}
