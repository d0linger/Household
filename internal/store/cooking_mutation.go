package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type CookingInput struct {
	Generation      int64    `json:"generation"`
	Version         int64    `json:"version"`
	Action          string   `json:"action"`
	SourceVersionID string   `json:"source_version_id"`
	Servings        string   `json:"servings"`
	CurrentStep     int      `json:"current_step"`
	CheckedIDs      []string `json:"checked_ids"`
	TimerID         string   `json:"timer_id"`
	TimerVersion    int64    `json:"timer_version"`
	TimerName       string   `json:"timer_name"`
	Seconds         int64    `json:"seconds"`
	ActualServings  string   `json:"actual_servings"`
	Rating          *int     `json:"rating"`
	Notes           string   `json:"notes"`
	CookedOn        string   `json:"cooked_on"`
}

type CookingConflict struct{ Message string }

func (e CookingConflict) Error() string { return e.Message }
func (e CookingConflict) Unwrap() error { return ErrConflict }

func cookingResponse(sid, state, recordID string) Record {
	return Record{"id": sid, "state": state, "record_id": recordID, "url": "/m/recipes/cooking/" + sid}
}

func (s *Store) MutateCooking(ctx context.Context, id auth.Identity, sid, key, requestID string, in CookingInput) (Record, error) {
	if !value.ValidUUID(key) || sid != "" && !value.ValidUUID(sid) || (sid == "") != (in.Action == "start") {
		return nil, Invalid{"Ungültiger Kochvorgang"}
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "recipes", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	op := "recipes/cooking/" + sid
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id.UserID+op+key); err != nil {
		return nil, err
	}
	var priorHash, prior []byte
	err = tx.QueryRow(ctx, "SELECT request_hash,response_payload FROM core.idempotency_records WHERE household_id=$1 AND user_id=$2 AND operation=$3 AND key=$4", id.HouseholdID, id.UserID, op, key).Scan(&priorHash, &prior)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return nil, ErrConflict
		}
		return decode(prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var response Record
	if in.Action == "start" {
		sid, err = s.startCooking(ctx, tx, id, in)
		response = cookingResponse(sid, "active", "")
	} else {
		var locked string
		err = tx.QueryRow(ctx, "SELECT id FROM recipes.cooking_sessions WHERE household_id=$1 AND user_id=$2 AND id=$3 FOR UPDATE", id.HouseholdID, id.UserID, sid).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "SELECT id FROM recipes.cooking_timers WHERE household_id=$1 AND session_id=$2 ORDER BY id FOR UPDATE NOWAIT", id.HouseholdID, sid); err != nil {
			return nil, shoppingContention(err)
		}
		var session CookingSession
		session, err = readCooking(ctx, tx, id, sid)
		if err != nil {
			return nil, err
		}
		if in.Action == "finish" {
			if err = normalizeCookingFinish(&in, session.Today); err != nil {
				return nil, err
			}
			var replay Record
			replay, err = existingCookingFinish(ctx, tx, id, session, in)
			if err != nil || replay != nil {
				return replay, err
			}
		}
		if in.Action == "abandon" && session.State == "abandoned" {
			return cookingResponse(sid, "abandoned", ""), nil
		}
		if session.Version != in.Version || session.State != "active" {
			return nil, ErrConflict
		}
		response, err = changeCooking(ctx, tx, id, session, in)
	}
	if err != nil {
		return nil, shoppingContention(err)
	}
	if err = Audit(ctx, tx, id, "recipes", "cooking_sessions", sid, "record_write", requestID); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload) VALUES($1,$2,$3,$4,$5,'recipes',$6,200,$7)`, id.HouseholdID, id.UserID, op, key, hash[:], in.Generation, raw); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return response, nil
}

func (s *Store) startCooking(ctx context.Context, tx pgx.Tx, id auth.Identity, in CookingInput) (string, error) {
	if !value.ValidUUID(in.SourceVersionID) {
		return "", ErrNotFound
	}
	servings, err := cookingServings(in.Servings)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "cooking/start/"+id.HouseholdID+id.UserID); err != nil {
		return "", err
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM recipes.cooking_sessions WHERE household_id=$1 AND user_id=$2 AND state='active'", id.HouseholdID, id.UserID).Scan(&count); err != nil {
		return "", err
	}
	if count >= 10 {
		return "", Invalid{"Zehn Kochsitzungen sind offen. Eine Sitzung zuerst abschließen oder verwerfen."}
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT v.snapshot FROM recipes.versions v JOIN recipes.recipes r ON r.household_id=v.household_id AND r.id=v.recipe_id WHERE v.household_id=$1 AND v.id=$2 FOR KEY SHARE OF r NOWAIT`, id.HouseholdID, in.SourceVersionID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	bundle, err := recipeBundle(raw)
	if err != nil {
		return "", err
	}
	if len(bundle.Steps) == 0 {
		return "", Invalid{"Dieses Rezept hat noch keine Zubereitungsschritte. Ergänze zuerst einen Schritt im Rezepteditor."}
	}
	sid := value.UUID()
	_, err = tx.Exec(ctx, "INSERT INTO recipes.cooking_sessions(id,household_id,user_id,source_version_id,servings) VALUES($1,$2,$3,$4,$5)", sid, id.HouseholdID, id.UserID, in.SourceVersionID, servings.FloatString(6))
	return sid, err
}

func validateCookingProgress(session CookingSession, in CookingInput) error {
	if in.CurrentStep < 0 || in.CurrentStep >= len(session.Source.Steps) || len(in.CheckedIDs) > 500 {
		return Invalid{"Schritt oder Zutatenfortschritt gehört nicht zu diesem Rezeptstand"}
	}
	available := make(map[string]bool, len(session.Source.Ingredients))
	for _, ingredient := range session.Source.Ingredients {
		available[String(ingredient["id"])] = true
	}
	for _, selected := range in.CheckedIDs {
		if !available[selected] {
			return Invalid{"Abgehakte Zutaten müssen eindeutig zu diesem Rezeptstand gehören"}
		}
		delete(available, selected)
	}
	return nil
}

func normalizeCookingFinish(in *CookingInput, today string) error {
	servings, err := cookingServings(in.ActualServings)
	if err != nil {
		return err
	}
	in.ActualServings = servings.FloatString(6)
	in.Notes = strings.TrimSpace(in.Notes)
	if !value.Date(in.CookedOn) || in.CookedOn > today {
		return Invalid{"Zubereitungsdatum: gültiges Datum bis einschließlich heute wählen"}
	}
	if in.Rating != nil && (*in.Rating < 1 || *in.Rating > 5) {
		return Invalid{"Bewertung: keine Angabe oder eine Zahl zwischen 1 und 5"}
	}
	if len(in.Notes) > 20000 || !utf8.ValidString(in.Notes) || strings.ContainsRune(in.Notes, '\x00') {
		return Invalid{"Kochnotizen: höchstens 20000 UTF-8-Bytes ohne Nullzeichen"}
	}
	return nil
}

func existingCookingFinish(ctx context.Context, tx pgx.Tx, id auth.Identity, session CookingSession, in CookingInput) (Record, error) {
	var rid, servings, date, notes string
	var rating int
	err := tx.QueryRow(ctx, "SELECT id,actual_servings::text,cooked_on::text,notes,coalesce(rating,0) FROM recipes.cooking_records WHERE household_id=$1 AND session_id=$2", id.HouseholdID, session.ID).Scan(&rid, &servings, &date, &notes, &rating)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	wantedRating := 0
	if in.Rating != nil {
		wantedRating = *in.Rating
	}
	if servings != in.ActualServings || date != in.CookedOn || notes != in.Notes || rating != wantedRating {
		return nil, CookingConflict{"Diese Kochsitzung ist bereits mit anderen Angaben abgeschlossen. Den gespeicherten Verlauf prüfen."}
	}
	return cookingResponse(session.ID, "completed", rid), nil
}

func changeCooking(ctx context.Context, tx pgx.Tx, id auth.Identity, session CookingSession, in CookingInput) (Record, error) {
	state, recordID := "active", ""
	var err error
	switch in.Action {
	case "progress":
		if err = validateCookingProgress(session, in); err != nil {
			return nil, err
		}
		if in.CheckedIDs == nil {
			in.CheckedIDs = []string{}
		}
		_, err = tx.Exec(ctx, "UPDATE recipes.cooking_sessions SET current_step=$3,checked_ids=$4 WHERE household_id=$1 AND id=$2", id.HouseholdID, session.ID, in.CurrentStep, in.CheckedIDs)
	case "portions":
		servings, parseErr := cookingServings(in.Servings)
		if parseErr != nil {
			return nil, parseErr
		}
		_, err = tx.Exec(ctx, "UPDATE recipes.cooking_sessions SET servings=$3 WHERE household_id=$1 AND id=$2", id.HouseholdID, session.ID, servings.FloatString(6))
	case "finish", "abandon":
		state = "abandoned"
		if in.Action == "finish" {
			state, recordID = "completed", value.UUID()
			_, err = tx.Exec(ctx, `INSERT INTO recipes.cooking_records(id,household_id,session_id,user_id,source_version_id,actual_servings,rating,notes,cooked_on) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, recordID, id.HouseholdID, session.ID, id.UserID, session.SourceVersionID, in.ActualServings, in.Rating, in.Notes, in.CookedOn)
			if err != nil {
				return nil, err
			}
		}
		// Stop timers while the parent is still active; the parent transition is last.
		if _, err = tx.Exec(ctx, "UPDATE recipes.cooking_timers SET state='done',deadline=NULL,remaining_millis=NULL WHERE household_id=$1 AND session_id=$2 AND state<>'done'", id.HouseholdID, session.ID); err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, "UPDATE recipes.cooking_sessions SET state=$3 WHERE household_id=$1 AND id=$2", id.HouseholdID, session.ID, state)
	case "timer_add", "pause", "resume", "restart", "acknowledge", "timer_remove":
		if err = changeCookingTimer(ctx, tx, id, session, in); err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, "UPDATE recipes.cooking_sessions SET updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2", id.HouseholdID, session.ID)
	default:
		return nil, Invalid{"Unbekannte Kochaktion"}
	}
	return cookingResponse(session.ID, state, recordID), err
}

func changeCookingTimer(ctx context.Context, tx pgx.Tx, id auth.Identity, session CookingSession, in CookingInput) error {
	if in.Action == "timer_add" {
		name := strings.TrimSpace(in.TimerName)
		if name == "" || utf8.RuneCountInString(name) > 100 || !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') {
			return Invalid{"Timername: 1 bis 100 Zeichen erforderlich"}
		}
		if len(session.Timers) >= 20 {
			return Invalid{"Höchstens 20 Timer pro Sitzung. Einen vorhandenen Timer zuerst entfernen."}
		}
		timer, err := cookingTimerTransition(CookingTimer{}, "restart", in.Seconds, session.ServerTime)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "INSERT INTO recipes.cooking_timers(household_id,session_id,name,duration_seconds,state,deadline) VALUES($1,$2,$3,$4,'running',$5)", id.HouseholdID, session.ID, name, timer.DurationSeconds, timer.Deadline)
		return err
	}
	for _, timer := range session.Timers {
		if timer.ID != in.TimerID {
			continue
		}
		if timer.Version != in.TimerVersion {
			return ErrConflict
		}
		if in.Action == "timer_remove" {
			_, err := tx.Exec(ctx, "DELETE FROM recipes.cooking_timers WHERE household_id=$1 AND session_id=$2 AND id=$3", id.HouseholdID, session.ID, timer.ID)
			return err
		}
		updated, err := cookingTimerTransition(timer, in.Action, in.Seconds, session.ServerTime)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE recipes.cooking_timers SET state=$4,duration_seconds=$5,deadline=$6,remaining_millis=$7 WHERE household_id=$1 AND session_id=$2 AND id=$3", id.HouseholdID, session.ID, timer.ID, updated.State, updated.DurationSeconds, updated.Deadline, updated.RemainingMillis)
		return err
	}
	return ErrNotFound
}
