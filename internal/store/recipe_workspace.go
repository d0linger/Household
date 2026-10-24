package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/module"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type RecipeBundle struct {
	Recipe      Record   `json:"recipe"`
	Ingredients []Record `json:"ingredients"`
	Steps       []Record `json:"steps"`
	Tags        []Record `json:"tags"`
	Fingerprint string   `json:"fingerprint"`
}

type RecipeRow struct {
	Key  string                     `json:"key"`
	ID   string                     `json:"id"`
	Data map[string]json.RawMessage `json:"data"`
}

type RecipeInput struct {
	Generation      int64                      `json:"generation"`
	Fingerprint     string                     `json:"fingerprint"`
	Data            map[string]json.RawMessage `json:"data"`
	Ingredients     []RecipeRow                `json:"ingredients"`
	Steps           []RecipeRow                `json:"steps"`
	TagIDs          []string                   `json:"tag_ids"`
	SourceVersionID string                     `json:"source_version_id"`
}

type RecipeErrors map[string]string

func (e RecipeErrors) Error() string { return "Bitte die markierten Rezeptfelder prüfen" }

func recipeBundle(raw []byte) (RecipeBundle, error) {
	var bundle RecipeBundle
	if len(raw) == 0 || string(raw) == "null" {
		return bundle, ErrNotFound
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&bundle); err != nil {
		return bundle, err
	}
	if len(bundle.Ingredients) > 500 || len(bundle.Steps) > 500 || len(bundle.Tags) > 100 {
		return bundle, Invalid{"Rezept aufteilen: höchstens 500 Zutaten, 500 Schritte und 100 Tags"}
	}
	canonical, err := json.Marshal(bundle)
	if err != nil {
		return bundle, err
	}
	sum := sha256.Sum256(canonical)
	bundle.Fingerprint = hex.EncodeToString(sum[:])
	return bundle, nil
}

// Recipe reads all three collections with one statement and a fixed number of
// indexed subqueries. Quantity values are strings before crossing the JSON API.
func (s *Store) Recipe(ctx context.Context, id auth.Identity, rid string) (RecipeBundle, error) {
	if !value.ValidUUID(rid) {
		return RecipeBundle{}, ErrNotFound
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, "SELECT recipes.content_snapshot($1,$2)", id.HouseholdID, rid).Scan(&raw)
	if err != nil {
		return RecipeBundle{}, err
	}
	return recipeBundle(raw)
}

func (s *Store) RecipeVersions(ctx context.Context, id auth.Identity, rid string, before int64) ([]Record, error) {
	if !value.ValidUUID(rid) || before < 0 {
		return nil, ErrNotFound
	}
	rows, err := s.Pool.Query(ctx, `SELECT jsonb_build_object('id',v.id,'revision',v.revision::text,'created_at',v.created_at,'title',v.snapshot->'recipe'->>'title')
 FROM recipes.versions v WHERE v.household_id=$1 AND v.recipe_id=$2 AND ($3::bigint=0 OR v.revision<$3)
 AND EXISTS(SELECT 1 FROM recipes.recipes r WHERE r.household_id=v.household_id AND r.id=v.recipe_id)
 ORDER BY v.revision DESC LIMIT 21`, id.HouseholdID, rid, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []Record{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		record, decodeErr := decode(raw)
		if decodeErr != nil {
			return nil, decodeErr
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) RecipeVersion(ctx context.Context, id auth.Identity, rid, vid string) (RecipeBundle, error) {
	if !value.ValidUUID(rid) || !value.ValidUUID(vid) {
		return RecipeBundle{}, ErrNotFound
	}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT v.snapshot FROM recipes.versions v JOIN recipes.recipes r ON r.household_id=v.household_id AND r.id=v.recipe_id WHERE v.household_id=$1 AND v.recipe_id=$2 AND v.id=$3`, id.HouseholdID, rid, vid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return RecipeBundle{}, ErrNotFound
	}
	if err != nil {
		return RecipeBundle{}, err
	}
	return recipeBundle(raw)
}

func recipeNormalize(e module.Entity, input map[string]json.RawMessage, path string, failures RecipeErrors) Record {
	out := Record{}
	allowed := map[string]bool{}
	for _, field := range e.Fields {
		allowed[field.Name] = true
		single := module.Entity{Fields: []module.Field{field}}
		values := map[string]json.RawMessage{}
		if raw, ok := input[field.Name]; ok {
			values[field.Name] = raw
		}
		normalized, err := single.Normalize(values, false)
		if err != nil {
			failures[path+"."+field.Name] = err.Error()
			continue
		}
		for key, val := range normalized {
			out[key] = val
		}
		if val, exists := out[field.Name]; !exists || val == nil {
			switch field.Kind {
			case "text", "longtext":
				out[field.Name] = ""
			case "integer":
				out[field.Name] = int64(0)
			default:
				out[field.Name] = nil
			}
		}
	}
	for key := range input {
		if !allowed[key] {
			failures[path+"."+key] = "Unbekanntes Feld"
		}
	}
	return out
}

func (s *Store) normalizeRecipe(in RecipeInput) (Record, []Record, []Record, error) {
	if len(in.Ingredients) > 500 || len(in.Steps) > 500 || len(in.TagIDs) > 100 {
		return nil, nil, nil, RecipeErrors{"recipe.title": "Höchstens 500 Zutaten, 500 Schritte und 100 Tags"}
	}
	m, _ := s.Module("recipes")
	recipe, _ := m.Entity("recipes")
	ingredient, _ := m.Entity("ingredients")
	step, _ := m.Entity("steps")
	failures := RecipeErrors{}
	data := recipeNormalize(recipe, in.Data, "recipe", failures)
	if utf8.RuneCountInString(String(data["title"])) > 200 {
		failures["recipe.title"] = "Rezeptname: höchstens 200 Zeichen"
	}
	for _, key := range []string{"prep_minutes", "cook_minutes"} {
		if data[key] == nil {
			data[key] = int64(0)
		}
	}
	normalizeRows := func(entity module.Entity, rows []RecipeRow, name string) []Record {
		out := make([]Record, 0, len(rows))
		seen := map[string]bool{}
		for position, row := range rows {
			path := name + "." + row.Key
			if !value.ValidUUID(row.Key) || seen[row.Key] || row.ID != "" && !value.ValidUUID(row.ID) {
				failures[path] = "Ungültige oder doppelte Zeilenidentität"
			}
			seen[row.Key] = true
			input := make(map[string]json.RawMessage, len(row.Data)+2)
			for key, val := range row.Data {
				input[key] = val
			}
			if _, ok := input["recipe_id"]; ok {
				failures[path] = "Rezeptzuordnung ist nicht bearbeitbar"
			}
			if _, ok := input["position"]; ok {
				failures[path] = "Reihenfolge über die Zeilensteuerung ändern"
			}
			input["recipe_id"] = json.RawMessage(`"00000000-0000-4000-8000-000000000000"`)
			input["position"] = json.RawMessage(fmt.Sprint(position + 1))
			normalized := recipeNormalize(entity, input, path, failures)
			if entity.ID == "ingredients" && utf8.RuneCountInString(String(normalized["name"])) > 200 {
				failures[path+".name"] = "Zutat: höchstens 200 Zeichen"
			}
			if entity.ID == "steps" {
				if strings.TrimSpace(String(normalized["instruction"])) == "" {
					failures[path+".instruction"] = "Anleitung: erforderlich"
				}
				if Int(normalized["timer_seconds"]) == 0 {
					normalized["timer_seconds"] = nil
				}
			}
			out = append(out, normalized)
		}
		return out
	}
	ingredients := normalizeRows(ingredient, in.Ingredients, "ingredients")
	steps := normalizeRows(step, in.Steps, "steps")
	seenTags := map[string]bool{}
	for _, tag := range in.TagIDs {
		if !value.ValidUUID(tag) || seenTags[tag] {
			failures["tags"] = "Tags müssen gültig und eindeutig sein"
		}
		seenTags[tag] = true
	}
	if len(failures) > 0 {
		return nil, nil, nil, failures
	}
	return data, ingredients, steps, nil
}

func (s *Store) SaveRecipe(ctx context.Context, id auth.Identity, rid, key, requestID string, in RecipeInput) (Record, error) {
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	if !value.ValidUUID(key) || rid != "" && !value.ValidUUID(rid) {
		return nil, Invalid{"Gültige Vorgangs-ID erforderlich"}
	}
	data, ingredients, steps, err := s.normalizeRecipe(in)
	if err != nil {
		return nil, err
	}
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
	op := "recipe/workspace/" + rid
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
	creating := rid == ""
	current := RecipeBundle{Recipe: Record{}}
	if creating {
		rid = value.UUID()
		if in.Fingerprint != "" {
			return nil, ErrConflict
		}
		if in.SourceVersionID != "" {
			var sourceID string
			if !value.ValidUUID(in.SourceVersionID) {
				return nil, ErrNotFound
			}
			err = tx.QueryRow(ctx, `SELECT r.id FROM recipes.versions v JOIN recipes.recipes r ON r.household_id=v.household_id AND r.id=v.recipe_id WHERE v.household_id=$1 AND v.id=$2 FOR KEY SHARE OF r NOWAIT`, id.HouseholdID, in.SourceVersionID).Scan(&sourceID)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			if err != nil {
				return nil, shoppingContention(err)
			}
		}
	} else {
		var locked string
		err = tx.QueryRow(ctx, "SELECT id FROM recipes.recipes WHERE household_id=$1 AND id=$2 FOR UPDATE", id.HouseholdID, rid).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		for _, entity := range []string{"ingredients", "steps"} {
			if _, err = tx.Exec(ctx, "SELECT id FROM "+pgx.Identifier{"recipes", entity}.Sanitize()+" WHERE household_id=$1 AND recipe_id=$2 ORDER BY id FOR UPDATE NOWAIT", id.HouseholdID, rid); err != nil {
				return nil, shoppingContention(err)
			}
		}
		if _, err = tx.Exec(ctx, `SELECT t.id FROM recipes.tags t WHERE t.household_id=$1 AND (t.id=ANY($3::uuid[]) OR EXISTS(SELECT 1 FROM recipes.recipe_tags rt WHERE rt.household_id=t.household_id AND rt.tag_id=t.id AND rt.recipe_id=$2)) ORDER BY t.id FOR SHARE NOWAIT`, id.HouseholdID, rid, in.TagIDs); err != nil {
			return nil, shoppingContention(err)
		}
		var raw []byte
		if err = tx.QueryRow(ctx, "SELECT recipes.content_snapshot($1,$2)", id.HouseholdID, rid).Scan(&raw); err != nil {
			return nil, err
		}
		current, err = recipeBundle(raw)
		if err != nil {
			return nil, err
		}
		if current.Fingerprint != in.Fingerprint {
			return nil, ErrConflict
		}
		if in.SourceVersionID != "" {
			return nil, Invalid{"Herkunft bestehender Rezepte ist unveränderlich"}
		}
	}
	m, _ := s.Module("recipes")
	entity, _ := m.Entity("recipes")
	result, err := save(ctx, tx, id, m, entity, rid, data, creating, Int(current.Recipe["version"]))
	if err != nil {
		return nil, err
	}
	if creating && in.SourceVersionID != "" {
		if _, err = tx.Exec(ctx, "UPDATE recipes.recipes SET source_version_id=$3 WHERE household_id=$1 AND id=$2", id.HouseholdID, rid, in.SourceVersionID); err != nil {
			return nil, err
		}
	}
	if err = s.replaceRecipeRows(ctx, tx, id, rid, in.Generation, "ingredients", in.Ingredients, ingredients, current.Ingredients); err != nil {
		return nil, err
	}
	if err = s.replaceRecipeRows(ctx, tx, id, rid, in.Generation, "steps", in.Steps, steps, current.Steps); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM recipes.recipe_tags WHERE household_id=$1 AND recipe_id=$2", id.HouseholdID, rid); err != nil {
		return nil, err
	}
	for _, tag := range in.TagIDs {
		var locked string
		err = tx.QueryRow(ctx, "SELECT id FROM recipes.tags WHERE household_id=$1 AND id=$2 FOR SHARE NOWAIT", id.HouseholdID, tag).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, RecipeErrors{"tags": "Ein ausgewählter Tag wurde gelöscht"}
		}
		if err != nil {
			return nil, shoppingContention(err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO recipes.recipe_tags(household_id,recipe_id,tag_id) VALUES($1,$2,$3)", id.HouseholdID, rid, tag); err != nil {
			return nil, err
		}
	}
	if err = s.afterWrite(ctx, tx, id, m, entity, result, in.Generation); err != nil {
		return nil, err
	}
	if err = s.cleanupOrphans(ctx, tx, id, m); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, id, "recipes", "recipes", rid, "record_write", requestID); err != nil {
		return nil, err
	}
	response := Record{"id": rid, "url": "/m/recipes/recipes/" + rid + "/workspace"}
	raw, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records(household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload) VALUES($1,$2,$3,$4,$5,'recipes',$6,200,$7)`, id.HouseholdID, id.UserID, op, key, hash[:], in.Generation, raw)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return response, nil
}

func (s *Store) replaceRecipeRows(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string, generation int64, name string, input []RecipeRow, normalized, current []Record) error {
	m, _ := s.Module("recipes")
	entity, _ := m.Entity(name)
	existing := map[string]Record{}
	for _, row := range current {
		existing[String(row["id"])] = row
	}
	used := map[string]bool{}
	for position, row := range input {
		rowID := row.ID
		creating := rowID == ""
		if !creating && (existing[rowID] == nil || used[rowID]) {
			return RecipeErrors{name + "." + row.Key: "Zeile gehört nicht zu diesem Rezept oder ist doppelt"}
		}
		used[rowID] = true
		version := int64(0)
		if creating {
			rowID = value.UUID()
		} else {
			version = Int(existing[rowID]["version"])
		}
		data := normalized[position]
		data["recipe_id"] = rid
		record, err := save(ctx, tx, id, m, entity, rowID, data, creating, version)
		if err != nil {
			return shoppingContention(err)
		}
		if err = s.afterWrite(ctx, tx, id, m, entity, record, generation); err != nil {
			return err
		}
	}
	for rowID := range existing {
		if !used[rowID] {
			if _, err := tx.Exec(ctx, "DELETE FROM "+pgx.Identifier{"recipes", name}.Sanitize()+" WHERE household_id=$1 AND recipe_id=$2 AND id=$3", id.HouseholdID, rid, rowID); err != nil {
				return shoppingContention(err)
			}
		}
	}
	return nil
}
