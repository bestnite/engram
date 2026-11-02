package notetype

import "strconv"

// Metadata field construction.
//
// The field named exactly MetadataFieldName carries per-note capability data such as the
// accepted alternative wordings of a cloze blank. It is a normal note field so every note write
// path stores it with the note, but it is not card content: it is plain text, collapsed in the
// editor, excluded from search and protected from deletion. The field is not referenced by any
// template, which is what keeps its JSON off an Anki card face.

// clozeBlanksControl is the answer control whose templates need the metadata field. It is a
// literal rather than an import of internal/answer: the note type package owns the config shape
// and must not depend on the package that implements the controls.
const clozeBlanksControl = "cloze_blanks"

// newMetadataField builds the metadata field with the id the caller chose. The flags are the
// ones the field needs on every config that carries it.
func newMetadataField(id string) Field {
	return Field{
		ID:                id,
		Name:              MetadataFieldName,
		Font:              "Arial",
		FontSize:          20,
		PlainText:         true,
		Collapsed:         true,
		ExcludeFromSearch: true,
		PreventDeletion:   true,
	}
}

// EnsureMetadataField makes the shared metadata field part of a config whose template grades
// cloze blanks.
//
// A config that declares no cloze_blanks answer control is left untouched: pure Anki note types
// and ordinary engram types never receive the field through this helper. Otherwise the helper
// appends the field when it is absent and normalises the four flags of an existing field in
// place, preserving its id, values and every other option.
//
// It is idempotent: the second call on the same config changes nothing and returns false. The
// return value reports whether the config changed, so a migration can skip rows that are already
// up to date.
func EnsureMetadataField(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	needsField := false
	for _, template := range cfg.Templates {
		if template.Answer != nil && template.Answer.Control == clozeBlanksControl {
			needsField = true
			break
		}
	}
	if !needsField {
		return false
	}

	changed := false
	found := false
	for i := range cfg.Fields {
		field := &cfg.Fields[i]
		if field.Name != MetadataFieldName {
			continue
		}
		found = true
		if !field.PlainText {
			field.PlainText = true
			changed = true
		}
		if !field.Collapsed {
			field.Collapsed = true
			changed = true
		}
		if !field.ExcludeFromSearch {
			field.ExcludeFromSearch = true
			changed = true
		}
		if !field.PreventDeletion {
			field.PreventDeletion = true
			changed = true
		}
	}
	if found {
		return changed
	}

	// The field id is derived from the reserved name and kept distinct from every id already
	// present. It is stable for the config it is written to; a later call finds the field by
	// name and never changes the id.
	id := MetadataFieldName
	for suffix := 1; fieldIDTaken(cfg.Fields, id); suffix++ {
		id = MetadataFieldName + "_" + strconv.Itoa(suffix)
	}
	cfg.Fields = append(cfg.Fields, newMetadataField(id))
	return true
}

// fieldIDTaken reports whether a field id is already used.
func fieldIDTaken(fields []Field, id string) bool {
	for i := range fields {
		if fields[i].ID == id {
			return true
		}
	}
	return false
}
