package store

import (
	"sort"
	"strings"

	"gorm.io/gorm"
)

// noteSearchText builds the value of notes.search_text from a note's fields.
//
// Search matches field text, not the JSON encoding of the fields: on the encoding a search for a field key hits every note,
// and characters JSON escapes (<, >, &, quotes, newlines) can never be found. Only string values are collected, recursively
// through maps and arrays; keys, numbers and booleans are left out, because they are field names, choice indexes, tolerances
// or true/false answers rather than text a user would search for. Map keys are visited in sorted order so the same fields
// always produce the same text. Lowering happens here in Go: SQLite's LOWER only folds ASCII, so lowering in SQL would make
// the two databases disagree on non-ASCII letters.
func noteSearchText(fields map[string]any) string {
	var parts []string
	collectSearchStrings(fields, &parts)
	return strings.ToLower(strings.Join(parts, "\n"))
}

// collectSearchStrings appends every non-empty string value found under v to parts.
func collectSearchStrings(v any, parts *[]string) {
	switch x := v.(type) {
	case string:
		if x != "" {
			*parts = append(*parts, x)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			collectSearchStrings(x[k], parts)
		}
	case []any:
		for _, child := range x {
			collectSearchStrings(child, parts)
		}
	}
}

// whereNoteSearch restricts q to notes whose search_text contains every whitespace-separated term of query.
// Terms need not be adjacent or in order. An empty or blank query adds no condition.
// Wildcards are escaped, so "%" and "_" in a term match only themselves.
func whereNoteSearch(q *gorm.DB, query string) *gorm.DB {
	for _, term := range strings.Fields(strings.ToLower(query)) {
		q = q.Where("notes.search_text LIKE ? ESCAPE '\\'", "%"+escapeLike(term)+"%")
	}
	return q
}
