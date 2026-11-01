package api

import (
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TestShippedSchemaExamplesValidate pins both examples shipped for external tools to the schemas
// that describe them: the examples are the payloads `schema/README.md` points an external tool at,
// so a schema change that invalidates one of them must fail the build instead of waiting for that
// tool to discover the drift.
func TestShippedSchemaExamplesValidate(t *testing.T) {
	cases := []struct {
		name    string
		schema  string
		example string
	}{
		{
			name:    "note-import",
			schema:  "../../schema/note-import.schema.json",
			example: "../../schema/examples/note-import.example.json",
		},
		{
			name:    "note-bulk",
			schema:  "../../schema/note-bulk.schema.json",
			example: "../../schema/examples/note-bulk.example.json",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sch, err := jsonschema.NewCompiler().Compile(tc.schema)
			if err != nil {
				t.Fatalf("compile %s: %v", tc.schema, err)
			}
			raw, err := os.ReadFile(tc.example)
			if err != nil {
				t.Fatalf("read %s: %v", tc.example, err)
			}
			doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
			if err != nil {
				t.Fatalf("decode %s: %v", tc.example, err)
			}
			if err := sch.Validate(doc); err != nil {
				t.Errorf("%s does not validate against %s: %v", tc.example, tc.schema, err)
			}
		})
	}
}
