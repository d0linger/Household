// Package i18n contains reviewed application copy. It never translates records,
// identifiers, uploaded content, or other user-authored values.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed *.json
var files embed.FS

var english = load()

// Normalize accepts only the two supported application languages. The product
// default remains German; arbitrary cookie/header values never become paths.
func Normalize(locale string) string {
	switch strings.ToLower(strings.TrimSpace(locale)) {
	case "en", "en-gb", "en-us":
		return "en"
	default:
		return "de"
	}
}

// LanguageTag is the locale used for presentation, independent of the
// household's timezone, currency, civil dates, and canonical API values.
func LanguageTag(locale string) string {
	if Normalize(locale) == "en" {
		return "en-GB"
	}
	return "de-AT"
}

// Text looks up authored source copy and then interpolates its arguments.
// Parameters are never looked up in the catalog. html/template retains normal
// contextual escaping for the returned string, including substituted content.
func Text(locale, source string, args ...any) string {
	message := source
	if Normalize(locale) == "en" {
		if translated, ok := english[source]; ok {
			message = translated
		}
	}
	if len(args) == 0 {
		return message
	}
	return fmt.Sprintf(message, args...)
}

// Lookup distinguishes cataloged application copy from arbitrary source data.
func Lookup(locale, source string) (string, bool) {
	translated, ok := english[source]
	if !ok {
		return source, false
	}
	if Normalize(locale) != "en" {
		return source, true
	}
	return translated, true
}

// English returns an independent catalog copy for coverage and generated-asset
// verification. Callers cannot mutate translations used by concurrent requests.
func English() map[string]string {
	result := make(map[string]string, len(english))
	for source, translation := range english {
		result[source] = translation
	}
	return result
}

func load() map[string]string {
	names, err := fs.Glob(files, "*.json")
	if err != nil {
		panic("i18n: cannot enumerate embedded catalogs")
	}
	sort.Strings(names)
	result := map[string]string{}
	for _, name := range names {
		data, err := files.ReadFile(name)
		if err != nil {
			panic("i18n: cannot read embedded catalog " + name)
		}
		var catalog map[string]string
		if err := json.Unmarshal(data, &catalog); err != nil {
			panic("i18n: invalid catalog " + name)
		}
		for source, translation := range catalog {
			if strings.TrimSpace(source) == "" || strings.TrimSpace(translation) == "" {
				panic("i18n: empty message in " + name)
			}
			if existing, ok := result[source]; ok && existing != translation {
				panic("i18n: conflicting translation for " + source)
			}
			result[source] = translation
		}
	}
	return result
}
