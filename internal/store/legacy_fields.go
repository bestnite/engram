package store

import (
	"strings"

	"git.nite07.com/nite/engram/internal/answer"
)

// This file translates stored field values back into the typed shapes of the legacy note payloads.
//
// A field value is an HTML string in the current model, but the old REST/MCP note payload describes the same content as
// a string array (options, items, accepted wordings), a boolean (ignore_case, ordered, true/false answers), a number
// (tolerances, numeric value) or an option index. The note response keeps writing those shapes until the rich-text
// editor replaces the card-kind editor, and a client reads them back. The table below is the one place that mapping lives.
//
// A field whose stored value is empty is left out: the old payload omitted a field the note did not fill, and an empty
// string would not satisfy the schema's type for a boolean or a number.

// fieldShape is how a field's stored HTML is presented in the legacy note payload.
type fieldShape int

const (
	shapeText fieldShape = iota
	shapeLines
	shapeBool
	shapeNumber
	shapeIndex
	shapeIndexes
)

// shapeFor reports the legacy payload shape of one field of an engram stock kind.
func shapeFor(kind, fieldID string) fieldShape {
	switch kind {
	case "typed":
		switch fieldID {
		case "accept":
			return shapeLines
		case "ignore_case", "ignore_whitespace":
			return shapeBool
		}
	case "numeric":
		switch fieldID {
		case "value", "tolerance_absolute", "tolerance_relative":
			return shapeNumber
		}
	case "choice_single":
		switch fieldID {
		case "options":
			return shapeLines
		case "answer":
			return shapeIndex
		}
	case "choice_multi":
		switch fieldID {
		case "options":
			return shapeLines
		case "answers":
			return shapeIndexes
		}
	case "true_false":
		if fieldID == "answer" {
			return shapeBool
		}
	case "list":
		switch fieldID {
		case "items":
			return shapeLines
		case "ordered":
			return shapeBool
		}
	}
	return shapeText
}

// LegacyNoteFields presents stored field values in the legacy note payload shapes. An unknown kind keeps every value as
// the stored string.
func LegacyNoteFields(kind string, values map[string]string) map[string]any {
	out := make(map[string]any, len(values))
	for fieldID, html := range values {
		shape := shapeFor(kind, fieldID)
		if shape == shapeText {
			if html != "" {
				out[fieldID] = html
			}
			continue
		}
		if strings.TrimSpace(html) == "" {
			continue
		}
		switch shape {
		case shapeLines:
			if lines := answer.ParseLines(html); len(lines) > 0 {
				out[fieldID] = lines
			}
		case shapeBool:
			if value, err := answer.ParseBool(html); err == nil {
				out[fieldID] = value
			}
		case shapeNumber:
			if value, err := answer.ParseNumber(html); err == nil {
				out[fieldID] = value
			}
		case shapeIndex:
			if value, err := answer.ParseNumber(html); err == nil {
				out[fieldID] = int(value)
			}
		case shapeIndexes:
			if indices := legacyOptionIndices(html); len(indices) > 0 {
				out[fieldID] = indices
			}
		}
	}
	return out
}

// legacyOptionIndices reads one non-negative option index per line.
func legacyOptionIndices(html string) []int {
	lines := answer.ParseLines(html)
	out := make([]int, 0, len(lines))
	for _, line := range lines {
		number, err := answer.ParseNumber(line)
		if err != nil || number < 0 || number != float64(int(number)) {
			continue
		}
		out = append(out, int(number))
	}
	return out
}
