package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/recipeimport"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

type RecipeImportDuplicate struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	Servings    string `json:"servings"`
	Fingerprint string `json:"fingerprint"`
	Reason      string `json:"reason"`
}

type RecipeImportRow struct {
	Row           int                     `json:"row"`
	Proposed      recipeimport.Recipe     `json:"proposed"`
	Losses        []string                `json:"losses"`
	Errors        []string                `json:"errors"`
	Duplicates    []RecipeImportDuplicate `json:"duplicates"`
	Status        string                  `json:"status"`
	PreviewDigest string                  `json:"preview_digest"`
}

type RecipeImportPreview struct {
	ID            string            `json:"id"`
	ExpiresAt     time.Time         `json:"expires_at"`
	PreviewDigest string            `json:"preview_digest"`
	Rows          []RecipeImportRow `json:"rows"`
}

type RecipeImportDecision struct {
	Decision          string `json:"decision"`
	TargetID          string `json:"target_id,omitempty"`
	TargetFingerprint string `json:"target_fingerprint,omitempty"`
	PreviewDigest     string `json:"preview_digest"`
	Generation        int64  `json:"generation"`
}

type recipeImportStored struct {
	Proposed       recipeimport.Recipe `json:"proposed"`
	Losses         []string            `json:"losses"`
	Profile        string              `json:"profile"`
	SourceDigest   string              `json:"source_digest"`
	PortableDigest string              `json:"portable_digest"`
	ConfirmedKey   string              `json:"confirmed_key,omitempty"`
	ConfirmedHash  string              `json:"confirmed_hash,omitempty"`
}

type recipeImportCandidate struct {
	Duplicate      RecipeImportDuplicate
	URL            string
	SourceDigest   string
	PortableDigest string
}

// A preview may contain twenty recipes, but it never materializes more than
// twenty complete existing recipe trees across all duplicate checks.
type recipeImportDuplicateBudget struct {
	Candidates map[string]recipeImportCandidate
	Limit      int
}

func recipeDigestBytes(s string) ([]byte, error) {
	if len(s) != 64 || strings.ToLower(s) != s {
		return nil, Invalid{Message: "Ungültiger Importfingerabdruck"}
	}
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != sha256.Size {
		return nil, Invalid{Message: "Ungültiger Importfingerabdruck"}
	}
	return b, nil
}

// CreateRecipeImportPreview persists only normalized, bounded data. Parsing and
// optional URL fetching happen before this method; no raw HTML or URL query is
// stored in a preview, audit event or database row.
func (s *Store) CreateRecipeImportPreview(ctx context.Context, id auth.Identity, generation int64,
	profile, sourceURL, sourceDigest string, items []recipeimport.Recipe) (RecipeImportPreview, error) {
	out := RecipeImportPreview{Rows: []RecipeImportRow{}}
	if len(items) == 0 || len(items) > recipeimport.MaxRecipes || len(profile) == 0 || len(profile) > 40 {
		return out, Invalid{Message: "Importprofil oder Rezeptanzahl ist ungültig"}
	}
	_, err := recipeDigestBytes(sourceDigest)
	if err != nil {
		return out, err
	}
	if sourceURL != "" {
		clean, err := recipeimport.CanonicalURL(sourceURL)
		if err != nil || clean != sourceURL {
			return out, Invalid{Message: "Quelladresse ist ungültig"}
		}
	}
	stored := make([]recipeImportStored, 0, len(items))
	for _, item := range items {
		if item.SourceURL == "" && sourceURL != "" {
			item.SourceURL = sourceURL
		}
		clean, err := recipeimport.Normalize(item)
		if err != nil {
			return out, Invalid{Message: err.Error()}
		}
		if _, _, _, err = s.normalizeRecipe(importedRecipeInput(clean, generation, "", nil)); err != nil {
			return out, Invalid{Message: "Importvorschlag passt nicht zum aktuellen Rezeptschema"}
		}
		fingerprint, err := recipeimport.PortableDigest(clean)
		if err != nil {
			return out, err
		}
		stored = append(stored, recipeImportStored{Proposed: clean, Losses: clean.Losses,
			Profile: profile, SourceDigest: sourceDigest, PortableDigest: fingerprint})
	}
	encoded, err := json.Marshal(stored)
	if err != nil || len(encoded) > recipeimport.MaxExpandedBytes {
		return out, Invalid{Message: "Rezeptvorschau ist zu groß"}
	}
	digest := sha256.Sum256(encoded)
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), value.UUID())
	tx, err := s.BeginMutation(ctx, id, "recipes", generation)
	if err != nil {
		return out, err
	}
	defer rollback(tx)
	// Reject an overbroad preview before writing its durable batch. This uses
	// only indexed candidate IDs, so the bound does not load recipe snapshots.
	uniqueCandidates := map[string]bool{}
	for _, row := range stored {
		matchingSource := row.SourceDigest
		if len(stored) != 1 {
			matchingSource = ""
		}
		ids, candidateErr := recipeImportCandidateIDs(ctx, tx, id, row.Proposed,
			row.PortableDigest, matchingSource, "")
		if candidateErr != nil {
			return out, candidateErr
		}
		for _, candidateID := range ids {
			uniqueCandidates[candidateID] = true
			if len(uniqueCandidates) > recipeimport.MaxRecipes {
				return out, Invalid{Message: "Zu viele mögliche Dubletten in dieser Vorschau; Import aufteilen"}
			}
		}
	}
	batchID := value.UUID()
	expires := time.Now().UTC().Add(25 * time.Minute)
	_, err = tx.Exec(ctx, `INSERT INTO core.import_batches
	 (id,household_id,module_id,module_generation,entity,state,row_count,valid_count,error_count,purpose,owner_user_id,expires_at,preview_sha256)
	 VALUES($1,$2,'recipes',$3,'recipes','preview',$4,$4,0,'recipe_review',$5,$6,$7)`,
		batchID, id.HouseholdID, generation, len(stored), id.UserID, expires, digest[:])
	if err != nil {
		return out, err
	}
	for i, row := range stored {
		raw, err := json.Marshal(row)
		if err != nil {
			return out, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO core.import_rows(batch_id,row_number,normalized_values,validation_errors)
		 VALUES($1,$2,$3::jsonb,'[]'::jsonb)`, batchID, i+1, raw)
		if err != nil {
			return out, err
		}
	}
	if err = Audit(ctx, tx, id, "recipes", "recipes", "", "record_write", ""); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return s.RecipeImportPreview(ctx, id, batchID)
}

func (s *Store) RecipeImportPreview(ctx context.Context, id auth.Identity, batchID string) (RecipeImportPreview, error) {
	out := RecipeImportPreview{Rows: []RecipeImportRow{}}
	if !value.ValidUUID(batchID) {
		return out, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer recipeRollback(tx)
	scope, err := recipeScope(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if !scope.CanWrite {
		return out, ErrForbidden
	}
	var digest []byte
	err = tx.QueryRow(ctx, `SELECT expires_at,preview_sha256 FROM core.import_batches
	 WHERE id=$1 AND household_id=$2 AND owner_user_id=$3 AND purpose='recipe_review'
	 AND module_id='recipes' AND entity='recipes' AND module_generation=$4
	 AND expires_at>clock_timestamp()`, batchID, id.HouseholdID, id.UserID, scope.Generation).
		Scan(&out.ExpiresAt, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.ID, out.PreviewDigest = batchID, hex.EncodeToString(digest)
	rows, err := tx.Query(ctx, `SELECT row_number,normalized_values,state FROM core.import_rows
	 WHERE batch_id=$1 ORDER BY row_number LIMIT 21`, batchID)
	if err != nil {
		return out, err
	}
	sourceDigests := make([]string, 0, recipeimport.MaxRecipes)
	for rows.Next() {
		var number int
		var raw []byte
		var state string
		if err = rows.Scan(&number, &raw, &state); err != nil {
			rows.Close()
			return out, err
		}
		var data recipeImportStored
		if err = json.Unmarshal(raw, &data); err != nil {
			rows.Close()
			return out, err
		}
		out.Rows = append(out.Rows, RecipeImportRow{Row: number, Proposed: data.Proposed,
			Losses: data.Losses, Errors: []string{}, Duplicates: []RecipeImportDuplicate{},
			Status:        map[bool]string{true: "confirmed", false: "pending"}[state == "imported"],
			PreviewDigest: out.PreviewDigest})
		sourceDigests = append(sourceDigests, data.SourceDigest)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Rows) == 0 || len(out.Rows) > recipeimport.MaxRecipes {
		return out, ErrConflict
	}
	budget := &recipeImportDuplicateBudget{Candidates: map[string]recipeImportCandidate{}, Limit: recipeimport.MaxRecipes}
	for i := range out.Rows {
		proposed := out.Rows[i].Proposed
		fingerprint, err := recipeimport.PortableDigest(proposed)
		if err != nil {
			return out, err
		}
		sourceDigest := sourceDigests[i]
		if len(out.Rows) != 1 {
			sourceDigest = ""
		}
		out.Rows[i].Duplicates, err = recipeImportDuplicates(ctx, tx, id, proposed, fingerprint, sourceDigest, "", budget)
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}

func recipeImportCandidateIDs(ctx context.Context, tx pgx.Tx, id auth.Identity,
	proposed recipeimport.Recipe, portableDigest, sourceDigest, excludeID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT r.id
	 FROM recipes.recipes r WHERE r.household_id=$1 AND r.archived_at IS NULL
	 AND r.id<>coalesce(nullif($6,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid)
	 AND (r.title=$2 OR r.source_url=nullif($3,'') OR r.portable_sha256=decode($4,'hex')
	  OR r.source_import_sha256=decode($5,'hex'))
	 ORDER BY r.updated_at DESC,r.id DESC LIMIT 6`, id.HouseholdID, proposed.Title,
		proposed.SourceURL, portableDigest, sourceDigest, excludeID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, 6)
	for rows.Next() {
		var rid string
		if err = rows.Scan(&rid); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, rid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > 5 {
		return nil, Invalid{Message: "Zu viele mögliche Dubletten; vorhandene Rezepte zuerst eingrenzen"}
	}
	return ids, nil
}

func recipeImportDuplicates(ctx context.Context, tx pgx.Tx, id auth.Identity,
	proposed recipeimport.Recipe, portableDigest, sourceDigest, excludeID string,
	budget *recipeImportDuplicateBudget) ([]RecipeImportDuplicate, error) {
	out := []RecipeImportDuplicate{}
	if budget == nil {
		budget = &recipeImportDuplicateBudget{Candidates: map[string]recipeImportCandidate{}, Limit: 5}
	}
	ids, err := recipeImportCandidateIDs(ctx, tx, id, proposed, portableDigest, sourceDigest, excludeID)
	if err != nil {
		return nil, err
	}
	for _, rid := range ids {
		candidate, found := budget.Candidates[rid]
		if !found {
			if len(budget.Candidates) >= budget.Limit {
				return nil, Invalid{Message: "Zu viele mögliche Dubletten in dieser Vorschau; Import aufteilen"}
			}
			var raw []byte
			var url, existingDigest string
			err = tx.QueryRow(ctx, `SELECT recipes.content_snapshot(r.household_id,r.id),
			 coalesce(r.source_url,''),coalesce(encode(r.source_import_sha256,'hex'),'')
			 FROM recipes.recipes r WHERE r.household_id=$1 AND r.id=$2 AND r.archived_at IS NULL
			 AND (r.title=$3 OR r.source_url=nullif($4,'') OR r.portable_sha256=decode($5,'hex')
			  OR r.source_import_sha256=decode($6,'hex'))`, id.HouseholdID, rid, proposed.Title,
				proposed.SourceURL, portableDigest, sourceDigest).Scan(&raw, &url, &existingDigest)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			bundle, parseErr := recipeBundle(raw)
			if parseErr != nil {
				return nil, parseErr
			}
			candidate = recipeImportCandidate{Duplicate: RecipeImportDuplicate{
				ID: rid, Title: String(bundle.Recipe["title"]),
				Summary: String(bundle.Recipe["summary"]), Servings: String(bundle.Recipe["base_servings"]),
				Fingerprint: bundle.Fingerprint}, URL: url, SourceDigest: existingDigest}
			current, portableErr := portableRecipeFromSnapshot(raw)
			if portableErr == nil {
				candidate.PortableDigest, _ = recipeimport.PortableDigest(current)
			}
			budget.Candidates[rid] = candidate
		}
		duplicate := candidate.Duplicate
		duplicate.Reason = "gleicher Titel"
		if candidate.PortableDigest == portableDigest {
			duplicate.Reason = "gleicher vollständiger Inhalt"
		} else if proposed.SourceURL != "" && candidate.URL == proposed.SourceURL {
			duplicate.Reason = "gleiche Quelladresse"
		} else if sourceDigest != "" && candidate.SourceDigest == sourceDigest {
			duplicate.Reason = "gleiche Importquelle"
		}
		out = append(out, duplicate)
	}
	return out, nil
}

func (s *Store) RecipePortable(ctx context.Context, id auth.Identity, rid string) ([]byte, error) {
	if !value.ValidUUID(rid) {
		return nil, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer recipeRollback(tx)
	if _, err = recipeScope(ctx, tx, id); err != nil {
		return nil, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, "SELECT recipes.content_snapshot($1,$2) FROM recipes.recipes WHERE household_id=$1 AND id=$2 AND archived_at IS NULL", id.HouseholdID, rid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	encoded, err := portableRecipeJSON(raw)
	if err != nil {
		return nil, err
	}
	return encoded, tx.Commit(ctx)
}

func rawRecipeField(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func importedRecipeInput(recipe recipeimport.Recipe, generation int64, fingerprint string, tagIDs []string) RecipeInput {
	data := map[string]json.RawMessage{
		"title": rawRecipeField(recipe.Title), "summary": rawRecipeField(recipe.Summary),
		"base_servings": rawRecipeField(recipe.Servings),
		"prep_minutes":  rawRecipeField(recipe.PrepMinutes), "cook_minutes": rawRecipeField(recipe.CookMinutes),
		"source_note": rawRecipeField(recipe.SourceNote), "notes": rawRecipeField(recipe.Notes),
	}
	out := RecipeInput{Generation: generation, Fingerprint: fingerprint, Data: data,
		Ingredients: make([]RecipeRow, 0, len(recipe.Ingredients)), Steps: make([]RecipeRow, 0, len(recipe.Steps)),
		TagIDs: tagIDs}
	for _, ingredient := range recipe.Ingredients {
		row := RecipeRow{Key: value.UUID(), Data: map[string]json.RawMessage{
			"section_name": rawRecipeField(ingredient.Section), "name": rawRecipeField(ingredient.Name),
			"quantity": rawRecipeField(ingredient.Quantity), "unit_code": rawRecipeField(ingredient.Unit),
			"quantity_note":    rawRecipeField(ingredient.QuantityNote),
			"preparation_note": rawRecipeField(ingredient.PreparationNote),
		}}
		out.Ingredients = append(out.Ingredients, row)
	}
	for _, step := range recipe.Steps {
		out.Steps = append(out.Steps, RecipeRow{Key: value.UUID(), Data: map[string]json.RawMessage{
			"instruction": rawRecipeField(step.Instruction), "timer_seconds": rawRecipeField(step.TimerSeconds),
		}})
	}
	return out
}

func recipeImportTags(ctx context.Context, tx pgx.Tx, id auth.Identity, names []string) ([]string, error) {
	ids := make([]string, 0, len(names))
	for _, name := range names {
		var tagID string
		err := tx.QueryRow(ctx, `SELECT id FROM recipes.tags WHERE household_id=$1 AND name=$2 FOR SHARE`,
			id.HouseholdID, name).Scan(&tagID)
		if errors.Is(err, pgx.ErrNoRows) {
			tagID = value.UUID()
			_, err = tx.Exec(ctx, `INSERT INTO recipes.tags(id,household_id,name,created_by)
			 VALUES($1,$2,$3,$4) ON CONFLICT(household_id,name) DO NOTHING`, tagID, id.HouseholdID, name, id.UserID)
			if err != nil {
				return nil, err
			}
			err = tx.QueryRow(ctx, `SELECT id FROM recipes.tags WHERE household_id=$1 AND name=$2 FOR SHARE`,
				id.HouseholdID, name).Scan(&tagID)
		}
		if err != nil {
			return nil, err
		}
		ids = append(ids, tagID)
	}
	return ids, nil
}

// ConfirmRecipeImportRow commits one complete recipe tree and its preview
// receipt in one database transaction. A repeated confirmation returns the
// durable receipt, while an update rechecks the chosen current fingerprint.
func (s *Store) ConfirmRecipeImportRow(ctx context.Context, id auth.Identity, batchID string, row int,
	key, requestID string, in RecipeImportDecision) (Record, error) {
	if !value.ValidUUID(batchID) || row < 1 || row > recipeimport.MaxRecipes || !value.ValidUUID(key) {
		return nil, Invalid{Message: "Ungültige Rezeptvorschau oder Vorgangskennung"}
	}
	if in.Decision != "create" && in.Decision != "update" || in.Generation < 1 || in.Generation > recipeSafeInteger {
		return nil, Invalid{Message: "Importentscheidung ist ungültig"}
	}
	if in.Decision == "update" && (!value.ValidUUID(in.TargetID) || len(in.TargetFingerprint) != 64) ||
		in.Decision == "create" && (in.TargetID != "" || in.TargetFingerprint != "") {
		return nil, Invalid{Message: "Zielrezept muss ausdrücklich geprüft werden"}
	}
	providedDigest, err := recipeDigestBytes(in.PreviewDigest)
	if err != nil {
		return nil, err
	}
	ctx = audit.WithRequest(audit.WithActor(ctx, id.UserID, id.HouseholdID), requestID)
	tx, err := s.BeginMutation(ctx, id, "recipes", in.Generation)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	decisionRaw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	decisionHash := sha256.Sum256(decisionRaw)
	decisionHashText := hex.EncodeToString(decisionHash[:])
	receiptOperation := "recipe/import-receipt/" + batchID + "/" + strconv.Itoa(row)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
		id.UserID+receiptOperation+key); err != nil {
		return nil, err
	}
	var priorHash, priorResponse []byte
	var priorGeneration int64
	err = tx.QueryRow(ctx, `SELECT request_hash,response_payload,module_generation
 FROM core.idempotency_records WHERE household_id=$1 AND user_id=$2
 AND operation=$3 AND key=$4 AND module_id='recipes'`,
		id.HouseholdID, id.UserID, receiptOperation, key).
		Scan(&priorHash, &priorResponse, &priorGeneration)
	if err == nil {
		if priorGeneration != in.Generation || !bytes.Equal(priorHash, decisionHash[:]) {
			return nil, ErrConflict
		}
		return decode(priorResponse)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var digest []byte
	var batchState string
	err = tx.QueryRow(ctx, `SELECT preview_sha256,state FROM core.import_batches
	 WHERE id=$1 AND household_id=$2 AND owner_user_id=$3 AND purpose='recipe_review'
	 AND module_id='recipes' AND entity='recipes' AND module_generation=$4
	 AND expires_at>clock_timestamp() FOR UPDATE`, batchID, id.HouseholdID, id.UserID,
		in.Generation).Scan(&digest, &batchState)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(digest, providedDigest) {
		return nil, ErrConflict
	}
	var raw []byte
	var rowState string
	var resultID *string
	err = tx.QueryRow(ctx, `SELECT normalized_values,state,result_resource_id FROM core.import_rows
	 WHERE batch_id=$1 AND row_number=$2 FOR UPDATE`, batchID, row).Scan(&raw, &rowState, &resultID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var stored recipeImportStored
	if err = json.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}
	if rowState == "imported" && resultID != nil {
		if stored.ConfirmedKey != key || stored.ConfirmedHash != decisionHashText {
			return nil, ErrConflict
		}
		response := Record{"id": *resultID, "url": "/m/recipes/recipes/" + *resultID + "/workspace"}
		if err = saveRecipeImportReceipt(ctx, tx, id, receiptOperation, key,
			decisionHash[:], in.Generation, response); err != nil {
			return nil, err
		}
		return response, tx.Commit(ctx)
	}
	if rowState != "pending" || batchState != "preview" {
		return nil, ErrConflict
	}
	if _, err = recipeDigestBytes(stored.SourceDigest); err != nil {
		return nil, err
	}
	actualDigest, err := recipeimport.PortableDigest(stored.Proposed)
	if err != nil || actualDigest != stored.PortableDigest {
		return nil, ErrConflict
	}
	if in.Decision == "update" {
		var count int
		if err = tx.QueryRow(ctx, "SELECT row_count FROM core.import_batches WHERE id=$1", batchID).Scan(&count); err != nil {
			return nil, err
		}
		matchingSource := stored.SourceDigest
		if count != 1 {
			matchingSource = ""
		}
		candidates, err := recipeImportDuplicates(ctx, tx, id, stored.Proposed,
			stored.PortableDigest, matchingSource, "", nil)
		if err != nil {
			return nil, err
		}
		found := false
		for _, candidate := range candidates {
			if candidate.ID == in.TargetID && candidate.Fingerprint == in.TargetFingerprint {
				found = true
				break
			}
		}
		if !found {
			return nil, ErrConflict
		}
	}
	tagIDs, err := recipeImportTags(ctx, tx, id, stored.Proposed.Tags)
	if err != nil {
		return nil, err
	}
	input := importedRecipeInput(stored.Proposed, in.Generation, in.TargetFingerprint, tagIDs)
	data, ingredients, steps, err := s.normalizeRecipe(input)
	if err != nil {
		return nil, err
	}
	var meta *recipeImportMetadata
	if in.Decision == "create" {
		sourceDigest, _ := recipeDigestBytes(stored.SourceDigest)
		provenance, err := json.Marshal(map[string]string{"profile": stored.Profile})
		if err != nil {
			return nil, err
		}
		meta = &recipeImportMetadata{SourceURL: stored.Proposed.SourceURL,
			SourceDigest: sourceDigest, Provenance: provenance}
	}
	target := in.TargetID
	response, err := s.saveRecipeTx(ctx, tx, id, target, key, requestID, input, data,
		ingredients, steps, meta, "recipe/import/"+batchID+"/"+strconv.Itoa(row))
	if err != nil {
		return nil, err
	}
	result := String(response["id"])
	if !value.ValidUUID(result) {
		return nil, ErrConflict
	}
	stored.ConfirmedKey, stored.ConfirmedHash = key, decisionHashText
	confirmedRaw, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE core.import_rows SET state='imported',result_resource_id=$3,
	 normalized_values=$4::jsonb WHERE batch_id=$1 AND row_number=$2 AND state='pending'`,
		batchID, row, result, confirmedRaw)
	if err != nil {
		return nil, err
	}
	var pending int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM core.import_rows WHERE batch_id=$1 AND state<>'imported'`,
		batchID).Scan(&pending); err != nil {
		return nil, err
	}
	if pending == 0 {
		_, err = tx.Exec(ctx, `UPDATE core.import_batches SET state='confirmed',confirmed_by=$2,
		 confirmed_at=clock_timestamp() WHERE id=$1`, batchID, id.UserID)
		if err != nil {
			return nil, err
		}
	}
	if err = Audit(ctx, tx, id, "recipes", "recipes", result, "import_confirm", requestID); err != nil {
		return nil, err
	}
	if err = saveRecipeImportReceipt(ctx, tx, id, receiptOperation, key,
		decisionHash[:], in.Generation, response); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, shoppingContention(err)
	}
	return response, nil
}

func saveRecipeImportReceipt(ctx context.Context, tx pgx.Tx, id auth.Identity,
	operation, key string, requestHash []byte, generation int64, response Record) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.idempotency_records
 (household_id,user_id,operation,key,request_hash,module_id,module_generation,response_status,response_payload)
 VALUES($1,$2,$3,$4,$5,'recipes',$6,200,$7)`,
		id.HouseholdID, id.UserID, operation, key, requestHash, generation, encoded)
	return err
}
