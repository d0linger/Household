package i18n

import (
	"regexp"
	"sort"
	"strings"
)

type messagePattern struct {
	match *regexp.Regexp
	parts []string
	verbs []string
}

// Only trusted application error/notice producers use Message. User records,
// notes and external service payloads must never pass through this function.
// Existing producers format errors before they reach the HTTP boundary. Keep
// captured arguments byte-for-byte intact when translating that authored copy.
var messagePatterns = compileMessagePatterns()

func Message(locale, source string) string {
	return authoredMessage(locale, source, 0)
}

func authoredMessage(locale, source string, depth int) string {
	if Normalize(locale) != "en" {
		return source
	}
	if translated, ok := Lookup(locale, source); ok {
		return translated
	}
	if len(source) > 16384 || depth >= 4 {
		return source
	}
	for _, pattern := range messagePatterns {
		matches := pattern.match.FindStringSubmatch(source)
		if matches == nil {
			continue
		}
		var translated strings.Builder
		translated.WriteString(pattern.parts[0])
		for index, argument := range matches[1:] {
			// %w denotes an actual wrapped application error, unlike values
			// interpolated with %s/%q/%v. Bound nesting and retain all values.
			if pattern.verbs[index] == "%w" {
				argument = authoredMessage(locale, argument, depth+1)
			}
			translated.WriteString(argument)
			translated.WriteString(pattern.parts[index+1])
		}
		return translated.String()
	}
	return source
}

func compileMessagePatterns() []messagePattern {
	placeholder := regexp.MustCompile(`%[sdvqw]`)
	var sources []string
	for source := range english {
		if placeholder.MatchString(source) {
			sources = append(sources, source)
		}
	}
	// Prefer specific messages over fragments such as "%s". Deterministic
	// ordering also prevents a random map iteration from changing a response.
	sort.Slice(sources, func(a, b int) bool {
		if len(sources[a]) != len(sources[b]) {
			return len(sources[a]) > len(sources[b])
		}
		return sources[a] < sources[b]
	})
	patterns := make([]messagePattern, 0, len(sources))
	for _, source := range sources {
		parts := placeholder.Split(source, -1)
		translated := placeholder.Split(english[source], -1)
		if len(parts) != len(translated) || len(strings.Join(parts, "")) < 8 {
			continue
		}
		var expression strings.Builder
		expression.WriteString(`(?s)^`)
		verbs := placeholder.FindAllString(source, -1)
		for index, part := range parts {
			if index > 0 {
				if verbs[index-1] == "%q" {
					expression.WriteString(`("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')`)
				} else {
					expression.WriteString(`(.*?)`)
				}
			}
			expression.WriteString(regexp.QuoteMeta(strings.ReplaceAll(part, "%%", "%")))
		}
		expression.WriteString(`$`)
		patterns = append(patterns, messagePattern{regexp.MustCompile(expression.String()), translated, verbs})
	}
	return patterns
}
