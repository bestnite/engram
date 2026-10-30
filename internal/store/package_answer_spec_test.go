package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// packageAnswerSpecEntries builds the entries of a one-type package whose template carries the given answer spec.
// The rest of the package is valid, so the import reaches the note type validation and fails there.
func packageAnswerSpecEntries(t *testing.T, spec map[string]any) [][2]string {
	t.Helper()
	noteTypes, err := json.Marshal([]map[string]any{{
		"ref":  "nt-1",
		"name": "Basic",
		"config": map[string]any{
			"kind": "normal",
			"fields": []map[string]any{
				{"id": "front", "name": "front"},
				{"id": "back", "name": "back"},
			},
			"templates": []map[string]any{{
				"id":     "t-forward",
				"name":   "Card 1",
				"qfmt":   "{{front}}{{type:back}}",
				"afmt":   "{{front}}<hr>{{back}}",
				"answer": spec,
			}},
		},
	}})
	if err != nil {
		t.Fatalf("encode note_types.json: %v", err)
	}
	return [][2]string{
		{"manifest.json", `{"format_version":2,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"d"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":1,"cards":0}}`},
		{"note_types.json", string(noteTypes)},
		{"notes.json", `[{"note_type":"nt-1","fields":{"front":"q","back":"a"}}]`},
		{"cards.json", `[]`},
		{"preset.json", testPresetJSON},
	}
}

// TestImportPackageRejectsInvalidAnswerSpec proves the package import rejects the same three answer-spec violations
// SetConfig rejects: an unknown control, a missing required role and a binding to an undeclared field id. Rejection
// happens before the transaction, so the package writes no row and names the offending note_types entry.
func TestImportPackageRejectsInvalidAnswerSpec(t *testing.T) {
	tests := []struct {
		name string
		spec map[string]any
		want []string
	}{
		{
			name: "unknown control",
			spec: map[string]any{"control": "does_not_exist"},
			want: []string{"note_types[0]", `Card 1`, `"does_not_exist"`},
		},
		{
			name: "missing required role",
			spec: map[string]any{"control": "numeric"},
			want: []string{"note_types[0]", `Card 1`, `role "value"`},
		},
		{
			name: "role bound to an undeclared field id",
			spec: map[string]any{"control": "numeric", "bindings": map[string]string{"value": "f-missing"}},
			want: []string{"note_types[0]", `Card 1`, `role "value"`, `"f-missing"`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for driver, db := range packageDatabases(t) {
				t.Run(driver, func(t *testing.T) {
					raw := zipWithEntries(t, packageAnswerSpecEntries(t, test.spec))
					importer := seedUsers(t, db, "pkg_answer_spec")[0]
					root := t.TempDir()
					_, err := NewDeckStore(db).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{
						Target:    PackageTargetNewDeck,
						MediaRoot: root,
					})
					var pe *PackageError
					if !errors.As(err, &pe) || pe.Code != CodePackageBadFormat {
						t.Fatalf("error = %v, want PackageError code %s", err, CodePackageBadFormat)
					}
					for _, want := range test.want {
						if !entryContains(pe.Entries, want) {
							t.Errorf("entries = %v, want one containing %q", pe.Entries, want)
						}
					}
					assertNoImportSideEffects(t, db, root)
					var noteTypes int64
					if err := db.Model(&NoteType{}).Count(&noteTypes).Error; err != nil {
						t.Fatalf("count note types: %v", err)
					}
					if noteTypes != 0 {
						t.Errorf("rejected import wrote %d note type row(s), want 0", noteTypes)
					}
				})
			}
		})
	}
}
