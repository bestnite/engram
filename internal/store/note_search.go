package store

import (
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// ParseFields decodes fields_json into its generic map form, used where a caller works with a value of any JSON type.
//
// Stored values are HTML strings in the current model; this decode stays generic because the one-time migration and the
// deck package format still read rows written under the old model, where a value could be an array, a boolean or a number.
func ParseFields(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("decode fields_json: %w", err)
	}
	return fields, nil
}

// FieldsOrEmpty decodes fields_json; corrupt data or null collapses into an empty object, so a response never carries null.
// The list, due-card and export read paths share it, so the same row yields the same shape at every exit.
func FieldsOrEmpty(raw string) map[string]any {
	fields, err := ParseFields(raw)
	if err != nil || fields == nil {
		return map[string]any{}
	}
	return fields
}

// noteSearchText builds the value of notes.search_text from a note's field values.
//
// Only the fields the note type marks searchable contribute; a field with exclude_from_search set is left out, because
// its content is machine syntax or a hint the learner should not find a card by. The text is the field's plain-text form:
// searching the stored HTML would make a search for a tag name hit every note that styles it, and characters HTML escapes
// (<, >, &, quotes) could never be found. Fields are visited in config order so the same note always produces the same text.
// Lowering happens here in Go: SQLite's LOWER only folds ASCII, so lowering in SQL would make the two databases disagree on
// non-ASCII letters.
func noteSearchText(cfg notetype.Config, values map[string]string) string {
	parts := make([]string, 0, len(cfg.Fields))
	for _, field := range cfg.Fields {
		if field.ExcludeFromSearch {
			continue
		}
		text := notetype.PlainText(values[field.ID])
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.ToLower(strings.Join(parts, "\n"))
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
