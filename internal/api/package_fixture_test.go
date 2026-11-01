package api

import (
	"bytes"
	"testing"

	"git.nite07.com/nite/engram/internal/anki"
)

func fixturePackage(t *testing.T, media []byte, weights []float64, withNote bool) []byte {
	t.Helper()
	pkg := anki.WritePackage{Collection: anki.WriteCollection{
		CreatedAt:   1700000000,
		Decks:       []anki.WriteDeck{{ID: 2, Name: "imported", ConfigID: 2}},
		DeckConfigs: []anki.WriteDeckConfig{{ID: 2, Name: "Imported", DesiredRetention: 0.9, LearningSteps: []float64{1}, RelearningSteps: []float64{10}, MaximumIntervalDays: 100, FSRSWeights: weights}},
		NoteTypes:   []anki.WriteNoteType{{ID: 2, Name: "Basic", Fields: []anki.Field{{Name: "Front"}, {Ord: 1, Name: "Back"}}, Templates: []anki.Template{{Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{FrontSide}}<hr>{{Back}}"}}}},
	}}
	if withNote {
		back := "a"
		if media != nil {
			back = `<img src="fixture.png">`
			pkg.Media = []anki.WriteMedia{{Name: "fixture.png", Bytes: media}}
		}
		pkg.Collection.Notes = []anki.WriteNote{{ID: 2, GUID: "fixture-guid", NoteTypeID: 2, Fields: []string{"q", back}}}
		pkg.Collection.Cards = []anki.WriteCard{{ID: 2, NoteID: 2, DeckID: 2}}
	}
	var buf bytes.Buffer
	if err := anki.WriteArchive(&buf, &pkg); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
