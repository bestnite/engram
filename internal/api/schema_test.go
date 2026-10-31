package api

import (
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TestNoteImportSchemaMatchesTheContract runs machine validation directly against
// schema/note-import.schema.json. The schema is the machine-readable contract external tools
// validate against, so it must accept the payload the server accepts: note_type plus a map from
// field name to HTML string.
func TestNoteImportSchemaMatchesTheContract(t *testing.T) {
	sch, err := jsonschema.NewCompiler().Compile("../../schema/note-import.schema.json")
	if err != nil {
		t.Fatalf("compile note-import schema: %v", err)
	}
	validate := func(body string) error {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(body))
		if err != nil {
			t.Fatalf("decode payload %s: %v", body, err)
		}
		return sch.Validate(doc)
	}

	valid := []string{
		// The note names a note type public id and field values as HTML strings.
		`{"notes":[{"note_type":"0198c0de-0000-7000-8000-000000000001","fields":{"Front":"<p>q</p>","Back":"a"}}]}`,
		// note_id addresses an existing note and is legal on its own.
		`{"notes":[{"note_id":"0198c0de-0000-7000-8000-000000000001","note_type":"0198c0de-0000-7000-8000-000000000001","fields":{"front":"q","back":"a"}}]}`,
		// external_ref on its own is still legal.
		`{"notes":[{"external_ref":"e:1","note_type":"0198c0de-0000-7000-8000-000000000001","fields":{"front":"q","back":"a"}}]}`,
	}
	for _, body := range valid {
		if err := validate(body); err != nil {
			t.Errorf("valid payload was rejected: %v (%s)", err, body)
		}
	}

	invalid := []string{
		// No note_type: the note names no type.
		`{"notes":[{"fields":{"front":"q"}}]}`,
		// The transitional card kind is gone from the contract.
		`{"notes":[{"kind":"basic","fields":{"front":"q","back":"a"}}]}`,
		// A field value is an HTML string; the old typed shapes are no longer part of the contract.
		`{"notes":[{"note_type":"0198c0de-0000-7000-8000-000000000001","fields":{"options":["a","b"]}}]}`,
		`{"notes":[{"note_type":"0198c0de-0000-7000-8000-000000000001","fields":{"answer":true}}]}`,
		// note_id and external_ref are mutually exclusive, expressed with allOf + not + required.
		`{"notes":[{"note_id":"0198c0de-0000-7000-8000-000000000001","external_ref":"e:1","note_type":"0198c0de-0000-7000-8000-000000000001","fields":{"front":"q","back":"a"}}]}`,
		// note_id is a non-empty string: numbers (the old auto-increment primary key form) are no longer accepted.
		`{"notes":[{"note_id":7,"note_type":"0198c0de-0000-7000-8000-000000000001","fields":{"front":"q","back":"a"}}]}`,
	}
	for _, body := range invalid {
		if err := validate(body); err == nil {
			t.Errorf("invalid payload was accepted, want a schema violation: %s", body)
		}
	}
}
