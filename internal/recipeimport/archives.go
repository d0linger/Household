package recipeimport

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

type archiveBudget struct {
	entries int
	bytes   int64
}

func (b *archiveBudget) add(n int64) error {
	b.bytes += n
	if b.bytes > MaxExpandedBytes {
		return errors.New("archiv überschreitet 4 MiB entpackte Daten")
	}
	return nil
}

func zipFiles(raw []byte, budget *archiveBudget, depth int, visit func(string, []byte, int) error) error {
	if len(raw) == 0 || len(raw) > MaxExpandedBytes || depth > 1 {
		return errors.New("archivtiefe oder Größe wird nicht unterstützt")
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return errors.New("ZIP-Archiv ist ungültig")
	}
	for _, entry := range reader.File {
		budget.entries++
		if budget.entries > 64 {
			return errors.New("archiv hat mehr als 64 Einträge")
		}
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		cleanName := strings.TrimSuffix(name, "/")
		if cleanName == "" || strings.HasPrefix(cleanName, "/") || path.Clean(cleanName) != cleanName ||
			cleanName == ".." || strings.HasPrefix(cleanName, "../") || strings.Contains(cleanName, "/../") {
			return errors.New("archivpfad ist ungültig")
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if !entry.FileInfo().Mode().IsRegular() || entry.UncompressedSize64 > MaxExpandedBytes ||
			entry.CompressedSize64 == 0 || entry.UncompressedSize64 > entry.CompressedSize64*20 {
			return errors.New("archiveintrag ist ungeeignet oder zu stark komprimiert")
		}
		stream, err := entry.Open()
		if err != nil {
			return errors.New("archiveintrag kann nicht gelesen werden")
		}
		data, err := io.ReadAll(io.LimitReader(stream, MaxExpandedBytes+1))
		closeErr := stream.Close()
		if err != nil || closeErr != nil || len(data) > MaxExpandedBytes || uint64(len(data)) != entry.UncompressedSize64 {
			return errors.New("archiveintrag ist beschädigt oder zu groß")
		}
		if err := budget.add(int64(len(data))); err != nil {
			return err
		}
		if err := visit(name, data, depth); err != nil {
			return err
		}
	}
	return nil
}

func gunzip(raw []byte, budget *archiveBudget) ([]byte, error) {
	if len(raw) == 0 || len(raw) > MaxExpandedBytes {
		return nil, errors.New("GZIP-Daten sind leer oder zu groß")
	}
	compressed := bytes.NewReader(raw)
	reader, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, errors.New("GZIP-Daten sind ungültig")
	}
	reader.Multistream(false)
	data, err := io.ReadAll(io.LimitReader(reader, MaxExpandedBytes+1))
	closeErr := reader.Close()
	if err != nil || closeErr != nil || compressed.Len() != 0 ||
		len(data) > MaxExpandedBytes || len(data) > len(raw)*20 {
		return nil, errors.New("GZIP-Daten sind beschädigt oder zu groß")
	}
	if err := budget.add(int64(len(data))); err != nil {
		return nil, err
	}
	return data, nil
}

func ParseTandoorZIP(raw []byte) ([]Recipe, error) {
	if len(raw) == 0 || len(raw) > MaxInputBytes {
		return nil, errors.New("Tandoor-ZIP ist leer oder größer als 2 MiB")
	}
	budget := &archiveBudget{}
	out := []Recipe{}
	var visit func(string, []byte, int) error
	visit = func(name string, data []byte, depth int) error {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".zip") {
			if depth != 0 {
				return errors.New("mehr als eine innere Tandoor-ZIP-Ebene")
			}
			return zipFiles(data, budget, depth+1, visit)
		}
		if path.Base(lower) != "recipe.json" {
			return nil // Images and metadata are deliberately not imported.
		}
		if len(out) >= MaxRecipes {
			return errors.New("Tandoor-Archiv enthält zu viele Rezepte")
		}
		items, err := ParseTandoorJSON(data)
		if err != nil {
			return fmt.Errorf("Tandoor-JSON %q: %w", path.Base(name), err)
		}
		out = append(out, items...)
		return nil
	}
	if err := zipFiles(raw, budget, 0, visit); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("Tandoor-Archiv enthält kein unterstütztes Rezept")
	}
	return out, nil
}

func ParsePaprikaGZIP(raw []byte) ([]Recipe, error) {
	if len(raw) == 0 || len(raw) > MaxInputBytes {
		return nil, errors.New("Paprika-Datei ist leer oder größer als 2 MiB")
	}
	budget := &archiveBudget{}
	data, err := gunzip(raw, budget)
	if err != nil {
		return nil, err
	}
	return ParsePaprikaJSON(data)
}

func ParsePaprikaZIP(raw []byte) ([]Recipe, error) {
	if len(raw) == 0 || len(raw) > MaxInputBytes {
		return nil, errors.New("Paprika-ZIP ist leer oder größer als 2 MiB")
	}
	budget := &archiveBudget{}
	out := []Recipe{}
	err := zipFiles(raw, budget, 0, func(name string, data []byte, depth int) error {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".zip") {
			return errors.New("Paprika-Archiv darf keine innere ZIP enthalten")
		}
		if !strings.HasSuffix(lower, ".paprikarecipe") && !strings.HasSuffix(lower, ".gz") {
			return nil
		}
		if len(out) >= MaxRecipes {
			return errors.New("Paprika-Archiv enthält zu viele Rezepte")
		}
		decoded, err := gunzip(data, budget)
		if err != nil {
			return err
		}
		items, err := ParsePaprikaJSON(decoded)
		if err != nil {
			return fmt.Errorf("Paprika-Eintrag %q: %w", path.Base(name), err)
		}
		out = append(out, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("Paprika-Archiv enthält kein unterstütztes Rezept")
	}
	return out, nil
}

func Parse(raw []byte, profile string) ([]Recipe, error) {
	switch profile {
	case "household-v1":
		return ParseHousehold(raw)
	case "schemaorg-jsonld":
		return ParseSchemaJSONLD(raw)
	case "schemaorg-html":
		return ParseSchemaHTML(raw)
	case "mealie-single-json":
		return ParseMealie(raw)
	case "tandoor-zip":
		return ParseTandoorZIP(raw)
	case "paprika-gzip":
		return ParsePaprikaGZIP(raw)
	case "paprika-zip":
		return ParsePaprikaZIP(raw)
	default:
		return nil, errors.New("importprofil wird nicht unterstützt")
	}
}
