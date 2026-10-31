package schedule

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/anki"
	"git.nite07.com/nite/engram/internal/store"
)

func TestAnkiImportedMemoryPreview(t *testing.T) {
	pkg := anki.WritePackage{Collection: anki.WriteCollection{
		CreatedAt:   1600000000,
		NoteTypes:   []anki.WriteNoteType{{ID: 10, Name: "Basic", Fields: []anki.Field{{Name: "Front"}}, Templates: []anki.Template{{Name: "Card", QFormat: "{{Front}}", AFormat: "{{Front}}"}}}},
		Decks:       []anki.WriteDeck{{ID: 20, Name: "Memory", ConfigID: 30}},
		DeckConfigs: []anki.WriteDeckConfig{{ID: 30, Name: "Memory", DesiredRetention: .9, LearningSteps: []float64{1, 10}, RelearningSteps: []float64{10}, MaximumIntervalDays: 36500}},
	}}
	for i, tc := range []struct {
		kind, factor, interval int
		history                bool
	}{{2, 2500, 7, true}, {1, 0, -600, true}, {2, 2500, 7, false}, {1, 0, -600, false}} {
		nid, cid := int64(40+i), int64(50+i)
		pkg.Collection.Notes = append(pkg.Collection.Notes, anki.WriteNote{ID: nid, GUID: fmt.Sprint(nid), NoteTypeID: 10, Fields: []string{"question"}})
		pkg.Collection.Cards = append(pkg.Collection.Cards, anki.WriteCard{ID: cid, NoteID: nid, DeckID: 20, Type: tc.kind, Queue: tc.kind, Ivl: tc.interval, Factor: tc.factor})
		if tc.history {
			pkg.Collection.Reviews = append(pkg.Collection.Reviews, anki.WriteReview{ID: 1600000000000 + int64(i), CardID: cid, Ease: 3, Type: 0}, anki.WriteReview{ID: 1600086400000 + int64(i), CardID: cid, Ease: 4, Type: 1})
		}
	}
	var raw bytes.Buffer
	if err := anki.WriteArchive(&raw, &pkg); err != nil {
		t.Fatal(err)
	}
	db := newTestDB(t)
	user := store.User{Username: "memory_preview", Email: "memory_preview@example.com", DisplayName: "memory_preview", Role: store.RoleUser, Status: store.StatusActive, Locale: "en", Timezone: "UTC"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	report, err := store.NewDeckStore(db).ImportPackage(t.Context(), user.ID, bytes.NewReader(raw.Bytes()), store.PackageImportOptions{Target: store.PackageTargetNewDeck, IncludeProgress: true, MediaRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var deck store.Deck
	if err := db.First(&deck, report.DeckID).Error; err != nil {
		t.Fatal(err)
	}
	var preset store.Preset
	if err := db.First(&preset, deck.PresetID).Error; err != nil {
		t.Fatal(err)
	}
	scheduler, err := NewScheduler(&preset)
	if err != nil {
		t.Fatal(err)
	}
	var states []store.CardState
	if err := db.Where("user_id = ?", user.ID).Find(&states).Error; err != nil {
		t.Fatal(err)
	}
	if len(states) != 3 || report.StatesSkipped != 1 {
		t.Fatalf("states=%+v report=%+v", states, report)
	}
	for i := range states {
		if _, err := scheduler.Preview(&states[i], time.Unix(1700000000, 0)); err != nil {
			t.Fatalf("state %+v: %v", states[i], err)
		}
	}
}
