package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"household/internal/recipeimport"
	"household/internal/store"
	"household/internal/value"
)

func (s *Server) recipeImportPage(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, err := s.Store.RecipeDeviceContext(r.Context(), identity(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	p := s.page(r, "Rezept importieren")
	p.View = "recipe-import-review"
	s.render(w, r, p)
}

func (s *Server) recipeImportAuthority(w http.ResponseWriter, r *http.Request) (int64, bool) {
	current, err := s.Store.RecipeDeviceContext(r.Context(), identity(r))
	if err != nil {
		s.fail(w, r, err)
		return 0, false
	}
	if !current.CanWrite {
		s.fail(w, r, store.ErrForbidden)
		return 0, false
	}
	return current.Generation, true
}

func validRecipeImportProfile(profile string) bool {
	switch profile {
	case "household-v1", "schemaorg-jsonld", "schemaorg-html", "mealie-single-json",
		"tandoor-zip", "paprika-gzip", "paprika-zip":
		return true
	default:
		return false
	}
}

func recipeImportFile(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, recipeimport.MaxInputBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return nil, "", store.Invalid{Message: "Importdatei ist ungültig oder zu groß"}
	}
	if len(r.MultipartForm.Value["profile"]) != 1 || len(r.MultipartForm.File["file"]) != 1 ||
		len(r.MultipartForm.File) != 1 || len(r.MultipartForm.Value) != 1 {
		return nil, "", store.Invalid{Message: "Genau eine Datei und ein Importprofil erforderlich"}
	}
	profile := r.FormValue("profile")
	if !validRecipeImportProfile(profile) {
		return nil, "", store.Invalid{Message: "Importprofil wird nicht unterstützt"}
	}
	header := r.MultipartForm.File["file"][0]
	if header.Size < 1 || header.Size > recipeimport.MaxInputBytes {
		return nil, "", store.Invalid{Message: "Importdatei muss zwischen 1 Byte und 2 MiB groß sein"}
	}
	file, err := header.Open()
	if err != nil {
		return nil, "", store.Invalid{Message: "Importdatei konnte nicht geöffnet werden"}
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, recipeimport.MaxInputBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > recipeimport.MaxInputBytes {
		return nil, "", store.Invalid{Message: "Importdatei konnte nicht begrenzt gelesen werden"}
	}
	return raw, profile, nil
}

func (s *Server) recipeImportPreviewCreate(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	generation, ok := s.recipeImportAuthority(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		s.fail(w, r, store.Invalid{Message: "Importformat ist ungültig"})
		return
	}
	var raw []byte
	var profile, sourceURL, sourceDigest string
	switch mediaType {
	case "multipart/form-data":
		raw, profile, err = recipeImportFile(w, r)
		if err == nil {
			digest := sha256.Sum256(raw)
			sourceDigest = hex.EncodeToString(digest[:])
		}
	case "application/json":
		var input struct {
			URL     string `json:"url"`
			Profile string `json:"profile"`
		}
		if err = readJSON(w, r, &input); err == nil {
			if !validRecipeImportProfile(input.Profile) ||
				input.Profile != "household-v1" && input.Profile != "schemaorg-jsonld" &&
					input.Profile != "schemaorg-html" && input.Profile != "mealie-single-json" {
				err = store.Invalid{Message: "URL-Import erfordert ein HTML- oder JSON-Profil"}
			} else if s.recipeFetcher == nil {
				err = store.Invalid{Message: "URL-Import ist nicht verfügbar"}
			} else {
				var fetched recipeimport.FetchResult
				fetched, err = s.recipeFetcher.Fetch(ctx, input.URL)
				if err == nil {
					compatible := input.Profile == "schemaorg-html" && fetched.ContentType == "text/html" ||
						input.Profile == "schemaorg-jsonld" &&
							(fetched.ContentType == "application/ld+json" || fetched.ContentType == "application/json") ||
						(input.Profile == "household-v1" || input.Profile == "mealie-single-json") &&
							fetched.ContentType == "application/json"
					if !compatible {
						err = store.Invalid{Message: "URL-Inhaltstyp passt nicht zum gewählten Profil"}
					} else {
						raw, profile, sourceURL, sourceDigest = fetched.Body, input.Profile,
							fetched.CanonicalURL, fetched.SHA256
					}
				}
			}
		}
	default:
		err = store.Invalid{Message: "Import erwartet eine Datei oder JSON mit URL"}
	}
	if err != nil {
		var invalid store.Invalid
		if errors.As(err, &invalid) {
			s.fail(w, r, err)
		} else {
			s.fail(w, r, store.Invalid{Message: "Importquelle konnte nicht sicher gelesen werden"})
		}
		return
	}
	items, err := recipeimport.Parse(raw, profile)
	if err != nil {
		s.fail(w, r, store.Invalid{Message: "Importprofil oder Rezeptdaten sind ungültig"})
		return
	}
	result, err := s.Store.CreateRecipeImportPreview(ctx, identity(r), generation,
		profile, sourceURL, sourceDigest, items)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) recipeImportPreviewGet(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, ok := s.recipeImportAuthority(w, r); !ok {
		return
	}
	result, err := s.Store.RecipeImportPreview(r.Context(), identity(r), r.PathValue("batch"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recipeImportConfirm(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, ok := s.recipeImportAuthority(w, r); !ok {
		return
	}
	row, err := strconv.Atoi(r.PathValue("row"))
	if err != nil || row < 1 || row > recipeimport.MaxRecipes ||
		!value.ValidUUID(r.PathValue("batch")) {
		s.fail(w, r, store.Invalid{Message: "Vorschauzeile ist ungültig"})
		return
	}
	var input store.RecipeImportDecision
	if err := readJSON(w, r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	if input.Decision != "create" && input.Decision != "update" ||
		strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		s.fail(w, r, store.Invalid{Message: "Entscheidung und Vorgangs-ID sind erforderlich"})
		return
	}
	result, err := s.Store.ConfirmRecipeImportRow(r.Context(), identity(r), r.PathValue("batch"),
		row, r.Header.Get("Idempotency-Key"), trace(r), input)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recipePortableExport(w http.ResponseWriter, r *http.Request) {
	s.Store.Gate.RLock()
	defer s.Store.Gate.RUnlock()
	if _, err := s.Store.RecipeDeviceContext(r.Context(), identity(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	result, err := s.Store.RecipePortable(r.Context(), identity(r), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="household-rezept.json"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.writeBytes(w, r, result)
}
