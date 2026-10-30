package recipeimport

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func portableFixture() Recipe {
	return Recipe{
		Title: "Gemüsesuppe", Servings: "2", PrepMinutes: 5, CookMinutes: 20,
		SourceURL: "https://recipes.example.com/soup?private=1#method",
		Ingredients: []Ingredient{{Name: "Wasser", Quantity: "1.5", Unit: "l"},
			{Name: "Salz", Quantity: "1", Unit: "tsp"}},
		Steps: []Step{{Instruction: "Kochen", TimerSeconds: 60}},
		Tags:  []string{"Winter", "Einfach"},
	}
}

func TestHouseholdRoundTripAndDigest(t *testing.T) {
	t.Parallel()
	fixture := portableFixture()
	encoded, err := ExportHousehold([]Recipe{fixture})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("private=1")) || bytes.Contains(encoded, []byte("#method")) ||
		bytes.Contains(encoded, []byte("household_id")) {
		t.Fatalf("portable output disclosed private source or identity: %s", encoded)
	}
	items, err := ParseHousehold(encoded)
	if err != nil || len(items) != 1 {
		t.Fatalf("round trip: %d items, %v", len(items), err)
	}
	if items[0].SourceURL != "https://recipes.example.com/soup" {
		t.Fatalf("source URL was not canonical: %q", items[0].SourceURL)
	}
	before, err := PortableDigest(fixture)
	if err != nil {
		t.Fatal(err)
	}
	after, err := PortableDigest(items[0])
	if err != nil || before != after {
		t.Fatalf("digest changed after export/import: %q %q %v", before, after, err)
	}
	fixture.Tags[0], fixture.Tags[1] = fixture.Tags[1], fixture.Tags[0]
	reordered, err := PortableDigest(fixture)
	if err != nil || before != reordered {
		t.Fatalf("tag ordering changed portable identity: %q %q %v", before, reordered, err)
	}
	fixture.Ingredients[0].Quantity = "2"
	changed, err := PortableDigest(fixture)
	if err != nil || before == changed {
		t.Fatalf("content change did not alter identity: %q %q %v", before, changed, err)
	}
}

func TestHouseholdRejectsUnlistedFieldsAndLimits(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"format":"household-recipe","version":1,"recipes":[{"title":"Soup","servings":"1","ingredients":[],"steps":[],"household_id":"private"}]}`,
		`{"format":"household-recipe","version":2,"recipes":[{"title":"Soup","servings":"1","ingredients":[],"steps":[]}]}`,
		`{"format":"household-recipe","version":1,"recipes":[{"title":"Soup","servings":"1","ingredients":[],"steps":[]}]} {}`,
	} {
		if _, err := ParseHousehold([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid Household file: %s", raw)
		}
	}
	file := HouseholdFile{Format: "household-recipe", Version: 1,
		Recipes: []Recipe{{Title: "Soup", Servings: "1", Ingredients: []Ingredient{{Name: "Water", Quantity: "1.0000001", Unit: "l"}}}}}
	encoded, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseHousehold(encoded); err == nil {
		t.Fatal("accepted silently rounded seven-place ingredient quantity")
	}
	file.Recipes[0].Ingredients = nil
	file.Recipes[0].Title = strings.Repeat("ä", 201)
	encoded, err = json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseHousehold(encoded); err == nil {
		t.Fatal("accepted overlong Unicode title")
	}
}

func TestLegacyContentDigestDoesNotBlockDatabaseRecipeEdits(t *testing.T) {
	t.Parallel()
	legacy := portableFixture()
	legacy.Servings = "100000000000.000000"
	legacy.PrepMinutes = 1_000_000_000
	legacy.Ingredients[0].Quantity = "100000000000.000000"
	legacy.Steps[0].TimerSeconds = 1_000_000_000
	if _, err := PortableDigest(legacy); err == nil {
		t.Fatal("strict interchange unexpectedly accepted oversized legacy values")
	}
	first, err := ContentDigest(legacy)
	if err != nil || len(first) != 64 {
		t.Fatalf("legacy database digest: %q, %v", first, err)
	}
	legacy.Tags[0], legacy.Tags[1] = legacy.Tags[1], legacy.Tags[0]
	second, err := ContentDigest(legacy)
	if err != nil || first != second {
		t.Fatalf("legacy digest depends on tag order: %q, %q, %v", first, second, err)
	}
	legacy.Title += " neu"
	third, err := ContentDigest(legacy)
	if err != nil || third == first {
		t.Fatalf("legacy content change not reflected: %q, %q, %v", first, third, err)
	}
}

func TestSchemaAndExternalProfiles(t *testing.T) {
	t.Parallel()
	schema := `{"@context":"https://schema.org","@type":"Recipe","name":"Brot","recipeYield":["2 servings"],"recipeIngredient":["200 g Mehl"],"recipeInstructions":[{"@type":"HowToStep","text":"Backen"}],"url":"https://example.com/brot?token=secret"}`
	items, err := ParseSchemaJSONLD([]byte(schema))
	if err != nil || len(items) != 1 || items[0].Title != "Brot" || items[0].Servings != "2" || items[0].SourceURL != "https://example.com/brot" {
		t.Fatalf("schema profile: %+v, %v", items, err)
	}
	html := `<html><head><script type="application/ld+json">{"@type":"WebSite","name":"ignored"}</script><script type="application/ld+json">` + schema + `</script><script src="https://attacker.example/x" type="application/ld+json">` + schema + `</script></head></html>`
	items, err = ParseSchemaHTML([]byte(html))
	if err != nil || len(items) != 1 || items[0].Title != "Brot" {
		t.Fatalf("HTML data extraction: %+v, %v", items, err)
	}
	mealie := `{"name":"Soup","recipeServings":2,"recipeIngredient":[{"display":"Carrot","quantity":2,"unit":{"name":"piece"}}],"recipeInstructions":[{"text":"Cook"}],"orgURL":"http://unsafe.example/soup"}`
	items, err = ParseMealie([]byte(mealie))
	if err != nil || len(items) != 1 || items[0].SourceURL != "" || len(items[0].Losses) == 0 {
		t.Fatalf("Mealie loss handling: %+v, %v", items, err)
	}
	paprika := `{"name":"Pie","servings":"4","ingredients":"1 kg Apfel\nZucker","directions":"Mischen\nBacken","categories":["Süß"]}`
	packed := gzipBytes(t, []byte(paprika))
	items, err = ParsePaprikaGZIP(packed)
	if err != nil || len(items) != 1 || len(items[0].Ingredients) != 2 || len(items[0].Steps) != 2 {
		t.Fatalf("Paprika profile: %+v, %v", items, err)
	}
	items, err = ParsePaprikaZIP(zipBytes(t, "one.paprikarecipe", packed))
	if err != nil || len(items) != 1 || items[0].Title != "Pie" {
		t.Fatalf("Paprika ZIP: %+v, %v", items, err)
	}
	tandoor := `{"name":"Salat","servings":2,"ingredients":[{"name":"Tomate","amount":2,"unit":"piece"}],"steps":[{"instruction":"Schneiden"}]}`
	items, err = ParseTandoorZIP(zipBytes(t, "Salat/recipe.json", []byte(tandoor)))
	if err != nil || len(items) != 1 || items[0].Title != "Salat" {
		t.Fatalf("Tandoor ZIP: %+v, %v", items, err)
	}
}

func TestArchiveTraversalAndNestedZIPRejected(t *testing.T) {
	t.Parallel()
	if _, err := ParseTandoorZIP(zipBytes(t, "../recipe.json", []byte(`{"name":"bad"}`))); err == nil {
		t.Fatal("accepted traversal")
	}
	inner := zipBytes(t, "nested.zip", []byte("x"))
	outer := zipBytes(t, "recipe.zip", inner)
	if _, err := ParseTandoorZIP(outer); err == nil {
		t.Fatal("accepted nested archive beyond one inner level")
	}
	if _, err := ParsePaprikaZIP(zipBytes(t, "nested.zip", inner)); err == nil {
		t.Fatal("accepted nested Paprika ZIP")
	}
	valid := gzipBytes(t, []byte(`{"name":"Simple","servings":"1","ingredients":"Water","directions":"Cook"}`))
	if _, err := ParsePaprikaGZIP(append(valid, []byte("trailing member")...)); err == nil {
		t.Fatal("accepted trailing data after the single Paprika gzip member")
	}
}

func zipBytes(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func gzipBytes(t *testing.T, content []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := io.ReadAll(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
