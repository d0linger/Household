package recipeimport

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// ParseSchemaHTML extracts only inert application/ld+json script text. It
// never loads a script, image, stylesheet or other page resource.
func ParseSchemaHTML(raw []byte) ([]Recipe, error) {
	if len(raw) == 0 || len(raw) > MaxInputBytes {
		return nil, errors.New("HTML-Quelle ist leer oder zu groß")
	}
	z := html.NewTokenizer(bytes.NewReader(raw))
	z.SetMaxBuf(MaxInputBytes + 1)
	var results []Recipe
	scripts := 0
	for {
		typeOfToken := z.Next()
		if typeOfToken == html.ErrorToken {
			if errors.Is(z.Err(), io.EOF) {
				break
			}
			return nil, errors.New("HTML-Quelle konnte nicht sicher gelesen werden")
		}
		if typeOfToken != html.StartTagToken {
			continue
		}
		name, hasAttributes := z.TagName()
		if string(name) != "script" {
			continue
		}
		validType, externalSource := false, false
		for hasAttributes {
			key, value, more := z.TagAttr()
			switch string(key) {
			case "type":
				validType = strings.EqualFold(strings.TrimSpace(string(value)), "application/ld+json")
			case "src":
				externalSource = true
			}
			hasAttributes = more
		}
		if !validType || externalSource {
			continue
		}
		scripts++
		if scripts > 20 {
			return nil, errors.New("HTML enthält zu viele Rezept-Datenskripte")
		}
		if z.Next() != html.TextToken {
			continue
		}
		content := append([]byte(nil), z.Text()...)
		if len(content) > MaxInputBytes {
			return nil, errors.New("Rezept-Datenskript ist zu groß")
		}
		parsed, err := decodeProfile(content)
		if err != nil {
			return nil, fmt.Errorf("Rezept-Datenskript %d: %w", scripts, err)
		}
		var candidates []map[string]any
		collectSchemaRecipes(parsed, 0, &candidates)
		for _, candidate := range candidates {
			recipe, parseErr := schemaRecipe(candidate)
			if parseErr != nil {
				return nil, fmt.Errorf("Rezept-Datenskript %d: %w", scripts, parseErr)
			}
			results = append(results, recipe)
		}
		if len(results) > MaxRecipes {
			return nil, errors.New("HTML enthält zu viele Rezepte")
		}
	}
	if len(results) == 0 {
		return nil, errors.New("kein unterstütztes Schema.org-Rezept im HTML gefunden")
	}
	return results, nil
}
