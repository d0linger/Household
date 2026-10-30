package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"time"

	"household/internal/auth"
	"household/internal/store"
	"household/internal/value"
)

func (s *Server) recipeInsightsPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, err := s.Store.RecipeDeviceContext(r.Context(), identity(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	if !value.ValidUUID(r.PathValue("id")) {
		s.fail(w, r, store.ErrNotFound)
		return
	}
	m, _ := s.Store.Module("recipes")
	e, _ := m.Entity("recipes")
	recipe, err := s.Store.Get(r.Context(), identity(r), m, e, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Rezeptwissen")
	p.View, p.Module, p.Entity, p.Record = "recipe-insights", m, e, recipe
	s.render(w, r, p)
}

func (s *Server) recipeInsightsAuthority(w http.ResponseWriter, r *http.Request, writable bool) (auth.Identity, store.RecipeDeviceContext, bool) {
	id := identity(r)
	current, err := s.Store.RecipeDeviceContext(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return auth.Identity{}, store.RecipeDeviceContext{}, false
	}
	if writable && !current.CanWrite {
		s.fail(w, r, store.ErrForbidden)
		return auth.Identity{}, store.RecipeDeviceContext{}, false
	}
	return id, current, true
}

func (s *Server) recipeInsightsGet(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	id, current, ok := s.recipeInsightsAuthority(w, r, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := s.Store.RecipeInsights(ctx, id, r.PathValue("id"), r.URL.Query().Get("servings"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	state, ok := result["context"].(map[string]any)
	if !ok || store.Int(state["recipes_generation"]) != current.Generation {
		s.fail(w, r, store.ErrConflict)
		return
	}
	state["owner_scope"], state["session_scope"], state["auth_scope"] =
		current.OwnerScope, current.SessionScope, current.AuthScope
	state["role"], state["offline_allowed"] = current.Role, current.OfflineAllowed
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recipeProfilePut(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	id, _, ok := s.recipeInsightsAuthority(w, r, true)
	if !ok {
		return
	}
	var input store.RecipeProfileInput
	if err := readJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	result, err := s.Store.SaveRecipeProfile(r.Context(), id, r.PathValue("id"),
		r.Header.Get("Idempotency-Key"), trace(r), input)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recipeSubstitutionsPut(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	id, _, ok := s.recipeInsightsAuthority(w, r, true)
	if !ok {
		return
	}
	var input store.RecipeSubstitutionInput
	if err := readJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	result, err := s.Store.SaveRecipeSubstitutions(r.Context(), id, r.PathValue("id"),
		r.Header.Get("Idempotency-Key"), trace(r), input)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recipeDensityPut(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	id, _, ok := s.recipeInsightsAuthority(w, r, true)
	if !ok {
		return
	}
	var input store.RecipeDensityInput
	if err := readJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	result, err := s.Store.SaveRecipeDensity(r.Context(), id, r.PathValue("id"),
		r.Header.Get("Idempotency-Key"), trace(r), input)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recipeShoppingBindingPut(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	id, _, ok := s.recipeInsightsAuthority(w, r, true)
	if !ok {
		return
	}
	var input store.RecipePriceBindingInput
	if err := readJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	result, err := s.Store.SaveRecipeShoppingBinding(r.Context(), id, r.PathValue("id"),
		r.Header.Get("Idempotency-Key"), trace(r), input)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recipeReadSnapshotGet(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	id, current, ok := s.recipeInsightsAuthority(w, r, false)
	if !ok {
		return
	}
	if !current.OfflineAllowed {
		s.fail(w, r, store.ErrForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := s.Store.RecipeReadSnapshot(ctx, id, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	snapshot, ok := result["snapshot"].(map[string]any)
	if !ok {
		s.fail(w, r, store.ErrConflict)
		return
	}
	images, err := s.recipeSnapshotImages(ctx, id.HouseholdID, r.PathValue("id"), current.Generation)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	snapshot["images"] = images
	latest, err := s.Store.RecipeDeviceContext(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	actual, ok := result["context"].(store.RecipeDeviceContext)
	if !ok || actual.SessionScope != current.SessionScope ||
		actual.AuthScope != current.AuthScope || actual.Generation != current.Generation ||
		latest.SessionScope != current.SessionScope || latest.AuthScope != current.AuthScope ||
		latest.Generation != current.Generation || !latest.OfflineAllowed {
		s.fail(w, r, store.ErrConflict)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// recipeSnapshotImages includes only verified, small first-party attachments.
// A missing or invalid file is omitted; storage paths and attachment IDs never
// cross the offline snapshot boundary.
func (s *Server) recipeSnapshotImages(ctx context.Context, householdID, recipeID string, generation int64) ([]store.Record, error) {
	images := []store.Record{}
	rows, err := s.Pool.Query(ctx, `SELECT storage_key,original_name,media_type,size_bytes,sha256
 FROM core.attachments WHERE household_id=$1 AND module_id='recipes'
 AND resource_type='recipes' AND resource_id=$2 AND module_generation=$3
 AND state='ready' AND media_type IN ('image/jpeg','image/png')
 AND size_bytes BETWEEN 1 AND 204800
 ORDER BY created_at DESC,id DESC LIMIT 3`, householdID, recipeID, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	root, err := os.OpenRoot(s.Cfg.UploadDir)
	if err != nil {
		return images, nil
	}
	defer func() { _ = root.Close() }()
	for rows.Next() {
		if len(images) == 2 {
			break
		}
		var key, name, media string
		var size int64
		var expected []byte
		if err := rows.Scan(&key, &name, &media, &size, &expected); err != nil {
			return nil, err
		}
		if !value.ValidUUID(key) || len(expected) != sha256.Size || size < 1 || size > 204800 {
			continue
		}
		select {
		case s.imageSlots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		func() {
			defer func() { <-s.imageSlots }()
			file, openErr := root.Open(key)
			if openErr != nil {
				return
			}
			defer func() { _ = file.Close() }()
			raw, readErr := io.ReadAll(io.LimitReader(file, 204801))
			if readErr != nil || len(raw) != int(size) || len(raw) > 204800 ||
				http.DetectContentType(raw) != media {
				return
			}
			digest := sha256.Sum256(raw)
			if !bytes.Equal(digest[:], expected) {
				return
			}
			config, format, decodeErr := image.DecodeConfig(bytes.NewReader(raw))
			if decodeErr != nil || config.Width < 1 || config.Height < 1 ||
				config.Width > 1024 || config.Height > 1024 ||
				format != "jpeg" && format != "png" {
				return
			}
			if len(name) > 300 {
				name = name[:300]
			}
			images = append(images, store.Record{"mime_type": media,
				"base64": base64.StdEncoding.EncodeToString(raw), "alt": name, "caption": ""})
		}()
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return images, nil
}
