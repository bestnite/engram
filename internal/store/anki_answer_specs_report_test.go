package store

import (
	"bytes"
	"context"
	"testing"

	"git.nite07.com/nite/engram/internal/anki"
)

// TestAnkiImportReportsMissingAnswerSpecs pins the report field: a package whose model JSON
// carries the engram key imports with answer_specs_missing at 0, and a package whose note type
// has no key imports with the count of note types that supplied none.
func TestAnkiImportReportsMissingAnswerSpecs(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		for _, tc := range []struct {
			name  string
			specs map[string]anki.AnswerSpec
			want  int
		}{
			{
				name:  "carried",
				specs: map[string]anki.AnswerSpec{"Card 1": {Control: "text", Bindings: map[string]string{"answer": "Back"}}},
				want:  0,
			},
			{name: "absent", want: 1},
		} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				user := seedUsers(t, db, "answer_specs_"+driver+"_"+tc.name)[0]
				pkg := anki.WritePackage{Collection: anki.WriteCollection{
					CreatedAt: 1600000000,
					NoteTypes: []anki.WriteNoteType{{
						ID:          10,
						Name:        "Basic",
						Fields:      []anki.Field{{Name: "Front"}, {Name: "Back"}},
						Templates:   []anki.Template{{Name: "Card 1", QFormat: "{{Front}}{{type:Back}}", AFormat: "{{Back}}"}},
						AnswerSpecs: tc.specs,
					}},
					Decks: []anki.WriteDeck{{ID: 20, Name: "Imported", ConfigID: 30}},
					DeckConfigs: []anki.WriteDeckConfig{{
						ID: 30, Name: "Default", DesiredRetention: .9,
						LearningSteps: []float64{1, 10}, RelearningSteps: []float64{10}, MaximumIntervalDays: 36500,
					}},
					Notes: []anki.WriteNote{{ID: 40, GUID: "guid", NoteTypeID: 10, Fields: []string{"front", "back"}}},
					Cards: []anki.WriteCard{{ID: 50, NoteID: 40, DeckID: 20}},
				}}
				var raw bytes.Buffer
				if err := anki.WriteArchive(&raw, &pkg); err != nil {
					t.Fatal(err)
				}
				report, err := NewDeckStore(db).ImportPackage(context.Background(), user, &raw, PackageImportOptions{Target: "new_deck"})
				if err != nil {
					t.Fatal(err)
				}
				if report.AnswerSpecsMissing != tc.want {
					t.Fatalf("answer_specs_missing = %d, want %d (report %+v)", report.AnswerSpecsMissing, tc.want, report)
				}
			})
		}
	}
}
