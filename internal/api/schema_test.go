package api

import (
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TestNoteImportSchemaAcceptsNoteIDAndRejectsUnknownKind runs machine validation directly against schema/note-import.schema.json:
// a body carrying note_id passes, while an unknown kind and a body giving both note_id and
// external_ref are rejected. The schema is the machine-readable contract external tools validate against and must stay in sync with the implementation.
func TestNoteImportSchemaAcceptsNoteIDAndRejectsUnknownKind(t *testing.T) {
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

	// The new note_id field (external id, a string) passes validation.
	if err := validate(`{"notes":[{"note_id":"0198c0de-0000-7000-8000-000000000001","kind":"basic","fields":{"front":"q","back":"a"}}]}`); err != nil {
		t.Errorf("payload carrying note_id was rejected: %v", err)
	}
	// external_ref on its own is still legal.
	if err := validate(`{"notes":[{"external_ref":"e:1","kind":"basic","fields":{"front":"q","back":"a"}}]}`); err != nil {
		t.Errorf("payload carrying only external_ref was rejected: %v", err)
	}
	// An unknown kind matches no oneOf branch and must be rejected.
	if err := validate(`{"notes":[{"kind":"made_up","fields":{"x":"y"}}]}`); err == nil {
		t.Error("unknown kind was accepted, want a schema violation")
	}
	// note_id and external_ref are mutually exclusive, expressed with allOf + not + required.
	if err := validate(`{"notes":[{"note_id":"0198c0de-0000-7000-8000-000000000001","external_ref":"e:1","kind":"basic","fields":{"front":"q","back":"a"}}]}`); err == nil {
		t.Error("payload carrying both note_id and external_ref was accepted, want a schema violation")
	}
	// note_id is a non-empty string: numbers (the old auto-increment primary key form) are no longer accepted.
	if err := validate(`{"notes":[{"note_id":7,"kind":"basic","fields":{"front":"q","back":"a"}}]}`); err == nil {
		t.Error("payload carrying a numeric note_id was accepted, want a schema violation")
	}
}
