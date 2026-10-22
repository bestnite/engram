package api

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// bulkActionsFromSchema extracts the value set of the action enum in the schema.
//
// It parses the JSON of the schema file itself directly (not relying on the validation library's internals), and is the nail that keeps
// "schema and implementation must not fork" in place: when a bulk action is added, if the schema's enum does not follow, or a Go-side constant is renamed,
// this test goes red - instead of waiting until an external tool validates against it and finds the mismatch.
func bulkActionsFromSchema(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(doc.Properties.Action.Enum) == 0 {
		t.Fatalf("%s: properties.action.enum is missing or empty", path)
	}
	out := append([]string{}, doc.Properties.Action.Enum...)
	sort.Strings(out)
	return out
}

// TestNoteBulkSchemaMatchesTheGoActions asserts the schema's action enum matches the Go-side constants exactly,
// and verifies that the example request body in the repo really passes its own schema (the schema is a machine-readable
// contract for external tools, so example and contract must stay in sync).
func TestNoteBulkSchemaMatchesTheGoActions(t *testing.T) {
	const path = "../../schema/note-bulk.schema.json"

	want := []string{bulkActionAddTags, bulkActionDelete, bulkActionRemoveTags, bulkActionSetTags}
	sort.Strings(want)
	if got := bulkActionsFromSchema(t, path); !equalStrings(got, want) {
		t.Fatalf("schema action enum = %v, want %v (the schema and the service must not drift)", got, want)
	}

	sch, err := jsonschema.NewCompiler().Compile(path)
	if err != nil {
		t.Fatalf("compile %s: %v", path, err)
	}
	raw, err := os.ReadFile("../../schema/examples/note-bulk.example.json")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("decode example: %v", err)
	}
	if err := sch.Validate(doc); err != nil {
		t.Errorf("the shipped example does not validate against the shipped schema: %v", err)
	}
}

// TestNoteBulkSchemaRejectsInvalidShapes covers the four kinds of request-level rejection the schema can express:
// unknown action, note_ids out of range, a tag action without tags, delete with tags.
// The server likewise returns 400 invalid_request for the same bodies (see bulk_notes_test.go).
func TestNoteBulkSchemaRejectsInvalidShapes(t *testing.T) {
	sch, err := jsonschema.NewCompiler().Compile("../../schema/note-bulk.schema.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	validate := func(body string) error {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(body))
		if err != nil {
			t.Fatalf("decode payload %s: %v", body, err)
		}
		return sch.Validate(doc)
	}

	ids501 := make([]string, 0, 501)
	for i := 1; i <= 501; i++ {
		// note_ids is a string array; only 501 valid strings exercise the count cap.
		ids501 = append(ids501, `"`+itoa(i)+`"`)
	}
	tags21 := make([]string, 0, 21)
	for i := 1; i <= 21; i++ {
		tags21 = append(tags21, `"t`+itoa(i)+`"`)
	}

	valid := []string{
		`{"action":"delete","note_ids":["0198c0de-0000-7000-8000-000000000001","0198c0de-0000-7000-8000-000000000002","0198c0de-0000-7000-8000-000000000003"]}`,
		`{"action":"add_tags","note_ids":["0198c0de-0000-7000-8000-000000000001"],"tags":["a"]}`,
		`{"action":"remove_tags","note_ids":["0198c0de-0000-7000-8000-000000000001","0198c0de-0000-7000-8000-000000000002"],"tags":["a","b"]}`,
		`{"action":"set_tags","note_ids":["0198c0de-0000-7000-8000-000000000001"],"tags":["a"],"dry_run":true}`,
	}
	for _, body := range valid {
		if err := validate(body); err != nil {
			t.Errorf("valid body %s was rejected: %v", body, err)
		}
	}

	invalid := []string{
		`{"action":"archive","note_ids":["0198c0de-0000-7000-8000-000000000001"]}`,
		`{"action":"delete","note_ids":[]}`,
		`{"action":"delete","note_ids":[` + strings.Join(ids501, ",") + `]}`,
		`{"action":"add_tags","note_ids":["0198c0de-0000-7000-8000-000000000001"]}`,
		`{"action":"add_tags","note_ids":["0198c0de-0000-7000-8000-000000000001"],"tags":[]}`,
		`{"action":"add_tags","note_ids":["0198c0de-0000-7000-8000-000000000001"],"tags":[` + strings.Join(tags21, ",") + `]}`,
		`{"action":"delete","note_ids":["0198c0de-0000-7000-8000-000000000001"],"tags":["x"]}`,
		`{"note_ids":["0198c0de-0000-7000-8000-000000000001"]}`,
		// Numbers (the old auto-increment primary key form) are no longer valid note_ids elements.
		`{"action":"delete","note_ids":[1]}`,
	}
	for _, body := range invalid {
		if err := validate(body); err == nil {
			t.Errorf("invalid body was accepted, want a schema violation: %s", body)
		}
	}
}

// itoa is the minimal number-to-string helper for tests, avoiding an fmt import just for one conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// equalStrings reports whether two sorted string slices are equal.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
