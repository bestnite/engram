package api

import (
	"bytes"
	"testing"

	"git.nite07.com/nite/engram/internal/apkg"
)

func fixturePackage(t *testing.T, media []byte, weights []float64, withNote bool) []byte {
	t.Helper()
	pkg := apkg.WritePackage{Collection: apkg.WriteCollection{
		CreatedAt:   1700000000,
		Decks:       []apkg.WriteDeck{{ID: 2, Name: "imported", ConfigID: 2}},
		DeckConfigs: []apkg.WriteDeckConfig{{ID: 2, Name: "Imported", DesiredRetention: 0.9, LearningSteps: []float64{1}, RelearningSteps: []float64{10}, MaximumIntervalDays: 100, FSRSWeights: weights}},
		NoteTypes:   []apkg.WriteNoteType{{ID: 2, Name: "Basic", Fields: []apkg.Field{{Name: "Front"}, {Ord: 1, Name: "Back"}}, Templates: []apkg.Template{{Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{FrontSide}}<hr>{{Back}}"}}}},
	}}
	if withNote {
		back := "a"
		if media != nil {
			back = `<img src="fixture.png">`
			pkg.Media = []apkg.WriteMedia{{Name: "fixture.png", Bytes: media}}
		}
		pkg.Collection.Notes = []apkg.WriteNote{{ID: 2, GUID: "fixture-guid", NoteTypeID: 2, Fields: []string{"q", back}}}
		pkg.Collection.Cards = []apkg.WriteCard{{ID: 2, NoteID: 2, DeckID: 2}}
	}
	var buf bytes.Buffer
	if err := apkg.WriteArchive(&buf, &pkg); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
